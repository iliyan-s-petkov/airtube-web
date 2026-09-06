package web_test

import (
	"strings"
	"testing"
)

// The sensor card is the kit's .place-host slot: a card under the map, in the
// flow. It used to be styled as an overlay positioned "within #area-map", but
// its container is a SIBLING of the map shell, so with no positioned ancestor
// the card resolved against the initial containing block and opened in the
// viewport's top-right corner, over the header. The class is what earns it the
// slot's spacing, so it is worth pinning.
func TestSensorPanelCarriesThePlaceHostClass(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/area/high").Body.String()

	if !strings.Contains(body, `<div class="place-host" data-island="panel"`) {
		t.Error("the sensor panel's container is not the kit's place-host slot")
	}
}

// Order is the fix, not decoration: a card that describes the marker the reader
// just clicked has to come after the map in the reading order, and before the
// freshness line that closes the page.
func TestSensorPanelSitsBetweenTheMapAndTheFreshnessLine(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/area/high").Body.String()

	mapAt := strings.Index(body, `id="area-map"`)
	panelAt := strings.Index(body, `class="place-host"`)
	freshAt := strings.Index(body, `data-island="freshness"`)

	if mapAt < 0 || panelAt < 0 || freshAt < 0 {
		t.Fatalf("the area page is missing one of its parts: map=%d panel=%d freshness=%d", mapAt, panelAt, freshAt)
	}
	if !(mapAt < panelAt && panelAt < freshAt) {
		t.Errorf("the sensor panel is not between the map and the freshness line: map=%d panel=%d freshness=%d", mapAt, panelAt, freshAt)
	}
}
