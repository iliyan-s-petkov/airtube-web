//go:build integration

package server_test

// This is the only test in Phase 2 that runs the real chain against a real
// PostGIS database. Everything else stubs the store, which is the right trade
// for speed — but a stub cannot catch a swapped coordinate order in the SQL, a
// missing migration, or a quality filter that was written but never applied.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/area"
	"airbg.org/internal/db"
	"airbg.org/internal/httpx"
	"airbg.org/internal/i18n"
	"airbg.org/internal/server"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
)

// newIntegrationStore starts a throwaway PostGIS container, migrates it, and
// hands back a Store ready to seed. testsupport.NewPostgres already registers
// the container/pool teardown with t.Cleanup, so the returned cleanup func is
// a no-op kept only so callers can `defer cleanup()` the way the rest of this
// file's helpers are documented to be used.
func newIntegrationStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	ctx := context.Background()

	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store.New(pool), func() {}
}

// seedArea inserts one area whose polygon comfortably contains every sensor an
// e2e test seeds around (lon, lat). area.AssignSensors (invoked by runningWith)
// assigns sensors into it by real ST_Covers containment, so the polygon must
// actually cover them — not just exist.
//
// The WKT text is built with fmt.Sprintf, but it is passed to Postgres as a
// single bound parameter ($5), never spliced into the SQL string itself: the
// query text is a fixed literal with placeholders, so this is not the
// string-concatenated-SQL pattern the project forbids.
func seedArea(t *testing.T, st *store.Store, slug, kind string, lon, lat float64) {
	t.Helper()
	ctx := context.Background()

	const delta = 0.5 // degrees; well outside the ~0.06° spread test cases seed
	wkt := fmt.Sprintf(
		"MULTIPOLYGON(((%f %f, %f %f, %f %f, %f %f, %f %f)))",
		lon-delta, lat-delta,
		lon+delta, lat-delta,
		lon+delta, lat+delta,
		lon-delta, lat+delta,
		lon-delta, lat-delta,
	)

	_, err := st.Pool().Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ($1, $2, $3, $4, ST_SetSRID(ST_GeomFromText($5), 4326)::geography)`,
		slug, kind, slug, slug, wkt)
	if err != nil {
		t.Fatalf("seedArea(%q): %v", slug, err)
	}
}

// seedReading upserts one sensor at (lon, lat) and one reading for it. All
// values travel as bound parameters — no fmt.Sprintf into the SQL text.
func seedReading(t *testing.T, st *store.Store, sensorID int64, lon, lat float64, metric string, value float64, quality string) {
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
		t.Fatalf("seedReading(%d): upsert sensor: %v", sensorID, err)
	}

	_, err = st.Pool().Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (sensor_id, metric, time) DO UPDATE
		   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
		now, sensorID, metric, value, quality)
	if err != nil {
		t.Fatalf("seedReading(%d): insert reading: %v", sensorID, err)
	}
}

// runningWith assigns the seeded sensors to their areas, builds one real
// snapshot from the store (the same way the collector's Publisher does after
// every ingest cycle), and starts a full server.Server backed by that store
// and snapshot. It returns the public and private listener addresses, exactly
// like server_test.go's running(t), but against real data instead of a fixed
// fixture snapshot.
// configure, when supplied, is applied to the Options after the fields above
// are set and before server.New runs — the seam TestConfiguredBasemapReachesTheResponsePolicy
// uses to set Options.CSP without every other e2e test needing to know that
// field exists. Existing call sites pass none, which is why it is variadic
// rather than a required parameter.
func runningWith(t *testing.T, st *store.Store, configure ...func(*server.Options)) (public, private string) {
	t.Helper()
	ctx := context.Background()

	if _, _, err := area.AssignSensors(ctx, st.Pool()); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	holder := snapshot.NewHolder()
	pub := server.NewPublisher(st, holder, log)
	if err := pub.Publish(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	public, private = free(t), free(t)
	opts := server.Options{
		ListenAddr: public, MetricsAddr: private,
		Catalogue: cat, Snapshots: holder, Store: st, Publisher: pub,
		BaseURL: "http://" + public, Logger: log,
	}
	for _, fn := range configure {
		fn(&opts)
	}
	srv, err := server.New(opts)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not return within 10s of cancellation")
		}
	})

	waitReady(t, private)
	return public, private
}

// readAll drains and returns a response body as a string.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return string(b)
}

// TestEndToEndOverviewReflectsIngestedReadings walks the whole path: seed
// readings → build a snapshot → serve /api/v1/overview → assert the numbers.
func TestEndToEndOverviewReflectsIngestedReadings(t *testing.T) {
	st, cleanup := newIntegrationStore(t) // testcontainers + migrations + one area
	defer cleanup()

	seedArea(t, st, "sofia", "oblast", 23.32, 42.69)
	// Four sensors: three usable, one out of range. The published average must
	// be over the three, and coverage must be met on three, not four.
	seedReading(t, st, 1, 23.30, 42.68, "P2", 10, "ok")
	seedReading(t, st, 2, 23.32, 42.69, "P2", 20, "ok")
	seedReading(t, st, 3, 23.34, 42.70, "P2", 30, "no_neighbours")
	seedReading(t, st, 4, 23.36, 42.71, "P2", 9000, "out_of_range")

	public, _ := runningWith(t, st)

	resp := get(t, public, "/api/v1/overview")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	// snapshot.Build's areaPayloadEntry (internal/snapshot/build.go) serialises
	// the per-area numbers under the JSON key "values", not "metrics" — the
	// brief's decode target named it "metrics"; the wire format actually
	// emitted by the running server is the ground truth this test must pin, so
	// the tag here matches build.go's `json:"values"` rather than the brief's
	// wording.
	var body struct {
		Areas []struct {
			Slug        string             `json:"slug"`
			SensorCount int                `json:"sensor_count"`
			Metrics     map[string]float64 `json:"values"`
		} `json:"areas"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Areas) != 1 {
		t.Fatalf("areas = %d, want 1", len(body.Areas))
	}

	got := body.Areas[0]
	if got.SensorCount != 3 {
		t.Errorf("sensor_count = %d, want 3 — the out-of-range sensor was counted", got.SensorCount)
	}
	if got, want := got.Metrics["P2"], 20.0; got != want {
		t.Errorf("P2 = %v, want %v — 9000 leaked into the average", got, want)
	}
}

