package snapshot

import (
	"context"
	"fmt"
	"sort"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// One prepared body per (metric, span): geometry once, a bare number array per
// frame. Name is the wire value of ?span=.
type FrameSpec struct {
	Name string
	Step time.Duration
	Dur  time.Duration
}

// Closed like WindowSpecs: each entry is a precomputed body per metric.
var FrameSpecs = []FrameSpec{
	{Name: "24h", Step: time.Hour, Dur: 24 * time.Hour},
	{Name: "7d", Step: 6 * time.Hour, Dur: 7 * 24 * time.Hour},
}

// KnownSpan reports whether name may be served; handlers 400 anything else.
func KnownSpan(name string) bool {
	for _, s := range FrameSpecs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// ringDur is the longest span published; older hours can reach no body.
func ringDur() time.Duration {
	d := time.Duration(0)
	for _, s := range FrameSpecs {
		if s.Dur > d {
			d = s.Dur
		}
	}
	return d
}

// hourCells is one rollup hour reduced to the grid, kept reduced: the ring
// lives in memory across cycles.
type hourCells struct {
	bucket time.Time
	cells  map[axial]float64
}

// frameRing is one metric's recent hours, oldest first, carried between cycles:
// a past hour's rollup does not change.
type frameRing struct {
	hours []hourCells
}

// Cells is the union across the span; each frame's V is positional against it.
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
	// Pointers: an absent cell is null, and 0 µg/m³ is a reading.
	V []*float64 `json:"v"`
}

// TimelapseBody returns the prepared body for one metric and span, if there is one.
func (s *Snapshot) TimelapseBody(metric, span string) (Body, bool) {
	if s == nil {
		return Body{}, false
	}
	b, ok := s.Timelapse[timelapseKey(metric, span)]
	return b, ok
}

func timelapseKey(metric, span string) string { return metric + "|" + span }

// buildTimelapse extends the previous cycle's rings and re-encodes the bodies.
// prev is nil only on the first build after a restart, which reads a whole week.
func buildTimelapse(ctx context.Context, st *store.Store, prev, snap *Snapshot, now time.Time) error {
	// The hour in progress has only part of its samples, so it is left out.
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

// foldHours bins each hour's readings onto the grid, in bucket order — which is
// what lets extendRing resume from the last one.
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

// timelapseFrom folds the ring into one span's frames; a step wider than an hour
// takes the median of the hours in it, per cell.
func timelapseFrom(now time.Time, metric string, spec FrameSpec, ring *frameRing, end time.Time) timelapsePayload {
	start := end.Add(-spec.Dur)

	// By step index, so the frames tile the span exactly and the last ends at end.
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

	// Union across the span, sorted: a cell must hold one index in every frame.
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
