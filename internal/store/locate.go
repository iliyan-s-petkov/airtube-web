package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// areaAtPointSQL finds the smallest area containing a point.
//
// ORDER BY ST_Area(geom) picks the tightest fit, so a Sofia visitor lands on a
// district rather than the oblast. ST_Covers rather than ST_Within: Covers
// treats a point exactly on the boundary as inside, and a visitor standing on a
// municipal line should get a map, not the national default.
//
// LIMIT 1 after the ordering, not instead of it — without the ORDER BY, which
// row comes back is whatever the planner produces.
const areaAtPointSQL = `
SELECT a.slug
  FROM area a
 WHERE ST_Covers(a.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography)
 ORDER BY ST_Area(a.geom)
 LIMIT 1`

// AreaAtPoint returns the slug of the smallest area containing (lon, lat), or
// "" when the point is outside every area.
//
// Argument order is longitude first, matching PostGIS geography and the rest of
// this codebase — and the inverse of the legacy PHP app's [lat, long]. A swap
// produces a valid coordinate somewhere off the Somali coast, so it fails
// silently by returning the default view rather than erroring.
//
// An empty result is not an error. A visitor abroad is a normal case.
func (s *Store) AreaAtPoint(ctx context.Context, lon, lat float64) (string, error) {
	var slug string
	err := s.pool.QueryRow(ctx, areaAtPointSQL, lon, lat).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: area at point: %w", err)
	}
	return slug, nil
}
