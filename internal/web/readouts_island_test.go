package web_test

import (
	"strings"
	"testing"
)

// The island replaces the strip's figures while a sensor is open, so it needs
// the metric vocabulary and every label it will print. Attributes, not an
// inline config object: style-src and script-src are both 'self'.
func TestReadoutsIslandCarriesItsLabels(t *testing.T) {
	rr := renderer(t, rankingSnapshot())

	for _, path := range []string{"/en/", "/en/area/high"} {
		tag := islandTag(t, fetch(t, rr, path).Body.String(), "readouts")
		for _, attr := range []string{
			"data-metrics", "data-metric-labels", "data-metric",
			"data-t-high", "data-t-low", "data-t-median",
			"data-t-this-sensor", "data-t-of-total",
			"data-t-above", "data-t-below", "data-t-at",
			"data-t-area-sensors", "data-t-sensors-only",
		} {
			if v := attrValue(t, tag, attr); v == "" {
				t.Errorf("%s: %s is empty on the readouts island", path, attr)
			}
		}
	}
}

// The server's own cells stay inside the island. Closing the sensor has to put
// back what was rendered, and it can only do that if it never left the DOM.
func TestReadoutsIslandWrapsTheServerRenderedStrip(t *testing.T) {
	rr := renderer(t, rankingSnapshot())

	for _, path := range []string{"/en/", "/en/area/high"} {
		body := fetch(t, rr, path).Body.String()
		islandAt := strings.Index(body, `data-island="readouts"`)
		stripAt := strings.Index(body, `<div class="readouts">`)
		closeAt := strings.Index(body[islandAt:], "</div>")
		if islandAt < 0 || stripAt < 0 {
			t.Fatalf("%s: island=%d strip=%d", path, islandAt, stripAt)
		}
		if stripAt < islandAt || stripAt > islandAt+closeAt {
			t.Errorf("%s: the strip is not inside the island: island=%d strip=%d", path, islandAt, stripAt)
		}
	}
}

// The placeholder is filled by the island, from the sensors the map already
// holds. A label that lost its placeholder would print "Highest ·".
func TestReadoutsIslandLabelsKeepTheirPlaceholders(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	tag := islandTag(t, fetch(t, rr, "/en/").Body.String(), "readouts")

	for attr, want := range map[string]string{
		"data-t-high":         "{metric}",
		"data-t-low":          "{metric}",
		"data-t-median":       "{metric}",
		"data-t-of-total":     "{total}",
		"data-t-area-sensors": "{area}",
		"data-t-sensors-only": "{total}",
	} {
		if v := attrValue(t, tag, attr); !strings.Contains(v, want) {
			t.Errorf("%s = %q, which cannot say %s", attr, v, want)
		}
	}
}
