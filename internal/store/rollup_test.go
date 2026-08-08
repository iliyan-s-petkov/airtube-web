package store_test

import (
	"testing"
	"time"

	"airbg.org/internal/quality"
	"airbg.org/internal/store"
)

func TestRollupHourExcludesFlaggedReadings(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucket := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{
		sample(1, "P1", 20, quality.FlagOK, bucket.Add(1*time.Minute)),
		sample(1, "P1", 30, quality.FlagOK, bucket.Add(2*time.Minute)),
		// A spatial outlier in the same bucket. If this leaks into the average,
		// the published number is 316 instead of 25.
		sample(1, "P1", 900, quality.FlagSpatialOutlier, bucket.Add(3*time.Minute)),
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	if _, err := s.RollupHour(ctx, bucket); err != nil {
		t.Fatalf("RollupHour: %v", err)
	}

	var avg float64
	var count int
	err := pool.QueryRow(ctx,
		`SELECT avg_value, sample_count FROM reading_hourly
		 WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`, bucket).
		Scan(&avg, &count)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if avg != 25 {
		t.Errorf("avg_value = %v, want 25 — flagged reading contaminated the average", avg)
	}
	if count != 2 {
		t.Errorf("sample_count = %d, want 2", count)
	}
}

func TestRollupHourIncludesNoNeighbours(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucket := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{
		sample(1, "P1", 20, quality.FlagOK, bucket.Add(1*time.Minute)),
		sample(1, "P1", 30, quality.FlagNoNeighbours, bucket.Add(2*time.Minute)),
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}
	if _, err := s.RollupHour(ctx, bucket); err != nil {
		t.Fatalf("RollupHour: %v", err)
	}

	var count int
	err := pool.QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1'`).
		Scan(&count)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if count != 2 {
		t.Errorf("sample_count = %d, want 2 — no_neighbours must count toward aggregates", count)
	}
}

func TestRollupHourIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucket := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{sample(1, "P1", 20, quality.FlagOK, bucket.Add(time.Minute))}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := s.RollupHour(ctx, bucket); err != nil {
			t.Fatalf("RollupHour run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading_hourly rows = %d, want 1", n)
	}
}

func TestTruncateHour(t *testing.T) {
	got := store.TruncateHour(time.Date(2026, 1, 1, 12, 34, 56, 0, time.UTC))
	want := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("TruncateHour = %v, want %v", got, want)
	}
}
