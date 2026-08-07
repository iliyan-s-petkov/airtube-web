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
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}
