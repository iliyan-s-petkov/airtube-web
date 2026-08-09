package area

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AssignSensors recomputes the sensor-to-area mapping by point-in-polygon
// containment. Sensors do not move, so this runs when boundaries or sensors
// change — never per request (spec §5.5).
//
// A sensor may belong to several areas at once: a Sofia sensor is in the Sofia
// city polygon, the Sofia-grad oblast, and its neighbourhood. That is intended,
// and the composite primary key keeps each pairing unique.
//
// area.kind = NationalBoundaryKind ("country") is excluded (task-17 review
// finding 2). That boundary exists solely for FilterByBoundary's ingest-time
// membership test; every sensor that reaches this query has, by
// construction, already passed it. Assigning it to a whole-country
// pseudo-area here would add a meaningless row to every "which areas is
// this sensor in" / "which sensors are in this area" query — exactly the
// confusion NationalBoundaryKind's own doc comment says a distinct kind
// exists to prevent.
func AssignSensors(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx,
		`INSERT INTO area_sensor (area_slug, sensor_id)
		 SELECT a.slug, s.sensor_id
		 FROM area a
		 JOIN sensor s ON ST_Covers(a.geom, s.location)
		 WHERE a.kind != $1
		 ON CONFLICT (area_slug, sensor_id) DO NOTHING`,
		NationalBoundaryKind)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
