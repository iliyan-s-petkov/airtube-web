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
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
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
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
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
	httpx.SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	const prefix = "access-control-"
	for name := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			t.Errorf("found CORS header %q, want no Access-Control-* headers present", name)
		}
	}
}
