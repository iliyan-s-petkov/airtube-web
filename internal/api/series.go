package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"airbg.org/internal/httpx"
	"airbg.org/internal/metrics"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// The period vocabulary. Fixed rather than free-form on purpose: an arbitrary
// duration lets one request ask for ten years of raw readings, which is
// unbounded database work that no rate limiter catches, because it is a single
// request.
//
// The hourly flag is not a performance hint — it is a correctness requirement.
// Raw readings are retained for 30 days (ingest.RawRetentionHours), so a 1-year
// window against `reading` silently returns the last 30 days under a "1 year"
// label: a chart that is wrong without being empty.
var periods = map[string]struct {
	window time.Duration
	hourly bool
}{
	"24h": {24 * time.Hour, false},
	"7d":  {7 * 24 * time.Hour, false},
	"30d": {30 * 24 * time.Hour, false},
	"1y":  {365 * 24 * time.Hour, true},
}

func parsePeriod(v string) (time.Duration, bool, bool) {
	p, ok := periods[v]
	return p.window, p.hourly, ok
}

// seriesMaxAge is the cache lifetime per period. An explicit table, not a
// formula: four values that each need their own justification are clearer as
// four literals than as a fitted curve.
//
// The reasoning is that a series' freshness requirement scales with how much of
// its window the newest point represents. A 24h chart of raw readings is a live
// view — one new point every few minutes visibly moves its right edge — so it
// keeps the snapshot-cadence 150 s. A 1-year chart is hourly rollups: one new
// point per hour, at the far right of 8,760, and re-fetching it every 150 s
// re-runs the single most expensive query in the service to redraw a pixel.
//
// This is also the volume bound the breadth counter cannot give. Breadth counts
// DISTINCT slugs and sensor IDs, so repeating one expensive request is free by
// design (see ratelimit/enumerate.go) — the only limit on a replayed
// ?period=1y is the token bucket. A long max-age lets any cache in front of the
// origin absorb that replay instead of PostgreSQL.
//
// Caveat, and the reason seriesLimiter below also exists: these responses are
// cachePrivate (they are keyed by slug or sensor ID and so are enumerable), so
// the cache absorbing the repeats is the requesting client's OWN browser, not a
// shared edge. That bounds a normal reader and an unsophisticated scraper; it
// does nothing against a client that ignores Cache-Control. The token bucket
// below is what bounds that one.
var seriesMaxAge = map[string]int{
	"24h": 150,   // one snapshot cycle; the chart's right edge is live
	"7d":  600,   // 10 min: a new raw point is a small fraction of the window
	"30d": 1800,  // 30 min
	"1y":  10800, // 3 h: hourly rollups, and the query is the heaviest we run
}

// maxAgeFor falls back to the shared data lifetime for an unrecognised period.
// Unreachable — parsePeriod rejects anything not in `periods` — but a missing
// map entry would otherwise mean max-age=0 and no caching at all.
func maxAgeFor(period string) int {
	if age, ok := seriesMaxAge[period]; ok {
		return age
	}
	return dataMaxAge
}

// ParsePeriodForTesting exposes parsePeriod so the raw/hourly cut-over can be
// asserted directly. Testing it only through the handler would leave the
// hourly flag verified by nothing — the stub returns the same points either way.
func ParsePeriodForTesting(v string) (time.Duration, bool, bool) { return parsePeriod(v) }

// seriesBody is columnar for the same reasons as the sensor payload: uPlot
// (Phase 3) consumes parallel arrays directly, and same-typed adjacent values
// compress well.
type seriesBody struct {
	SensorID *int64      `json:"sensor_id,omitempty"`
	Slug     string      `json:"slug,omitempty"`
	Metric   string      `json:"metric"`
	Period   string      `json:"period"`
	Hourly   bool        `json:"hourly"`
	Times    []time.Time `json:"t"`
	Values   []float64   `json:"v"`
}

