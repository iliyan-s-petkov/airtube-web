package snapshot

import (
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
	})
	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 hex, got %d", len(p.Hexes))
	}
	if p.Hexes[0].N != 2 {
		t.Errorf("n = %d, want 2", p.Hexes[0].N)
	}
	if got := p.Hexes[0].Values["P1"]; got != 25 {
		t.Errorf("P1 = %v, want 25 (the mean, not a sum)", got)
	}
}

// Sofia and Varna are ~370 km apart and must never merge.
func TestDistantSensorsGetSeparateHexes(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(2, 27.9147, 43.2141, map[string]float64{"P1": 40}),
	})
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
			c := hexOf(lon, lat)
			x, y := project(lon, lat)
			cx, cy := project(hexCentre(c))
			best := math.Hypot(x-cx, y-cy)

			for dq := -2; dq <= 2; dq++ {
				for dr := -2; dr <= 2; dr++ {
					nx, ny := project(hexCentre(axial{c.q + dq, c.r + dr}))
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
	ox, oy := project(hexCentre(origin))
	neighbours := []axial{{4, -7}, {2, -7}, {3, -6}, {3, -8}, {4, -8}, {2, -6}}
	for _, n := range neighbours {
		nx, ny := project(hexCentre(n))
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
	})
	withCompany := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
		sensorAt(2, 27.9147, 43.2141, map[string]float64{"P1": 40}),
	})
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

	first, err := encode(hexPayloadFrom(time.Unix(1000, 0), a))
	if err != nil {
		t.Fatal(err)
	}
	second, err := encode(hexPayloadFrom(time.Unix(9000, 0), b))
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
	})
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
	})
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
