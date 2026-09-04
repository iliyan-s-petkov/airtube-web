package upstream

import "testing"

// The allow list has to reach upstream as a filter segment, or every cycle
// downloads the whole world's sensors and throws almost all of them away at
// the boundary test. These tests pin the one place the two representations —
// a Go slice and sensor.community's path syntax — are converted.
func TestFetchURLAppendsTheCountryFilter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		base      string
		countries []string
		want      string
	}{
		{
			name:      "several countries are comma separated",
			base:      "https://data.sensor.community/airrohr/v1/filter/",
			countries: []string{"BG", "GR", "MK", "RO", "RS", "TR"},
			want:      "https://data.sensor.community/airrohr/v1/filter/country=BG,GR,MK,RO,RS,TR",
		},
		{
			// A trailing slash is easy to omit by hand, and omitting it would
			// otherwise produce ".../filtercountry=BG" — a 404 rather than a
			// visible configuration error.
			name:      "a missing trailing slash is supplied",
			base:      "https://data.sensor.community/airrohr/v1/filter",
			countries: []string{"BG"},
			want:      "https://data.sensor.community/airrohr/v1/filter/country=BG",
		},
		{
			// Not reachable through a validated config, which rejects an empty
			// list; pinned so a future caller that skips validation degrades to
			// the unfiltered endpoint rather than to a malformed URL.
			name:      "an empty list leaves the base untouched",
			base:      "https://data.sensor.community/airrohr/v1/filter/",
			countries: nil,
			want:      "https://data.sensor.community/airrohr/v1/filter/",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fetchURL(tc.base, tc.countries); got != tc.want {
				t.Errorf("fetchURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The client must build its URL from the configured list, not from whatever
// the URL happened to end in. Config validation rejects a url that still
// carries its own country= segment, so this is the only path that can produce
// one.
func TestNewBuildsTheFetchURLFromTheAllowList(t *testing.T) {
	cfg := testUpstreamConfig("https://example.test/v1/filter/")
	cfg.Countries = []string{"BG", "RO"}

	c := New(cfg)
	const want = "https://example.test/v1/filter/country=BG,RO"
	if c.baseURL != want {
		t.Errorf("baseURL = %q, want %q", c.baseURL, want)
	}
}
