package api_test

import (
	"testing"

	"airbg.org/internal/api"
)

func TestAttributionsNameBothNetworksAndTheirMaps(t *testing.T) {
	got := api.Attributions()

	want := map[string]string{
		"sensor.community": "https://maps.sensor.community/",
		"eea":              "https://eea.government.bg/kav/",
	}
	seen := map[string]bool{}
	for _, a := range got {
		if url, ok := want[a.Source]; ok {
			if a.URL != url {
				t.Errorf("%s links to %q, want %q", a.Source, a.URL, url)
			}
			seen[a.Source] = true
		}
		if a.Text == "" {
			t.Errorf("attribution %q has no text", a.Source)
		}
	}
	for s := range want {
		if !seen[s] {
			t.Errorf("no attribution for %s", s)
		}
	}
}

// ODbL 1.0 and the EEA reuse terms both require attribution.
func TestEveryIngestedSourceIsCredited(t *testing.T) {
	credited := map[string]bool{}
	for _, a := range api.Attributions() {
		credited[a.Source] = true
	}
	for _, s := range []string{"sensor.community", "eea", "openstreetmap"} {
		if !credited[s] {
			t.Errorf("%s is used and not credited", s)
		}
	}
}
