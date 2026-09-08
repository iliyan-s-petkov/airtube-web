package store

import (
	"context"
	"fmt"
	"time"
)

// A frame is the map as it stood in one past hour, which is a different question
// from the two aggregate.go and window.go answer. Live says "now"; a window says
// "the mean of the last day". A frame says "07:00, and then 08:00, and then
// 09:00" — the sequence a timelapse plays.
//
// It carries a coordinate rather than a sensor id: a frame is only ever binned
// into hexes, and a per-hour registry of which device stood where would be the
// walkable history of every device in the country. What comes back is where the
// air was measured and what it read, and nothing about who measured it.
type FrameReading struct {
	Bucket time.Time
	Lon    float64
	Lat    float64
	Value  float64
}

// The rollup already holds one row per sensor per metric per hour, so there is
// nothing to average here — the fold into coarser steps happens in Go, over
// bins rather than over sensors, because the caller wants a hex either way.
//
// No freshness predicate, unlike the live and windowed queries. Those ask what a
// device is saying now, and a silent device has nothing to say; a frame asks what
// the air was at a past hour, and a device that has since died still measured it.
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
