package store_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/db"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
)

// The two type aliases keep the helper signatures short; contextT and poolT are
// declared once here rather than repeating the full types in every helper.
type contextT = context.Context
type poolT = *pgxpool.Pool

// seedArea inserts one area with a square polygon around the given centre.
// Parameterised, like every query in this project — no string concatenation,
// including in test helpers.
func seedArea(t *testing.T, ctx contextT, pool poolT, slug, kind string, lon, lat float64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ($1, $2, $1, $1,
		         ST_Buffer(ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, 5000)::geography)`,
		slug, kind, lon, lat)
	if err != nil {
		t.Fatalf("seed area %s: %v", slug, err)
	}
}

func seedSensor(t *testing.T, ctx contextT, pool poolT, id int64, lon, lat float64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, 'TEST', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)
		 ON CONFLICT (sensor_id) DO NOTHING`,
		id, lon, lat)
	if err != nil {
		t.Fatalf("seed sensor %d: %v", id, err)
	}
}

func seedSensorReading(t *testing.T, ctx contextT, pool poolT, id int64, lon, lat float64, metric string, value float64, quality string, at time.Time) {
	t.Helper()
	seedSensor(t, ctx, pool, id, lon, lat)
	_, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, $5::quality_flag)
		 ON CONFLICT (sensor_id, metric, time) DO UPDATE
		   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
		at, id, metric, value, quality)
	if err != nil {
		t.Fatalf("seed reading %d/%s: %v", id, metric, err)
	}
}

func assignAreas(t *testing.T, ctx contextT, pool poolT) {
	t.Helper()
	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}
}

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool
}

// TestAreaAggregatesRespectsCoverageThreshold is the central test of this task.
// Two sensors must NOT produce a published average, three must. Phase 1 §5.7:
// below the threshold, deeper tiers manufacture confident-looking averages from
// single sensors.
func TestAreaAggregatesRespectsCoverageThreshold(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	seedArea(t, ctx, pool, "two-sensors", "oblast", 23.0, 42.0)
	seedArea(t, ctx, pool, "three-sensors", "oblast", 25.0, 43.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 2, 23.001, 42.001, "P2", 20, "ok", now)

	seedSensorReading(t, ctx, pool, 3, 25.0, 43.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 4, 25.001, 43.001, "P2", 20, "ok", now)
	seedSensorReading(t, ctx, pool, 5, 25.002, 43.002, "P2", 30, "ok", now)

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	byslug := map[string]store.AreaAggregate{}
	for _, a := range aggs {
		byslug[a.Slug] = a
	}

	two, ok := byslug["two-sensors"]
	if !ok {
		t.Fatal("two-sensors area missing from aggregates; an under-covered area must still be listed, so the map can render its insufficient-coverage state")
	}
	if two.Covered {
		t.Errorf("two-sensors Covered = true with 2 sensors; CoverageThreshold is %d", testStoreConfig().CoverageThreshold)
	}
	if len(two.Values) != 0 {
		t.Errorf("two-sensors published values %v; an uncovered area must publish no number at all", two.Values)
	}

	three, ok := byslug["three-sensors"]
	if !ok {
		t.Fatal("three-sensors area missing from aggregates")
	}
	if !three.Covered {
		t.Error("three-sensors Covered = false with 3 sensors; the threshold is a minimum, not an exclusive bound")
	}
	if got := three.Values["P2"]; got < 19.9 || got > 20.1 {
		t.Errorf("three-sensors P2 = %v, want 20 (mean of 10, 20, 30)", got)
	}

	// Bulgaria's lon range (22-29) and lat range (41-45) do not overlap, so
	// checking each coordinate against its OWN range — never as a combined
	// pair or distance tolerance — is what catches a lon/lat swap. A swapped
	// pair here would put CentroidLon around 43 (outside 22-29) and
	// CentroidLat around 25 (outside 41-45); both checks below would fail.
	assertInBulgaria(t, "two-sensors centroid", two.CentroidLon, two.CentroidLat)
	assertInBulgaria(t, "three-sensors centroid", three.CentroidLon, three.CentroidLat)
}

// assertInBulgaria checks lon and lat against their OWN ranges separately.
// Bulgaria spans lon 22-29, lat 41-45 — ranges that do not overlap, so a swap
// (lon and lat transposed) is detectable only by checking each axis on its own
// range, never as a combined pair or distance-from-expected tolerance.
func assertInBulgaria(t *testing.T, label string, lon, lat float64) {
	t.Helper()
	if lon < 22 || lon > 29 {
		t.Errorf("%s: lon = %v, want [22, 29]; a value in the lat range (41-45) means lon/lat are swapped", label, lon)
	}
	if lat < 41 || lat > 45 {
		t.Errorf("%s: lat = %v, want [41, 45]; a value in the lon range (22-29) means lon/lat are swapped", label, lat)
	}
}

