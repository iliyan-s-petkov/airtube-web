// Package area imports administrative boundaries and assigns sensors to them.
package area

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type featureCollection struct {
	Type     string `json:"type"`
	Features []struct {
		Properties struct {
			Slug   string `json:"slug"`
			NameBG string `json:"name_bg"`
			NameEN string `json:"name_en"`
		} `json:"properties"`
		Geometry json.RawMessage `json:"geometry"`
	} `json:"features"`
}

// Import loads a GeoJSON FeatureCollection into the area table. Each feature
// needs slug, name_bg and name_en properties. Geometry is handed to PostGIS as
// raw GeoJSON and coerced to MultiPolygon, so both Polygon and MultiPolygon
// inputs work.
//
// GeoJSON coordinates are [longitude, latitude], which matches the storage
// convention. No axis swapping happens anywhere in this function, and none
// should be added.
//
// Every geometry is validated before anything is written: ST_IsValid must hold
// and ST_IsEmpty must not. Both checks were absent, and task 17 changed what
// their absence costs. When areas were only cities, an invalid or empty polygon
// was a cosmetic defect in one map layer. Task 17 made area.kind = "country"
// load-bearing for *all* ingest, and never revisited them:
//
//   - `"coordinates": []` produces MULTIPOLYGON EMPTY, which is not NULL and so
//     satisfies the column's NOT NULL constraint and inserts silently. As a
//     country boundary it matches nothing, so ST_Covers rejects every sensor
//     while the boundary-presence check still finds the row — the collector then
//     stores nothing, cycle after cycle, with the boundary reported present.
//   - A self-intersecting polygon is accepted by ST_GeomFromGeoJSON and only
//     surfaces later as wrong containment: sensors assigned to the wrong area,
//     or excluded from the country, with no error anywhere.
//
// Both now fail the import, naming the offending slug. ST_MakeValid is
// deliberately *not* applied: silently repairing an operator's boundary would
// store a polygon they never supplied and cannot inspect, and the repaired shape
// is exactly as likely to be subtly wrong as the input. Rejecting tells them
// which feature to fix.
//
// Validation runs as one query per feature before the write batch, so an invalid
// feature anywhere in the file means nothing at all is imported — a partial
// import of a national boundary is the failure mode worth avoiding most.
func Import(ctx context.Context, pool *pgxpool.Pool, path, kind string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("area: read %s: %w", path, err)
	}

	var fc featureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		return 0, fmt.Errorf("area: parse %s: %w", path, err)
	}

	// A file that is not a FeatureCollection unmarshals without error into a
	// featureCollection with no features, because encoding/json tolerates the
	// absent "features" key. The loop below then does not run, and with it none
	// of the per-feature validation runs either — so a bare Feature (the shape
	// most GeoJSON exports of a single country produce) used to import "0
	// features, no error". As a national boundary that is the worst outcome
	// available: import reports success, the boundary-presence check finds no
	// row, and every collect cycle stores nothing.
	//
	// Both of these checks therefore live *before* the loop. Validation placed
	// inside the loop cannot fire on a file with zero features, which is exactly
	// the case that needs catching.
	if fc.Type != "FeatureCollection" {
		return 0, fmt.Errorf(
			"area: %s has GeoJSON type %q, want \"FeatureCollection\" — wrap the feature in {\"type\":\"FeatureCollection\",\"features\":[...]}; only a FeatureCollection's features are read, so any other type imports nothing",
			path, fc.Type)
	}
	if len(fc.Features) == 0 {
		return 0, fmt.Errorf(
			"area: %s contains no features — importing nothing is never the intent, and as a %q boundary it leaves the collector storing nothing every cycle",
			path, NationalBoundaryKind)
	}

	batch := &pgx.Batch{}
	for _, f := range fc.Features {
		if f.Properties.Slug == "" {
			return 0, fmt.Errorf("area: feature in %s has no slug property", path)
		}
		if err := validateGeometry(ctx, pool, path, f.Properties.Slug, string(f.Geometry)); err != nil {
			return 0, err
		}
		batch.Queue(
			`INSERT INTO area (slug, kind, name_bg, name_en, geom)
			 VALUES ($1, $2, $3, $4,
			         ST_Multi(ST_GeomFromGeoJSON($5))::geography)
			 ON CONFLICT (slug) DO UPDATE
			   SET kind = EXCLUDED.kind,
			       name_bg = EXCLUDED.name_bg,
			       name_en = EXCLUDED.name_en,
			       geom = EXCLUDED.geom`,
			f.Properties.Slug, kind, f.Properties.NameBG, f.Properties.NameEN,
			string(f.Geometry))
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, fmt.Errorf("area: import %s: %w", path, err)
	}
	return len(fc.Features), nil
}

// validateGeometry rejects geometry that would insert successfully and then
// behave wrongly. See Import's doc comment for why each check exists.
//
// The parse itself is part of the check: ST_GeomFromGeoJSON errors on malformed
// or unsupported GeoJSON, and catching that here attributes it to a named slug
// instead of surfacing as an opaque batch failure with no indication of which
// of several hundred features was at fault.
func validateGeometry(ctx context.Context, pool *pgxpool.Pool, path, slug, geometry string) error {
	var valid, empty bool
	var reason string
	err := pool.QueryRow(ctx, `
		WITH g AS (SELECT ST_Multi(ST_GeomFromGeoJSON($1)) AS geom)
		SELECT ST_IsValid(geom), ST_IsEmpty(geom), ST_IsValidReason(geom) FROM g`,
		geometry).Scan(&valid, &empty, &reason)
	if err != nil {
		return fmt.Errorf("area: feature %q in %s has unusable geometry: %w", slug, path, err)
	}

	if empty {
		return fmt.Errorf(
			"area: feature %q in %s has empty geometry (MULTIPOLYGON EMPTY) — it would insert successfully and then match no point at all; as a %q boundary that silently rejects every sensor",
			slug, path, NationalBoundaryKind)
	}
	if !valid {
		return fmt.Errorf(
			"area: feature %q in %s has invalid geometry: %s — fix the source outline rather than importing it; an invalid polygon surfaces later as wrong containment, not as an error",
			slug, path, reason)
	}
	return nil
}
