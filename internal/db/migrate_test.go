package db_test

import (
	"strings"
	"testing"
)

// TestSourceColumnsExist asserts migration 00011 added the source-tagging
// columns and the reserved id range for EEA-origin sensors, and that the
// CHECK constraint actually rejects an unknown source (not just that the
// column exists).
func TestSourceColumnsExist(t *testing.T) {
	ctx, pool := migrated(t)

	var def string
	err := pool.QueryRow(ctx,
		`SELECT column_default FROM information_schema.columns
		 WHERE table_name = 'sensor' AND column_name = 'source'`).Scan(&def)
	if err != nil {
		t.Fatalf("sensor.source is missing: %v", err)
	}
	if !strings.Contains(def, "sensor.community") {
		t.Errorf("sensor.source defaults to %q, want the sensor.community default", def)
	}

	// The reserved range must not collide with any community sensor id.
	var start int64
	if err := pool.QueryRow(ctx,
		`SELECT last_value FROM official_sensor_id_seq`).Scan(&start); err != nil {
		t.Fatalf("official_sensor_id_seq is missing: %v", err)
	}
	if start < 9_000_000_000 {
		t.Errorf("official_sensor_id_seq starts at %d, want >= 9000000000", start)
	}

	var ok bool
	if err := pool.QueryRow(ctx,
		`SELECT 'source_invalid' = ANY(enum_range(NULL::quality_flag)::text[])`).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("quality_flag has no source_invalid value")
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location, source)
		 VALUES (1, 'test', ST_SetSRID(ST_MakePoint(23.3, 42.7), 4326)::geography, 'made-up')`)
	if err == nil {
		t.Error("sensor.source accepted an unknown source")
	}
}
