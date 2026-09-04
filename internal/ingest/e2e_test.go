package ingest_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"airbg.org/internal/area"
	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

// testUpstreamConfig builds a config.Upstream for the given base URL, mirroring
// airbg.yaml's shape (see internal/upstream/client_test.go's testUpstreamConfig).
func testUpstreamConfig(url string, timeout time.Duration) config.Upstream {
	return config.Upstream{
		URL:             url,
		RequestTimeout:  timeout,
		PollInterval:    5 * time.Minute,
		MinPollInterval: 30 * time.Second,
		MaxPayloadBytes: 64 << 20,
	}
}

// TestEndToEndFromRecordedPayload runs the whole pipeline against the recorded
// upstream fixture served over HTTP: fetch, normalise, score, persist, roll up.
func TestEndToEndFromRecordedPayload(t *testing.T) {
	payload, err := os.ReadFile("../upstream/testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// The fixture's readings all carry the fixed timestamp below. The rollup
	// step is anchored to wall-clock time, not to any reading's own
	// timestamp (task-16 review finding 2), so a fixed historical timestamp
	// would never land in the bucket RunOnce actually rolls up. Rewriting it
	// to "now" at serve time keeps the fixture's other fields (values,
	// coordinates, IDs) byte-identical while letting the rollup assertions
	// below work for however long this test file lives.
	now := time.Now().UTC()
	wantBucket := store.TruncateHour(now)
	payload = bytes.ReplaceAll(payload,
		[]byte("2026-08-07 12:00:00"),
		[]byte(now.Format("2006-01-02 15:04:05")))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Sofia (23.2-23.5, 42.6-42.8) and Plovdiv (24.6-24.9, 42.0-42.2) cover
	// exactly the two sensors in bg_sample.json: sensor 12345 at
	// (23.3327, 42.6957) falls inside Sofia, sensor 12346 at
	// (24.7453, 42.1354) falls inside Plovdiv. Imported before RunOnce so
	// AssignSensors (called at the end of RunOnce) has a boundary to match
	// against — otherwise it necessarily assigns zero sensors and the call
	// only proves "did not error", not that assignment works.
	if _, err := area.Import(ctx, pool, "../area/testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("area.Import: %v", err)
	}
	// The national boundary (task 17) must exist too, or RunOnce's
	// geographic filter fails closed and rejects every sensor before
	// scoring ever runs — this test would then see stats.Written == 0
	// instead of exercising the pipeline it's meant to verify end-to-end.
	if _, err := area.Import(ctx, pool, "../area/testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("area.Import(bulgaria): %v", err)
	}

	client := upstream.New(testUpstreamConfig(srv.URL, 10*time.Second))
	ing := ingest.New(client, store.New(pool, testStoreConfig(), testSeriesTimeout), quality.NewHistory(12), testScorer(), testAssignTimeout, testCountries)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Written != 5 {
		t.Errorf("Written = %d, want 5", stats.Written)
	}

	var sensors int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&sensors); err != nil {
		t.Fatalf("count sensors: %v", err)
	}
	if sensors != 2 {
		t.Errorf("sensor count = %d, want 2 (the third fixture entry has no coordinates)", sensors)
	}

	// Every stored sensor must be inside Bulgaria.
	var outside int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM sensor
		 WHERE ST_X(location::geometry) NOT BETWEEN 22 AND 29
		    OR ST_Y(location::geometry) NOT BETWEEN 41 AND 45`).Scan(&outside)
	if err != nil {
		t.Fatalf("bounds check: %v", err)
	}
	if outside != 0 {
		t.Errorf("%d sensors stored outside Bulgaria — coordinates swapped", outside)
	}

	// The rollup must land in the exact bucket the (now rewritten) fixture
	// readings fall into, and must carry the aggregate values the fixture
	// readings actually produce — not just "some row exists somewhere". A
	// rollup that wrote one garbage row in the wrong bucket would pass a
	// bare count(*) > 0 check.
	var hourly int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly WHERE bucket = $1`, wantBucket).Scan(&hourly); err != nil {
		t.Fatalf("count rollup: %v", err)
	}
	// 12345/P1, 12345/P2, 12346/temperature, 12346/humidity, 12346/pressure.
	if hourly != 5 {
		t.Errorf("hourly rollup rows in bucket %v = %d, want 5", wantBucket, hourly)
	}

	var p1Avg float64
	var p1Samples int
	err = pool.QueryRow(ctx,
		`SELECT avg_value, sample_count FROM reading_hourly
		 WHERE sensor_id = 12345 AND metric = 'P1' AND bucket = $1`, wantBucket).
		Scan(&p1Avg, &p1Samples)
	if err != nil {
		t.Fatalf("read P1 rollup: %v", err)
	}
	if p1Avg != 24.30 {
		t.Errorf("sensor 12345 P1 avg_value = %v, want 24.30", p1Avg)
	}
	if p1Samples != 1 {
		t.Errorf("sensor 12345 P1 sample_count = %d, want 1", p1Samples)
	}

	var pressureAvg float64
	err = pool.QueryRow(ctx,
		`SELECT avg_value FROM reading_hourly
		 WHERE sensor_id = 12346 AND metric = 'pressure' AND bucket = $1`, wantBucket).
		Scan(&pressureAvg)
	if err != nil {
		t.Fatalf("read pressure rollup: %v", err)
	}
	// 94210.00 Pa / 100 = 942.10 hPa.
	if pressureAvg != 942.1 {
		t.Errorf("sensor 12346 pressure avg_value = %v hPa, want 942.1 — Pa->hPa conversion may not have survived the rollup", pressureAvg)
	}

	// Area assignment: RunOnce calls area.AssignSensors as its last step.
	// Confirm both fixture sensors actually landed in the boundary imported
	// above, in the correct city each — not just that AssignSensors ran
	// without error against an empty area table.
	var sofiaSlug string
	err = pool.QueryRow(ctx, `SELECT area_slug FROM area_sensor WHERE sensor_id = 12345`).Scan(&sofiaSlug)
	if err != nil {
		t.Fatalf("read area assignment for sensor 12345: %v — sensor was not assigned to any area", err)
	}
	if sofiaSlug != "sofia" {
		t.Errorf("sensor 12345 area_slug = %q, want %q", sofiaSlug, "sofia")
	}

	var plovdivSlug string
	err = pool.QueryRow(ctx, `SELECT area_slug FROM area_sensor WHERE sensor_id = 12346`).Scan(&plovdivSlug)
	if err != nil {
		t.Fatalf("read area assignment for sensor 12346: %v — sensor was not assigned to any area", err)
	}
	if plovdivSlug != "plovdiv" {
		t.Errorf("sensor 12346 area_slug = %q, want %q", plovdivSlug, "plovdiv")
	}
}

