package snapshot

import (
	"testing"
	"time"

	"airbg.org/internal/store"
)

// sensorIn is sensorAt with a country code, for the cross-border cases.
func sensorIn(id int64, lon, lat float64, country string, values map[string]float64) store.SensorReading {
	sr := sensorAt(id, lon, lat, values)
	sr.Country = country
	return sr
}

// A 15 km bin can straddle a border, and the entry has one country field. The
// modal value names the country most of the bin's data actually came from —
// which is the only honest answer a single field can give.
func TestStraddlingBinTakesTheMajorityCountry(t *testing.T) {
	// One coordinate, so all four land in the same bin regardless of where the
	// real border runs; this test is about the vote, not about geography.
	const lon, lat = 22.9, 41.4
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorIn(1, lon, lat, "BG", map[string]float64{"P1": 10}),
		sensorIn(2, lon, lat, "BG", map[string]float64{"P1": 10}),
		sensorIn(3, lon, lat, "BG", map[string]float64{"P1": 10}),
		sensorIn(4, lon, lat, "GR", map[string]float64{"P1": 10}),
	})
	if len(p.Hexes) != 1 {
		t.Fatalf("got %d hexes, want 1", len(p.Hexes))
	}
	if got := p.Hexes[0].Country; got != "BG" {
		t.Errorf("country = %q, want %q — three of four sensors are BG", got, "BG")
	}
	if p.Hexes[0].N != 4 {
		t.Errorf("n = %d, want 4 — the minority sensors still count toward the bin", p.Hexes[0].N)
	}
}

// A tie must not be broken by map iteration order. The ETag is a hash of the
// payload, so a bin that flipped country between builds would invalidate every
// cached copy of the grid on a coin toss.
func TestTiedBinPicksTheSameCountryEveryTime(t *testing.T) {
	const lon, lat = 22.9, 41.4
	sensors := []store.SensorReading{
		sensorIn(1, lon, lat, "BG", map[string]float64{"P1": 10}),
		sensorIn(2, lon, lat, "GR", map[string]float64{"P1": 10}),
		sensorIn(3, lon, lat, "MK", map[string]float64{"P1": 10}),
	}
	first := hexPayloadFrom(time.Now(), sensors).Hexes[0].Country
	for i := 0; i < 50; i++ {
		if got := hexPayloadFrom(time.Now(), sensors).Hexes[0].Country; got != first {
			t.Fatalf("country = %q on build %d, %q on the first — a tie must not depend on map order", got, i, first)
		}
	}
	if first != "BG" {
		t.Errorf("country = %q, want %q — ties break on the lowest code", first, "BG")
	}
}

// Sensors stored before the country_code column existed have no code. The bin
// says so explicitly rather than emitting "", which a client would have to
// guess the meaning of.
func TestBinWithNoKnownCountryIsMarkedUnknown(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorAt(1, 23.3219, 42.6977, map[string]float64{"P1": 20}),
	})
	if got := p.Hexes[0].Country; got != hexCountryUnknown {
		t.Errorf("country = %q, want %q", got, hexCountryUnknown)
	}
}

// Foreign sensors are the whole reason the grid exists at this tier: they carry
// no area membership, so the hex payload is the only place they appear.
func TestForeignSensorsGetTheirOwnBins(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorIn(1, 23.3327, 42.6957, "BG", map[string]float64{"P1": 20}), // Sofia
		sensorIn(2, 21.4254, 41.9981, "MK", map[string]float64{"P1": 40}), // Skopje
	})
	if len(p.Hexes) != 2 {
		t.Fatalf("got %d hexes, want 2 — Sofia and Skopje are ~170 km apart", len(p.Hexes))
	}
	seen := map[string]bool{}
	for _, h := range p.Hexes {
		seen[h.Country] = true
	}
	for _, want := range []string{"BG", "MK"} {
		if !seen[want] {
			t.Errorf("no bin reported country %q; got %v", want, seen)
		}
	}
}
