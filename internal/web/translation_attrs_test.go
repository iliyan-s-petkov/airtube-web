package web

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"airbg.org/internal/snapshot"
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

// TestNoDataTAttributeHoldsALiteral rejects a data-t-* attribute whose value is
// plain text rather than a translation call. That renders one language on every
// page, and TestEveryTemplateKeyExistsInEveryCatalogue cannot see it: a scan for
// {{.T}} calls only judges the calls that are there.
//
// The key-existence half deliberately lives in that test and not here. Every
// {{.T "key"}} inside one of these attributes is already one of the calls it
// walks, so repeating the catalogue check would be a second scanner over the
// same templates, kept in sync by hand.
func TestNoDataTAttributeHoldsALiteral(t *testing.T) {
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

// mapIslandRe captures the map island's open tag, which is where the island's
// own data-t-* attributes are rendered.
var mapIslandRe = regexp.MustCompile(`(?s)<[^<>]*data-island="map".*?>`)

// TestEveryPageRendersTheSameMapIslandAttributes pins the three pages that mount
// the map to one attribute set. The island's JS reads the set through a single
// readConfig, and its test reads index.gohtml — so an attribute added to index
// alone would leave /areas/* and the embed mounting a map whose config is short
// a key, with every test green and nothing failing until someone looked at the
// page.
func TestEveryPageRendersTheSameMapIslandAttributes(t *testing.T) {
	want := map[string][]string{}
	for _, name := range []string{"index.gohtml", "area.gohtml", "embed.gohtml"} {
		src, err := fs.ReadFile(templateFS, "templates/"+name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		tag := mapIslandRe.FindString(string(src))
		if tag == "" {
			t.Fatalf("%s: no data-island=\"map\" tag; this test would pass vacuously", name)
		}
		var names []string
		for _, attr := range dataTAttrs(tag) {
			names = append(names, attr.name)
		}
		if len(names) == 0 {
			t.Fatalf("%s: map island carries no data-t-* attributes", name)
		}
		sort.Strings(names)
		want[name] = names
	}

	base := want["index.gohtml"]
	for _, name := range []string{"area.gohtml", "embed.gohtml"} {
		if strings.Join(want[name], ",") != strings.Join(base, ",") {
			t.Errorf("%s renders a different map island attribute set than index.gohtml:\n %s\n index.gohtml:\n %s",
				name, strings.Join(want[name], ","), strings.Join(base, ","))
		}
	}
}

// TestDataTWindowsMatchesWindowSpecs pins base.gohtml's data-t-windows list
// against snapshot.WindowSpecs, which mapwindow.js's WINDOW_CHOICES is now
// generated from (see contract.go). data-t-windows is hand-written HTML, not
// generated, and it is positional: live first, then WindowSpecs in order (see
// the comment above mapLayerLabels in base.gohtml). A published window added
// to WindowSpecs without a matching entry here leaves the selector one choice
// short, silently, since nothing else reads this attribute's length.
func TestDataTWindowsMatchesWindowSpecs(t *testing.T) {
	src, err := fs.ReadFile(templateFS, "templates/base.gohtml")
	if err != nil {
		t.Fatalf("reading base.gohtml: %v", err)
	}

	var windowsAttr string
	for _, attr := range dataTAttrs(string(src)) {
		if attr.name == "windows" {
			windowsAttr = attr.value
			break
		}
	}
	if windowsAttr == "" {
		t.Fatal("base.gohtml: no data-t-windows attribute found")
	}

	calls := tCallRe.FindAllString(windowsAttr, -1)
	want := len(snapshot.WindowSpecs) + 1 // +1 for the live choice, which has no WindowSpec of its own
	if len(calls) != want {
		t.Fatalf("data-t-windows has %d {{.T}} calls (%v), want %d (1 live + len(snapshot.WindowSpecs)=%d)",
			len(calls), calls, want, len(snapshot.WindowSpecs))
	}

	if calls[0] != `{{.T "map.window.live"}}` {
		t.Errorf("data-t-windows[0] = %s, want the live choice {{.T \"map.window.live\"}} first", calls[0])
	}
	for i, w := range snapshot.WindowSpecs {
		want := `{{.T "map.window.` + w.Name + `"}}`
		if got := calls[i+1]; got != want {
			t.Errorf("data-t-windows[%d] = %s, want %s (snapshot.WindowSpecs[%d].Name = %q)", i+1, got, want, i, w.Name)
		}
	}
}
