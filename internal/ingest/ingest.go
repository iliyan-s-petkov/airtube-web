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

	// Roll up the bucket the batch landed in. Recomputing is idempotent, so
	// re-rolling the same hour every cycle is correct and cheap.
	bucket := store.TruncateHour(scored[0].Reading.Timestamp)
	if _, err := i.store.RollupHour(ctx, bucket); err != nil {
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
