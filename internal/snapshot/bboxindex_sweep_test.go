package snapshot

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// sweepSeed is fixed so a sweep failure reproduces byte-for-byte on a rerun.
const sweepSeed = 20260919

// sweepEntries spans all four quadrants and includes points sitting exactly
// on BBoxQuantumDegrees lines, so bucketOf's floor-vs-truncation choice is
// live in every quadrant, not just the positive one gridEntries covers.
func sweepEntries() []hexEntry {
	var out []hexEntry
	id := int64(1)
	for lon := -10.0; lon <= 10.0; lon += 0.37 {
		for lat := -10.0; lat <= 10.0; lat += 0.41 {
			out = append(out, hexEntry{
				Lon: lon, Lat: lat, SensorID: id, N: 1,
				Country: "??", Values: map[string]float64{"P1": float64(id % 50)},
			})
			id++
		}
	}
	for _, lon := range []float64{-0.25, 0, 0.25, -5.0, 5.0} {
		for _, lat := range []float64{-0.25, 0, 0.25, -5.0, 5.0} {
			out = append(out, hexEntry{
				Lon: lon, Lat: lat, SensorID: id, N: 1,
				Country: "??", Values: map[string]float64{"P1": float64(id % 50)},
			})
			id++
		}
	}
	return out
}

// randomSweepBox draws a box from one of several categories, chosen by i mod
// the category count so a full sweep run visits every category repeatedly.
// The quantum lines referenced below are BBoxQuantumDegrees (0.25) multiples.
func randomSweepBox(rng *rand.Rand, i int) BBox {
	q := BBoxQuantumDegrees
	span := func() float64 { return rng.Float64()*4 - 2 } // [-2, 2)
	quantLine := func() float64 { return math.Floor(rng.Float64()*40-20) * q }

	switch i % 8 {
	case 0: // plain negative-region box
		w := -5 + span()
		return BBox{W: w, S: -5 + span(), E: w + rng.Float64()*3 + 0.1, N: -5 + span() + rng.Float64()*3 + 0.1}
	case 1: // zero-straddling box
		return BBox{W: -rng.Float64() * 2, S: -rng.Float64() * 2, E: rng.Float64() * 2, N: rng.Float64() * 2}
	case 2: // exactly on quantum lines
		w, s := quantLine(), quantLine()
		return BBox{W: w, S: s, E: w + q*float64(1+rng.Intn(4)), N: s + q*float64(1+rng.Intn(4))}
	case 3: // +/-1e-9 astride a quantum line
		const eps = 1e-9
		w, s := quantLine()+eps*(rng.Float64()*2-1), quantLine()+eps*(rng.Float64()*2-1)
		return BBox{W: w, S: s, E: w + q + eps*(rng.Float64()*2-1), N: s + q + eps*(rng.Float64()*2-1)}
	case 4: // degenerate: zero width, zero height, or both
		w, s := span(), span()
		e, n := w, s
		if rng.Intn(2) == 0 {
			e = w + rng.Float64()*2
		}
		if rng.Intn(2) == 0 {
			n = s + rng.Float64()*2
		}
		return BBox{W: w, S: s, E: e, N: n}
	case 5: // inverted (W>E or S>N)
		w, s := span(), span()
		return BBox{W: w + 1, S: s + 1, E: w, N: s}
	case 6: // extreme magnitude — bounded to real longitude/latitude scale.
		// clip's bucket walk is O((e0-w0)*(n0-s0)); an unbounded magnitude here
		// (e.g. 1e6) turns this case into a multi-hour hang rather than a test,
		// with no correctness signal to show for it.
		mag := rng.Float64() * 170
		return BBox{W: -mag, S: -mag, E: mag, N: mag}
	default: // ordinary interior box
		w, s := span(), span()
		return BBox{W: w, S: s, E: w + rng.Float64()*2 + 0.05, N: s + rng.Float64()*2 + 0.05}
	}
}

// TestBBoxIndexClipRandomSweep is the committed form of the ad hoc sweep this
// round's review demanded: thousands of boxes, deterministically seeded,
// checked against the naive walk. It is the guarantee the pin tests above can
// only sample; this is what actually backs the floor-not-truncation claim.
func TestBBoxIndexClipRandomSweep(t *testing.T) {
	entries := sweepEntries()
	idx := buildBBoxIndex(entries)
	rng := rand.New(rand.NewSource(sweepSeed))

	const n = 5000
	for i := 0; i < n; i++ {
		bb := randomSweepBox(rng, i)
		want := naiveClip(entries, bb)
		got := idx.clip(entries, bb)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("sweep box #%d %+v: clip diverges from the unindexed walk: want %d entries, got %d",
				i, bb, len(want), len(got))
		}
	}
}

// TestBBoxIndexClipRandomSweepEmptyIndex runs the same sweep over an index
// with no entries at all — minCol/maxCol/minRow/maxRow are never set, and
// clip must not read them as if they were.
func TestBBoxIndexClipRandomSweepEmptyIndex(t *testing.T) {
	var entries []hexEntry
	idx := buildBBoxIndex(entries)
	rng := rand.New(rand.NewSource(sweepSeed + 1))

	const n = 500
	for i := 0; i < n; i++ {
		bb := randomSweepBox(rng, i)
		want := naiveClip(entries, bb)
		got := idx.clip(entries, bb)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("empty-index sweep box #%d %+v: clip diverges from the unindexed walk: want %d entries, got %d",
				i, bb, len(want), len(got))
		}
	}
}
