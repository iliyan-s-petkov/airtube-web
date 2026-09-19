package snapshot

import (
	"bytes"
	"math"
	"reflect"
	"testing"
	"time"

	"airbg.org/internal/store"
)

// naiveClip is the walk bboxIndex.clip replaces: check every entry, in order.
// The test below holds the two to the same answer.
func naiveClip(entries []hexEntry, bb BBox) []hexEntry {
	out := make([]hexEntry, 0, len(entries))
	for _, e := range entries {
		if bb.contains(e.Lon, e.Lat) {
			out = append(out, e)
		}
	}
	return out
}

// gridEntries builds a set of hexEntry deliberately including points exactly
// on BBoxQuantumDegrees lines — the one place bboxIndex.clip needs the
// per-entry re-check — alongside ordinary interior points, over a wider span
// than any of the test boxes below.
func gridEntries() []hexEntry {
	var out []hexEntry
	id := int64(1)
	for lon := 22.0; lon <= 25.0; lon += 0.25 {
		for lat := 41.0; lat <= 43.0; lat += 0.25 {
			for _, dLon := range []float64{0, 0.001, 0.249, -0.001} {
				for _, dLat := range []float64{0, 0.001, 0.249, -0.001} {
					out = append(out, hexEntry{
						Lon: lon + dLon, Lat: lat + dLat, SensorID: id, N: 1,
						Country: "BG", Values: map[string]float64{"P1": float64(id % 50)},
					})
					id++
				}
			}
		}
	}
	return out
}

func TestBBoxIndexClipMatchesUnindexedWalk(t *testing.T) {
	entries := gridEntries()
	idx := buildBBoxIndex(entries)

	boxes := map[string]BBox{
		"empty region":          {W: 40.0, S: 40.0, E: 40.25, N: 40.25},
		"box on bucket bound":   {W: 23.0, S: 42.0, E: 23.25, N: 42.25},
		"box inside one bucket": {W: 24.0, S: 41.5, E: 24.25, N: 41.75},
		"whole span":            {W: 21.75, S: 40.75, E: 25.25, N: 43.25},
		// Not on the BBoxQuantumDegrees grid on any edge — the case clip must
		// still get right without a prior Quantise().
		"non-quantised box": {W: 22.3, S: 41.3, E: 22.8, N: 41.8},
	}
	for name, bb := range boxes {
		t.Run(name, func(t *testing.T) {
			want := naiveClip(entries, bb)
			got := idx.clip(entries, bb)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("clip(%v) diverges from the unindexed walk: want %d entries, got %d",
					bb, len(want), len(got))
			}
		})
	}
}

// TestBBoxIndexClipPinsFloorNotTruncate pins bucketOf's use of math.Floor
// (hexes.go:168) against plain truncation. Bulgaria is all-positive, so
// every other fixture in this file passes under either — this is the one
// case a negative coordinate is load-bearing.
//
// The entry sits just outside the box's west/south edge, in the bucket that
// Floor assigns it (an edge bucket, always re-checked, so it is correctly
// excluded). Truncation shifts a negative, non-integer quotient one bucket
// towards zero, landing this entry in an INTERIOR bucket instead — one
// clip takes whole, with no per-entry contains() check — so a truncating
// bucketOf would wrongly include it.
//
// A second, distant entry is load-bearing too: with only the one entry near
// the box, minCol/maxCol/minRow/maxRow collapse onto it and the box covers
// the whole index under EITHER keying function, so clip's short-circuit
// (hexes.go) sends every case through clipLinear and never reaches the
// bucket walk this test exists to exercise — a45a509 did exactly that to an
// earlier version of this fixture. The distant entry keeps the index wider
// than the box, so the short-circuit does not fire here.
func TestBBoxIndexClipPinsFloorNotTruncate(t *testing.T) {
	entries := []hexEntry{
		{Lon: -0.9, Lat: -0.9, SensorID: 1, N: 1, Country: "??", Values: map[string]float64{"P1": 1}},
		{Lon: 10.0, Lat: 10.0, SensorID: 2, N: 1, Country: "??", Values: map[string]float64{"P1": 1}},
	}
	bb := BBox{W: -0.8, S: -0.8, E: -0.25, N: -0.25}
	idx := buildBBoxIndex(entries)

	want := naiveClip(entries, bb)
	got := idx.clip(entries, bb)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("clip(%v) diverges from the unindexed walk: want %d entries, got %d",
			bb, len(want), len(got))
	}
}

