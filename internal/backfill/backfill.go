// Package backfill imports historical hourly data from the public
// sensor.community archive (archive.sensor.community), which publishes one CSV
// per sensor per day.
//
// Only hourly buckets are imported. Raw rows are dropped after 30 days by the
// retention policy, so importing raw history would be deleted almost
// immediately (spec §5.2, §5.3).
package backfill

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/quality"
	"airbg.org/internal/upstream"
)

const archiveTimeLayout = "2006-01-02T15:04:05"

type HourlyBucket struct {
	SensorID int64
	Metric   string
	Bucket   time.Time
	Avg      float64
	Min      float64
	Max      float64
	Count    int
}

type accumulator struct {
	sum   float64
	min   float64
	max   float64
	count int
}

type key struct {
	metric string
	bucket time.Time
}

// ParseReport records what ParseCSV dropped and why, so a rejection is a
// reported fact rather than a silent one. An archive file that is 90%
// rejected — a dead sensor's day, or an upstream format change — must be
// visible to the operator running the import, not quietly folded into a
// plausible-looking average.
type ParseReport struct {
	// Values is every metric cell that parsed as a float, before filtering.
	Values int
	// Accepted is the number folded into an hourly bucket.
	Accepted int
	// RejectedNonFinite counts NaN and ±Inf cells. These are counted apart
	// from ordinary out-of-range values because they are categorically worse:
	// a single one poisons its whole bucket's avg/min/max irrecoverably (see
	// the filter's own comment) and has no defensible reading.
	RejectedNonFinite int
	// RejectedOutOfRange counts finite cells outside the metric's plausible
	// range — the −999 sentinels a dead sensor emits, most often.
	RejectedOutOfRange int
	// RejectedByMetric breaks the rejections down per metric, so "this
	// sensor's humidity channel is dead" is distinguishable from "the whole
	// file is junk".
	RejectedByMetric map[string]int
}

// Rejected is the total number of metric cells dropped.
func (r ParseReport) Rejected() int { return r.RejectedNonFinite + r.RejectedOutOfRange }

// RejectedFraction is the share of parseable cells that were dropped, in the
// range 0..1. It returns 0 for an empty file rather than NaN — reporting NaN
// from the function that exists to keep NaN out of the database would be a
// poor joke.
func (r ParseReport) RejectedFraction() float64 {
	if r.Values == 0 {
		return 0
	}
	return float64(r.Rejected()) / float64(r.Values)
}

// HighRejectionFraction is the share of rejected values above which an import
// is reported at ERROR rather than WARN. Half the file being unusable is not a
// dead channel on an otherwise healthy sensor; it means either the sensor was
// broken for that day or the archive format has moved, and in both cases the
// buckets that *did* import are built from a minority of the data and should
// not pass for a normal import.
const HighRejectionFraction = 0.5

