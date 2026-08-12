package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"airbg.org/internal/httpx"
)

// TestSecurityHeadersDenyDeviceCapabilities. Phase 3a embeds a bundle built from
// hundreds of transitive npm packages and serves it same-origin under a CSP that
// trusts 'self'. A malicious package does not need to escape a sandbox — it only
// needs to be in the bundle. This header is what keeps that bundle from reaching
// for the camera, the microphone or the user's location.
func TestSecurityHeadersDenyDeviceCapabilities(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("Permissions-Policy")
	if got == "" {
		t.Fatal("no Permissions-Policy header")
	}
	for _, feature := range []string{"geolocation=()", "camera=()", "microphone=()", "payment=()", "usb=()"} {
		if !strings.Contains(got, feature) {
			t.Errorf("Permissions-Policy = %q, missing %s", got, feature)
		}
	}
}

// TestSecurityHeadersSetCORP. The API responses are per-entity and enumerable.
// same-origin keeps them out of a third-party document context — which is the
// job people mistakenly expect CORS to do. There are deliberately no
// Access-Control-* headers anywhere in this project: their absence stops
// other-origin browser JS from READING a response and stops nothing else, so it
// is not an anti-extraction control and must not be presented as one.
func TestSecurityHeadersSetCORP(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("Cross-Origin-Resource-Policy = %q, want \"same-origin\"", got)
	}
}

// TestNoCORSHeaders pins the deliberate absence. An undocumented missing header
// looks like an oversight, and the plausible "fix" — a permissive ACAO — would
// be a straight downgrade with no compensating benefit. It checks every response
// header key for the Access-Control- prefix, case-insensitively, rather than
// enumerating a hand-picked subset: the header that gets added later is the one
// that would have been forgotten from a fixed list.
func TestNoCORSHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	const prefix = "access-control-"
	for name := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			t.Errorf("found CORS header %q, want no Access-Control-* headers present", name)
		}
	}
}

// TestCSPWithNoBasemapIsUnchanged is the safety net for the whole refactor. A
// deployment with no basemap must get byte-for-byte the Phase 2 policy — so
// turning a constant into a function provably changed nothing for anyone not
// using the new feature.
func TestCSPWithNoBasemapIsUnchanged(t *testing.T) {
	if got := httpx.CSP(""); got != httpx.CSPValue {
		t.Errorf("CSP(\"\") differs from CSPValue:\n got: %s\nwant: %s", got, httpx.CSPValue)
	}
}

// TestCSPWidensExactlyTwoDirectives. Widening a policy by string surgery is how
// object-src 'none' or frame-ancestors 'none' silently disappears, so the test
// compares directive by directive rather than asserting a substring.
func TestCSPWidensExactlyTwoDirectives(t *testing.T) {
	base := directives(t, httpx.CSP(""))
	wide := directives(t, httpx.CSP("tiles.example"))

	if len(base) != len(wide) {
		t.Fatalf("directive count changed: %d -> %d", len(base), len(wide))
	}
	for name, baseVal := range base {
		wideVal, ok := wide[name]
		if !ok {
			t.Errorf("directive %q disappeared when widening", name)
			continue
		}
		switch name {
		case "connect-src":
			if wideVal != "'self' https://tiles.example" {
				t.Errorf("connect-src = %q, want \"'self' https://tiles.example\"", wideVal)
			}
		case "img-src":
			if !strings.Contains(wideVal, "https://tiles.example") {
				t.Errorf("img-src = %q, missing the basemap host", wideVal)
			}
			// data: and blob: are what MapLibre needs for canvas sprites and
			// worker-produced tiles; widening must not drop them.
			for _, scheme := range []string{"data:", "blob:"} {
				if !strings.Contains(wideVal, scheme) {
					t.Errorf("img-src = %q, lost %s", wideVal, scheme)
				}
			}
		default:
			if wideVal != baseVal {
				t.Errorf("directive %q changed from %q to %q; only connect-src and img-src may widen", name, baseVal, wideVal)
			}
		}
	}
	// The directives that make the policy worth having, pinned by name so a
	// future edit cannot quietly drop one.
	for _, name := range []string{"object-src", "base-uri", "form-action", "frame-ancestors", "script-src", "worker-src"} {
		if _, ok := wide[name]; !ok {
			t.Errorf("widened policy has no %s directive", name)
		}
	}
}

// TestSecurityHeadersUsesTheSuppliedPolicy. Without this the parameter could be
// ignored and every other test here would still pass, since they all call CSP
// directly.
func TestSecurityHeadersUsesTheSuppliedPolicy(t *testing.T) {
	const custom = "default-src 'none'"
	rec := httptest.NewRecorder()
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), custom).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != custom {
		t.Errorf("Content-Security-Policy = %q, want %q", got, custom)
	}
}

// TestSecurityHeadersFallsBackWhenGivenNoPolicy fails closed. An empty CSP
// header is worse than a wrong one: it is a page with no policy at all, and the
// most likely cause is a new call site that forgot the argument.
func TestSecurityHeadersFallsBackWhenGivenNoPolicy(t *testing.T) {
	rec := httptest.NewRecorder()
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != httpx.CSPValue {
		t.Errorf("Content-Security-Policy = %q, want the CSPValue baseline", got)
	}
}

// directives splits a policy into name -> value. Fails the test on a malformed
// policy rather than returning a partial map that would make later assertions
// lie.
func directives(t *testing.T, policy string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, " ")
		if !ok {
			t.Fatalf("directive %q has no value", part)
		}
		out[name] = value
	}
	return out
}
