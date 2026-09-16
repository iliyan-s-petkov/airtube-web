package api_test

import (
	"testing"

	"airbg.org/internal/api"
)

// The published EAQI table, transcribed from the index's own "Legend explained"
// panel at https://airindex.eea.europa.eu/AQI/index.html. Upper edges in µg/m³;
// the sixth band is open-ended in the source and carries no edge here.
//
// These are the bands revised against the WHO 2021 guidelines (ETC HE Report
// 2024/17): the first two edges are the WHO annual and 24-hour guideline values,
// which is why PM2.5 Good stops at 5 and not at the 10 the older table used.
// Nothing pinned these numbers before, and every one of the five tables had
// drifted to the superseded index.
var publishedEAQI = map[string][]float64{
	"P2":  {5, 15, 50, 90, 140},
	"P1":  {15, 45, 120, 195, 270},
	"O3":  {60, 100, 120, 160, 180},
	"NO2": {10, 25, 60, 100, 150},
	"SO2": {20, 40, 125, 190, 275},
}

func TestTheEAQITablesMatchThePublishedBands(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range api.Scales() {
		if s.Name != "eaqi" {
			continue
		}
		want, ok := publishedEAQI[s.Metric]
		if !ok {
			t.Errorf("%s is published as an EAQI scale but the index has no bands for it", s.Metric)
			continue
		}
		seen[s.Metric] = true

		if len(s.Bands) != len(want)+1 {
			t.Errorf("%s has %d bands, want %d: the index names six classes", s.Metric, len(s.Bands), len(want)+1)
			continue
		}
		for i, edge := range want {
			got := s.Bands[i].Upper
			if got == nil {
				t.Errorf("%s band %d (%s) is open-ended, want an upper edge of %g", s.Metric, i, s.Bands[i].Label, edge)
				continue
			}
			if *got != edge {
				t.Errorf("%s band %d (%s) upper = %g, want %g", s.Metric, i, s.Bands[i].Label, *got, edge)
			}
		}
		if last := s.Bands[len(s.Bands)-1]; last.Upper != nil {
			t.Errorf("%s top band has an upper edge of %g; the index leaves it open", s.Metric, *last.Upper)
		}
	}

	for metric := range publishedEAQI {
		if !seen[metric] {
			t.Errorf("no EAQI scale published for %s", metric)
		}
	}
}
