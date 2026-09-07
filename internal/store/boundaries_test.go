package store_test

import (
	"encoding/json"
	"math"
	"testing"

	"airbg.org/internal/store"
)

func TestAreaBoundariesReturnsOneShapePerAreaOfTheKindsAsked(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3, 42.7)
	seedArea(t, ctx, pool, "plovdiv", "oblast", 24.7, 42.1)
	seedArea(t, ctx, pool, "lozenets", "neighbourhood", 23.3, 42.68)

	got, err := s.AreaBoundaries(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaBoundaries: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d boundaries, want 2 (the neighbourhood must not be included)", len(got))
	}
	// Ordered by slug, like every other listing here, so a snapshot built twice
	// from unchanged geometry produces byte-identical JSON and keeps its ETag.
	if got[0].Slug != "plovdiv" || got[1].Slug != "sofia" {
		t.Fatalf("got slugs %q, %q; want them ordered by slug", got[0].Slug, got[1].Slug)
	}
	if got[0].NameBG == "" || got[0].NameEN == "" {
		t.Fatalf("got empty names for %s; the overlay labels its shapes", got[0].Slug)
	}
}

func TestAreaBoundariesReturnsDrawableGeoJSONGeometry(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3, 42.7)

	got, err := s.AreaBoundaries(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaBoundaries: %v", err)
	}

	var geom struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(got[0].GeoJSON, &geom); err != nil {
		t.Fatalf("geometry is not JSON: %v", err)
	}
	if geom.Type != "Polygon" && geom.Type != "MultiPolygon" {
		t.Fatalf("got geometry type %q, want Polygon or MultiPolygon", geom.Type)
	}
	if len(geom.Coordinates) == 0 {
		t.Fatal("geometry has no coordinates")
	}
}

// The country tier is 28 shapes fetched by every visitor who leaves the overlay
// on. Real oblast outlines are tens of thousands of vertices; served raw they
// would cost more than the readings the page exists to show.
func TestAreaBoundariesSimplifiesTheOutline(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	// A 5 km circle, which PostGIS buffers into a many-vertex ring.
	seedArea(t, ctx, pool, "sofia", "oblast", 23.3, 42.7)

	var raw int
	if err := pool.QueryRow(ctx,
		`SELECT ST_NPoints(geom::geometry) FROM area WHERE slug = 'sofia'`).Scan(&raw); err != nil {
		t.Fatalf("count raw vertices: %v", err)
	}

	got, err := s.AreaBoundaries(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaBoundaries: %v", err)
	}

	var geom struct {
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(got[0].GeoJSON, &geom); err != nil {
		t.Fatalf("geometry is not a Polygon: %v", err)
	}
	simplified := 0
	for _, ring := range geom.Coordinates {
		simplified += len(ring)
	}
	if simplified >= raw {
		t.Fatalf("got %d vertices from %d raw; the outline was not simplified", simplified, raw)
	}

	// Every coordinate at four decimals — about 10 m, which is finer than the
	// simplification and far coarser than a sensor position.
	for _, ring := range geom.Coordinates {
		for _, c := range ring {
			for _, v := range c {
				// Against the rounded value rather than testing v*1e4 for
				// integrality: 42.6816 times 1e4 is 426816.00000000006 in
				// binary floating point, which would fail a value that is
				// exactly what four decimals can represent.
				if math.Abs(v-math.Round(v*1e4)/1e4) > 1e-9 {
					t.Fatalf("coordinate %v carries more than four decimals", v)
				}
			}
		}
	}
}
