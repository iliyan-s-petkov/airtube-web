package web

import "testing"

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

// Five of the seven metrics have no band table at all. A colour here would be a
// class the scale never published — the caller draws no swatch instead.
func TestBandColourRefusesAMetricWithNoScale(t *testing.T) {
	for _, metric := range []string{"temperature", "humidity", "pressure", "noise_LAeq", ""} {
		if got := bandColour(metric, 20); got != "" {
			t.Errorf("bandColour(%q, 20) = %q, want no colour", metric, got)
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
