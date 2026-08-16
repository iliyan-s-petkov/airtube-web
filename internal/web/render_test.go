package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/web"
)

// testConfig is the committed configuration, loaded once, so these tests
// exercise the frontend paint values and zoom thresholds the service actually
// ships with (airbg.yaml's frontend.* and series.*) rather than a second copy
// that can drift — see internal/server/server_test.go's testConfig for the
// same shape. BaseURL is overridden because every assertion in this file pins
// it to "https://airbg.org", not to whatever the committed file's
// listen.base_url happens to be.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	cfg.Listen.BaseURL = "https://airbg.org"
	return cfg
}

func renderer(t *testing.T, snap *snapshot.Snapshot) *web.Renderer {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	cfg := testConfig(t)
	h := snapshot.NewHolder(cfg.Series)
	if snap != nil {
		h.Store(snap)
	}
	rr, err := web.NewRenderer(cat, h, cfg)
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

// newTestRendererWithTiles builds a renderer whose basemap is the given
// public URL, or none when it is empty — the two states the templates must
// render differently.
func newTestRendererWithTiles(t *testing.T, publicURL string) *web.Renderer {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	cfg := testConfig(t)
	if publicURL != "" {
		cfg.Tiles = config.Tiles{
			Addr:      "127.0.0.1:8082",
			Dir:       "/var/lib/airbg/tiles",
			PublicURL: publicURL,
			Archive:   "bulgaria-20260815.pmtiles",
		}
	}
	h := snapshot.NewHolder(cfg.Series)
	h.Store(fixture(t))
	rr, err := web.NewRenderer(cat, h, cfg)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return rr
}

// TestNewRendererFailsClosedOnEmptyPeriodNames. cfg.Series.PeriodNames[0] is
// how NewRenderer picks the default period; config.Config.Validate rejects an
// empty series.periods list before LoadFile ever returns one, so this cannot
// happen via the normal startup path — but NewRenderer already returns an
// error, and indexing [0] unguarded would panic the process on a slice a
// different package's validation happens to keep non-empty today. Proves it
// fails closed instead.
func TestNewRendererFailsClosedOnEmptyPeriodNames(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	cfg := testConfig(t)
	cfg.Series.PeriodNames = nil
	h := snapshot.NewHolder(cfg.Series)

	_, err = web.NewRenderer(cat, h, cfg)
	if err == nil {
		t.Fatal("NewRenderer error = nil, want an error for empty PeriodNames")
	}
}

// TestThemeCSSLoadsBeforeAppCSS. app.css consumes theme.css's custom
// properties (var(--border) and friends), so the <link> order in the rendered
// <head> is load-bearing: a browser that requested app.css first would apply
// it before the custom properties it references exist, which is silently
// wrong rather than a visible error.
func TestThemeCSSLoadsBeforeAppCSS(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/").Body.String()

	theme := strings.Index(body, `<link rel="stylesheet" href="/static/theme.css">`)
	app := strings.Index(body, `<link rel="stylesheet" href="/static/app.css">`)
	if theme < 0 {
		t.Fatal("the page does not link /static/theme.css")
	}
	if app < 0 {
		t.Fatal("the page does not link /static/app.css")
	}
	if theme > app {
		t.Errorf("theme.css (offset %d) is linked after app.css (offset %d); app.css's var(--border) "+
			"and friends would resolve against nothing", theme, app)
	}
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

	// The exact markup of area.gohtml's {{else}} branch, tags included — not a
	// bare Contains("Недостатъчно данни"), which was satisfied by
	// data-t-no-data="Недостатъчно данни" (map.legend.no_data) on every area
	// page regardless of coverage, because that string is a strict PREFIX of
	// area.no_coverage ("Недостатъчно данни за този район"). The reviewer proved
	// the old form inert: replacing this whole <p> with MUTATED left the test
	// passing. Same fix as the sibling assertion in internal/server/e2e_test.go.
	//
	// Fix 5 has since removed the colliding data-t-no-data attribute, but the
	// exact-markup form stays: an assertion that only holds because a colliding
	// string happens to be absent today is the fragility being eliminated, not
	// a fix for it.
	const wantNoCoverageMarkup = `<p><strong>Недостатъчно данни за този район</strong></p>`
	if !strings.Contains(body, wantNoCoverageMarkup) {
		t.Errorf("the uncovered area page does not state that coverage is insufficient (want %q):\n%s",
			wantNoCoverageMarkup, body)
	}
	// Anchored to the chart island's value-label attribute rather than to the
	// bare unit string. chart.axis.value IS literally "µg/m³", so a bare
	// Contains would fire on any future edit that moves the chart div out of
	// {{if .Area.Covered}} even if no measurement were rendered — the assertion
	// is about "no measurement is shown here", and this is the markup that
	// would show one.
	if strings.Contains(body, `data-t-value="µg/m³"`) {
		t.Error("the uncovered area page renders the chart island's value label, implying a measurement it does not have")
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
// internal/i18n/i18n.go. Matched by SHAPE, not by a bare "!": every catalogue
// key is dotted (e.g. "nav.about", "error.unavailable.title"), so the pattern
// requires an interior dot. Without that, "!" + word + "!" would also match
// ordinary copy such as two adjacent exclamatory words with no space between
// them ("Wow!Great!") — legitimate Bulgarian or English text should never be
// able to fail this test.
var missingKeyMarker = regexp.MustCompile(`![A-Za-z0-9_]+\.[A-Za-z0-9_.]+!`)

// TestNoMissingCatalogueKeyMarkerAnywhere renders every page Routes serves —
// including all three error states, not just 404 — in both languages, and
// fails if any rendered page contains i18n's "!key!" fallback marker.
//
// T is fail-visible, not fail-loud: a typo'd or renamed key renders as
// "!nav.abuot!" on a live page while every other test — and the process
// itself — stays green. Nothing else in this tree pins "every key a template
// references exists in every language", and Tasks 17/18 and Phase 3 will all
// add or rename keys against these templates, so this is exactly the
// boundary where that contract needs to be enforced.
//
// The "unavailable" and "internal" error states matter as much as "not_found":
// they are what a visitor sees when the snapshot is not ready or a handler
// panics — the "we are having problems" page is the worst possible place for
// a missing-key marker, and the one nobody looks at during normal
// development. "unavailable" is reached through its real call path
// (handleIndex answers 503 before the first snapshot); "internal" has no
// caller inside this package yet, so it is reached the way a future caller
// (Task 17's panic recovery) will reach it: by calling the exported
// RenderError directly, without touching its signature.
func TestNoMissingCatalogueKeyMarkerAnywhere(t *testing.T) {
	rr := renderer(t, fixture(t))

	bodies := map[string]string{}

	for _, path := range []string{
		"/", "/en/", // index
		"/area/sofia", "/en/area/sofia", // area, covered branch
		"/area/vidin", "/en/area/vidin", // area, insufficient-coverage branch
		"/area/nope", "/en/area/nope", // error page, not_found branch
	} {
		bodies[path] = fetch(t, rr, path).Body.String()
	}

	unavailableRR := renderer(t, nil)
	bodies["/ (unavailable)"] = fetch(t, unavailableRR, "/").Body.String()
	bodies["/en/ (unavailable)"] = fetch(t, unavailableRR, "/en/").Body.String()

	for _, path := range []string{"/broken", "/en/broken"} {
		rec := httptest.NewRecorder()
		rr.RenderError(rec, httptest.NewRequest(http.MethodGet, path, nil), http.StatusInternalServerError, "internal")
		bodies[path+" (internal)"] = rec.Body.String()
	}

	for name, body := range bodies {
		if m := missingKeyMarker.FindString(body); m != "" {
			t.Errorf("%s renders a missing-catalogue-key marker %s — the template references a key that does not exist in this language's catalogue", name, m)
		}
	}
}

// TestRenderedErrorPagesAreNotCacheable pins that an error render's no-store
// survives, and that a successful render is still publicly cacheable.
//
// The bug this closes: render() set "public, max-age=150" unconditionally, AFTER
// RenderError had already set "no-store" one frame up, so the error page's
// no-store was silently overwritten and rendered 404s and 503s were
// edge-cacheable for 150 s. The 503 is the damaging one — a transient
// no-snapshot window (restart, failed poll) gets pinned at the edge and served
// to every visitor for 150 s after the process is healthy again, which converts
// a blip into an outage.
//
// The success case is asserted in the same test on purpose: "no page is
// cacheable" would satisfy the error half while throwing away the edge caching
// the pages rely on, and would look green.
func TestRenderedErrorPagesAreNotCacheable(t *testing.T) {
	// 404: a known-good snapshot, an unknown slug.
	// 503: no snapshot at all, so every page is unavailable.
	for _, tc := range []struct {
		name       string
		snap       *snapshot.Snapshot
		path       string
		wantStatus int
	}{
		{"rendered 404", fixture(t), "/area/no-such-place", http.StatusNotFound},
		{"rendered 404 (en)", fixture(t), "/en/area/no-such-place", http.StatusNotFound},
		{"rendered 503", nil, "/", http.StatusServiceUnavailable},
		{"rendered 503 (en)", nil, "/en/", http.StatusServiceUnavailable},
	} {
		rec := fetch(t, renderer(t, tc.snap), tc.path)
		if rec.Code != tc.wantStatus {
			t.Fatalf("%s: status = %d, want %d", tc.name, rec.Code, tc.wantStatus)
		}
		cc := rec.Header().Get("Cache-Control")
		if cc != "no-store" {
			t.Errorf("%s (%s): Cache-Control = %q, want %q — a cached error page pins a "+
				"transient failure at the edge and serves it to every visitor after the "+
				"origin has recovered", tc.name, tc.path, cc, "no-store")
		}
		// Belt and braces: whatever the exact string, it must not invite a
		// shared cache to store it.
		if strings.Contains(cc, "public") || strings.Contains(cc, "max-age=150") {
			t.Errorf("%s (%s): Cache-Control = %q marks an error response cacheable",
				tc.name, tc.path, cc)
		}
	}

	// And the other half: a successful page render keeps its public caching.
	for _, path := range []string{"/", "/en/", "/area/sofia"} {
		rec := fetch(t, renderer(t, fixture(t)), path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=150" {
			t.Errorf("%s: Cache-Control = %q, want %q — page renders are the aggregate, "+
				"non-enumerable surface and must stay edge-cacheable", path, cc,
				"public, max-age=150")
		}
	}
}

// TestMapIslandCarriesItsConfiguration. The island reads all of this from
// data-* attributes because the CSP forbids an inline script; a missing
// attribute is a map that silently falls back to a default nobody chose.
//
// Run over BOTH templates: area.gohtml carries an equivalent attribute block,
// and this branch's one real regression came from editing exactly that block
// (see TestAreaPageStatesInsufficientCoverage). Coverage of / alone would not
// have caught it.
func TestMapIslandCarriesItsConfiguration(t *testing.T) {
	rr := newTestRendererWithTiles(t, "https://tiles.example")

	for _, path := range []string{"/", "/area/sofia"} {
		t.Run(path, func(t *testing.T) {
			body := fetch(t, rr, path).Body.String()
			// Narrowed to the map island's own opening tag before asserting.
			// On /area/sofia the chart island carries a data-metric="P2" of its
			// own, so a whole-body Contains would be satisfied by the WRONG
			// element — verified by mutation: deleting data-metric from
			// area.gohtml's map div left the whole-body form green.
			tag := islandTag(t, body, "map")
			for _, want := range []string{
				`data-metric="P2"`,
				`data-basemap="https://tiles.example/style.json"`,
				`data-t-legend="`,
				`data-t-hint="`,
				`data-t-unavailable="`,
			} {
				if !strings.Contains(tag, want) {
					t.Errorf("%s: the map island's tag is missing %s: %s", path, want, tag)
				}
			}
		})
	}
}

// TestMapIslandRendersFrontendConfiguration pins the map island's paint values
// and zoom thresholds against the LITERAL values committed in airbg.yaml's
// frontend block, not against cfg.Frontend.* re-read from the same Renderer —
// re-deriving the expectation from the value under test would only prove Go
// can compare a value to itself. A mutation to airbg.yaml's
// frontend.no_data_colour must fail exactly this assertion.
func TestMapIslandRendersFrontendConfiguration(t *testing.T) {
	rr := renderer(t, fixture(t))

	for _, path := range []string{"/", "/area/sofia"} {
		t.Run(path, func(t *testing.T) {
			body := fetch(t, rr, path).Body.String()
			tag := islandTag(t, body, "map")
			for _, want := range []string{
				`data-no-data-colour="#9ca3af"`,
				`data-marker-stroke-colour="#ffffff"`,
				`data-empty-basemap-colour="#eef2f5"`,
				`data-zoom-city="9"`,
				`data-zoom-sensor="11"`,
			} {
				if !strings.Contains(tag, want) {
					t.Errorf("%s: the map island's tag is missing %s: %s", path, want, tag)
				}
			}
		})
	}
}

// TestHomeMapIslandRendersTheConfiguredDefaultView pins the home page's opening
// view against airbg.yaml's frontend.default_* literals.
//
// index.gohtml carried these three as attribute literals while area.gohtml
// templated its own — so the home page silently ignored configuration, and the
// same numbers lived a second time in api/locate.go. Only the home page is
// asserted here: /area/sofia's view comes from the area row, not from
// frontend.default_*.
func TestHomeMapIslandRendersTheConfiguredDefaultView(t *testing.T) {
	rr := renderer(t, fixture(t))
	tag := islandTag(t, fetch(t, rr, "/").Body.String(), "map")
	for _, want := range []string{
		`data-zoom="7"`,
		`data-lon="25.4858"`,
		`data-lat="42.7339"`,
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("the home map island's tag is missing %s: %s", want, tag)
		}
	}
}

// TestChartIslandRendersLineColourAndDefaults pins the chart island's stroke
// colour and its default metric/period against airbg.yaml's committed
// literals — frontend.chart_line_colour, series.default_metric, and the first
// entry of series.periods. Deleting the || 'P2' / || '24h' fallbacks in
// chart.js only matters if something on the server side actually asserts
// these attributes are rendered; this is that assertion.
func TestChartIslandRendersLineColourAndDefaults(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/area/sofia").Body.String()
	tag := islandTag(t, body, "chart")
	for _, want := range []string{
		`data-metric="P2"`,
		`data-period="24h"`,
		`data-line-colour="#2563eb"`,
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("the chart island's tag is missing %s: %s", want, tag)
		}
	}
}

// TestChartIslandCarriesItsUnavailableString. The chart island writes this
// string into its container when the series request fails; without the
// attribute the island reads "" and a failed fetch leaves an empty div, which
// on an air-quality page is indistinguishable from "nothing to report".
func TestChartIslandCarriesItsUnavailableString(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/area/sofia").Body.String()
	tag := islandTag(t, body, "chart")

	// The value, not just the attribute name: an empty attribute would satisfy
	// a name-only check and still leave the reader with a blank container.
	want := `data-t-unavailable="Данните за картата в момента не са достъпни"`
	if !strings.Contains(tag, want) {
		t.Errorf("the chart island's tag is missing %s: %s", want, tag)
	}
}

// islandTag returns the opening tag that carries data-island="<name>", so an
// attribute assertion cannot be satisfied by a different island's identically
// named attribute elsewhere on the page.
func islandTag(t *testing.T, body, name string) string {
	t.Helper()
	marker := `data-island="` + name + `"`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("no %s island on the page:\n%s", name, body)
	}
	// Back up to the element's own "<", forward to the tag's closing ">".
	open := strings.LastIndex(body[:start], "<")
	end := strings.Index(body[start:], ">")
	if open < 0 || end < 0 {
		t.Fatalf("could not delimit the %s island's tag:\n%s", name, body)
	}
	return body[open : start+end+1]
}

