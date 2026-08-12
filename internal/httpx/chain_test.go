package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/httpx"
	"airbg.org/internal/ratelimit"
)

// testBucket builds a config.Bucket for tests that only care about
// PerSecond/Burst; TTL is the one field these tests vary directly, so it stays
// a parameter rather than being folded into the literal below.
func testBucket(perSecond, burst float64, ttl time.Duration) config.Bucket {
	return config.Bucket{
		PerSecond:     perSecond,
		Burst:         burst,
		TTL:           ttl,
		EvictInterval: 5 * time.Minute,
		RetryAfter:    2 * time.Second,
	}
}

// testShardCount is a small, deterministic shard count for chain tests, which
// never exercise sharding directly.
const testShardCount = 8

func TestRecoverTurnsPanicInto500(t *testing.T) {
	h := httpx.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	// If Recover did not recover, this call panics and the test fails loudly —
	// which is the assertion. One handler bug must not take the process down and
	// with it every other in-flight request.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("the panic value leaked into the response body; panic text routinely contains paths, SQL and internal state")
	}
	if strings.Contains(rec.Body.String(), "goroutine") {
		t.Error("a stack trace leaked into the response body")
	}
}

// TestRecoverDoesNotWriteAfterHeaders: if the handler already wrote a status,
// Recover must not try to write another. net/http logs "superfluous
// WriteHeader" and the client gets a truncated body under a 200 — a corrupt
// response that looks successful.
func TestRecoverDoesNotWriteAfterHeaders(t *testing.T) {
	h := httpx.Recover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the already-written 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("the panic value was appended to the partial body")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := httpx.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), httpx.CSPValue)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"X-Frame-Options":        "DENY",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy is absent")
	}
	// frame-ancestors, not just X-Frame-Options: modern browsers honour the CSP
	// directive and ignore the legacy header when both are present.
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP is missing %q: %s", want, csp)
		}
	}
	// unsafe-inline would defeat the point of having a CSP at all.
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP contains an unsafe-* directive: %s", csp)
	}
}

// TestLimitBodyRejectsOversizeRequest: without a cap, a single request with an
// enormous body makes the origin allocate until it dies — a one-line
// denial-of-service that no rate limiter catches, because it is one request.
func TestLimitBodyRejectsOversizeRequest(t *testing.T) {
	var readErr error
	h := httpx.LimitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<16)
		for {
			_, err := r.Body.Read(buf)
			if err != nil {
				readErr = err
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}), 1024)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 8192)))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if readErr == nil {
		t.Error("reading an 8 KiB body under a 1 KiB cap produced no error")
	}
}

func TestRateLimitReturns429WithRetryAfter(t *testing.T) {
	l := ratelimit.New(testBucket(1, 1, time.Hour), testShardCount)
	res := resolver(t)

	h := httpx.WithClientIP(httpx.RateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), l), res)

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
		r.RemoteAddr = "203.0.113.50:41000"
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
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	ra := second.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("429 has no Retry-After")
	}
	if n, err := strconv.Atoi(ra); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive integer number of seconds", ra)
	}
}

// TestChainRateLimitsBeforeReachingTheHandler is the ordering test, and the one
// that catches the mistake that matters. If the limiter sits AFTER the handler
// — or after anything expensive — then the work a flood is meant to be denied
// has already been done by the time it is denied. A safety mechanism must not
// sit downstream of the failure it guards.
func TestChainRateLimitsBeforeReachingTheHandler(t *testing.T) {
	reached := 0
	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      ratelimit.New(testBucket(0, 1, time.Hour), testShardCount),
		MaxBodyBytes: 4096,
	}
	h := chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
		r.RemoteAddr = "203.0.113.51:41000"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	if reached != 1 {
		t.Errorf("handler ran %d times under a burst of 1; the limiter is downstream of the work it protects", reached)
	}
}

// TestChainRecoversAndStillSetsHeaders: a panicking handler must still produce a
// response carrying the security headers. Recover has to be OUTSIDE
// SecurityHeaders for that, and getting the nesting backwards yields a bare 500
// with no CSP — on an HTML error page that is an XSS surface.
func TestChainRecoversAndStillSetsHeaders(t *testing.T) {
	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      ratelimit.New(testBucket(100, 100, time.Hour), testShardCount),
		MaxBodyBytes: 4096,
	}
	h := chain.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	r.RemoteAddr = "203.0.113.52:41000"
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("the 500 produced by Recover carries no CSP; SecurityHeaders must run inside Recover so its headers are already set when the panic unwinds")
	}
}

// TestChainProvidesClientIPToTheHandler: the handler must see the resolved IP.
// If WithClientIP is missing from the chain, BucketKeyFrom falls back to
// "unattributed" and every client shares one bucket.
func TestChainProvidesClientIPToTheHandler(t *testing.T) {
	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      ratelimit.New(testBucket(100, 100, time.Hour), testShardCount),
		MaxBodyBytes: 4096,
	}

	var got string
	h := chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = httpx.BucketKeyFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	r.RemoteAddr = "203.0.113.53:41000"
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "203.0.113.53" {
		t.Errorf("BucketKeyFrom = %q, want %q", got, "203.0.113.53")
	}
}

// TestChainRecoverIsOutermostOfEveryMiddleware is the test the earlier round
// was missing. Every other panic-based test in this file panics from the
// FINAL HANDLER, which is downstream of every middleware regardless of their
// relative order — so those tests cannot tell "Recover outermost" apart from
// "Recover nested one layer in".
//
// This test panics from INSIDE a middleware instead. RateLimit panics on
// l.Allow when given a nil *ratelimit.Limiter — a real, reachable bug
// surface, not a synthetic stand-in — and that panic occurs entirely inside
// RateLimit's closure, before next.ServeHTTP is ever called. The test only
// passes if Recover genuinely wraps RateLimit, not merely the handler.
func TestChainRecoverIsOutermostOfEveryMiddleware(t *testing.T) {
	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      nil, // l.Allow(key) on a nil *Limiter panics inside RateLimit.
		MaxBodyBytes: 4096,
	}
	h := chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	r.RemoteAddr = "203.0.113.61:41000"
	// If Recover does not wrap RateLimit, this call panics and the test fails
	// loudly — a panic in one middleware must not be able to kill a
	// connection that every other middleware is still relying on Recover for.
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "nil pointer") || strings.Contains(body, "invalid memory address") {
		t.Error("the panic value leaked into the response body")
	}
	if strings.Contains(body, "goroutine") {
		t.Error("a stack trace leaked into the response body")
	}
}

// TestChainWrapComposesInDocumentedOrder observes Chain.Wrap's actual
// composition order directly, by recording the sequence in which each layer's
// handler runs as one request passes through — not by inspecting Chain's
// fields, which hold a Resolver, a Limiter and a byte count and reveal
// nothing about wrap order.
func TestChainWrapComposesInDocumentedOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	record := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, name)
	}

	httpx.SetOrderProbeForTesting(record)
	defer httpx.SetOrderProbeForTesting(nil)

	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      ratelimit.New(testBucket(100, 100, time.Hour), testShardCount),
		MaxBodyBytes: 4096,
	}
	h := chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		record("handler")
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	r.RemoteAddr = "203.0.113.62:41000"
	h.ServeHTTP(httptest.NewRecorder(), r)

	want := []string{"recover", "securityHeaders", "withClientIP", "rateLimit", "limitBody", "handler"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("composition order = %v, want %v", order, want)
	}
}