// TestAreaAggregatesCountsDistinctSensorsNotRows pins "three readings from one
// sensor is not coverage" (brief, Phase 1 §5.7) directly: one sensor reporting
// several metrics must count as ONE sensor toward the threshold, not one per
// row. If count(DISTINCT sensor_id) were ever "optimised" to count(*), this
// area would wrongly cross CoverageThreshold on a single sensor.
func TestAreaAggregatesCountsDistinctSensorsNotRows(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	seedArea(t, ctx, pool, "one-sensor-many-readings", "oblast", 23.0, 42.0)

	now := time.Now().UTC().Truncate(time.Minute)
	for _, m := range []struct {
		name string
		v    float64
	}{
		{"P1", 30}, {"P2", 18}, {"temperature", 21}, {"humidity", 55}, {"pressure", 1013},
	} {
		seedSensorReading(t, ctx, pool, 40, 23.0, 42.0, m.name, m.v, "ok", now)
	}

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(aggs))
	}
	if aggs[0].SensorCount != 1 {
		t.Errorf("SensorCount = %d, want 1 (5 readings from one sensor is not 5 sensors)", aggs[0].SensorCount)
	}
	if aggs[0].Covered {
		t.Error("Covered = true with 1 sensor reporting 5 metrics; a single sensor cannot cross CoverageThreshold no matter how many metrics it reports")
	}
	if len(aggs[0].Values) != 0 {
		t.Errorf("published values %v; an uncovered area must publish no number at all", aggs[0].Values)
	}
}

// TestAreaAggregatesExcludesStaleReadings pins freshnessWindow: a reading
// older than the window must not count toward SensorCount or influence
// Values, even though the sensor otherwise looks perfectly healthy.
func TestAreaAggregatesExcludesStaleReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	seedArea(t, ctx, pool, "stale-mix", "oblast", 23.0, 42.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 50, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 51, 23.001, 42.001, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 52, 23.002, 42.002, "P2", 10, "ok", now)
	// Stale: outside freshnessWindow (2h), and far enough from 10 that if it
	// were counted, both SensorCount (4, not 3) and the mean would be wrong.
	seedSensorReading(t, ctx, pool, 53, 23.003, 42.003, "P2", 1000, "ok", now.Add(-3*time.Hour))

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(aggs))
	}
	if aggs[0].SensorCount != 3 {
		t.Errorf("SensorCount = %d, want 3; the stale sensor (reading is 3h old) must not count", aggs[0].SensorCount)
	}
	if got := aggs[0].Values["P2"]; got < 9.9 || got > 10.1 {
		t.Errorf("P2 = %v, want 10; a value near 257 means the stale reading was counted", got)
	}
}

// TestLatestSensorsExcludesStaleReadings pins the same freshnessWindow rule
// for LatestSensors: a sensor whose only reading is stale must not appear at
// all in the result.
func TestLatestSensorsExcludesStaleReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 60, 23.0, 42.0, "P2", 15, "ok", now.Add(-3*time.Hour))

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	for _, sr := range sensors {
		if sr.SensorID == 60 {
			t.Fatalf("sensor 60 present with only a 3h-old reading; freshnessWindow (2h) must exclude it: %+v", sr)
		}
	}
}

// TestAreaAggregatesExcludesFlaggedReadings asserts the quality filter. Written
// so it fails if the filter is dropped: the flagged value is far enough from
// the good ones that including it moves the mean well outside tolerance.
func TestAreaAggregatesExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	seedArea(t, ctx, pool, "flagged", "oblast", 23.0, 42.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 10, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 11, 23.001, 42.001, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 12, 23.002, 42.002, "P2", 10, "no_neighbours", now)
	// A stuck sensor reporting 1000: if the filter is missing, the mean jumps
	// from 10 to ~257 and the assertion below fails loudly.
	seedSensorReading(t, ctx, pool, 13, 23.003, 42.003, "P2", 1000, "stuck", now)

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(aggs))
	}
	// 'ok' and 'no_neighbours' are usable, 'stuck' is not — three usable
	// sensors, all reading 10.
	if got := aggs[0].Values["P2"]; got < 9.9 || got > 10.1 {
		t.Errorf("P2 = %v, want 10; a value near 257 means the quality filter is missing", got)
	}
	if aggs[0].SensorCount != 3 {
		t.Errorf("SensorCount = %d, want 3 (the stuck sensor must not count toward coverage either)", aggs[0].SensorCount)
	}
}

