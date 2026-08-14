// Package db owns the connection pool and schema migrations.
package db

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"airbg.org/internal/config"
	"airbg.org/internal/db/migrations"
)

// Open opens a pool sized by whatever the connection string asks for, falling
// back to pgxpool's own max(4, numCPU) default. Correct for the one-shot
// subcommands, which run a single workload and then exit.
//
// The serve command must use OpenPair instead.
func Open(ctx context.Context, dbCfg config.Database) (*pgxpool.Pool, error) {
	return open(ctx, dbCfg, 0)
}

// OpenPair opens the two pools the serve command needs: one for request
// handlers and one for the collector.
//
// Two pools rather than one is a bulkhead, not a tuning knob, and the failure
// it prevents needs no traffic and no attacker. AssignSensors runs under the
// configured assign statement timeout on every poll cycle, so the collector may
// legitimately hold a connection for as long as that allows. While both
// workloads shared one pool of max(4, numCPU), request handlers blocked inside
// Acquire behind the poll cycle on a schedule — and every control in place saw
// a healthy system, because it was one. Rate limiting bounds one client and
// admission control bounds the crowd; neither can bound one workload's effect
// on another's capacity. Only separate pools can.
//
// Sizes are required and must be positive: pgxpool reads MaxConns <= 0 as "use
// the default", so accepting a zero here would quietly restore the host's core
// count as the deployed capacity, which is the thing this is fixing.
func OpenPair(ctx context.Context, dbCfg config.Database) (api, collector *pgxpool.Pool, err error) {
	if dbCfg.APIConns < 1 {
		return nil, nil, fmt.Errorf("db: api pool size must be at least 1, got %d", dbCfg.APIConns)
	}
	if dbCfg.CollectorConns < 1 {
		return nil, nil, fmt.Errorf("db: collector pool size must be at least 1, got %d", dbCfg.CollectorConns)
	}

	api, err = open(ctx, dbCfg, dbCfg.APIConns)
	if err != nil {
		return nil, nil, fmt.Errorf("db: open api pool: %w", err)
	}
	collector, err = open(ctx, dbCfg, dbCfg.CollectorConns)
	if err != nil {
		// Never hand back a half-built pair: a caller that ignored the error
		// would proceed with one live pool and one nil, which is the shared-pool
		// bug wearing a disguise.
		api.Close()
		return nil, nil, fmt.Errorf("db: open collector pool: %w", err)
	}
	return api, collector, nil
}

// open builds one pool. maxConns of 0 means "leave the configured size alone";
// any positive value overrides pool_max_conns from the connection string,
// because one shared string cannot express two different per-pool sizes.
func open(ctx context.Context, dbCfg config.Database, maxConns int32) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dbCfg.URL)
	if err != nil {
		return nil, err
	}
	// A statement timeout bounds every query, so a pathological plan cannot
	// pin a connection indefinitely (spec §10).
	//
	// PostgreSQL reads a bare statement_timeout as milliseconds. Formatting the
	// duration instead ("15s") also works, but an explicit millisecond
	// conversion is the one that cannot be broken by a unit-suffix change.
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] =
		strconv.FormatInt(dbCfg.StatementTimeouts.Default.Milliseconds(), 10)
	if maxConns > 0 {
		poolCfg.MaxConns = maxConns
	}
	return pgxpool.NewWithConfig(ctx, poolCfg)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	return goose.UpContext(ctx, sqlDB, ".")
}
