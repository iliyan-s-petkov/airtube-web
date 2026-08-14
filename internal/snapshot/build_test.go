package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
)

// testAssignTimeout mirrors airbg.yaml's database.statement_timeouts.assign
// default; see internal/area/area_test.go's testAssignTimeout for the same
// convention.
const testAssignTimeout = 60 * time.Second

// testConfig is the committed configuration, loaded once, so these tests
// exercise the values the service actually ships with (Series.DefaultMetric,
// Series.DefaultWindow, Store.CoverageThreshold, ...) rather than a second copy
// that can drift. Same shape as internal/api/router_test.go's testConfig — each
// package that needs one keeps its own copy rather than sharing a test helper
// package.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	return cfg
}

// testStore builds a *store.Store against pool using the committed config.
func testStore(t *testing.T, pool *pgxpool.Pool) *store.Store {
	t.Helper()
	cfg := testConfig(t)
	return store.New(pool, cfg.Store, cfg.Database.StatementTimeouts.Series)
}

// testHolder builds a *snapshot.Holder carrying the committed default series
// combination, for the h argument Build takes.
func testHolder(t *testing.T) *snapshot.Holder {
	t.Helper()
	return snapshot.NewHolder(testConfig(t).Series)
}

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool
}

// seed inserts one oblast with three usable sensors, which is exactly
// CoverageThreshold — enough to publish. It also inserts one city-kind area
// with no sensors, so the country tier (oblast only) and the combined Areas
// tier (oblast + city) are never byte-identical: without a distinct city
// entry here, TestBuildETagsDifferPerBody's "Overview vs Areas" comparison
// would coincidentally pass with equal content instead of proving anything
// about per-body hashing.
func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('sofia', 'oblast', 'София', 'Sofia',
		         ST_Buffer(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 20000)::geography)`,
		23.3219, 42.6977)
	if err != nil {
		t.Fatalf("seed area: %v", err)
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('plovdiv', 'city', 'Пловдив', 'Plovdiv',
		         ST_Buffer(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 5000)::geography)`,
		24.75, 42.15)
	if err != nil {
		t.Fatalf("seed city area: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	for i, v := range []float64{10, 20, 30} {
		id := int64(100 + i)
		_, err := pool.Exec(ctx,
			`INSERT INTO sensor (sensor_id, sensor_type, location)
			 VALUES ($1, 'SDS011', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)`,
			id, 23.3219+float64(i)*0.001, 42.6977)
		if err != nil {
			t.Fatalf("seed sensor: %v", err)
		}
		_, err = pool.Exec(ctx,
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, 'P2', $3, 'ok')`,
			now, id, v)
		if err != nil {
			t.Fatalf("seed reading: %v", err)
		}
	}
	if _, _, err := area.AssignSensors(ctx, pool, testAssignTimeout); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}
}

func TestBuildProducesValidJSONAndMatchingGzip(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The gzip body must decompress to exactly the JSON body. Two independently
	// produced representations of the same response is precisely the kind of
	// thing that drifts, and a client sent mismatched bytes under an ETag that
	// claims they are the same resource has no way to detect it.
	for name, b := range map[string]snapshot.Body{
		"overview":      snap.Overview,
		"overview_city": snap.OverviewCity,
		"areas":         snap.Areas,
	} {
		if !json.Valid(b.JSON) {
			t.Errorf("%s: JSON is not valid: %s", name, b.JSON)
		}
		zr, err := gzip.NewReader(bytes.NewReader(b.Gzip))
		if err != nil {
			t.Fatalf("%s: gzip reader: %v", name, err)
		}
		got, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("%s: gzip read: %v", name, err)
		}
		if !bytes.Equal(got, b.JSON) {
			t.Errorf("%s: gzip body does not decompress to the JSON body", name)
		}
		if b.ETag == "" {
			t.Errorf("%s: ETag is empty", name)
		}
	}
}

// TestBuildETagsDifferPerBody is the bug this design deliberately avoids:
// deriving ETags from GeneratedAt would give every tier built in the same cycle
// the same ETag, so a client that had fetched /overview would get a spurious 304
// for /overview?tier=city and render the wrong tier's data.
func TestBuildETagsDifferPerBody(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.Overview.ETag == snap.OverviewCity.ETag {
		t.Error("Overview and OverviewCity share an ETag; they must be hashed per body, not from GeneratedAt")
	}
	if snap.Overview.ETag == snap.Areas.ETag {
		t.Error("Overview and Areas share an ETag")
	}
}

// TestBuildETagIsStableForIdenticalData asserts the other half: identical data
// must yield an identical ETag, or every cycle invalidates every cache even when
// nothing changed, and the edge cache becomes useless.
func TestBuildETagIsStableForIdenticalData(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	s := testStore(t, pool)
	h := testHolder(t)
	a, err := snapshot.Build(ctx, s, h, time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build a: %v", err)
	}
	// A DIFFERENT build time, same data. GeneratedAt is excluded from the hash
	// for exactly this reason.
	b, err := snapshot.Build(ctx, s, h, time.Unix(1_800_000_300, 0).UTC())
	if err != nil {
		t.Fatalf("Build b: %v", err)
	}
	if a.Overview.ETag != b.Overview.ETag {
		t.Errorf("ETag changed between builds of identical data (%s vs %s); GeneratedAt must not be hashed", a.Overview.ETag, b.Overview.ETag)
	}
}

// seedAreasWithOneEmptyArea extends seed with one more oblast that has no
// sensors at all, for the tests that need to distinguish "no such area" (404)
// from "this area has nothing in it" (200, empty).
func seedAreasWithOneEmptyArea(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	seed(t, ctx, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('empty-oblast', 'oblast', 'Празна', 'Empty',
		         ST_Buffer(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 5000)::geography)`,
		26.0, 43.5)
	if err != nil {
		t.Fatalf("seed empty area: %v", err)
	}
}

