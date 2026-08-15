package db_test

import (
	"context"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

// testDBConfig mirrors airbg.yaml's database.statement_timeouts, with the
// pool sizes supplied per test so OpenPair's size-related behaviour stays
// observable.
func testDBConfig(url string, apiConns, collectorConns int32) config.Database {
	return config.Database{
		URL:            url,
		APIConns:       apiConns,
		CollectorConns: collectorConns,
		StatementTimeouts: config.StatementTimeouts{
			Default:  15 * time.Second,
			Assign:   60 * time.Second,
			Operator: 10 * time.Minute,
			Series:   5 * time.Second,
		},
	}
}

// TestOpenPairIsolatesTheCollectorFromRequestHandlers is the bulkhead.
//
// The failure it prevents needs no traffic and no attacker. AssignSensors runs
// under a 60s statement timeout on every poll cycle, so the collector may
// legitimately hold a connection for a minute; while both workloads shared one
// pool, request handlers blocked in Acquire behind it on a schedule, and every
// control already in place — the per-IP limiter, the admission cap — correctly
// observed a perfectly healthy system, because it was one.
//
// Both pools are deliberately sized to one connection. Holding the collector's
// only connection is a *deterministic* saturation: no sleep, no race between
// the two goroutines, and the API query either gets a connection from its own
// pool immediately or blocks until its deadline. There is no third outcome.
func TestOpenPairIsolatesTheCollectorFromRequestHandlers(t *testing.T) {
	ctx := context.Background()
	url := testsupport.NewPostgresURL(t)

	api, collector, err := db.OpenPair(ctx, testDBConfig(url, 1, 1))
	if err != nil {
		t.Fatalf("db.OpenPair: %v", err)
	}
	defer api.Close()
	defer collector.Close()

	// Occupy the collector's only connection for the rest of the test.
	held, err := collector.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the collector's connection: %v", err)
	}
	defer held.Release()

	// Self-validation. If the collector pool were not genuinely saturated, the
	// assertion below would pass even with both consumers sharing one pool, and
	// the test would prove nothing in either direction.
	starved, cancelStarved := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancelStarved()
	if _, err := collector.Acquire(starved); err == nil {
		t.Fatal("the collector pool handed out a second connection; it is not saturated, so this test cannot detect a shared pool")
	}

	// The API pool must be entirely unaffected.
	apiCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var got int
	if err := api.QueryRow(apiCtx, `SELECT 1`).Scan(&got); err != nil {
		t.Fatalf("an API query failed while the collector held its own pool's connection: %v\n"+
			"the two pools are not independent — request handlers starve behind the poll cycle", err)
	}
	if got != 1 {
		t.Errorf("SELECT 1 = %d, want 1", got)
	}
}

// TestOpenPairAppliesTheRequestedSizes pins the plumbing. Without it, OpenPair
// could ignore both arguments and every other test here would still pass:
// isolation comes from there being two pools, and the sizes are what make the
// bulkhead a stated capacity rather than a property of the container's core
// count.
func TestOpenPairAppliesTheRequestedSizes(t *testing.T) {
	ctx := context.Background()

	api, collector, err := db.OpenPair(ctx, testDBConfig(testsupport.NewPostgresURL(t), 7, 3))
	if err != nil {
		t.Fatalf("db.OpenPair: %v", err)
	}
	defer api.Close()
	defer collector.Close()

	if n := api.Config().MaxConns; n != 7 {
		t.Errorf("api MaxConns = %d, want 7", n)
	}
	if n := collector.Config().MaxConns; n != 3 {
		t.Errorf("collector MaxConns = %d, want 3", n)
	}
}

// TestOpenPairOverridesPoolMaxConnsInTheURL settles the precedence question a
// deployment will eventually ask. The argument is a per-pool decision; a single
// pool_max_conns in the shared connection string cannot express two different
// sizes, so it must lose rather than silently win for one of the two pools.
func TestOpenPairOverridesPoolMaxConnsInTheURL(t *testing.T) {
	ctx := context.Background()

	api, collector, err := db.OpenPair(ctx, testDBConfig(testsupport.NewPostgresURL(t)+"&pool_max_conns=17", 7, 3))
	if err != nil {
		t.Fatalf("db.OpenPair: %v", err)
	}
	defer api.Close()
	defer collector.Close()

	if n := api.Config().MaxConns; n != 7 {
		t.Errorf("api MaxConns = %d, want 7 — pool_max_conns in the URL won", n)
	}
	if n := collector.Config().MaxConns; n != 3 {
		t.Errorf("collector MaxConns = %d, want 3 — pool_max_conns in the URL won", n)
	}
}

// TestOpenPairRejectsNonPositiveSizes fails closed. pgxpool treats MaxConns <= 0
// as "use the default", so passing a zero through would quietly restore
// max(4, numCPU) on both pools — the bulkhead would still exist but its sizing
// would be back to a property of the host, which is the thing being fixed.
func TestOpenPairRejectsNonPositiveSizes(t *testing.T) {
	ctx := context.Background()
	url := testsupport.NewPostgresURL(t)

	for _, tc := range []struct {
		name                string
		apiConns, collConns int32
	}{
		{"zero api", 0, 4},
		{"zero collector", 8, 0},
		{"negative api", -1, 4},
		{"negative collector", 8, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, collector, err := db.OpenPair(ctx, testDBConfig(url, tc.apiConns, tc.collConns))
			if err == nil {
				api.Close()
				collector.Close()
				t.Fatalf("OpenPair(api=%d, collector=%d) succeeded; non-positive sizes must be rejected", tc.apiConns, tc.collConns)
			}
		})
	}
}

// TestOpenPairReturnsNoPoolsAlongsideAnError pins the error contract: a caller
// that gets an error must not also get a usable half-built pair. Without this,
// a caller written as `api, collector, _ := OpenPair(...)` would proceed with
// one live pool and one nil, which is the shared-pool bug wearing a disguise.
//
// Stated honestly: this asserts the contract, not the absence of a leak. Whether
// OpenPair closed the first pool before returning is not observable from
// outside it — the container accepts far more connections than this test uses,
// so a leaked pool of one would change nothing measurable here. The Close is
// there because it is correct, and it is reviewed by reading, not by this test.
func TestOpenPairReturnsNoPoolsAlongsideAnError(t *testing.T) {
	ctx := context.Background()

	api, collector, err := db.OpenPair(ctx, testDBConfig(testsupport.NewPostgresURL(t), 1, -1))
	if err == nil {
		if api != nil {
			api.Close()
		}
		if collector != nil {
			collector.Close()
		}
		t.Fatal("OpenPair with a negative collector size succeeded")
	}
	if api != nil {
		api.Close()
		t.Error("OpenPair returned a live api pool alongside an error")
	}
	if collector != nil {
		collector.Close()
		t.Error("OpenPair returned a live collector pool alongside an error")
	}
}
