package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

// Sofia's Alexander Nevsky Cathedral. Longitude first — PostGIS geography is
// (lon, lat), the inverse of the legacy [lat, long] convention. Swapping these
// silently relocates every Bulgarian sensor into the Indian Ocean, which is why
// this test exists (spec §5, §11.2).
const (
	sofiaLon = 23.3327
	sofiaLat = 42.6957
)

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, pool
}

func TestSensorCoordinateOrder(t *testing.T) {
	ctx, pool := migrated(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography)`,
		int64(1), "SDS011", sofiaLon, sofiaLat)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	var lon, lat float64
	err = pool.QueryRow(ctx,
		`SELECT ST_X(location::geometry), ST_Y(location::geometry)
		 FROM sensor WHERE sensor_id = $1`, int64(1)).Scan(&lon, &lat)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if lon < 22 || lon > 29 {
		t.Errorf("longitude = %v, outside Bulgaria — coordinates are swapped", lon)
	}
	if lat < 41 || lat > 45 {
		t.Errorf("latitude = %v, outside Bulgaria — coordinates are swapped", lat)
	}
}

func TestReadingIsHypertable(t *testing.T) {
	ctx, pool := migrated(t)

	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM timescaledb_information.hypertables
		 WHERE hypertable_name = 'reading'`).Scan(&count)
	if err != nil {
		t.Fatalf("query hypertables: %v", err)
	}
	if count != 1 {
		t.Fatalf("reading is not a hypertable (found %d)", count)
	}
}

func TestReadingRejectsDuplicateSamples(t *testing.T) {
	ctx, pool := migrated(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	mustInsertSensor(t, ctx, pool, 1)

	insert := func() error {
		_, err := pool.Exec(ctx,
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, $3, $4, 'ok')
			 ON CONFLICT (sensor_id, metric, time) DO NOTHING`,
			ts, int64(1), "P1", 24.3)
		return err
	}
	if err := insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insert(); err != nil {
		t.Fatalf("second insert should be a no-op, got: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading count = %d, want 1 (duplicate not suppressed)", n)
	}
}

func mustInsertSensor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, 'SDS011', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)`,
		id, sofiaLon, sofiaLat)
	if err != nil {
		t.Fatalf("insert sensor %d: %v", id, err)
	}
}
