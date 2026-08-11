package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/httpx"
	"airbg.org/internal/ratelimit"
)

// serve wraps the mux in WithClientIP so BucketKeyFrom resolves, which is how
// the enumeration counters key. Without it every test client would share the
// "unattributed" key and the breadth tests would interfere with each other.
func serve(t *testing.T, d api.Deps, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router(t, d).ServeHTTP(rec, req)
	return rec
}

// router builds the wrapped handler once, for tests that need per-router state
// (the series token bucket) to persist across several requests. serve builds a
// fresh one per call, which is what keeps unrelated tests from sharing buckets.
func router(t *testing.T, d api.Deps) http.Handler {
	t.Helper()
	res, err := httpx.NewIPResolver(nil)
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	return httpx.WithClientIP(api.NewRouter(d), res)
}

func get(path, clientIP string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = clientIP + ":41000"
	return r
}

func TestOverviewServesTheCountryTierByDefault(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/overview", "203.0.113.1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != `{"areas":[{"slug":"sofia"}]}` {
		t.Errorf("body = %q, want the country tier", got)
	}
}

func TestOverviewTierCityServesTheRegionalTier(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/overview?tier=city", "203.0.113.2"))

	if got := rec.Body.String(); got != `{"areas":[{"slug":"sofia-center"}]}` {
		t.Errorf("body = %q, want the city tier", got)
	}
}

// TestOverviewRejectsUnknownTier: an unrecognised tier must be a 400, not a
// silent fall back to the country tier. Silently substituting a different answer
// than the one asked for is how a frontend bug becomes invisible.
func TestOverviewRejectsUnknownTier(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/overview?tier=street", "203.0.113.3"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for tier=street", rec.Code)
	}
}

// TestOverviewTakesNoBoundingBox is the anti-extraction invariant from Phase 1
// §7.1, asserted rather than assumed. A bbox parameter would let a caller walk
// the country in a loop, which is the single change that would undo the entire
// tiering design — so it gets a test that fails the moment someone adds one.
func TestOverviewTakesNoBoundingBox(t *testing.T) {
	fix := fixture(t)
	plain := serve(t, deps(t, fix), get("/api/v1/overview", "203.0.113.4"))
	withBBox := serve(t, deps(t, fix), get("/api/v1/overview?bbox=22,41,29,45", "203.0.113.5"))

	if plain.Body.String() != withBBox.Body.String() {
		t.Error("a bbox parameter changed the response; /overview must never accept spatial filtering")
	}
}

func TestMetaReportsGeneratedAtAndCoverage(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/meta", "203.0.113.6"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		GeneratedAt         time.Time `json:"generated_at"`
		CoverageThreshold   int       `json:"coverage_threshold"`
		Attribution         string    `json:"attribution"`
		BoundaryAttribution string    `json:"boundary_attribution"`
		Metrics             []string  `json:"metrics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if got.GeneratedAt.IsZero() {
		t.Error("generated_at is zero; a client cannot tell how stale the data is")
	}
	if got.CoverageThreshold != 3 {
		t.Errorf("coverage_threshold = %d, want 3", got.CoverageThreshold)
	}
	// Both attributions are licence obligations, not decoration: sensor.community
	// data is ODbL and the OSM boundaries are ODbL. Omitting either is a licence
	// breach, so it is asserted rather than left to the template.
	if got.Attribution == "" {
		t.Error("attribution is empty")
	}
	if got.BoundaryAttribution == "" {
		t.Error("boundary_attribution is empty")
	}
	if len(got.Metrics) != 7 {
		t.Errorf("metrics has %d entries, want the 7 canonical metrics", len(got.Metrics))
	}
}

func TestAreasListsEveryKnownArea(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/areas", "203.0.113.7"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag on /areas")
	}
}

func TestScalesEndpointServesTheTables(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/scales", "203.0.113.8"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []api.Scale
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != len(api.Scales()) {
		t.Errorf("got %d scales, want %d", len(got), len(api.Scales()))
	}
	// Long-lived cache: the bands are legislation, not measurements.
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Error("no Cache-Control on /scales")
	}
}

func TestAreaSensorsServesTheColumnarBody(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/area/sofia/sensors", "203.0.113.9"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"sensors":{"id":[1]}}` {
		t.Errorf("body = %q", got)
	}
}

// TestAreaSensorsEnumerationTrips: after DistinctAreaLimit distinct slugs from
// one client, further distinct slugs must be refused. This is the breadth check
// wired into a real request path, not just the counter in isolation.
func TestAreaSensorsEnumerationTrips(t *testing.T) {
	fix := fixture(t)
	// Populate enough known areas that the limit is reachable.
	for i := 0; i < ratelimit.DistinctAreaLimit+2; i++ {
		slug := "area-" + string(rune('a'+i))
		fix.KnownSlugs[slug] = fix.KnownSlugs["sofia"]
		fix.AreaSensors[slug] = fix.AreaSensors["sofia"]
	}
	d := deps(t, fix)

	allowed, refused := 0, 0
	for i := 0; i < ratelimit.DistinctAreaLimit+2; i++ {
		slug := "area-" + string(rune('a'+i))
		rec := serve(t, d, get("/api/v1/area/"+slug+"/sensors", "203.0.113.10"))
		switch rec.Code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			refused++
			if rec.Header().Get("Retry-After") == "" {
				t.Error("the enumeration 429 has no Retry-After")
			}
		default:
			t.Fatalf("%s: status = %d", slug, rec.Code)
		}
	}
	if allowed != ratelimit.DistinctAreaLimit {
		t.Errorf("allowed %d distinct areas, want %d", allowed, ratelimit.DistinctAreaLimit)
	}
	if refused != 2 {
		t.Errorf("refused %d requests, want 2", refused)
	}
}

