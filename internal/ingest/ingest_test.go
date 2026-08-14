package ingest_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

// testScorer builds a Scorer with the same values airbg.yaml ships, so the
// package's tests keep exercising the same thresholds the live scorer uses.
// It is package-level (package ingest_test) so every _test.go file in this
// package can share it.
func testScorer() *quality.Scorer {
	return quality.NewScorer(config.Quality{
		MinNeighbours:         3,
		MADScale:              1.4826,
		MADThreshold:          3.5,
		NeighbourRadiusMetres: 15000.0,
		EarthRadiusMetres:     6371000.0,
		HistoryDepth:          12,
		Ranges: map[string]config.Range{
			"P1":           {Min: 0, Max: 1000},
			"P2":           {Min: 0, Max: 1000},
			"temperature":  {Min: -40, Max: 60},
			"humidity":     {Min: 0, Max: 100},
			"pressure":     {Min: 650, Max: 1100},
			"noise_LAeq":   {Min: 25, Max: 120},
			"noise_LA_max": {Min: 25, Max: 120},
		},
	})
}

// testStoreConfig mirrors airbg.yaml's store: block, the same way testScorer
// above mirrors quality:. None of this package's tests call AreaAggregates or
// LatestSensors — the two methods that consult CoverageThreshold and
// FreshnessWindow — so RunOnce/Loop behaviour here is indifferent to the exact
// numbers. The values are pinned to the live config anyway, rather than to an
// arbitrary literal, so a reader never has to wonder whether a mismatch here
// is deliberate.
func testStoreConfig() config.Store {
	return config.Store{CoverageThreshold: 3, FreshnessWindow: 2 * time.Hour}
}

// testSeriesTimeout and testAssignTimeout mirror airbg.yaml's
// database.statement_timeouts.series and .assign.
const (
	testSeriesTimeout = 5 * time.Second
	testAssignTimeout = 60 * time.Second
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
	st := store.New(pool, testStoreConfig(), testSeriesTimeout)
	return ctx, st, ingest.New(f, st, quality.NewHistory(12), testScorer(), testAssignTimeout)
}

// newTestIngester builds an Ingester on a migrated database with an empty,
// error-free fetch — the harness the snapshot-publisher tests need for a
// successful RunOnce, without caring about what was fetched. Extracted so
// there is exactly one way to stand up an Ingester for these tests, rather
// than a second harness drifting from newIngester above.
func newTestIngester(t *testing.T) (*ingest.Ingester, func()) {
	t.Helper()
	_, _, ing := newIngester(t, stubFetcher{})
	return ing, func() {}
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
	ing := ingest.New(f, st, quality.NewHistory(12), testScorer(), testAssignTimeout)

	loopCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ing.Loop(loopCtx, 5*time.Millisecond)
		close(done)
	}()

	// Reaching a third fetch after the first one failed is the assertion: this
	// pollUntil fatals if it does not, so it carries the whole claim. A trailing
	// `if calls.Load() < 2` check used to follow the cancel below, which could
	// never fire — pollUntil has already established calls >= 3 by then. Dead
	// assertions read as coverage they do not provide, so it is gone rather than
	// restated.
	pollUntil(t, 2*time.Second, 5*time.Millisecond, func() bool {
		return calls.Load() >= 3
	})

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Loop did not return after context cancellation")
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
	st := store.New(pool, testStoreConfig(), testSeriesTimeout)
	hist := quality.NewHistory(depth)

	var calls atomic.Int64
	f := countingFetcher{calls: &calls, readings: []upstream.Reading{same}}
	ing := ingest.New(f, st, hist, testScorer(), testAssignTimeout)

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

type recordingPublisher struct {
	calls int
	err   error
	when  time.Time
}

func (p *recordingPublisher) Publish(_ context.Context, now time.Time) error {
	p.calls++
	p.when = now
	return p.err
}

// TestRunOncePublishesASnapshot: the snapshot is built at the END of a cycle.
// Built on a timer instead, it could read the reading table mid-write and
// publish an area average over a partially inserted cycle.
func TestRunOncePublishesASnapshot(t *testing.T) {
	// Reuse this package's existing harness for a successful RunOnce. Follow
	// whatever the neighbouring success-path test does to build the Ingester.
	ing, cleanup := newTestIngester(t)
	defer cleanup()

	pub := &recordingPublisher{}
	ing.SetSnapshotPublisher(pub)

	if _, err := ing.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if pub.calls != 1 {
		t.Errorf("Publish called %d times, want 1", pub.calls)
	}
	if pub.when.IsZero() {
		t.Error("Publish was handed a zero time")
	}
}

// TestSnapshotFailureDoesNotFailTheCycle. Serving data one cycle stale is a
// degraded page; returning an error from RunOnce is a collector that a
// supervisor may restart-loop, and the readings for that cycle are then lost
// for good. The safety property runs the other way round from the usual one.
func TestSnapshotFailureDoesNotFailTheCycle(t *testing.T) {
	ing, cleanup := newTestIngester(t)
	defer cleanup()

	ing.SetSnapshotPublisher(&recordingPublisher{err: errors.New("boom")})

	if _, err := ing.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce = %v, want nil — a snapshot failure must not fail ingest", err)
	}
}

// TestNoPublisherIsFine — `airbg ingest` run as a bare cron job has no server
// attached and must not nil-panic.
func TestNoPublisherIsFine(t *testing.T) {
	ing, cleanup := newTestIngester(t)
	defer cleanup()

	if _, err := ing.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce with no publisher: %v", err)
	}
}

// observingPublisher queries the store, at the exact moment Publish is
// invoked, for the row this cycle's fetch should have just written. It exists
// because TestRunOncePublishesASnapshot and its neighbours only count
// invocations: that stays green even if publishSnapshot is hoisted to the
// first line of RunOnce, before the fetch and the write, because a
// recordingPublisher never looks at what state (if any) the write left
// behind. This fake does — it asserts on causation, not position.
type observingPublisher struct {
	pool     *pgxpool.Pool
	sensorID int64
	metric   string
	sawRows  int
}

func (p *observingPublisher) Publish(ctx context.Context, _ time.Time) error {
	return p.pool.QueryRow(ctx,
		`SELECT count(*) FROM reading WHERE sensor_id = $1 AND metric = $2`,
		p.sensorID, p.metric,
	).Scan(&p.sawRows)
}

// TestPublishSeesThisCyclesWrites pins publish-after-commit ordering: the
// snapshot must be built from data that already includes what this cycle
// just wrote, not built before the write lands (which would silently and
// permanently serve one-cycle-stale data — the pages, JSON API and metrics
// all look healthy, and nothing but a direct comparison against upstream
// would ever reveal it).
func TestPublishSeesThisCyclesWrites(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 3, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(42, "temperature", 22, 0, ts),
	}}
	ctx, st, ing := newIngester(t, f)

	pub := &observingPublisher{pool: st.Pool(), sensorID: 42, metric: "temperature"}
	ing.SetSnapshotPublisher(pub)

	if _, err := ing.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if pub.sawRows != 1 {
		t.Errorf("Publish observed %d row(s) for sensor 42/temperature, want 1 — this cycle's write must be committed before publish runs", pub.sawRows)
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
	ing := ingest.New(f, st, quality.NewHistory(12), testScorer(), testAssignTimeout)

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
