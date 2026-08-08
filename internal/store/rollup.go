package store

import (
	"context"
	"time"
)

// RollupHour recomputes the hourly aggregate for one bucket from raw readings.
//
// Only readings whose quality flag permits aggregation are included, so a
// flagged sensor is structurally incapable of moving a published average
// (spec §5.3). Recomputing rather than incrementing makes the operation
// idempotent and safe to re-run over any bucket.
func (s *Store) RollupHour(ctx context.Context, bucket time.Time) (int64, error) {
	bucket = TruncateHour(bucket)

	tag, err := s.pool.Exec(ctx,
		`INSERT INTO reading_hourly
		     (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 SELECT $1, sensor_id, metric, avg(value), min(value), max(value), count(*)
		 FROM reading
		 WHERE time >= $1 AND time < $1 + interval '1 hour'
		   AND quality IN ('ok', 'no_neighbours')
		 GROUP BY sensor_id, metric
		 ON CONFLICT (sensor_id, metric, bucket) DO UPDATE
		   SET avg_value = EXCLUDED.avg_value,
		       min_value = EXCLUDED.min_value,
		       max_value = EXCLUDED.max_value,
		       sample_count = EXCLUDED.sample_count`,
		bucket)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
