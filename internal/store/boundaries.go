package store

import (
	"context"
	"fmt"
)

// AreaBoundary is one area's outline, ready to be a GeoJSON Feature's geometry.
//
// GeoJSON arrives as bytes and is never decoded here: it is built by PostGIS,
// travels through the snapshot, and is written into the response verbatim.
// Parsing it only to re-encode it would cost a round trip through Go's float
// formatting on every build for no gain.
type AreaBoundary struct {
	Slug    string
	NameBG  string
	NameEN  string
	GeoJSON []byte
}

// boundarySimplifyDeg is the tolerance the outlines are generalised at, in
// degrees — roughly 400 m at this latitude.
//
// The overlay exists to say which province you are looking at, not to survey
// it. At the zoom the country tier is read at, 400 m is well under a pixel, and
// the raw outlines are tens of thousands of vertices each — a payload larger
// than the readings the page exists to show.
const boundarySimplifyDeg = 0.004

// PreserveTopology, not plain ST_Simplify: plain simplification can collapse a
// narrow province into an invalid ring or drop an island outright, and the
// result is drawn, not measured against.
//
// ST_AsGeoJSON at four decimals both shortens the payload and keeps it stable —
// unrounded PostGIS output differs in its last digits between server versions,
// which would break the snapshot's ETag on an upgrade that changed nothing.
const areaBoundariesSQL = `
SELECT a.slug, a.name_bg, a.name_en,
       ST_AsGeoJSON(
           ST_SimplifyPreserveTopology(a.geom::geometry, $2::float8), 4)
  FROM area a
 WHERE a.kind = ANY($1::text[])
   AND a.geom IS NOT NULL
 ORDER BY a.slug`

// AreaBoundaries returns the outline of every area of the requested kinds,
// ordered by slug.
//
// kinds is a bound text[] parameter, never interpolated.
func (s *Store) AreaBoundaries(ctx context.Context, kinds []string) ([]AreaBoundary, error) {
	rows, err := s.pool.Query(ctx, areaBoundariesSQL, kinds, boundarySimplifyDeg)
	if err != nil {
		return nil, fmt.Errorf("store: area boundaries: %w", err)
	}
	defer rows.Close()

	var out []AreaBoundary
	for rows.Next() {
		var b AreaBoundary
		if err := rows.Scan(&b.Slug, &b.NameBG, &b.NameEN, &b.GeoJSON); err != nil {
			return nil, fmt.Errorf("store: scan area boundary: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: area boundaries: %w", err)
	}
	return out, nil
}
