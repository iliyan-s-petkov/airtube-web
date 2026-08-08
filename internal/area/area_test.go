package area_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
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

func TestImportLoadsFeatures(t *testing.T) {
	ctx, pool := migrated(t)

	n, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported %d areas, want 2", n)
	}

	var nameBG string
	err = pool.QueryRow(ctx, `SELECT name_bg FROM area WHERE slug = 'sofia'`).Scan(&nameBG)
	if err != nil {
		t.Fatalf("read area: %v", err)
	}
	if nameBG != "София" {
		t.Errorf("name_bg = %q, want %q", nameBG, "София")
	}
}

func TestImportIsIdempotent(t *testing.T) {
	ctx, pool := migrated(t)

	for i := 0; i < 2; i++ {
		if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
			t.Fatalf("Import run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM area`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("area count = %d, want 2", n)
	}
}

// This is the §11.2 mandatory test: a sensor at Sofia's real coordinates must
// land inside the Sofia polygon. A latitude/longitude swap places it in the
// Indian Ocean and this assertion fails.
func TestAssignSensorsPlacesSofiaSensorInSofia(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	if _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	var slug string
	err = pool.QueryRow(ctx, `SELECT area_slug FROM area_sensor WHERE sensor_id = 1`).Scan(&slug)
	if err != nil {
		t.Fatalf("read assignment: %v — sensor was not placed in any area", err)
	}
	if slug != "sofia" {
		t.Errorf("area_slug = %q, want %q", slug, "sofia")
	}
}

func TestAssignSensorsSkipsSensorsOutsideEveryArea(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	// A rural sensor between the two cities, inside neither polygon.
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (2, 'SDS011', ST_SetSRID(ST_MakePoint(24.0, 42.4), 4326)::geography)`)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	if _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM area_sensor WHERE sensor_id = 2`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("assignments = %d, want 0 — a sensor outside every polygon must not be assigned", n)
	}
}

func TestAssignSensorsIsIdempotent(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := area.AssignSensors(ctx, pool); err != nil {
			t.Fatalf("AssignSensors run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM area_sensor`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("assignment count = %d, want 1", n)
	}
}
