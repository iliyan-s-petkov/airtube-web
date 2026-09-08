package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"airbg.org/internal/httpx"
	"airbg.org/internal/web"
)

// framed serves path through the same SecurityHeaders wrapper production puts
// in front of these routes. Testing the handler alone would prove nothing about
// framing: the headers that refuse it are set upstream of every page handler,
// and the embed route's whole job is to override them on its own response.
func framed(t *testing.T, rr *web.Renderer, path string) *httptest.ResponseRecorder {
	t.Helper()
	cfg := testConfig(t)
	h := httpx.SecurityHeaders(rr.Routes(), cfg.Listen.CSP, cfg.Listen.PermissionsPolicy)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The route exists, in every language, and says a partner may frame it.
func TestEmbedRouteIsFramable(t *testing.T) {
	rr := renderer(t, fixture(t))

	for _, path := range []string{"/embed", "/en/embed"} {
		rec := framed(t, rr, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors "+httpx.EmbedFrameAncestors) {
			t.Errorf("GET %s CSP = %q, want frame-ancestors %s", path, csp, httpx.EmbedFrameAncestors)
		}
		if strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("GET %s kept the refusal that makes framing impossible: %q", path, csp)
		}
		if xfo := rec.Header().Get("X-Frame-Options"); xfo != "" {
			t.Errorf("GET %s X-Frame-Options = %q, want it removed: it has no allowlist form, so DENY would override the CSP above", path, xfo)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("GET %s X-Robots-Tag = %q, want noindex: the page it mirrors is /", path, got)
		}
	}
}

// Widening one route must not widen the site. This is the test that fails if
// the override moves from the handler into the chain.
func TestEveryOtherPageStillRefusesFraming(t *testing.T) {
	rr := renderer(t, fixture(t))

	for _, path := range []string{"/", "/areas", "/area/sofia", "/about-the-data"} {
		rec := framed(t, rr, path)
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("GET %s CSP = %q, want frame-ancestors 'none'", path, csp)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("GET %s X-Frame-Options = %q, want DENY", path, got)
		}
	}
}

// The frame is the map. Everything that makes airbg.org a site rather than a
// map — masthead, nav, province table, footer — belongs to the host page.
func TestEmbedCarriesTheMapAndNotTheChrome(t *testing.T) {
	body := framed(t, renderer(t, fixture(t)), "/embed").Body.String()

	for _, want := range []string{
		`data-island="map"`,
		`data-island="switcher"`,
		`data-island="panel"`,
		`class="embed__switcher"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the embed is missing %s", want)
		}
	}
	for _, unwanted := range []string{
		`class="masthead"`,
		`class="langpick"`,
		`<table class="table"`,
		`data-island="finder"`,
		`<footer`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the embed carries the site's chrome: %s", unwanted)
		}
	}
}

// The credit is what the reader gets in exchange for the data, so it is in the
// served HTML rather than drawn by the bundle, it points at the site's own
// origin, and it leaves the frame when clicked.
func TestEmbedCreditsTheSite(t *testing.T) {
	body := framed(t, renderer(t, fixture(t)), "/embed").Body.String()

	credit := `<a class="embed__credit" href="https://airbg.org/" target="_blank" rel="noopener">`
	if !strings.Contains(body, credit) {
		t.Errorf("the embed does not credit its origin with %s", credit)
	}
}

// Two query parameters, both checked against what the server already knows. A
// host page can pick a metric and an area; it cannot name a bounding box, a
// coordinate or anything else that would turn the embed into a bulk read.
func TestEmbedHonoursOnlyKnownParameters(t *testing.T) {
	rr := renderer(t, fixture(t))

	body := framed(t, rr, "/embed?area=sofia&metric=humidity").Body.String()
	if !strings.Contains(body, `data-lat="42.69"`) || !strings.Contains(body, `data-lon="23.32"`) {
		t.Error("area=sofia did not move the map to Sofia's centroid")
	}
	if !strings.Contains(body, `data-metric="humidity"`) {
		t.Error("metric=humidity was not applied")
	}

	fallback := framed(t, rr, "/embed").Body.String()
	junk := framed(t, rr, "/embed?area=../../etc&metric=DROP%20TABLE").Body.String()
	if junk != fallback {
		t.Error("an unknown area or metric changed the page; both must be ignored entirely")
	}
}
