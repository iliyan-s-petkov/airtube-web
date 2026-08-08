package ingest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

type stubFetcher struct {
	readings []upstream.Reading
	err      error
}

func (s stubFetcher) Fetch(context.Context) ([]upstream.Reading, error) {
	return s.readings, s.err
}

func reading(id int64, metric string, value float64, lonOffset float64, ts time.Time) upstream.Reading {
	return upstream.Reading{
		SensorID:   id,
		SensorType: "BME280",
		Lon:        23.3327 + lonOffset,
		Lat:        42.6957,
		Metric:     metric,
		Value:      value,
		Timestamp:  ts,
	}
}

func newIngester(t *testing.T, f ingest.Fetcher) (context.Context, *store.Store, *ingest.Ingester) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st := store.New(pool)
	return ctx, st, ingest.New(f, st, quality.NewHistory(12))
}

func TestRunOnceStoresScoredReadings(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 3, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "temperature", 22, 0, ts),
		reading(2, "temperature", -10, 0.01, ts),
		reading(3, "temperature", -10.5, 0.02, ts),
		reading(4, "temperature", -9.5, 0.03, ts),
	}}
	ctx, _, ing := newIngester(t, f)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fetched != 4 {
		t.Errorf("Fetched = %d, want 4", stats.Fetched)
	}
	if stats.Written != 4 {
		t.Errorf("Written = %d, want 4 — flagged readings must still be stored", stats.Written)
	}
	if stats.Flagged[quality.FlagSpatialOutlier] != 1 {
		t.Errorf("spatial_outlier count = %d, want 1", stats.Flagged[quality.FlagSpatialOutlier])
	}
}

func TestRunOncePropagatesFetchFailure(t *testing.T) {
	wantErr := errors.New("upstream down")
	ctx, _, ing := newIngester(t, stubFetcher{err: wantErr})

	if _, err := ing.RunOnce(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestRunOnceHandlesEmptyBatch(t *testing.T) {
	ctx, _, ing := newIngester(t, stubFetcher{})

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce on empty batch: %v", err)
	}
	if stats.Written != 0 {
		t.Errorf("Written = %d, want 0", stats.Written)
	}
}

func TestRunOnceUpdatesRollup(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 3, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "P1", 20, 0, ts),
	}}
	ctx, st, ing := newIngester(t, f)

	if _, err := ing.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// The rollup for the current hour must exist after a cycle.
	var count int
	err := st.Pool().QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1'`).
		Scan(&count)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if count != 1 {
		t.Errorf("sample_count = %d, want 1", count)
	}
}
