package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"airbg.org/internal/api"
)

// An AGGREGATE hex request never fails on its parameters. The resolution is
// snapped onto a published tier and a garbled viewport is discarded, so every
// one of these is a 200 with a usable grid rather than a 400 and a blank map —
// the opposite of the /overview tier parameter, which is an explicit 400
// because a wrong tier means a frontend bug rather than a stale bookmark.
//
// The point tier is the single exception, and it is below.
func TestHexesToleratesAnyParameters(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	urls := []string{
		"/api/v1/hexes",
		"/api/v1/hexes?resolution_km=0.25",
		"/api/v1/hexes?resolution_km=99999",
		"/api/v1/hexes?resolution_km=-1",
		"/api/v1/hexes?resolution_km=abc",
		"/api/v1/hexes?resolution_km=",
		"/api/v1/hexes?bbox=22,41,24,43",
		"/api/v1/hexes?bbox=nonsense",
		"/api/v1/hexes?bbox=24,43,22,41",
		"/api/v1/hexes?resolution_km=1&bbox=22,41,24,43",
	}
	for _, u := range urls {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", u, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", u)
		}
	}
}

// The response must stay shared-cacheable: neither parameter carries anything
// about the caller, so a proxy holding one copy per URL is correct.
func TestHexesStaysPubliclyCacheable(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/hexes?resolution_km=0.5&bbox=22,41,24,43", nil))

	if cc := rec.Header().Get("Cache-Control"); cc == "" || !containsPublic(cc) {
		t.Errorf("Cache-Control = %q, want a public directive", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag on a hex response")
	}
}

func containsPublic(s string) bool {
	for i := 0; i+6 <= len(s); i++ {
		if s[i:i+6] == "public" {
			return true
		}
	}
	return false
}

// The point tier refuses an unbounded request. Falling through to the aggregate
// path — which is what a bare `res == 0` would do, since SnapResolutionKM maps
// nonsense onto the default tier — would serve every sensor in the country with
// its id to a caller who asked for a viewport.
func TestPointTierRequiresABoundingBox(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	for _, u := range []string{
		"/api/v1/hexes?resolution_km=0",
		"/api/v1/hexes?resolution_km=0&bbox=nonsense",
		"/api/v1/hexes?resolution_km=0&bbox=24,43,22,41", // inverted
		"/api/v1/hexes?resolution_km=0&bbox=22,41,24",    // three parts
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", u, rec.Code)
		}
	}
}

// With a box it answers, and it answers from the point tier.
//
// The resolution it echoes is what proves the routing: dropping the point
// branch sends res=0 down the aggregate path, where SnapResolutionKM maps it
// onto the 15 km default and the body says so. What the point payload CONTAINS
// is pinned in the snapshot package, which can reach the tier's sensors —
// this test owns the handler's contract, not the payload's.
func TestPointTierAnswersFromThePointTier(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/hexes?resolution_km=0&bbox=22,41,28,44", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"resolution_km":0`) {
		t.Errorf("point tier did not report resolution 0: %s", rec.Body.String())
	}
}