// TestEnumerationCheckRunsBeforeTheBodyIsWritten: the refusal must not send the
// data first. A check that answers 429 after already writing the payload has
// leaked exactly what it was there to withhold.
func TestEnumerationCheckRunsBeforeTheBodyIsWritten(t *testing.T) {
	fix := fixture(t)
	d := api.Deps{
		Snapshots: deps(t, fix).Snapshots,
		// A limit of zero trips on the very first request.
		Breadth: ratelimit.NewBreadth(0, 0, time.Hour),
		Store:   stubSource{slug: "sofia"},
		BaseURL: "https://airbg.org",
	}

	rec := serve(t, d, get("/api/v1/area/sofia/sensors", "203.0.113.11"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if body := rec.Body.String(); body == `{"sensors":{"id":[1]}}` {
		t.Fatal("the 429 response carried the sensor payload")
	}
}

// TestUnknownSlugDoesNotConsumeAreaBudget pins the ordering that
// handleAreaSensors relies on: the known-slug lookup runs BEFORE
// ObserveArea, so a 404 for garbage never touches the caller's enumeration
// budget. If that ordering were reversed, an attacker could exhaust a
// shared bucket cheaply with nonexistent slugs, and an innocent client with
// a stale bookmark would burn its own budget on a typo.
func TestUnknownSlugDoesNotConsumeAreaBudget(t *testing.T) {
	fix := fixture(t)
	d := deps(t, fix)

	// Fire more distinct UNKNOWN slugs than the area budget allows. If an
	// unknown slug consumed budget, this alone would exhaust it.
	for i := 0; i < ratelimit.DistinctAreaLimit+2; i++ {
		slug := "unknown-" + string(rune('a'+i))
		rec := serve(t, d, get("/api/v1/area/"+slug+"/sensors", "203.0.113.12"))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", slug, rec.Code)
		}
	}

	// A real, known slug from the SAME client must still succeed: the budget
	// must not have been touched by the unknown-slug requests above.
	rec := serve(t, d, get("/api/v1/area/sofia/sensors", "203.0.113.12"))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a known slug after only unknown-slug requests", rec.Code)
	}
}

// cacheVisibility returns the visibility token of a Cache-Control header
// ("public"/"private") and its max-age, failing the test if either is missing.
func cacheVisibility(t *testing.T, rec *httptest.ResponseRecorder, what string) (string, string) {
	t.Helper()
	cc := rec.Header().Get("Cache-Control")
	if cc == "" {
		t.Fatalf("%s: no Cache-Control header at all", what)
	}
	visibility, maxAge := "", ""
	for _, part := range strings.Split(cc, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "public" || part == "private":
			visibility = part
		case strings.HasPrefix(part, "max-age="):
			maxAge = strings.TrimPrefix(part, "max-age=")
		}
	}
	if visibility == "" {
		t.Fatalf("%s: Cache-Control = %q names neither public nor private; a shared cache "+
			"decides for itself what to do with it", what, cc)
	}
	if maxAge == "" {
		t.Fatalf("%s: Cache-Control = %q has no max-age", what, cc)
	}
	return visibility, maxAge
}

// TestOverviewIsPubliclyCacheableAndPerEntityIsNot pins the cache-visibility
// split, which is a security control rather than a performance setting.
//
// The breadth counter only sees requests that reach the origin. A per-entity
// response marked `public` may be served by a shared or edge cache without
// ObserveArea ever being called — so a scraper's distinct-slug count would stop
// growing for warmed slugs, and a client that had ALREADY tripped the limit
// could still read every warm area out of the edge. The aggregate responses have
// no per-entity key to walk, are requested identically by every visitor, and
// must stay `public` so edge caching keeps doing its denial-of-service work.
//
// Both halves are asserted together on purpose: pinning only "per-entity is
// private" would be satisfied by making everything private, which throws away
// the edge protection and would look like a passing test.
func TestOverviewIsPubliclyCacheableAndPerEntityIsNot(t *testing.T) {
	d := deps(t, fixture(t))

	for _, path := range []string{
		"/api/v1/overview",
		"/api/v1/areas",
		"/api/v1/meta",
		"/api/v1/scales",
	} {
		rec := serve(t, d, get(path, "203.0.113.90"))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if got, _ := cacheVisibility(t, rec, path); got != "public" {
			t.Errorf("%s is %s, want public: it is a single non-enumerable resource "+
				"every visitor requests, and edge-caching it is real DoS protection", path, got)
		}
	}

	// Everything keyed by a slug or a sensor ID is enumerable.
	for _, path := range []string{
		"/api/v1/area/sofia/sensors",
		"/api/v1/area/sofia/series?metric=P2&period=24h",
		"/api/v1/sensor/42/series?metric=P2&period=24h",
	} {
		rec := serve(t, d, get(path, "203.0.113.91"))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200 (body: %s)", path, rec.Code, rec.Body.String())
		}
		if got, _ := cacheVisibility(t, rec, path); got != "private" {
			t.Errorf("%s is %s, want private: a shared cache serving this by slug or "+
				"sensor id would hand out entities the breadth counter never saw requested, "+
				"including to a client already refused by the origin", path, got)
		}
	}
}
