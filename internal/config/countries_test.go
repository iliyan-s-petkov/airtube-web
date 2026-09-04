package config

import (
	"strings"
	"testing"
)

// wantInvalid asserts Validate rejects cfg with a message naming want.
func wantInvalid(t *testing.T, cfg Config, want string) {
	t.Helper()
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() = nil, want an error mentioning %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("Validate() = %v, want a message containing %q", err, want)
	}
}

// The two enforcement points — the fetch URL and the boundary set — are driven
// by one list precisely so they cannot drift. A url that still carries its own
// country= segment reintroduces the drift: sensor.community honours the first
// filter, so the fetch stays narrow while the boundary set widens, and the
// missing countries look like countries with no sensors.
func TestURLCarryingItsOwnCountryFilterIsRejected(t *testing.T) {
	cfg := good(t)
	cfg.Upstream.URL = "https://data.sensor.community/airrohr/v1/filter/country=BG"
	wantInvalid(t, cfg, "upstream.countries")
}

// An empty list must not mean "everything". It would fetch the global feed and
// leave no boundary to test it against — the unfiltered-ingest hole the
// geometric filter exists to close.
func TestEmptyCountryListIsRejected(t *testing.T) {
	cfg := good(t)
	cfg.Upstream.Countries = nil
	wantInvalid(t, cfg, "upstream.countries is empty")
}

func TestMalformedCountryCodeIsRejected(t *testing.T) {
	for _, code := range []string{"bg", "BGR", "B", "", "B1", "БГ"} {
		t.Run(code, func(t *testing.T) {
			cfg := good(t)
			cfg.Upstream.Countries = []string{code}
			wantInvalid(t, cfg, "ISO 3166-1 alpha-2")
		})
	}
}

func TestDuplicateCountryIsRejected(t *testing.T) {
	cfg := good(t)
	cfg.Upstream.Countries = []string{"BG", "GR", "BG"}
	wantInvalid(t, cfg, "listed more than once")
}

// The committed file is the deployed one. Bulgaria must stay in the list — the
// site is a Bulgarian air-quality map — and every entry must be well formed,
// which the loader would otherwise only discover at startup.
func TestCommittedConfigEnablesBulgariaAndItsNeighbours(t *testing.T) {
	cfg := good(t)

	want := map[string]bool{"BG": false, "GR": false, "MK": false, "RO": false, "RS": false, "TR": false}
	for _, code := range cfg.Upstream.Countries {
		if !IsCountryCode(code) {
			t.Errorf("upstream.countries contains %q, which is not an alpha-2 code", code)
		}
		if _, ok := want[code]; !ok {
			t.Errorf("upstream.countries contains unexpected %q; adding a country also needs data/boundaries/<country>.geojson", code)
			continue
		}
		want[code] = true
	}
	for code, seen := range want {
		if !seen {
			t.Errorf("upstream.countries is missing %q", code)
		}
	}
}

func TestIsCountryCode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"BG", true},
		{"TR", true},
		{"bg", false},  // case is normalised at import, not accepted in config
		{"BGR", false}, // alpha-3, the other ISO 3166-1 form
		{"B", false},
		{"", false},
		{"B1", false},
		{" BG", false},
	} {
		if got := IsCountryCode(tc.in); got != tc.want {
			t.Errorf("IsCountryCode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
