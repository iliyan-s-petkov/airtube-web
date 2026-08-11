package web

// This file lives in package web, not web_test like the rest of
// internal/web/render_test.go, because it needs to reach the unexported
// Renderer.assets field: NewRenderer's signature does not take an Assets
// parameter (see the interface note in the Phase 3a task-1 brief — the value
// is loaded inside NewRenderer, not passed in), so there is no front-door way
// to substitute a fixture manifest into a *Renderer built through the public
// API. Go allows package web and package web_test test files to coexist in one
// directory; this is the injection seam, kept in its own file rather than
// converting render_test.go wholesale (which would lose its use of the
// unqualified web.Renderer type and force every other test in that file to
// drop its "web." qualifiers).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

// assetsFixtureManifest is the Step 6 fixture, restated here because a JSON
// literal typed twice in two files is what makes a manifest-shape drift show
// up as a diff instead of a silent divergence between "what parseManifest
// tests" and "what the page test renders".
const assetsFixtureManifest = `{
  "src/main.js": {
    "file": "assets/main-DEADBEEF.js",
    "name": "main",
    "src": "src/main.js",
    "isEntry": true,
    "css": ["assets/main-CAFEBABE.css"]
  }
}`

func mustParseFixtureManifest(t *testing.T) Assets {
	t.Helper()
	a, err := parseManifest([]byte(assetsFixtureManifest))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	return a
}

// rendererForAssetsTest builds a *Renderer the same way render_test.go's
// renderer() does, but from inside package web so the test below can reach
// rr.assets directly afterward.
func rendererForAssetsTest(t *testing.T) *Renderer {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	h := snapshot.NewHolder()
	h.Store(&snapshot.Snapshot{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		KnownSlugs: map[string]snapshot.AreaMeta{
			"sofia": {Slug: "sofia", Kind: "oblast", NameBG: "София", NameEN: "Sofia",
				CentroidLon: 23.32, CentroidLat: 42.69, DefaultZoom: 9,
				Covered: true, SensorCount: 12},
		},
	})
	rr, err := NewRenderer(cat, h, "https://airbg.org")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return rr
}

func renderIndex(t *testing.T, rr *Renderer) string {
	t.Helper()
	rec := httptest.NewRecorder()
	rr.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Body.String()
}

// TestPageEmitsTheHashedScriptWhenAManifestExists is the seam Vitest cannot
// see: whether the path Go resolved is the path the browser is told to fetch.
func TestPageEmitsTheHashedScriptWhenAManifestExists(t *testing.T) {
	rr := rendererForAssetsTest(t)
	// Substitute a fixture manifest rather than depending on a real build, so
	// the assertion holds on a machine with no Node.
	rr.assets = mustParseFixtureManifest(t)

	body := renderIndex(t, rr)
	want := `<script type="module" src="/static/build/assets/main-DEADBEEF.js"></script>`
	if !strings.Contains(body, want) {
		t.Errorf("page does not contain %s\n---\n%s", want, body)
	}
}

// TestPageEmitsNoScriptWithoutAManifest is the graceful-degradation gate, and
// per the spec it is the assertion most at risk of being inert — it passes both
// when the code is right and when LoadAssets is never called at all. The
// fallback-content assertion is what gives it teeth: a page with no script AND
// no content would be a blank page, which is the failure this is guarding
// against, not the success.
func TestPageEmitsNoScriptWithoutAManifest(t *testing.T) {
	rr := rendererForAssetsTest(t)
	// Routed through loadAssetsFrom on a directory holding only .keep, rather
	// than assigned Assets{} directly: assigning the zero value by hand tests
	// only that the template guards on emptiness, and stays green even if
	// LoadAssets's not-found path is mutated to fabricate an entry — exactly
	// the mutation the spec calls out. Going through the real function is what
	// gives this test teeth against that mutation.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".keep"), nil, 0o644); err != nil {
		t.Fatalf("write .keep: %v", err)
	}
	rr.assets, _ = loadAssetsFrom(os.DirFS(dir))

	body := renderIndex(t, rr)
	if strings.Contains(body, "<script") {
		t.Errorf("page contains a script tag with no manifest:\n%s", body)
	}
	if !strings.Contains(body, `data-island="map"`) {
		t.Error("page lost its island placeholder")
	}
	if !strings.Contains(body, "<ul") && !strings.Contains(body, "<li") {
		t.Errorf("page has no server-rendered area list to fall back to:\n%s", body)
	}
}