// The same comparison one level up: a hexPayload built the normal way carries
// an idx. The point tier here is compared against an index this test builds
// itself, so it says nothing about what buildHexes assigns — see
// TestBuiltSnapshotPointBodyMatchesUnindexedWalk for that wiring.
func TestClippedBodiesMatchUnindexedWalk(t *testing.T) {
	now := time.Now()
	var sensors []store.SensorReading
	id := int64(1)
	for lon := 22.0; lon <= 25.0; lon += 0.3 {
		for lat := 41.0; lat <= 43.0; lat += 0.3 {
			sensors = append(sensors, sensorAt(id, lon, lat, map[string]float64{"P1": float64(id % 50)}))
			id++
		}
	}
	// A few points sitting exactly on bucket lines, to exercise the same edge
	// case at this level.
	for _, lon := range []float64{23.0, 23.25} {
		for _, lat := range []float64{42.0, 42.25} {
			sensors = append(sensors, sensorAt(id, lon, lat, map[string]float64{"P1": 10}))
			id++
		}
	}

	p := hexPayloadFrom(now, sensors, HexTiersKM[len(HexTiersKM)-1]) // finest tier
	pts := pointsFrom(sensors)

	boxes := []BBox{
		{W: 40.0, S: 40.0, E: 40.25, N: 40.25},   // empty region
		{W: 23.0, S: 42.0, E: 23.25, N: 42.25},   // on a bucket boundary / inside one bucket
		{W: 21.75, S: 40.75, E: 25.25, N: 43.25}, // whole span
	}

	for _, bb := range boxes {
		wantHexes := naiveClip(p.Hexes, bb)
		gotHexes := p.idx.clip(p.Hexes, bb)
		if !reflect.DeepEqual(wantHexes, gotHexes) {
			t.Errorf("hex clip(%v): indexed walk diverges from unindexed", bb)
		}

		wantPts := naiveClip(pts, bb)
		idx := buildBBoxIndex(pts)
		gotPts := idx.clip(pts, bb)
		if !reflect.DeepEqual(wantPts, gotPts) {
			t.Errorf("point clip(%v): indexed walk diverges from unindexed", bb)
		}
	}
}

// Byte-identical, not just field-equal: HexBody's own output must not change
// shape now that the clip is indexed.
func TestHexBodyClipIsByteIdenticalAcrossBoxes(t *testing.T) {
	now := time.Now()
	var sensors []store.SensorReading
	id := int64(1)
	for lon := 22.0; lon <= 25.0; lon += 0.3 {
		for lat := 41.0; lat <= 43.0; lat += 0.3 {
			sensors = append(sensors, sensorAt(id, lon, lat, map[string]float64{"P1": float64(id % 50)}))
			id++
		}
	}
	s := &Snapshot{GeneratedAt: now, hexTiers: map[float64]hexPayload{}}
	for _, res := range HexTiersKM {
		s.hexTiers[res] = hexPayloadFrom(now, sensors, res)
	}

	res := HexTiersKM[len(HexTiersKM)-1]
	boxes := []BBox{
		{W: 40.0, S: 40.0, E: 40.25, N: 40.25},
		{W: 23.0, S: 42.0, E: 23.25, N: 42.25},
		{W: 21.75, S: 40.75, E: 25.25, N: 43.25},
	}
	for _, bb := range boxes {
		b, err := s.HexBody(res, bb, true)
		if err != nil {
			t.Fatalf("HexBody(%v): %v", bb, err)
		}
		tier := s.hexTiers[res]
		want, err := encode(hexPayload{
			GeneratedAt: tier.GeneratedAt, ResolutionKM: tier.ResolutionKM,
			Coverage: tier.Coverage, Hexes: naiveClip(tier.Hexes, bb),
		})
		if err != nil {
			t.Fatalf("encode(naiveClip(%v)): %v", bb, err)
		}
		if !bytes.Equal(b.JSON, want.JSON) {
			t.Errorf("HexBody(%v): JSON bytes diverge from the unindexed walk's own encode", bb)
		}
	}
}

