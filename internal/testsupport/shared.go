package testsupport

import (
	"context"
	"fmt"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// A package whose TestMain calls StartSharedPostgres gets one container for the
// whole binary; NewPostgresURL then hands each test its own freshly created
// database inside it. internal/store was starting one container per test — 66
// in a full run — and Docker intermittently failed to report the published
// 5432/tcp mapping under that churn, which took the whole package down with a
// "port 5432/tcp not found" at container start rather than at an assertion.
//
// Isolation is unchanged: a test still gets an empty database and runs
// db.Migrate itself, exactly as it did with a container of its own.
var (
	sharedBaseURL atomic.Pointer[string]
	sharedDBSeq   atomic.Int64
)

// StartSharedPostgres starts the container and returns the function that stops
// it. Call it from TestMain and defer nothing: os.Exit skips defers, so the
// stop must run before it.
func StartSharedPostgres() (stop func(), err error) {
	ctx := context.Background()
	container, err := runPostgresContainer(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("connection string: %w", err)
	}
	sharedBaseURL.Store(&raw)
	return func() {
		sharedBaseURL.Store(nil)
		_ = container.Terminate(context.Background())
	}, nil
}

func runPostgresContainer(ctx context.Context) (*tcpostgres.PostgresContainer, error) {
	c, err := tcpostgres.Run(ctx,
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
		return nil, fmt.Errorf("start postgres: %w", err)
	}
	return c, nil
}

// freshSharedDatabase creates a new empty database in the shared container and
// returns its connection string, or "" when no shared container is running.
//
// The database is not dropped afterwards: terminating the container disposes of
// every one of them, and dropping requires first terminating the pool's
// connections, which is a second way for a test to fail for reasons that have
// nothing to do with what it asserts.
func freshSharedDatabase(ctx context.Context) (string, error) {
	base := sharedBaseURL.Load()
	if base == nil {
		return "", nil
	}
	name := fmt.Sprintf("airbg_test_%d", sharedDBSeq.Add(1))

	conn, err := pgx.Connect(ctx, *base)
	if err != nil {
		return "", fmt.Errorf("connect to the shared container: %w", err)
	}
	defer conn.Close(ctx)

	// pgx cannot parameterise an identifier, and CREATE DATABASE cannot run
	// inside the implicit transaction a parameterised Exec would use. The name
	// is built from a counter above, never from input.
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		return "", fmt.Errorf("create database %s: %w", name, err)
	}

	u, err := url.Parse(*base)
	if err != nil {
		return "", fmt.Errorf("parse the shared connection string: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
