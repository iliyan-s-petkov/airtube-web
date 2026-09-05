package snapshot

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"airbg.org/internal/store"
)

func TestSnapResolutionKMSnapsGeometrically(t *testing.T) {
	cases := []struct {
		want float64
		tier float64
	}{
		{15, 15},
		{0.25, 0.25},
		{100, 15},
		{0.001, 0.25},
		// The ratio rule, not the absolute one: 0.4 is 0.15 above 0.25 and only
		// 0.1 below 0.5, but it is a factor 1.6 above 0.25 and 1.25 below 0.5.
		{0.4, 0.5},
		{3, 2},
		// The 5/15 boundary sits at the geometric midpoint √75 ≈ 8.66, not the
		// arithmetic 10.
		{8, 5},
		{9, 15},
	}
	for _, c := range cases {
		if got := SnapResolutionKM(c.want); got != c.tier {
			t.Errorf("SnapResolutionKM(%v) = %v, want %v", c.want, got, c.tier)
		}
	}
}

func TestSnapResolutionKMRejectsNonsense(t *testing.T) {
	for _, v := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := SnapResolutionKM(v); got != HexResolutionKM {
			t.Errorf("SnapResolutionKM(%v) = %v, want the default %v", v, got, HexResolutionKM)
		}
	}
}

func TestSnapResolutionKMAlwaysLandsOnAPublishedTier(t *testing.T) {
	published := make(map[float64]bool, len(HexTiersKM))
	for _, t := range HexTiersKM {
		published[t] = true
	}
	// Swept rather than spot-checked: the guarantee the handler leans on is that
	// no input produces a resolution the snapshot has no tier for.
	for v := 0.01; v < 200; v *= 1.07 {
		if got := SnapResolutionKM(v); !published[got] {
			t.Fatalf("SnapResolutionKM(%v) = %v, which is not in HexTiersKM", v, got)
		}
	}
}

func TestParseBBox(t *testing.T) {
	b, ok := ParseBBox("22.5,41.9,23.5,42.9")
	if !ok || b != (BBox{W: 22.5, S: 41.9, E: 23.5, N: 42.9}) {
		t.Fatalf("ParseBBox = %+v, %v", b, ok)
	}
	if _, ok := ParseBBox(" 22.5 , 41.9 , 23.5 , 42.9 "); !ok {
		t.Error("ParseBBox rejected a spaced but valid box")
	}
	bad := []string{
		"", "1,2,3", "1,2,3,4,5", "a,2,3,4", "NaN,2,3,4", "Inf,2,3,4",
		// Inverted and degenerate boxes: both would filter every bin away and
		// read to a client as "no sensors here".
		"23.5,41.9,22.5,42.9", "22.5,42.9,23.5,41.9", "22.5,41.9,22.5,42.9",
	}
	for _, s := range bad {
		if _, ok := ParseBBox(s); ok {
			t.Errorf("ParseBBox(%q) accepted a bad box", s)
		}
	}
}

func TestBBoxContainsIsInclusiveOnTheEdge(t *testing.T) {
	b := BBox{W: 22, S: 41, E: 24, N: 43}
	in := [][2]float64{{23, 42}, {22, 41}, {24, 43}, {22, 43}}
	for _, p := range in {
		if !b.contains(p[0], p[1]) {
			t.Errorf("contains(%v, %v) = false, want true", p[0], p[1])
		}
	}
	out := [][2]float64{{21.99, 42}, {24.01, 42}, {23, 40.99}, {23, 43.01}}
	for _, p := range out {
		if b.contains(p[0], p[1]) {
			t.Errorf("contains(%v, %v) = true, want false", p[0], p[1])
		}
	}
}

// tierSensors spreads readings across Sofia and Burgas, far enough apart that a
// Sofia viewport must exclude every Burgas bin at any tier.
func tierSensors() []store.SensorReading {
	var out []store.SensorReading
	for i := 0; i < 12; i++ {
		f := float64(i)
		out = append(out, store.SensorReading{
			Lon: 23.30 + f*0.004, Lat: 42.68 + f*0.003,
			Country: "BG", Values: map[string]float64{"P1": 10 + f},
		})
		out = append(out, store.SensorReading{
			Lon: 27.45 + f*0.004, Lat: 42.50 + f*0.003,
			Country: "BG", Values: map[string]float64{"P1": 20 + f},
		})
	}
	return out
}

func tierSnapshot(t *testing.T) *Snapshot {
	t.Helper()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	sensors := tierSensors()
	s := &Snapshot{GeneratedAt: now, hexTiers: map[float64]hexPayload{}}
	for _, res := range HexTiersKM {
		s.hexTiers[res] = hexPayloadFrom(now, sensors, res)
	}
	b, err := encode(s.hexTiers[HexResolutionKM])
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	s.Hexes = b
	return s
}

func decodeHexBody(t *testing.T, b Body) hexPayload {
	t.Helper()
	var p hexPayload
	if err := json.Unmarshal(b.JSON, &p); err != nil {
		t.Fatalf("unmarshal hex body: %v", err)
	}
	return p
}

// Every sensor lands in exactly one bin at every tier, so the counts must add up
// to the same total no matter how finely the country is cut. This is what makes
// a coarse tier an aggregate of the data rather than a different dataset.
func TestHexTiersConserveTheSensorCount(t *testing.T) {
	s := tierSnapshot(t)
	total := len(tierSensors())
	for _, res := range HexTiersKM {
		p := decodeHexBody(t, mustHexBody(t, s, res, BBox{}, false))
		sum := 0
		for _, h := range p.Hexes {
			sum += h.N
		}
		if sum != total {
			t.Errorf("tier %v km holds %d sensors, want %d", res, sum, total)
		}
		if p.ResolutionKM != res {
			t.Errorf("tier %v km reports resolution_km %v", res, p.ResolutionKM)
		}
	}
}

