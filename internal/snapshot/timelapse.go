package snapshot

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
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
	{Name: "48h", Step: 2 * time.Hour, Dur: 48 * time.Hour},
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

// TimelapseTiersKM are the resolutions the animation is published at, coarsest
// first — a deliberate subset of HexTiersKM.
//
// The live grid answers the national view with 100 km bins; a replay fixed at
// 15 km drew a third of that size, so pressing play visibly shrank every cell.
// Only the three finest are withheld: a replay carries every cell for every
// frame of a week, so the payload grows with the cell count where a live body
// pays it once, and below 2 km the bins hold one sensor each anyway.
var TimelapseTiersKM = []float64{100, 50, 25, 15, 5, 2}

// SnapTimelapseKM maps a requested resolution onto the nearest published
// timelapse tier, by the same geometric rule and for the same reasons as
// SnapResolutionKM — including that nonsense lands on the default rather than
// erroring.
func SnapTimelapseKM(want float64) float64 {
	best, bestErr := HexResolutionKM, math.Inf(1)
	for _, t := range TimelapseTiersKM {
		if e := math.Abs(math.Log(want / t)); e < bestErr {
			best, bestErr = t, e
		}
	}
	return best
}

// hourCells is one rollup hour reduced to the grid, kept reduced: the ring
// lives in memory across cycles.
type hourCells struct {
	bucket time.Time
	// One reduction per published tier, each folded from the hour's readings.
	// A coarse tier is NOT derived from a finer one's cells: averaging cell
	// medians would weight a bin holding one sensor like a bin holding twenty.
	tiers map[float64]map[axial]float64
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

// TimelapseBody returns the prepared body for one metric, span and resolution,
// if there is one.
//
// Snapped here rather than in the handler, for the reason HexBody gives: this
// package owns the tier list, so it is the only place that can guarantee the
// key it looks up is one a build wrote.
func (s *Snapshot) TimelapseBody(metric, span string, resKM float64) (Body, bool) {
	if s == nil {
		return Body{}, false
	}
	b, ok := s.Timelapse[timelapseKey(metric, span, SnapTimelapseKM(resKM))]
	return b, ok
}

func timelapseKey(metric, span string, resKM float64) string {
	return metric + "|" + span + "|" + formatTier(resKM)
}

func formatTier(resKM float64) string { return strconv.FormatFloat(resKM, 'g', -1, 64) }

// buildTimelapse extends the previous cycle's rings and re-encodes the bodies.
// prev is nil only on the first build after a restart, which reads a whole week.
func buildTimelapse(ctx context.Context, st *store.Store, prev, snap *Snapshot, now time.Time) error {
	// The hour in progress has only part of its samples, so it is left out.
	end := now.Truncate(time.Hour)
	oldest := end.Add(-ringDur())

	snap.frames = make(map[string]*frameRing, len(upstream.CanonicalMetrics()))
	snap.Timelapse = make(map[string]Body,
		len(upstream.CanonicalMetrics())*len(FrameSpecs)*len(TimelapseTiersKM))

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
			for _, res := range TimelapseTiersKM {
				body, err := encode(timelapseFrom(now, metric, spec, ring, end, res))
				if err != nil {
					return fmt.Errorf("snapshot: encode timelapse %s/%s at %v km: %w",
						metric, spec.Name, res, err)
				}
				snap.Timelapse[timelapseKey(metric, spec.Name, res)] = body
			}
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
	byHour := map[time.Time]map[float64]map[axial][]float64{}
	var order []time.Time
	for _, r := range readings {
		tiers := byHour[r.Bucket]
		if tiers == nil {
			tiers = make(map[float64]map[axial][]float64, len(TimelapseTiersKM))
			for _, res := range TimelapseTiersKM {
				tiers[res] = map[axial][]float64{}
			}
			byHour[r.Bucket] = tiers
			order = append(order, r.Bucket)
		}
		// Every tier bins the same reading independently, so each cell's median
		// is taken over the sensors actually inside it.
		for _, res := range TimelapseTiersKM {
			c := hexOf(r.Lon, r.Lat, res)
			tiers[res][c] = append(tiers[res][c], r.Value)
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })

	out := make([]hourCells, 0, len(order))
	for _, b := range order {
		tiers := make(map[float64]map[axial]float64, len(byHour[b]))
		for res, vals := range byHour[b] {
			cells := make(map[axial]float64, len(vals))
			for c, vs := range vals {
				cells[c] = median(vs)
			}
			tiers[res] = cells
		}
		out = append(out, hourCells{bucket: b, tiers: tiers})
	}
	return out
}

// timelapseFrom folds the ring into one span's frames at one published tier; a
// step wider than an hour takes the median of the hours in it, per cell.
func timelapseFrom(now time.Time, metric string, spec FrameSpec, ring *frameRing, end time.Time, resKM float64) timelapsePayload {
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
		for c, v := range h.tiers[resKM] {
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
		lon, lat := hexCentre(c, resKM)
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
		ResolutionKM: resKM,
		Cells:        cells,
		Frames:       frames,
	}
}
