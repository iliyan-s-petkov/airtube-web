package store

import (
	"context"
	"fmt"
	"time"
)

// CoverageThreshold is the minimum number of distinct sensors with usable
// readings an area needs before it publishes an aggregate number (Phase 1
// §5.7). Below it the area still appears — the map must be able to render an
// insufficient-coverage state — but carries no value.
//
// Three, not one, because a "regional average" derived from a single sensor is
// not an average. It is one sensor's reading with a region's name on it, and it
// looks exactly as authoritative as a real one.
const CoverageThreshold = 3

// usableQuality is the quality filter every published aggregate applies.
// 'no_neighbours' is usable: it records that the spatial-outlier check had
// nothing to compare against, not that the reading failed it. Excluding it
// would silently drop every rural sensor.
var usableQuality = []string{"ok", "no_neighbours"}

// freshnessWindow bounds how old a reading may be and still count toward a
// "current" aggregate. Two hours tolerates a missed poll or two without
// letting a sensor that died last week keep contributing to the number on the
// map — which is the more dangerous failure, because a stale value looks
// current.
const freshnessWindow = 2 * time.Hour

type AreaAggregate struct {
	Slug        string
	Kind        string
	NameBG      string
	NameEN      string
	CentroidLon float64
	CentroidLat float64
	DefaultZoom int
	// SensorCount counts distinct sensors with a usable, fresh reading — the
	// number the coverage threshold is applied to, not the total inside the
	// polygon.
	SensorCount int
	Values      map[string]float64
	Covered     bool
}

const areaAggregateSQL = `
WITH latest AS (
    SELECT DISTINCT ON (r.sensor_id, r.metric)
           r.sensor_id, r.metric, r.value
      FROM reading r
     WHERE r.time >= $1
       AND r.quality = ANY($2::quality_flag[])
     ORDER BY r.sensor_id, r.metric, r.time DESC
),
per_area AS (
    SELECT a.slug, l.metric,
           avg(l.value)               AS avg_value,
           count(DISTINCT l.sensor_id) AS sensors
      FROM area a
      JOIN area_sensor asx ON asx.area_slug = a.slug
      JOIN latest l        ON l.sensor_id = asx.sensor_id
     WHERE a.kind = ANY($3::text[])
     GROUP BY a.slug, l.metric
),
coverage AS (
    SELECT a.slug, count(DISTINCT asx.sensor_id) AS sensors
      FROM area a
      JOIN area_sensor asx ON asx.area_slug = a.slug
      JOIN latest l        ON l.sensor_id = asx.sensor_id
     WHERE a.kind = ANY($3::text[])
     GROUP BY a.slug
)
SELECT a.slug, a.kind, a.name_bg, a.name_en,
       ST_X(a.centroid::geometry), ST_Y(a.centroid::geometry), a.default_zoom,
       COALESCE(c.sensors, 0),
       COALESCE(
           (SELECT jsonb_object_agg(p.metric, round(p.avg_value::numeric, 2))
              FROM per_area p WHERE p.slug = a.slug),
           '{}'::jsonb)
  FROM area a
  LEFT JOIN coverage c ON c.slug = a.slug
 WHERE a.kind = ANY($3::text[])
 ORDER BY a.slug`

