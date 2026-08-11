package httpx

import "net/http"

// CSPValue is the policy from Phase 1 §9.7.
//
// No 'unsafe-inline' and no 'unsafe-eval': Phase 3's islands ship as external
// modules and its map styles as external JSON, so nothing needs them. Adding
// either would make the CSP decorative — an inline-script allowance is the
// single most common way a CSP stops mitigating XSS.
//
// connect-src 'self' keeps the API calls same-origin. img-src allows data: for
// MapLibre's canvas-generated sprites and blob: for its worker-produced tiles;
// worker-src blob: is required by MapLibre GL JS, which constructs its workers
// from blobs.
const CSPValue = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"worker-src 'self' blob:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// PermissionsPolicyValue denies every browser capability the site does not use.
//
// The threat this addresses is the frontend bundle itself: it is built from
// hundreds of transitive npm packages and served same-origin under a CSP that
// trusts 'self', so a malicious package does not need to escape a sandbox — it
// only needs to be in the bundle. A denial here is enforced by the browser
// regardless of what the bundle asks for.
//
// geolocation is denied even though Phase 3b's "sensors near me" button will
// need it. Opening it then is a reviewed one-line change; having it open before
// anything uses it is an allowance nobody chose.
const PermissionsPolicyValue = "geolocation=(), camera=(), microphone=(), payment=(), usb=()"

// CSP builds the policy, widening connect-src and img-src by the basemap host.
//
// An empty host yields exactly CSPValue, byte for byte — pinned by
// TestCSPWithNoBasemapIsUnchanged, so a deployment with no basemap is provably
// unaffected by this function existing.
//
// Built by assembling named directives rather than by string-replacing inside
// CSPValue. Substring surgery on a policy is how `object-src 'none'` silently
// disappears: the edit that drops it looks like the edit that widens
// connect-src, and nothing fails.
//
// The host is a bare hostname (optionally with a port), taken from the basemap
// style URL's origin at startup — never from a request. https:// is prepended
// unconditionally: a tile vendor reached over plain HTTP would be a
// mixed-content error in the browser long before the CSP mattered.
//
// basemapHost is trusted here because config.Load already validated it against
// hostPattern before storing it in Config.BasemapHost — this function does not
// re-validate, so any new call site must go through that same gate rather than
// handing a request-derived or otherwise unvalidated string to a function that
// assembles a header value by concatenation.
func CSP(basemapHost string) string {
	connect := "'self'"
	img := "'self' data: blob:"
	if basemapHost != "" {
		connect += " https://" + basemapHost
		img += " https://" + basemapHost
	}
	return "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"img-src " + img + "; " +
		"font-src 'self'; " +
		"connect-src " + connect + "; " +
		"worker-src 'self' blob:; " +
		"object-src 'none'; " +
		"base-uri 'none'; " +
		"form-action 'none'; " +
		"frame-ancestors 'none'"
}

// SecurityHeaders sets the response headers that do not depend on the handler.
//
// Set BEFORE calling next, so they are already on the ResponseWriter if the
// handler panics — a 500 rendered as HTML with no CSP is an XSS surface.
//
// HSTS is deliberately absent here and set by Cloudflare instead: sending it
// from the origin would also apply to a local `serve` over plain HTTP, pinning
// a developer's browser to HTTPS for localhost.
//
// csp is per-process rather than the CSPValue constant, because it is widened
// by the configured basemap host. Empty falls back to CSPValue — fail closed:
// an empty policy means a caller forgot the argument, and a response with no
// CSP at all is a worse outcome than one with a policy that does not know
// about the basemap.
func SecurityHeaders(next http.Handler, csp string) http.Handler {
	if csp == "" {
		csp = CSPValue
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe("securityHeaders")
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Legacy, for browsers that predate frame-ancestors. Harmless where
		// both are understood; the CSP directive wins there.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", PermissionsPolicyValue)
		// same-origin, so the per-entity JSON payloads cannot be pulled into
		// a third-party document context. This is the header that does the
		// job CORS is commonly mistaken for; see the deliberate absence of
		// every Access-Control-* header, pinned by TestNoCORSHeaders.
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// LimitBody caps how much of a request body a handler can read.
//
// Every Phase 2 endpoint is a GET with no body, so the cap is small. It exists
// because without it one request with an enormous body can make the origin
// allocate until it dies — a denial of service that no rate limiter sees, since
// it is a single request.
func LimitBody(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe("limitBody")
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}
