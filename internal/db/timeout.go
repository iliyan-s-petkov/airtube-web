package db

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// StatementTimeoutValue renders a duration for SET LOCAL statement_timeout.
//
// Milliseconds, explicitly: a duration's default String() (e.g. "10m0s") is
// not PostgreSQL syntax, and "10min" is valid PostgreSQL but only by accident
// of its parser — there is no "min" unit in time.ParseDuration, so it cannot
// round-trip through configuration. Milliseconds is the one representation
// both sides agree on.
//
// The scoped timeouts this renders — AssignSensors' area x sensor join,
// PurgeOutsideBoundary's bulk deletes, and the series-scoped queries in
// internal/store — exist because the pool-wide default (see Open) is the
// right bound for the collector's ordinary read/write path and for every
// request handler, but the wrong bound for those three specific workloads.
// Scoping the exception per transaction, rather than raising the default,
// keeps the DoS protection in force everywhere else.
func StatementTimeoutValue(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}

// SetLocalStatementTimeout raises statement_timeout for the duration of tx
// only. Postgres resets a local setting at transaction end, so no connection
// returns to the pool with a relaxed timeout.
//
// It uses set_config rather than `SET LOCAL`, because SET does not accept bind
// parameters and the alternative would be interpolating the value into SQL
// text. Concatenated SQL is forbidden project-wide — the legacy app's InfluxQL
// injection hole is a stated reason this rewrite exists — and "but the value is
// a constant" is exactly the exception that erodes a rule like that. set_config
// takes the value as $1.
func SetLocalStatementTimeout(ctx context.Context, tx pgx.Tx, value string) error {
	_, err := tx.Exec(ctx, `SELECT set_config('statement_timeout', $1, true)`, value)
	return err
}
