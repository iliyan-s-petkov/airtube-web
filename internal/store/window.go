package store

import (
	"context"
	"fmt"
	"time"
)

// Windowed answers: what was the air like over the last day, two days or week —
// as opposed to the live question the rest of aggregate.go answers.
//
// The source is reading_hourly, not reading. Raw readings are retained 30 days
// (migration 00003) so a week would still fit, but a week of raw rows is millions
// of them per rebuild where the rollup is 168 per sensor per metric. The rollup
// is also already quality-filtered at build time, which is why the windowed mean
// below carries no quality predicate of its own.
//
// The mean is weighted by sample_count rather than a flat avg(avg_value): an
// hour in which a sensor reported twice must not count as much as one in which
// it reported sixty times, or a device that dropped out for most of an hour
// steers the average it barely contributed to. Weighted this way the result
// equals the mean of the underlying readings, which is what a reader asked for.
const windowedSensorCTE = `
windowed AS (
    SELECT h.sensor_id, h.metric,
           sum(h.avg_value * h.sample_count) / NULLIF(sum(h.sample_count), 0) AS value
      FROM reading_hourly h
     WHERE h.bucket >= $%d
     GROUP BY h.sensor_id, h.metric
)`

// The windowed per-area mean joins the window's numbers onto the LIVE latest
// CTE, and that join is the whole design. Identity, freshness and quality stay
// the live answer, so switching the window changes the numbers and nothing else:
// the same areas exist, with the same station counts, and an area that has gone
// silent today does not reappear because it had readings on Tuesday.
const windowedPerAreaCTE = `
per_area AS (
    SELECT a.slug, l.metric, avg(w.value) AS avg_value
      FROM area a
      JOIN area_sensor asx ON asx.area_slug = a.slug
      JOIN latest l        ON l.sensor_id = asx.sensor_id
      JOIN windowed w      ON w.sensor_id = l.sensor_id AND w.metric = l.metric
     WHERE a.kind = ANY($3::text[])
     GROUP BY a.slug, l.metric
)`

var windowedAreaAggregateSQL = "WITH" + latestCTE +
	"," + fmt.Sprintf(windowedSensorCTE, 4) +
	"," + windowedPerAreaCTE +
	"," + coverageCTE + areaAggregateSelect

// WindowedAreaAggregates is AreaAggregates with the published value replaced by
// the mean over [since, now). Every other column — coverage, station count,
// which areas appear at all — is identical, by construction: both queries are
// assembled from the same CTE and projection fragments.
func (s *Store) WindowedAreaAggregates(ctx context.Context, kinds []string, since time.Time) ([]AreaAggregate, error) {
	fresh := time.Now().UTC().Add(-s.cfg.FreshnessWindow)

	rows, err := s.pool.Query(ctx, windowedAreaAggregateSQL, fresh, usableQuality, kinds, since)
	if err != nil {
		return nil, fmt.Errorf("store: windowed area aggregates: %w", err)
	}
	return s.scanAreaAggregates(rows)
}

var windowedSensorsSQL = "WITH" + latestSensorsCTE +
	"," + fmt.Sprintf(windowedSensorCTE, 3) +
	sensorsSelect("w.value", "  LEFT JOIN windowed w ON w.sensor_id = l.sensor_id AND w.metric = l.metric")

// WindowedSensors is LatestSensors with each published value replaced by the
// device's mean over the window.
//
// The join is a LEFT join, not an inner one: a device that started reporting an
// hour ago has a live reading and no rollup row inside a week-long window, and
// dropping it would make the marker vanish when the reader widened the window —
// the opposite of what widening it means. It comes back with its Measures list
// intact and no value for that metric, which the panel already renders as "no
// reading".
func (s *Store) WindowedSensors(ctx context.Context, since time.Time) ([]SensorReading, error) {
	fresh := time.Now().UTC().Add(-s.cfg.FreshnessWindow)

	rows, err := s.pool.Query(ctx, windowedSensorsSQL, fresh, usableQuality, since)
	if err != nil {
		return nil, fmt.Errorf("store: windowed sensors: %w", err)
	}
	return scanSensorReadings(rows)
}