// seriesRequest validates everything a series endpoint takes from the caller.
// Returning ok=false means a response has already been written.
func seriesRequest(w http.ResponseWriter, r *http.Request) (metric, period string, since time.Time, hourly, ok bool) {
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
		return "", "", time.Time{}, false, false
	}

	period = r.URL.Query().Get("period")
	window, hourly, valid := parsePeriod(period)
	if !valid {
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "period" parameter must be one of: 24h, 7d, 30d, 1y.`)
		return "", "", time.Time{}, false, false
	}

	return metric, period, time.Now().UTC().Add(-window), hourly, true
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

// seriesRate bounds the two DB-backed routes directly.
//
// Why a second bucket at all: the global limit (10 rps, burst 60) is sized for a
// human page load, which fans out to several snapshot reads costing a pointer
// load each. The series endpoints are the only ones that reach PostgreSQL, and
// the breadth counter cannot help — it counts DISTINCT slugs and sensor IDs, so
// replaying ONE ?period=1y request is free by design. Without this, the cheapest
// way to load the database is to ask the same expensive question 10 times a
// second forever.
//
// 1 rps with a burst of 10 is generous for the real client: a chart is drawn
// once per user interaction, and a page showing several metrics at once spends
// the burst, not the sustained rate. It is two orders of magnitude below what a
// replay attack needs to matter.
//
// Deliberately keyed on the same client key as the global bucket, so the two
// limits compose rather than each granting a separate allowance.
var seriesRate = ratelimit.Rate{PerSecond: 1, Burst: 10}

// seriesBucketTTL matches the global bucket's TTL: long enough that stepping
// away and coming back does not hand out a fresh burst.
const seriesBucketTTL = 30 * time.Minute

// seriesEvictInterval sweeps the series bucket's map. Matches the server's own
// eviction cadence; the map is small (one entry per client key that asked for
// history) so the sweep is cheap.
const seriesEvictInterval = 5 * time.Minute

// NewSeriesLimiter builds a series bucket. The caller owns it and is responsible
// for running its evictor — an un-swept limiter map is itself the memory leak the
// limiter exists to prevent. server.New does exactly that, tying the evictor to
// the server's context.
func NewSeriesLimiter() *ratelimit.Limiter {
	return ratelimit.New(seriesRate, seriesBucketTTL)
}

// defaultSeriesLimiter is the fallback NewRouter substitutes when Deps carries no
// SeriesLimiter, so a router is never built with the heaviest endpoints
// unlimited.
//
// Built once per process and swept, rather than freshly per NewRouter call.
// Per-call would have been the obvious thing and is wrong twice over: an
// un-swept limiter grows its map for as long as its router lives (unbounded for
// an embedder that keeps one), and starting a goroutine per NewRouter call to
// sweep it would leak a ticker per call instead. One shared, swept instance has
// neither problem, and its evictor is correctly scoped to the process because
// the value itself is.
//
// context.Background() is deliberate and is the one place in this codebase that
// starts a goroutine outside a caller's lifetime: this limiter has no owner to
// take a context from, and it must live exactly as long as the process.
var defaultSeriesLimiter = sync.OnceValue(func() *ratelimit.Limiter {
	l := NewSeriesLimiter()
	l.StartEvicting(context.Background(), seriesEvictInterval)
	return l
})

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

	metric, period, since, hourly, ok := seriesRequest(w, r)
	if !ok {
		return
	}

	// Breadth check before the query, not after: a refused request must not
	// have cost a database round trip, or the refusal is the expensive path.
	if !d.Breadth.ObserveSensor(httpx.BucketKeyFrom(r.Context()), id) {
		enumerationTrips.With("sensor").Inc()
		writeTooManySensors(w)
		return
	}

	if !d.allowSeriesQuery(w, r, "sensor") {
		return
	}

	points, err := d.Store.SensorSeries(r.Context(), id, metric, since, hourly)
	if err != nil {
		// Logged with the detail, answered without it. A pgx error carries the
		// SQL text and table names.
		slog.Error("sensor series query failed", "sensor_id", id, "metric", metric, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	writeSeries(w, seriesBody{
		SensorID: &id, Metric: metric, Period: period, Hourly: hourly,
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

	metric, period, since, hourly, ok := seriesRequest(w, r)
	if !ok {
		return
	}

	if !d.Breadth.ObserveArea(httpx.BucketKeyFrom(r.Context()), slug) {
		enumerationTrips.With("area").Inc()
		writeTooManyAreas(w)
		return
	}

	if !d.allowSeriesQuery(w, r, "area") {
		return
	}

	points, err := d.Store.AreaSeries(r.Context(), slug, metric, since, hourly)
	if err != nil {
		slog.Error("area series query failed", "slug", slug, "metric", metric, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	writeSeries(w, seriesBody{Slug: slug, Metric: metric, Period: period, Hourly: hourly}, points)
}

func writeSeries(w http.ResponseWriter, body seriesBody, points []store.Point) {
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
	setCacheControl(w.Header(), cachePrivate, maxAgeFor(body.Period))
	_, _ = w.Write(encoded)
}

// SeriesRateLimitedCountForTesting reads the series-refusal counter for one
// dimension, so a test can assert in DELTA that a 429 was recorded. The counter
// is process-global (internal/metrics registers it once at init), so an absolute
// count would depend on which other tests had already run.
func SeriesRateLimitedCountForTesting(dimension string) int64 {
	return seriesRateLimited.With(dimension).Value()
}
