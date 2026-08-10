package api_test

import (
	"testing"

	"airbg.org/internal/api"
)

// TestScaleBandsAreMonotonic. Bands out of order, or with a repeated upper
// bound, would silently mis-colour readings: a lookup walking the slice returns
// the first match, so a low band placed after a high one is never reached.
func TestScaleBandsAreMonotonic(t *testing.T) {
	for _, s := range api.Scales() {
		if len(s.Bands) < 2 {
			t.Errorf("%s/%s: %d bands, want at least 2", s.Name, s.Metric, len(s.Bands))
			continue
		}
		prev := -1.0
		for i, b := range s.Bands {
			if b.Upper == nil {
				if i != len(s.Bands)-1 {
					t.Errorf("%s/%s: band %d is open-ended but is not last; every band after it is unreachable", s.Name, s.Metric, i)
				}
				continue
			}
			if *b.Upper <= prev {
				t.Errorf("%s/%s: band %d upper %v is not above the previous %v", s.Name, s.Metric, i, *b.Upper, prev)
			}
			prev = *b.Upper
		}
		if last := s.Bands[len(s.Bands)-1]; last.Upper != nil {
			t.Errorf("%s/%s: the last band has an upper bound of %v; a reading above it would fall into no band at all", s.Name, s.Metric, *last.Upper)
		}
	}
}

// TestScalesAreBilingualAndCarryTheDisclaimer. Phase 1 §9.2 requires the
// indicative-data disclaimer wherever a value is shown; shipping it with the
// scale means a consumer cannot render bands without also having the caveat.
func TestScalesAreBilingualAndCarryTheDisclaimer(t *testing.T) {
	for _, s := range api.Scales() {
		if s.Notes == "" || s.NotesBG == "" {
			t.Errorf("%s/%s: notes missing (en=%q bg=%q)", s.Name, s.Metric, s.Notes, s.NotesBG)
		}
		if s.Unit == "" {
			t.Errorf("%s/%s: unit is empty", s.Name, s.Metric)
		}
		for i, b := range s.Bands {
			if b.Label == "" || b.LabelBG == "" {
				t.Errorf("%s/%s band %d: a label is empty (en=%q bg=%q)", s.Name, s.Metric, i, b.Label, b.LabelBG)
			}
			if len(b.Colour) != 7 || b.Colour[0] != '#' {
				t.Errorf("%s/%s band %d: colour %q is not a #rrggbb hex string", s.Name, s.Metric, i, b.Colour)
			}
		}
	}
}

// TestScalesReturnsIndependentCopies: the Upper fields are pointers, so a shared
// package-level slice would let one caller's mutation change what every other
// caller reads — including the JSON the API has already promised.
func TestScalesReturnsIndependentCopies(t *testing.T) {
	a, b := api.Scales(), api.Scales()
	if a[0].Bands[0].Upper == b[0].Bands[0].Upper {
		t.Error("two calls returned the same *float64; Scales must not share mutable state")
	}
}

// TestScalesCoverBothParticulateMetrics.
func TestScalesCoverBothParticulateMetrics(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range api.Scales() {
		seen[s.Name+"/"+s.Metric] = true
	}
	for _, want := range []string{"eaqi/P1", "eaqi/P2", "eu_limit/P1", "eu_limit/P2", "who/P1", "who/P2"} {
		if !seen[want] {
			t.Errorf("missing scale %s", want)
		}
	}
}
