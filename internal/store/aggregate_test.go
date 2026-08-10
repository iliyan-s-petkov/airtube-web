package store_test

import (
	"context"
	"testing"
	"time"

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
	s := store.New(pool)

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
		t.Errorf("two-sensors Covered = true with 2 sensors; CoverageThreshold is %d", store.CoverageThreshold)
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
}

// TestAreaAggregatesExcludesFlaggedReadings asserts the quality filter. Written
// so it fails if the filter is dropped: the flagged value is far enough from
// the good ones that including it moves the mean well outside tolerance.
func TestAreaAggregatesExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

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
	s := store.New(pool)

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
	s := store.New(pool)

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
	s := store.New(pool)

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
}
