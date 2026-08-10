package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/web"
)

func renderer(t *testing.T, snap *snapshot.Snapshot) *web.Renderer {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	h := snapshot.NewHolder()
	if snap != nil {
		h.Store(snap)
	}
	rr, err := web.NewRenderer(cat, h, "https://airbg.org")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return rr
}

func fixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	return &snapshot.Snapshot{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		KnownSlugs: map[string]snapshot.AreaMeta{
			"sofia": {Slug: "sofia", Kind: "oblast", NameBG: "София", NameEN: "Sofia",
				CentroidLon: 23.32, CentroidLat: 42.69, DefaultZoom: 9,
				Covered: true, SensorCount: 12},
			"vidin": {Slug: "vidin", Kind: "oblast", NameBG: "Видин", NameEN: "Vidin",
				CentroidLon: 22.87, CentroidLat: 43.99, DefaultZoom: 9,
				Covered: false, SensorCount: 1},
		},
	}
}

func fetch(t *testing.T, rr *web.Renderer, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	rr.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexRendersInBulgarianByDefault(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `lang="bg"`) {
		t.Error(`the <html> element does not carry lang="bg"; screen readers will use the wrong pronunciation for the whole page`)
	}
	if !strings.Contains(body, "Мръсен въздух") {
		t.Error("the Bulgarian title is missing")
	}
}

func TestEnglishPrefixRendersInEnglish(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/en/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `lang="en"`) {
		t.Error(`the <html> element does not carry lang="en"`)
	}
	if !strings.Contains(body, "Dusty Air") {
		t.Error("the English title is missing")
	}
}

// TestAreaPageStatesInsufficientCoverage: an area below the 3-sensor threshold
// must SAY so. Rendering nothing where a number belongs reads as "clean air"
// to anyone scanning the page, which is the single most consequential way this
// site could mislead.
func TestAreaPageStatesInsufficientCoverage(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/area/vidin")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Недостатъчно данни") {
		t.Errorf("the uncovered area page does not state that coverage is insufficient:\n%s", body)
	}
	if strings.Contains(body, "µg/m³") {
		t.Error("the uncovered area page shows a unit, implying a measurement it does not have")
	}
}

func TestAreaPageCarriesTheDisclaimer(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/area/sofia")
	if !strings.Contains(rec.Body.String(), "индикативни") {
		t.Error("the indicative-data disclaimer is missing from a page that shows values")
	}
}

// TestUnknownAreaIs404WithAPage: an unknown slug must produce a rendered 404,
// not a blank body under a 404 status and not a 200.
func TestUnknownAreaIs404WithAPage(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/area/atlantis")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Страницата не е намерена") {
		t.Errorf("the 404 has no rendered body:\n%s", rec.Body.String())
	}
}

// TestSlugIsEscapedInOutput. html/template escapes by context, which is why the
// templates use it — but the escaping only holds if the value is interpolated as
// DATA. A slug pasted into an attribute without quotes, or into a <script>,
// escapes differently. Asserting on a hostile slug pins that.
func TestSlugIsEscapedInOutput(t *testing.T) {
	snap := fixture(t)
	hostile := `"><script>alert(1)</script>`
	snap.KnownSlugs[hostile] = snapshot.AreaMeta{
		Slug: hostile, Kind: "oblast", NameBG: hostile, NameEN: hostile,
		DefaultZoom: 9, Covered: true, SensorCount: 5,
	}

	rec := httptest.NewRecorder()
	// url.PathEscape, not a hand-rolled replace: the slug contains characters
	// that would otherwise terminate the request target and change what is
	// being tested.
	req := httptest.NewRequest(http.MethodGet, "/area/"+url.PathEscape(hostile), nil)
	renderer(t, snap).Routes().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Error("a script tag from a slug reached the output unescaped")
	}
}

func TestErrorPageRendersInTheRequestLanguage(t *testing.T) {
	rr := renderer(t, fixture(t))

	bg := fetch(t, rr, "/area/nope")
	if !strings.Contains(bg.Body.String(), "Страницата не е намерена") {
		t.Error("the Bulgarian 404 is not in Bulgarian")
	}

	en := fetch(t, rr, "/en/area/nope")
	if !strings.Contains(en.Body.String(), "Page not found") {
		t.Errorf("the English 404 is not in English:\n%s", en.Body.String())
	}
}

// TestPageIs503BeforeTheFirstSnapshot — same rule as the API: no data means say
// so, never render an empty country.
func TestPageIs503BeforeTheFirstSnapshot(t *testing.T) {
	rec := fetch(t, renderer(t, nil), "/")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestNoInlineScriptOrStyle: the CSP forbids 'unsafe-inline', so any inline
// <script> or style="" the templates emit would be blocked in a real browser
// and silently do nothing — a page that renders correctly in a test and is
// broken in production.
func TestNoInlineScriptOrStyle(t *testing.T) {
	for _, path := range []string{"/", "/en/", "/area/sofia", "/area/vidin"} {
		body := fetch(t, renderer(t, fixture(t)), path).Body.String()
		if strings.Contains(body, "<script>") {
			t.Errorf("%s contains an inline <script>, which the CSP blocks", path)
		}
		if strings.Contains(body, "style=\"") {
			t.Errorf("%s contains an inline style attribute, which the CSP blocks", path)
		}
	}
}

// TestAlternateLanguageLinks: hreflang pairs are how a search engine learns the
// two URLs are the same page in different languages, and how a reader switches
// without losing their place.
func TestAlternateLanguageLinks(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/area/sofia").Body.String()

	for _, want := range []string{
		`hreflang="bg"`,
		`hreflang="en"`,
		`https://airbg.org/area/sofia`,
		`https://airbg.org/en/area/sofia`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}

// missingKeyMarker matches i18n.Catalogue.T's fallback marker, "!key!" — see
// internal/i18n/i18n.go. Matched by shape (an exclamation mark, a run of
// catalogue-key characters, another exclamation mark), not by a bare "!",
// so ordinary copy or punctuation containing "!" cannot trip this test.
var missingKeyMarker = regexp.MustCompile(`![A-Za-z0-9_.]+!`)

// TestNoMissingCatalogueKeyMarkerAnywhere renders every page Routes serves, in
// both languages, and fails if any rendered page contains i18n's "!key!"
// fallback marker.
//
// T is fail-visible, not fail-loud: a typo'd or renamed key renders as
// "!nav.abuot!" on a live page while every other test — and the process
// itself — stays green. Nothing else in this tree pins "every key a template
// references exists in every language", and Tasks 17/18 and Phase 3 will all
// add or rename keys against these templates, so this is exactly the
// boundary where that contract needs to be enforced.
func TestNoMissingCatalogueKeyMarkerAnywhere(t *testing.T) {
	rr := renderer(t, fixture(t))

	pages := []string{
		"/", "/en/", // index
		"/area/sofia", "/en/area/sofia", // area, covered branch
		"/area/vidin", "/en/area/vidin", // area, insufficient-coverage branch
		"/area/nope", "/en/area/nope", // error page (404)
	}

	for _, path := range pages {
		body := fetch(t, rr, path).Body.String()
		if m := missingKeyMarker.FindString(body); m != "" {
			t.Errorf("%s renders a missing-catalogue-key marker %s — the template references a key that does not exist in this language's catalogue", path, m)
		}
	}
}