// TestSensorSeriesUsesRawBelowThirtyDays and its hourly counterpart pin the
// table-selection rule from Phase 1 §7.2. Getting it backwards is silent: both
// tables answer the query, one just returns nothing useful.
func TestSensorSeriesUsesRawBelowThirtyDays(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 20, 23.0, 42.0, "P2", 12, "ok", now.Add(-2*time.Hour))

	// A DIFFERENT value in reading_hourly for the same sensor and hour. If
	// SensorSeries reads the wrong table, it returns 99 and this fails.
	_, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES ($1, 20, 'P2', 99, 99, 99, 1)`,
		now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("seed hourly: %v", err)
	}

	pts, err := s.SensorSeries(ctx, 20, "P2", now.Add(-24*time.Hour), false)
	if err != nil {
		t.Fatalf("SensorSeries: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	if pts[0].Value != 12 {
		t.Errorf("value = %v, want 12; 99 means it read reading_hourly instead of reading", pts[0].Value)
	}
}

func TestSensorSeriesUsesHourlyAboveThirtyDays(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	now := time.Now().UTC().Truncate(time.Hour)
	bucket := now.Add(-60 * 24 * time.Hour)

	seedSensor(t, ctx, pool, 21, 23.0, 42.0)
	_, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES ($1, 21, 'P2', 42, 40, 44, 6)`,
		bucket)
	if err != nil {
		t.Fatalf("seed hourly: %v", err)
	}

	pts, err := s.SensorSeries(ctx, 21, "P2", bucket.Add(-time.Hour), true)
	if err != nil {
		t.Fatalf("SensorSeries: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	if pts[0].Value != 42 {
		t.Errorf("value = %v, want 42", pts[0].Value)
	}
}

// TestLatestSensorsReturnsOneRowPerSensor guards against the classic
// join-fanout bug: seven metrics per sensor must produce one SensorReading with
// seven values, not seven SensorReadings.
func TestLatestSensorsReturnsOneRowPerSensor(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	now := time.Now().UTC().Truncate(time.Minute)
	for _, m := range []struct {
		name string
		v    float64
	}{
		{"P1", 30}, {"P2", 18}, {"temperature", 21}, {"humidity", 55},
	} {
		seedSensorReading(t, ctx, pool, 30, 23.0, 42.0, m.name, m.v, "ok", now)
	}

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	if len(sensors) != 1 {
		t.Fatalf("got %d sensors, want 1 (4 metrics on one sensor must not fan out into 4 rows)", len(sensors))
	}
	if len(sensors[0].Values) != 4 {
		t.Errorf("got %d values, want 4: %v", len(sensors[0].Values), sensors[0].Values)
	}
	if got := sensors[0].Values["P2"]; got != 18 {
		t.Errorf("P2 = %v, want 18", got)
	}
	assertInBulgaria(t, "sensor 30", sensors[0].Lon, sensors[0].Lat)
}

// TestAreaSeriesAveragesAcrossSensors: the area series is the mean of the
// sensors in the area at each instant, not a concatenation of their readings.
// Concatenating would produce a sawtooth that looks like violent air-quality
// swings but is really just sensors disagreeing — the most misleading possible
// chart to publish under a public-health banner.
func TestAreaSeriesAveragesAcrossSensors(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	for i, v := range []float64{10, 30} {
		id := int64(700 + i)
		lon := 23.3219 + float64(i)*0.001
		seedSensorReading(t, ctx, pool, id, lon, 42.6977, "P2", v, "ok", base)
	}
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 (two sensors at one instant must average into one point)", len(points))
	}
	if points[0].Value != 20 {
		t.Errorf("value = %v, want 20 (the mean of 10 and 30)", points[0].Value)
	}
}

// TestAreaSeriesExcludesFlaggedReadings: the same quality filter the aggregates
// use must apply here, or a chart shows values the map refuses to.
func TestAreaSeriesExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 800, 23.3219, 42.6977, "P2", 10, "ok", base)
	// A stuck sensor pegged at 1000. If the filter is dropped the mean becomes
	// 505 rather than 10 — a 50x error, impossible to miss.
	seedSensorReading(t, ctx, pool, 801, 23.3229, 42.6977, "P2", 1000, "stuck", base)
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 10 {
		t.Errorf("value = %v, want 10; a flagged reading was included", points[0].Value)
	}
}