// TestBasemapURLCannotBreakOutOfTheAttribute. BasemapStyleURL is
// operator-supplied config (from tiles.public_url), not user input, but
// it still lands in an HTML attribute and html/template's attribute-context
// escaping is what stands between a hostile config value and an injected
// script — this pins that the template consumes the field as DATA in
// attribute context, not as a pre-built HTML fragment.
func TestBasemapURLCannotBreakOutOfTheAttribute(t *testing.T) {
	hostile := `javascript:alert(1)"><script>alert(1)</script>`
	rr := newTestRendererWithTiles(t, hostile)
	body := fetch(t, rr, "/").Body.String()

	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("the hostile basemap URL reached the page as an unescaped <script> tag")
	}
	if strings.Contains(body, `"><script>`) {
		t.Error("the hostile basemap URL broke out of the data-basemap attribute")
	}
	// The value must still be present, escaped, inside the attribute — proving
	// this is contextual escaping (which lets the value through, transformed)
	// rather than a filter that strips or blocks it outright.
	if !strings.Contains(body, `data-basemap="javascript:alert(1)&#34;&gt;`) &&
		!strings.Contains(body, `data-basemap="javascript:alert(1)&#34;&gt;&lt;script&gt;`) {
		t.Errorf("the basemap value does not appear escaped inside the attribute:\n%s", body)
	}
}

