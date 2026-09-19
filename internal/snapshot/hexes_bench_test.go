package snapshot

import (
	"testing"
	"time"

	"airbg.org/internal/store"
)

// benchSensors spreads sensors across roughly Bulgaria's extent at a density
// fine enough to make the finest hex tier (250 m) and the point tier both
// carry many thousands of entries — the case the viewport index exists for.
func benchSensors(n int) []store.SensorReading {
	const (
		w, e  = 22.36, 28.61
		s, n2 = 41.23, 44.23
	)
	out := make([]store.SensorReading, n)
	for i := 0; i < n; i++ {
		// A low-discrepancy-ish spread rather than a tight grid, so entries do
		// not all land on tidy bucket lines.
		fLon := float64(i%317) / 317.0
		fLat := float64((i/317)%251) / 251.0
		lon := w + fLon*(e-w)
		lat := s + fLat*(n2-s)
		out[i] = sensorAt(int64(i+1), lon, lat, map[string]float64{"P1": float64(i%80) + 1})
	}
	return out
}

// A small viewport near Sofia — the common case a pan-and-zoom map asks for,
// already on the BBoxQuantumDegrees grid as the API layer's Quantise would
// leave it.
var benchViewport = BBox{W: 23.0, S: 42.5, E: 23.75, N: 43.0}

func BenchmarkHexBodyClip(b *testing.B) {
	now := time.Now()
	sensors := benchSensors(20000)
	p := hexPayloadFrom(now, sensors, 0.25) // finest published tier

	s := &Snapshot{GeneratedAt: now, hexTiers: map[float64]hexPayload{0.25: p}}
	bb := benchViewport

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.HexBody(0.25, bb, true); err != nil {
			b.Fatal(err)
		}
		// Each call would otherwise hit the memoised body from the second
		// iteration on; a fresh cache keeps the benchmark measuring the clip
		// walk itself rather than one encode plus N map hits.
		s.bodies = nil
	}
}

func BenchmarkPointBodyClip(b *testing.B) {
	now := time.Now()
	sensors := benchSensors(20000)
	pts := pointsFrom(sensors)

	s := &Snapshot{GeneratedAt: now, points: pts, pointsIndex: buildBBoxIndex(pts)}
	bb := benchViewport

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.PointBody(bb); err != nil {
			b.Fatal(err)
		}
		s.bodies = nil
	}
}
