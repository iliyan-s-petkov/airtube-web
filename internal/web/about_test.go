package web_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestAboutPageRendersInEveryLanguage. The page exists to carry the data
// caveats to a reader, so the assertions are about the caveats being present
// in that reader's language — not about the page merely returning 200. The
// municipality split is checked specifically because it is the one that
// silently changes how a city's number should be read.
func TestAboutPageRendersInEveryLanguage(t *testing.T) {
	rr := renderer(t, fixture(t))

	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/about-the-data", []string{"За данните", "община", "sensor.community", "OpenStreetMap"}},
		{"/en/about-the-data", []string{"About the data", "municipality", "sensor.community", "OpenStreetMap"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := fetch(t, rr, tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
			// Catalogue.T renders "!key!" for a key it does not hold. The
			// key-parity test covers templates as a class; this catches the
			// same failure through the actual rendered response, which is
			// what a reader would see.
			if strings.Contains(body, "!about.") {
				t.Errorf("body contains an untranslated key marker:\n%s", body)
			}
		})
	}
}

// TestAboutPageRendersWithoutASnapshot. Every other page 503s when the
// snapshot holder is empty, and this one deliberately does not: the content is
// static prose that needs no snapshot, and the moment a reader goes looking for
// "can I trust this site" is exactly the moment the data is not loading. A
// future refactor that gives this handler the same snapshot guard as the others
// would look like consistency and would remove the page precisely when it is
// most wanted.
func TestAboutPageRendersWithoutASnapshot(t *testing.T) {
	rec := fetch(t, renderer(t, nil), "/about-the-data")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the about page must not depend on a snapshot", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "За данните") {
		t.Errorf("body is missing the page title:\n%s", rec.Body)
	}
}

// TestEveryPageFootersLinkToTheAboutPage, in the reader's own language. The
// link is the only route to this page — there is no nav entry — so a page
// whose footer lost it has silently unpublished the caveats. The
// language-prefix half matters just as much: a bare href="/about-the-data" in
// base.gohtml would render identically on both pages, pass a naive existence
// check, and drop every English reader back into Bulgarian.
func TestEveryPageFootersLinkToTheAboutPage(t *testing.T) {
	rr := renderer(t, fixture(t))

	for _, tc := range []struct{ page, wantHref string }{
		{"/", `href="/about-the-data"`},
		{"/areas", `href="/about-the-data"`},
		{"/area/sofia", `href="/about-the-data"`},
		{"/en/", `href="/en/about-the-data"`},
		{"/en/areas", `href="/en/about-the-data"`},
		{"/en/area/sofia", `href="/en/about-the-data"`},
		// The error page renders through the same base template; a reader who
		// mistyped a slug is still owed the link.
		{"/en/area/no-such-place", `href="/en/about-the-data"`},
	} {
		t.Run(tc.page, func(t *testing.T) {
			body := fetch(t, rr, tc.page).Body.String()
			if !strings.Contains(body, tc.wantHref) {
				t.Errorf("footer does not link to the about page with %s", tc.wantHref)
			}
		})
	}
}

// TestCityBoundaryNoteAppearsOnCityPagesOnly. The city page is where the
// municipality/city-proper split actually misleads someone — it is the number
// a reader compares against another city's. The note is deliberately not shown
// on oblast or district pages, where it would be simply false: those tiers have
// one consistent geometry.
func TestCityBoundaryNoteAppearsOnCityPagesOnly(t *testing.T) {
	snap := fixture(t)
	// "sofia" in the fixture is an oblast; give the snapshot a city too, so
	// both branches of the template are exercised against real routing rather
	// than against a hand-built PageData.
	meta := snap.KnownSlugs["sofia"]
	meta.Slug, meta.Kind, meta.NameBG, meta.NameEN = "plovdiv", "city", "Пловдив", "Plovdiv"
	snap.KnownSlugs["plovdiv"] = meta
	rr := renderer(t, snap)

	const bgNote = "цялата община"
	if body := fetch(t, rr, "/area/plovdiv").Body.String(); !strings.Contains(body, bgNote) {
		t.Errorf("city page does not carry the boundary note")
	}
	if body := fetch(t, rr, "/area/sofia").Body.String(); strings.Contains(body, bgNote) {
		t.Errorf("oblast page carries the city boundary note, which is not true of oblast geometry")
	}
	if body := fetch(t, rr, "/en/area/plovdiv").Body.String(); !strings.Contains(body, "whole municipality") {
		t.Errorf("English city page does not carry the boundary note")
	}
}