// AreaAggregates returns one row per area of the requested kinds, including
// areas with no sensors at all. Areas below CoverageThreshold come back with
// Covered false and an empty Values map — the filtering happens here, once, so
// no handler can forget it.
//
// kinds is passed as a bound text[] parameter, never interpolated. A slug or
// kind reaching SQL as text is the legacy application's injection bug.
func (s *Store) AreaAggregates(ctx context.Context, kinds []string) ([]AreaAggregate, error) {
	since := time.Now().UTC().Add(-freshnessWindow)

	rows, err := s.pool.Query(ctx, areaAggregateSQL, since, usableQuality, kinds)
	if err != nil {
		return nil, fmt.Errorf("store: area aggregates: %w", err)
	}
	defer rows.Close()

	var out []AreaAggregate
	for rows.Next() {
		var a AreaAggregate
		var values map[string]float64
		if err := rows.Scan(&a.Slug, &a.Kind, &a.NameBG, &a.NameEN,
			&a.CentroidLon, &a.CentroidLat, &a.DefaultZoom,
			&a.SensorCount, &values); err != nil {
			return nil, fmt.Errorf("store: scan area aggregate: %w", err)
		}
		a.Covered = a.SensorCount >= CoverageThreshold
		if a.Covered {
			a.Values = values
		} else {
			// Explicitly empty, not the scanned map. An uncovered area must
			// carry no number anywhere downstream — a handler that checked
			// Covered but serialised Values anyway would leak it.
			a.Values = map[string]float64{}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: area aggregates rows: %w", err)
	}
	return out, nil
}

type SensorReading struct {
	SensorID   int64
	SensorType string
	Lon        float64
	Lat        float64
	AreaSlugs  []string
	Quality    string
	Values     map[string]float64
}

const latestSensorsSQL = `
WITH latest AS (
    SELECT DISTINCT ON (r.sensor_id, r.metric)
           r.sensor_id, r.metric, r.value, r.quality
      FROM reading r
     WHERE r.time >= $1
     ORDER BY r.sensor_id, r.metric, r.time DESC
)
SELECT s.sensor_id, s.sensor_type,
       ST_X(s.location::geometry), ST_Y(s.location::geometry),
       COALESCE(
           (SELECT array_agg(asx.area_slug ORDER BY asx.area_slug)
              FROM area_sensor asx WHERE asx.sensor_id = s.sensor_id),
           ARRAY[]::text[]),
       -- The worst flag on any of this sensor's metrics, so one bad metric
       -- marks the sensor rather than being averaged away. The FILTER excludes
       -- 'ok' rows before max() runs, so any surviving non-ok flag wins; only
       -- if every metric is 'ok' does max() see nothing and COALESCE to 'ok'.
       COALESCE(max(l.quality::text) FILTER (WHERE l.quality <> 'ok'), 'ok'),
       jsonb_object_agg(l.metric, round(l.value::numeric, 2))
           FILTER (WHERE l.quality = ANY($2::quality_flag[]))
  FROM sensor s
  JOIN latest l ON l.sensor_id = s.sensor_id
 GROUP BY s.sensor_id, s.sensor_type, s.location
 ORDER BY s.sensor_id`

// LatestSensors returns one row per sensor with a fresh reading, carrying every
// usable metric value. Grouping happens in SQL: the naive join returns one row
// per sensor-metric pair, and a caller assembling those in Go is one forgotten
// map lookup away from emitting seven markers where one belongs.
func (s *Store) LatestSensors(ctx context.Context) ([]SensorReading, error) {
	since := time.Now().UTC().Add(-freshnessWindow)

	rows, err := s.pool.Query(ctx, latestSensorsSQL, since, usableQuality)
	if err != nil {
		return nil, fmt.Errorf("store: latest sensors: %w", err)
	}
	defer rows.Close()

	var out []SensorReading
	for rows.Next() {
		var sr SensorReading
		var values map[string]float64
		if err := rows.Scan(&sr.SensorID, &sr.SensorType, &sr.Lon, &sr.Lat,
			&sr.AreaSlugs, &sr.Quality, &values); err != nil {
			return nil, fmt.Errorf("store: scan sensor: %w", err)
		}
		if values == nil {
			values = map[string]float64{}
		}
		sr.Values = values
		out = append(out, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: latest sensors rows: %w", err)
	}
	return out, nil
}

type Point struct {
	Time  time.Time
	Value float64
}

const rawSeriesSQL = `
SELECT time, value FROM reading
 WHERE sensor_id = $1 AND metric = $2 AND time >= $3
   AND quality = ANY($4::quality_flag[])
 ORDER BY time`

const hourlySeriesSQL = `
SELECT bucket, avg_value FROM reading_hourly
 WHERE sensor_id = $1 AND metric = $2 AND bucket >= $3
 ORDER BY bucket`

// SensorSeries returns a time series for one sensor and metric. hourly selects
// reading_hourly instead of reading.
//
// The caller decides which table, because the rule is a property of the
// requested period, not of the data: raw readings are retained 30 days
// (migration 00003), so any window reaching further back must come from
// reading_hourly or it silently returns a truncated series that looks complete.
func (s *Store) SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool) ([]Point, error) {
	var rows interface {
		Next() bool
		Scan(...any) error
		Err() error
		Close()
	}
	var err error

	if hourly {
		rows, err = s.pool.Query(ctx, hourlySeriesSQL, sensorID, metric, since)
	} else {
		rows, err = s.pool.Query(ctx, rawSeriesSQL, sensorID, metric, since, usableQuality)
	}
	if err != nil {
		return nil, fmt.Errorf("store: sensor series: %w", err)
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, fmt.Errorf("store: scan point: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: sensor series rows: %w", err)
	}
	return out, nil
}
