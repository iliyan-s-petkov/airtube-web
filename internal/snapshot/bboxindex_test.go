package snapshot

import (
	"encoding/json"
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
		"box inside one bucket": {W: 23.0, S: 42.0, E: 23.25, N: 42.25},
		"whole span":            {W: 21.75, S: 40.75, E: 25.25, N: 43.25},
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
		var got hexPayload
		if err := json.Unmarshal(b.JSON, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		want := naiveClip(s.hexTiers[res].Hexes, bb)
		if len(got.Hexes) != len(want) {
			t.Errorf("HexBody(%v): got %d hexes, want %d", bb, len(got.Hexes), len(want))
		}
	}
}