// TestBBoxIndexShortCircuitTakesTheRightPath fences the "never maintain
// minCol/maxCol/minRow/maxRow" mutation clip's short-circuit (hexes.go) is
// exposed to. It deliberately does NOT call idx.clip: clipLinear and the
// bucket walk are proven equal on every input by the sweep test, so no box
// exists for which clip's own return value reveals which path it took — the
// only way to observe the routing decision is to read the index's bucket
// bounds directly, which is what this does.
//
// nearCorner is chosen so the mutation is provably observable, not merely
// plausible: gridEntries' first entry sits at (22.0, 41.0), bucket (88, 164),
// which is close to but not exactly the grid's real corner bucket (87, 163)
// — a lower-left duplicate exists at (21.999, 40.999). A build that stops
// updating minCol/maxCol/minRow/maxRow after the first entry leaves the index
// believing its bounds are the single bucket (88, 88, 164, 164) forever.
// nearCorner's own bucket range is w0=88, e0=90, s0=164, n0=165 — under the
// REAL bounds (87, 100, 163, 172) w0<=minCol fails (88 <= 87 is false), so
// the box must take the bucket walk; under the STUCK bounds every inequality
// happens to hold (88<=88, 90>=88, 164<=164, 165>=164), so the mutation
// misroutes it to clipLinear instead. The two boxes originally used here
// (a viewport well inside the span, and one covering the whole span) turned
// out not to test this: both their real answers happened to survive the
// stuck values by coincidence, so a corrupted index passed silently. This
// box does not have that problem.
//
// The mutation that forces the short-circuit itself always on (rather than
// corrupting what it reads) is a different matter: it changes a condition
// inside clip, not the index's stored bounds, and clipLinear/the bucket walk
// are proven to return byte-identical results for every input (this file's
// TestHexBodyClipIsByteIdenticalAcrossBoxes, and the sweep). No box, and no
// assertion reachable through clip's public behaviour, can distinguish
// "took the short-circuit" from "took the bucket walk and got the same
// answer" — that mutation is not pinned here, or anywhere in this package,
// without adding an execution-path hook to clip itself.
func TestBBoxIndexShortCircuitTakesTheRightPath(t *testing.T) {
	entries := gridEntries()
	idx := buildBBoxIndex(entries)
	const q = BBoxQuantumDegrees
	shortCircuits := func(bb BBox) bool {
		w0 := int(math.Floor(bb.W / q))
		e0 := int(math.Floor(bb.E / q))
		s0 := int(math.Floor(bb.S / q))
		n0 := int(math.Floor(bb.N / q))
		return w0 <= idx.minCol && e0 >= idx.maxCol && s0 <= idx.minRow && n0 >= idx.maxRow
	}

	viewport := BBox{W: 23.0, S: 42.0, E: 23.25, N: 42.25} // one bucket, well inside gridEntries' span
	if shortCircuits(viewport) {
		t.Fatalf("viewport box %v takes clipLinear; want the bucket walk (index bounds col[%d,%d] row[%d,%d])",
			viewport, idx.minCol, idx.maxCol, idx.minRow, idx.maxRow)
	}

	// See the nearCorner comment above: this is the box that actually kills
	// the stuck-bounds mutation, where the other two do not.
	nearCorner := BBox{W: 22.1, S: 41.1, E: 22.6, N: 41.4}
	if shortCircuits(nearCorner) {
		t.Fatalf("near-corner box %v takes clipLinear; want the bucket walk (index bounds col[%d,%d] row[%d,%d])",
			nearCorner, idx.minCol, idx.maxCol, idx.minRow, idx.maxRow)
	}

	whole := BBox{W: 21.75, S: 40.75, E: 25.25, N: 43.25} // covers gridEntries' whole span
	if !shortCircuits(whole) {
		t.Fatalf("country-sized box %v takes the bucket walk; want clipLinear (index bounds col[%d,%d] row[%d,%d])",
			whole, idx.minCol, idx.maxCol, idx.minRow, idx.maxRow)
	}
}

// The wiring TestClippedBodiesMatchUnindexedWalk does not reach: it builds its
// own index beside the payload, so buildHexes' assignment of snap.pointsIndex
// is unpinned and PointBody can be served an index of the wrong entries.
// Proved by mutation: buildBBoxIndex(snap.points[:0]) makes PointBody return no
// sensors for any viewport, and without this the whole package stays green.
func TestBuiltSnapshotPointBodyMatchesUnindexedWalk(t *testing.T) {
	now := time.Now()
	var sensors []store.SensorReading
	id := int64(1)
	for lon := 22.0; lon <= 25.0; lon += 0.3 {
		for lat := 41.0; lat <= 43.0; lat += 0.3 {
			sensors = append(sensors, sensorAt(id, lon, lat, map[string]float64{"P1": float64(id % 50)}))
			id++
		}
	}

	indexed := &Snapshot{GeneratedAt: now}
	if err := buildHexes(indexed, sensors, now); err != nil {
		t.Fatalf("buildHexes: %v", err)
	}
	if indexed.pointsIndex == nil {
		t.Fatal("buildHexes left pointsIndex nil: PointBody would silently take the linear fallback")
	}
	// Same points, no index, so PointBody takes clipLinear — the walk the
	// index replaces, compared here through the real body encoder.
	linear := &Snapshot{GeneratedAt: now, coverage: indexed.coverage, points: indexed.points}

	for _, bb := range []BBox{
		{W: 40.0, S: 40.0, E: 40.25, N: 40.25},
		{W: 23.0, S: 42.0, E: 23.25, N: 42.25},
		{W: 21.75, S: 40.75, E: 25.25, N: 43.25},
	} {
		want, err := linear.PointBody(bb)
		if err != nil {
			t.Fatalf("PointBody without an index: %v", err)
		}
		got, err := indexed.PointBody(bb)
		if err != nil {
			t.Fatalf("PointBody: %v", err)
		}
		if !bytes.Equal(want.JSON, got.JSON) {
			t.Errorf("PointBody(%v) on a built snapshot diverges from the unindexed walk", bb)
		}
	}
}
