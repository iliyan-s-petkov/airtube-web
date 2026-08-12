// Package ingest runs one poll-score-persist cycle.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"airbg.org/internal/area"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

const (
	// maxBucketsPerTick bounds how many hourly buckets a single RunOnce call
	// will roll up when draining a backlog, so a long outage cannot make one
	// tick run unboundedly long or hold unboundedly many in-flight
	// transactions. 24 buckets is a day's worth of backlog per tick: cheap
	// (24 short transactions), and with the default 5-minute poll interval a
	// week-long backlog (168 buckets, see backlogAlertThreshold) drains in
	// about 7 ticks — well under an hour after the ingest loop resumes.
	// Successive ticks drain whatever is left.
	maxBucketsPerTick = 24

	// backlogAlertThreshold is the gap, in hours, between the rollup
	// watermark and the current hour that triggers an ERROR log. Raw
	// readings are retained for RawRetentionHours before TimescaleDB deletes
	// them; alerting at 168 hours (7 days) leaves roughly 23 days of margin,
	// so an operator gets days of warning to notice and fix a stalled
	// rollup, not hours.
	backlogAlertThreshold = 168

	// RawRetentionHours mirrors the `reading` hypertable's retention policy
	// (internal/db/migrations/00003_rollup_retention.sql: drop_after => 30
	// days). It is exported so a test can assert it still matches the live
	// policy in timescaledb_information.jobs — an edit to that migration's
	// drop_after that forgets this constant would otherwise silently widen
	// (or shrink) the alert's actual margin without anyone noticing
	// (task-16 review finding 4).
	RawRetentionHours = 30 * 24
)

type Fetcher interface {
	Fetch(ctx context.Context) (upstream.Batch, error)
}

type Stats struct {
	Fetched int
	// Skipped is how many upstream entries Normalise could not use. It is
	// carried here, and logged on every cycle, because a schema break that
	// affects every entry is otherwise silent: Fetch returns no error, so the
	// only signal is Fetched dropping to 0, which is indistinguishable from a
	// quiet upstream.
	Skipped int
	Written int
	Flagged map[quality.Flag]int
	// RejectedOutsideBoundary counts distinct sensors this cycle whose
	// coordinates fell outside the national boundary (task 17) and were
	// therefore dropped before scoring or storage. It stays 0 whenever the
	// boundary itself was absent — see the fail-closed handling in RunOnce.
	RejectedOutsideBoundary int
}

// SnapshotPublisher rebuilds the served snapshot. Declared here as an
// interface rather than importing internal/snapshot, so ingest keeps no
// dependency on the serving side and the test needs no database.
type SnapshotPublisher interface {
	Publish(ctx context.Context, now time.Time) error
}

type Ingester struct {
	fetcher   Fetcher
	store     *store.Store
	history   *quality.History
	scorer    *quality.Scorer
	now       func() time.Time
	publisher SnapshotPublisher
}

func New(f Fetcher, s *store.Store, hist *quality.History, scorer *quality.Scorer) *Ingester {
	return &Ingester{fetcher: f, store: s, history: hist, scorer: scorer, now: time.Now}
}

// SetClockForTesting overrides RunOnce's notion of "now" and returns a
// function that restores the previous clock. Without this, a test wanting
// to assert on the exact bucket RunOnce rolls up would have to compute
// store.TruncateHour(time.Now()) itself and hope no hour boundary falls
// between that call and RunOnce's own internal time.Now() call — a real,
// if rare, source of flaky failures. Pinning both to the same value removes
// it (task-16 review round 2, flaky-test finding).
func (i *Ingester) SetClockForTesting(clock func() time.Time) (restore func()) {
	prev := i.now
	i.now = clock
	return func() { i.now = prev }
}

// SetSnapshotPublisher attaches the publisher RunOnce calls at the end of
// every successful cycle. Left unset, `airbg ingest` run as a bare cron job
// with no server attached is fine — publishSnapshot no-ops.
func (i *Ingester) SetSnapshotPublisher(p SnapshotPublisher) { i.publisher = p }

