package ingest_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"airbg.org/internal/area"
	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

type stubFetcher struct {
	readings []upstream.Reading
	// skipped mirrors upstream.Batch.Skipped, so a test can reproduce the
	// "upstream answered but nothing normalised" condition without standing up
	// a broken HTTP payload.
	skipped int
	err     error
}

func (s stubFetcher) Fetch(context.Context) (upstream.Batch, error) {
	return upstream.Batch{Readings: s.readings, Skipped: s.skipped}, s.err
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

// newIngester sets up a migrated database with the national boundary
// (task 17) already imported, so tests using realistic Bulgarian
// coordinates (the reading() helper below) are not rejected by the
// fail-closed absent-boundary path. Tests that specifically need the
// boundary to be absent (see internal/ingest/boundary_test.go) build their
// own pool + store instead of using this helper.
func newIngester(t *testing.T, f ingest.Fetcher) (context.Context, *store.Store, *ingest.Ingester) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := area.Import(ctx, pool, "../area/testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("area.Import(bulgaria): %v", err)
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

// TestRawRetentionHoursMatchesLivePolicy guards task-16 review finding 4:
// ingest.RawRetentionHours mirrors the retention policy that migration 00003
// installs on the `reading` hypertable (drop_after => 30 days), purely as a
// documentation constant with no structural link to the migration. If a
// future edit to that migration's drop_after forgot to update the constant,
// the alert's "margin_hours" would silently misreport how much runway is
// actually left before raw data starts getting dropped. This test reads the
// live policy back out of timescaledb_information.jobs and fails loudly on
// drift instead.
func TestRawRetentionHoursMatchesLivePolicy(t *testing.T) {
	ctx, st, _ := newIngester(t, nil)

	var gotHours float64
	err := st.Pool().QueryRow(ctx, `
		SELECT EXTRACT(EPOCH FROM (config->>'drop_after')::interval) / 3600
		FROM timescaledb_information.jobs
		WHERE proc_name = 'policy_retention' AND hypertable_name = 'reading'`).
		Scan(&gotHours)
	if err != nil {
		t.Fatalf("query live retention policy: %v", err)
	}

	if int(gotHours) != ingest.RawRetentionHours {
		t.Errorf("live retention policy on reading = %v hours, ingest.RawRetentionHours = %d — the constant has drifted from migration 00003's drop_after", gotHours, ingest.RawRetentionHours)
	}
}

func TestRunOnceUpdatesRollup(t *testing.T) {
	// The rollup step is anchored to wall-clock time (task-16 review finding
	// 2), not to this reading's own timestamp, so the reading must actually
	// land in the hour RunOnce rolls up. Pinning RunOnce's clock to the same
	// ts used for the reading (task-16 review round 2, flaky-test finding)
	// avoids relying on time.Now() called separately in the test and inside
	// RunOnce landing in the same hour.
	ts := time.Now().UTC()
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "P1", 20, 0, ts),
	}}
	ctx, st, ing := newIngester(t, f)
	restore := ing.SetClockForTesting(func() time.Time { return ts })
	defer restore()

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

// countingFetcher records how many times Fetch was called (guarded by an
// atomic counter so -race is happy about the concurrent Loop goroutine
// reading it) and optionally signals on notify after each call, letting a
// test synchronise on "a cycle has started" instead of sleeping and hoping.
type countingFetcher struct {
	calls    *atomic.Int64
	readings []upstream.Reading
	err      error
	notify   chan struct{}
}

func (f countingFetcher) Fetch(context.Context) (upstream.Batch, error) {
	f.calls.Add(1)
	if f.notify != nil {
		select {
		case f.notify <- struct{}{}:
		default:
		}
	}
	return upstream.Batch{Readings: f.readings}, f.err
}

// pollUntil polls cond every step until it reports true or timeout elapses,
// at which point it fails the test. It observes real state rather than
// guessing at timing, so it stays deterministic regardless of how fast or
// slow the ticker actually fires.
func pollUntil(t *testing.T, timeout, step time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(step)
	}
}

