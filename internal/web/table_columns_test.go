package web_test

import (
	"strings"
	"testing"
)

// The Колони menu is a control the server renders nothing of — every column it
// can hide is already in the table — so what it needs from the server is the
// two words that name it.
func TestColumnMenuCarriesItsVocabulary(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/").Body.String()

	for _, want := range []string{
		`data-t-columns="Columns"`,
		`data-t-visible-columns="Visible columns"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the table island does not carry %s", want)
		}
	}
}

func TestColumnMenuIsTranslated(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/").Body.String()

	if !strings.Contains(body, `data-t-columns="Колони"`) {
		t.Error("the Bulgarian page does not carry the Bulgarian columns label")
	}
}
