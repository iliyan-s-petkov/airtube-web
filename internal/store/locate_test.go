package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/store"
)

// TestAreaAtPointPrefersTheSmallestArea: a point in Sofia falls inside the
// oblast, the city AND a district. Returning the oblast would drop a visitor
// into a country-scale view when a neighbourhood view was available.
func TestAreaAtPointPrefersTheSmallestArea(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	// Concentric buffers around the same point: 40 km, 15 km, 2 km.
	seedAreaWithRadius(t, ctx, pool, "sofia-oblast", "oblast", 23.3219, 42.6977, 40000)
	seedAreaWithRadius(t, ctx, pool, "sofia-city", "city", 23.3219, 42.6977, 15000)
	seedAreaWithRadius(t, ctx, pool, "lozenets", "neighbourhood", 23.3219, 42.6977, 2000)

	got, err := s.AreaAtPoint(ctx, 23.3219, 42.6977)
	if err != nil {
		t.Fatalf("AreaAtPoint: %v", err)
	}
	if got != "lozenets" {
		t.Errorf("AreaAtPoint = %q, want %q (the smallest containing area)", got, "lozenets")
	}
}

// TestAreaAtPointOutsideBulgariaReturnsEmpty: no area, no error. A visitor
// abroad is a normal case, not a failure, and must produce the default national
// view rather than a 500.
func TestAreaAtPointOutsideBulgariaReturnsEmpty(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedAreaWithRadius(t, ctx, pool, "sofia-oblast", "oblast", 23.3219, 42.6977, 40000)

	// Berlin.
	got, err := s.AreaAtPoint(ctx, 13.4050, 52.5200)
	if err != nil {
		t.Fatalf("AreaAtPoint: %v", err)
	}
	if got != "" {
		t.Errorf("AreaAtPoint = %q for a point outside every area, want \"\"", got)
	}
}

// TestAreaAtPointRejectsSwappedCoordinates. (23.3, 42.7) is Sofia;
// (42.7, 23.3) is in Somalia. PostGIS geography takes (lon, lat) — the reverse
// of the legacy PHP app's [lat, long] — so a swap here silently sends every
// Bulgarian visitor to the default view and nothing errors.
func TestAreaAtPointRejectsSwappedCoordinates(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool, testStoreConfig(), testSeriesTimeout)

	seedAreaWithRadius(t, ctx, pool, "sofia-oblast", "oblast", 23.3219, 42.6977, 40000)

	if got, err := s.AreaAtPoint(ctx, 23.3219, 42.6977); err != nil || got != "sofia-oblast" {
		t.Fatalf("AreaAtPoint(lon, lat) = %q, %v; want sofia-oblast", got, err)
	}
	if got, _ := s.AreaAtPoint(ctx, 42.6977, 23.3219); got == "sofia-oblast" {
		t.Error("AreaAtPoint(lat, lon) also matched Sofia; the argument order is not being honoured")
	}
}

func seedAreaWithRadius(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, kind string, lon, lat float64, metres int) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ($1, $2, $1, $1,
		         ST_Buffer(ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5)::geography)`,
		slug, kind, lon, lat, metres)
	if err != nil {
		t.Fatalf("seed area %s: %v", slug, err)
	}
}
