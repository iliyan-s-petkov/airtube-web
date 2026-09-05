package web_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/web"
)

// This file is the end-to-end proof of the claim the i18n design makes: a
// language nobody compiled in is a FILE, not a release. Everything here goes
// through the real mux and the real templates, because that is where the two
// ways this can fail live — a language that loads but has no route, and a
// switcher that links to a language it cannot render.

// germanCatalogue renders a complete de.json: every key the embedded default
// holds, valued "de:<key>" so a fallback to Bulgarian is unmistakable in an
// assertion. Derived from Keys() so a key added next month is included without
// anyone remembering this file.
func germanCatalogue(t *testing.T, extra map[string]string) string {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	m := map[string]string{}
	for _, key := range cat.Keys() {
		m[key] = "de:" + key
	}
	for k, v := range extra {
		m[k] = v
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshalling de.json: %v", err)
	}
	return string(body)
}

// trilingualRenderer is the `renderer` helper with one difference: its
// catalogue comes from an override directory holding a German catalogue, the
// way a deployment with i18n.dir set would build it.
func trilingualRenderer(t *testing.T, snap *snapshot.Snapshot, extra map[string]string) *web.Renderer {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "de.json"), []byte(germanCatalogue(t, extra)), 0o600); err != nil {
		t.Fatalf("writing de.json: %v", err)
	}
	cat, err := i18n.LoadWithOverrides(dir)
	if err != nil {
		t.Fatalf("LoadWithOverrides: %v", err)
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

// The route exists. Registering prefixes from a hardcoded {"", "/en"} would
// 404 here while the switcher cheerfully linked to it.
func TestThirdLanguageIsRoutedAndRendered(t *testing.T) {
	rr := trilingualRenderer(t, fixture(t), nil)

	for _, path := range []string{"/de/", "/de/areas", "/de/area/sofia", "/de/about-the-data"} {
		rec := fetch(t, rr, path)
		if rec.Code != 200 {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `<html lang="de">`) {
			t.Errorf("GET %s did not render as German", path)
		}
		// Serving its own text, not falling through to Bulgarian.
		if !strings.Contains(body, "de:site.title") {
			t.Errorf("GET %s rendered no German copy", path)
		}
	}
}

// The map island navigates on its own (find-me, marker clicks) and cannot
// derive the language prefix in the browser — the language set is data, so no
// client-side expression can tell a language segment from a page segment. The
// server has to render it, per page, per language.
func TestMapIslandCarriesTheLanguagePrefix(t *testing.T) {
	rr := trilingualRenderer(t, fixture(t), nil)

	for path, want := range map[string]string{
		"/":              `data-lang-prefix=""`,
		"/area/sofia":    `data-lang-prefix=""`,
		"/de/":           `data-lang-prefix="/de"`,
		"/de/area/sofia": `data-lang-prefix="/de"`,
	} {
		if body := fetch(t, rr, path).Body.String(); !strings.Contains(body, want) {
			t.Errorf("GET %s does not render %s", path, want)
		}
	}
}

// The switcher is generated from the loaded set, so a third language appears in
// it without a template edit — and every link it offers must be a link that
// works, which is the pairing the previous test's routes half guarantees.
func TestSwitcherListsEveryLoadedLanguage(t *testing.T) {
	rr := trilingualRenderer(t, fixture(t), nil)
	body := fetch(t, rr, "/area/sofia").Body.String()

	for _, want := range []string{
		`href="https://airbg.org/en/area/sofia" hreflang="en"`,
		`href="https://airbg.org/de/area/sofia" hreflang="de"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Bulgarian page's switcher is missing %s", want)
		}
	}
	// Each language is named in its own words — a reader who cannot read the
	// current page is the one who needs this link.
	if !strings.Contains(body, ">English<") || !strings.Contains(body, ">de:lang.name<") {
		t.Error("the switcher does not label the links with each language's own name")
	}
	// The current language is present but not a link to the page you are on.
	if strings.Contains(body, `href="https://airbg.org/area/sofia" hreflang="bg"`) {
		t.Error("the switcher links to the page the reader is already on")
	}
	if !strings.Contains(body, `<span class="langpick__opt" lang="bg" aria-current="true">`) {
		t.Error("the current language is not marked in the switcher")
	}
	// The rel=alternate set must grow with it, or search engines never learn
	// the German pages exist.
	if !strings.Contains(body, `<link rel="alternate" hreflang="de" href="https://airbg.org/de/area/sofia">`) {
		t.Error("no rel=alternate for the third language")
	}
}

// Area names are the part that would otherwise demand a name_de column. Two
// rules, both exercised here: the Latin-script stored name is the day-one
// default for any non-Bulgarian language, and a catalogue key overrides it.
func TestThirdLanguageAreaNames(t *testing.T) {
	t.Run("falls back to the stored Latin name", func(t *testing.T) {
		rr := trilingualRenderer(t, fixture(t), nil)
		body := fetch(t, rr, "/de/area/sofia").Body.String()
		if !strings.Contains(body, "Sofia") {
			t.Error("the German page does not use the stored name_en")
		}
		// Cyrillic would mean it fell back to name_bg, which a German reader
		// cannot use.
		if strings.Contains(body, "София") {
			t.Error("the German page rendered the Bulgarian name")
		}
	})

	t.Run("a catalogue key wins", func(t *testing.T) {
		rr := trilingualRenderer(t, fixture(t), map[string]string{"area.name.sofia": "Sofia Stadt"})
		body := fetch(t, rr, "/de/area/sofia").Body.String()
		if !strings.Contains(body, "Sofia Stadt") {
			t.Error("the area.name.sofia override did not reach the page")
		}
	})

	// The languages that were already there must be unaffected by all of it.
	t.Run("bulgarian is untouched", func(t *testing.T) {
		rr := trilingualRenderer(t, fixture(t), map[string]string{"area.name.sofia": "Sofia Stadt"})
		body := fetch(t, rr, "/area/sofia").Body.String()
		if !strings.Contains(body, "София") {
			t.Error("the Bulgarian page lost its Bulgarian area name")
		}
		if strings.Contains(body, "Sofia Stadt") {
			t.Error("a German area name leaked onto the Bulgarian page")
		}
	})
}
