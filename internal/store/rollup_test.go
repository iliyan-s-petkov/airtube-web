package store_test

import (
	"context"
	"errors"
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

func TestRollupBacklogAdvancesWatermarkToBucketActuallyRolledUp(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucket := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{sample(1, "P1", 20, quality.FlagOK, bucket.Add(time.Minute))}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	processed, watermark, err := s.RollupBacklog(ctx, bucket, 24)
	if err != nil {
		t.Fatalf("RollupBacklog: %v", err)
	}
	if processed != 1 {
		t.Errorf("processed = %d, want 1", processed)
	}
	if !watermark.Equal(bucket) {
		t.Errorf("returned watermark = %v, want %v", watermark, bucket)
	}

	gotBucket, _, found, err := s.Watermark(ctx)
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	if !found {
		t.Fatal("watermark not found after RollupBacklog")
	}
	if !gotBucket.Equal(bucket) {
		t.Errorf("stored watermark = %v, want %v", gotBucket, bucket)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
		bucket).Scan(&count); err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if count != 1 {
		t.Errorf("sample_count = %d, want 1", count)
	}
}

// TestRollupBacklogDrainsGapNotJustNewestBucket seeds raw readings across
// several past hours with the watermark left behind, then asserts every
// intervening bucket — not just the newest one — gets correct aggregate
// rows. Before RollupBacklog existed, the ingest loop only ever rolled up
// the current hour; this test fails against that behaviour because the
// older buckets would be left empty.
func TestRollupBacklogDrainsGapNotJustNewestBucket(t *testing.T) {
	ctx, pool, s := newStore(t)
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	hours := []time.Time{base, base.Add(time.Hour), base.Add(2 * time.Hour), base.Add(3 * time.Hour)}

	var scored []quality.Scored
	for i, h := range hours {
		scored = append(scored, sample(1, "P1", float64(10*(i+1)), quality.FlagOK, h.Add(time.Minute)))
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	// Seed the watermark two hours behind the earliest bucket, simulating a
	// rollup that stalled well before this data arrived.
	if _, _, err := s.RollupBacklog(ctx, base.Add(-2*time.Hour), 24); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	current := hours[len(hours)-1]
	processed, watermark, err := s.RollupBacklog(ctx, current, 24)
	if err != nil {
		t.Fatalf("RollupBacklog: %v", err)
	}
	// 5 buckets between the seeded watermark (base-2h) and current (base+3h):
	// base-1h (empty, no readings), then base through base+3h.
	if processed != 5 {
		t.Errorf("processed = %d, want 5", processed)
	}
	if !watermark.Equal(current) {
		t.Errorf("watermark = %v, want %v", watermark, current)
	}

	for i, h := range hours {
		var count int
		err := pool.QueryRow(ctx,
			`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
			h).Scan(&count)
		if err != nil {
			t.Fatalf("bucket %d (%v): read rollup: %v", i, h, err)
		}
		if count != 1 {
			t.Errorf("bucket %d (%v): sample_count = %d, want 1 — backlog bucket was not drained", i, h, count)
		}
	}
}

func TestRollupBacklogHonoursPerCallCap(t *testing.T) {
	ctx, _, s := newStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Establish a watermark at base-1h before any backlog exists, so the
	// backlog below (base .. base+9h) is a genuine gap rather than
	// triggering fresh-database bootstrap (which deliberately only touches
	// the current hour, see TestRollupBacklogBootstrapsAtCurrentHourOnly).
	if _, _, err := s.RollupBacklog(ctx, base.Add(-time.Hour), 24); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	var scored []quality.Scored
	for i := 0; i < 10; i++ {
		scored = append(scored, sample(1, "P1", float64(i), quality.FlagOK, base.Add(time.Duration(i)*time.Hour+time.Minute)))
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	current := base.Add(9 * time.Hour) // 10 buckets outstanding: base .. base+9h
	const cap = 3
	processed, watermark, err := s.RollupBacklog(ctx, current, cap)
	if err != nil {
		t.Fatalf("RollupBacklog: %v", err)
	}
	if processed != cap {
		t.Fatalf("processed = %d, want %d (the cap)", processed, cap)
	}
	wantWatermark := base.Add(time.Duration(cap-1) * time.Hour)
	if !watermark.Equal(wantWatermark) {
		t.Fatalf("watermark = %v, want %v — must reflect exactly what completed", watermark, wantWatermark)
	}

	gotBucket, _, found, err := s.Watermark(ctx)
	if err != nil {
		t.Fatalf("Watermark: %v", err)
	}
	if !found || !gotBucket.Equal(wantWatermark) {
		t.Fatalf("stored watermark = %v (found=%v), want %v", gotBucket, found, wantWatermark)
	}

	// A second call should pick up where the first left off.
	processed2, watermark2, err := s.RollupBacklog(ctx, current, cap)
	if err != nil {
		t.Fatalf("RollupBacklog second call: %v", err)
	}
	if processed2 != cap {
		t.Fatalf("second call processed = %d, want %d", processed2, cap)
	}
	wantWatermark2 := base.Add(time.Duration(2*cap-1) * time.Hour)
	if !watermark2.Equal(wantWatermark2) {
		t.Fatalf("watermark after second call = %v, want %v", watermark2, wantWatermark2)
	}
}

// TestRollupBacklogBootstrapsAtCurrentHourOnly proves that on a fresh
// database (no watermark row yet) RollupBacklog does not walk back to the
// beginning of time — it processes only the current hour — and that it does
// not skip the current hour either.
func TestRollupBacklogBootstrapsAtCurrentHourOnly(t *testing.T) {
	ctx, _, s := newStore(t)
	current := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{sample(1, "P1", 20, quality.FlagOK, current.Add(time.Minute))}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	processed, watermark, err := s.RollupBacklog(ctx, current, 1000)
	if err != nil {
		t.Fatalf("RollupBacklog: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 — bootstrap must roll up exactly the current hour, not history", processed)
	}
	if !watermark.Equal(current) {
		t.Fatalf("watermark = %v, want %v", watermark, current)
	}
}

func TestRollupBacklogIsIdempotentOverAlreadyRolledUpRange(t *testing.T) {
	ctx, pool, s := newStore(t)
	base := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	hours := []time.Time{base, base.Add(time.Hour), base.Add(2 * time.Hour)}

	// Seed the watermark before base so the buckets below are treated as an
	// outstanding backlog rather than triggering fresh-database bootstrap.
	if _, _, err := s.RollupBacklog(ctx, base.Add(-time.Hour), 24); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	var scored []quality.Scored
	for i, h := range hours {
		scored = append(scored, sample(1, "P1", float64(10*(i+1)), quality.FlagOK, h.Add(time.Minute)))
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	current := hours[len(hours)-1]
	if _, _, err := s.RollupBacklog(ctx, current, 24); err != nil {
		t.Fatalf("first RollupBacklog: %v", err)
	}

	var firstCounts [3]int
	for i, h := range hours {
		if err := pool.QueryRow(ctx,
			`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
			h).Scan(&firstCounts[i]); err != nil {
			t.Fatalf("read bucket %d: %v", i, err)
		}
	}

	// Re-run RollupBacklog with the watermark already at `current`: nothing
	// historical is outstanding, but the current hour is always re-rolled.
	// Aggregates for every bucket must be unchanged (no double counting).
	if _, _, err := s.RollupBacklog(ctx, current, 24); err != nil {
		t.Fatalf("second RollupBacklog: %v", err)
	}

	for i, h := range hours {
		var count int
		if err := pool.QueryRow(ctx,
			`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
			h).Scan(&count); err != nil {
			t.Fatalf("read bucket %d after rerun: %v", i, err)
		}
		if count != firstCounts[i] {
			t.Errorf("bucket %d (%v): sample_count changed from %d to %d on rerun — not idempotent", i, h, firstCounts[i], count)
		}
	}

	var totalRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1'`).Scan(&totalRows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if totalRows != 3 {
		t.Errorf("reading_hourly rows = %d, want 3 (one per bucket, no duplicates)", totalRows)
	}
}

// TestRollupBacklogStopsExactlyWhereADrainFails is task-16 review finding
// 6's regression test: it proves the watermark reflects exactly the buckets
// whose aggregates actually committed when a multi-bucket drain fails
// partway through — no gap (some completed bucket left unrecorded) and no
// overshoot (the watermark claiming a bucket that never committed).
//
// It uses SetRollupBacklogHookForTesting to deterministically cancel the
// context after the 3rd of 8 outstanding buckets, forcing the 4th bucket's
// transaction to fail. Note this test only reads database ground truth via
// a fresh s.Watermark(ctx) call — it never inspects RollupBacklog's own
// returned watermark — so it does not exercise finding 3 (whether the
// *returned* value can diverge from the database). Finding 3 is pinned
// separately, indirectly, by the alert tests in internal/ingest comparing
// the logged gap_hours against a fresh database read.
func TestRollupBacklogStopsExactlyWhereADrainFails(t *testing.T) {
	ctx, _, s := newStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Seed a watermark at base-1h so the 8 hours below (base .. base+7h) are
	// a genuine outstanding backlog, not fresh-database bootstrap.
	if _, _, err := s.RollupBacklog(ctx, base.Add(-time.Hour), 24); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	var scored []quality.Scored
	for i := 0; i < 8; i++ {
		scored = append(scored, sample(1, "P1", float64(i), quality.FlagOK, base.Add(time.Duration(i)*time.Hour+time.Minute)))
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	const failAfter = 3
	drainCtx, cancel := context.WithCancel(ctx)
	restore := store.SetRollupBacklogHookForTesting(func(processed int, bucket time.Time) {
		if processed == failAfter {
			cancel()
		}
	})
	defer restore()

	current := base.Add(7 * time.Hour) // 8 buckets outstanding: base .. base+7h
	processed, watermark, err := s.RollupBacklog(drainCtx, current, 24)
	if err == nil {
		t.Fatalf("RollupBacklog: want error from the cancelled context after bucket %d, got nil (processed=%d, watermark=%v)", failAfter, processed, watermark)
	}
	if processed != failAfter {
		t.Fatalf("processed = %d, want %d — the drain must stop reporting progress at the last bucket that actually committed", processed, failAfter)
	}

	// Read the ground truth back with a fresh, uncancelled context: the
	// watermark must reflect exactly the failAfter buckets that committed —
	// no gap, no overshoot.
	wantWatermark := base.Add(time.Duration(failAfter-1) * time.Hour)
	gotBucket, _, found, werr := s.Watermark(ctx)
	if werr != nil {
		t.Fatalf("Watermark: %v", werr)
	}
	if !found {
		t.Fatal("watermark missing after partial drain")
	}
	if !gotBucket.Equal(wantWatermark) {
		t.Fatalf("stored watermark = %v, want %v (exactly the buckets that committed)", gotBucket, wantWatermark)
	}

	// The bucket the failed iteration was working on (and everything after
	// it) must not have been aggregated.
	failedBucket := base.Add(time.Duration(failAfter) * time.Hour)
	var count int
	err = s.Pool().QueryRow(ctx,
		`SELECT count(*) FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
		failedBucket).Scan(&count)
	if err != nil {
		t.Fatalf("query failed bucket: %v", err)
	}
	if count != 0 {
		t.Errorf("bucket %v has %d aggregate rows, want 0 — it must not have committed once the context was cancelled", failedBucket, count)
	}
}

// TestRollupBacklogReAggregatesPreviousHourAfterLateArrival is task-16
// review round 2's regression test for finding 5: it pins the actual
// data-loss scenario the previous-hour reconcile exists to prevent, not
// just that the reconcile code runs. A reading lands in hour H *after* a
// tick has already rolled H up and advanced the watermark past it (clock
// skew, delivery lag); a later tick must still pick it up. Before the
// reconcile existed, deleting that block left every other test passing —
// this is the test that would have caught its absence.
func TestRollupBacklogReAggregatesPreviousHourAfterLateArrival(t *testing.T) {
	ctx, pool, s := newStore(t)
	h := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	early := []quality.Scored{sample(1, "P1", 10, quality.FlagOK, h.Add(time.Minute))}
	if err := s.UpsertSensors(ctx, early); err != nil {
		t.Fatalf("UpsertSensors (early): %v", err)
	}
	if _, err := s.WriteReadings(ctx, early); err != nil {
		t.Fatalf("WriteReadings (early): %v", err)
	}

	// Tick 1: current = H. Rolls up H and advances the watermark to H.
	if _, watermark, err := s.RollupBacklog(ctx, h, 24); err != nil {
		t.Fatalf("RollupBacklog (tick 1): %v", err)
	} else if !watermark.Equal(h) {
		t.Fatalf("watermark after tick 1 = %v, want %v", watermark, h)
	}

	var countBefore int
	if err := pool.QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
		h).Scan(&countBefore); err != nil {
		t.Fatalf("read rollup after tick 1: %v", err)
	}
	if countBefore != 1 {
		t.Fatalf("sample_count after tick 1 = %d, want 1", countBefore)
	}

	// A reading lands in H after the tick that already rolled H up and moved
	// the watermark past it.
	late := []quality.Scored{sample(1, "P1", 999, quality.FlagOK, h.Add(45*time.Minute))}
	if err := s.UpsertSensors(ctx, late); err != nil {
		t.Fatalf("UpsertSensors (late): %v", err)
	}
	if _, err := s.WriteReadings(ctx, late); err != nil {
		t.Fatalf("WriteReadings (late): %v", err)
	}

	// Tick 2: current advances to H+1h. The main loop only touches H+1h;
	// only the previous-hour reconcile re-rolls H.
	if _, watermark, err := s.RollupBacklog(ctx, h.Add(time.Hour), 24); err != nil {
		t.Fatalf("RollupBacklog (tick 2): %v", err)
	} else if !watermark.Equal(h.Add(time.Hour)) {
		t.Fatalf("watermark after tick 2 = %v, want %v", watermark, h.Add(time.Hour))
	}

	var countAfter int
	if err := pool.QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`,
		h).Scan(&countAfter); err != nil {
		t.Fatalf("read rollup after tick 2: %v", err)
	}
	if countAfter != 2 {
		t.Errorf("sample_count for bucket H after the late arrival + reconcile = %d, want 2 — the late reading was not picked up", countAfter)
	}
}

// TestRollupBacklogReturnsCommittedWatermarkOnMidDrainFailure is task-16
// review round 2's regression test for finding 1's residual: when a
// bucket's own transaction fails partway through the drain (pool
// exhaustion, a transient DB error — simulated here via
// SetRollupBacklogFailureForTesting so the context itself stays healthy),
// RollupBacklog must still return whatever watermark actually committed
// before the failure, not the zero time. The caller (internal/ingest)
// depends on this to keep alerting on a genuine backlog even when the drain
// call itself errors.
func TestRollupBacklogReturnsCommittedWatermarkOnMidDrainFailure(t *testing.T) {
	ctx, _, s := newStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if _, _, err := s.RollupBacklog(ctx, base.Add(-time.Hour), 24); err != nil {
		t.Fatalf("seed watermark: %v", err)
	}

	var scored []quality.Scored
	for i := 0; i < 8; i++ {
		scored = append(scored, sample(1, "P1", float64(i), quality.FlagOK, base.Add(time.Duration(i)*time.Hour+time.Minute)))
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	wantErr := errors.New("simulated transient DB error")
	const failAfter = 3
	restore := store.SetRollupBacklogFailureForTesting(func(processed int) error {
		if processed == failAfter {
			return wantErr
		}
		return nil
	})
	defer restore()

	current := base.Add(7 * time.Hour) // 8 buckets outstanding: base .. base+7h
	processed, watermark, err := s.RollupBacklog(ctx, current, 24)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RollupBacklog err = %v, want %v", err, wantErr)
	}
	if processed != failAfter {
		t.Fatalf("processed = %d, want %d", processed, failAfter)
	}

	wantWatermark := base.Add(time.Duration(failAfter-1) * time.Hour)
	if watermark.IsZero() {
		t.Fatal("RollupBacklog returned the zero time alongside the error — the committed watermark was discarded instead of read back")
	}
	if !watermark.Equal(wantWatermark) {
		t.Fatalf("returned watermark = %v, want %v (exactly what committed before the injected failure)", watermark, wantWatermark)
	}
}

func TestTruncateHour(t *testing.T) {
	got := store.TruncateHour(time.Date(2026, 1, 1, 12, 34, 56, 0, time.UTC))
	want := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("TruncateHour = %v, want %v", got, want)
	}
}
