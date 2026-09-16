package web_test

import (
	"strings"
	"testing"
)

// The map's text alternative is what aria-describedby points at and the only
// route to the numbers for a reader who cannot use the map — so it stays in the
// document, and stays a link a crawler follows. It is no longer painted under
// the map: sighted readers have the Areas tab and the sentence only repeated it.
func TestTheMapTextAlternativeIsForScreenReadersOnly(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/").Body.String()

	i := strings.Index(body, `id="map-alt"`)
	if i < 0 {
		t.Fatal("the map has no text alternative to describe it")
	}
	para := body[strings.LastIndex(body[:i], "<p") : i+400]
	if !strings.Contains(para, "visually-hidden") {
		t.Errorf("the text alternative still paints under the map; it belongs to screen readers alone")
	}
	if !strings.Contains(para, `/areas"`) {
		t.Errorf("the text alternative no longer links to the areas page")
	}
}
