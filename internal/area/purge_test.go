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

	result, err := area.PurgeOutsideBoundary(ctx, pool)
	if err != nil {
		t.Fatalf("PurgeOutsideBoundary: %v", err)
	}
	if result.SensorsRemoved != 1 {
		t.Errorf("SensorsRemoved = %d, want 1", result.SensorsRemoved)
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

// TestPurgeOutsideBoundaryReachesOrphanedReadings is the reachability
// regression test. `reading.sensor_id` has no foreign key to
// `sensor(sensor_id)`, so rows can exist for a sensor_id with no `sensor` row
// at all — which is exactly what `airbg backfill <sensor_id> <csv>` produced
// before it gained a boundary check. A purge that discovers rows to delete by
// selecting from `sensor` cannot see them, so the one command documented as the
// cleanup for foreign data provably could not reach backfilled foreign data.
//
// Sensor 1 (in Sofia, with readings) must survive: the orphan sweep keys on the
// absence of a `sensor` row, not on anything about the readings themselves, and
// must not become a blanket delete.
func TestPurgeOutsideBoundaryReachesOrphanedReadings(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`); err != nil {
		t.Fatalf("insert sensor 1: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES (now(), 1, 'P1', 15, 'ok')`); err != nil {
		t.Fatalf("insert reading for sensor 1: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES (date_trunc('hour', now()), 1, 'P1', 15, 15, 15, 1)`); err != nil {
		t.Fatalf("insert reading_hourly for sensor 1: %v", err)
	}

	// Sensor 999 has no `sensor` row — the shape a pre-check backfill left
	// behind. Its location, and therefore whether it is Bulgarian, is
	// unknowable from the database.
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES (now(), 999, 'P1', 15, 'ok')`); err != nil {
		t.Fatalf("insert orphan reading: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES (date_trunc('hour', now()), 999, 'P1', 15, 15, 15, 1)`); err != nil {
		t.Fatalf("insert orphan reading_hourly: %v", err)
	}

	result, err := area.PurgeOutsideBoundary(ctx, pool)
	if err != nil {
		t.Fatalf("PurgeOutsideBoundary: %v", err)
	}
	if result.OrphanRawRows != 1 {
		t.Errorf("OrphanRawRows = %d, want 1", result.OrphanRawRows)
	}
	if result.OrphanHourlyRows != 1 {
		t.Errorf("OrphanHourlyRows = %d, want 1", result.OrphanHourlyRows)
	}

	var orphanRaw, orphanHourly int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading WHERE sensor_id = 999`).Scan(&orphanRaw); err != nil {
		t.Fatalf("count orphan reading: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly WHERE sensor_id = 999`).Scan(&orphanHourly); err != nil {
		t.Fatalf("count orphan reading_hourly: %v", err)
	}
	if orphanRaw != 0 || orphanHourly != 0 {
		t.Errorf("orphan rows left behind: reading=%d reading_hourly=%d, want 0 — the documented cleanup cannot reach them any other way",
			orphanRaw, orphanHourly)
	}

	var keptRaw, keptHourly int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading WHERE sensor_id = 1`).Scan(&keptRaw); err != nil {
		t.Fatalf("count kept reading: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly WHERE sensor_id = 1`).Scan(&keptHourly); err != nil {
		t.Fatalf("count kept reading_hourly: %v", err)
	}
	if keptRaw != 1 || keptHourly != 1 {
		t.Errorf("readings for the in-boundary sensor were deleted: reading=%d reading_hourly=%d, want 1 and 1",
			keptRaw, keptHourly)
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

	result, err := area.PurgeOutsideBoundary(ctx, pool)
	if err == nil {
		t.Fatalf("PurgeOutsideBoundary returned no error with no country boundary imported (%+v) — it must refuse to run, not treat absence as \"nothing qualifies\"", result)
	}
	if result.SensorsRemoved != 0 {
		t.Errorf("SensorsRemoved = %d, want 0 on refusal", result.SensorsRemoved)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&n); err != nil {
		t.Fatalf("count sensors: %v", err)
	}
	if n != 1 {
		t.Errorf("sensor rows = %d, want 1 — refusal must leave existing data untouched", n)
	}
}