// RunOnce performs a single cycle: fetch, score, persist, roll up.
//
// An error from the fetch aborts the data pipeline for this cycle, leaving
// previously stored data untouched — the caller keeps serving the last good
// snapshot (spec §10). The rollup backlog drain and its alert check
// (rollupBacklog), however, run unconditionally on every call regardless of
// fetch or write outcome (task-16 review finding 1): a sustained upstream
// outage — the single most likely real-world cause of a large backlog — must
// not silence the alert that exists specifically to catch it. Gating that
// step behind fetch success left it dead through exactly the failure mode it
// was built for.
func (i *Ingester) RunOnce(ctx context.Context) (Stats, error) {
	batch, fetchErr := i.fetcher.Fetch(ctx)
	readings := batch.Readings

	stats := Stats{Flagged: make(map[quality.Flag]int)}
	var scored []quality.Scored
	var pipelineErr error

	if fetchErr == nil {
		stats.Fetched = len(readings)
		stats.Skipped = batch.Skipped

		// Surface unusable entries here, before anything downstream can turn
		// "we could not read it" into "there was nothing to read". Normalise
		// only errors when the payload is not a JSON array, so per-entry drift
		// — the exact failure class task 14 hardened the parser against —
		// arrives as a nil error and a smaller slice.
		//
		// Total drift is escalated to ERROR deliberately: a fetch that
		// succeeded at the HTTP level and salvaged nothing from a non-empty
		// payload is an upstream contract break, not a quiet day. It needs to
		// page someone, and it is categorically different from upstream simply
		// returning an empty array (batch.Total() == 0), which is normal and
		// stays quiet.
		switch {
		case batch.Skipped > 0 && len(readings) == 0:
			slog.Error("every upstream entry was unusable — upstream schema may have changed; storing nothing this cycle",
				"skipped", batch.Skipped,
				"fetched", 0)
		case batch.Skipped > 0:
			slog.Warn("some upstream entries were unusable",
				"skipped", batch.Skipped,
				"fetched", len(readings),
				"total", batch.Total())
		}

		// Geographic filter (task 17): upstream's self-reported country is
		// not trusted — sensor 48524 reports "BG" from London — so
		// membership is decided by ST_Covers against the national boundary
		// instead, before anything reaches quality.Score. Placement matters:
		// the spatial outlier check derives its median/MAD from geographic
		// neighbours, and a foreign sensor thousands of kilometres away
		// would already have distorted that neighbourhood by the time the
		// scorer saw it. Filtering here means it never does.
		accepted, rejected, boundaryPresent, filterErr := area.FilterByBoundary(ctx, i.store.Pool(), readings)
		switch {
		case filterErr != nil:
			pipelineErr = fmt.Errorf("ingest: boundary filter: %w", filterErr)

		case !boundaryPresent:
			// Fail closed: the national boundary (area.kind = "country") has
			// never been imported, so there is nothing to test membership
			// against. Ingesting everything unfiltered here would silently
			// reopen exactly the hole this task closes — a foreign sensor
			// corrupting the spatial quality check with no visible symptom
			// until someone notices bad data downstream. Instead this cycle
			// stores nothing and says so at ERROR level: loud, immediately
			// visible in logs and (via stats.Written staying 0) in metrics,
			// and trivially recoverable with one already-existing command
			// (`airbg import-areas <geojson> country`) rather than requiring
			// anyone to hunt down and repair corrupted statistics after the
			// fact.
			slog.Error("national boundary not imported — rejecting entire batch this cycle (fail closed); run: airbg import-areas <path.geojson> country",
				"fetched", stats.Fetched,
				"required_kind", area.NationalBoundaryKind,
				"remedy", "airbg import-areas <path.geojson> country")

		default:
			stats.RejectedOutsideBoundary = rejected
			switch {
			case rejected > 0 && len(accepted) == 0:
				// Every sensor rejected while a boundary *does* exist. The
				// operational outcome is identical to the absent-boundary case
				// above — this cycle stores nothing — so the severity must be
				// too. Previously this was a WARN, which made the
				// better-diagnosed case (absent boundary: ERROR naming the
				// remedy command) the *less* severe one, and left the more
				// likely failure quieter than it.
				//
				// It is more likely because operators must source their own
				// national outline (README §Areas). A degenerate outline — an
				// empty geometry, which inserts happily as MULTIPOLYGON EMPTY
				// since EMPTY is not NULL — matches nothing, so ST_Covers
				// rejects every sensor while the presence check still finds the
				// row and reports the boundary present. Import now rejects
				// invalid and empty geometry outright, but a boundary that is
				// merely *wrong* (right shape, wrong place) cannot be caught at
				// import, and this is the only place it becomes visible.
				//
				// The message names both plausible causes, because from here
				// they are genuinely indistinguishable and the operator can
				// check the cheap one first.
				slog.Error("every sensor was rejected by the national boundary — storing nothing this cycle; the boundary geometry is probably wrong or in the wrong place, or upstream sent only foreign sensors; verify with: airbg import-areas <path.geojson> country",
					"fetched", stats.Fetched,
					"rejected_sensors", rejected,
					"required_kind", area.NationalBoundaryKind)
			case rejected > 0:
				slog.Warn("rejected sensors outside national boundary",
					"sensors", rejected)
			}

			scored = i.scorer.Score(accepted, i.history)
			for _, s := range scored {
				stats.Flagged[s.Flag]++
			}

			if len(scored) > 0 {
				if err := i.store.UpsertSensors(ctx, scored); err != nil {
					pipelineErr = fmt.Errorf("ingest: upsert sensors: %w", err)
				} else if written, err := i.store.WriteReadings(ctx, scored); err != nil {
					pipelineErr = fmt.Errorf("ingest: write readings: %w", err)
				} else {
					stats.Written = int(written)
				}
			}
		}
	}

	// Anchored to wall-clock time, not any single reading's timestamp: this
	// step must run even when there is no batch at all this cycle (fetch
	// failed, or returned nothing), so it cannot depend on batch data being
	// present (task-16 review finding 2).
	rollupErr := i.rollupBacklog(ctx, i.now())

	switch {
	case fetchErr != nil:
		return Stats{}, fmt.Errorf("ingest: fetch: %w", fetchErr)
	case pipelineErr != nil:
		return stats, pipelineErr
	case rollupErr != nil:
		return stats, fmt.Errorf("ingest: rollup: %w", rollupErr)
	}

	var assigned, revoked int64
	if len(scored) > 0 {
		var err error
		assigned, revoked, err = area.AssignSensors(ctx, i.store.Pool())
		if err != nil {
			return stats, fmt.Errorf("ingest: assign sensors: %w", err)
		}
	}

	// Emitted unconditionally, including for a cycle that scored nothing.
	// Previously RunOnce returned before this line whenever len(scored) == 0,
	// so a collector that fetched successfully and stored nothing — the exact
	// outcome of a total upstream schema break, or of a boundary that matches
	// nothing — logged *nothing at all*. The only signal distinguishing it from
	// a healthy system was the absence of a log line, which no alerting rule
	// short of a deliberate dead-man's-switch will notice. "0 fetched, 0
	// written" must be a visible event, not silence.
	slog.Info("ingest cycle complete",
		"fetched", stats.Fetched,
		"skipped", stats.Skipped,
		"written", stats.Written,
		"rejected_outside_boundary", stats.RejectedOutsideBoundary,
		"areas_assigned", assigned,
		"areas_revoked", revoked,
		"out_of_range", stats.Flagged[quality.FlagOutOfRange],
		"stuck", stats.Flagged[quality.FlagStuck],
		"spatial_outlier", stats.Flagged[quality.FlagSpatialOutlier],
	)

	i.publishSnapshot(ctx)

	return stats, nil
}

