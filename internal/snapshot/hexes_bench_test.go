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
//
// Indexed vs. the nil-index (linear) fallback on this box, at
// -benchtime=200x -count=5: roughly 2-3x, measured 2.1-3.7x across separate
// runs on the same machine — quote the range, not a point estimate; the
// spread at this sample size is bigger than the difference between any two
// of the point estimates this was revised through (79dc8ee ~5.5x, a45a509
// ~3.1-4.4x). Memory is the one figure stable enough to quote as a point:
// 2.421 -> 0.815 MB/op, 2.97x, reproduced on every run.
var benchViewport = BBox{W: 23.0, S: 42.5, E: 23.75, N: 43.0}

// A box wide enough to cover the whole of benchSensors' spread — the case
// the index gives up nothing on, and the reason clip short-circuits to a
// linear walk once a box's bucket range covers the whole index. Only the
// point tier guards against this box size in production (overview.go's
// MaxPointBBoxDegrees); a country-sized hex request takes this path.
var benchCountryViewport = BBox{W: 22.25, S: 41.0, E: 28.75, N: 44.25}

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

// The country-sized case: no bucket the index built is outside the box, so
// clip's short-circuit takes over and this should track the linear-walk
// cost rather than pay for bucket bookkeeping on top of it.
func BenchmarkHexBodyClipCountryBox(b *testing.B) {
	now := time.Now()
	sensors := benchSensors(20000)
	p := hexPayloadFrom(now, sensors, 0.25)

	s := &Snapshot{GeneratedAt: now, hexTiers: map[float64]hexPayload{0.25: p}}
	bb := benchCountryViewport

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.HexBody(0.25, bb, true); err != nil {
			b.Fatal(err)
		}
		s.bodies = nil
	}
}
