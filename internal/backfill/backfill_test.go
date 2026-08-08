package backfill_test

import (
	"context"
	"os"
	"testing"
	"time"

	"airbg.org/internal/backfill"
	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

func TestParseCSVGroupsIntoHourlyBuckets(t *testing.T) {
	f, err := os.Open("testdata/sample.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	// Two hours × two metrics.
	if len(buckets) != 4 {
		t.Fatalf("len(buckets) = %d, want 4", len(buckets))
	}

	want := time.Date(2025, 8, 7, 10, 0, 0, 0, time.UTC)
	var found bool
	for _, b := range buckets {
		if b.Metric != "P1" || !b.Bucket.Equal(want) {
			continue
		}
		found = true
		if b.Avg != 25 {
			t.Errorf("P1 10:00 avg = %v, want 25", b.Avg)
		}
		if b.Min != 20 || b.Max != 30 {
			t.Errorf("P1 10:00 min/max = %v/%v, want 20/30", b.Min, b.Max)
		}
		if b.Count != 2 {
			t.Errorf("P1 10:00 count = %d, want 2", b.Count)
		}
	}
	if !found {
		t.Fatal("no P1 bucket for 10:00")
	}
}

func TestParseCSVSkipsNonCanonicalColumns(t *testing.T) {
	f, err := os.Open("testdata/sample.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	for _, b := range buckets {
		if b.Metric != "P1" && b.Metric != "P2" {
			t.Errorf("non-canonical metric %q in output", b.Metric)
		}
	}
}

func TestWriteBucketsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	buckets := []backfill.HourlyBucket{{
		SensorID: 12345,
		Metric:   "P1",
		Bucket:   time.Date(2025, 8, 7, 10, 0, 0, 0, time.UTC),
		Avg:      25, Min: 20, Max: 30, Count: 2,
	}}

	for i := 0; i < 2; i++ {
		if _, err := backfill.WriteBuckets(ctx, pool, buckets); err != nil {
			t.Fatalf("WriteBuckets run %d: %v", i, err)
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
