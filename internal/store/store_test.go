package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

// testStoreConfig mirrors airbg.yaml so existing coverage assertions keep
// asserting the same threshold.
func testStoreConfig() config.Store {
	return config.Store{CoverageThreshold: 3, FreshnessWindow: 2 * time.Hour}
}

// testSeriesTimeout and testAssignTimeout mirror airbg.yaml's
// database.statement_timeouts.series and .assign.
const (
	testSeriesTimeout = 5 * time.Second
	testAssignTimeout = 60 * time.Second
)

func newStore(t *testing.T) (context.Context, *pgxpool.Pool, *store.Store) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, pool, store.New(pool, testStoreConfig(), testSeriesTimeout)
}

func sample(id int64, metric string, value float64, flag quality.Flag, ts time.Time) quality.Scored {
	return quality.Scored{
		Reading: upstream.Reading{
			SensorID:   id,
			SensorType: "SDS011",
			Lon:        23.3327,
			Lat:        42.6957,
			Metric:     metric,
			Value:      value,
			Timestamp:  ts,
		},
		Flag: flag,
	}
}

func TestUpsertSensorsIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{
		sample(1, "P1", 24.3, quality.FlagOK, ts),
		sample(1, "P2", 16.1, quality.FlagOK, ts),
	}

	for i := 0; i < 2; i++ {
		if err := s.UpsertSensors(ctx, scored, nil); err != nil {
			t.Fatalf("UpsertSensors: %v", err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("sensor count = %d, want 1", n)
	}
}

func TestUpsertSensorsStoresCoordinatesInBulgaria(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if err := s.UpsertSensors(ctx, []quality.Scored{sample(1, "P1", 24.3, quality.FlagOK, ts)}, nil); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}

	var lon, lat float64
	err := pool.QueryRow(ctx,
		`SELECT ST_X(location::geometry), ST_Y(location::geometry) FROM sensor WHERE sensor_id = 1`).
		Scan(&lon, &lat)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if lon < 22 || lon > 29 || lat < 41 || lat > 45 {
		t.Errorf("stored (%v, %v) is outside Bulgaria — coordinates swapped", lon, lat)
	}
}

func TestWriteReadingsPersistsFlags(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{
		sample(1, "P1", 24.3, quality.FlagOK, ts),
		sample(2, "P1", 900, quality.FlagSpatialOutlier, ts),
	}
	if err := s.UpsertSensors(ctx, scored, nil); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}

	n, err := s.WriteReadings(ctx, scored)
	if err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d rows, want 2", n)
	}

	var flag string
	err = pool.QueryRow(ctx,
		`SELECT quality::text FROM reading WHERE sensor_id = 2`).Scan(&flag)
	if err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if flag != "spatial_outlier" {
		t.Errorf("flag = %q, want %q — bad readings must be stored, not dropped", flag, "spatial_outlier")
	}
}

func TestWriteReadingsIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{sample(1, "P1", 24.3, quality.FlagOK, ts)}

	if err := s.UpsertSensors(ctx, scored, nil); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.WriteReadings(ctx, scored); err != nil {
			t.Fatalf("WriteReadings run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading count = %d, want 1", n)
	}
}
