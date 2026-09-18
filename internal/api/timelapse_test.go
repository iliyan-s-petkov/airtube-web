package api_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"airbg.org/internal/snapshot"
	"airbg.org/internal/upstream"
)

// timelapseFixture carries a body per published (metric, span, tier), each
// naming itself, so a test can tell "the handler resolved the request" apart
// from "the handler served whatever it had".
func timelapseFixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	live := fixture(t)
	live.Timelapse = map[string]snapshot.Body{}
	for _, m := range upstream.CanonicalMetrics() {
		for _, s := range snapshot.FrameSpecs {
			for _, res := range snapshot.TimelapseTiersKM {
				r := strconv.FormatFloat(res, 'g', -1, 64)
				b := `{"metric":"` + m + `","span":"` + s.Name + `","resolution_km":` + r + `}`
				live.Timelapse[m+"|"+s.Name+"|"+r] = snapshot.Body{
					JSON: []byte(b), Gzip: []byte("gzipped-" + b), ETag: `"` + m + s.Name + r + `"`,
				}
			}
		}
	}
	return live
}

// body is what the fixture above writes for one (metric, span, tier), so a test
// asserts on the exact triple the handler resolved.
func timelapseBodyFor(metric, span string, res float64) string {
	r := strconv.FormatFloat(res, 'g', -1, 64)
	return `{"metric":"` + metric + `","span":"` + span + `","resolution_km":` + r + `}`
}

