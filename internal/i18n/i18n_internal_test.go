package i18n

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// These tests live in package i18n (not i18n_test) so they can reach the
// unexported parseCatalogue directly. Load's own guard against an empty or
// unparseable catalogue only ever runs against the embedded bg.json/en.json,
// which are always well-formed — so without this, the guard has no failing
// input to prove itself against and could be deleted with the black-box
// suite still green.
//
// The two rejection shapes must be told apart, not just detected. When
// json.Unmarshal fails, m is left nil, so a swallowed unmarshal error would
// still trip the len(m)==0 empty-catalogue check and return *an* error — just
// the wrong one. An operator debugging a startup failure on malformed JSON
// would then be told "catalogue is empty", which is false and points nowhere
// near the actual syntax error. So each test asserts on the shape of the
// error, not merely its presence.

func TestParseCatalogueRejectsEmptyCatalogue(t *testing.T) {
	_, err := parseCatalogue("xx", []byte(`{}`))
	if err == nil {
		t.Fatal("parseCatalogue(\"xx\", `{}`) returned a nil error for an empty catalogue")
	}
	if !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("parseCatalogue error %q does not report an empty catalogue", err)
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		t.Fatalf("parseCatalogue on valid-but-empty JSON reported a JSON syntax error (%v); the empty-catalogue check did not fire on its own", err)
	}
}

func TestParseCatalogueRejectsUnparseableJSON(t *testing.T) {
	_, err := parseCatalogue("xx", []byte(`not valid json`))
	if err == nil {
		t.Fatal("parseCatalogue(\"xx\", `not valid json`) returned a nil error for malformed JSON")
	}
	// Must be a genuine JSON syntax error, not the empty-catalogue error
	// standing in for it — malformed JSON and an empty catalogue need
	// different operator responses, and only the syntax error carries the
	// byte offset that says where to look.
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("parseCatalogue error %q does not wrap a *json.SyntaxError; the unmarshal failure may have been swallowed and misreported as an empty catalogue", err)
	}
}
