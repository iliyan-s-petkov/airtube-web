package snapshot

import (
	"math"
	"testing"
	"time"

	"airbg.org/internal/store"
)

func TestKnownSpanAcceptsOnlyThePublishedSpans(t *testing.T) {
	for _, name := range []string{"24h", "7d"} {
		if !KnownSpan(name) {
			t.Errorf("KnownSpan(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "48h", "1h", "365d", "24H"} {
		if KnownSpan(name) {
			t.Errorf("KnownSpan(%q) = true; the span list is closed", name)
		}
	}
}

func hourly(t time.Time, cells map[axial]float64) hourCells {
	return hourCells{bucket: t, tiers: map[float64]map[axial]float64{HexResolutionKM: cells}}
}

// The whole reason the wire shape is what it is: geometry once, numbers per
// frame. A frame's array is positional against the cell list, so a cell that
// only reports halfway through must still hold its index in the earlier frames.
func TestTimelapseFramesArePositionalAgainstOneCellList(t *testing.T) {
	end := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a, b := hexOf(23.0, 42.0, HexResolutionKM), hexOf(27.0, 43.0, HexResolutionKM)
	spec := FrameSpec{Name: "3h", Step: time.Hour, Dur: 3 * time.Hour}
	ring := &frameRing{hours: []hourCells{
		hourly(end.Add(-3*time.Hour), map[axial]float64{a: 10}),
		hourly(end.Add(-2*time.Hour), map[axial]float64{a: 20, b: 5}),
		hourly(end.Add(-1*time.Hour), map[axial]float64{b: 6}),
	}}

	p := timelapseFrom(end, "P2", spec, ring, end, HexResolutionKM)

	if len(p.Cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(p.Cells))
	}
	if len(p.Frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(p.Frames))
	}
	for i, f := range p.Frames {
		if len(f.V) != len(p.Cells) {
			t.Fatalf("frame %d has %d values for %d cells", i, len(f.V), len(p.Cells))
		}
	}
	// Whichever index cell a landed on, it is the same index in every frame.
	ai := 0
	if p.Frames[0].V[1] != nil {
		ai = 1
	}
	if p.Frames[0].V[ai] == nil || *p.Frames[0].V[ai] != 10 {
		t.Errorf("frame 0 cell a = %v, want 10", p.Frames[0].V[ai])
	}
	if p.Frames[1].V[ai] == nil || *p.Frames[1].V[ai] != 20 {
		t.Errorf("frame 1 cell a = %v, want 20", p.Frames[1].V[ai])
	}
	if p.Frames[2].V[ai] != nil {
		t.Errorf("frame 2 cell a = %v, want null: it stopped reporting", *p.Frames[2].V[ai])
	}
}

// 0 µg/m³ is a reading and an absent cell is not. Sent as the same JSON value
// they would draw as the same colour.
func TestTimelapseDistinguishesZeroFromAbsent(t *testing.T) {
	end := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a := hexOf(23.0, 42.0, HexResolutionKM)
	spec := FrameSpec{Name: "2h", Step: time.Hour, Dur: 2 * time.Hour}
	ring := &frameRing{hours: []hourCells{
		hourly(end.Add(-2*time.Hour), map[axial]float64{a: 0}),
	}}

	p := timelapseFrom(end, "P2", spec, ring, end, HexResolutionKM)

	if p.Frames[0].V[0] == nil || *p.Frames[0].V[0] != 0 {
		t.Errorf("reported zero = %v, want 0 and not null", p.Frames[0].V[0])
	}
	if p.Frames[1].V[0] != nil {
		t.Errorf("unreported hour = %v, want null", *p.Frames[1].V[0])
	}
}

