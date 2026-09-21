package web_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"airbg.org/internal/httpx"
)

// extractSensorCardHost pulls the .place-host div out of a rendered page's
// body: the sensor card's whole host block, data-t-* vocabulary included.
func extractSensorCardHost(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<div class="place-host`)
	if start < 0 {
		t.Fatal("the page has no place-host div")
	}
	end := strings.Index(body[start:], "></div>")
	if end < 0 {
		t.Fatal("the place-host div is never closed")
	}
	return body[start : start+end+len("></div>")]
}

// sensorCardHostGolden pins the sensor card's host block, byte for byte, on
// each of the three pages that mount it (home, area, embed). It was captured
// from the three copy-pasted blocks before they were folded into the
// "sensorCardHost" partial in base.gohtml (see internal/web/render.go's
// PanelHostClass) — a refactor that changes what any one of these pages
// renders is the refactor gone wrong, not this test gone stale.
//
// Mutation check: dropping one data-t-* attribute from the partial fails all
// three cases below with the same one assertion, proving the golden actually
// pins the block rather than merely running past it.
func TestSensorCardHostIsByteIdenticalAcrossPages(t *testing.T) {
	rr := renderer(t, rankingSnapshot())
	cfg := testConfig(t)
	framed := httpx.SecurityHeaders(rr.Routes(), cfg.Listen.CSP, cfg.Listen.PermissionsPolicy)

	cases := []struct {
		name   string
		path   string
		mux    http.Handler
		golden string
	}{
		{"index", "/en/", rr.Routes(), indexHostGolden},
		{"area", "/en/area/high", rr.Routes(), areaHostGolden},
		{"embed", "/en/embed", framed, embedHostGolden},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			got := extractSensorCardHost(t, rec.Body.String())
			if got != c.golden {
				t.Errorf("%s sensor card host changed:\ngot:  %s\nwant: %s", c.name, got, c.golden)
			}
		})
	}
}

const indexHostGolden = `<div class="place-host" data-island="panel"
     data-metrics="C6H6,CO,NO2,NOX,O3,P1,P2,SO2,humidity,noise_LA_max,noise_LAeq,pressure,temperature"
     data-metric-labels="Benzene,Carbon monoxide,Nitrogen dioxide,Nitrogen oxides,Ozone,PM10,PM2.5,Sulphur dioxide,Humidity,Noise (max),Noise (LAeq),Pressure,Temperature"
     data-metric="P2"
     data-period="24h"
     data-line-colour="#2563eb"
     data-compare-colour="#8a3ffc"
     data-t-title="Sensor"
     data-t-close="Close"
     data-t-no-value="no reading"
     data-t-flag-out-of-range="This reading is out of the expected range."
     data-t-flag-stuck="This reading has not changed in a while."
     data-t-flag-spatial-outlier="This reading disagrees with nearby sensors."
     data-t-flag-source-invalid="The newest reading was rejected by its source; this is the last accepted one."
     data-t-chart-title="PM2.5, last 24 hours"
     data-t-chart-value="µg/m³"
     data-t-chart-time="Time"
     data-t-chart-empty="No readings in the selected period"
     data-t-chart-unavailable="Map data is unavailable right now"
     data-periods="24h,7d,30d,1y"
     data-period-labels="24 hours,7 days,30 days,1 year"
     data-t-chart-metric-legend="Metric"
     data-t-chart-period-legend="Period"
     data-series-colours="#0f9d58,#d97706,#dc2626,#0891b2,#a16207"
     data-t-chart-reset="Reset view"
     data-t-period-custom="Custom range"
     data-t-period-from="From"
     data-t-period-to="To"
     data-t-period-now="Now"
     data-t-chart-range-invalid="Choose a start and an end, with the start earlier."
     data-t-nearby-legend="Nearby sensors"
     data-t-nearby-off="off"
     data-t-nearby-single-only="Available while one metric is shown"
     data-t-nearby-low="Nearby lowest"
     data-t-nearby-median="Nearby median"
     data-t-nearby-high="Nearby highest"
     data-t-details="About this station"
     data-t-detail-devices="Devices"
     data-t-detail-hardware="Hardware"
     data-t-detail-since="In our data since"
     data-t-detail-updated="Last reading"
     data-t-detail-coords="Coordinates"
     data-t-network="Network"
     data-t-station-code="EoI code"
     data-t-station-type="Station type"
     data-t-station-area="Area type"></div>`

