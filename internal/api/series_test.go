package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/store"
)

var errBoom = errors.New("boom: pq: relation \"reading\" does not exist")

func itoa(i int) string { return strconv.Itoa(i) }

func withPoints(t *testing.T, points []store.Point) api.Deps {
	t.Helper()
	d := deps(t, fixture(t))
	d.Store = stubSource{slug: "sofia", points: points}
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
	cases := map[string]bool{"24h": false, "7d": false, "30d": false, "1y": true}
	for period, wantHourly := range cases {
		_, hourly, ok := api.ParsePeriodForTesting(period)
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
	d.Breadth = ratelimit.NewBreadth(100, 3, time.Hour)

	allowed := 0
	for id := 1; id <= 5; id++ {
		rec := serve(t, d, get("/api/v1/sensor/"+itoa(id)+"/series?metric=P2&period=24h", "203.0.113.25"))
		if rec.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("allowed %d distinct sensors, want 3", allowed)
	}
}

// TestSeriesDatabaseErrorIsNotLeaked: the message must be a fixed sentence. A
// pgx error carries the SQL and the table names.
func TestSeriesDatabaseErrorIsNotLeaked(t *testing.T) {
	d := deps(t, fixture(t))
	d.Store = stubSource{err: errBoom}

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
	d.Breadth = ratelimit.NewBreadth(100, 2, time.Hour)

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
