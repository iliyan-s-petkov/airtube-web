package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"airbg.org/internal/db"
)

// usableQuality is the quality filter every published aggregate applies.
// 'no_neighbours' is usable: it records that the spatial-outlier check had
// nothing to compare against, not that the reading failed it. Excluding it
// would silently drop every rural sensor.
var usableQuality = []string{"ok", "no_neighbours"}

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
	since := time.Now().UTC().Add(-s.cfg.FreshnessWindow)

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
		a.Covered = a.SensorCount >= s.cfg.CoverageThreshold
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
	since := time.Now().UTC().Add(-s.cfg.FreshnessWindow)

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

// bucketExpr is time_bucket with the width supplied as a query parameter rather
// than a literal. make_interval takes the seconds because pgx has no unambiguous
// mapping from time.Duration to interval, and an interval built from seconds is
// fixed-width — which is what time_bucket needs for a stable origin.
const bucketExpr = `time_bucket(make_interval(secs => $%d::double precision), %s)`

func bucketed(col string, param int) string {
	return fmt.Sprintf(bucketExpr, param, col)
}

var (
	rawSeriesSQL = `
SELECT ` + bucketed("time", 5) + ` AS b, avg(value) FROM reading
 WHERE sensor_id = $1 AND metric = $2 AND time >= $3
   AND quality = ANY($4::quality_flag[])
 GROUP BY b
 ORDER BY b`

	hourlySeriesSQL = `
SELECT ` + bucketed("bucket", 4) + ` AS b, avg(avg_value) FROM reading_hourly
 WHERE sensor_id = $1 AND metric = $2 AND bucket >= $3
 GROUP BY b
 ORDER BY b`
)