// TestBuildIncludesEmptyAreasInAreaSensors: a known area with no sensors must
// have an AreaSensors entry, so the handler can distinguish 404 (no such area)
// from 200-with-nothing-in-it. Collapsing those two is how "this region has no
// data" gets served as "this region does not exist".
func TestBuildIncludesEmptyAreasInAreaSensors(t *testing.T) {
	ctx, pool := migrated(t)
	seedAreasWithOneEmptyArea(t, ctx, pool)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := snap.AreaSensors["empty-oblast"]; !ok {
		t.Error("AreaSensors has no entry for an existing but sensor-less area; the handler cannot then tell 404 from an empty 200")
	}
	if _, ok := snap.KnownSlugs["empty-oblast"]; !ok {
		t.Error("KnownSlugs is missing an existing area")
	}
	if meta := snap.KnownSlugs["empty-oblast"]; meta.Covered {
		t.Error("an area with no sensors reports Covered = true")
	}
}

// TestBuildIncludesAreaSeriesForEveryKnownSlug mirrors the AreaSensors rule: a
// missing key must mean "no such area" (404), never "this area happens to have
// no history" (which must be a 200 with empty arrays). An area page for a quiet
// area must render an empty chart, not a not-found.
func TestBuildIncludesAreaSeriesForEveryKnownSlug(t *testing.T) {
	ctx, pool := migrated(t)
	seedAreasWithOneEmptyArea(t, ctx, pool)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), time.Now().UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.KnownSlugs) == 0 {
		t.Fatal("no known slugs; the fixture is not being seen")
	}
	for slug := range snap.KnownSlugs {
		body, ok := snap.AreaSeries[slug]
		if !ok {
			t.Errorf("AreaSeries has no entry for known slug %q", slug)
			continue
		}
		if len(body.JSON) == 0 || len(body.Gzip) == 0 || body.ETag == "" {
			t.Errorf("AreaSeries[%q] is not fully prepared: json=%d gzip=%d etag=%q",
				slug, len(body.JSON), len(body.Gzip), body.ETag)
		}
	}
}

