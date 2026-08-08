package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const rollupSQL = `INSERT INTO reading_hourly
	     (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
	 SELECT $1, sensor_id, metric, avg(value), min(value), max(value), count(*)
	 FROM reading
	 WHERE time >= $1 AND time < $1 + interval '1 hour'
	   AND quality IN ('ok', 'no_neighbours')
	 GROUP BY sensor_id, metric
	 ON CONFLICT (sensor_id, metric, bucket) DO UPDATE
	   SET avg_value = EXCLUDED.avg_value,
	       min_value = EXCLUDED.min_value,
	       max_value = EXCLUDED.max_value,
	       sample_count = EXCLUDED.sample_count`

// watermarkSQL upserts the singleton rollup_watermark row. The WHERE guard on
// the UPDATE makes the advance monotonic: even if this were ever called with
// buckets out of order, the stored watermark can only move forward, never
// backward, so it can never claim to have rolled up more than it actually
// has. A consequence (task-16 review finding 3): the UPDATE can silently
// affect zero rows when the guard rejects it, so a caller must not assume an
// advance happened just because rollupAndAdvance returned no error — it must
// read the watermark back from the database to know what actually stuck.
const watermarkSQL = `INSERT INTO rollup_watermark (id, bucket, updated_at)
	 VALUES (true, $1, now())
	 ON CONFLICT (id) DO UPDATE
	   SET bucket = EXCLUDED.bucket, updated_at = EXCLUDED.updated_at
	   WHERE EXCLUDED.bucket > rollup_watermark.bucket`

// RollupHour recomputes the hourly aggregate for one bucket from raw readings.
//
// Only readings whose quality flag permits aggregation are included, so a
// flagged sensor is structurally incapable of moving a published average
// (spec §5.3). Recomputing rather than incrementing makes the operation
// idempotent and safe to re-run over any bucket.
//
// This does not touch the watermark — callers that need the watermark to
// advance in lockstep with the data (the ingest loop's backlog drain) should
// use RollupBacklog instead, which performs both under a single transaction.
func (s *Store) RollupHour(ctx context.Context, bucket time.Time) (int64, error) {
	bucket = TruncateHour(bucket)
	tag, err := s.pool.Exec(ctx, rollupSQL, bucket)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Watermark returns the last bucket successfully rolled up and when that
// happened. found is false on a fresh database where the watermark has never
// been set — the caller must not interpret the zero time as "rolled up
// through the Unix epoch".
func (s *Store) Watermark(ctx context.Context) (bucket time.Time, updatedAt time.Time, found bool, err error) {
	err = s.pool.QueryRow(ctx, `SELECT bucket, updated_at FROM rollup_watermark WHERE id`).
		Scan(&bucket, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	return bucket, updatedAt, true, nil
}

// rollupAndAdvance recomputes one bucket's aggregate and advances the
// watermark to it inside a single transaction. Doing both together means it
// is structurally impossible for the watermark to report a bucket as rolled
// up when the aggregate write did not commit (or the reverse): either both
// happen or neither does.
func (s *Store) rollupAndAdvance(ctx context.Context, bucket time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	tag, err := tx.Exec(ctx, rollupSQL, bucket)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, watermarkSQL, bucket); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// rollupBacklogHook, when non-nil, is invoked after each bucket in
// RollupBacklog's main drain loop commits successfully, before the next
// iteration begins. It exists solely so tests can deterministically inject a
// context cancellation (or other failure) partway through a multi-bucket
// drain, to prove the watermark reflects exactly what committed — no gap, no
// overshoot — even when a later bucket in the same call fails (task-16
// review finding 6). Production code never sets it; see
// SetRollupBacklogHookForTesting.
var rollupBacklogHook func(processed int, bucket time.Time)

// SetRollupBacklogHookForTesting installs h as RollupBacklog's per-bucket
// hook and returns a function that restores the previous hook. Exported
// only so tests in other packages (internal/store's tests, or a future
// internal/ingest test) can use it; production code must never call it.
func SetRollupBacklogHookForTesting(h func(processed int, bucket time.Time)) (restore func()) {
	prev := rollupBacklogHook
	rollupBacklogHook = h
	return func() { rollupBacklogHook = prev }
}

// RollupBacklog rolls up every bucket from just after the watermark through
// the bucket containing now, oldest first, advancing the watermark after
// each bucket's aggregate rows are durably committed (see rollupAndAdvance).
// Work is capped at maxBuckets per call so a long backlog cannot stall a
// single call indefinitely or hold an unbounded number of buckets in
// flight; the caller is expected to call again on the next tick to drain
// whatever is left.
//
// The returned watermark is always read back from the database rather than
// tracked locally while looping (task-16 review finding 3): watermarkSQL's
// monotonic guard can make an individual advance a silent no-op, so a
// locally accumulated value could over-report progress and mask exactly the
// backlog the caller's alert exists to catch.
//
// Bootstrap: on a fresh database (no watermark row yet) this does not walk
// back to the beginning of time — it starts at, and rolls up, only the
// current hour. That keeps first-run cost bounded and still gets the
// current hour's data aggregated immediately, matching the pre-watermark
// behaviour.
//
// Steady state: even when the watermark is already caught up to the current
// hour, the current hour itself is always (re-)rolled up, because new
// readings can keep landing in it after the watermark advanced past it.
// Recomputing is idempotent so this is safe and cheap. In addition, the hour
// immediately before "current" is unconditionally reconciled on every call
// once the watermark has genuinely reached it (task-16 review finding 5):
// once the wall clock moves an hour forward, the loop above stops touching
// the hour that just ended, but readings for it can still arrive briefly
// afterwards (clock skew, delivery lag) — without this, the watermark would
// cement that hour as "done" while data kept trickling in. The reconcile
// step never moves the watermark backward and is skipped entirely while a
// real backlog is still outstanding, so it can never paper over undrained
// history.
func (s *Store) RollupBacklog(ctx context.Context, now time.Time, maxBuckets int) (processed int, watermark time.Time, err error) {
	current := TruncateHour(now)

	wm, _, found, err := s.Watermark(ctx)
	if err != nil {
		return 0, time.Time{}, err
	}

	start := current
	if found {
		start = wm.Add(time.Hour)
		if start.After(current) {
			// Already caught up beyond (or exactly at) the current hour.
			// Nothing historical to drain, but the current hour is always
			// re-rolled below to pick up any readings written since the
			// watermark last advanced.
			start = current
		}
	}

	for bucket := start; processed < maxBuckets && !bucket.After(current); bucket = bucket.Add(time.Hour) {
		if _, err := s.rollupAndAdvance(ctx, bucket); err != nil {
			return processed, time.Time{}, err
		}
		processed++
		if rollupBacklogHook != nil {
			rollupBacklogHook(processed, bucket)
		}
	}

	watermark, _, wmFound, err := s.Watermark(ctx)
	if err != nil {
		return processed, time.Time{}, err
	}
	if !wmFound {
		// Nothing has ever been committed (only reachable if maxBuckets was
		// 0 on a fresh database — not a configuration the ingest loop ever
		// uses, but keep the return value well-defined rather than a
		// misleading zero time).
		return processed, time.Time{}, nil
	}

	previousHour := current.Add(-time.Hour)
	if !watermark.Before(previousHour) {
		if _, err := s.rollupAndAdvance(ctx, previousHour); err != nil {
			return processed, watermark, err
		}
	}

	return processed, watermark, nil
}
