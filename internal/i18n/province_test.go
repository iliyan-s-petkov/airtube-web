package i18n_test

import (
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

// In English an област is a province, never "oblast". Transliterating it leaves
// an English reader with a term they have to look up, on a page whose whole job
// is to be understood quickly — and the plural was worse: "oblasti" is not an
// English word at all.
//
// This is a rule about what appears ON SCREEN, so it checks the catalogue's
// values and deliberately not its keys. `col.oblast`, `data-oblast` and
// oblast-table.go are identifiers; renaming them would churn every reference to
// buy a reader nothing.
//
// Six strings breached this when the check was written — the map's tier line
// and five sentences on the about page. A regression here is easy to introduce
// and invisible to anyone reading only the Bulgarian.
func TestEnglishSaysProvinceNotOblast(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	for _, key := range cat.Keys() {
		value := cat.T("en", key)
		if strings.Contains(strings.ToLower(value), "oblast") {
			t.Errorf("en[%s] transliterates област: %q", key, value)
		}
	}
}