// Every span is fully tiled, including the hours nothing reported in. A player
// that received only the frames with data would run the animation at a speed
// that varied with sensor uptime.
func TestTimelapseEmitsAFrameForEveryStep(t *testing.T) {
	end := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	ring := &frameRing{hours: []hourCells{
		hourly(end.Add(-time.Hour), map[axial]float64{hexOf(23.0, 42.0, HexResolutionKM): 3}),
	}}

	p := timelapseFrom(end, "P2", FrameSpecs[0], ring, end, HexResolutionKM)

	if len(p.Frames) != 24 {
		t.Fatalf("frames = %d, want 24", len(p.Frames))
	}
	if !p.Frames[0].T.Equal(end.Add(-24 * time.Hour)) {
		t.Errorf("first frame at %v, want %v", p.Frames[0].T, end.Add(-24*time.Hour))
	}
	if !p.Frames[23].T.Equal(end.Add(-time.Hour)) {
		t.Errorf("last frame at %v, want the hour before end", p.Frames[23].T)
	}
	if p.Frames[23].V[0] == nil || *p.Frames[23].V[0] != 3 {
		t.Errorf("the one reported hour landed in frame %v, not the last", p.Frames[23].V[0])
	}
}

// A six-hourly frame is the median of the hours in it, per cell — the same
// statistic an hourly cell carries, so widening the span does not change what a
// cell means.
func TestTimelapseFoldsHoursIntoACoarserStep(t *testing.T) {
	end := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a := hexOf(23.0, 42.0, HexResolutionKM)
	spec := FrameSpec{Name: "12h", Step: 6 * time.Hour, Dur: 12 * time.Hour}
	var hours []hourCells
	for i := 1; i <= 6; i++ {
		hours = append(hours, hourly(end.Add(-time.Duration(i)*time.Hour), map[axial]float64{a: float64(i)}))
	}
	ring := &frameRing{hours: hours}

	p := timelapseFrom(end, "P2", spec, ring, end, HexResolutionKM)

	if len(p.Frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(p.Frames))
	}
	// 1..6 in the second half of the span; median 3.5.
	if p.Frames[1].V[0] == nil || *p.Frames[1].V[0] != 3.5 {
		t.Errorf("folded value = %v, want the median 3.5", p.Frames[1].V[0])
	}
	if p.Frames[0].V[0] != nil {
		t.Errorf("first half = %v, want null: nothing reported there", *p.Frames[0].V[0])
	}
}

// Hours older than the span are not in it. Without this the ring's whole week
// would land in the 24h animation's first frame.
func TestTimelapseIgnoresHoursOlderThanTheSpan(t *testing.T) {
	end := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	a := hexOf(23.0, 42.0, HexResolutionKM)
	spec := FrameSpec{Name: "2h", Step: time.Hour, Dur: 2 * time.Hour}
	ring := &frameRing{hours: []hourCells{
		hourly(end.Add(-50*time.Hour), map[axial]float64{a: 99}),
	}}

	p := timelapseFrom(end, "P2", spec, ring, end, HexResolutionKM)

	if len(p.Cells) != 0 {
		t.Fatalf("cells = %d, want none: the only hour is outside the span", len(p.Cells))
	}
	for i, f := range p.Frames {
		if len(f.V) != 0 {
			t.Errorf("frame %d carries %d values against an empty cell list", i, len(f.V))
		}
	}
}

// The bin is the median of the sensors in it, the same rule the live grid uses,
// so a cell says the same kind of thing whether it is drawn now or replayed.
func TestFoldHoursTakesTheMedianOfTheSensorsInACell(t *testing.T) {
	b := time.Date(2026, 9, 8, 7, 0, 0, 0, time.UTC)
	got := foldHours([]store.FrameReading{
		{Bucket: b, Lon: 23.0, Lat: 42.0, Value: 10},
		{Bucket: b, Lon: 23.001, Lat: 42.001, Value: 20},
		{Bucket: b, Lon: 23.002, Lat: 42.002, Value: 60},
	})

	if len(got) != 1 {
		t.Fatalf("hours = %d, want 1", len(got))
	}
	if len(got[0].tiers[HexResolutionKM]) != 1 {
		t.Fatalf("cells = %d, want 1: three sensors metres apart share a 15 km bin",
			len(got[0].tiers[HexResolutionKM]))
	}
	for _, v := range got[0].tiers[HexResolutionKM] {
		if v != 20 {
			t.Errorf("cell = %v, want the median 20", v)
		}
	}
}