// A finer tier can never merge points a coarser one separated, so bin counts
// rise monotonically as the cells shrink. Hexagons do not subdivide into
// hexagons, so this — not cell-for-cell nesting — is the relationship that holds.
func TestFinerTiersNeverHaveFewerBins(t *testing.T) {
	s := tierSnapshot(t)
	prev := 0
	for i := len(HexTiersKM) - 1; i >= 0; i-- {
		n := len(decodeHexBody(t, mustHexBody(t, s, HexTiersKM[i], BBox{}, false)).Hexes)
		if i < len(HexTiersKM)-1 && n > prev {
			t.Errorf("tier %v km has %d bins, more than the finer tier's %d",
				HexTiersKM[i], n, prev)
		}
		prev = n
	}
}

// The address-level tier must actually separate sensors a few hundred metres
// apart, or the whole dynamic-scaling feature draws one blob at every zoom.
func TestAddressTierSeparatesNeighbouringSensors(t *testing.T) {
	s := tierSnapshot(t)
	coarse := decodeHexBody(t, mustHexBody(t, s, 15, BBox{}, false))
	fine := decodeHexBody(t, mustHexBody(t, s, 0.25, BBox{}, false))
	if len(fine.Hexes) <= len(coarse.Hexes) {
		t.Fatalf("0.25 km tier has %d bins against 15 km's %d; it is not resolving",
			len(fine.Hexes), len(coarse.Hexes))
	}
}

func TestHexBodyClipsToTheViewport(t *testing.T) {
	s := tierSnapshot(t)
	sofia := BBox{W: 23.0, S: 42.4, E: 23.6, N: 42.9}
	p := decodeHexBody(t, mustHexBody(t, s, 1, sofia, true))
	if len(p.Hexes) == 0 {
		t.Fatal("Sofia viewport returned no bins")
	}
	for _, h := range p.Hexes {
		if !sofia.contains(h.Lon, h.Lat) {
			t.Errorf("bin at (%v, %v) is outside the requested viewport", h.Lon, h.Lat)
		}
	}
	whole := decodeHexBody(t, mustHexBody(t, s, 1, BBox{}, false))
	if len(p.Hexes) >= len(whole.Hexes) {
		t.Errorf("clipped body has %d bins, unclipped %d; the box did nothing",
			len(p.Hexes), len(whole.Hexes))
	}
}

// A viewport over the sea is empty, not unfiltered. Falling back to the whole
// country here would draw bins hundreds of kilometres outside the map.
func TestHexBodyEmptyViewportStaysEmpty(t *testing.T) {
	s := tierSnapshot(t)
	p := decodeHexBody(t, mustHexBody(t, s, 1, BBox{W: 10, S: 10, E: 11, N: 11}, true))
	if len(p.Hexes) != 0 {
		t.Errorf("empty viewport returned %d bins", len(p.Hexes))
	}
}

func TestHexBodyDefaultServesThePrebuiltBytes(t *testing.T) {
	s := tierSnapshot(t)
	b := mustHexBody(t, s, HexResolutionKM, BBox{}, false)
	if b.ETag != s.Hexes.ETag {
		t.Errorf("default request served ETag %q, want the prebuilt %q", b.ETag, s.Hexes.ETag)
	}
}

// Two callers asking the same question must get the same bytes, or the response
// cannot be cached publicly.
func TestHexBodyIsDeterministic(t *testing.T) {
	s := tierSnapshot(t)
	bb := BBox{W: 23.0, S: 42.4, E: 23.6, N: 42.9}
	a := mustHexBody(t, s, 0.5, bb, true)
	b := mustHexBody(t, s, 0.5, bb, true)
	if a.ETag != b.ETag || string(a.JSON) != string(b.JSON) {
		t.Error("two identical hex requests produced different bodies")
	}
}

// The snapping is HexBody's job, not the handler's. A body that answered 0.4 by
// falling through to the 15 km default would claim a resolution it did not
// serve, and the envelope would say so — which is exactly what this pins.
func TestHexBodySnapsTheRequestedResolution(t *testing.T) {
	s := tierSnapshot(t)
	cases := map[float64]float64{0.4: 0.5, 3: 2, 0.001: 0.25, 8: 5}
	for want, tier := range cases {
		p := decodeHexBody(t, mustHexBody(t, s, want, BBox{}, false))
		if p.ResolutionKM != tier {
			t.Errorf("HexBody(%v) served resolution_km %v, want the %v km tier",
				want, p.ResolutionKM, tier)
		}
	}
}

// A snapshot built before this tier map existed — or one whose build failed
// part-way — still has to answer. The default body is the fallback, never an
// error and never an empty grid.
func TestHexBodyWithNoTiersServesTheDefault(t *testing.T) {
	s := tierSnapshot(t)
	s.hexTiers = nil
	b := mustHexBody(t, s, 0.25, BBox{}, false)
	if b.ETag != s.Hexes.ETag {
		t.Error("a snapshot with no tiers did not fall back to the default body")
	}
}

func mustHexBody(t *testing.T, s *Snapshot, res float64, bb BBox, clip bool) Body {
	t.Helper()
	b, err := s.HexBody(res, bb, clip)
	if err != nil {
		t.Fatalf("HexBody(%v, %+v, %v): %v", res, bb, clip, err)
	}
	return b
}
