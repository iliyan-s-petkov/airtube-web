package db_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"airbg.org/internal/db/migrations"
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
		 VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5)`,
		int64(1), "test", 23.3, 42.7, "made-up")
	if err == nil {
		t.Error("sensor.source accepted an unknown source")
	}
}

// TestMigration00011DownGuardBlocksBeforeDroppingColumns runs the actual
// goose Down migration (not just the extracted guard statement) against a
// database carrying a 'source_invalid' reading. 00011 is NO TRANSACTION, so
// each Down statement commits independently; the guard must be the first
// statement, or a rollback would already have dropped sensor.source (the
// only column distinguishing an EEA row from a community row) by the time
// the guard raises. This proves the reorder, not just that a guard exists.
func TestMigration00011DownGuardBlocksBeforeDroppingColumns(t *testing.T) {
	ctx, pool := migrated(t)

	mustInsertSensor(t, ctx, pool, 1)
	_, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, $5)`,
		time.Now().UTC(), int64(1), "P1", 24.3, "source_invalid")
	if err != nil {
		t.Fatalf("insert source_invalid reading: %v", err)
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	err = goose.DownContext(ctx, sqlDB, ".")
	if err == nil {
		t.Fatal("Down succeeded with a source_invalid reading present; the guard did not run")
	}
	for _, want := range []string{"00011", "source_invalid", "UPDATE reading"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("guard error %q does not mention %q", err, want)
		}
	}

	// The guard firing first must mean none of the destructive DDL after it
	// ran. If sensor.source were gone here, the guard fired too late to help
	// an operator — the data it exists to protect would already be lost.
	var def string
	if err := pool.QueryRow(ctx,
		`SELECT column_default FROM information_schema.columns
		 WHERE table_name = 'sensor' AND column_name = 'source'`).Scan(&def); err != nil {
		t.Fatalf("sensor.source was dropped despite the guard raising: %v", err)
	}
}
