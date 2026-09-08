package api_test

import (
	"net/http"
	"strings"
	"testing"

	"airbg.org/internal/snapshot"
	"airbg.org/internal/upstream"
)

// timelapseFixture carries a body per published (metric, span), each naming
// itself, so a test can tell "the handler resolved the pair" apart from "the
// handler served whatever it had".
func timelapseFixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	live := fixture(t)
	live.Timelapse = map[string]snapshot.Body{}
	for _, m := range upstream.CanonicalMetrics() {
		for _, s := range snapshot.FrameSpecs {
			b := `{"metric":"` + m + `","span":"` + s.Name + `"}`
			live.Timelapse[m+"|"+s.Name] = snapshot.Body{
				JSON: []byte(b), Gzip: []byte("gzipped-" + b), ETag: `"` + m + s.Name + `"`,
			}
		}
	}
	return live
}

func TestTimelapseServesTheRequestedMetricAndSpan(t *testing.T) {
	rec := serve(t, deps(t, timelapseFixture(t)), get("/api/v1/timelapse?metric=P1&span=7d", clientIPFor(200)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != `{"metric":"P1","span":"7d"}` {
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
		"/api/v1/timelapse?span=48h",
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
	if got := rec.Body.String(); got != `{"metric":"P2","span":"24h"}` {
		t.Errorf("body = %q, want the whole-country animation", got)
	}
}
