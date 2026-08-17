package web

import (
	"io/fs"
	"regexp"
	"testing"

	"airbg.org/internal/i18n"
)

// tCallRe matches a translation call in a template: {{.T "key"}} inside a
// normal context and {{$.T "key"}} inside a range block, which is how
// index.gohtml reaches the page's own T from within {{range .Areas}}.
var tCallRe = regexp.MustCompile(`\{\{\s*\$?\.T\s+"([^"]+)"\s*\}\}`)

// TestEveryTemplateKeyExistsInEveryCatalogue closes a gap nothing else covers:
// Catalogue.T falls back to the visible marker "!key!" for a key it does not
// hold, so deleting a key while a template still references it renders
// "!locate.outside!" to visitors and passes every other suite — the render
// tests assert on the values they expect to see, never on the absence of a
// marker.
//
// Checks every supported language, not just the default: T falls back through
// DefaultLang, so a key present in bg.json and missing from en.json renders
// Bulgarian on an English page rather than a marker. That is a subtler bug
// than the marker and would otherwise pass unnoticed.
//
// The reverse direction — a catalogue key no template or Go call site uses —
// is deliberately NOT checked here. render.go builds "metric."+m at runtime,
// so a static scan cannot tell a dead key from a dynamically composed one
// without an allowlist that would itself go stale.
func TestEveryTemplateKeyExistsInEveryCatalogue(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("loading catalogues: %v", err)
	}

	files, err := fs.Glob(templateFS, "templates/*.gohtml")
	if err != nil {
		t.Fatalf("globbing templates: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no templates matched; this test would pass vacuously")
	}

	total := 0
	for _, name := range files {
		src, err := fs.ReadFile(templateFS, name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, m := range tCallRe.FindAllSubmatch(src, -1) {
			key := string(m[1])
			total++
			for _, lang := range cat.Languages() {
				if !cat.Has(lang, key) {
					t.Errorf("%s references %q, missing from %s.json", name, key, lang)
				}
			}
		}
	}

	// Guards the regexp itself: a pattern that stopped matching would leave
	// every assertion above unreached and the test green. base.gohtml alone
	// carries well over ten calls, so this floor cannot be met by accident.
	if total < 10 {
		t.Errorf("found only %d translation calls across %d templates; the regexp is probably wrong", total, len(files))
	}
}
