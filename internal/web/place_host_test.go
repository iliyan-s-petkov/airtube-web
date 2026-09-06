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

// The kit reads top-down: where (map), then which sensor (the card), then how
// much over time (the chart). The chart used to be printed above the toolbar
// and the map — an answer before its question, and a metric switcher below the
// first thing that metric governs.
func TestChartIsLastOnTheAreaPage(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	body := fetch(t, rr, "/en/area/high").Body.String()

	mapAt := strings.Index(body, `id="area-map"`)
	panelAt := strings.Index(body, `class="place-host"`)
	chartAt := strings.Index(body, `id="chart"`)

	if mapAt < 0 || panelAt < 0 || chartAt < 0 {
		t.Fatalf("the area page is missing one of its parts: map=%d panel=%d chart=%d", mapAt, panelAt, chartAt)
	}
	if chartAt < panelAt || chartAt < mapAt {
		t.Errorf("the chart is not last: map=%d panel=%d chart=%d", mapAt, panelAt, chartAt)
	}
}

// The no-coverage notice does NOT travel with the chart. It says why the page
// has no numbers, so it belongs beside the readouts it explains — printed at
// the foot it would arrive after the reader has already given up.
func TestNoCoverageNoticeStaysAboveTheMap(t *testing.T) {
	rr := renderer(t, fixture(t))
	body := fetch(t, rr, "/en/area/vidin").Body.String()

	noticeAt := strings.Index(body, `class="notice"`)
	mapAt := strings.Index(body, `id="area-map"`)
	if noticeAt < 0 {
		t.Fatal("the uncovered area lost its no-coverage notice")
	}
	if mapAt >= 0 && noticeAt > mapAt {
		t.Errorf("the no-coverage notice sank below the map: notice=%d map=%d", noticeAt, mapAt)
	}
}
