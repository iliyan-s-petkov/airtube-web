package db_test

import (
	"context"
	"testing"

	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	// Assert the migration actually did something: both extensions that
	// 00001_extensions.sql creates must be present. A no-op Migrate would
	// leave pg_extension without them and fail here.
	for _, ext := range []string{"postgis", "timescaledb"} {
		var exists bool
		if err := pool.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)", ext,
		).Scan(&exists); err != nil {
			t.Fatalf("query pg_extension for %s: %v", ext, err)
		}
		if !exists {
			t.Fatalf("extension %s was not created by Migrate", ext)
		}
	}

	var versionAfterFirst int64
	if err := pool.QueryRow(ctx,
		"SELECT version_id FROM goose_db_version ORDER BY id DESC LIMIT 1",
	).Scan(&versionAfterFirst); err != nil {
		t.Fatalf("query goose_db_version after first Migrate: %v", err)
	}

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	// Idempotency means the applied-version state is unchanged, not merely
	// that the call returned nil: a Migrate that reapplied migration 1 (or
	// inserted a spurious version row) would still return nil but would
	// change this value.
	var versionAfterSecond int64
	if err := pool.QueryRow(ctx,
		"SELECT version_id FROM goose_db_version ORDER BY id DESC LIMIT 1",
	).Scan(&versionAfterSecond); err != nil {
		t.Fatalf("query goose_db_version after second Migrate: %v", err)
	}

	if versionAfterSecond != versionAfterFirst {
		t.Fatalf("second Migrate changed applied version: %d -> %d", versionAfterFirst, versionAfterSecond)
	}
}
