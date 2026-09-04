package area_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The slug rule for the three OSM-derived boundary files, executable rather
// than described.
//
// The rule previously survived only in the committed output. That is a quiet
// hazard: the files were produced from Overpass by a process that no longer
// exists, so regenerating them would silently drop the `-oblast` suffix and
// every oblast slug would change. Slugs are URLs (`/oblast/{slug}`), so the
// failure surfaces as mass 404s on pages that used to work, with nothing in
// the diff explaining why. This test recomputes every slug from the feature's
// own `name_bg` and fails if the committed file disagrees, so a regeneration
// that drops the rule cannot be committed green.
//
// docs/boundary-regeneration.md carries the same rule in prose, with the
// Overpass queries and the PostGIS cleaning pipeline.

// streamlined is the Bulgarian Streamlined System (the official 2009
// transliteration), one entry per Cyrillic letter.
//
// The word-final "ия" → "ia" exception in the transliteration law is
// deliberately NOT implemented: the committed slugs do not apply it, which is
// why София is `sofiya` and not `sofia`. bulgaria.geojson's `bulgaria` slug
// looks like the exception but is not produced by this rule at all — that file
// comes from Natural Earth with a hand-set slug, and is excluded below.
var streamlined = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ж': "zh",
	'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n",
	'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f",
	'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sht", 'ъ': "a",
	'ь': "y", 'ю': "yu", 'я': "ya",
}

// slugify transliterates name_bg and hyphenates word breaks. Anything outside
// the table passes through unchanged, so an unmapped character shows up as a
// mismatch naming the exact name rather than being silently dropped.
func slugify(nameBG string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(nameBG) {
		switch {
		case r == ' ' || r == '-':
			b.WriteByte('-')
		default:
			if s, ok := streamlined[r]; ok {
				b.WriteString(s)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

type boundaryFile struct {
	Features []struct {
		Properties struct {
			Slug   string `json:"slug"`
			NameBG string `json:"name_bg"`
		} `json:"properties"`
	} `json:"features"`
}

func readBoundaries(t *testing.T, path string) boundaryFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var fc boundaryFile
	if err := json.Unmarshal(raw, &fc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return fc
}

// TestCommittedSlugsFollowTheDocumentedRule. Every oblast carries a uniform
// `-oblast` suffix; cities and Sofia districts carry the bare transliteration.
//
// The suffix is uniform rather than applied only where a collision exists
// today, because area.slug is the table's PRIMARY KEY across every kind and
// area.Import upserts on it: "Варна" the oblast and "Варна" the city both
// slugify to `varna`, so importing oblasti then cities once rewrote 26 of 28
// oblast rows into city rows. A collision-driven rule would break again the
// first time a new city matched an oblast that has no colliding city today.
func TestCommittedSlugsFollowTheDocumentedRule(t *testing.T) {
	for _, f := range []struct {
		path   string
		suffix string
		want   int
	}{
		{"../../data/boundaries/oblasti.geojson", "-oblast", 28},
		{"../../data/boundaries/cities.geojson", "", 27},
		{"../../data/boundaries/sofia-districts.geojson", "", 24},
	} {
		fc := readBoundaries(t, f.path)
		// The count is the positive control. Without it a file that parsed to
		// zero features would pass every assertion below by never entering the
		// loop -- exactly how a bare-Feature bulgaria.geojson once shipped.
		if got := len(fc.Features); got != f.want {
			t.Errorf("%s has %d features, want %d", f.path, got, f.want)
			continue
		}
		for _, feat := range fc.Features {
			p := feat.Properties
			if p.NameBG == "" {
				t.Errorf("%s: feature with slug %q has no name_bg, so its slug cannot be verified", f.path, p.Slug)
				continue
			}
			if want := slugify(p.NameBG) + f.suffix; p.Slug != want {
				t.Errorf("%s: name_bg %q has slug %q, want %q", f.path, p.NameBG, p.Slug, want)
			}
		}
	}
}

// TestSlugsAreUniqueAcrossEveryFile. area.slug is a single global PRIMARY KEY,
// so a collision between two files is a row silently overwriting another row of
// a different kind. TestAllFourFilesImportWithoutRowLoss catches that in the
// database; this catches it in the files, without needing Postgres, so it also
// runs on a machine with no Docker.
func TestSlugsAreUniqueAcrossEveryFile(t *testing.T) {
	// 6 countries (Bulgaria and the five neighbours in upstream.countries) +
	// 28 oblasti + 27 cities + 24 Sofia districts.
	const wantTotal = 85

	// Globbed rather than listed: the list is the thing that goes stale. A
	// sixth boundary file added without a matching edit here would otherwise
	// be committed with its slugs never checked against the other files'.
	paths, err := filepath.Glob("../../data/boundaries/*.geojson")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	sort.Strings(paths)

	seen := map[string]string{}
	total := 0
	for _, path := range paths {
		for _, feat := range readBoundaries(t, path).Features {
			total++
			slug := feat.Properties.Slug
			if first, dup := seen[slug]; dup {
				t.Errorf("slug %q appears in both %s and %s; the second import would overwrite the first row", slug, first, path)
				continue
			}
			seen[slug] = path
		}
	}
	if total != wantTotal {
		t.Errorf("read %d features across %d files, want %d", total, len(paths), wantTotal)
	}
	if len(seen) != total {
		t.Errorf("%d distinct slugs across %d features", len(seen), total)
	}
}
