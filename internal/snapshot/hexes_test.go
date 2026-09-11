package snapshot

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"airbg.org/internal/store"
)

func sensorAt(id int64, lon, lat float64, values map[string]float64) store.SensorReading {
	return store.SensorReading{
		SensorID: id, SensorType: "SDS011", Lon: lon, Lat: lat,
		Quality: "ok", Values: values,
	}
}

// Two sensors a few hundred metres apart must land in one bin at 15 km. This is
// the whole point of the tier: the payload must not distinguish them.
func TestNearbySensorsShareOneHex(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(2, 23.3260, 42.7001, map[string]float64{"P1": 30}),
	}, HexResolutionKM)
	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 hex, got %d", len(p.Hexes))
	}
	if p.Hexes[0].N != 2 {
		t.Errorf("n = %d, want 2", p.Hexes[0].N)
	}
	// 25 is both the mean and the median of two values, so this asserts only
	// that the bin summarises rather than sums. Which statistic it uses is
	// TestBinReportsMedianNotMean's job.
	if got := p.Hexes[0].Values["P1"]; got != 25 {
		t.Errorf("P1 = %v, want 25 (a summary, not a sum)", got)
	}
}

// Sofia and Varna are ~370 km apart and must never merge.
func TestDistantSensorsGetSeparateHexes(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(2, 27.9147, 43.2141, map[string]float64{"P1": 40}),
	}, HexResolutionKM)
	if len(p.Hexes) != 2 {
		t.Fatalf("want 2 hexes, got %d", len(p.Hexes))
	}
}

// Every point must land in the hex whose centre is nearest it. Independent
// rounding of q and r passes a centre-of-hex test and fails this one, which is
// why the sweep covers corners rather than a single point.
func TestEveryPointLandsInItsNearestHex(t *testing.T) {
	for lon := 22.4; lon <= 28.6; lon += 0.31 {
		for lat := 41.3; lat <= 44.2; lat += 0.17 {
			c := hexOf(lon, lat, HexResolutionKM)
			x, y := project(lon, lat)
			cx, cy := project(hexCentre(c, HexResolutionKM))
			best := math.Hypot(x-cx, y-cy)

			for dq := -2; dq <= 2; dq++ {
				for dr := -2; dr <= 2; dr++ {
					nx, ny := project(hexCentre(axial{c.q + dq, c.r + dr}, HexResolutionKM))
					if d := math.Hypot(x-nx, y-ny); d < best-1e-9 {
						t.Fatalf("(%.2f,%.2f) binned to %v at %.3f km, but %v is %.3f km away",
							lon, lat, c, best, axial{c.q + dq, c.r + dr}, d)
					}
				}
			}
		}
	}
}

// Adjacent bin centres must sit HexResolutionKM apart, or "15 km" is a label
// rather than a property.
func TestNeighbouringHexCentresAreOneResolutionApart(t *testing.T) {
	origin := axial{3, -7}
	ox, oy := project(hexCentre(origin, HexResolutionKM))
	neighbours := []axial{{4, -7}, {2, -7}, {3, -6}, {3, -8}, {4, -8}, {2, -6}}
	for _, n := range neighbours {
		nx, ny := project(hexCentre(n, HexResolutionKM))
		d := math.Hypot(ox-nx, oy-ny)
		if math.Abs(d-HexResolutionKM) > 0.01 {
			t.Errorf("centre distance to %v = %.3f km, want %.1f", n, d, HexResolutionKM)
		}
	}
}

// The grid is anchored in projected space, not at the data, so the same sensor
// bins identically whether or not other sensors are present.
func TestBinningDoesNotDependOnTheOtherSensors(t *testing.T) {
	alone := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
	}, HexResolutionKM)
	withCompany := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(2, 27.9147, 43.2141, map[string]float64{"P1": 40}),
	}, HexResolutionKM)
	var moved bool
	for _, h := range withCompany.Hexes {
		if h.N == 1 && h.Values["P1"] == 20 {
			if h.Lon != alone.Hexes[0].Lon || h.Lat != alone.Hexes[0].Lat {
				moved = true
			}
		}
	}
	if moved {
		t.Error("a sensor's bin centre moved when an unrelated sensor was added")
	}
}

// The ETag must be a function of the readings alone: two builds of the same
// data at different times, with the sensors in a different order, must agree.
func TestHexETagIgnoresTimeAndInputOrder(t *testing.T) {
	a := []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(2, 27.9147, 43.2141, map[string]float64{"P1": 40}),
		sensorAt(3, 24.7453, 42.1354, map[string]float64{"P1": 15}),
	}
	b := []store.SensorReading{a[2], a[0], a[1]}

	first, err := encode(hexPayloadFrom(time.Unix(1000, 0), a, HexResolutionKM))
	if err != nil {
		t.Fatal(err)
	}
	second, err := encode(hexPayloadFrom(time.Unix(9000, 0), b, HexResolutionKM))
	if err != nil {
		t.Fatal(err)
	}
	if first.ETag != second.ETag {
		t.Errorf("ETag moved with time or input order: %s vs %s", first.ETag, second.ETag)
	}
}

