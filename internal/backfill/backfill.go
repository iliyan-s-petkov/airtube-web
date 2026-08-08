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
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

// ParseCSV reads one archive CSV and aggregates it into hourly buckets.
// Unparseable rows are skipped rather than failing the file — an archive day
// with one corrupt line still yields a usable import.
func ParseCSV(r io.Reader, sensorID int64) ([]HourlyBucket, error) {
	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("backfill: read header: %w", err)
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
		return nil, fmt.Errorf("backfill: no timestamp column")
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
	return buckets, nil
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