// Level is the slog level an import of this report should be logged at, so the
// severity rule lives next to the counters it reads and can be asserted
// directly rather than re-derived at each call site.
//
// Total rejection is deliberately ERROR even when the file parsed cleanly: an
// archive file from which nothing at all survived filtering stored nothing, and
// "stored nothing" must never be reported at the same level as a successful
// import. That is the same fail-loud rule ingest.RunOnce applies to a cycle
// that fetched successfully and salvaged nothing.
func (r ParseReport) Level() slog.Level {
	switch {
	case r.Values > 0 && r.Accepted == 0:
		return slog.LevelError
	case r.RejectedFraction() >= HighRejectionFraction:
		return slog.LevelError
	case r.Rejected() > 0:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// LogAttrs returns the report as slog key/value pairs.
func (r ParseReport) LogAttrs() []any {
	return []any{
		"values", r.Values,
		"accepted", r.Accepted,
		"rejected", r.Rejected(),
		"rejected_non_finite", r.RejectedNonFinite,
		"rejected_out_of_range", r.RejectedOutOfRange,
		"rejected_fraction", fmt.Sprintf("%.3f", r.RejectedFraction()),
		"rejected_by_metric", fmt.Sprint(r.RejectedByMetric),
	}
}

// ParseCSV reads one archive CSV and aggregates it into hourly buckets.
// Unparseable rows are skipped rather than failing the file — an archive day
// with one corrupt line still yields a usable import.
//
// Values are filtered to the same quality.InRange standard the live ingest
// path applies (internal/quality/score.go), and non-finite values are rejected
// outright. This deliberately supersedes task 13's brief, which specified no
// filtering here; that brief predates the whole-branch evidence below, and the
// unfiltered behaviour is the defect:
//
//   - strconv.ParseFloat accepts "nan", "NaN", "inf", "+Inf" and "Infinity",
//     so a non-finite cell is reachable from archive text. One such cell makes
//     the bucket's sum NaN, hence its avg NaN. min/max are worse: `value <
//     a.min` is false for NaN, so a NaN arriving first seeds both and no later
//     value can ever displace it.
//   - Postgres accepts NaN in `double precision NOT NULL`, so it stores
//     silently, and nothing ever rewrites a historical hourly bucket — the
//     live rollup only touches buckets at or after its watermark. A poisoned
//     row therefore persists for reading_hourly's full 2-year retention.
//   - encoding/json.Marshal returns UnsupportedValueError for NaN and Inf, so
//     in Phase 2 one poisoned row does not spoil one chart: it fails the
//     entire JSON response containing it, every time, until someone finds and
//     deletes the row by hand.
//   - Even setting NaN aside, an unfiltered −999 sentinel enters the
//     historical hourly mean and is then indistinguishable from a live-path
//     bucket built to the quality IN ('ok','no_neighbours') standard. No
//     column records which standard produced a row, so historical and live
//     buckets must be built to one standard or the difference is unknowable
//     after the fact.
//
// Filtering happens per value, not per row: a row whose humidity channel reads
// −999 still contributes its good P1 and P2 cells.
func ParseCSV(r io.Reader, sensorID int64) ([]HourlyBucket, ParseReport, error) {
	report := ParseReport{RejectedByMetric: map[string]int{}}

	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, report, fmt.Errorf("backfill: read header: %w", err)
	}

	tsCol := -1
	metricCols := map[int]string{}
	for i, name := range header {
		if name == "timestamp" {
			tsCol = i
			continue
		}
		if upstream.IsCanonicalMetric(name) {
			metricCols[i] = name
		}
	}
	if tsCol == -1 {
		return nil, report, fmt.Errorf("backfill: no timestamp column")
	}

	acc := map[key]*accumulator{}

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed row, keep the file
		}
		if tsCol >= len(record) {
			continue
		}
		ts, err := time.Parse(archiveTimeLayout, record[tsCol])
		if err != nil {
			continue
		}
		bucket := ts.UTC().Truncate(time.Hour)

		for col, metric := range metricCols {
			if col >= len(record) || record[col] == "" {
				continue
			}
			value, err := strconv.ParseFloat(record[col], 64)
			if err != nil {
				continue
			}
			if metric == "pressure" {
				value /= 100 // archive matches the live API: Pascals
			}

			// Conversion happens before filtering, because the range bounds
			// are expressed in canonical units (pressure in hPa, not Pa).
			report.Values++
			switch {
			case math.IsNaN(value) || math.IsInf(value, 0):
				report.RejectedNonFinite++
				report.RejectedByMetric[metric]++
				continue
			case !quality.InRange(metric, value):
				report.RejectedOutOfRange++
				report.RejectedByMetric[metric]++
				continue
			}
			report.Accepted++

			k := key{metric: metric, bucket: bucket}
			a, ok := acc[k]
			if !ok {
				acc[k] = &accumulator{sum: value, min: value, max: value, count: 1}
				continue
			}
			a.sum += value
			a.count++
			if value < a.min {
				a.min = value
			}
			if value > a.max {
				a.max = value
			}
		}
	}

	buckets := make([]HourlyBucket, 0, len(acc))
	for k, a := range acc {
		buckets = append(buckets, HourlyBucket{
			SensorID: sensorID,
			Metric:   k.metric,
			Bucket:   k.bucket,
			Avg:      a.sum / float64(a.count),
			Min:      a.min,
			Max:      a.max,
			Count:    a.count,
		})
	}
	return buckets, report, nil
}

