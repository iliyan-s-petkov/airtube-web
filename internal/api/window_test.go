package api_test

import (
	"net/http"
	"strconv"
	"testing"

	"airbg.org/internal/snapshot"
)

// windowFixture is the plain fixture plus one alternate view, so a test can tell
// "the handler resolved the window" apart from "the handler ignored it and the
// bodies happened to match".
func windowFixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	body := func(s string) snapshot.Body {
		return snapshot.Body{JSON: []byte(s), Gzip: []byte("gzipped-" + s), ETag: `"` + s + `"`}
	}
	live := fixture(t)
	w := *live
	w.Windows = nil
	w.Overview = body(`{"areas":[{"slug":"sofia","window":"7d"}]}`)
	w.OverviewCity = body(`{"areas":[{"slug":"sofia-center","window":"7d"}]}`)
	w.Areas = body(`{"areas":[{"slug":"sofia","window":"7d"}]}`)
	w.AreaSensors = map[string]snapshot.Body{"sofia": body(`{"sensors":{"id":[1],"window":"7d"}}`)}
	live.Windows = map[string]*snapshot.Snapshot{"7d": &w}
	return live
}

func TestWindowedEndpointsServeTheRequestedWindow(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/api/v1/overview?window=7d", `{"areas":[{"slug":"sofia","window":"7d"}]}`},
		{"/api/v1/overview?tier=city&window=7d", `{"areas":[{"slug":"sofia-center","window":"7d"}]}`},
		{"/api/v1/areas?window=7d", `{"areas":[{"slug":"sofia","window":"7d"}]}`},
		{"/api/v1/area/sofia/sensors?window=7d", `{"sensors":{"id":[1],"window":"7d"}}`},
	}
	for i, c := range cases {
		rec := serve(t, deps(t, windowFixture(t)), get(c.path, clientIPFor(i)))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", c.path, rec.Code)
			continue
		}
		if got := rec.Body.String(); got != c.want {
			t.Errorf("%s: body = %q, want %q", c.path, got, c.want)
		}
	}
}

// Absent means live. The default view has to keep its parameterless URL, or
// every cached copy of the front page is invalidated by the change.
func TestNoWindowParameterServesTheLiveView(t *testing.T) {
	rec := serve(t, deps(t, windowFixture(t)), get("/api/v1/overview", "203.0.113.71"))

	if got := rec.Body.String(); got != `{"areas":[{"slug":"sofia"}]}` {
		t.Errorf("body = %q, want the live overview", got)
	}
}

// An unknown window is a 400 on every endpoint that takes one, for the reason
// an unknown tier is: a reader who asked for a week's average and was handed the
// last five minutes cannot see that they were.
func TestUnknownWindowIsRejected(t *testing.T) {
	paths := []string{
		"/api/v1/overview?window=1y",
		"/api/v1/areas?window=1y",
		"/api/v1/hexes?window=1y",
		"/api/v1/area/sofia/sensors?window=1y",
	}
	for i, p := range paths {
		rec := serve(t, deps(t, windowFixture(t)), get(p, clientIPFor(50+i)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", p, rec.Code)
		}
	}
}

// The 400 fires before the slug is looked up and before the breadth counter is
// touched, so a malformed window cannot be used to burn someone else's area
// budget from a shared address.
func TestUnknownWindowIsRejectedBeforeAnUnknownSlugIs(t *testing.T) {
	rec := serve(t, deps(t, windowFixture(t)), get("/api/v1/area/nope/sensors?window=1y", "203.0.113.79"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; the window is checked before the slug", rec.Code)
	}
}

// Every window the snapshot publishes has to be accepted by the handlers, or
// the client offers an option the server refuses.
func TestEveryPublishedWindowIsAccepted(t *testing.T) {
	for i, spec := range snapshot.WindowSpecs {
		rec := serve(t, deps(t, windowFixture(t)), get("/api/v1/overview?window="+spec.Name, clientIPFor(80+i)))
		if rec.Code != http.StatusOK {
			t.Errorf("window %q: status = %d, want 200", spec.Name, rec.Code)
		}
	}
}

// clientIPFor gives each request its own address. The area endpoint counts
// distinct slugs per client, so tests sharing one address would trip each
// other's breadth budget rather than testing what they claim to.
func clientIPFor(n int) string {
	return "203.0.113." + strconv.Itoa(100+n)
}