func TestTimelapseServesTheRequestedMetricAndSpan(t *testing.T) {
	rec := serve(t, deps(t, timelapseFixture(t)), get("/api/v1/timelapse?metric=P1&span=7d", clientIPFor(200)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != timelapseBodyFor("P1", "7d", snapshot.HexResolutionKM) {
		t.Errorf("body = %q, want the P1/7d animation", got)
	}
}

// Every span the snapshot publishes must be accepted, or the player offers an
// option the server refuses.
func TestEveryPublishedSpanIsAccepted(t *testing.T) {
	for i, spec := range snapshot.FrameSpecs {
		rec := serve(t, deps(t, timelapseFixture(t)), get("/api/v1/timelapse?span="+spec.Name, clientIPFor(210+i)))
		if rec.Code != http.StatusOK {
			t.Errorf("span %q: status = %d, want 200", spec.Name, rec.Code)
		}
	}
}

// An arbitrary span is refused rather than snapped to the nearest published one.
// The bodies are precomputed per span, so accepting a duration a caller invented
// would mean either computing it per request or lying about what was served.
func TestUnknownSpanIsRejected(t *testing.T) {
	for i, p := range []string{
		"/api/v1/timelapse?span=1y",
		"/api/v1/timelapse?span=12h",
		"/api/v1/timelapse?span=3h",
		"/api/v1/timelapse?metric=P2&span=2026-09-01T00:00Z/2026-09-08T00:00Z",
	} {
		rec := serve(t, deps(t, timelapseFixture(t)), get(p, clientIPFor(220+i)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", p, rec.Code)
		}
	}
}

func TestUnknownMetricIsRejected(t *testing.T) {
	rec := serve(t, deps(t, timelapseFixture(t)), get("/api/v1/timelapse?metric=secrets", clientIPFor(230)))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// Naming neither is the map's own default view, so the island's first fetch has
// no parameters and every reader shares one cached copy of it.
func TestTimelapseDefaultsToTheSiteMetricAndTheShortestSpan(t *testing.T) {
	rec := serve(t, deps(t, timelapseFixture(t)), get("/api/v1/timelapse", clientIPFor(240)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"span":"`+snapshot.FrameSpecs[0].Name+`"`) {
		t.Errorf("body = %q, want the first published span", got)
	}
}

// A published pair with no body yet is a cycle that has not built one, not a
// bad request: the caller asked a question we publish.
func TestTimelapseWithNoBodyYetIsUnavailable(t *testing.T) {
	snap := fixture(t)
	snap.Timelapse = nil

	rec := serve(t, deps(t, snap), get("/api/v1/timelapse?metric=P2&span=24h", clientIPFor(250)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// The animation is the same bytes for everyone who asks the same question —
// that is what lets it be cached in front of the origin rather than rebuilt per
// reader. A private or no-store answer here would put every frame on the origin.
func TestTimelapseIsPubliclyCacheable(t *testing.T) {
	rec := serve(t, deps(t, timelapseFixture(t)), get("/api/v1/timelapse?metric=P2&span=24h", clientIPFor(260)))

	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "public") {
		t.Errorf("Cache-Control = %q, want a public directive", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag; a replayed animation should revalidate rather than re-download")
	}
}

// No box, ever. A timelapse of a viewport the caller supplies is the walk
// /overview refuses, one hour at a time.
func TestTimelapseIgnoresABoundingBox(t *testing.T) {
	rec := serve(t, deps(t, timelapseFixture(t)),
		get("/api/v1/timelapse?metric=P2&span=24h&bbox=23,42,24,43", clientIPFor(270)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != timelapseBodyFor("P2", "24h", snapshot.HexResolutionKM) {
		t.Errorf("body = %q, want the whole-country animation", got)
	}
}

// Every tier the snapshot publishes must be servable, and each must report the
// size it was actually cut at — the client builds its hex geometry from that
// number, so a body that misreports it draws the wrong shape.
func TestEveryPublishedTimelapseTierIsServed(t *testing.T) {
	for i, res := range snapshot.TimelapseTiersKM {
		r := strconv.FormatFloat(res, 'g', -1, 64)
		rec := serve(t, deps(t, timelapseFixture(t)),
			get("/api/v1/timelapse?metric=P2&span=24h&resolution_km="+r, clientIPFor(280+i)))
		if rec.Code != http.StatusOK {
			t.Fatalf("tier %v km: status = %d, want 200", res, rec.Code)
		}
		if got := rec.Body.String(); got != timelapseBodyFor("P2", "24h", res) {
			t.Errorf("tier %v km: body = %q", res, got)
		}
	}
}

// A caller that names no resolution gets the size the endpoint has always
// served, so a client written before tiers existed is unaffected.
func TestTimelapseWithNoResolutionServesTheDefaultTier(t *testing.T) {
	rec := serve(t, deps(t, timelapseFixture(t)), get("/api/v1/timelapse?metric=P2&span=24h", clientIPFor(290)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != timelapseBodyFor("P2", "24h", snapshot.HexResolutionKM) {
		t.Errorf("body = %q, want the %v km default", got, snapshot.HexResolutionKM)
	}
}

// Unlike the span, the resolution is snapped rather than refused: being handed
// a coarser animation than asked for still draws a map, and the body says which
// one it is. What it must never do is answer at a size we do not publish.
func TestTimelapseSnapsAnUnpublishedResolution(t *testing.T) {
	cases := map[string]float64{
		// Nonsense, in every shape a query string can carry it.
		"0": snapshot.HexResolutionKM, "-1": snapshot.HexResolutionKM,
		"abc": snapshot.HexResolutionKM, "": snapshot.HexResolutionKM,
		"NaN": snapshot.HexResolutionKM, "Inf": snapshot.HexResolutionKM,
		"1e9": 100, "12": snapshot.HexResolutionKM,
		// Tiers the live grid publishes and the replay deliberately does not:
		// the finest one published here is 2 km, not the grid's 0.25.
		"1": 2, "0.25": 2,
	}
	i := 0
	for param, want := range cases {
		i++
		rec := serve(t, deps(t, timelapseFixture(t)),
			get("/api/v1/timelapse?metric=P2&span=24h&resolution_km="+param, clientIPFor(300+i)))
		if rec.Code != http.StatusOK {
			t.Fatalf("resolution_km=%q: status = %d, want 200", param, rec.Code)
		}
		if got := rec.Body.String(); got != timelapseBodyFor("P2", "24h", want) {
			t.Errorf("resolution_km=%q: body = %q, want the %v km tier", param, got, want)
		}
	}
}
