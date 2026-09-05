package httpx

import (
	"net/http"
	"strings"

	"airbg.org/internal/origin"
)

// CORS answers cross-origin reads of the JSON API for the origins a allows.
//
// Wrapped around the API mux rather than added to Chain, because the pages are
// not a cross-origin surface and a header set for everything is a header nobody
// can reason about. It sits INSIDE the chain, so a request refused by the rate
// limiter is refused before it gets here — a cross-origin caller lands on the
// same per-route bucket as any other, which is the point: this widens who may
// read the API, never how much of it they may read.
//
// No preflight handling. Every route is a GET with no custom request headers,
// which is a simple request — a browser sends it without asking first. An
// OPTIONS arriving here means something the API does not serve anyway.
func CORS(next http.Handler, a *origin.Allowlist) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echoed byte for byte. A browser compares ACAO to the Origin it sent,
		// so any other allowed value is a refusal in disguise, and an absent
		// header is the unambiguous refusal.
		if o := r.Header.Get("Origin"); a.Allows(o) {
			w.Header().Set("Access-Control-Allow-Origin", o)
		}
		next.ServeHTTP(&varyOnOrigin{ResponseWriter: w}, r)
	})
}

// varyOnOrigin adds Origin to Vary as the response goes out, rather than
// before the handler runs.
//
// The ordering is the whole point. Handlers downstream — internal/api/router.go
// and locate.go — SET Vary rather than adding to it, so a value written on the
// way in is silently erased by the time the response leaves. That failure is
// invisible in the code and invisible in a header test that stubs the handler:
// the middleware looks correct, and production ships responses with no Vary:
// Origin at all.
//
// It matters because the API's overview responses are Cache-Control: public
// behind a CDN. A cache keyed without Origin stores one origin's response —
// Access-Control-Allow-Origin included — and replays it to every other origin,
// which turns the allowlist into decoration. Refusals need it for the same
// reason: a refusal is a cacheable response too.
type varyOnOrigin struct {
	http.ResponseWriter
	done bool
}

// Unwrap lets http.ResponseController reach the wrapped writer, so wrapping
// here does not quietly cost a handler its Flush or its write deadline.
func (v *varyOnOrigin) Unwrap() http.ResponseWriter { return v.ResponseWriter }

func (v *varyOnOrigin) WriteHeader(code int) {
	v.addVary()
	v.ResponseWriter.WriteHeader(code)
}

// Write covers the handler that never calls WriteHeader: net/http sends 200 and
// the headers on the first Write, and by then it is too late to add one.
func (v *varyOnOrigin) Write(b []byte) (int, error) {
	v.addVary()
	return v.ResponseWriter.Write(b)
}

func (v *varyOnOrigin) addVary() {
	if v.done {
		return
	}
	v.done = true
	h := v.Header()
	// Add, not Set: whatever the handler chose to vary on is still true. Vary
	// is a comma-separated token list that may also arrive as repeated header
	// lines, so both shapes have to be searched before adding a duplicate.
	for _, line := range h.Values("Vary") {
		for _, tok := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(tok), "Origin") {
				return
			}
		}
	}
	h.Add("Vary", "Origin")
}