// extendRing resumes from the last hour it has, which only works if the folded
// hours come back in bucket order regardless of how the rows arrived.
func TestFoldHoursReturnsHoursInBucketOrder(t *testing.T) {
	b := time.Date(2026, 9, 8, 7, 0, 0, 0, time.UTC)
	got := foldHours([]store.FrameReading{
		{Bucket: b.Add(2 * time.Hour), Lon: 23, Lat: 42, Value: 3},
		{Bucket: b, Lon: 23, Lat: 42, Value: 1},
		{Bucket: b.Add(time.Hour), Lon: 23, Lat: 42, Value: 2},
	})

	if len(got) != 3 {
		t.Fatalf("hours = %d, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i-1].bucket.Before(got[i].bucket) {
			t.Fatalf("hour %d at %v is not after %v", i, got[i].bucket, got[i-1].bucket)
		}
	}
}

func TestRingHoldsTheLongestSpanAndNoMore(t *testing.T) {
	want := time.Duration(0)
	for _, s := range FrameSpecs {
		if s.Dur > want {
			want = s.Dur
		}
	}
	if got := ringDur(); got != want {
		t.Errorf("ringDur = %v, want the longest span %v", got, want)
	}
}

// The replay tiers are a subset of the live grid's, and every one of them is a
// tier the live grid also publishes — a replay at a size the map cannot draw
// live would shrink the cells the moment play is pressed.
func TestTimelapseTiersAreAPublishedSubsetOfTheHexTiers(t *testing.T) {
	hex := make(map[float64]bool, len(HexTiersKM))
	for _, h := range HexTiersKM {
		hex[h] = true
	}
	for _, r := range TimelapseTiersKM {
		if !hex[r] {
			t.Errorf("timelapse tier %v km is not a published hex tier", r)
		}
	}
	if len(TimelapseTiersKM) >= len(HexTiersKM) {
		t.Errorf("timelapse publishes %d tiers against the grid's %d; the fine tiers are deliberately withheld",
			len(TimelapseTiersKM), len(HexTiersKM))
	}
	for _, fine := range []float64{2, 1, 0.5, 0.25} {
		for _, r := range TimelapseTiersKM {
			if r == fine {
				t.Errorf("tier %v km is published; a nationwide replay at that size is too large", fine)
			}
		}
	}
}

func TestSnapTimelapseKMAlwaysLandsOnAPublishedTier(t *testing.T) {
	published := make(map[float64]bool, len(TimelapseTiersKM))
	for _, r := range TimelapseTiersKM {
		published[r] = true
	}
	for v := 0.01; v < 2000; v *= 1.07 {
		if got := SnapTimelapseKM(v); !published[got] {
			t.Fatalf("SnapTimelapseKM(%v) = %v, which is not in TimelapseTiersKM", v, got)
		}
	}
	// A tier the grid publishes but the replay does not snaps onto the finest
	// one published here rather than being served at a size we never built.
	for _, v := range []float64{2, 1, 0.5, 0.25} {
		if got := SnapTimelapseKM(v); got != 5 {
			t.Errorf("SnapTimelapseKM(%v) = %v, want the finest published tier 5", v, got)
		}
	}
	for _, v := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := SnapTimelapseKM(v); got != HexResolutionKM {
			t.Errorf("SnapTimelapseKM(%v) = %v, want the default %v", v, got, HexResolutionKM)
		}
	}
}

