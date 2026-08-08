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
func AssignSensors(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx,
		`INSERT INTO area_sensor (area_slug, sensor_id)
		 SELECT a.slug, s.sensor_id
		 FROM area a
		 JOIN sensor s ON ST_Covers(a.geom, s.location)
		 ON CONFLICT (area_slug, sensor_id) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