// seedAreaSeriesFixture seeds two sensors in one area and one sensor in
// another, plus an out-of-range reading in the first area, so the parity test
// exercises both the (slug, time) grouping and the quality filter rather than
// assuming either.
func seedAreaSeriesFixture(t *testing.T, ctx contextT, pool poolT) {
	t.Helper()
	seedArea(t, ctx, pool, "series-a", "oblast", 23.0, 42.0)
	seedArea(t, ctx, pool, "series-b", "oblast", 25.0, 43.0)

	base := time.Now().UTC().Truncate(time.Minute)
	// Two sensors in series-a reporting at the SAME instant.
	seedSensorReading(t, ctx, pool, 200, 23.0, 42.0, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 201, 23.001, 42.001, "P2", 20, "ok", base)
	// A flagged reading in series-a. If the quality filter were dropped, this
	// would move AllAreaSeries's mean away from AreaSeries's, and the parity
	// test would fail on the point value rather than passing by accident
	// because the fixture had nothing bad in it.
	seedSensorReading(t, ctx, pool, 203, 23.002, 42.002, "P2", 999, "out_of_range", base)
	// One sensor in series-b.
	seedSensorReading(t, ctx, pool, 202, 25.0, 43.0, "P2", 30, "ok", base)

	assignAreas(t, ctx, pool)
}

// seedTwoSensorsOneInstant seeds one area with two sensors reporting the given
// values at the same instant, and returns the area's slug and that instant.
func seedTwoSensorsOneInstant(t *testing.T, ctx contextT, pool poolT, v1, v2 float64) (string, time.Time) {
	t.Helper()
	const slug = "one-instant"
	seedArea(t, ctx, pool, slug, "oblast", 24.0, 42.5)
	at := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 300, 24.0, 42.5, "P2", v1, "ok", at)
	seedSensorReading(t, ctx, pool, 301, 24.001, 42.501, "P2", v2, "ok", at)
	assignAreas(t, ctx, pool)
	return slug, at
}

// TestAllAreaSeriesMatchesThePerAreaQuery is the anti-drift test. AllAreaSeries
// and AreaSeries are two SQL statements that must produce the same numbers, and
// the snapshot path only serves the right data for as long as they agree. Any
// future edit to one that is not mirrored in the other fails here rather than
// silently shipping a chart that disagrees with the database-backed fall-through.
func TestAllAreaSeriesMatchesThePerAreaQuery(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	// Two sensors in one area, one in another, and two readings at the SAME
	// timestamp so the grouping is exercised rather than assumed.
	seedAreaSeriesFixture(t, ctx, pool)

	since := time.Now().UTC().Add(-24 * time.Hour)
	all, err := s.AllAreaSeries(ctx, "P2", since, false)
	if err != nil {
		t.Fatalf("AllAreaSeries: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("AllAreaSeries returned no areas; the fixture is not being seen, so this test proves nothing")
	}

	for slug, batched := range all {
		single, err := s.AreaSeries(ctx, slug, "P2", since, false)
		if err != nil {
			t.Fatalf("AreaSeries(%q): %v", slug, err)
		}
		if len(batched) != len(single) {
			t.Fatalf("area %q: AllAreaSeries returned %d points, AreaSeries returned %d", slug, len(batched), len(single))
		}
		for i := range single {
			if !batched[i].Time.Equal(single[i].Time) {
				t.Errorf("area %q point %d: time = %v, want %v", slug, i, batched[i].Time, single[i].Time)
			}
			if batched[i].Value != single[i].Value {
				t.Errorf("area %q point %d: value = %v, want %v", slug, i, batched[i].Value, single[i].Value)
			}
		}
	}
}

// TestAllAreaSeriesGroupsSensorsAtTheSameInstant pins the averaging directly.
// Without the (slug, time) grouping the result is every sensor's reading in
// timestamp order, which renders as a sawtooth a reader would interpret as
// rapid air-quality swings rather than as two sensors disagreeing.
func TestAllAreaSeriesGroupsSensorsAtTheSameInstant(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20) // one area, one timestamp, values 10 and 20

	points, err := s.AllAreaSeries(ctx, "P2", at.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AllAreaSeries: %v", err)
	}
	got := points[slug]
	if len(got) != 1 {
		t.Fatalf("got %d points for %q, want 1 — the two sensors were not grouped", len(got), slug)
	}
	if got[0].Value != 15 {
		t.Errorf("value = %v, want 15 (the mean of 10 and 20)", got[0].Value)
	}
}

