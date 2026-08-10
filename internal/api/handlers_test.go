package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	res, err := httpx.NewIPResolver(nil)
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	h := httpx.WithClientIP(api.NewRouter(d), res)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
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
		GeneratedAt        time.Time `json:"generated_at"`
		CoverageThreshold  int       `json:"coverage_threshold"`
		Attribution        string    `json:"attribution"`
		BoundaryAttribution string   `json:"boundary_attribution"`
		Metrics            []string  `json:"metrics"`
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