// A metric no sensor in the bin reports must be absent, not zero.
func TestAbsentMetricIsOmittedRatherThanZero(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
	}, HexResolutionKM)
	if _, ok := p.Hexes[0].Values["P2"]; ok {
		t.Error("P2 present in a bin where no sensor reported it")
	}
	if got := p.Hexes[0].Values["P1"]; got != 20 {
		t.Errorf("P1 = %v, want 20", got)
	}
}

// The payload must carry no sensor identity — that is the tier's reason to
// exist. A field added later that leaks an ID would pass every test above.
func TestHexEntryCarriesNoSensorIdentity(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(4242, 23.3219, 42.6977, map[string]float64{"P1": 20}),
	}, HexResolutionKM)
	body, err := encode(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"4242", "sensor_id", "SDS011", "quality"} {
		if containsBytes(body.JSON, forbidden) {
			t.Errorf("hex payload leaks %q", forbidden)
		}
	}
}

func containsBytes(b []byte, s string) bool {
	return len(s) > 0 && len(b) >= len(s) && indexOf(string(b), s) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func sensorFrom(id int64, lon, lat float64, source string, values map[string]float64) store.SensorReading {
	sr := sensorAt(id, lon, lat, values)
	sr.Source = source
	return sr
}

// A bin holding both networks reports each network's own median beside the
// blended one. Neither per-source number is derivable from the blended median,
// which is why all three are carried.
func TestMixedBinReportsEachNetworkSeparately(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "sensor.community", map[string]float64{"P1": 10}),
		sensorFrom(2, 23.3220, 42.6978, "sensor.community", map[string]float64{"P1": 20}),
		sensorFrom(3, 23.3221, 42.6979, "sensor.community", map[string]float64{"P1": 30}),
		sensorFrom(4, 23.3222, 42.6980, "eea", map[string]float64{"P1": 100}),
	}, HexResolutionKM)

	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 hex, got %d", len(p.Hexes))
	}
	h := p.Hexes[0]
	if h.N != 4 {
		t.Errorf("n = %d, want 4", h.N)
	}
	if got := h.Values["P1"]; got != 25 {
		t.Errorf("blended P1 = %v, want 25", got)
	}
	if h.Source != "" {
		t.Errorf("source = %q, want empty on a two-network bin", h.Source)
	}
	sc, ok := h.BySource["sensor.community"]
	if !ok {
		t.Fatalf("by_source has no sensor.community: %#v", h.BySource)
	}
	if sc.N != 3 || sc.Values["P1"] != 20 {
		t.Errorf("sensor.community = {n:%d P1:%v}, want {n:3 P1:20}", sc.N, sc.Values["P1"])
	}
	eea, ok := h.BySource["eea"]
	if !ok {
		t.Fatalf("by_source has no eea: %#v", h.BySource)
	}
	if eea.N != 1 || eea.Values["P1"] != 100 {
		t.Errorf("eea = {n:%d P1:%v}, want {n:1 P1:100}", eea.N, eea.Values["P1"])
	}
}

// One network in the bin: the entry names it and omits by_source, because
// `values` already IS that network's numbers and repeating them would double
// the payload of the common case.
func TestSingleNetworkBinNamesItsSourceAndOmitsBySource(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "eea", map[string]float64{"P1": 40}),
		sensorFrom(2, 23.3220, 42.6978, "eea", map[string]float64{"P1": 60}),
	}, HexResolutionKM)

	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 hex, got %d", len(p.Hexes))
	}
	h := p.Hexes[0]
	if h.Source != "eea" {
		t.Errorf("source = %q, want \"eea\"", h.Source)
	}
	if h.BySource != nil {
		t.Errorf("by_source = %#v, want nil on a one-network bin", h.BySource)
	}
	if got := h.Values["P1"]; got != 50 {
		t.Errorf("P1 = %v, want 50", got)
	}
}

// Rows written before the source column existed carry an empty Source. They are
// sensor.community, not a third network.
func TestBlankSourceCountsAsCommunity(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "", map[string]float64{"P1": 10}),
		sensorFrom(2, 23.3220, 42.6978, "eea", map[string]float64{"P1": 30}),
	}, HexResolutionKM)

	h := p.Hexes[0]
	if _, ok := h.BySource[""]; ok {
		t.Fatalf("by_source has an empty-string network: %#v", h.BySource)
	}
	sc, ok := h.BySource["sensor.community"]
	if !ok {
		t.Fatalf("by_source has no sensor.community: %#v", h.BySource)
	}
	if sc.N != 1 || sc.Values["P1"] != 10 {
		t.Errorf("sensor.community = {n:%d P1:%v}, want {n:1 P1:10}", sc.N, sc.Values["P1"])
	}
}

