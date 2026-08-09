package area_test

import (
	"context"
	"strings"
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

	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
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

	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
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

// TestAssignSensorsExcludesCountryBoundary is task-17 review finding 2's
// regression test. Importing the national boundary alongside city
// boundaries must not create an area_sensor row linking every sensor to the
// whole-country pseudo-area — a sensor genuinely inside both the country
// boundary and one city polygon must land in exactly one area_sensor row,
// the city's.
func TestAssignSensorsExcludesCountryBoundary(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import(sofia): %v", err)
	}
	if _, err := area.Import(ctx, pool, "testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("Import(bulgaria): %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT area_slug FROM area_sensor WHERE sensor_id = 1`)
	if err != nil {
		t.Fatalf("query assignments: %v", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			t.Fatalf("scan: %v", err)
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(slugs) != 1 {
		t.Fatalf("area_sensor rows for sensor 1 = %v (%d), want exactly 1", slugs, len(slugs))
	}
	if slugs[0] != "sofia" {
		t.Errorf("area_slug = %q, want %q — the country boundary must never be assigned as an area", slugs[0], "sofia")
	}
}

// TestAssignSensorsRevokesMembershipAfterSensorMoves is the regression test for
// the insert-only AssignSensors. store.UpsertSensor deliberately updates
// `location` on conflict, so a sensor that is physically moved gets new
// coordinates — but AssignSensors only ever INSERTed ... ON CONFLICT DO
// NOTHING, so the old area_sensor row survived forever. The sensor then
// appeared in two cities at once, in both Phase 2's per-area listings and any
// per-area aggregate, and no amount of re-running the assignment fixed it.
func TestAssignSensorsRevokesMembershipAfterSensorMoves(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`,
	); err != nil {
		t.Fatalf("insert sensor: %v", err)
	}
	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors (before move): %v", err)
	}

	// The sensor is relocated to Plovdiv — exactly what store.UpsertSensor's
	// `SET location = EXCLUDED.location` produces when upstream reports new
	// coordinates for a known sensor_id.
	if _, err := pool.Exec(ctx,
		`UPDATE sensor
		 SET location = ST_SetSRID(ST_MakePoint(24.7453, 42.1354), 4326)::geography
		 WHERE sensor_id = 1`,
	); err != nil {
		t.Fatalf("move sensor: %v", err)
	}

	_, revoked, err := area.AssignSensors(ctx, pool)
	if err != nil {
		t.Fatalf("AssignSensors (after move): %v", err)
	}
	if revoked != 1 {
		t.Errorf("revoked = %d, want 1 — the stale Sofia membership must be withdrawn", revoked)
	}

	slugs := memberships(ctx, t, pool, 1)
	if len(slugs) != 1 || slugs[0] != "plovdiv" {
		t.Errorf("memberships = %v, want exactly [plovdiv] — a moved sensor must not stay a member of the area it left", slugs)
	}
}

