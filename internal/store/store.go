// Package store persists sensors and readings.
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/config"
	"airbg.org/internal/quality"
)

type Store struct {
	pool *pgxpool.Pool
	cfg  config.Store
	// seriesTimeout scopes statement_timeout for the series-shaped reads
	// (SensorSeries, AreaSeries, AreaAtPoint) below the pool-wide default —
	// see internal/db.StatementTimeoutValue.
	seriesTimeout time.Duration
}

func New(pool *pgxpool.Pool, cfg config.Store, seriesTimeout time.Duration) *Store {
	return &Store{pool: pool, cfg: cfg, seriesTimeout: seriesTimeout}
}

// UpsertSensors records every distinct sensor in the batch. Location is
// refreshed on conflict because sensors are occasionally relocated upstream.
// country maps sensor ID to the ISO 3166-1 alpha-2 code of the boundary that
// admitted it, as decided geometrically by area.FilterByBoundary. A sensor
// missing from the map keeps whatever code it already had rather than being
// reset to NULL: COALESCE, not EXCLUDED, below.
func (s *Store) UpsertSensors(ctx context.Context, scored []quality.Scored, country map[int64]string) error {
	seen := make(map[int64]bool, len(scored))
	batch := &pgx.Batch{}

	for _, sc := range scored {
		r := sc.Reading
		if seen[r.SensorID] {
			continue
		}
		seen[r.SensorID] = true
		var code *string
		if c, ok := country[r.SensorID]; ok {
			code = &c
		}
		batch.Queue(
			`INSERT INTO sensor (sensor_id, sensor_type, location, last_seen, country_code)
			 VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5, $6)
			 ON CONFLICT (sensor_id) DO UPDATE
			   SET location = EXCLUDED.location,
			       sensor_type = EXCLUDED.sensor_type,
			       last_seen = EXCLUDED.last_seen,
			       country_code = COALESCE(EXCLUDED.country_code, sensor.country_code),
			       active = true`,
			r.SensorID, r.SensorType, r.Lon, r.Lat, r.Timestamp, code)
	}
	if batch.Len() == 0 {
		return nil
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

// WriteReadings persists every scored reading, including flagged ones. Duplicate
// samples are upserted (value and quality overwritten) rather than erroring,
// so a re-run of the same cycle is safe. Returns the number of statements sent.
func (s *Store) WriteReadings(ctx context.Context, scored []quality.Scored) (int64, error) {
	batch := &pgx.Batch{}
	for _, sc := range scored {
		r := sc.Reading
		batch.Queue(
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (sensor_id, metric, time) DO UPDATE
			   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
			r.Timestamp, r.SensorID, r.Metric, r.Value, string(sc.Flag))
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return int64(len(scored)), nil
}

// TruncateHour returns the UTC hour bucket containing t.
func TruncateHour(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// Pool exposes the underlying connection pool for callers that need ad-hoc
// reads, such as tests and the API's chart queries.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
