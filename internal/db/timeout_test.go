package db_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

// testOperatorTimeout mirrors airbg.yaml's database.statement_timeouts.operator.
const testOperatorTimeout = 10 * time.Minute

// TestOpenSetsPoolStatementTimeout pins the default that the scoped exceptions
// are exceptions to. If this ever became empty or 0, the two SetLocalStatement-
// Timeout callers would look correct while protecting nothing.
func TestOpenSetsPoolStatementTimeout(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)

	var got string
	if err := pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&got); err != nil {
		t.Fatalf("SHOW statement_timeout: %v", err)
	}
	if got != "15s" {
		t.Errorf("pool statement_timeout = %q, want %q", got, "15s")
	}
}

// TestPoolStatementTimeoutAbortsLongQuery proves the pool default is enforced
// and not merely reported by SHOW. pg_sleep(20) exceeds 15s, so this must fail.
// Without it, "the timeout is scoped" would be an untested claim in both
// directions.
func TestPoolStatementTimeoutAbortsLongQuery(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)

	_, err := pool.Exec(ctx, `SELECT pg_sleep(20)`)
	if err == nil {
		t.Fatal("a 20s query completed under a 15s statement_timeout — the pool-wide bound is not in force")
	}
	if !strings.Contains(err.Error(), "canceling statement due to statement timeout") {
		t.Errorf("error = %v, want a statement timeout cancellation", err)
	}
}

// TestSetLocalStatementTimeoutAllowsLongerWorkInTransaction is the positive half
// of finding 6: a legitimately long operation (a bulk delete over 30 hypertable
// chunks; an area x sensor join) must be able to complete. The same pg_sleep(20)
// that fails above succeeds here.
func TestSetLocalStatementTimeoutAllowsLongerWorkInTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback-only test

	if err := db.SetLocalStatementTimeout(ctx, tx, db.StatementTimeoutValue(testOperatorTimeout)); err != nil {
		t.Fatalf("SetLocalStatementTimeout: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_sleep(20)`); err != nil {
		t.Fatalf("a 20s statement failed inside a transaction raised to %s: %v", testOperatorTimeout, err)
	}
}

// TestSetLocalStatementTimeoutDoesNotLeakToTheConnection is the load-bearing
// half. Raising the timeout per-transaction is only safe if it is genuinely
// per-transaction: a pooled connection that returned with a 10-minute timeout
// still set would silently remove the DoS bound from whichever unrelated
// request picked it up next, which is materially worse than raising the default
// openly.
//
// The pool is deliberately sized to one connection, so the query after the
// transaction is guaranteed to be running on the same physical connection that
// the raised timeout was set on. Without that, this test could pass by getting a
// different connection and would prove nothing.
func TestSetLocalStatementTimeoutDoesNotLeakToTheConnection(t *testing.T) {
	ctx := context.Background()
	pool, err := db.Open(ctx, testDBConfig(testsupport.NewPostgresURL(t)+"&pool_max_conns=1", 0, 0))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := db.SetLocalStatementTimeout(ctx, tx, db.StatementTimeoutValue(testOperatorTimeout)); err != nil {
		t.Fatalf("SetLocalStatementTimeout: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&got); err != nil {
		t.Fatalf("SHOW statement_timeout: %v", err)
	}
	if got != "15s" {
		t.Errorf("statement_timeout after the transaction committed = %q, want %q — the raised value leaked back into the pool", got, "15s")
	}
}
