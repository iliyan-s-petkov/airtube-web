package httpx

import (
	"net/http"
	"strconv"

	"airbg.org/internal/metrics"
	"airbg.org/internal/ratelimit"
)

// requestsTotal used to live here too, labelled "pattern", but it duplicated
// internal/metrics.httpRequests (same name, different label name) and — because
// RateLimit runs OUTSIDE the mux, before routing — could only ever record
// pattern="unmatched". A counter that structurally cannot distinguish routes
// carried no information the other copy did not already carry correctly, so
// it was removed rather than renamed.
var rateLimited = metrics.CounterVec(
	"airbg_http_rate_limited_total",
	"Requests refused by the origin token buckets, by route pattern.",
	"pattern")

// orderProbe, when non-nil, is called by name at each middleware's entry, in
// the order a request actually passes through them. Nothing else in this
// package can observe Chain.Wrap's real composition order — the struct only
// holds a Resolver, a Limiter and a byte count, none of which reveal wrapping
// order — so tests that need to pin the order call SetOrderProbeForTesting
// rather than inspect Chain's fields.
//
// Nil in production: every call site guards on it, so shipping this costs one
// nil check per middleware per request.
var orderProbe func(name string)

// SetOrderProbeForTesting installs or clears the order-observation hook.
func SetOrderProbeForTesting(fn func(name string)) {
	orderProbe = fn
}

func probe(name string) {
	if orderProbe != nil {
		orderProbe(name)
	}
}

// RateLimit refuses a request when its client's bucket is empty.
//
// It requires WithClientIP upstream of it; BucketKeyFrom returns
// "unattributed" otherwise, which would pool every client into one bucket. Chain
// guarantees the ordering.
func RateLimit(next http.Handler, l *ratelimit.Limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe("rateLimit")
		key := BucketKeyFrom(r.Context())
		ok, retryAfter := l.Allow(key)
		if !ok {
			// Label by the route PATTERN, never the concrete path: the path is
			// caller-controlled and would give the metric unbounded label
			// cardinality — an attacker could exhaust memory through the
			// counters that exist to report the attack.
			rateLimited.With(patternLabel(r)).Inc()

			secs := int(retryAfter.Seconds())
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited","message":"Too many requests. Please slow down."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// patternLabel returns the matched route pattern, or "unmatched" when the
// request never reached the mux (which is the case for middleware running
// outside it). Bounded by the route table, so label cardinality is bounded too.
func patternLabel(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}

// Chain composes the middleware every public request passes through.
type Chain struct {
	Resolver     *IPResolver
	Limiter      *ratelimit.Limiter
	MaxBodyBytes int64
}

// Wrap builds the handler. Order, outermost first:
//
//  1. Recover      — must be outermost, or a panic in any other middleware
//                    kills the connection with no response and no metric.
//  2. SecurityHeaders — inside Recover so its headers are already set when a
//                    panic unwinds and Recover writes its 500.
//  3. WithClientIP — must precede RateLimit, which keys on its output.
//  4. RateLimit    — as early as possible: everything downstream of it is work
//                    a refused request must not cost us. This is the ordering
//                    property TestChainRateLimitsBeforeReachingTheHandler pins.
//  5. LimitBody    — cheap, and only relevant to a request that got this far.
//  6. the handler.
//
// Enumeration detection is NOT here. It needs the parsed {slug} and {id} path
// parameters, which only exist after the mux has matched, so it lives in the
// api package's per-route handlers.
func (c Chain) Wrap(h http.Handler) http.Handler {
	h = LimitBody(h, c.MaxBodyBytes)
	h = RateLimit(h, c.Limiter)
	h = WithClientIP(h, c.Resolver)
	h = SecurityHeaders(h)
	h = Recover(h)
	return h
}
