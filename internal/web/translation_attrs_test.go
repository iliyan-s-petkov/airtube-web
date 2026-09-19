package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

// dataTAttrRe finds the START of a data-t-* attribute. The value is scanned by
// hand below rather than captured here: it holds quoted template actions
// ({{.T "key"}}), so no RE2 pattern can say where the closing quote is.
//
// The leading (^|[^\w-]) is the whole reason this is not \bdata-t-: a hyphen is
// a non-word character, so \b matches inside words like `custom-data-t-x`. The
// trailing [a-z0-9] before the '=' keeps it off a bare `data-t-`.
var dataTAttrRe = regexp.MustCompile(`(^|[^\w-])data-t-([a-z0-9-]*[a-z0-9])="`)

type dataTAttr struct {
	name  string
	value string
}

// dataTAttrs returns every data-t-* attribute in src with its raw value. The
// value ends at the first '"' that is not inside a {{...}} action — which is
// what lets data-t-legend="{{.T "map.legend.title"}}" parse at all.
func dataTAttrs(src string) []dataTAttr {
	var out []dataTAttr
	for _, m := range dataTAttrRe.FindAllStringSubmatchIndex(src, -1) {
		name := src[m[4]:m[5]]
		depth := 0
		start := m[1]
		i := start
		for ; i < len(src); i++ {
			switch {
			case strings.HasPrefix(src[i:], "{{"):
				depth++
				i++
			case strings.HasPrefix(src[i:], "}}"):
				depth--
				i++
			case src[i] == '"' && depth == 0:
				goto done
			}
		}
	done:
		out = append(out, dataTAttr{name: name, value: src[start:i]})
	}
	return out
}

// TestEveryDataTAttributeIsTranslated is the Go half of the data-t-* rule whose
// JS half lives in web/src/islands/__tests__/map.test.js. That one proves the
// template and the island's reader agree on the attribute NAMES; this one
// proves each attribute's VALUE is a translation that exists.
//
// It is narrower than TestEveryTemplateKeyExistsInEveryCatalogue in one way and
// wider in another: wider because it also rejects a data-t-* attribute holding
// a hardcoded literal, which renders one language on every page and which a
// scan for {{.T}} calls cannot see; narrower because it reports the attribute
// name, so a failure points at the markup rather than at the file.
func TestEveryDataTAttributeIsTranslated(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("loading catalogues: %v", err)
	}

	files, err := fs.Glob(templateFS, "templates/*.gohtml")
	if err != nil {
		t.Fatalf("globbing templates: %v", err)
	}

	total := 0
	for _, name := range files {
		src, err := fs.ReadFile(templateFS, name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, attr := range dataTAttrs(string(src)) {
			total++
			if !strings.Contains(attr.value, "{{") {
				t.Errorf("%s: data-t-%s=%q holds a literal, not a translation", name, attr.name, attr.value)
				continue
			}
			// Keys composed at render time — {{.T (printf "metric.%s" ...)}} —
			// are unreachable from a static scan and are left to the render
			// tests; a static key that does not exist is caught here.
			for _, m := range tCallRe.FindAllStringSubmatch(attr.value, -1) {
				for _, lang := range cat.Languages() {
					if !cat.Has(lang, m[1]) {
						t.Errorf("%s: data-t-%s references %q, missing from %s.json", name, attr.name, m[1], lang)
					}
				}
			}
		}
	}

	// Guards the scanner: a pattern that stopped matching would leave every
	// assertion above unreached and this test green. base.gohtml alone carries
	// over fifty.
	if total < 50 {
		t.Errorf("found only %d data-t-* attributes across %d templates; the scanner is probably wrong", total, len(files))
	}
}
