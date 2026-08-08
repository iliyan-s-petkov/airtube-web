package backfill_test

import (
	"context"
	"os"
	"strings"
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

// TestParseCSVBucketsExactHourBoundary pins that a reading timestamped
// exactly on the hour lands in that hour's bucket (time.Truncate is
// inclusive of the boundary instant, not the previous hour).
func TestParseCSVBucketsExactHourBoundary(t *testing.T) {
	const csvData = "sensor_id;sensor_type;location;lat;lon;timestamp;P1;durP1;ratioP1;P2;durP2;ratioP2\n" +
		"12345;SDS011;500;42.696;23.333;2025-08-07T11:00:00;40.00;;;20.00;;\n"

	buckets, err := backfill.ParseCSV(strings.NewReader(csvData), 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	want := time.Date(2025, 8, 7, 11, 0, 0, 0, time.UTC)
	for _, b := range buckets {
		if b.Metric == "P1" && !b.Bucket.Equal(want) {
			t.Errorf("P1 bucket = %v, want %v", b.Bucket, want)
		}
	}
}

// TestParseCSVConvertsPressurePascalsToHPa pins the Pascals→hPa conversion
// (backfill.go pressure branch). sample.csv has no pressure column, so this
// path has no coverage from the other tests; a regression that dropped,
// duplicated, or misapplied the /100 conversion would pass every other test
// in this file.
func TestParseCSVConvertsPressurePascalsToHPa(t *testing.T) {
	f, err := os.Open("testdata/pressure.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	want := time.Date(2025, 8, 7, 10, 0, 0, 0, time.UTC)
	var pressureFound, temperatureFound bool
	for _, b := range buckets {
		if !b.Bucket.Equal(want) {
			continue
		}
		switch b.Metric {
		case "pressure":
			pressureFound = true
			// 95000 Pa and 96000 Pa -> 950 hPa and 960 hPa.
			if b.Avg != 955 {
				t.Errorf("pressure avg = %v, want 955 (hPa)", b.Avg)
			}
			if b.Min != 950 || b.Max != 960 {
				t.Errorf("pressure min/max = %v/%v, want 950/960", b.Min, b.Max)
			}
		case "temperature":
			temperatureFound = true
			// 22.50 and 23.50, untouched by the pressure conversion.
			if b.Avg != 23 {
				t.Errorf("temperature avg = %v, want 23 (unconverted)", b.Avg)
			}
			if b.Min != 22.5 || b.Max != 23.5 {
				t.Errorf("temperature min/max = %v/%v, want 22.5/23.5", b.Min, b.Max)
			}
		}
	}
	if !pressureFound {
		t.Fatal("no pressure bucket for 10:00")
	}
	if !temperatureFound {
		t.Fatal("no temperature bucket for 10:00")
	}
}

// TestParseCSVSkipsMalformedRowsButKeepsGoodOnes pins the tolerance that lets
// one bad archive row survive without losing the rest of the file — the same
// class of fix as the live client's unquoted-field tolerance. testdata/malformed.csv
// mixes a row missing the timestamp column, a row with an unparseable float, a
// row with an unparseable timestamp, and a row with a CSV syntax error (bare
// quote) among otherwise-good rows. A regression that aborted the whole file
// on the first bad row, or that returned nil but silently dropped rows after
// it, must fail this test: the assertion is on the surviving values, not on
// "no error returned".
func TestParseCSVSkipsMalformedRowsButKeepsGoodOnes(t *testing.T) {
	f, err := os.Open("testdata/malformed.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if len(buckets) != 2 {
		t.Fatalf("len(buckets) = %d, want 2 (P1, P2 for the single surviving hour)", len(buckets))
	}

	want := time.Date(2025, 8, 7, 10, 0, 0, 0, time.UTC)
	var p1Found, p2Found bool
	for _, b := range buckets {
		if !b.Bucket.Equal(want) {
			t.Errorf("bucket %v not in surviving hour %v", b.Bucket, want)
			continue
		}
		switch b.Metric {
		case "P1":
			p1Found = true
			// Good rows only: 20.00 (10:05) and 30.00 (10:45). The
			// unparseable-float row's P1 and the bare-quote row's P1 are
			// dropped; the missing-timestamp and bad-timestamp rows never
			// reach metric parsing at all.
			if b.Count != 2 {
				t.Errorf("P1 count = %d, want 2", b.Count)
			}
			if b.Avg != 25 {
				t.Errorf("P1 avg = %v, want 25", b.Avg)
			}
			if b.Min != 20 || b.Max != 30 {
				t.Errorf("P1 min/max = %v/%v, want 20/30", b.Min, b.Max)
			}
		case "P2":
			p2Found = true
			// Good rows: 10.00 (10:05), 12.00 (10:25, alongside the bad P1
			// float on the same row), and 14.00 (10:45).
			if b.Count != 3 {
				t.Errorf("P2 count = %d, want 3", b.Count)
			}
			if b.Avg != 12 {
				t.Errorf("P2 avg = %v, want 12", b.Avg)
			}
			if b.Min != 10 || b.Max != 14 {
				t.Errorf("P2 min/max = %v/%v, want 10/14", b.Min, b.Max)
			}
		}
	}
	if !p1Found {
		t.Fatal("no P1 bucket survived")
	}
	if !p2Found {
		t.Fatal("no P2 bucket survived")
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