// municipalityRe pulls the "Whole municipality" row out of the limitations doc.
// Permissive on the count in the header cell so that fixing the doc's number
// alone cannot make this stop matching silently.
var municipalityRe = regexp.MustCompile(`\*\*Whole municipality\*\*[^|]*\|([^|]*)\|`)

// TestAboutPageListsTheSameCitiesAsTheLimitationsDoc. The 14 municipality-
// boundary cities are stated in three places — data/boundaries/README.md,
// docs/known-limitations.md, and now user-facing copy in two catalogues. Copy
// is where drift is least likely to be noticed: nothing about a wrong city name
// in a translation string looks wrong, and the page reads perfectly either way.
//
// Compared as a set, not a sequence: the doc's order and the sentence's order
// are both arbitrary and neither should force a change in the other. The
// Bulgarian list is checked by count only — the names are Cyrillic and have no
// counterpart in the doc, so a name-level check there would need a
// transliteration table this test has no business owning.
func TestAboutPageListsTheSameCitiesAsTheLimitationsDoc(t *testing.T) {
	const wantCities = 14

	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "known-limitations.md"))
	if err != nil {
		t.Fatalf("reading known-limitations.md: %v", err)
	}
	m := municipalityRe.FindSubmatch(raw)
	if m == nil {
		t.Fatal("no `**Whole municipality**` row found in docs/known-limitations.md — " +
			"the table changed shape and this test is comparing nothing")
	}
	fromDoc := splitCityList(string(m[1]))
	if len(fromDoc) != wantCities {
		t.Fatalf("doc lists %d municipality cities, want %d", len(fromDoc), wantCities)
	}

	rr := renderer(t, fixture(t))

	fromPage := splitCityList(cityListSentence(t, fetch(t, rr, "/en/about-the-data").Body.String(), "Whole municipality:"))
	if len(fromPage) != wantCities {
		t.Fatalf("the English page lists %d municipality cities, want %d: %v", len(fromPage), wantCities, fromPage)
	}
	if strings.Join(fromDoc, "|") != strings.Join(fromPage, "|") {
		t.Errorf("the English page and docs/known-limitations.md disagree about which cities use a municipality boundary\npage: %v\ndoc:  %v", fromPage, fromDoc)
	}

	bg := splitCityList(cityListSentence(t, fetch(t, rr, "/about-the-data").Body.String(), "С граница на общината:"))
	if len(bg) != wantCities {
		t.Errorf("the Bulgarian page lists %d municipality cities, want %d: %v", len(bg), wantCities, bg)
	}
}

// cityListSentence returns the comma-separated remainder of the rendered
// sentence introduced by prefix.
func cityListSentence(t *testing.T, body, prefix string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, prefix)
	if !ok {
		t.Fatalf("rendered page has no sentence beginning %q", prefix)
	}
	sentence, _, ok := strings.Cut(rest, "<")
	if !ok {
		t.Fatalf("no element end after %q; the template changed shape", prefix)
	}
	return sentence
}

// splitCityList normalises either source into a sorted set of city names.
func splitCityList(s string) []string {
	out := make([]string, 0, 16)
	for _, name := range strings.Split(strings.TrimSuffix(strings.TrimSpace(s), "."), ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
