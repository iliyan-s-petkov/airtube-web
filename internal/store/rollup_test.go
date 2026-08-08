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

// TestRollupHourBucketBoundariesAreHalfOpen pins the half-open window
// [bucket, bucket+1h) used by RollupHour's WHERE clause. A reading at
// exactly the bucket start belongs to that bucket; a reading at exactly
// bucket+1h belongs to the *next* bucket, not this one. If the upper bound
// were changed to "<=" the T+1h reading would be double-counted into both
// buckets; if the lower bound were changed to ">" the T reading would be
// dropped from both. sample_count (not avg_value) is asserted because a
// double-count can leave the average unchanged when the duplicated value
// equals the mean.
func TestRollupHourBucketBoundariesAreHalfOpen(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucketStart := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	nextBucketStart := bucketStart.Add(time.Hour)

	scored := []quality.Scored{
		sample(1, "P1", 10, quality.FlagOK, bucketStart),                     // exactly T
		sample(1, "P1", 20, quality.FlagOK, bucketStart.Add(30*time.Minute)), // interior
		sample(1, "P1", 30, quality.FlagOK, nextBucketStart),                 // exactly T+1h
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	if _, err := s.RollupHour(ctx, bucketStart); err != nil {
		t.Fatalf("RollupHour(T): %v", err)
	}
	if _, err := s.RollupHour(ctx, nextBucketStart); err != nil {
		t.Fatalf("RollupHour(T+1h): %v", err)
	}

	var countT, countNext int
	err := pool.QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
		bucketStart).Scan(&countT)
	if err != nil {
		t.Fatalf("read bucket T: %v", err)
	}
	err = pool.QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
		nextBucketStart).Scan(&countNext)
	if err != nil {
		t.Fatalf("read bucket T+1h: %v", err)
	}

	if countT != 2 {
		t.Errorf("bucket T sample_count = %d, want 2 (T and interior) — upper bound leaked or lower bound dropped a reading", countT)
	}
	if countNext != 1 {
		t.Errorf("bucket T+1h sample_count = %d, want 1 (only T+1h) — upper bound of previous bucket leaked this reading in", countNext)
	}
	if countT+countNext != 3 {
		t.Errorf("countT+countNext = %d, want 3 — a reading was double-counted or dropped", countT+countNext)
	}
}

func TestTruncateHour(t *testing.T) {
	got := store.TruncateHour(time.Date(2026, 1, 1, 12, 34, 56, 0, time.UTC))
	want := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("TruncateHour = %v, want %v", got, want)
	}
}
