package api_test

import (
	"math"
	"strings"
	"testing"

	"airbg.org/internal/api"
	"airbg.org/internal/upstream"
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
		// Below every possible boundary, not below zero: temperature bands are
		// legitimately negative, and a floor of -1 would reject the frost band
		// for being where frost is.
		prev := math.Inf(-1)
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

// TestEveryScaleStatesItsCeiling. Without one the client guesses the top of the
// ramp from the width of the band below, which put the top of the PM2.5 bar at
// 75 µg/m³ — so every winter reading above that painted the same colour and the
// key printed no number for it. A ceiling at or below the last stated band
// boundary is the same failure with extra steps.
func TestEveryScaleStatesItsCeiling(t *testing.T) {
	for _, s := range api.Scales() {
		if s.Ceiling == nil {
			t.Errorf("%s/%s: no ceiling; the client would have to guess the top of the ramp", s.Name, s.Metric)
			continue
		}
		highest := math.Inf(-1)
		for _, b := range s.Bands {
			if b.Upper != nil && *b.Upper > highest {
				highest = *b.Upper
			}
		}
		if *s.Ceiling <= highest {
			t.Errorf("%s/%s: ceiling %v is not above the highest band boundary %v, so the open top band has no width to draw",
				s.Name, s.Metric, *s.Ceiling, highest)
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

// TestEveryCanonicalMetricIsScaled. A metric the store keeps but this file has
// no table for reaches the reader as a bare number with no unit after it and a
// dot painted the same grey as one with no reading at all — which is how
// temperature, humidity and pressure shipped. The metric list is the store's,
// so a metric added there fails here until it has a table.
func TestEveryCanonicalMetricIsScaled(t *testing.T) {
	units := map[string]string{}
	for _, s := range api.Scales() {
		units[s.Metric] = s.Unit
	}
	for _, m := range upstream.CanonicalMetrics() {
		if units[m] == "" {
			t.Errorf("canonical metric %q has no scale, so it has no unit and no colour", m)
		}
	}
}

// TestEveryScaleForOneMetricAgreesOnItsUnit. The frontend asks for a metric's
// unit and takes the first table it finds (unitFor, web/src/lib/metrics.js), so
// two tables for one metric disagreeing about the unit would make the printed
// unit depend on the order of this slice.
func TestEveryScaleForOneMetricAgreesOnItsUnit(t *testing.T) {
	first := map[string]string{}
	for _, s := range api.Scales() {
		if prev, seen := first[s.Metric]; seen && prev != s.Unit {
			t.Errorf("%s: unit %q here but %q in an earlier table", s.Metric, s.Unit, prev)
			continue
		}
		first[s.Metric] = s.Unit
	}
}

// A scale that cites an authority must link it: the legend's info dialog offers
// the reader the guideline itself, and a table naming "Directive 2008/50/EC"
// with nowhere to read it asks for the colours to be taken on trust.
//
// The meteo tables are the exception and say so by carrying no source — they
// are an axis, not a health guideline.
func TestGuidelineScalesLinkTheirSource(t *testing.T) {
	for _, s := range api.Scales() {
		if s.Name == "meteo" {
			if s.Source != "" {
				t.Errorf("%s/%s cites %q, but a weather axis has no guideline behind it", s.Name, s.Metric, s.Source)
			}
			continue
		}
		if !strings.HasPrefix(s.Source, "https://") {
			t.Errorf("%s/%s source = %q, want an https link to the published guideline", s.Name, s.Metric, s.Source)
		}
	}
}
