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
