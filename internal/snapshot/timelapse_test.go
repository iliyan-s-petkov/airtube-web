package snapshot

import (
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
	return hourCells{bucket: t, cells: cells}
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

	p := timelapseFrom(end, "P2", spec, ring, end)

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

	p := timelapseFrom(end, "P2", spec, ring, end)

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

	p := timelapseFrom(end, "P2", FrameSpecs[0], ring, end)

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

	p := timelapseFrom(end, "P2", spec, ring, end)

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

	p := timelapseFrom(end, "P2", spec, ring, end)

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
	if len(got[0].cells) != 1 {
		t.Fatalf("cells = %d, want 1: three sensors metres apart share a 15 km bin", len(got[0].cells))
	}
	for _, v := range got[0].cells {
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