// TestBasemapStyleURLIsDerivedFromTilesPublicURL. The style URL is not a
// separate configuration value: writing it twice is how the CSP host and the
// URL the browser fetches drift apart.
func TestBasemapStyleURLIsDerivedFromTilesPublicURL(t *testing.T) {
	rr := newTestRendererWithTiles(t, "https://tiles.airbg.org")
	body := fetch(t, rr, "/").Body.String()
	if !strings.Contains(body, `data-basemap="https://tiles.airbg.org/style.json"`) {
		t.Errorf("rendered page does not carry the derived style URL:\n%s", body)
	}
}

// TestNoTilesRendersAnEmptyBasemapAttribute. Empty is the map island's signal
// to fall back to a flat colour — a fallback that was live, tested and
// unreachable for as long as validation rejected an empty style URL. Empty
// rather than absent: an absent attribute reads as undefined in the island,
// which is not the same branch.
func TestNoTilesRendersAnEmptyBasemapAttribute(t *testing.T) {
	rr := newTestRendererWithTiles(t, "")
	body := fetch(t, rr, "/").Body.String()
	if !strings.Contains(body, `data-basemap=""`) {
		t.Errorf("rendered page does not carry an empty basemap attribute:\n%s", body)
	}
}

// TestBasemapAttribution. ODbL requires the credit wherever the tiles are
// shown — and requires it nowhere when no tiles are shown, so the footer does
// not claim a basemap the page does not render.
func TestBasemapAttribution(t *testing.T) {
	with := fetch(t, newTestRendererWithTiles(t, "https://tiles.airbg.org"), "/").Body.String()
	if !strings.Contains(with, "Protomaps") {
		t.Errorf("page with a basemap does not credit Protomaps:\n%s", with)
	}
	without := fetch(t, newTestRendererWithTiles(t, ""), "/").Body.String()
	if strings.Contains(without, "Protomaps") {
		t.Error("page with no basemap credits Protomaps anyway")
	}
}
