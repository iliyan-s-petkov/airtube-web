package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"airbg.org/internal/admit"
	"airbg.org/internal/config"
	"airbg.org/internal/httpx"
	"airbg.org/internal/metrics"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// admissionRejected counts requests shed by the admission semaphore.
//
// Separate from the two rate-limit counters because it answers a different
// operational question. A rate-limit refusal says "this client is asking for too
// much"; an admission refusal says "the service is at its database capacity
// regardless of who is asking". Under load an operator needs to tell those
// apart: the first is somebody misbehaving, the second is a sizing decision that
// has been reached.
//
// The label is the route, chosen from a fixed set of literals in the handlers.
// No request input reaches it, so cardinality is bounded by the code — the same
// rule as enumerationTrips and seriesRateLimited.
var admissionRejected = metrics.CounterVec(
	"airbg_admission_rejected_total",
	"Requests shed by the database admission semaphore, by route.",
	"route")

// tryAdmitQuery takes an admission slot, counting a refusal but writing nothing.
//
// Called immediately before the query and released immediately after, so the
// slot covers the database round trip and nothing else. Wrapping the whole
// handler would hold a slot through JSON encoding and the response write, which
// are not the scarce resource.
//
// The counter is incremented here rather than at the call sites so that every
// route — the ones that fail and the one that degrades — is visible under the
// same metric. An operator needs to know the control fired even when the caller
// never saw an error.
func (d Deps) tryAdmitQuery(route string) (release func(), ok bool) {
	release, ok = d.Admission.TryAcquire()
	if ok {
		return release, true
	}
	admissionRejected.With(route).Inc()
	return nil, false
}

// admitQuery takes an admission slot or answers 503.
//
// For the routes whose whole answer IS the query: with no slot there is nothing
// truthful to return, so the request fails. /locate is the exception — it has a
// usable default view and calls tryAdmitQuery directly.
//
// 503 rather than 429 on purpose: the client is within its own limit and did
// nothing wrong. The 503's Retry-After hint is ratelimit.series.retry_after —
// chosen to be long enough for the in-flight queries to drain, short enough
// that a legitimate reader's chart appears late rather than never. It is a
// configured value because nothing here can compute one: this is admission
// pressure from every client at once, not one client's token deficit (which is
// what the 429 path in httpx.Chain derives its own Retry-After from).
func (d Deps) admitQuery(w http.ResponseWriter, route string) (release func(), ok bool) {
	release, ok = d.tryAdmitQuery(route)
	if ok {
		return release, true
	}
	// Retry-After in seconds. No bucket rejected this request — the admission
	// semaphore did — so the value is the configured series hint.
	w.Header().Set("Retry-After", strconv.Itoa(int(d.Config.RateLimit.Series.RetryAfter.Seconds())))
	writeError(w, http.StatusServiceUnavailable, "unavailable",
		"The service is busy. Please try again shortly.")
	return nil, false
}

// AdmissionRejectedCountForTesting reads the shed counter for one route so a
// test can assert in DELTA. The counter is process-global, so an absolute count
// would depend on which other tests had already run.
func AdmissionRejectedCountForTesting(route string) int64 {
	return admissionRejected.With(route).Value()
}

// parsePeriod resolves a caller's ?period= against the configured table. The
// hourly flag is not a performance hint but a correctness requirement: raw
// readings are retained for 30 days, so a 1-year window against `reading`
// silently returns the last 30 days under a "1 year" label — a chart that is
// wrong without being empty.
func parsePeriod(cfg config.Series, v string) (config.Period, bool) {
	p, ok := cfg.Periods[v]
	return p, ok
}

// maxAgeFor is per period because each value needs its own justification: a 24h
// chart of raw readings is a live view whose right edge moves every few
// minutes, while a 1-year chart is hourly rollups where refetching repaints one
// pixel of 8,760 and re-runs the heaviest query in the service.
//
// An unrecognised period cannot reach here (parsePeriod rejects it), but a zero
// max-age would mean no caching at all, so fall back to the shared lifetime.
func maxAgeFor(cfg config.Config, period string) int {
	if p, ok := cfg.Series.Periods[period]; ok && p.MaxAge > 0 {
		return int(p.MaxAge.Seconds())
	}
	return int(cfg.Cache.DataMaxAge.Seconds())
}

