package ingest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

// TestEndToEndFromRecordedPayload runs the whole pipeline against the recorded
// upstream fixture served over HTTP: fetch, normalise, score, persist, roll up.
func TestEndToEndFromRecordedPayload(t *testing.T) {
	payload, err := os.ReadFile("../upstream/testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

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

	client := upstream.New(srv.URL, 10*time.Second)
	ing := ingest.New(client, store.New(pool), quality.NewHistory(12))

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

	var hourly int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly`).Scan(&hourly); err != nil {
		t.Fatalf("count rollup: %v", err)
	}
	if hourly == 0 {
		t.Error("no hourly rollup rows after a cycle")
	}
}

// TestUpstreamContractLive is opt-in and hits the real API. It exists to detect
// upstream schema drift. Run with: AIRBG_LIVE_TEST=1 go test ./internal/ingest/
func TestUpstreamContractLive(t *testing.T) {
	if os.Getenv("AIRBG_LIVE_TEST") == "" {
		t.Skip("set AIRBG_LIVE_TEST=1 to run against the live upstream API")
	}

	client := upstream.New(
		"https://data.sensor.community/airrohr/v1/filter/country=BG",
		30*time.Second)

	readings, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("live fetch: %v", err)
	}
	if len(readings) == 0 {
		t.Fatal("live fetch returned no readings — upstream schema may have changed")
	}
	for _, r := range readings {
		if r.Lon < 22 || r.Lon > 29 || r.Lat < 41 || r.Lat > 45 {
			t.Fatalf("sensor %d at (%v, %v) is outside Bulgaria", r.SensorID, r.Lon, r.Lat)
		}
	}
}
