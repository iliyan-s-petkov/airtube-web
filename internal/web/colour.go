package web

import "airbg.org/internal/api"

// bandColour is the table's swatch colour for one reading: the first band whose
// inclusive upper bound is at or above the value, from the first scale table
// published for that metric.
//
// It is the same rule web/src/lib/colour.js applies to the map's dots, and the
// same table selection web/src/islands/map.js's bandsFor makes — deliberately,
// because a province coloured one way as a dot and another way as a row would
// be the site disagreeing with itself on one screen. The rule lives twice
// because the audiences differ: the map colours GeoJSON in the browser from
// /api/v1/scales, while this row is rendered server-side and must be coloured
// with no JavaScript at all. colour_test.go pins the two copies to the same
// boundary cases.
//
// An empty string, not a colour, when there is no band table for the metric or
// no value: five of the seven metrics have no bands at all, and a swatch that
// is a colour anyway would assert a class the scale does not claim. The caller
// renders the chip without a swatch instead.
// gaugeUnit is the only unit an arc is drawn for. A particulate reading starts
// at zero and gets worse, so the share of the scale it has used up is a fact
// about the air. Pressure, temperature and noise do not work that way — 1013
// hPa is an ordinary day, not 97% of a danger — and an arc there would invent
// an alarm out of a scale that only ever meant to bound an axis.
const gaugeUnit = "µg/m³"

// gaugePercent places a reading on its metric's published scale, 0-100, for the
// arc a readout card draws. The ceiling is the highest FINITE band bound: the
// top band is open-ended, so a fraction of it has no meaning and a value above
// the ceiling is a full arc rather than an overflowing one.
//
// ok is false for a metric counted in anything but gaugeUnit, and for one with
// no bands at all, so the card renders a plain figure instead.
func gaugePercent(metric string, value float64) (int, bool) {
	ceiling, found := 0.0, false
	for _, scale := range api.Scales() {
		if scale.Metric != metric {
			continue
		}
		if scale.Unit != gaugeUnit {
			return 0, false
		}
		ceiling, found = finiteCeiling(scale.Bands)
		break
	}
	if !found || ceiling <= 0 {
		return 0, false
	}
	pct := int(value / ceiling * 100)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct, true
}

// finiteCeiling is the highest bound in the table, not the last one: nothing
// requires a published scale to arrive in ascending order, and taking the last
// would put the arc against whichever bound happened to be written at the end.
func finiteCeiling(bands []api.Band) (float64, bool) {
	top, found := 0.0, false
	for _, band := range bands {
		if band.Upper != nil && *band.Upper > top {
			top, found = *band.Upper, true
		}
	}
	return top, found
}

func bandColour(metric string, value float64) string {
	for _, scale := range api.Scales() {
		if scale.Metric != metric {
			continue
		}
		for _, band := range scale.Bands {
			// Upper is INCLUSIVE, so a value exactly on a boundary belongs to
			// the lower band; a nil Upper is the open-ended top.
			if band.Upper == nil || value <= *band.Upper {
				return band.Colour
			}
		}
		// Only reachable from a scale with no open top band, which would be a
		// server bug. No colour rather than the last band's, for the reason
		// above: better to show nothing than to claim a class.
		return ""
	}
	return ""
}
