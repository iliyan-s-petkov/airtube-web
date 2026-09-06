package web_test

import (
	"strings"
	"testing"
)

// The search box is a control with no server-side meaning — filtering 28 rows
// the server already ranked needs no round trip — so nothing of it is rendered.
// What the server owes it is the translated vocabulary.
func TestTableSearchCarriesItsVocabulary(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/").Body.String()

	for _, want := range []string{
		`data-t-search-label="Search for an area"`,
		`data-t-search-placeholder="For example: Gabrovo"`,
		`data-t-search-hint=`,
		`data-t-search-empty="No area by that name"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the table island does not carry %s", want)
		}
	}
}

// The "no such area" sentence is the masthead finder's key, reused. A second
// key saying the same thing is a second thing to translate and to keep in
// parity across both catalogues.
func TestTableSearchReusesTheFindersEmptyLine(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/").Body.String()

	empty := strings.Count(body, `="No area by that name"`)
	if empty != 2 {
		t.Errorf("the finder and the table search do not share one empty line: found %d uses", empty)
	}
}

func TestTableSearchIsTranslated(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	if !strings.Contains(body, `data-t-search-label="Търсене на област"`) {
		t.Error("the Bulgarian page does not carry the Bulgarian search label")
	}
}
