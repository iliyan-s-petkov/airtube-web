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

// FilterByBoundary partitions readings by whether their sensor's coordinates
// fall inside the area.kind = NationalBoundaryKind boundary, using the same
// ST_Covers predicate AssignSensors already uses for city/oblast/
// neighbourhood containment (task-17 brief: "use the same predicate against
// the national boundary").
//
// Upstream's self-reported country field is not trusted for this (spec
// task-17's motivating case: sensor 48524 reports country "BG" from London).
// Only geometry decides.
//
// Sensors are deduplicated by ID before the query — a poll batch carries
// several readings (one per metric) per sensor, all sharing one location, so
// there is no reason to test the same point more than once.
//
// boundaryPresent reports whether an area row of kind NationalBoundaryKind
// existed at all. When it is false, accepted and rejectedSensors are
// meaningless (both zero) — the caller must decide what "no boundary to
// filter against" means for its own pipeline; this function only reports the
// fact, it does not itself choose fail-open or fail-closed.
func FilterByBoundary(ctx context.Context, pool *pgxpool.Pool, readings []upstream.Reading) (accepted []upstream.Reading, rejectedSensors int, boundaryPresent bool, err error) {
	if len(readings) == 0 {
		// Nothing to test against a boundary, present or not — and querying
		// for presence here would only produce a spurious "boundary absent"
		// signal on every cycle where upstream simply returned nothing.
		return readings, 0, true, nil
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

	rows, err := pool.Query(ctx, `
		WITH candidate AS (
			SELECT * FROM unnest($1::bigint[], $2::float8[], $3::float8[])
				AS c(sensor_id, lon, lat)
		),
		boundary AS (
			SELECT geom FROM area WHERE kind = $4
		)
		SELECT c.sensor_id
		FROM candidate c
		WHERE EXISTS (
			SELECT 1 FROM boundary b
			WHERE ST_Covers(b.geom, ST_SetSRID(ST_MakePoint(c.lon, c.lat), 4326)::geography)
		)`,
		ids, lons, lats, NationalBoundaryKind)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()

	inside := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, false, err
		}
		inside[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}

	// A boundary row of the right kind may simply not exist. EXISTS above
	// would then be false for every candidate, indistinguishable from "every
	// sensor is genuinely outside the boundary" unless checked separately —
	// unless at least one candidate already matched, which is only possible
	// if a boundary row existed to match against. That lets the common case
	// (boundary present, at least one sensor inside it) skip this second
	// round trip entirely.
	present := len(inside) > 0
	if !present {
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM area WHERE kind = $1)`, NationalBoundaryKind).
			Scan(&present); err != nil {
			return nil, 0, false, err
		}
		if !present {
			return nil, 0, false, nil
		}
	}

	accepted = make([]upstream.Reading, 0, len(readings))
	rejected := make(map[int64]bool, len(ids))
	for _, r := range readings {
		if inside[r.SensorID] {
			accepted = append(accepted, r)
		} else {
			rejected[r.SensorID] = true
		}
	}
	return accepted, len(rejected), true, nil
}
