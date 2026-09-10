// Package testsupport provides throwaway PostgreSQL instances for tests.
// Every test gets a real database with PostGIS and TimescaleDB, because the
// behaviour under test (spatial containment, hypertables, retention) cannot
// be faked.
package testsupport

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
)

// StoreConfig mirrors airbg.yaml's store: block, the same threshold every
// other package's local testStoreConfig helper already duplicates.
func StoreConfig() config.Store {
	return config.Store{CoverageThreshold: 3, FreshnessWindow: 2 * time.Hour}
}

// testDatabaseConfig mirrors airbg.yaml's database.statement_timeouts. URL is
// filled in by the caller; it is a credential and never belongs in a
// committed fixture.
func testDatabaseConfig(url string) config.Database {
	return config.Database{
		URL: url,
		StatementTimeouts: config.StatementTimeouts{
			Default:  15 * time.Second,
			Assign:   60 * time.Second,
			Operator: 10 * time.Minute,
			Series:   5 * time.Second,
		},
	}
}

// NewPostgres returns a pool opened exactly the way production opens one —
// through db.Open, so the pool-wide statement_timeout is in force. It used to
// call pgxpool.New directly, which meant no container test ever exercised the
// timeout the collector actually runs under; a query that would abort in
// production passed happily in tests.
//
// (No import cycle: every test in internal/db is in external package db_test.)
func NewPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, testDatabaseConfig(NewPostgresURL(t)))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewPostgresURL returns an empty database's connection string, for tests that
// need to control pool configuration themselves. When the package's TestMain
// has called StartSharedPostgres it is a new database inside that one
// container; otherwise it is a container of this test's own. Either way the
// database is empty and the caller runs db.Migrate.
func NewPostgresURL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	if shared, err := freshSharedDatabase(ctx); err != nil {
		t.Fatalf("shared postgres: %v", err)
	} else if shared != "" {
		return shared
	}

	container, err := runPostgresContainer(ctx)
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}
