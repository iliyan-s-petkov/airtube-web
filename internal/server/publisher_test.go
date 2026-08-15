package server

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/config"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// testSeries and testStoreConfig restate the two scalar groups this file's
// tests need from airbg.yaml as literals, the same way
// internal/web/assets_render_test.go does — these tests are about the
// publish/store failure path, not about the configured defaults themselves,
// so a full config.LoadFile would be a heavier fixture than the thing under
// test.
var testSeries = config.Series{DefaultMetric: "P2", DefaultWindow: 24 * time.Hour}

var testStoreConfig = config.Store{CoverageThreshold: 1, FreshnessWindow: time.Hour}

// brokenPool builds a pool that never successfully connects: the address is
// unroutable and lazily-established, so pgxpool.NewWithConfig itself succeeds
// but every query against it fails fast. That is enough to drive
// snapshot.Build down its error path without a real database.
func brokenPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://nobody:nobody@127.0.0.1:1/nope?connect_timeout=1")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestPublishNeverStoresOnBuildFailure pins the load-bearing property that a
// failed snapshot.Build must never reach Holder.Store. internal/web and
// internal/api read the holder on every request; storing a nil or half-built
// snapshot on a failed build would turn one bad ingest cycle into a
// user-visible outage across every subsequent request, instead of the
// intended "keep serving the last good snapshot" behaviour.
func TestPublishNeverStoresOnBuildFailure(t *testing.T) {
	holder := snapshot.NewHolder(testSeries)
	good := &snapshot.Snapshot{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		KnownSlugs:  map[string]snapshot.AreaMeta{},
		Overview:    snapshot.Body{JSON: []byte(`{"areas":[]}`), ETag: `"good"`},
	}
	holder.Store(good)

	st := store.New(brokenPool(t), testStoreConfig, 5*time.Second)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := NewPublisher(st, holder, log)

	failuresBefore := snapshotFailures.Value()
	buildsBefore := snapshotBuilds.Value()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pub.Publish(ctx, time.Now().UTC()); err == nil {
		t.Fatal("Publish against an unreachable database returned nil error; " +
			"the test setup no longer exercises the failure path")
	}

	// The property under test: Store must not be reachable on the error path.
	if got := holder.Load(); got != good {
		t.Errorf("holder was overwritten by a failed build; want the pre-existing "+
			"snapshot to remain served, got ETag %q", got.Overview.ETag)
	}

	// The failure must be both counted...
	if got := snapshotFailures.Value(); got != failuresBefore+1 {
		t.Errorf("snapshotFailures = %d, want %d (incremented once)", got, failuresBefore+1)
	}
	// ...and must not be miscounted as a success.
	if got := snapshotBuilds.Value(); got != buildsBefore {
		t.Errorf("snapshotBuilds = %d, want unchanged at %d", got, buildsBefore)
	}
}
