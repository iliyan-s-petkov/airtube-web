package web

import (
	"testing"

	"airbg.org/internal/api"
	"airbg.org/internal/upstream"
)

// bandColour is a second copy of a rule web/src/lib/colour.js already applies to
// the map's dots, so these cases are that file's cases: the boundary is
// INCLUSIVE and belongs to the lower band, the top band is open, and anything
// the scale does not claim gets no colour rather than the nearest one.
func TestBandColourPicksTheBandTheValueFallsIn(t *testing.T) {
	cases := []struct {
		name   string
		metric string
		value  float64
		want   string
	}{
		{"the first band", "P2", 1, "#50f0e6"},
		// 5 is EAQI PM2.5's first upper bound. On the boundary is IN the lower
		// band, which is the one rule the two copies could most easily disagree
		// about without anyone noticing.
		{"exactly on a boundary stays below it", "P2", 5, "#50f0e6"},
		{"just past a boundary moves up", "P2", 5.1, "#50ccaa"},
		{"the open top band", "P2", 10000, "#7d2181"},
		// PM10's table is not PM2.5's: 25 is "Fair" for PM10 and "Poor" for
		// PM2.5. A lookup that ignored the metric would paint both the same.
		{"the metric picks the table", "P1", 25, "#50ccaa"},
		{"and the same value bands differently for PM2.5", "P2", 25, "#ff5050"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bandColour(c.metric, c.value); got != c.want {
				t.Errorf("bandColour(%q, %v) = %q, want %q", c.metric, c.value, got, c.want)
			}
		})
	}
}

// A metric no table claims gets no colour rather than the nearest one: a
// colour here would be a class the scale never published, and the caller draws
// no swatch instead.
func TestBandColourRefusesAMetricWithNoScale(t *testing.T) {
	for _, metric := range []string{"", "durP1", "signal"} {
		if got := bandColour(metric, 20); got != "" {
			t.Errorf("bandColour(%q, 20) = %q, want no colour", metric, got)
		}
	}
}

// Every metric the store keeps now has a table (api.Scales), so every row this
// package renders can carry a swatch. Before the official layer, five of the
// seven community metrics drew none.
func TestBandColourCoversEveryCanonicalMetric(t *testing.T) {
	for _, metric := range upstream.CanonicalMetrics() {
		if got := bandColour(metric, 20); got == "" {
			t.Errorf("bandColour(%q, 20) is empty; the metric has no band table", metric)
		}
	}
}

// The first table published for a metric, matched on the metric and not on
// position — the same choice bandsFor makes in web/src/islands/map.js. Scales()
// carries three tables for P2 (eaqi, eu_limit, who) and the map paints the
// first; a row painted from a different one would disagree with its own dot.
func TestBandColourUsesTheSameTableTheMapDoes(t *testing.T) {
	// 20 is "Moderate" (#f0e641) under EAQI and "within the limit" (#50ccaa)
	// under both eu_limit and who, so this value can only come out right from
	// the table the map uses.
	if got, want := bandColour("P2", 20), "#f0e641"; got != want {
		t.Errorf("bandColour(P2, 20) = %q, want the EAQI band %q", got, want)
	}
}

// The gauge's ceiling is the scale's DRAWN ceiling, the same top the map ramp
// and the legend stop at. Against the top guideline band instead, every reading
// over 50 µg/m³ drew an identical full ring. Only µg/m³ is gauged — a pressure
// or a temperature is not a fraction of its axis.
func TestGaugePercentScalesAgainstTheDrawnCeiling(t *testing.T) {
	cases := []struct {
		name   string
		metric string
		value  float64
		want   int
		wantOK bool
	}{
		{"nothing", "P2", 0, 0, true},
		{"the ceiling itself", "P2", ceilingOf(t, "P2"), 100, true},
		{"half the ceiling", "P2", ceilingOf(t, "P2") / 2, 50, true},
		{"past the open top band", "P2", ceilingOf(t, "P2") * 3, 100, true},
		{"a negative reading", "P2", -5, 0, true},
		{"a metric counted in another unit", "pressure", 1013, 0, false},
		{"a metric with no scale at all", "not_a_metric", 5, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := gaugePercent(c.metric, c.value)
			if ok != c.wantOK || got != c.want {
				t.Errorf("gaugePercent(%q, %v) = %d, %v; want %d, %v", c.metric, c.value, got, ok, c.want, c.wantOK)
			}
		})
	}
}

// ceilingOf reads the same first-matching table gaugePercent reads, so the
// cases above state a ratio rather than a hardcoded 500 that a config change
// would silently falsify.
func ceilingOf(t *testing.T, metric string) float64 {
	t.Helper()
	for _, scale := range api.Scales() {
		if scale.Metric != metric {
			continue
		}
		if scale.Ceiling == nil || *scale.Ceiling <= 0 {
			t.Fatalf("%s states no drawn ceiling", metric)
		}
		return *scale.Ceiling
	}
	t.Fatalf("%s has no scale at all", metric)
	return 0
}

// A ceiling well above the top band is the whole point: the arc must be able to
// say 51 and 500 differently, and the top EAQI band for PM2.5 opens at 50.
func TestTheDrawnCeilingIsAboveTheTopBand(t *testing.T) {
	band, ok := finiteCeiling(scaleFor(t, "P2").Bands)
	if !ok {
		t.Fatal("P2 has no finite band bound")
	}
	if ceiling := ceilingOf(t, "P2"); ceiling <= band {
		t.Errorf("drawn ceiling %v is not above the top band %v — every bad reading gauges the same", ceiling, band)
	}
}

func scaleFor(t *testing.T, metric string) api.Scale {
	t.Helper()
	for _, scale := range api.Scales() {
		if scale.Metric == metric {
			return scale
		}
	}
	t.Fatalf("no scale for %s", metric)
	return api.Scale{}
}

func TestFiniteCeilingTakesTheHighestBoundWhateverTheOrder(t *testing.T) {
	up := func(v float64) *float64 { return &v }
	bands := []api.Band{{Upper: up(50)}, {Upper: up(120)}, {Upper: up(80)}, {Upper: nil}}

	if got, ok := finiteCeiling(bands); !ok || got != 120 {
		t.Errorf("finiteCeiling = %v, %v; want 120, true — the highest bound, not the last", got, ok)
	}
	if _, ok := finiteCeiling([]api.Band{{Upper: nil}}); ok {
		t.Error("an open-ended band alone is no ceiling: a fraction of infinity means nothing")
	}
}
