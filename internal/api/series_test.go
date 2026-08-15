package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/admit"
	"airbg.org/internal/api"
	"airbg.org/internal/config"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

var errBoom = errors.New("boom: pq: relation \"reading\" does not exist")

func itoa(i int) string { return strconv.Itoa(i) }

func withPoints(t *testing.T, points []store.Point) api.Deps {
	t.Helper()
	d := deps(t, fixture(t))
	d.Store = &stubSource{slug: "sofia", points: points}
	return d
}

func samplePoints() []store.Point {
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	return []store.Point{
		{Time: base, Value: 12.5},
		{Time: base.Add(time.Hour), Value: 14},
	}
}

func TestSensorSeriesReturnsPoints(t *testing.T) {
	rec := serve(t, withPoints(t, samplePoints()),
		get("/api/v1/sensor/42/series?metric=P2&period=24h", "203.0.113.20"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		SensorID int64     `json:"sensor_id"`
		Metric   string    `json:"metric"`
		Period   string    `json:"period"`
		Times    []string  `json:"t"`
		Values   []float64 `json:"v"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if got.SensorID != 42 {
		t.Errorf("sensor_id = %d, want 42", got.SensorID)
	}
	// Columnar, same as the sensor payload — and the two columns must be the
	// same length or the points do not line up.
	if len(got.Times) != 2 || len(got.Values) != 2 {
		t.Fatalf("t has %d entries and v has %d, want 2 each", len(got.Times), len(got.Values))
	}
	if got.Values[0] != 12.5 {
		t.Errorf("v[0] = %v, want 12.5", got.Values[0])
	}
}

// TestSeriesRejectsUnknownMetric: the metric reaches a WHERE clause. It is
// validated against the canonical set, so no caller-supplied string is ever
// interpolated — and an unrecognised one is a 400, not an empty 200 that would
// read as "this sensor measures nothing".
func TestSeriesRejectsUnknownMetric(t *testing.T) {
	for _, metric := range []string{"", "durP1", "P2; DROP TABLE reading", "../P2"} {
		rec := serve(t, withPoints(t, samplePoints()),
			get("/api/v1/sensor/42/series?metric="+url.QueryEscape(metric)+"&period=24h", "203.0.113.21"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("metric=%q: status = %d, want 400", metric, rec.Code)
		}
	}
}

// TestSeriesRejectsUnknownPeriod: a fixed vocabulary, not a free-form duration.
// An arbitrary window lets a caller request ten years of raw readings and make
// the database do unbounded work — one request, no rate limit triggered.
func TestSeriesRejectsUnknownPeriod(t *testing.T) {
	for _, period := range []string{"", "99y", "1s", "forever", "-24h"} {
		rec := serve(t, withPoints(t, samplePoints()),
			get("/api/v1/sensor/42/series?metric=P2&period="+period, "203.0.113.22"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("period=%q: status = %d, want 400", period, rec.Code)
		}
	}
}

func TestSeriesRejectsNonNumericSensorID(t *testing.T) {
	rec := serve(t, withPoints(t, samplePoints()),
		get("/api/v1/sensor/abc/series?metric=P2&period=24h", "203.0.113.23"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestLongPeriodsUseTheRollup. Raw readings are retained 30 days; a 1-year
// window against `reading` would return the last 30 days and label it a year —
// a chart that is wrong without being empty, which is the hardest kind to catch.
func TestLongPeriodsUseTheRollup(t *testing.T) {
	cfg := testConfig(t).Series
	cases := map[string]bool{"24h": false, "7d": false, "30d": false, "1y": true}
	for period, wantHourly := range cases {
		_, hourly, ok := api.ParsePeriodForTesting(cfg, period)
		if !ok {
			t.Errorf("period %q was rejected", period)
			continue
		}
		if hourly != wantHourly {
			t.Errorf("period %q: hourly = %v, want %v", period, hourly, wantHourly)
		}
	}
}

// TestEmptySeriesIsTwoEmptyArraysNotNull: `null` and `[]` are different values
// to every JSON consumer, and a chart library handed null throws rather than
// drawing an empty axis.
func TestEmptySeriesIsTwoEmptyArraysNotNull(t *testing.T) {
	rec := serve(t, withPoints(t, nil),
		get("/api/v1/sensor/42/series?metric=P2&period=24h", "203.0.113.24"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"t", "v"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
}

// TestSensorSeriesEnumerationTrips: the sensor dimension of the breadth check.
func TestSensorSeriesEnumerationTrips(t *testing.T) {
	d := withPoints(t, samplePoints())
	d.Breadth = ratelimit.NewBreadth(config.Enumerate{
		AreasPerWindow: 100, SensorsPerWindow: 3, Window: time.Hour, RetryAfter: 900 * time.Second,
	})

	allowed := 0
	var lastRejected *httptest.ResponseRecorder
	for id := 1; id <= 5; id++ {
		rec := serve(t, d, get("/api/v1/sensor/"+itoa(id)+"/series?metric=P2&period=24h", "203.0.113.25"))
		if rec.Code == http.StatusOK {
			allowed++
		} else {
			lastRejected = rec
		}
	}
	if allowed != 3 {
		t.Errorf("allowed %d distinct sensors, want 3", allowed)
	}
	// The Retry-After on a breadth rejection must come from the Enumerate bucket
	// that rejected it (900s in this test's config), not the unrelated Series
	// rate-limit bucket (2s) — asserting the literal proves nothing on its own,
	// but this test's bucket is built with RetryAfter: 900*time.Second above, so
	// a wiring bug that reads the wrong bucket changes this value.
	if lastRejected == nil {
		t.Fatal("no request was rejected; cannot check Retry-After")
	}
	if got, want := lastRejected.Header().Get("Retry-After"), "900"; got != want {
		t.Errorf("Retry-After = %q, want %q (config.Enumerate.RetryAfter)", got, want)
	}
}

// TestSeriesDatabaseErrorIsNotLeaked: the message must be a fixed sentence. A
// pgx error carries the SQL and the table names.
func TestSeriesDatabaseErrorIsNotLeaked(t *testing.T) {
	d := deps(t, fixture(t))
	d.Store = &stubSource{err: errBoom}

	rec := serve(t, d, get("/api/v1/sensor/42/series?metric=P2&period=24h", "203.0.113.26"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "boom") || strings.Contains(body, "reading") {
		t.Errorf("the database error leaked into the response: %s", body)
	}
}

func TestAreaSeriesRequiresAKnownSlug(t *testing.T) {
	rec := serve(t, withPoints(t, samplePoints()),
		get("/api/v1/area/atlantis/series?metric=P2&period=24h", "203.0.113.27"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestSensorSeriesRejectsNegativeAndZeroID: the id is a bare integer taken
// straight into the enumeration-breadth key and the store query. Neither a
// negative nor a zero sensor id names a real sensor, so both must be rejected
// before either is observed or queried.
func TestSensorSeriesRejectsNegativeAndZeroID(t *testing.T) {
	for _, id := range []string{"-1", "0"} {
		rec := serve(t, withPoints(t, samplePoints()),
			get("/api/v1/sensor/"+id+"/series?metric=P2&period=24h", "203.0.113.28"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("id=%q: status = %d, want 400", id, rec.Code)
		}
	}
}

// TestUnknownSensorSeriesDoesNotConsumeBudget mirrors
// TestUnknownSlugDoesNotConsumeAreaBudget (Task 11) for the sensor dimension:
// a request that fails validation before the breadth check must not have
// touched the shared bucket. Here "unknown" is represented by a malformed id,
// since the sensor series endpoint (unlike area) has no snapshot to check a
// real id against before querying — the id-parse failure is the only gate
// that runs before ObserveSensor, so it is what this test pins.
func TestUnknownSensorSeriesDoesNotConsumeBudget(t *testing.T) {
	d := withPoints(t, samplePoints())
	d.Breadth = ratelimit.NewBreadth(config.Enumerate{
		AreasPerWindow: 100, SensorsPerWindow: 2, Window: time.Hour, RetryAfter: 900 * time.Second,
	})

	for i := 0; i < 10; i++ {
		rec := serve(t, d, get("/api/v1/sensor/abc/series?metric=P2&period=24h", "203.0.113.29"))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	}

	// Two distinct real sensor ids from the same client must still both
	// succeed: the malformed-id requests above must not have burned the
	// budget.
	for _, id := range []string{"1", "2"} {
		rec := serve(t, d, get("/api/v1/sensor/"+id+"/series?metric=P2&period=24h", "203.0.113.29"))
		if rec.Code != http.StatusOK {
			t.Errorf("sensor %s: status = %d, want 200", id, rec.Code)
		}
	}
}

// TestLongPeriodSeriesGetsALongerMaxAge pins the period-scaled cache lifetime.
//
// The series endpoints are the only ones that reach PostgreSQL, and the breadth
// counter cannot bound them: it counts DISTINCT slugs and sensor IDs, so
// replaying ONE ?period=1y request is free by design. A 1-year series is hourly
// rollups — one new point per hour at the right edge of 8,760 — so re-running the
// heaviest query in the service on the same 150 s cadence as a live 24h chart is
// pure waste. Longer windows therefore get longer TTLs, monotonically.
//
// Monotonicity across the WHOLE vocabulary, not just the endpoints of it: a
// mapping that gives 1y a long TTL while leaving 7d and 30d at the short one
// would pass a two-value comparison and still re-run two of the three expensive
// periods at full rate.
func TestLongPeriodSeriesGetsALongerMaxAge(t *testing.T) {
	d := withPoints(t, samplePoints())

	// The declared period order, shortest window first.
	ordered := []string{"24h", "7d", "30d", "1y"}

	ages := make(map[string]int, len(ordered))
	for i, period := range ordered {
		// A fresh client IP per period: the series token bucket is per client
		// key, and this test is about cache lifetimes, not refusals.
		rec := serve(t, d, get("/api/v1/sensor/42/series?metric=P2&period="+period,
			"203.0.113."+itoa(200+i)))
		if rec.Code != http.StatusOK {
			t.Fatalf("period=%s: status = %d, want 200 (body: %s)", period, rec.Code, rec.Body.String())
		}
		_, raw := cacheVisibility(t, rec, "period="+period)
		age, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("period=%s: max-age=%q is not an integer: %v", period, raw, err)
		}
		ages[period] = age
	}

	for i := 1; i < len(ordered); i++ {
		prev, cur := ordered[i-1], ordered[i]
		if ages[cur] <= ages[prev] {
			t.Errorf("max-age for period=%s is %d, not greater than period=%s's %d: a longer "+
				"window must be cacheable for longer, or its repeats go to the database at "+
				"the same rate as a live chart's", cur, ages[cur], prev, ages[prev])
		}
	}

	// And the specific pair the finding is about, stated outright so a failure
	// names the endpoint that actually costs money.
	if ages["1y"] <= ages["24h"] {
		t.Errorf("period=1y max-age is %d and period=24h is %d: the most expensive query "+
			"in the service is cached no longer than the cheapest", ages["1y"], ages["24h"])
	}
}

// TestSeriesRepeatsAreBoundedByTheirOwnBucket pins the direct cost bound on the
// two DB-backed routes.
//
// Repeats are free to the breadth counter by design — that is what makes "reads
// one city all day" indistinguishable from one request — so before this bucket
// existed the only limit on replaying ?period=1y was the global 10 rps, i.e.
// 10 PostgreSQL queries per second per client, forever. The same request must
// eventually be refused, and refused BEFORE the query, not after.
func TestSeriesRepeatsAreBoundedByTheirOwnBucket(t *testing.T) {
	d := withPoints(t, samplePoints())
	// One router, so one bucket persists across the whole loop.
	h := router(t, d)

	const path = "/api/v1/sensor/42/series?metric=P2&period=1y"
	refusedAt := 0
	// Comfortably more than the burst; 1 rps refill cannot mask it at this speed.
	for i := 1; i <= 40; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, get(path, "203.0.113.150"))
		if rec.Code == http.StatusTooManyRequests {
			refusedAt = i
			break
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 or 429 (body: %s)", i, rec.Code, rec.Body.String())
		}
	}

	if refusedAt == 0 {
		t.Fatal("40 identical ?period=1y requests were all served: the heaviest query in " +
			"the service has no volume bound, because the breadth counter treats repeats as free")
	}
	if refusedAt <= 1 {
		t.Errorf("refused on request %d; the first request must always be served — a series "+
			"endpoint that 429s a fresh client is broken, not protected", refusedAt)
	}
}

// TestNilSeriesLimiterStillFailsClosed covers the substitution path in NewRouter.
//
// A nil SeriesLimiter must not mean "unlimited" — that would make the fail-closed
// default a hole rather than a default — and it must not mean an un-swept limiter
// either, which is why the substitute is the shared, evicted instance rather than
// a fresh one per call.
func TestNilSeriesLimiterStillFailsClosed(t *testing.T) {
	d := withPoints(t, samplePoints())
	d.SeriesLimiter = nil // the case under test

	h := router(t, d)

	// A client key used by no other test, because the substituted limiter is
	// process-wide and shared.
	const clientIP = "198.18.7.7"
	const path = "/api/v1/sensor/42/series?metric=P2&period=1y"

	refused := false
	for i := 1; i <= 60; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, get(path, clientIP))
		if rec.Code == http.StatusTooManyRequests {
			refused = true
			break
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 or 429 (body: %s)", i, rec.Code, rec.Body.String())
		}
	}
	if !refused {
		t.Error("a router built with a nil SeriesLimiter served 60 identical ?period=1y " +
			"requests: the fail-closed default is not limiting anything")
	}
}

// TestSeriesRefusalIsCounted pins that a series 429 reaches the metrics.
//
// Refusals on the heaviest path are exactly what an operator needs to see under
// attack, and the global airbg_http_rate_limited_total cannot show them — it is
// incremented outside the mux by a different bucket. Counted in DELTA, because
// internal/metrics registers process-global counters shared by every test in the
// binary; an absolute count would depend on test order.
func TestSeriesRefusalIsCounted(t *testing.T) {
	before := api.SeriesRateLimitedCountForTesting("sensor")

	d := withPoints(t, samplePoints())
	h := router(t, d)

	const path = "/api/v1/sensor/42/series?metric=P2&period=1y"
	var refusals int64
	for i := 1; i <= 40; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, get(path, "203.0.113.160"))
		if rec.Code == http.StatusTooManyRequests {
			refusals++
		}
	}
	if refusals == 0 {
		t.Fatal("no request was refused, so there is nothing to have counted")
	}

	if got := api.SeriesRateLimitedCountForTesting("sensor") - before; got != refusals {
		t.Errorf("airbg_series_rate_limited_total{dimension=\"sensor\"} rose by %d, want %d: "+
			"a refusal on the database-backed path that no counter records is invisible to "+
			"the operator precisely when it matters", got, refusals)
	}
}

// newTestRouter builds a router over a stub store and a snapshot, reusing the
// package's existing deps/router helpers rather than a parallel construction.
//
// Delegates to newTestRouterWithAdmission with a nil semaphore so the two
// constructions cannot drift: a test that needs an explicit admission cap and
// one that does not are otherwise easy to accidentally diverge.
func newTestRouter(t *testing.T, stub *stubSource, snap *snapshot.Snapshot) http.Handler {
	t.Helper()
	return newTestRouterWithAdmission(t, stub, snap, nil)
}

// newTestRouterWithAdmission is newTestRouter with an explicit admission
// semaphore, for tests that need to control the database admission cap
// directly (a nil Admission would make NewRouter substitute the process-wide
// default, sized 16, which these tests would then have to outrun).
func newTestRouterWithAdmission(t *testing.T, stub *stubSource, snap *snapshot.Snapshot, sem *admit.Semaphore) http.Handler {
	t.Helper()
	d := deps(t, snap)
	d.Store = stub
	d.Admission = sem
	return router(t, d)
}

// newSeriesRequest builds a GET request carrying a client IP, reusing the
// package's existing get() helper. The IP is fixed and distinct from the ones
// used elsewhere in this file: these tests build a fresh router (and so a
// fresh breadth counter and rate limiter) per call, but a shared, unusual IP
// keeps them from ever coincidentally colliding with another test's bucket.
func newSeriesRequest(path string) *http.Request {
	return get(path, "203.0.113.200")
}

// snapshotWithAreaSeries builds on fixture's minimal snapshot, replacing
// KnownSlugs and AreaSeries with one entry per given slug.
func snapshotWithAreaSeries(t *testing.T, slugs ...string) *snapshot.Snapshot {
	t.Helper()
	snap := fixture(t)
	snap.KnownSlugs = make(map[string]snapshot.AreaMeta, len(slugs))
	snap.AreaSeries = make(map[string]snapshot.Body, len(slugs))
	for _, slug := range slugs {
		snap.KnownSlugs[slug] = snapshot.AreaMeta{
			Slug: slug, Kind: "oblast", NameBG: slug, NameEN: slug,
			CentroidLon: 23.32, CentroidLat: 42.69, DefaultZoom: 9, Covered: true, SensorCount: 5,
		}
		snap.AreaSeries[slug] = snapshot.Body{
			JSON: []byte(`{"slug":"` + slug + `","metric":"P2","period":"24h","hourly":false,"t":[],"v":[]}`),
			Gzip: []byte("gzipped-series-" + slug),
			ETag: `"series-` + slug + `"`,
		}
	}
	return snap
}

// manySlugs returns n distinct, deterministic area slugs.
func manySlugs(t *testing.T, n int) []string {
	t.Helper()
	slugs := make([]string, n)
	for i := range slugs {
		slugs[i] = fmt.Sprintf("area-%d", i)
	}
	return slugs
}

// breadthAreaLimitForTesting exposes the breadth area limit under the name
// this file's tests expect, read from the committed configuration so it
// cannot drift from what deps(t, ...) actually wires into the router.
func breadthAreaLimitForTesting(t *testing.T) int {
	t.Helper()
	return testConfig(t).RateLimit.Enumerate.AreasPerWindow
}

// TestDefaultAreaSeriesIsServedFromTheSnapshot is the whole point of the change:
// the combination the frontend requests on every area page view must cost zero
// database queries.
func TestDefaultAreaSeriesIsServedFromTheSnapshot(t *testing.T) {
	stub := &stubSource{}
	srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, "sofia"))

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=24h"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if stub.areaSeriesCalls != 0 {
		t.Errorf("AreaSeries called %d times, want 0 — the request reached the database", stub.areaSeriesCalls)
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Error("no ETag; the snapshot path must serve the prepared body, not a re-marshalled one")
	}
	// private, not public: a series response is keyed by slug, so it is
	// enumerable and must never sit in a shared cache the breadth counter
	// cannot see.
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=150" {
		t.Errorf("Cache-Control = %q, want \"private, max-age=150\"", got)
	}
}

// TestNonDefaultAreaSeriesFallsThroughToTheDatabase. Only one combination is
// precomputed; every other one must still work, or the metric and period
// selectors in 3b have nothing to call.
func TestNonDefaultAreaSeriesFallsThroughToTheDatabase(t *testing.T) {
	for _, q := range []string{"metric=P1&period=24h", "metric=P2&period=7d"} {
		t.Run(q, func(t *testing.T) {
			stub := &stubSource{}
			srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, "sofia"))

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?"+q))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
			}
			if stub.areaSeriesCalls != 1 {
				t.Errorf("AreaSeries called %d times, want 1", stub.areaSeriesCalls)
			}
		})
	}
}

// TestSnapshotSeriesSpendsNoSeriesToken. The series bucket is 1 rps / burst 10
// and exists to protect Postgres. A request that issues no query must not spend
// from it, or the frontend's own page views would exhaust the budget that is
// there to bound the expensive path.
func TestSnapshotSeriesSpendsNoSeriesToken(t *testing.T) {
	stub := &stubSource{}
	srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, "sofia"))

	before := api.SeriesRateLimitedCountForTesting("area")
	// Comfortably more than the burst of 10.
	for i := 0; i < 40; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=24h"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 — the snapshot path is spending tokens", i, rec.Code)
		}
	}
	// Delta, not absolute: the counter is process-global.
	if got := api.SeriesRateLimitedCountForTesting("area") - before; got != 0 {
		t.Errorf("series refusals = %d, want 0", got)
	}
}

// TestSnapshotSeriesIsStillCountedForBreadth. The response is per-entity and
// enumerable whether it came from memory or from Postgres. If the fast path
// skipped ObserveArea, a scraper could walk every slug's history for free —
// which is precisely the extraction the tiering design exists to prevent.
func TestSnapshotSeriesIsStillCountedForBreadth(t *testing.T) {
	stub := &stubSource{}
	slugs := manySlugs(t, breadthAreaLimitForTesting(t)+5) // more distinct slugs than the limit allows
	srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, slugs...))

	var refused bool
	for _, slug := range slugs {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/"+slug+"/series?metric=P2&period=24h"))
		if rec.Code == http.StatusTooManyRequests {
			refused = true
			break
		}
	}
	if !refused {
		t.Error("walked more distinct slugs than the breadth limit allows without a single refusal")
	}
}

// TestDefaultSeriesPeriodMatchesParsePeriod ties config.Series.DefaultWindow —
// the window Build derives its `since` from — to the api package's period
// vocabulary, which the handler derives its own window from via parsePeriod.
// If those two ever disagree, the snapshot serves a window that is not the one
// the label claims, and nothing else in the suite would notice. Both sides now
// come from the same config.Config, so config.Validate is what actually
// enforces this — this test pins that enforcement rather than a hardcoded
// literal.
func TestDefaultSeriesPeriodMatchesParsePeriod(t *testing.T) {
	cfg := testConfig(t)
	window, hourly, ok := api.ParsePeriodForTesting(cfg.Series, snapshot.DefaultSeriesPeriod)
	if !ok {
		t.Fatalf("parsePeriod(%q) rejected the snapshot's default period", snapshot.DefaultSeriesPeriod)
	}
	if window != cfg.Series.DefaultWindow {
		t.Errorf("window = %v, want config.Series.DefaultWindow = %v", window, cfg.Series.DefaultWindow)
	}
	if hourly {
		t.Error("hourly = true, but Build precomputes the raw series (hourly=false)")
	}
}

// TestSeriesRefusesWhenAdmissionIsFull. The status is 503 with Retry-After, not
// 429: the client did nothing wrong and its own limit is not the thing that was
// exceeded. Telling it "too many requests" would be a lie, and a client that
// backs off per-client when the server is globally saturated backs off wrongly.
func TestSeriesRefusesWhenAdmissionIsFull(t *testing.T) {
	sem, err := admit.New(1)
	if err != nil {
		t.Fatalf("admit.New: %v", err)
	}
	// Occupy the only slot for the duration of the request. Deterministic: no
	// sleep and no race, the handler either finds a slot or it does not.
	release, ok := sem.TryAcquire()
	if !ok {
		t.Fatal("could not occupy the semaphore")
	}
	defer release()

	stub := &stubSource{}
	srv := newTestRouterWithAdmission(t, stub, snapshotWithAreaSeries(t, "sofia"), sem)

	before := api.AdmissionRejectedCountForTesting("area_series")
	rec := httptest.NewRecorder()
	// A period the snapshot does not precompute, so the request really wants
	// the database.
	srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=7d"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	// Pinned to the configured value, not a literal, so this fails if the
	// admission-full Retry-After is ever wired to a different bucket's config
	// (e.g. RateLimit.Enumerate instead of RateLimit.Series).
	wantRetry := strconv.Itoa(int(testConfig(t).RateLimit.Series.RetryAfter.Seconds()))
	if got := rec.Header().Get("Retry-After"); got != wantRetry {
		t.Errorf("Retry-After = %q, want %q (config.RateLimit.Series.RetryAfter)", got, wantRetry)
	}
	if stub.areaSeriesCalls != 0 {
		t.Errorf("AreaSeries called %d times, want 0 — a refused request must cost no database work", stub.areaSeriesCalls)
	}
	if got := api.AdmissionRejectedCountForTesting("area_series") - before; got != 1 {
		t.Errorf("admission refusals = %d, want 1", got)
	}
}

// TestSeriesReleasesItsSlot. Without this the cap is a one-way ratchet: the
// service would work for exactly `size` requests and refuse everything after,
// which is a failure mode that only appears in production and looks like a
// database outage.
func TestSeriesReleasesItsSlot(t *testing.T) {
	sem, err := admit.New(1)
	if err != nil {
		t.Fatalf("admit.New: %v", err)
	}
	stub := &stubSource{}
	srv := newTestRouterWithAdmission(t, stub, snapshotWithAreaSeries(t, "sofia"), sem)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=7d"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 — the slot was not released", i, rec.Code)
		}
	}
	if got := sem.InFlight(); got != 0 {
		t.Errorf("InFlight = %d after three completed requests, want 0", got)
	}
}

// TestSnapshotSeriesDoesNotConsumeAdmission. The snapshot path issues no query,
// so it must not compete for a slot sized against the database.
func TestSnapshotSeriesDoesNotConsumeAdmission(t *testing.T) {
	sem, err := admit.New(1)
	if err != nil {
		t.Fatalf("admit.New: %v", err)
	}
	release, ok := sem.TryAcquire()
	if !ok {
		t.Fatal("could not occupy the semaphore")
	}
	defer release()

	srv := newTestRouterWithAdmission(t, &stubSource{}, snapshotWithAreaSeries(t, "sofia"), sem)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=24h"))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — a memory-served response was refused by a database cap", rec.Code)
	}
}
