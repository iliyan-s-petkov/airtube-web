package backfill_test

import (
	"context"
	"log/slog"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/backfill"
	"airbg.org/internal/db"
	"airbg.org/internal/quality"
	"airbg.org/internal/testsupport"
)

func TestParseCSVGroupsIntoHourlyBuckets(t *testing.T) {
	f, err := os.Open("testdata/sample.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, _, err := backfill.ParseCSV(f, 12345)
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

	buckets, _, err := backfill.ParseCSV(f, 12345)
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

	buckets, _, err := backfill.ParseCSV(strings.NewReader(csvData), 12345)
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

	buckets, _, err := backfill.ParseCSV(f, 12345)
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

	buckets, _, err := backfill.ParseCSV(f, 12345)
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

// TestParseCSVRejectsNonFiniteAndOutOfRangeValues is the regression test for
// the unfiltered-archive-values defect. testdata/poisoned.csv mixes every
// non-finite spelling strconv.ParseFloat accepts ("nan", "NaN", "inf", "+Inf",
// "Infinity") with −999 sentinels and an out-of-range humidity, across four
// metrics in one hour.
//
// Against the pre-fix parser every assertion here fails, and the failures show
// exactly why this mattered: P1's avg/min/max all come back NaN (a NaN first
// value seeds min and max, and `value < a.min` is false for NaN, so nothing can
// ever displace them), and temperature's mean is dragged by −999. A NaN
// avg_value stores silently in `double precision NOT NULL`, is never rewritten
// (nothing revisits a historical hourly bucket), survives reading_hourly's
// 2-year retention, and in Phase 2 makes json.Marshal fail for the entire
// response containing it.
func TestParseCSVRejectsNonFiniteAndOutOfRangeValues(t *testing.T) {
	f, err := os.Open("testdata/poisoned.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, report, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	if len(buckets) != 4 {
		t.Fatalf("len(buckets) = %d, want 4 (P1, P2, temperature, humidity for one hour)", len(buckets))
	}

	want := map[string]struct {
		avg, min, max float64
		count         int
	}{
		// P1: nan, 20, inf, 30, Infinity, 40 -> three survivors.
		"P1": {avg: 30, min: 20, max: 40, count: 3},
		// P2: 10, NaN, 12, +Inf, 14, 16 -> four survivors.
		"P2": {avg: 13, min: 10, max: 16, count: 4},
		// temperature: 22.5 x5 and one -999 sentinel.
		"temperature": {avg: 22.5, min: 22.5, max: 22.5, count: 5},
		// humidity: 55 x4, one -999 and one 101 (above the 0-100 range).
		"humidity": {avg: 55, min: 55, max: 55, count: 4},
	}

	seen := map[string]bool{}
	for _, b := range buckets {
		w, ok := want[b.Metric]
		if !ok {
			t.Errorf("unexpected metric %q in output", b.Metric)
			continue
		}
		seen[b.Metric] = true

		// Stated first and explicitly: no aggregate may ever be non-finite.
		if math.IsNaN(b.Avg) || math.IsInf(b.Avg, 0) ||
			math.IsNaN(b.Min) || math.IsInf(b.Min, 0) ||
			math.IsNaN(b.Max) || math.IsInf(b.Max, 0) {
			t.Errorf("%s: non-finite aggregate stored (avg=%v min=%v max=%v) — this row would break json.Marshal for an entire Phase 2 response", b.Metric, b.Avg, b.Min, b.Max)
		}
		if b.Avg != w.avg {
			t.Errorf("%s avg = %v, want %v", b.Metric, b.Avg, w.avg)
		}
		if b.Min != w.min || b.Max != w.max {
			t.Errorf("%s min/max = %v/%v, want %v/%v", b.Metric, b.Min, b.Max, w.min, w.max)
		}
		if b.Count != w.count {
			t.Errorf("%s count = %d, want %d — sample_count must reflect the values actually folded in, not the rows read", b.Metric, b.Count, w.count)
		}
	}
	for metric := range want {
		if !seen[metric] {
			t.Errorf("no bucket for metric %q — filtering must drop values, never whole metrics", metric)
		}
	}

	// The report is the other half of the fix: dropping silently is what made
	// this invisible in the first place.
	if report.Values != 24 {
		t.Errorf("report.Values = %d, want 24 (6 rows x 4 metrics)", report.Values)
	}
	if report.Accepted != 16 {
		t.Errorf("report.Accepted = %d, want 16", report.Accepted)
	}
	if report.RejectedNonFinite != 5 {
		t.Errorf("report.RejectedNonFinite = %d, want 5 (nan, NaN, inf, +Inf, Infinity)", report.RejectedNonFinite)
	}
	if report.RejectedOutOfRange != 3 {
		t.Errorf("report.RejectedOutOfRange = %d, want 3 (two -999 sentinels and one humidity of 101)", report.RejectedOutOfRange)
	}
	if report.Rejected() != 8 {
		t.Errorf("report.Rejected() = %d, want 8", report.Rejected())
	}
	wantByMetric := map[string]int{"P1": 3, "P2": 2, "temperature": 1, "humidity": 2}
	for metric, n := range wantByMetric {
		if report.RejectedByMetric[metric] != n {
			t.Errorf("report.RejectedByMetric[%q] = %d, want %d", metric, report.RejectedByMetric[metric], n)
		}
	}
	// A third of this file was dropped: loud enough to notice, not so bad that
	// the surviving buckets are meaningless.
	if got := report.Level(); got != slog.LevelWarn {
		t.Errorf("report.Level() = %v, want WARN for a partially rejected file", got)
	}
}

// TestParseCSVAppliesTheSameRangesAsLiveIngest pins that this path uses
// quality.InRange rather than a second, drifting copy of the bounds. The
// pressure rows are the discriminating case: the archive sends Pascals, so the
// /100 conversion must happen *before* the range check, or every pressure
// reading in every archive file is rejected as out of range (95000 is far above
// the 1100 hPa ceiling) and the whole metric silently vanishes from history.
func TestParseCSVAppliesTheSameRangesAsLiveIngest(t *testing.T) {
	f, err := os.Open("testdata/pressure.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, report, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if report.Rejected() != 0 {
		t.Errorf("report.Rejected() = %d, want 0 — every value in pressure.csv is plausible once converted to hPa; a non-zero count means the range check ran against Pascals", report.Rejected())
	}
	if report.Level() != slog.LevelInfo {
		t.Errorf("report.Level() = %v, want INFO for a clean file", report.Level())
	}

	var found bool
	for _, b := range buckets {
		if b.Metric != "pressure" {
			continue
		}
		found = true
		if !quality.InRange("pressure", b.Avg) {
			t.Errorf("pressure avg %v is not InRange — the stored aggregate must satisfy the same bounds as the values it came from", b.Avg)
		}
	}
	if !found {
		t.Fatal("no pressure bucket survived — the range check is rejecting Pascals it should have converted first")
	}
}

// TestParseCSVReportsTotalRejectionAtError covers the archive file that is
// 90%-or-worse junk: a dead sensor's day, or an upstream format change. An
// import that stored nothing must not be reported at the same level as a
// successful one.
func TestParseCSVReportsTotalRejectionAtError(t *testing.T) {
	const allJunk = "sensor_id;sensor_type;location;lat;lon;timestamp;P1;P2\n" +
		"12345;SDS011;500;42.696;23.333;2025-08-07T10:05:00;-999.00;nan\n" +
		"12345;SDS011;500;42.696;23.333;2025-08-07T10:35:00;-999.00;-999.00\n"

	buckets, report, err := backfill.ParseCSV(strings.NewReader(allJunk), 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(buckets) != 0 {
		t.Errorf("len(buckets) = %d, want 0 — nothing in this file is usable", len(buckets))
	}
	if report.Accepted != 0 {
		t.Errorf("report.Accepted = %d, want 0", report.Accepted)
	}
	if report.RejectedFraction() != 1 {
		t.Errorf("report.RejectedFraction() = %v, want 1", report.RejectedFraction())
	}
	if got := report.Level(); got != slog.LevelError {
		t.Errorf("report.Level() = %v, want ERROR — an import that salvaged nothing must be as loud as a failure, not indistinguishable from a clean run", got)
	}
}

// TestParseCSVReportsMajorityRejectionAtError pins the HighRejectionFraction
// escalation: buckets derived from a minority of the day still import, but they
// must not be reported as a normal import.
func TestParseCSVReportsMajorityRejectionAtError(t *testing.T) {
	const mostlyJunk = "sensor_id;sensor_type;location;lat;lon;timestamp;P1\n" +
		"12345;SDS011;500;42.696;23.333;2025-08-07T10:05:00;20.00\n" +
		"12345;SDS011;500;42.696;23.333;2025-08-07T10:15:00;-999.00\n" +
		"12345;SDS011;500;42.696;23.333;2025-08-07T10:25:00;-999.00\n" +
		"12345;SDS011;500;42.696;23.333;2025-08-07T10:35:00;-999.00\n"

	buckets, report, err := backfill.ParseCSV(strings.NewReader(mostlyJunk), 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("len(buckets) = %d, want 1 — the one good value must still import", len(buckets))
	}
	if report.RejectedFraction() < backfill.HighRejectionFraction {
		t.Fatalf("precondition: RejectedFraction() = %v, want >= %v", report.RejectedFraction(), backfill.HighRejectionFraction)
	}
	if got := report.Level(); got != slog.LevelError {
		t.Errorf("report.Level() = %v, want ERROR when most of the file was dropped", got)
	}
}

// TestParseReportFractionOfEmptyFileIsNotNaN — the function whose job is
// keeping NaN out of the database must not itself report NaN.
func TestParseReportFractionOfEmptyFileIsNotNaN(t *testing.T) {
	const headerOnly = "sensor_id;sensor_type;location;lat;lon;timestamp;P1\n"

	_, report, err := backfill.ParseCSV(strings.NewReader(headerOnly), 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if math.IsNaN(report.RejectedFraction()) {
		t.Error("RejectedFraction() on an empty file = NaN, want 0")
	}
	// A header-only file used to report INFO, on the reasoning that nothing was
	// dropped. Nothing was stored either, and that is the fact an operator needs:
	// "nothing was rejected" is vacuously true of a file nothing was read from.
	// Level() is ERROR whenever Accepted == 0, however the zero arose.
	if got := report.Level(); got != slog.LevelError {
		t.Errorf("Level() = %v, want ERROR for a file that stored nothing", got)
	}
}

// TestParseCSVRejectsHeaderWithNoMetricColumns pins the header-rename blind
// spot. The rejection counters can only count cells the parser recognised, so
// they are structurally blind to a column it never looked at: rename P1 to pm10
// upstream and every row parses, no cell is counted, Values is 0, nothing reads
// as rejected. Before this check the import exited 0 having stored nothing.
//
// The timestamp column is present and valid here — that is the point. Only the
// metric columns are unrecognised, so nothing else in the file looks wrong.
func TestParseCSVRejectsHeaderWithNoMetricColumns(t *testing.T) {
	const renamed = "sensor_id;sensor_type;location;lat;lon;timestamp;pm10;pm25\n" +
		"12345;SDS011;5678;42.6977;23.3219;2026-08-01T00:00:00;42.0;21.0\n" +
		"12345;SDS011;5678;42.6977;23.3219;2026-08-01T00:10:00;44.0;22.0\n"

	buckets, _, err := backfill.ParseCSV(strings.NewReader(renamed), 12345)
	if err == nil {
		t.Fatal("ParseCSV accepted a header with no recognised metric column; it must refuse rather than parse every row into nothing")
	}
	if len(buckets) != 0 {
		t.Errorf("ParseCSV returned %d buckets alongside an error, want 0", len(buckets))
	}
	// The message has to be actionable: an operator seeing it needs to know
	// which names were expected, not only that theirs were wrong.
	for _, want := range []string{"pm10", "P1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, pool
}

// insertBoundary stores a rectangle around Bulgaria as the national boundary.
// The geometry is built in-database from bind parameters rather than read from a
// GeoJSON fixture, because Import is not what these tests are about — and the
// argument order is (xmin, ymin, xmax, ymax), i.e. longitude first, matching the
// (lon, lat) convention used everywhere else.
func insertBoundary(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('bulgaria', $1, 'България', 'Bulgaria',
		         ST_Multi(ST_SetSRID(ST_MakeEnvelope($2, $3, $4, $5), 4326))::geography)`,
		area.NationalBoundaryKind, 22.3, 41.2, 28.6, 44.2); err != nil {
		t.Fatalf("insert boundary: %v", err)
	}
}

func insertSensor(ctx context.Context, t *testing.T, pool *pgxpool.Pool, id int64, lon, lat float64) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, 'SDS011', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)`,
		id, lon, lat); err != nil {
		t.Fatalf("insert sensor %d: %v", id, err)
	}
}

// TestCheckSensorInBoundaryAcceptsKnownBulgarianSensor is the positive control
// for the three rejection tests below: the guard must not make legitimate
// backfill impossible.
func TestCheckSensorInBoundaryAcceptsKnownBulgarianSensor(t *testing.T) {
	ctx, pool := migrated(t)
	insertBoundary(ctx, t, pool)
	insertSensor(ctx, t, pool, 1, 23.3327, 42.6957) // Sofia

	if err := backfill.CheckSensorInBoundary(ctx, pool, 1); err != nil {
		t.Errorf("CheckSensorInBoundary rejected a known Bulgarian sensor: %v", err)
	}
}

// TestCheckSensorInBoundaryRejectsUnknownSensor covers the orphan-creating case.
// `airbg backfill <sensor_id> <csv>` takes the sensor_id from the command line
// and writes straight to reading_hourly; reading_hourly has no foreign key to
// sensor, so a typo'd or foreign id produced rows that no sensor-driven cleanup
// could ever find.
func TestCheckSensorInBoundaryRejectsUnknownSensor(t *testing.T) {
	ctx, pool := migrated(t)
	insertBoundary(ctx, t, pool)

	err := backfill.CheckSensorInBoundary(ctx, pool, 999)
	if err == nil {
		t.Fatal("CheckSensorInBoundary accepted a sensor_id with no sensor row")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("error %q does not name the sensor", err)
	}
}

// TestCheckSensorInBoundaryRejectsForeignSensor is the backfill half of the
// boundary filter. Live ingest applies ST_Covers; backfill did not, so archive
// history for a foreign sensor could be imported through the side door that the
// task-17 filter was built to close.
func TestCheckSensorInBoundaryRejectsForeignSensor(t *testing.T) {
	ctx, pool := migrated(t)
	insertBoundary(ctx, t, pool)
	insertSensor(ctx, t, pool, 48524, -0.1276, 51.5074) // London

	if err := backfill.CheckSensorInBoundary(ctx, pool, 48524); err == nil {
		t.Fatal("CheckSensorInBoundary accepted a sensor outside the national boundary")
	}
}

// TestCheckSensorInBoundaryFailsClosedWithoutBoundary matches the fail-closed
// policy of FilterByBoundary and PurgeOutsideBoundary. With no boundary to test
// against, allowing the write would silently reopen the hole.
func TestCheckSensorInBoundaryFailsClosedWithoutBoundary(t *testing.T) {
	ctx, pool := migrated(t)
	insertSensor(ctx, t, pool, 1, 23.3327, 42.6957)

	err := backfill.CheckSensorInBoundary(ctx, pool, 1)
	if err == nil {
		t.Fatal("CheckSensorInBoundary allowed a backfill with no national boundary imported")
	}
	if !strings.Contains(err.Error(), "import-areas") {
		t.Errorf("error %q does not name the remedy command", err)
	}
}

func TestWriteBucketsIsIdempotent(t *testing.T) {
	ctx, pool := migrated(t)

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