// A metric no sensor of a network reported is ABSENT from that network's
// values, never present as zero: 0 µg/m³ is a reading.
func TestNetworkWithoutTheMetricOmitsIt(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "sensor.community", map[string]float64{"P1": 10, "humidity": 55}),
		sensorFrom(2, 23.3220, 42.6978, "eea", map[string]float64{"P1": 30}),
	}, HexResolutionKM)

	eea := p.Hexes[0].BySource["eea"]
	if _, ok := eea.Values["humidity"]; ok {
		t.Errorf("eea values carry humidity: %#v", eea.Values)
	}
	sc := p.Hexes[0].BySource["sensor.community"]
	if got := sc.Values["humidity"]; got != 55 {
		t.Errorf("sensor.community humidity = %v, want 55", got)
	}
}

// The point tier is one sensor per entry, so it names its network and never
// carries by_source.
func TestPointEntriesNameTheirNetwork(t *testing.T) {
	pts := pointsFrom([]store.SensorReading{
		sensorFrom(7, 23.3219, 42.6977, "eea", map[string]float64{"P1": 12}),
		sensorFrom(8, 23.3220, 42.6978, "", map[string]float64{"P1": 14}),
	})

	if len(pts) != 2 {
		t.Fatalf("want 2 points, got %d", len(pts))
	}
	if pts[0].Source != "eea" {
		t.Errorf("point 7 source = %q, want \"eea\"", pts[0].Source)
	}
	if pts[1].Source != "sensor.community" {
		t.Errorf("point 8 source = %q, want \"sensor.community\"", pts[1].Source)
	}
	if pts[0].BySource != nil || pts[1].BySource != nil {
		t.Error("a point entry carries by_source")
	}
}

// Coverage counts SENSORS WITH A USABLE READING per network per metric. It is
// what the layer menu says about a metric before the reader picks it, so a
// network that reports nothing for a metric must not appear under it at all.
func TestCoverageCountsSensorsWithAReadingPerNetwork(t *testing.T) {
	cov := coverageFrom([]store.SensorReading{
		sensorFrom(1, 23.32, 42.69, "sensor.community", map[string]float64{"P1": 10, "P2": 5}),
		sensorFrom(2, 23.33, 42.70, "sensor.community", map[string]float64{"P1": 12}),
		sensorFrom(3, 24.00, 43.00, "eea", map[string]float64{"P1": 30, "O3": 60}),
		sensorFrom(4, 24.10, 43.10, "eea", map[string]float64{"O3": 55}),
	})

	if got := cov["sensor.community"]["P1"]; got != 2 {
		t.Errorf("community P1 = %d, want 2", got)
	}
	if got := cov["sensor.community"]["P2"]; got != 1 {
		t.Errorf("community P2 = %d, want 1", got)
	}
	if got := cov["eea"]["O3"]; got != 2 {
		t.Errorf("eea O3 = %d, want 2", got)
	}
	if _, ok := cov["eea"]["P2"]; ok {
		t.Errorf("eea carries a P2 entry with no eea P2 reading: %#v", cov["eea"])
	}
	if _, ok := cov[""]; ok {
		t.Errorf("coverage has an empty-string network: %#v", cov)
	}
}

// Every tier answers the same question about coverage, so the block survives
// the viewport clip. Without this a reader who has panned sees the counts
// vanish from the layer menu.
func TestClippedHexBodyKeepsCoverage(t *testing.T) {
	s := &Snapshot{
		GeneratedAt: time.Now(),
		coverage:    map[string]map[string]int{"eea": {"P2": 4}},
		hexTiers: map[float64]hexPayload{
			HexResolutionKM: {
				ResolutionKM: HexResolutionKM,
				Coverage:     map[string]map[string]int{"eea": {"P2": 4}},
				Hexes: []hexEntry{
					{Lon: 23.32, Lat: 42.69, N: 1, Values: map[string]float64{"P2": 9}},
				},
			},
		},
	}
	b, err := s.HexBody(HexResolutionKM, BBox{W: 23, S: 42, E: 24, N: 43}, true)
	if err != nil {
		t.Fatalf("HexBody: %v", err)
	}
	var got hexPayload
	if err := json.Unmarshal(b.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Coverage["eea"]["P2"] != 4 {
		t.Errorf("coverage = %#v, want eea P2 = 4", got.Coverage)
	}
}

// The point tier is served from its own builder, so it needs the block wired
// separately or the menu empties out at the deepest zoom.
func TestPointBodyCarriesCoverage(t *testing.T) {
	s := &Snapshot{
		GeneratedAt: time.Now(),
		coverage:    map[string]map[string]int{"eea": {"P1": 27}},
		points: []hexEntry{
			{Lon: 23.32, Lat: 42.69, SensorID: 1, N: 1, Source: "eea",
				Values: map[string]float64{"P1": 20}},
		},
	}
	b, err := s.PointBody(BBox{W: 23, S: 42, E: 24, N: 43})
	if err != nil {
		t.Fatalf("PointBody: %v", err)
	}
	var got hexPayload
	if err := json.Unmarshal(b.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Coverage["eea"]["P1"] != 27 {
		t.Errorf("coverage = %#v, want eea P1 = 27", got.Coverage)
	}
}
