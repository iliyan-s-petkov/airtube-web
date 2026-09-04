package db_test

import (
	"strings"
	"testing"

	"airbg.org/internal/db/migrations"
)

// TestMigration00006DownGuard executes the guard from 00006's Down section
// against a migrated database. Nothing in the binary calls goose down — a
// rollback is an operator running the goose CLI — so without this the guard
// would ship never having been parsed or run, and would first be exercised
// during an actual rollback, which is the worst moment to discover a syntax
// error in it.
//
// The SQL is read out of the embedded migration rather than duplicated here, so
// this cannot drift into asserting something the migration no longer says.
func TestMigration00006DownGuard(t *testing.T) {
	raw, err := migrations.FS.ReadFile("00006_area_kind_country.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	_, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("00006 has no Down section")
	}
	_, guard, ok := strings.Cut(down, "-- +goose StatementBegin")
	if !ok {
		t.Fatal("00006's Down section has no StatementBegin block; the country-row guard is gone")
	}
	guard, _, ok = strings.Cut(guard, "-- +goose StatementEnd")
	if !ok {
		t.Fatal("00006's Down guard block is unterminated")
	}

	ctx, pool := migrated(t)

	// With no country row the guard must be a no-op, so a rollback of a database
	// that never imported a national boundary still works.
	if _, err := pool.Exec(ctx, guard); err != nil {
		t.Fatalf("guard raised with no country row present: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, country_code, geom)
		 VALUES ('bulgaria', 'country', 'България', 'Bulgaria', 'BG',
		         ST_Multi(ST_SetSRID(ST_MakeEnvelope($1, $2, $3, $4), 4326))::geography)`,
		22.3, 41.2, 28.6, 44.2); err != nil {
		t.Fatalf("insert boundary: %v", err)
	}

	// With one present it must refuse, and the message must carry the remedy —
	// the whole point is replacing an opaque check-constraint violation with
	// something an operator can act on.
	_, err = pool.Exec(ctx, guard)
	if err == nil {
		t.Fatal("guard allowed a rollback with a country row present; the narrowed CHECK would then fail opaquely")
	}
	for _, want := range []string{"00006", "country", "DELETE FROM area"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("guard error %q does not mention %q", err, want)
		}
	}
}
