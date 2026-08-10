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

// SecurityHeaders sets the response headers that do not depend on the handler.
//
// Set BEFORE calling next, so they are already on the ResponseWriter if the
// handler panics — a 500 rendered as HTML with no CSP is an XSS surface.
//
// HSTS is deliberately absent here and set by Cloudflare instead: sending it
// from the origin would also apply to a local `serve` over plain HTTP, pinning
// a developer's browser to HTTPS for localhost.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe("securityHeaders")
		h := w.Header()
		h.Set("Content-Security-Policy", CSPValue)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Legacy, for browsers that predate frame-ancestors. Harmless where
		// both are understood; the CSP directive wins there.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
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
