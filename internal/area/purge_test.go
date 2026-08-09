package area_test

import (
	"testing"

	"airbg.org/internal/area"
)

// TestPurgeOutsideBoundaryDeletesSensorsOutsideBoundary is task-17 review
// finding 4's regression test: a sensor stored under the old
// country-field-trusting behaviour (a London sensor, standing in for
// 48524) must be removed by an explicit purge, while a genuinely
// Bulgarian sensor survives untouched.
func TestPurgeOutsideBoundaryDeletesSensorsOutsideBoundary(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Sofia — must survive.
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`); err != nil {
		t.Fatalf("insert sensor 1: %v", err)
	}
	// London (stand-in for 48524) — must be removed.
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (48524, 'SDS011', ST_SetSRID(ST_MakePoint(-0.1276, 51.5074), 4326)::geography)`); err != nil {
		t.Fatalf("insert sensor 48524: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES (now(), 48524, 'P1', 15, 'ok')`); err != nil {
		t.Fatalf("insert reading: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES (date_trunc('hour', now()), 48524, 'P1', 15, 15, 15, 1)`); err != nil {
		t.Fatalf("insert reading_hourly: %v", err)
	}

	removed, err := area.PurgeOutsideBoundary(ctx, pool)
	if err != nil {
		t.Fatalf("PurgeOutsideBoundary: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	var sofiaCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor WHERE sensor_id = 1`).Scan(&sofiaCount); err != nil {
		t.Fatalf("count sofia: %v", err)
	}
	if sofiaCount != 1 {
		t.Errorf("sofia sensor rows = %d, want 1 — a genuinely Bulgarian sensor must survive the purge", sofiaCount)
	}

	var londonSensors, londonReadings, londonHourly int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor WHERE sensor_id = 48524`).Scan(&londonSensors); err != nil {
		t.Fatalf("count 48524 sensor: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading WHERE sensor_id = 48524`).Scan(&londonReadings); err != nil {
		t.Fatalf("count 48524 reading: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly WHERE sensor_id = 48524`).Scan(&londonHourly); err != nil {
		t.Fatalf("count 48524 reading_hourly: %v", err)
	}
	if londonSensors != 0 || londonReadings != 0 || londonHourly != 0 {
		t.Errorf("sensor 48524 rows left behind: sensor=%d reading=%d reading_hourly=%d, want all 0",
			londonSensors, londonReadings, londonHourly)
	}
}

// TestPurgeOutsideBoundaryRefusesWhenBoundaryAbsent is task-17 review
// finding 4's fail-closed regression test: with no country boundary
// imported, "nothing qualifies for deletion" must not be conflated with
// "the purge ran successfully" — it must refuse to run at all.
func TestPurgeOutsideBoundaryRefusesWhenBoundaryAbsent(t *testing.T) {
	ctx, pool := migrated(t)

	// A sensor that would (wrongly) look "clean" if absence were treated as
	// "nothing outside the boundary".
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (48524, 'SDS011', ST_SetSRID(ST_MakePoint(-0.1276, 51.5074), 4326)::geography)`); err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	removed, err := area.PurgeOutsideBoundary(ctx, pool)
	if err == nil {
		t.Fatalf("PurgeOutsideBoundary returned no error with no country boundary imported (removed=%d) — it must refuse to run, not treat absence as \"nothing qualifies\"", removed)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 on refusal", removed)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&n); err != nil {
		t.Fatalf("count sensors: %v", err)
	}
	if n != 1 {
		t.Errorf("sensor rows = %d, want 1 — refusal must leave existing data untouched", n)
	}
}
