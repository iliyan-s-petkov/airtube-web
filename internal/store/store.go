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

// OfficialSensorIDFloor is the id at and above which a sensor must be an EEA
// station. It mirrors migration 00012's CHECK and official_sensor_id_seq's
// START WITH. Callers filter on it before upserting so that a colliding
// upstream id is dropped as one row: pgx.Batch fails whole, so leaving it to
// the CHECK would cost the entire ingest cycle.
const OfficialSensorIDFloor int64 = 9_000_000_000

// UpsertSensors records every distinct sensor in the batch. Location is
// refreshed on conflict because sensors are occasionally relocated upstream.
// country maps sensor ID to the ISO 3166-1 alpha-2 code of the boundary that
// admitted it, as decided geometrically by area.FilterByBoundary. A sensor
// missing from the map keeps whatever code it already had rather than being
// reset to NULL: COALESCE, not EXCLUDED, below.
//
// The DO UPDATE is confined to rows this source owns. source, source_ref and
// the station_* columns are absent from both the column list and the SET list,
// so without the WHERE an upstream id colliding with an allocated official id
// would leave one row still badged 'eea' with its station identity intact, at
// the citizen device's coordinates, merging both devices' readings. The WHERE
// makes that a no-op; migration 00012's CHECK makes it a loud error.
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
			       active = true
			 WHERE sensor.source = 'sensor.community'`,
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

// StationUpsert is one official reference station, keyed by its EEA sampling
// point.
type StationUpsert struct {
	SourceRef string
	Code      string
	Name      string
	Type      string
	Area      string
	Lon       float64
	Lat       float64
	LastSeen  time.Time
}

// UpsertStations records every station and returns sampling point -> sensor_id.
// source_ref is unique, so the conflict path returns the id assigned on first
// insert; the id must stay stable or the reading history detaches from it.
func (s *Store) UpsertStations(ctx context.Context, sts []StationUpsert) (map[string]int64, error) {
	ids := make(map[string]int64, len(sts))
	if len(sts) == 0 {
		return ids, nil
	}

	batch := &pgx.Batch{}
	for _, st := range sts {
		batch.Queue(
			`INSERT INTO sensor (sensor_id, sensor_type, location, last_seen,
			                     source, source_ref, station_code, station_name,
			                     station_type, station_area)
			 VALUES (nextval('official_sensor_id_seq'), 'eea_reference',
			         ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3,
			         'eea', $4, $5, $6, $7, $8)
			 ON CONFLICT (source_ref) WHERE source_ref IS NOT NULL DO UPDATE
			   SET location     = EXCLUDED.location,
			       last_seen    = EXCLUDED.last_seen,
			       station_code = EXCLUDED.station_code,
			       station_name = EXCLUDED.station_name,
			       station_type = EXCLUDED.station_type,
			       station_area = EXCLUDED.station_area,
			       active       = true
			 RETURNING sensor_id, source_ref`,
			st.Lon, st.Lat, st.LastSeen, st.SourceRef, st.Code, st.Name, st.Type, st.Area)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range sts {
		var id int64
		var ref string
		if err := br.QueryRow().Scan(&id, &ref); err != nil {
			return nil, err
		}
		ids[ref] = id
	}
	return ids, nil
}

// StationReading is one hourly observation, already mapped to a canonical
// metric and normalised to µg/m³.
type StationReading struct {
	SensorID  int64
	Metric    string
	Value     float64
	Timestamp time.Time
	Quality   string
}

// WriteStationReadings persists hourly observations, flagged ones included.
// The UTD dataset is revised in place upstream, so a re-fetched hour
// overwrites.
func (s *Store) WriteStationReadings(ctx context.Context, rs []StationReading) (int64, error) {
	if len(rs) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, r := range rs {
		batch.Queue(
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (sensor_id, metric, time) DO UPDATE
			   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
			r.Timestamp, r.SensorID, r.Metric, r.Value, r.Quality)
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return int64(len(rs)), nil
}

// TruncateHour returns the UTC hour bucket containing t.
func TruncateHour(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// Pool exposes the underlying connection pool for callers that need ad-hoc
// reads, such as tests and the API's chart queries.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
