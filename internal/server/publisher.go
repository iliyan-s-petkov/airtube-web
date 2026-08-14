package server

import (
	"context"
	"log/slog"
	"time"

	"airbg.org/internal/metrics"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

var (
	snapshotBuilds   = metrics.Counter("airbg_snapshot_builds_total", "Snapshots built.")
	snapshotFailures = metrics.Counter("airbg_snapshot_build_failures_total", "Snapshot builds that failed.")
	snapshotAge      = metrics.Gauge("airbg_snapshot_age_seconds", "Seconds since the served snapshot was built.")
)

// Publisher rebuilds the snapshot and swaps it into the holder. It satisfies
// ingest.SnapshotPublisher.
type Publisher struct {
	store  *store.Store
	holder *snapshot.Holder
	log    *slog.Logger
}

func NewPublisher(st *store.Store, h *snapshot.Holder, log *slog.Logger) *Publisher {
	return &Publisher{store: st, holder: h, log: log}
}

func (p *Publisher) Publish(ctx context.Context, now time.Time) error {
	snap, err := snapshot.Build(ctx, p.store, p.holder, now)
	if err != nil {
		snapshotFailures.Inc()
		return err
	}
	// Stored only on success. A partial snapshot must never replace a good one:
	// serving last cycle's complete data beats serving this cycle's half of it.
	p.holder.Store(snap)
	snapshotBuilds.Inc()
	p.log.Info("snapshot published",
		"areas", len(snap.KnownSlugs), "generated_at", snap.GeneratedAt)
	return nil
}

// ObserveAge updates the age gauge. Called from the metrics handler path rather
// than on a ticker, so the value is computed at scrape time and a wedged
// publisher shows an age that keeps climbing.
func (p *Publisher) ObserveAge(now time.Time) {
	if snap := p.holder.Load(); snap != nil {
		snapshotAge.Set(now.Sub(snap.GeneratedAt).Seconds())
	}
}