// TestAreaSeriesExcludesOutOfRangeNaN: deferred item (b) — a faulty sensor can
// report NaN, and strconv.ParseFloat("nan", ...) succeeds while NaN compares
// false against every < and > in a plain range check. The ingest-time
// out_of_range flag (internal/quality) is what actually rejects it — InRange
// returns false for NaN because NaN >= min and NaN <= max are both false — and
// this test pins that AreaSeries' quality filter honours that flag rather than
// re-deriving its own numeric check that a NaN could slip past.
func TestAreaSeriesExcludesOutOfRangeNaN(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	seedArea(t, ctx, pool, "nan-area", "oblast", 23.5, 42.5)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensorReading(t, ctx, pool, 900, 23.5, 42.5, "P2", 10, "ok", base)
	seedSensorReading(t, ctx, pool, 901, 23.501, 42.5, "P2", math.NaN(), "out_of_range", base)
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "nan-area", "P2", base.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 10 {
		t.Errorf("value = %v, want 10; a NaN reading flagged out_of_range leaked into the average", points[0].Value)
	}
}

// TestAreaSeriesTimesOutUnderItsOwnScopedBound drives AreaSeries itself,
// rather than db.SetLocalStatementTimeout and pg_sleep in isolation. An
// earlier version of this test (TestSeriesQueriesUseTheShortStatementTimeout)
// called the db helper and ran an ad-hoc pg_sleep directly, never AreaSeries
// or SensorSeries — so it could not have caught a missing timeout call in
// either function, which is exactly the mutation review found inert.
//
// A second session locks the reading table ACCESS EXCLUSIVE and holds it open
// for the whole test, so AreaSeries' own query blocks waiting for the lock.
// Its own scoped statement_timeout — not the pool's 15s default — must abort
// that wait at 5s.
func TestAreaSeriesTimesOutUnderItsOwnScopedBound(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	// Two live connections are required: one held open by the blocker's
	// transaction, one for AreaSeries' own Begin. Asserted, not assumed — the
	// pool defaults to more than one, but a future change to that default
	// must not silently turn this into a test that deadlocks on itself.
	if pool.Config().MaxConns < 2 {
		t.Fatalf("pool MaxConns = %d, want >= 2 so the blocker and AreaSeries use distinct connections", pool.Config().MaxConns)
	}

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20)

	// Another session holds ACCESS EXCLUSIVE on reading; AreaSeries' own
	// statement_timeout must abort it, not the pool's 15s.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker Begin: %v", err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `LOCK TABLE reading IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("LOCK TABLE: %v", err)
	}

	start := time.Now()
	_, err = s.AreaSeries(ctx, slug, "P2", at.Add(-time.Hour), false)
	elapsed := time.Since(start)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("AreaSeries err = %v, want SQLSTATE 57014 (query_canceled)", err)
	}
	// The bound must be the scoped 5s, not the pool's 15s default.
	if elapsed > 10*time.Second {
		t.Errorf("took %v; the pool's 15s bound applied, not the scoped 5s", elapsed)
	}
}

// TestSensorSeriesTimesOutUnderItsOwnScopedBound mirrors the AreaSeries test
// above for SensorSeries: same lock, same assertion, the other database-backed
// series query that got the same scoped-timeout treatment.
func TestSensorSeriesTimesOutUnderItsOwnScopedBound(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	if pool.Config().MaxConns < 2 {
		t.Fatalf("pool MaxConns = %d, want >= 2 so the blocker and SensorSeries use distinct connections", pool.Config().MaxConns)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 950, 23.5, 42.5, "P2", 10, "ok", now)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("blocker Begin: %v", err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `LOCK TABLE reading IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("LOCK TABLE: %v", err)
	}

	start := time.Now()
	_, err = s.SensorSeries(ctx, 950, "P2", now.Add(-time.Hour), false)
	elapsed := time.Since(start)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "57014" {
		t.Fatalf("SensorSeries err = %v, want SQLSTATE 57014 (query_canceled)", err)
	}
	if elapsed > 10*time.Second {
		t.Errorf("took %v; the pool's 15s bound applied, not the scoped 5s", elapsed)
	}
}

// TestAreaSeriesStillReturnsDataInsideItsTransaction. Wrapping a read in a
// transaction is exactly the kind of change that can return an empty result set
// while every timeout test still passes — a chart that is blank rather than
// wrong, which is harder to notice in review than a failure.
func TestAreaSeriesStillReturnsDataInsideItsTransaction(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig())

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20)

	points, err := s.AreaSeries(ctx, slug, "P2", at.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 15 {
		t.Errorf("value = %v, want 15", points[0].Value)
	}
}