// TestUpstreamContractLive is opt-in and hits the real API. It exists to
// detect upstream *schema* drift only — field presence, JSON types, the
// value_type vocabulary, timestamp format. It deliberately asserts nothing
// about the truthfulness of individual sensor data (coordinates, self-reported
// country, plausible readings): upstream ships bad data from misconfigured
// sensors routinely, and that is a data-quality problem, not a contract
// break. A test that fails on data quality forever is noise that trains
// people to ignore it right when it has something real (an actual schema
// change) to say. Run with: AIRBG_LIVE_TEST=1 go test ./internal/ingest/
func TestUpstreamContractLive(t *testing.T) {
	if os.Getenv("AIRBG_LIVE_TEST") == "" {
		t.Skip("set AIRBG_LIVE_TEST=1 to run against the live upstream API")
	}

	client := upstream.New(testUpstreamConfig(
		"https://data.sensor.community/airrohr/v1/filter/country=BG",
		30*time.Second))

	batch, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("live fetch: %v — payload failed to parse; upstream schema may have changed", err)
	}
	readings := batch.Readings

	// Skipped is now reported rather than discarded, so the live contract check
	// can assert on it directly: a large skip count is drift that has already
	// started, even while enough entries still parse to keep the count above
	// the threshold below. 10% is loose enough for the handful of genuinely
	// broken sensors upstream always carries.
	if batch.Total() > 0 {
		if fraction := float64(batch.Skipped) / float64(batch.Total()); fraction > 0.10 {
			t.Errorf("live fetch skipped %d of %d entries (%.1f%%) — upstream entry shape may be drifting", batch.Skipped, batch.Total(), fraction*100)
		}
	}
	if len(readings) == 0 {
		t.Fatal("live fetch returned no readings — upstream schema may have changed")
	}
	// A plausible-sized country-wide fetch. If the value_type vocabulary or
	// the array-vs-object shape of sensordatavalues drifted in a way that
	// makes most/all entries fail to normalise, len(readings) collapses
	// towards 0 without necessarily hitting it exactly, and the check above
	// alone would miss that. Bulgaria has run several hundred active sensors
	// for years; 100 is comfortably below normal and comfortably above "the
	// vocabulary broke and almost everything got dropped".
	if len(readings) < 100 {
		t.Errorf("live fetch returned only %d readings, want > 100 — a schema or vocabulary drift may be silently dropping most entries", len(readings))
	}

	metricsSeen := map[string]bool{}
	var pressures []float64
	for _, r := range readings {
		// Every canonical metric must have parsed to a real number and a
		// valid, non-zero-value timestamp — the two fields whose JSON
		// encoding has actually drifted historically (this task's own fix
		// was for value; a broken timestamp format would zero it out).
		if !upstream.IsCanonicalMetric(r.Metric) {
			t.Errorf("sensor %d: metric %q leaked through Normalise — not in the canonical set", r.SensorID, r.Metric)
		}
		if r.Timestamp.IsZero() {
			t.Errorf("sensor %d: metric %q has a zero timestamp — timestamp format may have changed", r.SensorID, r.Metric)
		}
		if r.SensorID == 0 {
			t.Error("reading with zero SensorID — sensor.id field may be missing or renamed")
		}
		metricsSeen[r.Metric] = true

		if r.Metric == "pressure" {
			pressures = append(pressures, r.Value)
		}
	}

	// P1 (PM10) and P2 (PM2.5) are what sensor.community's dominant sensor
	// type (SDS011) reports on every cycle; their absence from a
	// several-hundred-reading fetch means the value_type vocabulary itself
	// has drifted, not that a specific rare sensor type went quiet.
	for _, want := range []string{"P1", "P2"} {
		if !metricsSeen[want] {
			t.Errorf("no %q readings in live fetch — value_type vocabulary may have changed", want)
		}
	}

	if len(pressures) == 0 {
		t.Log("no pressure readings in this live fetch — cannot verify the Pa->hPa conversion this run")
	} else {
		// Individual station-pressure readings are altitude-dependent (a
		// Musala-altitude sensor at ~2925 m legitimately reads ~710 hPa) and
		// crowd-sourced hardware contributes outright garbage, so asserting
		// a band on every reading is a data-quality check wearing a
		// contract check's clothes — it doesn't belong here. The median is
		// robust to both: a minority of faulty or high-altitude sensors
		// cannot move it far, but losing the Pa->hPa /100 conversion moves
		// it by two orders of magnitude, to roughly 95,000 — nowhere near
		// any plausible hPa band. The band is deliberately wide (700-1100)
		// so normal altitude spread across Bulgaria's terrain cannot trip
		// it; its only job is telling hPa from Pa, a factor-of-100 gap.
		sort.Float64s(pressures)
		median := pressures[len(pressures)/2]
		if len(pressures)%2 == 0 {
			median = (pressures[len(pressures)/2-1] + pressures[len(pressures)/2]) / 2
		}
		if median < 700 || median > 1100 {
			t.Errorf("median pressure = %v hPa (n=%d), want within 700-1100 — Pa->hPa conversion may be broken", median, len(pressures))
		}
	}
}