// A coarse cell is folded from the sensors themselves, not from the finer
// cells' medians: a bin holding one sensor must not count for as much as a bin
// holding three.
func TestFoldHoursFoldsEachTierFromTheRawReadings(t *testing.T) {
	b := time.Date(2026, 9, 8, 7, 0, 0, 0, time.UTC)
	lone, crowd := 23.0, 23.5
	if hexOf(lone, 42.0, 15) == hexOf(crowd, 42.0, 15) {
		t.Fatal("fixture: the two sites must fall in different 15 km bins")
	}
	if hexOf(lone, 42.0, 100) != hexOf(crowd, 42.0, 100) {
		t.Fatal("fixture: the two sites must share one 100 km bin")
	}
	got := foldHours([]store.FrameReading{
		{Bucket: b, Lon: lone, Lat: 42.0, Value: 100},
		{Bucket: b, Lon: crowd, Lat: 42.0, Value: 10},
		{Bucket: b, Lon: crowd + 0.001, Lat: 42.0, Value: 10},
		{Bucket: b, Lon: crowd + 0.002, Lat: 42.0, Value: 10},
	})

	if len(got) != 1 {
		t.Fatalf("hours = %d, want 1", len(got))
	}
	coarse := got[0].tiers[100]
	if len(coarse) != 1 {
		t.Fatalf("100 km cells = %d, want 1", len(coarse))
	}
	for _, v := range coarse {
		// Four readings: 10, 10, 10, 100 — median 10. Folding the 15 km medians
		// instead would give the midpoint of 10 and 100.
		if v != 10 {
			t.Errorf("100 km cell = %v, want the median of the four readings, 10", v)
		}
	}
	if len(got[0].tiers) != len(TimelapseTiersKM) {
		t.Errorf("hour folded at %d tiers, want %d", len(got[0].tiers), len(TimelapseTiersKM))
	}
}

// The payload names the size it was cut at, because the client builds the hex
// geometry from that number and nothing else.
func TestTimelapseFromReportsTheTierItWasCutAt(t *testing.T) {
	end := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	spec := FrameSpec{Name: "2h", Step: time.Hour, Dur: 2 * time.Hour}
	ring := &frameRing{hours: []hourCells{{
		bucket: end.Add(-time.Hour),
		tiers: map[float64]map[axial]float64{
			100: {hexOf(23.0, 42.0, 100): 7},
			15:  {hexOf(23.0, 42.0, 15): 9},
		},
	}}}

	for _, res := range []float64{100, 15} {
		p := timelapseFrom(end, "P2", spec, ring, end, res)
		if p.ResolutionKM != res {
			t.Errorf("cut at %v km, payload says %v", res, p.ResolutionKM)
		}
		if len(p.Cells) != 1 {
			t.Fatalf("tier %v km: cells = %d, want 1", res, len(p.Cells))
		}
	}
	coarse := timelapseFrom(end, "P2", spec, ring, end, 100)
	fine := timelapseFrom(end, "P2", spec, ring, end, 15)
	if coarse.Cells[0] == fine.Cells[0] {
		t.Error("both tiers put the cell centre in the same place; the geometry is not tier-dependent")
	}
	// Differing from each other is not enough: two tiers' axial coordinates
	// differ anyway, so centres placed at the wrong tier would still differ.
	// The client builds its hexagons around these points using the payload's
	// own resolution_km, so each must be that tier's true centre.
	for _, tc := range []struct {
		res float64
		got [2]float64
	}{{100, coarse.Cells[0]}, {15, fine.Cells[0]}} {
		lon, lat := hexCentre(hexOf(23.0, 42.0, tc.res), tc.res)
		if want := [2]float64{round4(lon), round4(lat)}; tc.got != want {
			t.Errorf("tier %v km: centre %v, want %v", tc.res, tc.got, want)
		}
	}
	if *coarse.Frames[1].V[0] != 7 || *fine.Frames[1].V[0] != 9 {
		t.Error("a tier served another tier's values")
	}
}

// TimelapseBody owns the snapping, the way HexBody does: a handler that forgot
// would ask for a key no build ever wrote and get a 503 instead of a map.
func TestTimelapseBodySnapsTheRequestedResolution(t *testing.T) {
	s := &Snapshot{Timelapse: map[string]Body{}}
	for _, res := range TimelapseTiersKM {
		s.Timelapse[timelapseKey("P2", "24h", res)] = Body{ETag: `"` + formatTier(res) + `"`}
	}
	cases := map[float64]float64{15: 15, 100: 100, 5: 5, 0.25: 5, 2: 5, 4000: 100, 0: 15, -1: 15}
	for want, tier := range cases {
		b, ok := s.TimelapseBody("P2", "24h", want)
		if !ok {
			t.Fatalf("TimelapseBody(%v) found no body", want)
		}
		if b.ETag != `"`+formatTier(tier)+`"` {
			t.Errorf("TimelapseBody(%v) served %s, want the %v km tier", want, b.ETag, tier)
		}
	}
}
