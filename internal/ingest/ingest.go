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
	// readings are retained for 30 days (720 hours) before TimescaleDB
	// deletes them; alerting at 168 hours (7 days) leaves roughly 23 days of
	// margin, so an operator gets days of warning to notice and fix a
	// stalled rollup, not hours.
	backlogAlertThreshold = 168
)

type Fetcher interface {
	Fetch(ctx context.Context) ([]upstream.Reading, error)
}

type Stats struct {
	Fetched int
	Written int
	Flagged map[quality.Flag]int
}

type Ingester struct {
	fetcher Fetcher
	store   *store.Store
	history *quality.History
}

func New(f Fetcher, s *store.Store, hist *quality.History) *Ingester {
	return &Ingester{fetcher: f, store: s, history: hist}
}

// RunOnce performs a single cycle: fetch, score, persist, roll up.
//
// An error from the fetch aborts the cycle, leaving previously stored data
// untouched — the caller keeps serving the last good snapshot (spec §10).
func (i *Ingester) RunOnce(ctx context.Context) (Stats, error) {
	readings, err := i.fetcher.Fetch(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("ingest: fetch: %w", err)
	}

	scored := quality.Score(readings, i.history)

	stats := Stats{Fetched: len(readings), Flagged: make(map[quality.Flag]int)}
	for _, s := range scored {
		stats.Flagged[s.Flag]++
	}

	if len(scored) == 0 {
		return stats, nil
	}

	if err := i.store.UpsertSensors(ctx, scored); err != nil {
		return stats, fmt.Errorf("ingest: upsert sensors: %w", err)
	}
	written, err := i.store.WriteReadings(ctx, scored)
	if err != nil {
		return stats, fmt.Errorf("ingest: write readings: %w", err)
	}
	stats.Written = int(written)

	// Drain the rollup backlog from the watermark forward through the
	// current hour, not just the bucket this batch landed in — otherwise a
	// stalled rollup (crash loop, outage, a swallowed error) would leave
	// older hours never aggregated, and the 30-day raw retention policy
	// would delete them before RunOnce ever got back around to them.
	//
	// "Current hour" is anchored to the batch's own timestamp rather than
	// wall-clock time: against live upstream data the two are the same
	// thing (the batch is fresh), but pinning to the data lets tests (and
	// the archive backfill's use of historical data) reason about "current"
	// deterministically instead of racing the real clock.
	if err := i.rollupBacklog(ctx, scored[0].Reading.Timestamp); err != nil {
		return stats, fmt.Errorf("ingest: rollup: %w", err)
	}

	if _, err := area.AssignSensors(ctx, i.store.Pool()); err != nil {
		return stats, fmt.Errorf("ingest: assign sensors: %w", err)
	}

	slog.Info("ingest cycle complete",
		"fetched", stats.Fetched,
		"written", stats.Written,
		"out_of_range", stats.Flagged[quality.FlagOutOfRange],
		"stuck", stats.Flagged[quality.FlagStuck],
		"spatial_outlier", stats.Flagged[quality.FlagSpatialOutlier],
	)
	return stats, nil
}

// rawRetention mirrors the `reading` hypertable's retention policy
// (internal/db/migrations/00003_rollup_retention.sql: drop_after => 30 days).
// It is used only to compute the remaining margin reported in the backlog
// alert, so an operator does not have to go look the retention window up.
const rawRetentionHours = 30 * 24

// rollupBacklog drains the rollup backlog (capped at maxBucketsPerTick) and,
// if the gap between the watermark and the current hour has crossed
// backlogAlertThreshold, logs at ERROR with enough detail to act on.
func (i *Ingester) rollupBacklog(ctx context.Context, now time.Time) error {
	_, watermark, err := i.store.RollupBacklog(ctx, now, maxBucketsPerTick)
	if err != nil {
		return err
	}

	current := store.TruncateHour(now)
	gap := BacklogHours(watermark, current)
	if gap >= backlogAlertThreshold {
		slog.Error("rollup backlog approaching raw retention boundary",
			"watermark_bucket", watermark,
			"current_bucket", current,
			"gap_hours", gap,
			"margin_hours", rawRetentionHours-gap,
		)
	}
	return nil
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
