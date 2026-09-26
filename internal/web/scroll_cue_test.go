package web_test

import (
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

// The strip under the map on the home and area pages, in both languages, and
// the target it points at.
func TestScrollCueRendersUnderTheMap(t *testing.T) {
	rr := renderer(t, fixture(t))
	cases := []struct{ path, text string }{
		{"/", "Още под картата"},
		{"/en/", "More below the map"},
		{"/area/sofia", "Още под картата"},
		{"/en/area/sofia", "More below the map"},
	}
	for _, c := range cases {
		body := fetch(t, rr, c.path).Body.String()
		i := strings.Index(body, `<a class="scroll-cue" href="#below-map"`)
		if i < 0 {
			t.Errorf("%s: no scroll cue anchor", c.path)
			continue
		}
		shell := strings.LastIndex(body[:i], `<div class="map-shell">`)
		if shell < 0 {
			t.Errorf("%s: the scroll cue is not after the map shell", c.path)
		}
		end := strings.Index(body[i:], "</a>")
		if end < 0 || !strings.Contains(body[i:i+end], c.text) {
			t.Errorf("%s: the scroll cue does not say %q", c.path, c.text)
		}
		if j := strings.Index(body, `id="below-map"`); j < i {
			t.Errorf("%s: #below-map is missing or above the cue", c.path)
		}
	}
}

func TestScrollCueKeyResolvesInBothLanguages(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	for lang, want := range map[string]string{"bg": "Още под картата", "en": "More below the map"} {
		if !cat.Has(lang, "map.more_below") {
			t.Errorf("%s: map.more_below missing", lang)
			continue
		}
		if got := cat.T(lang, "map.more_below"); got != want {
			t.Errorf("%s: map.more_below = %q, want %q", lang, got, want)
		}
	}
}
