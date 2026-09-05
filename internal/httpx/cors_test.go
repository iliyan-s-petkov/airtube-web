package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/httpx"
	"airbg.org/internal/origin"
	"airbg.org/internal/ratelimit"
)

const siteOrigin = "https://airbg.org"

// okHandler mimics the real API handler in the one respect that breaks
// middleware: internal/api/router.go SETS Vary rather than adding to it, so
// anything written before the handler runs is erased. A stub that used Add
// would pass a middleware that is defeated in production.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.WriteHeader(http.StatusOK)
	})
}

// The other real shape: locate.go varies on the geolocation headers, and no
// handler calls WriteHeader before writing a body.
func implicitStatusHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Vary", "CF-IPLatitude, CF-IPLongitude")
		_, _ = w.Write([]byte("{}"))
	})
}

func allowlist(t *testing.T, loopback bool) *origin.Allowlist {
	t.Helper()
	a, err := origin.NewAllowlist([]string{siteOrigin}, loopback)
	if err != nil {
		t.Fatalf("NewAllowlist error = %v, want nil", err)
	}
	return a
}

func TestCORSEchoesAllowedOriginsOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		loopback bool
		o        string
		want     bool
	}{
		{"the site's own origin", false, siteOrigin, true},
		{"a loopback origin, switch off", false, "http://127.0.0.1:60659", false},
		{"a loopback origin, switch on", true, "http://127.0.0.1:60659", true},
		{"a loopback lookalike, switch on", true, "http://127.0.0.1.evil.test", false},
		{"a stranger, switch on", true, "https://evil.test", false},
		{"no Origin header", true, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := httpx.CORS(okHandler(), allowlist(t, tc.loopback))
			r := httptest.NewRequest(http.MethodGet, "/api/v1/hexes", nil)
			if tc.o != "" {
				r.Header.Set("Origin", tc.o)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			got, ok := w.Header()["Access-Control-Allow-Origin"]
			if !tc.want {
				if ok {
					t.Errorf("Origin %q: Access-Control-Allow-Origin = %q, want the header absent", tc.o, got)
				}
				return
			}
			// Echoed byte for byte, or the browser rejects it as surely as an
			// absent header.
			if len(got) != 1 || got[0] != tc.o {
				t.Errorf("Origin %q: Access-Control-Allow-Origin = %q, want it echoed", tc.o, got)
			}
		})
	}
}

// TestCORSAlwaysVariesOnOrigin is the cache-poisoning guard, and the reason the
// header goes on refusals too. These responses are Cache-Control: public behind
// a CDN; a cache keyed without Origin would store the allowed origin's response
// and replay its ACAO to every other origin, and the allowlist would mean
// nothing. It must also not clobber the handler's own Vary.
func TestCORSAlwaysVariesOnOrigin(t *testing.T) {
	for _, h := range []struct {
		name string
		next http.Handler
		keep string
	}{
		{"a handler that sets Vary and WriteHeader", okHandler(), "Accept-Encoding"},
		{"a handler that sets Vary and only writes", implicitStatusHandler(), "CF-IPLatitude"},
	} {
		t.Run(h.name, func(t *testing.T) {
			wrapped := httpx.CORS(h.next, allowlist(t, true))
			for _, o := range []string{siteOrigin, "http://127.0.0.1:60659", "https://evil.test", ""} {
				r := httptest.NewRequest(http.MethodGet, "/api/v1/hexes", nil)
				if o != "" {
					r.Header.Set("Origin", o)
				}
				w := httptest.NewRecorder()
				wrapped.ServeHTTP(w, r)

				vary := w.Header().Values("Vary")
				if !varies(vary, "Origin") {
					t.Errorf("Origin %q: Vary = %q, want it to include Origin", o, vary)
				}
				if !varies(vary, h.keep) {
					t.Errorf("Origin %q: Vary = %q, want the handler's %s preserved", o, vary, h.keep)
				}
			}
		})
	}
}

// varies searches both shapes Vary takes: repeated header lines, and one line
// of comma-separated tokens.
func varies(lines []string, token string) bool {
	for _, line := range lines {
		for _, tok := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), token) {
				return true
			}
		}
	}
	return false
}

// A second CORS in the chain, or a handler that already varies on Origin, must
// not produce "Vary: Origin, Origin".
func TestCORSDoesNotDuplicateVary(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Vary", "Accept-Encoding, Origin")
		w.WriteHeader(http.StatusOK)
	})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/hexes", nil)
	r.Header.Set("Origin", siteOrigin)
	w := httptest.NewRecorder()
	httpx.CORS(inner, allowlist(t, true)).ServeHTTP(w, r)

	var n int
	for _, line := range w.Header().Values("Vary") {
		for _, tok := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "Origin") {
				n++
			}
		}
	}
	if n != 1 {
		t.Errorf("Vary = %q, want Origin exactly once, got %d", w.Header().Values("Vary"), n)
	}
}

// TestCORSDoesNotBypassTheRateLimiter is the assertion that a header-only test
// would miss. Widening who may read the API must not widen how much they may
// read: a cross-origin browser request lands on the same per-client bucket as
// any other, and a refused one gets no ACAO, because it never reaches the
// middleware that sets it.
func TestCORSDoesNotBypassTheRateLimiter(t *testing.T) {
	l := ratelimit.New(testBucket(1, 1, time.Hour), testShardCount)
	h := httpx.WithClientIP(httpx.RateLimit(httpx.CORS(okHandler(), allowlist(t, true)), l), resolver(t))

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/hexes", nil)
		r.Header.Set("Origin", "http://127.0.0.1:60659")
		r.RemoteAddr = "203.0.113.51:41000"
		return r
	}

	first := httptest.NewRecorder()
	h.ServeHTTP(first, req())
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, req())
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request from an allowed loopback origin = %d, want %d; "+
			"CORS must not route around the per-client bucket", second.Code, http.StatusTooManyRequests)
	}
}