// SensorSeries returns a time series for one sensor and metric. hourly selects
// reading_hourly instead of reading.
//
// The caller decides which table, because the rule is a property of the
// requested period, not of the data: raw readings are retained 30 days
// (migration 00003), so any window reaching further back must come from
// reading_hourly or it silently returns a truncated series that looks complete.
// bucket is the resolution, a separate decision from hourly: hourly picks the
// table, bucket picks how many points come back.
func (s *Store) SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool, bucket time.Duration) ([]Point, error) {
	// A transaction only so statement_timeout can be scoped: set_config's local
	// flag is transaction-scoped, and this read must not inherit the pool-wide
	// 15s. Rolled back rather than committed — nothing is written, and a rollback
	// of a read-only transaction is the cheaper of the two.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: begin sensor series: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := db.SetLocalStatementTimeout(ctx, tx, db.StatementTimeoutValue(s.seriesTimeout)); err != nil {
		return nil, fmt.Errorf("store: sensor series timeout: %w", err)
	}

	var rows pgx.Rows
	if hourly {
		rows, err = tx.Query(ctx, hourlySeriesSQL, sensorID, metric, since, bucket.Seconds())
	} else {
		rows, err = tx.Query(ctx, rawSeriesSQL, sensorID, metric, since, usableQuality, bucket.Seconds())
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

// areaRawSeriesSQL averages across the area's sensors within each time bucket.
//
// The bucket is what makes this an aggregate. Grouping on the raw timestamp
// instead collapses nothing: sensors report asynchronously at second
// resolution, so equality on r.time almost never matches two rows and every
// avg() averages a single sensor. The result is one point per sensor per
// report, in timestamp order, which renders as a sawtooth a reader interprets
// as rapid air-quality swings rather than as sensors disagreeing.
//
// area_sensor carries area_slug directly (migration 00004) — there is no
// numeric area.id to join through.
var areaRawSeriesSQL = `
SELECT ` + bucketed("r.time", 5) + ` AS b, avg(r.value)
  FROM reading r
  JOIN area_sensor asx ON asx.sensor_id = r.sensor_id
  JOIN area a          ON a.slug = asx.area_slug
 WHERE a.slug   = $1
   AND r.metric = $2
   AND r.time  >= $3
   AND r.quality = ANY($4::quality_flag[])
 GROUP BY b
 ORDER BY b`

// areaHourlySeriesSQL is the same over the rollup. reading_hourly carries no
// quality column — the rollup is built from readings that already passed the
// filter, so re-filtering here would be impossible AND unnecessary.
var areaHourlySeriesSQL = `
SELECT ` + bucketed("h.bucket", 4) + ` AS b, avg(h.avg_value)
  FROM reading_hourly h
  JOIN area_sensor asx ON asx.sensor_id = h.sensor_id
  JOIN area a          ON a.slug = asx.area_slug
 WHERE a.slug   = $1
   AND h.metric = $2
   AND h.bucket >= $3
 GROUP BY b
 ORDER BY b`

// allAreaRawSeriesSQL is areaRawSeriesSQL for EVERY area in one round trip.
//
// The per-area query exists for the database-backed fall-through, where the
// caller has named one slug. This one exists for snapshot.Build, which needs all
// of them at once: looping the per-area query would issue one query per area per
// ingest cycle — hundreds once neighbourhood boundaries are imported — against
// the collector pool's four connections.
//
// Grouped by (slug, bucket), so a sensor belonging to two areas contributes to
// both means, and sensors reporting within one bucket produce one point.
// Ordered by slug then bucket, so the scan below can rely on time order within
// each slug without sorting afterwards.
//
// It must bucket on the same rule as areaRawSeriesSQL. The snapshot is built
// from this query and the fall-through is served by that one; if they disagree
// the same chart changes shape depending on whether the cache was warm.
var allAreaRawSeriesSQL = `
SELECT a.slug, ` + bucketed("r.time", 4) + ` AS b, avg(r.value)
  FROM reading r
  JOIN area_sensor asx ON asx.sensor_id = r.sensor_id
  JOIN area a          ON a.slug = asx.area_slug
 WHERE r.metric = $1
   AND r.time  >= $2
   AND r.quality = ANY($3::quality_flag[])
 GROUP BY a.slug, b
 ORDER BY a.slug, b`

// allAreaHourlySeriesSQL is the same over the rollup. reading_hourly carries no
// quality column: the rollup is built from readings that already passed the
// filter.
var allAreaHourlySeriesSQL = `
SELECT a.slug, ` + bucketed("h.bucket", 3) + ` AS b, avg(h.avg_value)
  FROM reading_hourly h
  JOIN area_sensor asx ON asx.sensor_id = h.sensor_id
  JOIN area a          ON a.slug = asx.area_slug
 WHERE h.metric = $1
   AND h.bucket >= $2
 GROUP BY a.slug, b
 ORDER BY a.slug, b`

// AllAreaSeries returns the area-mean series for one metric, for every area
// that has data in the window, keyed by slug.
//
// Areas with no readings are absent from the map rather than present with an
// empty slice. snapshot.Build iterates its known slugs and looks each one up, so
// a missing key is the correct representation of "no data" there — and a caller
// that needs an entry per area must iterate its own slug set, not this map.
func (s *Store) AllAreaSeries(ctx context.Context, metric string, since time.Time, hourly bool, bucket time.Duration) (map[string][]Point, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if hourly {
		rows, err = s.pool.Query(ctx, allAreaHourlySeriesSQL, metric, since, bucket.Seconds())
	} else {
		rows, err = s.pool.Query(ctx, allAreaRawSeriesSQL, metric, since, usableQuality, bucket.Seconds())
	}
	if err != nil {
		return nil, fmt.Errorf("store: all area series for %q: %w", metric, err)
	}
	defer rows.Close()

	out := make(map[string][]Point)
	for rows.Next() {
		var (
			slug string
			p    Point
		)
		if err := rows.Scan(&slug, &p.Time, &p.Value); err != nil {
			return nil, fmt.Errorf("store: scan all area series: %w", err)
		}
		out[slug] = append(out[slug], p)
	}
	return out, rows.Err()
}

// AreaSeries returns the area-mean time series for one metric.
//
// hourly selects the rollup. The caller decides, because only the caller knows
// the requested window — and raw readings are retained for 30 days, so a longer
// window queried against `reading` returns a silently truncated series rather
// than an error.
func (s *Store) AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool, bucket time.Duration) ([]Point, error) {
	// A transaction only so statement_timeout can be scoped: set_config's local
	// flag is transaction-scoped, and this read must not inherit the pool-wide
	// 15s. Rolled back rather than committed — nothing is written, and a rollback
	// of a read-only transaction is the cheaper of the two.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: begin area series: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := db.SetLocalStatementTimeout(ctx, tx, db.StatementTimeoutValue(s.seriesTimeout)); err != nil {
		return nil, fmt.Errorf("store: area series timeout: %w", err)
	}

	var rows pgx.Rows
	if hourly {
		rows, err = tx.Query(ctx, areaHourlySeriesSQL, slug, metric, since, bucket.Seconds())
	} else {
		rows, err = tx.Query(ctx, areaRawSeriesSQL, slug, metric, since, usableQuality, bucket.Seconds())
	}
	if err != nil {
		return nil, fmt.Errorf("store: area series for %q: %w", slug, err)
	}
	defer rows.Close()

	var points []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, fmt.Errorf("store: scan area series: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}
