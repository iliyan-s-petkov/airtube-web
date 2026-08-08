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
func Import(ctx context.Context, pool *pgxpool.Pool, path, kind string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("area: read %s: %w", path, err)
	}

	var fc featureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		return 0, fmt.Errorf("area: parse %s: %w", path, err)
	}

	batch := &pgx.Batch{}
	for _, f := range fc.Features {
		if f.Properties.Slug == "" {
			return 0, fmt.Errorf("area: feature in %s has no slug property", path)
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
	if batch.Len() == 0 {
		return 0, nil
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, fmt.Errorf("area: import %s: %w", path, err)
	}
	return len(fc.Features), nil
}
