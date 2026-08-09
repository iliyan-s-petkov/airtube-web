package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// StatementTimeout values, as Postgres accepts them.
//
// The pool-wide default (see Open) is the right bound for the collector's
// ordinary read/write path and for every Phase 2 request handler: it stops a
// pathological plan pinning a connection indefinitely, which is a DoS control
// the API will need. It is the wrong bound for two specific workloads, and the
// answer is to scope the exception rather than raise the default — raising the
// default would remove the protection everywhere to accommodate two callers.
const (
	// AssignStatementTimeout bounds AssignSensors' full area x sensor ST_Covers join, which
	// runs on every poll cycle. Generous relative to 15s but still bounded, so
	// a runaway plan cannot hold a transaction open indefinitely across
	// successive cycles.
	AssignStatementTimeout = "60s"

	// OperatorStatementTimeout bounds the bulk deletes in
	// PurgeOutsideBoundary, and nothing else. At production volume (~900
	// sensors x 7 metrics x 30 days of 5-minute samples, spread over 30 daily
	// chunks) a bulk delete plausibly exceeds 15s; because it runs inside a
	// transaction, a timeout aborts the whole purge, so the documented cleanup
	// could never complete. An operator watching a command they typed is the
	// one caller for whom a long wait is preferable to a failure.
	//
	// It deliberately does not cover backfill's write. An earlier version of
	// this comment claimed it did, which was never true — backfill.WriteBuckets
	// batches on the pool rather than in a transaction, so it never reaches
	// SetLocalStatementTimeout. Nor does it need to: one archive day is at most
	// 24 buckets x 7 metrics of single-row upserts, and statement_timeout bounds
	// each statement individually, so the pool default is ample. Extending the
	// exception there would mean opening a transaction solely to relax a limit
	// nothing was hitting.
	OperatorStatementTimeout = "10min"
)

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
