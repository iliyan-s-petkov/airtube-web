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
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"airbg.org/internal/db"
)

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
	pool, err := db.Open(ctx, NewPostgresURL(t))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewPostgresURL starts a throwaway instance and returns its connection string,
// for tests that need to control pool configuration themselves.
func NewPostgresURL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"timescale/timescaledb-ha:pg18",
		tcpostgres.WithDatabase("airbg"),
		tcpostgres.WithUsername("airbg"),
		tcpostgres.WithPassword("airbg"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}