// TestLoopSurvivesFetchErrors proves that a fetch failure logs and falls
// through rather than ending the loop: the fetcher errors on every call, and
// the loop must still call it again on the next tick.
//
// A real store is required (not nil): since task-16, RunOnce always runs the
// rollup backlog step even when the fetch fails (finding 1), so a nil store
// would panic instead of exercising the intended "survives fetch errors"
// behaviour.
func TestLoopSurvivesFetchErrors(t *testing.T) {
	var calls atomic.Int64
	f := countingFetcher{calls: &calls, err: errors.New("upstream down")}
	_, st, _ := newIngester(t, f)
	ing := ingest.New(f, st, quality.NewHistory(12))

	loopCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ing.Loop(loopCtx, 5*time.Millisecond)
		close(done)
	}()

	// Let several ticks elapse — proves one failure did not end the loop.
	pollUntil(t, 2*time.Second, 5*time.Millisecond, func() bool {
		return calls.Load() >= 3
	})

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Loop did not return after context cancellation")
	}

	if got := calls.Load(); got < 2 {
		t.Errorf("fetch calls = %d, want > 1 (loop must survive a failed cycle)", got)
	}
}

// TestLoopReusesHistoryAcrossCycles proves the same *quality.History instance
// persists across ticks. If Loop recreated History per cycle, the stuck run
// counter would reset every tick and this would never flip, so this test
// fails under exactly the regression the review flagged.
func TestLoopReusesHistoryAcrossCycles(t *testing.T) {
	const depth = 3
	ts := time.Date(2026, 1, 15, 8, 3, 0, 0, time.UTC)
	same := reading(1, "temperature", 22, 0, ts)

	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := area.Import(ctx, pool, "../area/testdata/bulgaria.geojson", area.NationalBoundaryKind); err != nil {
		t.Fatalf("area.Import(bulgaria): %v", err)
	}
	st := store.New(pool)
	hist := quality.NewHistory(depth)

	var calls atomic.Int64
	f := countingFetcher{calls: &calls, readings: []upstream.Reading{same}}
	ing := ingest.New(f, st, hist)

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		ing.Loop(loopCtx, 5*time.Millisecond)
		close(done)
	}()

	pollUntil(t, 10*time.Second, 10*time.Millisecond, func() bool {
		return hist.IsStuck(same.SensorID, same.Metric)
	})

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Loop did not return after context cancellation")
	}

	if !hist.IsStuck(same.SensorID, same.Metric) {
		t.Fatal("history should still report the sensor stuck after the loop stopped")
	}
	if calls.Load() < int64(depth) {
		t.Fatalf("fetch calls = %d, want >= %d cycles to cross the stuck threshold", calls.Load(), depth)
	}
}

// TestLoopStopsOnContextCancel proves cancellation is observed immediately
// rather than only after the next tick. The poll interval is set far longer
// than the test's own timeout, so a regression that waits on the ticker
// before checking ctx.Done would hang this test until it fails.
func TestLoopStopsOnContextCancel(t *testing.T) {
	var calls atomic.Int64
	notify := make(chan struct{}, 1)
	f := countingFetcher{calls: &calls, notify: notify}
	// A real store is required: RunOnce always runs the rollup backlog step
	// now, even for an empty fetch result, so a nil store would panic.
	_, st, _ := newIngester(t, f)
	ing := ingest.New(f, st, quality.NewHistory(12))

	loopCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ing.Loop(loopCtx, time.Hour)
		close(done)
	}()

	// Wait for the first cycle to actually run before cancelling, so we know
	// Loop has reached its select and is waiting on the (long) ticker.
	select {
	case <-notify:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop never ran its first cycle")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Loop did not return promptly after context cancellation; it appears to be waiting on the ticker instead of ctx.Done")
	}
}
