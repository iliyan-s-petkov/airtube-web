package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
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
