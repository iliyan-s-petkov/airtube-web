package wind_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/wind"
)

const collectorSeriesTimeout = 5 * time.Second

// testStoreConfig mirrors airbg.yaml's store: block, including the official
// window testsupport.StoreConfig omits — LatestSensors requires it to admit
// any reading at all (see internal/store/aggregate.go's freshnessPredicate).
func testStoreConfig() config.Store {
	return config.Store{
		CoverageThreshold:       3,
		FreshnessWindow:         2 * time.Hour,
		OfficialFreshnessWindow: 6 * time.Hour,
	}
}

func migratedStore(t *testing.T) (context.Context, *pgxpool.Pool, *store.Store) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool, store.New(pool, testStoreConfig(), collectorSeriesTimeout)
}

// seedFreshSensor inserts a sensor with a reading recent enough for
// LatestSensors to return it, so HexGridOf has something to grid.
func seedFreshSensor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64, lon, lat float64) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, 'TEST', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)
		 ON CONFLICT (sensor_id) DO NOTHING`,
		id, lon, lat); err != nil {
		t.Fatalf("seed sensor %d: %v", id, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, 'P2', 10, 'ok'::quality_flag)
		 ON CONFLICT (sensor_id, metric, time) DO UPDATE SET value = EXCLUDED.value`,
		time.Now().UTC().Truncate(time.Second), id); err != nil {
		t.Fatalf("seed reading %d: %v", id, err)
	}
}

func testWindConfig(url string, pointsPerReq int) config.Wind {
	return config.Wind{
		URL:             url,
		Model:           "ecmwf_ifs025",
		ResolutionDeg:   0.25,
		RequestTimeout:  5 * time.Second,
		PollInterval:    time.Hour,
		ForecastHours:   24,
		PointsPerReq:    pointsPerReq,
		MaxPayloadBytes: 1 << 20,
		Retention:       48 * time.Hour,
	}
}

func countForecastRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM wind_forecast`).Scan(&n); err != nil {
		t.Fatalf("count wind_forecast: %v", err)
	}
	return n
}

// locationsInRequest reports how many comma-separated latitudes a batch
// request asked about — the request's own record of its batch size.
func locationsInRequest(r *http.Request) int {
	lat := r.URL.Query().Get("latitude")
	if lat == "" {
		return 0
	}
	return len(strings.Split(lat, ","))
}

// TestRunOnceStoresOneRowPerHexPerHour is the success path: every hex the
// sensors fall into, at every forecast hour the upstream returns, must land
// in wind_forecast — not merely "no error".
func TestRunOnceStoresOneRowPerHexPerHour(t *testing.T) {
	ctx, pool, s := migratedStore(t)

	// Three sensors far enough apart (roughly 50-100 km) to fall into three
	// distinct 15 km hexes.
	seedFreshSensor(t, ctx, pool, 1, 23.0, 42.0)
	seedFreshSensor(t, ctx, pool, 2, 24.0, 43.0)
	seedFreshSensor(t, ctx, pool, 3, 25.0, 44.0)

	var requests int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		n := locationsInRequest(r)
		var b strings.Builder
		b.WriteByte('[')
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"hourly":{"time":["2026-09-05T00:00","2026-09-05T01:00"],`+
				`"wind_speed_10m":[%d,%d],"wind_direction_10m":[10,20]}}`, 3+i, 4+i)
		}
		b.WriteByte(']')
		w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	c := wind.NewCollector(testWindConfig(srv.URL, 100), s)
	n, err := c.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// 3 hexes x 2 forecast hours each.
	if n != 6 {
		t.Fatalf("RunOnce returned %d rows written, want 6", n)
	}
	if got := countForecastRows(t, ctx, pool); got != 6 {
		t.Fatalf("wind_forecast has %d rows, want 6", got)
	}
	if requests != 1 {
		t.Fatalf("upstream got %d requests, want 1 (all 3 points fit in one batch of 100)", requests)
	}

	vs, _, model, err := s.CurrentWind(ctx, time.Date(2026, 9, 5, 0, 30, 0, 0, time.UTC), 15)
	if err != nil {
		t.Fatalf("CurrentWind: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("CurrentWind returned %d vectors for the first hour, want 3", len(vs))
	}
	if model != "ecmwf_ifs025" {
		t.Errorf("model = %q, want ecmwf_ifs025", model)
	}
}

// TestRunOnceUpstream5xxStoresNothing covers a failing upstream: a batch that
// fails must abort the whole cycle, not write whatever batches happened to
// succeed first.
func TestRunOnceUpstream5xxStoresNothing(t *testing.T) {
	ctx, pool, s := migratedStore(t)
	seedFreshSensor(t, ctx, pool, 1, 23.0, 42.0)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := wind.NewCollector(testWindConfig(srv.URL, 100), s)
	n, err := c.RunOnce(ctx)
	if err == nil {
		t.Fatal("RunOnce succeeded against a 500 upstream, want an error")
	}
	if n != 0 {
		t.Errorf("RunOnce reported %d rows written on failure, want 0", n)
	}
	if got := countForecastRows(t, ctx, pool); got != 0 {
		t.Errorf("wind_forecast has %d rows after a failed cycle, want 0", got)
	}
}

