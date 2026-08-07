// Package db owns the connection pool and schema migrations.
package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"airbg.org/internal/db/migrations"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// A statement timeout bounds every query, so a pathological plan cannot
	// pin a connection indefinitely (spec §10).
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	return pgxpool.NewWithConfig(ctx, cfg)
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
