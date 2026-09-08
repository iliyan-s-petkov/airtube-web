package snapshot

import (
	"context"
	"fmt"
	"sort"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// A timelapse answers the question neither the live view nor a window can: not
// what the air is, nor what it averaged, but how it MOVED — the morning build-up
// in the valley, the evening inversion, a front crossing the country.
//
// It is one body per (metric, span) rather than a frame per request. The
// geometry is the bulk of a hex payload and it is the same in every frame, so
// stating the cells once and giving each frame a bare array of numbers is the
// difference between 35 KB a frame and about 3 KB a frame — an animation that
// arrives in one fetch instead of twenty-four.
type FrameSpec struct {
	// Name is the wire value of ?span=, and Step is how much ground one frame
	// covers. Both are published on /api/v1/meta so the client can label the
	// scrubber without a second vocabulary of its own.
	Name string
	Step time.Duration
	Dur  time.Duration
}

// FrameSpecs are the spans a timelapse may be asked for — closed, for the same
// reason WindowSpecs is: each one is a precomputed body per metric, so the cache
// stays bounded by this list rather than by what the internet asks for.
//
// A week runs at six hours rather than at one. 168 frames is not an animation a
// reader can follow, and at that length what shows is the daily cycle, which six
// hours still resolves.
var FrameSpecs = []FrameSpec{
	{Name: "24h", Step: time.Hour, Dur: 24 * time.Hour},
	{Name: "7d", Step: 6 * time.Hour, Dur: 7 * 24 * time.Hour},
}

// KnownSpan reports whether name may be served. Handlers answer 400 otherwise
// rather than substituting a default, so a reader who asked for a week and got a
// day has been told.
func KnownSpan(name string) bool {
	for _, s := range FrameSpecs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// ringDur is how much history the ring holds: the longest span published, and no
// more. Anything older can never appear in a body, so keeping it would be memory
// spent on frames no request can reach.
func ringDur() time.Duration {
	d := time.Duration(0)
	for _, s := range FrameSpecs {
		if s.Dur > d {
			d = s.Dur
		}
	}
	return d
}

// hourCells is one rollup hour reduced to the grid: the median of every sensor
// that reported in that cell in that hour. Reduced at build time and kept
// reduced, because the ring is held in memory across cycles and a week of raw
// per-sensor rows would be two orders of magnitude more of it.
type hourCells struct {
	bucket time.Time
	cells  map[axial]float64
}

// frameRing is one metric's recent hours, oldest first. It is carried forward
// from the previous snapshot and extended by the hours that have completed
// since: a past hour's rollup does not change once the hour has passed, so
// re-reading a week of it every cycle would be a week of work to learn one new
// number.
type frameRing struct {
	hours []hourCells
}

// timelapsePayload is the wire shape. Cells is the union of every cell any frame
// touches, and each frame's Values is positional against it — index i is Cells[i]
// — with null where that cell had no reading in that frame.
type timelapsePayload struct {
	GeneratedAt  time.Time        `json:"generated_at"`
	Metric       string           `json:"metric"`
	Span         string           `json:"span"`
	StepSeconds  int              `json:"step_seconds"`
	ResolutionKM float64          `json:"resolution_km"`
	Cells        [][2]float64     `json:"cells"`
	Frames       []timelapseFrame `json:"frames"`
}

type timelapseFrame struct {
	T time.Time `json:"t"`
	// Pointers, so a cell with no reading in this frame is null rather than 0 —
	// 0 µg/m³ is a reading, and the client draws the two differently.
	V []*float64 `json:"v"`
}

// TimelapseBody returns the prepared body for one metric and span, and whether
// there is one. A metric outside the catalogue, or a span outside FrameSpecs,
// has no body; the handler turns that into a 400 rather than an empty animation.
func (s *Snapshot) TimelapseBody(metric, span string) (Body, bool) {
	if s == nil {
		return Body{}, false
	}
	b, ok := s.Timelapse[timelapseKey(metric, span)]
	return b, ok
}

func timelapseKey(metric, span string) string { return metric + "|" + span }

// buildTimelapse extends the previous cycle's rings and re-encodes the bodies.
//
// prev is the snapshot currently being served, or nil on the first build after a
// restart — which is the only cycle that reads a whole week. Its errors are the
// build's errors: a timelapse that silently failed would leave the player
// showing the previous cycle's animation as though it were current.
func buildTimelapse(ctx context.Context, st *store.Store, prev, snap *Snapshot, now time.Time) error {
	// Truncated because the ring is made of rollup hours, and the hour in
	// progress has only part of its samples in it — including it would make the
	// last frame of every animation dip for reasons that are about the clock
	// rather than about the air.
	end := now.Truncate(time.Hour)
	oldest := end.Add(-ringDur())

	snap.frames = make(map[string]*frameRing, len(upstream.CanonicalMetrics()))
	snap.Timelapse = make(map[string]Body, len(upstream.CanonicalMetrics())*len(FrameSpecs))

	for _, metric := range upstream.CanonicalMetrics() {
		var carried *frameRing
		if prev != nil {
			carried = prev.frames[metric]
		}
		ring, err := extendRing(ctx, st, metric, carried, oldest, end)
		if err != nil {
			return err
		}
		snap.frames[metric] = ring

		for _, spec := range FrameSpecs {
			body, err := encode(timelapseFrom(now, metric, spec, ring, end))
			if err != nil {
				return fmt.Errorf("snapshot: encode timelapse %s/%s: %w", metric, spec.Name, err)
			}
			snap.Timelapse[timelapseKey(metric, spec.Name)] = body
		}
	}
	return nil
}

// extendRing drops the hours that have fallen out of the longest span and reads
// only the hours the carried ring does not already have.
func extendRing(ctx context.Context, st *store.Store, metric string, carried *frameRing, oldest, end time.Time) (*frameRing, error) {
	ring := &frameRing{}
	since := oldest
	if carried != nil {
		for _, h := range carried.hours {
			if !h.bucket.Before(oldest) {
				ring.hours = append(ring.hours, h)
			}
		}
		if n := len(ring.hours); n > 0 {
			since = ring.hours[n-1].bucket.Add(time.Hour)
		}
	}
	if !since.Before(end) {
		return ring, nil
	}

	readings, err := st.FrameReadings(ctx, metric, since, end)
	if err != nil {
		return nil, fmt.Errorf("snapshot: timelapse %s: %w", metric, err)
	}
	ring.hours = append(ring.hours, foldHours(readings)...)
	return ring, nil
}

// foldHours bins each hour's readings onto the grid. The readings arrive in
// bucket order, so the result is in bucket order too, which is what lets
// extendRing resume from the last one.
func foldHours(readings []store.FrameReading) []hourCells {
	byHour := map[time.Time]map[axial][]float64{}
	var order []time.Time
	for _, r := range readings {
		vals := byHour[r.Bucket]
		if vals == nil {
			vals = map[axial][]float64{}
			byHour[r.Bucket] = vals
			order = append(order, r.Bucket)
		}
		c := hexOf(r.Lon, r.Lat, HexResolutionKM)
		vals[c] = append(vals[c], r.Value)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })

	out := make([]hourCells, 0, len(order))
	for _, b := range order {
		cells := make(map[axial]float64, len(byHour[b]))
		for c, vs := range byHour[b] {
			cells[c] = median(vs)
		}
		out = append(out, hourCells{bucket: b, cells: cells})
	}
	return out
}

// timelapseFrom folds the ring into one span's frames.
//
// A step wider than an hour takes the median of the hours in it, per cell —
// the same statistic a cell already carries, so a six-hourly frame and an
// hourly one mean the same thing about the same ground.
func timelapseFrom(now time.Time, metric string, spec FrameSpec, ring *frameRing, end time.Time) timelapsePayload {
	start := end.Add(-spec.Dur)

	// Grouped by step index rather than by wall clock, so the frames of one
	// request tile the span exactly and the last one ends at end.
	groups := map[int64]map[axial][]float64{}
	for _, h := range ring.hours {
		if h.bucket.Before(start) {
			continue
		}
		i := int64(h.bucket.Sub(start) / spec.Step)
		g := groups[i]
		if g == nil {
			g = map[axial][]float64{}
			groups[i] = g
		}
		for c, v := range h.cells {
			g[c] = append(g[c], v)
		}
	}

	// The cell list is the union across the whole span, sorted by grid
	// coordinate: a frame's array is positional against it, and a cell that
	// appears only halfway through must hold the same index throughout or the
	// animation would slide sideways.
	seen := map[axial]bool{}
	for _, g := range groups {
		for c := range g {
			seen[c] = true
		}
	}
	coords := make([]axial, 0, len(seen))
	for c := range seen {
		coords = append(coords, c)
	}
	sort.Slice(coords, func(i, j int) bool {
		if coords[i].q != coords[j].q {
			return coords[i].q < coords[j].q
		}
		return coords[i].r < coords[j].r
	})
	index := make(map[axial]int, len(coords))
	cells := make([][2]float64, 0, len(coords))
	for i, c := range coords {
		lon, lat := hexCentre(c, HexResolutionKM)
		cells = append(cells, [2]float64{round4(lon), round4(lat)})
		index[c] = i
	}

	steps := int(spec.Dur / spec.Step)
	frames := make([]timelapseFrame, 0, steps)
	for i := 0; i < steps; i++ {
		f := timelapseFrame{
			T: start.Add(time.Duration(i) * spec.Step),
			V: make([]*float64, len(coords)),
		}
		for c, vs := range groups[int64(i)] {
			v := round1(median(vs))
			f.V[index[c]] = &v
		}
		frames = append(frames, f)
	}

	return timelapsePayload{
		GeneratedAt:  now,
		Metric:       metric,
		Span:         spec.Name,
		StepSeconds:  int(spec.Step / time.Second),
		ResolutionKM: HexResolutionKM,
		Cells:        cells,
		Frames:       frames,
	}
}