// TestAreaSeriesPayloadUsesEmptyArraysNotNull guards the exact failure a nil
// slice causes: `null` reaches uPlot, which throws instead of drawing an empty
// axis. writeSeries already allocates with make for the same reason; the
// snapshot path must not reintroduce the bug on the other side.
func TestAreaSeriesPayloadUsesEmptyArraysNotNull(t *testing.T) {
	ctx, pool := migrated(t)
	seedAreasWithOneEmptyArea(t, ctx, pool)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), time.Now().UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var found bool
	for slug, body := range snap.AreaSeries {
		if !strings.Contains(string(body.JSON), `"t":[]`) {
			continue
		}
		found = true
		if strings.Contains(string(body.JSON), "null") {
			t.Errorf("AreaSeries[%q] contains null: %s", slug, body.JSON)
		}
	}
	if !found {
		t.Fatal("no empty series in the snapshot; the fixture must include an area with no readings or this test proves nothing")
	}
}

// TestBuildAreaSeriesRespectsConfiguredWindow pins Holder.window (taken from
// config.Series.DefaultWindow by NewHolder) to the actual query bound Build
// uses: since := now.Add(-h.window). A reading placed just inside the
// committed 24h default window, but outside a plausible smaller one (e.g. a
// hardcoded 1h), must appear in the series Build produces — and disappear if
// the window used were narrower than configured. This is the direct proof
// that DefaultWindow (not just DefaultMetric) flows from config through the
// holder into the query, catching e.g. NewHolder hardcoding window instead of
// reading cfg.DefaultWindow.
func TestBuildAreaSeriesRespectsConfiguredWindow(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	now := time.Now().UTC()
	// 2h old: inside the committed 24h default window, outside any window an
	// hour or less. A distinctive value makes the assertion unambiguous.
	const marker = 777.0
	_, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, 100, 'P2', $2, 'ok')`,
		now.Add(-2*time.Hour), marker)
	if err != nil {
		t.Fatalf("seed old reading: %v", err)
	}

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), now)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	body, ok := snap.AreaSeries["sofia"]
	if !ok {
		t.Fatal("no AreaSeries entry for sofia")
	}
	if !strings.Contains(string(body.JSON), "777") {
		t.Errorf("AreaSeries[\"sofia\"] does not contain the 2h-old reading (value %v); "+
			"want it included under the committed 24h default window: %s", marker, body.JSON)
	}
}

// TestBuildSensorPayloadIsColumnar pins the wire format from Phase 1 §7.3.
// Phase 3's MapLibre layer consumes typed arrays; a silent switch to
// row-per-sensor would break it at runtime, not at compile time.
func TestBuildSensorPayloadIsColumnar(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	snap, err := snapshot.Build(ctx, testStore(t, pool), testHolder(t), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	body, ok := snap.AreaSensors["sofia"]
	if !ok {
		t.Fatal("no AreaSensors entry for sofia")
	}

	var got struct {
		GeneratedAt string `json:"generated_at"`
		Sensors     struct {
			ID      []int64    `json:"id"`
			Type    []string   `json:"type"`
			Lon     []float64  `json:"lon"`
			Lat     []float64  `json:"lat"`
			Quality []string   `json:"quality"`
			P2      []*float64 `json:"P2"`
		} `json:"sensors"`
	}
	if err := json.Unmarshal(body.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body.JSON)
	}
	if n := len(got.Sensors.ID); n != 3 {
		t.Fatalf("got %d sensor ids, want 3", n)
	}
	for name, n := range map[string]int{
		"type": len(got.Sensors.Type), "lon": len(got.Sensors.Lon),
		"lat": len(got.Sensors.Lat), "quality": len(got.Sensors.Quality),
		"P2": len(got.Sensors.P2),
	} {
		if n != 3 {
			t.Errorf("column %q has length %d, want 3 — every column must be the same length or the arrays do not line up", name, n)
		}
	}
	// Longitude near 23 and latitude near 42. Asserted separately, because
	// equal-length columns would happily hold swapped values.
	if got.Sensors.Lon[0] < 23 || got.Sensors.Lon[0] > 24 {
		t.Errorf("lon[0] = %v, want ~23.3 (a value near 42 means lon/lat are swapped)", got.Sensors.Lon[0])
	}
	if got.Sensors.Lat[0] < 42 || got.Sensors.Lat[0] > 43 {
		t.Errorf("lat[0] = %v, want ~42.7", got.Sensors.Lat[0])
	}
}
