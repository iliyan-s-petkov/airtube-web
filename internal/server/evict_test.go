package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

// TestSeriesLimiterEvictsOnItsOwnInterval pins that
// ratelimit.series.evict_interval drives the series limiter's sweep, and that
// ratelimit.api.evict_interval does not.
//
// This is a regression test for a key that existed and was read by nothing:
// startEvicting once swept all three limiters on the API interval, which was
// invisible only because airbg.yaml shipped "5m" for both. The two intervals
// below are made wildly different (5ms vs 1h) precisely so that the shared-value
// version cannot pass: with the wiring reverted, the series limiter would wait
// an hour and the assertion below times out.
//
// Bounding the limiter map is a memory-exhaustion defence, so "the operator
// tightened the interval and nothing happened" is a DoS finding, not a tidiness
// one.
func TestSeriesLimiterEvictsOnItsOwnInterval(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}

	// The API interval is the value the reverted wiring would use. An hour is
	// far longer than this test's deadline, so a sweep that fires at all within
	// the deadline can only have come from the series interval.
	cfg.RateLimit.API.EvictInterval = time.Hour
	cfg.RateLimit.Series.EvictInterval = 5 * time.Millisecond
	// TTL is read by ratelimit.New, so it must be set before the server is
	// built. A millisecond makes the entry evictable immediately.
	cfg.RateLimit.Series.TTL = time.Millisecond

	holder := snapshot.NewHolder(cfg.Series)
	srv, err := New(Options{Config: cfg, Catalogue: cat, Snapshots: holder})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// One entry in the series limiter's map, then wait for a sweep to drop it.
	srv.seriesLimiter.Allow("203.0.113.7")
	if got := srv.seriesLimiter.Len(); got != 1 {
		t.Fatalf("series limiter Len() = %d after one Allow, want 1", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.startEvicting(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.seriesLimiter.Len() == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("series limiter still holds %d entries after 2s with "+
		"ratelimit.series.evict_interval = %v (ratelimit.api.evict_interval = %v); "+
		"the series limiter is sweeping on the wrong interval",
		srv.seriesLimiter.Len(), cfg.RateLimit.Series.EvictInterval, cfg.RateLimit.API.EvictInterval)
}
