package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"airbg.org/internal/httpx"
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
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(dataMaxAge))
	_, _ = w.Write(encoded)
}