// CheckSensorInBoundary refuses a backfill for any sensor_id that is not
// already a known sensor inside the national boundary.
//
// Live ingest filters every reading through area.FilterByBoundary before it can
// reach storage; backfill wrote straight to reading_hourly with whatever
// sensor_id the operator typed, applying no such filter. That was not merely an
// inconsistency, it was unrecoverable: reading.sensor_id has no foreign key to
// sensor(sensor_id) (reading is a hypertable), so a backfill under a sensor_id
// that was never ingested left rows that PurgeOutsideBoundary — which discovers
// its work by selecting from `sensor` — could not see. The single command
// documented as the cleanup for foreign data could not clean it.
//
// Checking here, at the entry point, is the cheap half of the fix: it costs one
// query on an operator-invoked command and prevents the orphan existing at all.
// PurgeOutsideBoundary closes the other half by reaching orphans already
// stored. Between them, no row in reading or reading_hourly is beyond the
// cleanup's reach.
//
// It fails closed on a missing boundary for the same reason ingest and purge do:
// with nothing to test membership against, "allow it" would silently reopen the
// hole, and the remedy is one already-documented command.
func CheckSensorInBoundary(ctx context.Context, pool *pgxpool.Pool, sensorID int64) error {
	var boundaryPresent bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM area WHERE kind = $1)`, area.NationalBoundaryKind).
		Scan(&boundaryPresent); err != nil {
		return fmt.Errorf("backfill: check boundary: %w", err)
	}
	if !boundaryPresent {
		return fmt.Errorf(
			"backfill: no boundary of kind %q imported — refusing to write readings that could not later be purged (run: airbg import-areas <path.geojson> %s)",
			area.NationalBoundaryKind, area.NationalBoundaryKind)
	}

	var known, inside bool
	err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM sensor WHERE sensor_id = $1),
			EXISTS (
				SELECT 1 FROM sensor s
				JOIN area a ON a.kind = $2 AND ST_Covers(a.geom, s.location)
				WHERE s.sensor_id = $1
			)`, sensorID, area.NationalBoundaryKind).Scan(&known, &inside)
	if err != nil {
		return fmt.Errorf("backfill: check sensor %d: %w", sensorID, err)
	}

	switch {
	case !known:
		return fmt.Errorf(
			"backfill: sensor %d is not a known sensor — backfilling it would create reading_hourly rows with no sensor row, which purge-outside-boundary reaches only as orphans; ingest the sensor first",
			sensorID)
	case !inside:
		return fmt.Errorf(
			"backfill: sensor %d is outside the national boundary — archive history for a foreign sensor must not be imported",
			sensorID)
	}
	return nil
}

// WriteBuckets upserts hourly buckets. Re-importing the same day is safe.
func WriteBuckets(ctx context.Context, pool *pgxpool.Pool, buckets []HourlyBucket) (int64, error) {
	batch := &pgx.Batch{}
	for _, b := range buckets {
		batch.Queue(
			`INSERT INTO reading_hourly
			     (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (sensor_id, metric, bucket) DO UPDATE
			   SET avg_value = EXCLUDED.avg_value,
			       min_value = EXCLUDED.min_value,
			       max_value = EXCLUDED.max_value,
			       sample_count = EXCLUDED.sample_count`,
			b.Bucket, b.SensorID, b.Metric, b.Avg, b.Min, b.Max, b.Count)
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return int64(len(buckets)), nil
}