// TestAssignSensorsRevokesMembershipWhenSensorLeavesEveryArea covers the case
// the relocation test cannot: moving somewhere with no replacement membership.
// An insert-only implementation returns "0 assigned" here and looks like it
// correctly did nothing, while the sensor silently remains listed in Sofia.
func TestAssignSensorsRevokesMembershipWhenSensorLeavesEveryArea(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`,
	); err != nil {
		t.Fatalf("insert sensor: %v", err)
	}
	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors (before move): %v", err)
	}

	// Rural coordinates between the two city polygons, inside neither.
	if _, err := pool.Exec(ctx,
		`UPDATE sensor
		 SET location = ST_SetSRID(ST_MakePoint(24.0, 42.4), 4326)::geography
		 WHERE sensor_id = 1`,
	); err != nil {
		t.Fatalf("move sensor: %v", err)
	}

	assigned, revoked, err := area.AssignSensors(ctx, pool)
	if err != nil {
		t.Fatalf("AssignSensors (after move): %v", err)
	}
	if assigned != 0 {
		t.Errorf("assigned = %d, want 0", assigned)
	}
	if revoked != 1 {
		t.Errorf("revoked = %d, want 1", revoked)
	}
	if slugs := memberships(ctx, t, pool, 1); len(slugs) != 0 {
		t.Errorf("memberships = %v, want none", slugs)
	}
}

// TestAssignSensorsRevokesMembershipWhenBoundaryShrinks is the other direction
// of the same defect: the sensor never moves, the polygon does. Re-importing a
// corrected (smaller) city outline must withdraw the sensors it no longer
// covers, otherwise every boundary correction leaves permanent false
// memberships behind.
func TestAssignSensorsRevokesMembershipWhenBoundaryShrinks(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`,
	); err != nil {
		t.Fatalf("insert sensor: %v", err)
	}
	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	// Shrink Sofia to a small box in its south-west corner, well away from the
	// sensor. Import itself is not under test here, so the geometry is set
	// directly.
	if _, err := pool.Exec(ctx,
		`UPDATE area
		 SET geom = ST_SetSRID(ST_MakeEnvelope($1, $2, $3, $4), 4326)::geography
		 WHERE slug = 'sofia'`,
		23.20, 42.60, 23.22, 42.62,
	); err != nil {
		t.Fatalf("shrink area: %v", err)
	}

	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors (after shrink): %v", err)
	}
	if slugs := memberships(ctx, t, pool, 1); len(slugs) != 0 {
		t.Errorf("memberships = %v, want none after the boundary stopped covering the sensor", slugs)
	}
}

// TestImportRejectsSelfIntersectingGeometry pins minor 25. A bow-tie polygon is
// accepted by ST_GeomFromGeoJSON and only surfaces later as wrong containment.
// The valid feature ordered before it must also not be imported: a partially
// imported boundary set is the failure mode worth avoiding most.
func TestImportRejectsSelfIntersectingGeometry(t *testing.T) {
	ctx, pool := migrated(t)

	_, err := area.Import(ctx, pool, "testdata/self_intersecting.geojson", "city")
	if err == nil {
		t.Fatal("Import accepted a self-intersecting polygon")
	}
	if !strings.Contains(err.Error(), "bowtie") {
		t.Errorf("error %q does not name the offending slug", err)
	}
	assertAreaCount(ctx, t, pool, 0)
}

// TestImportRejectsEmptyGeometry pins minor 26. `"coordinates": []` yields
// MULTIPOLYGON EMPTY, which is not NULL, so it satisfies the column constraint
// and inserts silently. Task 17 made that catastrophic rather than cosmetic: as
// the `country` boundary it is *present* (so the fail-closed check passes) and
// covers nothing (so every sensor is rejected), and the collector stores nothing
// indefinitely.
func TestImportRejectsEmptyGeometry(t *testing.T) {
	ctx, pool := migrated(t)

	_, err := area.Import(ctx, pool, "testdata/empty_geometry.geojson", area.NationalBoundaryKind)
	if err == nil {
		t.Fatal("Import accepted a geometry with empty coordinates")
	}
	if !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("error %q does not name the offending slug", err)
	}
	assertAreaCount(ctx, t, pool, 0)
}

func memberships(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sensorID int64) []string {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT area_slug FROM area_sensor WHERE sensor_id = $1 ORDER BY area_slug`, sensorID)
	if err != nil {
		t.Fatalf("query memberships: %v", err)
	}
	defer rows.Close()
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			t.Fatalf("scan: %v", err)
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return slugs
}

func assertAreaCount(ctx context.Context, t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM area`).Scan(&n); err != nil {
		t.Fatalf("count areas: %v", err)
	}
	if n != want {
		t.Errorf("area count = %d, want %d", n, want)
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
		if _, _, err := area.AssignSensors(ctx, pool); err != nil {
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
