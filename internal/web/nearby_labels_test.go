package web_test

import (
	"testing"
)

// The nearby-sensors control names three lines, an off state and the reason it
// is sometimes unavailable. Every page that mounts the panel island has to carry
// all five, or the control renders with a blank button on that page alone —
// which is exactly what one shared define is meant to prevent.
func TestPanelIslandCarriesTheNearbyLabels(t *testing.T) {
	rr := renderer(t, rankingSnapshot())

	for _, path := range []string{"/en/", "/en/area/high", "/en/embed"} {
		tag := islandTag(t, fetch(t, rr, path).Body.String(), "panel")
		for _, attr := range []string{
			"data-t-nearby-legend", "data-t-nearby-off", "data-t-nearby-single-only",
			"data-t-nearby-low", "data-t-nearby-median", "data-t-nearby-high",
		} {
			if v := attrValue(t, tag, attr); v == "" {
				t.Errorf("%s: %s is empty on the panel island", path, attr)
			}
		}
	}
}
