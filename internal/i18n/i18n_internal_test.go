package i18n

import "testing"

// These tests live in package i18n (not i18n_test) so they can reach the
// unexported parseCatalogue directly. Load's own guard against an empty or
// unparseable catalogue only ever runs against the embedded bg.json/en.json,
// which are always well-formed — so without this, the guard has no failing
// input to prove itself against and could be deleted with the black-box
// suite still green.

func TestParseCatalogueRejectsEmptyCatalogue(t *testing.T) {
	_, err := parseCatalogue("xx", []byte(`{}`))
	if err == nil {
		t.Fatal("parseCatalogue(\"xx\", `{}`) returned a nil error for an empty catalogue")
	}
}

func TestParseCatalogueRejectsUnparseableJSON(t *testing.T) {
	_, err := parseCatalogue("xx", []byte(`not valid json`))
	if err == nil {
		t.Fatal("parseCatalogue(\"xx\", `not valid json`) returned a nil error for malformed JSON")
	}
}