// ParsePeriodForTesting exposes parsePeriod so the raw/hourly cut-over can be
// asserted directly. Testing it only through the handler would leave the
// hourly flag verified by nothing — the stub returns the same points either way.
func ParsePeriodForTesting(cfg config.Series, v string) (time.Duration, bool, bool) {
	p, ok := parsePeriod(cfg, v)
	return p.Window, p.Hourly, ok
}

// seriesRequest validates everything a series endpoint takes from the caller.
// Returning ok=false means a response has already been written.
func seriesRequest(w http.ResponseWriter, r *http.Request, cfg config.Series) (metric, period string, since time.Time, pd config.Period, ok bool) {
	metric = r.URL.Query().Get("metric")
	// Validated against the canonical set. The value reaches a WHERE clause, so
	// this is also what guarantees no caller string is ever interpolated —
	// belt and braces alongside the parameterised query. It is also the answer
	// to a faulty sensor reporting garbage: an unrecognised metric name can
	// never reach the query in the first place, and the values a real metric
	// returns are already filtered at ingest (internal/quality) to exclude
	// out-of-range and NaN readings before they are ever stored as 'ok'.
	if !upstream.IsCanonicalMetric(metric) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"Unknown metric. Valid metrics are: "+joinComma(upstream.CanonicalMetrics())+".")
		return "", "", time.Time{}, config.Period{}, false
	}

	period = r.URL.Query().Get("period")
	pd, valid := parsePeriod(cfg, period)
	if !valid {
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "period" parameter must be one of: 24h, 7d, 30d, 1y.`)
		return "", "", time.Time{}, config.Period{}, false
	}

	return metric, period, time.Now().UTC().Add(-pd.Window), pd, true
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// NewSeriesLimiter builds a series bucket from cfg.RateLimit.Series, sharded
// across cfg.RateLimit.ShardCount. The caller owns it and is responsible for
// running its evictor — an un-swept limiter map is itself the memory leak the
// limiter exists to prevent. server.New does exactly that, tying the evictor to
// the server's context.
//
// Why a second bucket at all: the global limit is sized for a human page load,
// which fans out to several snapshot reads costing a pointer load each. The
// series endpoints are the only ones that reach PostgreSQL, and the breadth
// counter cannot help — it counts DISTINCT slugs and sensor IDs, so replaying
// ONE ?period=1y request is free by design. Without this, the cheapest way to
// load the database is to ask the same expensive question repeatedly forever.
//
// Deliberately keyed on the same client key as the global bucket, so the two
// limits compose rather than each granting a separate allowance.
func NewSeriesLimiter(cfg config.Config) *ratelimit.Limiter {
	return ratelimit.New(cfg.RateLimit.Series.Bucket, cfg.RateLimit.ShardCount)
}

// defaultSeriesLimiterOnce and defaultSeriesLimiterVal back defaultSeriesLimiter.
//
// Built once per process and swept, rather than freshly per NewRouter call.
// Per-call would have been the obvious thing and is wrong twice over: an
// un-swept limiter grows its map for as long as its router lives (unbounded for
// an embedder that keeps one), and starting a goroutine per NewRouter call to
// sweep it would leak a ticker per call instead. One shared, swept instance has
// neither problem, and its evictor is correctly scoped to the process because
// the value itself is.
var (
	defaultSeriesLimiterOnce sync.Once
	defaultSeriesLimiterVal  *ratelimit.Limiter
)

// defaultSeriesLimiter is the fallback NewRouter substitutes when Deps carries no
// SeriesLimiter, so a router is never built with the heaviest endpoints
// unlimited.
//
// cfg is only consulted the first time this is called in the process — later
// callers get the already-built instance regardless of their own cfg. That is
// the same one-instance-per-process contract sync.OnceValue gave the constant
// version; a plain sync.OnceValue no longer fits because building the limiter
// now needs an argument.
//
// context.Background() is deliberate and is the one place in this codebase that
// starts a goroutine outside a caller's lifetime: this limiter has no owner to
// take a context from, and it must live exactly as long as the process.
func defaultSeriesLimiter(cfg config.Config) *ratelimit.Limiter {
	defaultSeriesLimiterOnce.Do(func() {
		l := NewSeriesLimiter(cfg)
		l.StartEvicting(context.Background(), cfg.RateLimit.Series.EvictInterval)
		defaultSeriesLimiterVal = l
	})
	return defaultSeriesLimiterVal
}

// defaultAdmission is the substitute NewRouter uses when Deps carries none.
// Built once per process, like defaultSeriesLimiter and for the same reason: a
// fresh one per NewRouter call would give each router its own cap, so an
// embedder holding several would collectively exceed the number.
//
// The error from admit.New is impossible here — the literal is positive — and is
// discarded rather than plumbed into a signature that has no way to report it.
var defaultAdmission = sync.OnceValue(func() *admit.Semaphore {
	s, _ := admit.New(defaultMaxDBInflight)
	return s
})

// defaultMaxDBInflight is admit.DefaultSize, the one definition shared with
// config and server, so an unconfigured router cannot end up with a different
// cap from the documented default.
const defaultMaxDBInflight = admit.DefaultSize

// seriesRateLimited counts refusals by the series bucket.
//
// A sibling of airbg_http_rate_limited_total rather than the same counter: that
// one reports the GLOBAL bucket, is incremented from outside the mux, and mixing
// two limiters with different rates under one name would make neither
// attributable. Under attack an operator needs to know which limit is biting,
// and the series bucket biting means the database is the target.
//
// The label is the dimension — "sensor" or "area" — chosen from the handler, a
// fixed two-value set. No request input reaches it, so cardinality is bounded by
// the code and not by the caller. (That is the same rule as enumerationTrips,
// and the reason neither labels by path.)
var seriesRateLimited = metrics.CounterVec(
	"airbg_series_rate_limited_total",
	"Series requests refused by the per-route series token bucket, by dimension.",
	"dimension")

// allowSeriesQuery spends a token from the series bucket, answering 429 with a
// truthful Retry-After when it is empty.
//
// Called after validation and after the breadth check, but BEFORE the query: the
// entire point is that a refused request costs no database work.
//
// dimension is "sensor" or "area", a literal from the calling handler.
func (d Deps) allowSeriesQuery(w http.ResponseWriter, r *http.Request, dimension string) bool {
	ok, retryAfter := d.SeriesLimiter.Allow(httpx.BucketKeyFrom(r.Context()))
	if ok {
		return true
	}
	// Counted before the response is written, so a refusal is never invisible to
	// metrics even if the write fails. Refusals on the heaviest path are exactly
	// what an operator needs to see under attack.
	seriesRateLimited.With(dimension).Inc()
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		"Too many history requests. Please slow down.")
	return false
}

func (d Deps) handleSensorSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "The sensor id must be a positive integer.")
		return
	}

	metric, period, since, pd, ok := seriesRequest(w, r, d.Config.Series)
	if !ok {
		return
	}

	// Breadth check before the query, not after: a refused request must not
	// have cost a database round trip, or the refusal is the expensive path.
	if !d.Breadth.ObserveSensor(httpx.BucketKeyFrom(r.Context()), id) {
		enumerationTrips.With("sensor").Inc()
		writeTooManySensors(w, d.Config.RateLimit.Enumerate)
		return
	}

	if !d.allowSeriesQuery(w, r, "sensor") {
		return
	}

	release, ok := d.admitQuery(w, "sensor_series")
	if !ok {
		return
	}
	points, err := d.Store.SensorSeries(r.Context(), id, metric, since, pd.Hourly, pd.Bucket)
	release()
	if err != nil {
		// Logged with the detail, answered without it. A pgx error carries the
		// SQL text and table names.
		slog.Error("sensor series query failed", "sensor_id", id, "metric", metric, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	writeSeries(w, d.Config, snapshot.SeriesPayload{
		SensorID: &id, Metric: metric, Period: period, Hourly: pd.Hourly,
	}, points)
}

func (d Deps) handleAreaSeries(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	slug := r.PathValue("slug")
	if _, known := snap.KnownSlugs[slug]; !known {
		writeError(w, http.StatusNotFound, "not_found", "No such area.")
		return
	}

	metric, period, since, pd, ok := seriesRequest(w, r, d.Config.Series)
	if !ok {
		return
	}

	if !d.Breadth.ObserveArea(httpx.BucketKeyFrom(r.Context()), slug) {
		enumerationTrips.With("area").Inc()
		writeTooManyAreas(w, d.Config.RateLimit.Enumerate)
		return
	}

	// Serve the one precomputed combination from memory. Placed here, not
	// earlier, and not later, for two reasons that are both load-bearing:
	//
	//   - AFTER the breadth check, because the response is per-entity and
	//     enumerable regardless of where it came from. A fast path that skipped
	//     ObserveArea would let a scraper walk every slug's history for free.
	//   - BEFORE allowSeriesQuery, because this request issues no query. The
	//     series bucket exists to protect Postgres, and spending its tokens on
	//     requests that never reach Postgres would starve the path it is
	//     actually guarding.
	if metric == d.Snapshots.DefaultMetric() && period == snapshot.DefaultSeriesPeriod {
		if body, ok := snap.AreaSeries[slug]; ok {
			serveBody(w, r, body, cachePrivate, maxAgeFor(d.Config, period))
			return
		}
		// No entry for a known slug means a snapshot built before this field
		// existed, or a build that failed partway. Fall through to the database
		// rather than 404 a slug we know exists.
	}

	if !d.allowSeriesQuery(w, r, "area") {
		return
	}

	release, ok := d.admitQuery(w, "area_series")
	if !ok {
		return
	}
	points, err := d.Store.AreaSeries(r.Context(), slug, metric, since, pd.Hourly, pd.Bucket)
	release()
	if err != nil {
		slog.Error("area series query failed", "slug", slug, "metric", metric, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	writeSeries(w, d.Config, snapshot.SeriesPayload{Slug: slug, Metric: metric, Period: period, Hourly: pd.Hourly}, points)
}

func writeSeries(w http.ResponseWriter, cfg config.Config, body snapshot.SeriesPayload, points []store.Point) {
	// Allocated with make, not left nil: a nil slice marshals to `null`, and a
	// charting library handed null throws instead of drawing an empty axis.
	body.Times = make([]time.Time, 0, len(points))
	body.Values = make([]float64, 0, len(points))
	for _, p := range points {
		body.Times = append(body.Times, p.Time)
		body.Values = append(body.Values, p.Value)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		// json.Marshal fails on a NaN or Inf float64 ("unsupported value").
		// That should be unreachable here — ingest (internal/quality) flags
		// NaN/out-of-range readings out_of_range, and both series queries
		// filter to the usable quality set — but if it ever happened anyway,
		// this is the fallback: a generic 500, not a half-written body with
		// the failure reason (and the offending numbers) leaked into it.
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// cachePrivate, not public: a series response is keyed by sensor ID or slug,
	// so it is enumerable and must never be servable from a shared cache that
	// the breadth counter cannot see. See router.go's cachePublic/cachePrivate.
	setCacheControl(w.Header(), cachePrivate, maxAgeFor(cfg, body.Period))
	_, _ = w.Write(encoded)
}

// SeriesRateLimitedCountForTesting reads the series-refusal counter for one
// dimension, so a test can assert in DELTA that a 429 was recorded. The counter
// is process-global (internal/metrics registers it once at init), so an absolute
// count would depend on which other tests had already run.
func SeriesRateLimitedCountForTesting(dimension string) int64 {
	return seriesRateLimited.With(dimension).Value()
}