// TestRunOnceBatchesAtThePointsPerReqBoundary pins the batch split itself:
// with 5 hexes and PointsPerReq=2, the fetch must issue 3 requests carrying
// 2, 2 and 1 points, and still store all 5 hexes' rows — not merely as many
// as the first batch held.
func TestRunOnceBatchesAtThePointsPerReqBoundary(t *testing.T) {
	ctx, pool, s := migratedStore(t)

	seedFreshSensor(t, ctx, pool, 1, 22.0, 41.0)
	seedFreshSensor(t, ctx, pool, 2, 23.0, 42.0)
	seedFreshSensor(t, ctx, pool, 3, 24.0, 43.0)
	seedFreshSensor(t, ctx, pool, 4, 25.0, 44.0)
	seedFreshSensor(t, ctx, pool, 5, 26.0, 45.0)

	var mu sync.Mutex
	var batchSizes []int
	var nextSpeed float64 = 100 // increments across every point in every batch, never resets

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := locationsInRequest(r)

		mu.Lock()
		batchSizes = append(batchSizes, n)
		mu.Unlock()

		var b strings.Builder
		b.WriteByte('[')
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			mu.Lock()
			speed := nextSpeed
			nextSpeed++
			mu.Unlock()
			fmt.Fprintf(&b, `{"hourly":{"time":["2026-09-05T00:00"],`+
				`"wind_speed_10m":[%v],"wind_direction_10m":[0]}}`, speed)
		}
		b.WriteByte(']')
		w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	c := wind.NewCollector(testWindConfig(srv.URL, 2), s)
	n, err := c.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 5 {
		t.Fatalf("RunOnce returned %d rows written, want 5", n)
	}
	if got := countForecastRows(t, ctx, pool); got != 5 {
		t.Fatalf("wind_forecast has %d rows, want 5", got)
	}

	mu.Lock()
	got := append([]int(nil), batchSizes...)
	mu.Unlock()
	want := []int{2, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("batch sizes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("batch sizes = %v, want %v", got, want)
		}
	}

	// Every point-index-derived speed (100..104) must have been stored
	// exactly once: proof the last, undersized batch was not dropped.
	rows, err := pool.Query(ctx, `SELECT speed_ms FROM wind_forecast ORDER BY speed_ms`)
	if err != nil {
		t.Fatalf("query speeds: %v", err)
	}
	var speeds []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan speed: %v", err)
		}
		speeds = append(speeds, v)
	}
	rows.Close()
	wantSpeeds := []float64{100, 101, 102, 103, 104}
	if len(speeds) != len(wantSpeeds) {
		t.Fatalf("stored speeds = %v, want %v", speeds, wantSpeeds)
	}
	for i := range wantSpeeds {
		if speeds[i] != wantSpeeds[i] {
			t.Fatalf("stored speeds = %v, want %v", speeds, wantSpeeds)
		}
	}
}

// TestRunOnceContextCancelledMidBatchStoresNothing covers cancellation while
// the fetch loop is partway through its batches: the first batch's rows must
// not be written just because it finished before the cycle was cancelled.
func TestRunOnceContextCancelledMidBatchStoresNothing(t *testing.T) {
	ctx, pool, s := migratedStore(t)

	seedFreshSensor(t, ctx, pool, 1, 22.0, 41.0)
	seedFreshSensor(t, ctx, pool, 2, 23.0, 42.0)
	seedFreshSensor(t, ctx, pool, 3, 24.0, 43.0)

	runCtx, cancel := context.WithCancel(ctx)

	var requestCount int
	var mu sync.Mutex
	secondRequestArrived := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		this := requestCount
		mu.Unlock()

		if this == 2 {
			// Tell the test this batch is in flight, then wait for the
			// client's context to be cancelled before answering: the
			// in-flight request must be aborted by ctx, not completed.
			close(secondRequestArrived)
			<-runCtx.Done()
			return
		}

		n := locationsInRequest(r)
		var b strings.Builder
		b.WriteByte('[')
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"hourly":{"time":["2026-09-05T00:00"],"wind_speed_10m":[5],"wind_direction_10m":[0]}}`)
		}
		b.WriteByte(']')
		w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	go func() {
		<-secondRequestArrived
		cancel()
	}()

	c := wind.NewCollector(testWindConfig(srv.URL, 1), s)
	n, err := c.RunOnce(runCtx)
	if err == nil {
		t.Fatal("RunOnce succeeded despite the context being cancelled mid-batch, want an error")
	}
	if n != 0 {
		t.Errorf("RunOnce reported %d rows written after cancellation, want 0", n)
	}
	if got := countForecastRows(t, context.Background(), pool); got != 0 {
		t.Errorf("wind_forecast has %d rows after a cancelled cycle, want 0 — the first batch's rows leaked", got)
	}
}