const areaHostGolden = `<div class="place-host" data-island="panel"
     data-metrics="C6H6,CO,NO2,NOX,O3,P1,P2,SO2,humidity,noise_LA_max,noise_LAeq,pressure,temperature"
     data-metric-labels="Benzene,Carbon monoxide,Nitrogen dioxide,Nitrogen oxides,Ozone,PM10,PM2.5,Sulphur dioxide,Humidity,Noise (max),Noise (LAeq),Pressure,Temperature"
     data-metric="P2"
     data-period="24h"
     data-line-colour="#2563eb"
     data-compare-colour="#8a3ffc"
     data-t-title="Sensor"
     data-t-close="Close"
     data-t-no-value="no reading"
     data-t-flag-out-of-range="This reading is out of the expected range."
     data-t-flag-stuck="This reading has not changed in a while."
     data-t-flag-spatial-outlier="This reading disagrees with nearby sensors."
     data-t-flag-source-invalid="The newest reading was rejected by its source; this is the last accepted one."
     data-t-chart-title="PM2.5, last 24 hours"
     data-t-chart-value="µg/m³"
     data-t-chart-time="Time"
     data-t-chart-empty="No readings in the selected period"
     data-t-chart-unavailable="Map data is unavailable right now"
     data-periods="24h,7d,30d,1y"
     data-period-labels="24 hours,7 days,30 days,1 year"
     data-t-chart-metric-legend="Metric"
     data-t-chart-period-legend="Period"
     data-series-colours="#0f9d58,#d97706,#dc2626,#0891b2,#a16207"
     data-t-chart-reset="Reset view"
     data-t-period-custom="Custom range"
     data-t-period-from="From"
     data-t-period-to="To"
     data-t-period-now="Now"
     data-t-chart-range-invalid="Choose a start and an end, with the start earlier."
     data-t-nearby-legend="Nearby sensors"
     data-t-nearby-off="off"
     data-t-nearby-single-only="Available while one metric is shown"
     data-t-nearby-low="Nearby lowest"
     data-t-nearby-median="Nearby median"
     data-t-nearby-high="Nearby highest"
     data-t-details="About this station"
     data-t-detail-devices="Devices"
     data-t-detail-hardware="Hardware"
     data-t-detail-since="In our data since"
     data-t-detail-updated="Last reading"
     data-t-detail-coords="Coordinates"
     data-t-network="Network"
     data-t-station-code="EoI code"
     data-t-station-type="Station type"
     data-t-station-area="Area type"></div>`

const embedHostGolden = `<div class="place-host embed__panel" data-island="panel"
     data-metrics="C6H6,CO,NO2,NOX,O3,P1,P2,SO2,humidity,noise_LA_max,noise_LAeq,pressure,temperature"
     data-metric-labels="Benzene,Carbon monoxide,Nitrogen dioxide,Nitrogen oxides,Ozone,PM10,PM2.5,Sulphur dioxide,Humidity,Noise (max),Noise (LAeq),Pressure,Temperature"
     data-metric="P2"
     data-period="24h"
     data-line-colour="#2563eb"
     data-compare-colour="#8a3ffc"
     data-t-title="Sensor"
     data-t-close="Close"
     data-t-no-value="no reading"
     data-t-flag-out-of-range="This reading is out of the expected range."
     data-t-flag-stuck="This reading has not changed in a while."
     data-t-flag-spatial-outlier="This reading disagrees with nearby sensors."
     data-t-flag-source-invalid="The newest reading was rejected by its source; this is the last accepted one."
     data-t-chart-title="PM2.5, last 24 hours"
     data-t-chart-value="µg/m³"
     data-t-chart-time="Time"
     data-t-chart-empty="No readings in the selected period"
     data-t-chart-unavailable="Map data is unavailable right now"
     data-periods="24h,7d,30d,1y"
     data-period-labels="24 hours,7 days,30 days,1 year"
     data-t-chart-metric-legend="Metric"
     data-t-chart-period-legend="Period"
     data-series-colours="#0f9d58,#d97706,#dc2626,#0891b2,#a16207"
     data-t-chart-reset="Reset view"
     data-t-period-custom="Custom range"
     data-t-period-from="From"
     data-t-period-to="To"
     data-t-period-now="Now"
     data-t-chart-range-invalid="Choose a start and an end, with the start earlier."
     data-t-nearby-legend="Nearby sensors"
     data-t-nearby-off="off"
     data-t-nearby-single-only="Available while one metric is shown"
     data-t-nearby-low="Nearby lowest"
     data-t-nearby-median="Nearby median"
     data-t-nearby-high="Nearby highest"
     data-t-details="About this station"
     data-t-detail-devices="Devices"
     data-t-detail-hardware="Hardware"
     data-t-detail-since="In our data since"
     data-t-detail-updated="Last reading"
     data-t-detail-coords="Coordinates"
     data-t-network="Network"
     data-t-station-code="EoI code"
     data-t-station-type="Station type"
     data-t-station-area="Area type"></div>`
