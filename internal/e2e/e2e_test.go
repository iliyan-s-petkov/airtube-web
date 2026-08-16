//go:build e2e

// Package e2e boots the real stack and drives it with Playwright.
//
// A Go TEST rather than a Go command, and therefore Playwright is launched by
// Go rather than the other way round: testsupport.NewPostgres takes a *testing.T
// (container lifetime is tied to t.Cleanup), which a plain main() cannot supply.
// Inverting it would mean a second, divergent copy of the container setup.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
)

func TestBrowser(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The same airbg.yaml the binary ships with — an E2E against a bespoke
	// config proves the config nobody runs. AIRBG_DATABASE_URL is only
	// present to satisfy config.Validate; the pool this test actually talks
	// to is the container pool above, wired in directly via store.New.
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	st := store.New(pool, cfg.Store, cfg.Database.StatementTimeouts.Series)
	seedFixtures(t, st) // the fixtures every spec relies on; see seedFixtures

	public, _ := testsupport.StartServer(t, st, cfg)
	baseURL := "http://" + public

	cmd := exec.Command("npx", "playwright", "test")
	cmd.Dir = filepath.Join("..", "..", "web")
	cmd.Env = append(os.Environ(), "AIRBG_E2E_BASE_URL="+baseURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("playwright: %v", err)
	}
}

// seedFixtures inserts the data every Playwright spec in this phase asserts
// against. New specs add data here rather than seeding their own, so the
// fixture set stays a single source of truth.
//
// All SQL below is parameterised — the query text is a fixed literal with
// placeholders, values travel as bound parameters, and the polygon WKT built
// with fmt.Sprintf is itself bound as a single parameter — copied from
// internal/server/e2e_test.go's seedArea/seedReading, which explain why that
// is not the string-concatenated-SQL pattern the project forbids.
func seedFixtures(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()

	const (
		lon = 23.32
		lat = 42.69
		// Comfortably outside the ~0.02° spread the sensors below sit in, and
		// area.AssignSensors (run by testsupport.StartServer) assigns by real
		// ST_Covers containment, so the polygon must actually cover them.
		delta = 0.5
	)
	wkt := fmt.Sprintf(
		"MULTIPOLYGON(((%f %f, %f %f, %f %f, %f %f, %f %f)))",
		lon-delta, lat-delta,
		lon+delta, lat-delta,
		lon+delta, lat+delta,
		lon-delta, lat+delta,
		lon-delta, lat-delta,
	)

	// name_bg contains "Sofia" in Latin script deliberately: the JS-disabled
	// smoke spec hits the unprefixed /area/sofia route, which i18n.DefaultLang
	// ("bg") renders with name_bg. name_en is distinct so a later spec can
	// assert the /en/area/sofia route renders the English name specifically.
	_, err := st.Pool().Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ($1, $2, $3, $4, ST_SetSRID(ST_GeomFromText($5), 4326)::geography)`,
		"sofia", "oblast", "София (Sofia)", "Sofia", wkt)
	if err != nil {
		t.Fatalf("seed area: %v", err)
	}

	now := time.Now().UTC()

	// Two ordinary sensors: both metrics present, both readings ok.
	seedSensor(t, st, 101, 23.30, 42.68)
	seedReading(t, st, 101, "P1", 15, "ok", now)
	seedReading(t, st, 101, "P2", 10, "ok", now)

	seedSensor(t, st, 102, 23.34, 42.70)
	seedReading(t, st, 102, "P1", 20, "ok", now)
	seedReading(t, st, 102, "P2", 18, "ok", now)

	// P1 absent, P2 present: the reading table has no NOT NULL escape hatch
	// for "no value" (value is NOT NULL), so "null P1" here means no P1 row
	// exists for this sensor at all — the absent case the sensor panel must
	// render differently from a present-but-flagged value.
	seedSensor(t, st, 103, 23.31, 42.71)
	seedReading(t, st, 103, "P2", 22, "ok", now)

	// A non-ok quality flag on an otherwise ordinary sensor.
	seedSensor(t, st, 104, 23.33, 42.67)
	seedReading(t, st, 104, "P1", 12, "ok", now)
	seedReading(t, st, 104, "P2", 300, "stuck", now)

	// Enough history on sensor 101's P2 series that a 24h chart is non-empty:
	// one point every two hours across the window.
	for i := 1; i <= 12; i++ {
		seedReading(t, st, 101, "P2", 10+float64(i%5), "ok", now.Add(-time.Duration(i)*2*time.Hour))
	}
}

// seedSensor upserts one sensor at (lon, lat). Every value travels as a bound
// parameter.
func seedSensor(t *testing.T, st *store.Store, sensorID int64, lon, lat float64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	_, err := st.Pool().Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location, last_seen)
		 VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5)
		 ON CONFLICT (sensor_id) DO UPDATE
		   SET location = EXCLUDED.location, last_seen = EXCLUDED.last_seen, active = true`,
		sensorID, "test-sensor", lon, lat, now)
	if err != nil {
		t.Fatalf("seedSensor(%d): %v", sensorID, err)
	}
}

// seedReading inserts one reading for an already-seeded sensor. Every value
// travels as a bound parameter.
func seedReading(t *testing.T, st *store.Store, sensorID int64, metric string, value float64, quality string, at time.Time) {
	t.Helper()
	ctx := context.Background()

	_, err := st.Pool().Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (sensor_id, metric, time) DO UPDATE
		   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
		at, sensorID, metric, value, quality)
	if err != nil {
		t.Fatalf("seedReading(%d, %s): %v", sensorID, metric, err)
	}
}
