package api_test

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"airbg.org/internal/api"
	"airbg.org/internal/snapshot"
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
		"/api/v1/hexes?resolution_km=0&bbox=22,41,23,42", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"resolution_km":0`) {
		t.Errorf("point tier did not report resolution 0: %s", rec.Body.String())
	}
}

// Presence is not enough. A world-sized box passes the presence check and then
// hands back the whole station registry, ids attached, in one GET — the bulk
// download the viewport requirement exists to prevent.
func TestPointTierRefusesAnOversizedBox(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	for _, u := range []string{
		"/api/v1/hexes?resolution_km=0&bbox=-180,-90,180,90", // the whole world
		"/api/v1/hexes?resolution_km=0&bbox=22,41,25,43",     // 3.0 wide
		"/api/v1/hexes?resolution_km=0&bbox=22,41,24,44",     // 3.0 tall
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", u, rec.Code)
			continue
		}
		var got struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Errorf("GET %s: body is not JSON: %v", u, err)
			continue
		}
		if got.Error != "bbox_too_large" {
			t.Errorf("GET %s: error code = %q, want %q", u, got.Error, "bbox_too_large")
		}
	}
}

// The limit is inclusive, and a box just under it is ordinary. A boundary that
// rejects the exact maximum would make the frontend's own viewport unservable
// at the zoom it computes for it.
func TestPointTierAcceptsABoxUpToTheLimit(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	for _, u := range []string{
		"/api/v1/hexes?resolution_km=0&bbox=22,41,23.9,42.9", // 1.9 x 1.9
		"/api/v1/hexes?resolution_km=0&bbox=22,41,24,43",     // exactly 2.0 x 2.0
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (body: %s)", u, rec.Code, rec.Body.String())
		}
	}
}

// The extent guard judges the box the CALLER asked for, not the widened one the
// snapshot is queried with. This box is exactly 2.0 degrees per axis and its
// edges are off the quantum grid, so quantising it first would measure 2.25 and
// refuse a request that is inside the published limit.
func TestPointTierMeasuresTheBoxBeforeWideningIt(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	// 22.125 and 24.125 are exact in binary, so the extent is exactly 2.0 and
	// the assertion is about ordering rather than about float slack.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/hexes?resolution_km=0&bbox=22.125,41.125,24.125,43.125", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// clientQuantise mirrors hexes.js's quantise: snapped outward to the nearest
// multiple of q, on all four sides. Reimplemented here rather than exported
// from the JS, because this test is proving what the SERVER receives once the
// client has already snapped — the two languages cannot share a function, only
// the number they snap to (see snapshot.Contract).
func clientQuantise(w, s, e, n, q float64) (float64, float64, float64, float64) {
	return math.Floor(w/q) * q, math.Floor(s/q) * q, math.Ceil(e/q) * q, math.Ceil(n/q) * q
}

// TestPointTierGuardSurvivesTheClientQuantum pins Ruling 1 of the phase-4
// brief: moving the client's bbox quantum from 0.05 to 0.25 (to match
// BBoxQuantumDegrees and stop the drift, see contract.go) lets client-side
// snapping grow a viewport by up to 2*BBoxQuantumDegrees per axis instead of
// 2*0.05, before the box ever reaches the "bbox_too_large" guard — which
// measures the box AS SENT, deliberately before the server's own Quantise.
//
// 1.5 degrees is the true, unsnapped extent under test: the design's own
// pixel-geometry estimate puts the largest viewport the point tier's zoom
// range can produce (even on an 8K-wide screen) under 1 degree, so 1.5 is a
// deliberately generous upper bound on "the largest viewport the point tier
// can produce" — not a tight one — chosen so the test does not depend on
// TARGET_HEX_PX, which is presentational and JS-only and so is not in the
// contract.
//
// The box is positioned at the worst case the client can produce: the low edge
// just BELOW a grid line, so floor drops nearly a whole quantum, and the high
// edge just above one, so ceil adds nearly another. A box whose extent is an
// exact multiple of the quantum cannot do both — the two edges then sit at the
// same offset and the growth is exactly one quantum, not two — so the extent
// carries a small kick off the grid.
func TestPointTierGuardSurvivesTheClientQuantum(t *testing.T) {
	q := snapshot.NewContract().Hex.BBoxQuantumDeg
	const trueExtent = 1.5
	eps := q * 0.01

	w := q - eps
	e := w + trueExtent + 2*eps
	s := 2*q - eps
	n := s + trueExtent + 2*eps

	qw, qs, qe, qn := clientQuantise(w, s, e, n, q)

	// The positioning above is a claim about arithmetic, and a later edit could
	// quietly flatten it back to a quarter-quantum. Checked, not asserted in a
	// comment: each axis must grow by nearly the full 2*q.
	for _, grown := range []float64{qe - qw, qn - qs} {
		if want := trueExtent + 2*q; math.Abs(grown-want) > 4*eps {
			t.Fatalf("box grew to %v, want the worst case %v: this test is no longer adversarial", grown, want)
		}
	}

	mux := api.NewRouter(deps(t, fixture(t)))
	url := fmt.Sprintf("/api/v1/hexes?resolution_km=0&bbox=%v,%v,%v,%v", qw, qs, qe, qn)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))

	if rec.Code != http.StatusOK {
		t.Errorf("GET %s = %d, want 200 (body: %s); a %v-degree viewport, comfortably wider than "+
			"the point tier ever produces, must survive client-side quantisation to %v degrees",
			url, rec.Code, rec.Body.String(), trueExtent, q)
	}
}
