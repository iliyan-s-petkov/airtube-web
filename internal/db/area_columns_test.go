package db_test

import "testing"

// TestAreaHasPresentationColumns asserts the columns exist AND are NOT NULL.
// Nullable columns would let a future import path insert an area with no
// centroid, which /locate would then resolve to a nil point — an error at the
// far end of the system from its cause. The database is the right place to
// make that impossible.
func TestAreaHasPresentationColumns(t *testing.T) {
	ctx, pool := migrated(t)

	rows, err := pool.Query(ctx,
		`SELECT column_name, is_nullable, data_type
		   FROM information_schema.columns
		  WHERE table_name = 'area'
		    AND column_name IN ('centroid', 'default_zoom')`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, nullable, dataType string
		if err := rows.Scan(&name, &nullable, &dataType); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = nullable
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, col := range []string{"centroid", "default_zoom"} {
		nullable, ok := got[col]
		if !ok {
			t.Errorf("area.%s does not exist", col)
			continue
		}
		if nullable != "NO" {
			t.Errorf("area.%s is nullable; it must be NOT NULL", col)
		}
	}
}

// TestDefaultZoomByKind pins the per-kind zoom levels. They are not arbitrary:
// Phase 1 §7.1 swaps the map from choropleth to individual sensors at z >= 11,
// so an oblast must resolve BELOW that threshold (or /locate would drop a
// visitor straight into the per-sensor tier for a region far too large to
// render) and a neighbourhood must resolve at or above it.
func TestDefaultZoomByKind(t *testing.T) {
	ctx, pool := migrated(t)

	// A minimal polygon somewhere inside Bulgaria; geometry is irrelevant here,
	// only the trigger-computed columns are under test.
	const poly = `MULTIPOLYGON(((23.0 42.0, 23.1 42.0, 23.1 42.1, 23.0 42.1, 23.0 42.0)))`

	// code is what area_country_code_check demands of each kind: present on a
	// country row, absent on every other.
	for _, tc := range []struct {
		kind     string
		code     any
		wantZoom int16
	}{
		{"country", "BG", 7},
		{"oblast", nil, 9},
		{"city", nil, 11},
		{"neighbourhood", nil, 13},
	} {
		var zoom int16
		err := pool.QueryRow(ctx,
			`INSERT INTO area (slug, kind, name_bg, name_en, country_code, geom)
			 VALUES ($1, $2, 'x', 'x', $4, ST_GeomFromText($3, 4326)::geography)
			 RETURNING default_zoom`,
			"zoomtest-"+tc.kind, tc.kind, poly, tc.code).Scan(&zoom)
		if err != nil {
			t.Fatalf("insert %s: %v", tc.kind, err)
		}
		if zoom != tc.wantZoom {
			t.Errorf("kind %q default_zoom = %d, want %d", tc.kind, zoom, tc.wantZoom)
		}
	}
}

// TestCentroidComputedOnInsert asserts the centroid is derived from geom
// rather than left to the caller. A caller-supplied centroid can disagree with
// its own polygon; a derived one cannot.
func TestCentroidComputedOnInsert(t *testing.T) {
	ctx, pool := migrated(t)

	const poly = `MULTIPOLYGON(((23.0 42.0, 23.2 42.0, 23.2 42.2, 23.0 42.2, 23.0 42.0)))`
	var lon, lat float64
	err := pool.QueryRow(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('centroid-test', 'city', 'x', 'x', ST_GeomFromText($1, 4326)::geography)
		 RETURNING ST_X(centroid::geometry), ST_Y(centroid::geometry)`,
		poly).Scan(&lon, &lat)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Centre of that square is (23.1, 42.1). Asserting lon and lat separately,
	// against non-overlapping tolerances, is what makes this test detect a
	// (lat, lon) swap — if the columns were transposed, lon would read 42.1.
	if lon < 23.05 || lon > 23.15 {
		t.Errorf("centroid longitude = %v, want ~23.1 (a value near 42 means lon/lat are swapped)", lon)
	}
	if lat < 42.05 || lat > 42.15 {
		t.Errorf("centroid latitude = %v, want ~42.1 (a value near 23 means lon/lat are swapped)", lat)
	}
}
