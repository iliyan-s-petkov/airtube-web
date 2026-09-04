package area

import "testing"

// The code an operator sees is the one in the boundary file's iso_a2 property,
// never a CLI flag — so a country cannot be mislabelled without editing the
// geometry it labels. That makes this function the only place a bad code can
// enter the table, and the only place to reject one.
func TestNormaliseCountryCode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "BG", want: "BG"},
		// GeoJSON exports differ on case and whitespace, and a lower-case
		// "bg" is unambiguous. Normalised rather than rejected.
		{in: "bg", want: "BG"},
		{in: " gr ", want: "GR"},
		{in: "", wantErr: true},    // Natural Earth uses "-99" or "" for disputed areas
		{in: "BGR", wantErr: true}, // alpha-3, the other ISO 3166-1 form
		{in: "-99", wantErr: true},
		{in: "B", wantErr: true},
	} {
		got, err := normaliseCountryCode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normaliseCountryCode(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normaliseCountryCode(%q) error = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normaliseCountryCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The empty case is the one an operator actually hits — a boundary file copied
// from a source that has no iso_a2 at all — so its message must say what to add
// and why it matters, not just "invalid".
func TestMissingCountryCodeErrorNamesTheProperty(t *testing.T) {
	_, err := normaliseCountryCode("")
	if err == nil {
		t.Fatal("normaliseCountryCode(\"\") = nil error, want one")
	}
	for _, want := range []string{"iso_a2", NationalBoundaryKind} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