// TestEndToEndCoordinatesAreNotSwapped. PostGIS geography is (lon, lat) and the
// legacy app was (lat, long). Bulgaria's lon (22.3–28.7) and lat (41.2–44.3)
// ranges do not overlap, so a swap anywhere in the SQL, the scan, or the JSON
// is caught by asserting each against its own range.
func TestEndToEndCoordinatesAreNotSwapped(t *testing.T) {
	st, cleanup := newIntegrationStore(t)
	defer cleanup()

	seedArea(t, st, "sofia", "oblast", 23.32, 42.69)
	seedReading(t, st, 1, 23.30, 42.68, "P2", 10, "ok")
	seedReading(t, st, 2, 23.32, 42.69, "P2", 12, "ok")
	seedReading(t, st, 3, 23.34, 42.70, "P2", 14, "ok")

	public, _ := runningWith(t, st)

	resp := get(t, public, "/api/v1/area/sofia/sensors")
	// snapshot.Build's sensorPayload (internal/snapshot/build.go) nests the
	// columnar fields under a top-level "sensors" object rather than at the
	// document root — the brief's decode target put lon/lat at the top level;
	// the tags here match the actual response shape instead.
	var body struct {
		Sensors struct {
			Lon []float64 `json:"lon"`
			Lat []float64 `json:"lat"`
		} `json:"sensors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	lon, lat := body.Sensors.Lon, body.Sensors.Lat
	if len(lon) != 3 || len(lat) != 3 {
		t.Fatalf("lon=%d lat=%d, want 3 each", len(lon), len(lat))
	}
	for i := range lon {
		if lon[i] < 22.0 || lon[i] > 29.0 {
			t.Errorf("lon[%d] = %v, outside Bulgaria — coordinates are swapped", i, lon[i])
		}
		if lat[i] < 41.0 || lat[i] > 45.0 {
			t.Errorf("lat[%d] = %v, outside Bulgaria — coordinates are swapped", i, lat[i])
		}
	}
}

// TestEndToEndEnumerationTrips: walk more distinct areas than the limit from one
// address and confirm the wall comes up — the anti-extraction property this
// whole phase exists for, verified through the real middleware chain rather
// than against the counter in isolation.
func TestEndToEndEnumerationTrips(t *testing.T) {
	st, cleanup := newIntegrationStore(t)
	defer cleanup()

	for i := 0; i < 20; i++ {
		seedArea(t, st, fmt.Sprintf("area-%02d", i), "oblast", 23.0+float64(i)/100, 42.0+float64(i)/100)
	}

	public, _ := runningWith(t, st)

	var lastStatus int
	for i := 0; i < 20; i++ {
		lastStatus = get(t, public, fmt.Sprintf("/api/v1/area/area-%02d/sensors", i)).StatusCode
		if lastStatus == http.StatusTooManyRequests {
			return
		}
	}
	t.Errorf("walked 20 distinct areas from one address without tripping; last status %d", lastStatus)
}

// TestEndToEndPageRendersFromTheDatabase — one assertion that the HTML path and
// the JSON path see the same data.
func TestEndToEndPageRendersFromTheDatabase(t *testing.T) {
	st, cleanup := newIntegrationStore(t)
	defer cleanup()

	seedArea(t, st, "sofia", "oblast", 23.32, 42.69)
	seedReading(t, st, 1, 23.30, 42.68, "P2", 10, "ok")
	seedReading(t, st, 2, 23.32, 42.69, "P2", 12, "ok")
	seedReading(t, st, 3, 23.34, 42.70, "P2", 14, "ok")

	public, _ := runningWith(t, st)

	body := readAll(t, get(t, public, "/area/sofia"))
	// A bare Contains(body, "3") is satisfied by the page's own
	// data-lon="23.32"/data-lat="42.69" attributes regardless of the actual
	// sensor count (proven by mutation below), so match the sensor count in
	// the exact markup produced by area.gohtml's "{{.Area.SensorCount}}
	// {{.T "area.sensors"}}" line: "<p>3 сензора</p>" for the (unprefixed,
	// Bulgarian-locale) /area/sofia route. Any digit from a coordinate,
	// timestamp, or CSS class cannot satisfy this.
	const wantSensorCount = "<p>3 сензора</p>"
	if !strings.Contains(body, wantSensorCount) {
		t.Errorf("the area page does not show the sensor count in its own markup: want substring %q, got body:\n%s", wantSensorCount, body)
	}
	// Guard against a page that renders blank (or near-blank) but still
	// returns 200: the template always emits the breadcrumb link and the
	// map island regardless of coverage, so their absence means nothing
	// rendered at all.
	if !strings.Contains(body, `data-island="map"`) {
		t.Error("the area page did not render the map island — page may be blank")
	}
	if strings.Contains(body, "Недостатъчно данни") {
		t.Error("a covered area is shown as uncovered")
	}
}

// TestConfiguredBasemapReachesTheResponsePolicy is the wiring test: CSP() being
// correct is worthless if the value never reaches a response. Nothing else in
// the suite crosses config -> main -> server -> Chain -> SecurityHeaders.
func TestConfiguredBasemapReachesTheResponsePolicy(t *testing.T) {
	st, cleanup := newIntegrationStore(t)
	defer cleanup()

	seedArea(t, st, "sofia", "oblast", 23.32, 42.69)

	public, _ := runningWith(t, st, func(o *server.Options) {
		o.CSP = httpx.CSP("tiles.example")
	})

	resp := get(t, public, "/")
	got := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(got, "connect-src 'self' https://tiles.example") {
		t.Errorf("Content-Security-Policy = %q, missing the basemap host in connect-src", got)
	}
}