// publishSnapshot rebuilds the served snapshot at the end of a cycle.
//
// Errors are logged, never returned: a stale snapshot serves last cycle's data,
// while a failed RunOnce loses this cycle's readings and may restart-loop the
// collector. The weaker failure mode wins.
func (i *Ingester) publishSnapshot(ctx context.Context) {
	if i.publisher == nil {
		return
	}
	// i.now() is the injectable clock this package already uses everywhere;
	// calling time.Now directly would make the publisher the one part of the
	// cycle that ignores a test's fixed clock.
	if err := i.publisher.Publish(ctx, i.now()); err != nil {
		slog.Error("snapshot publish failed; serving the previous snapshot",
			"error", err)
	}
}

// rollupBacklog drains the rollup backlog (capped at maxBucketsPerTick) and,
// if the gap between the watermark and the current hour has crossed
// backlogAlertThreshold, logs at ERROR with enough detail to act on.
//
// The gap is evaluated whenever RollupBacklog returned a usable watermark,
// independent of whether it also returned an error (task-16 review round 2,
// finding 1 residual). A database problem severe enough to stall the
// rollup — a single bucket's transaction failing, or the end-of-call
// previous-hour reconcile failing — is exactly the scenario in which the
// backlog grows toward the retention boundary; it must not be the same
// condition that silences the alert meant to catch it. RollupBacklog
// itself makes a best-effort attempt to return whatever watermark is
// actually on record even when it errors, so watermark.IsZero() is the
// right "nothing usable at all" signal here, not err != nil.
func (i *Ingester) rollupBacklog(ctx context.Context, now time.Time) error {
	_, watermark, rollupErr := i.store.RollupBacklog(ctx, now, maxBucketsPerTick)

	if !watermark.IsZero() {
		current := store.TruncateHour(now)
		gap := BacklogHours(watermark, current)
		if gap >= backlogAlertThreshold {
			slog.Error("rollup backlog approaching raw retention boundary",
				"watermark_bucket", watermark,
				"current_bucket", current,
				"gap_hours", gap,
				"margin_hours", RawRetentionHours-gap,
			)
		}
	}

	return rollupErr
}

// BacklogHours returns the whole number of hours between the watermark
// bucket and the current bucket. It is exported (rather than folded silently
// into a log line) so the alert threshold's crossing behaviour can be
// asserted directly in tests without scraping log output.
func BacklogHours(watermark, current time.Time) int {
	return int(store.TruncateHour(current).Sub(store.TruncateHour(watermark)).Hours())
}

// Loop runs RunOnce on a ticker until the context is cancelled. A failed cycle
// is logged and retried on the next tick; it never terminates the loop.
func (i *Ingester) Loop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if _, err := i.RunOnce(ctx); err != nil {
			slog.Error("ingest cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
