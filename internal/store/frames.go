package store

import (
	"context"
	"fmt"
	"time"
)

// One past hour of the map. A coordinate and no sensor id: the rows are only
// ever binned into hexes, and a per-hour device registry would be a walkable
// history of every device in the country.
type FrameReading struct {
	Bucket time.Time
	Lon    float64
	Lat    float64
	Value  float64
}

// No freshness predicate, unlike the live and windowed queries: a device that
// has since gone silent still measured the hour being asked about.
const frameReadingsSQL = `
SELECT h.bucket,
       ST_X(s.location::geometry), ST_Y(s.location::geometry),
       h.avg_value
  FROM reading_hourly h
  JOIN sensor s ON s.sensor_id = h.sensor_id
 WHERE h.metric = $1 AND h.bucket >= $2 AND h.bucket < $3
 ORDER BY h.bucket`

// FrameReadings returns every rollup hour in [since, until) for one metric,
// with the position it was measured at.
func (s *Store) FrameReadings(ctx context.Context, metric string, since, until time.Time) ([]FrameReading, error) {
	rows, err := s.pool.Query(ctx, frameReadingsSQL, metric, since, until)
	if err != nil {
		return nil, fmt.Errorf("store: frame readings: %w", err)
	}
	defer rows.Close()

	var out []FrameReading
	for rows.Next() {
		var f FrameReading
		if err := rows.Scan(&f.Bucket, &f.Lon, &f.Lat, &f.Value); err != nil {
			return nil, fmt.Errorf("store: scan frame reading: %w", err)
		}
		f.Bucket = f.Bucket.UTC()
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: frame readings: %w", err)
	}
	return out, nil
}
