package snapshot

import (
	"bytes"
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
func TestBBoxIndexClipPinsFloorNotTruncate(t *testing.T) {
	entries := []hexEntry{
		{Lon: -0.9, Lat: -0.9, SensorID: 1, N: 1, Country: "??", Values: map[string]float64{"P1": 1}},
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

// The same comparison one level up, through HexBody and PointBody, so the
// wiring — not just the bucket algorithm — is covered: a hexPayload built the
// normal way carries an idx, and a Snapshot built the normal way carries a
// pointsIndex.
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
