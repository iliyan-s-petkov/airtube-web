package area

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/upstream"
)

// NationalBoundaryKind is the area.kind value reserved for the whole-country
// boundary this file filters sensors against. It is deliberately distinct
// from "city", "oblast" and "neighbourhood" (task 17) so a query that means
// "the national boundary" can never accidentally match a city polygon, and
// vice versa.
const NationalBoundaryKind = "country"

// BoundaryFilterResult is what one filtering pass learned. Country carries the
// code of the boundary that covered each accepted sensor, keyed by sensor ID:
// the containment test already knows which country admitted the sensor, and
// recomputing that later would mean a second spatial join over the same points.
//
// MissingCountries lists allow-list entries with no imported boundary. Not an
// error — an operator widening the list before sourcing the geometry is a
// normal intermediate state — but silence about it would look identical to a
// country that genuinely reports no sensors.
type BoundaryFilterResult struct {
	Accepted         []upstream.Reading
	RejectedSensors  int
	BoundaryPresent  bool
	Country          map[int64]string
	MissingCountries []string
}

// FilterByBoundary partitions readings by whether their sensor's coordinates
// fall inside one of the enabled countries' boundaries, using the same
// ST_Covers predicate AssignSensors already uses for city/oblast/
// neighbourhood containment (task-17 brief: "use the same predicate against
// the national boundary").
//
// Upstream's self-reported country field is not trusted for this (spec
// task-17's motivating case: sensor 48524 reports country "BG" from London).
// Only geometry decides — which is also why the allow list names countries by
// code rather than filtering on the upstream field: the code selects which
// polygons to test against, never what a sensor claims about itself.
//
// countries is the enabled set as ISO 3166-1 alpha-2 codes. Scoping the
// boundary set by config at query time rather than by what happens to be
// imported means disabling a country is a config change, not a re-import, and
// an accidentally imported boundary does not silently start admitting sensors.
// An empty list admits nothing — see boundaryPresent below.
//
// Sensors are deduplicated by ID before the query — a poll batch carries
// several readings (one per metric) per sensor, all sharing one location, so
// there is no reason to test the same point more than once.
//
// BoundaryPresent reports whether any *enabled* country has an imported
// boundary. When it is false the other fields are meaningless — the caller
// must decide what "no boundary to filter against" means for its own pipeline;
// this function only reports the fact, it does not itself choose fail-open or
// fail-closed.
func FilterByBoundary(ctx context.Context, pool *pgxpool.Pool, readings []upstream.Reading, countries []string) (BoundaryFilterResult, error) {
	if len(readings) == 0 {
		// Nothing to test against a boundary, present or not — and querying
		// for presence here would only produce a spurious "boundary absent"
		// signal on every cycle where upstream simply returned nothing.
		return BoundaryFilterResult{
			Accepted: readings, BoundaryPresent: true, Country: map[int64]string{},
		}, nil
	}

	// Deduplicate by sensor ID, preserving first-seen order so the query
	// parameters (and any future debugging of them) stay stable.
	type point struct{ lon, lat float64 }
	seen := make(map[int64]point, len(readings))
	ids := make([]int64, 0, len(readings))
	for _, r := range readings {
		if _, ok := seen[r.SensorID]; ok {
			continue
		}
		seen[r.SensorID] = point{r.Lon, r.Lat}
		ids = append(ids, r.SensorID)
	}
	lons := make([]float64, len(ids))
	lats := make([]float64, len(ids))
	for i, id := range ids {
		p := seen[id]
		lons[i] = p.lon
		lats[i] = p.lat
	}

	// LATERAL with LIMIT 1, not a plain join: a point in an overlapping or
	// disputed border strip would otherwise emit one row per matching boundary
	// and be counted twice. Ordering by code first makes the pick deterministic,
	// so a sensor on a border does not change country between cycles.
	rows, err := pool.Query(ctx, `
		WITH candidate AS (
			SELECT * FROM unnest($1::bigint[], $2::float8[], $3::float8[])
				AS c(sensor_id, lon, lat)
		),
		boundary AS (
			SELECT country_code, geom FROM area
			WHERE kind = $4 AND country_code = ANY($5::text[])
		)
		SELECT c.sensor_id, m.country_code
		FROM candidate c
		JOIN LATERAL (
			SELECT b.country_code FROM boundary b
			WHERE ST_Covers(b.geom, ST_SetSRID(ST_MakePoint(c.lon, c.lat), 4326)::geography)
			ORDER BY b.country_code
			LIMIT 1
		) m ON true`,
		ids, lons, lats, NationalBoundaryKind, countries)
	if err != nil {
		return BoundaryFilterResult{}, err
	}
	defer rows.Close()

	country := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return BoundaryFilterResult{}, err
		}
		country[id] = code
	}
	if err := rows.Err(); err != nil {
		return BoundaryFilterResult{}, err
	}

	// Which enabled countries actually have geometry. Needed for two different
	// answers the query above cannot distinguish on its own: no rows because no
	// boundary is imported, versus no rows because every sensor is genuinely
	// outside the ones that are.
	//
	// Unlike the previous single-boundary version this cannot be skipped when
	// some sensor matched: with several countries enabled, one of them missing
	// its geometry is invisible in a result that other countries populated.
	imported, err := importedCountries(ctx, pool, countries)
	if err != nil {
		return BoundaryFilterResult{}, err
	}
	if len(imported) == 0 {
		return BoundaryFilterResult{}, nil
	}
	var missing []string
	for _, c := range countries {
		if !imported[c] {
			missing = append(missing, c)
		}
	}

	accepted := make([]upstream.Reading, 0, len(readings))
	rejected := make(map[int64]bool, len(ids))
	for _, r := range readings {
		if _, ok := country[r.SensorID]; ok {
			accepted = append(accepted, r)
		} else {
			rejected[r.SensorID] = true
		}
	}
	return BoundaryFilterResult{
		Accepted:         accepted,
		RejectedSensors:  len(rejected),
		BoundaryPresent:  true,
		Country:          country,
		MissingCountries: missing,
	}, nil
}

func importedCountries(ctx context.Context, pool *pgxpool.Pool, countries []string) (map[string]bool, error) {
	rows, err := pool.Query(ctx,
		`SELECT DISTINCT country_code FROM area
		 WHERE kind = $1 AND country_code = ANY($2::text[])`,
		NationalBoundaryKind, countries)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	imported := make(map[string]bool, len(countries))
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		imported[code] = true
	}
	return imported, rows.Err()
}
