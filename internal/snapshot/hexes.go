package snapshot

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// HexResolutionKM is the default centre-to-centre spacing of the hex grid, in
// kilometres — what a caller naming no resolution receives.
//
// This was once the only spacing, on the reasoning that a caller free to choose
// could ask for one metre and read the sensor list back a bin at a time. That
// reasoning is superseded and the history matters, because the code below now
// does the thing the old comment forbade:
//
//   - The choice is not free. HexTiersKM is a closed list and a request is
//     snapped onto it, so there is no one-metre bin to ask for — only the
//     tiers we publish.
//   - Every tier is anchored at the same projected origin, so a tier is a fixed
//     tiling of the ground and not a grid re-cut around the request. The
//     extraction the old rule feared came from a grid that could shift under the
//     caller: slide the same cell size a few metres at a time and the moving
//     intersections isolate a point. A fixed anchor removes that seam — asking
//     twice at one tier returns the same bins over the same ground.
//   - Coarse tiers add nothing to the finest one. Hexagons do NOT subdivide into
//     hexagons, so a 15 km bin is not the union of the 2 km bins under it and
//     the tiers cannot be differenced cell-for-cell. What settles the question
//     is simpler: the 250 m tier is published outright, so no combination of
//     coarser tiers can locate anything more precisely than it already does.
//
// What remains true is that the finest tier IS the disclosure: at 250 m a bin
// holding one sensor is that sensor's street. That is a deliberate decision,
// taken to match maps.sensor.community, whose upstream already publishes exact
// sensor coordinates. It is a product choice about how precisely to locate a
// device, not a hole in the grid, and it is the number to revisit if that
// choice is ever reconsidered.
//
// Every visitor asking the same question still gets the same bytes, so
// responses stay publicly cacheable.
const HexResolutionKM = 15.0

// HexTiersKM are the resolutions the grid is published at, coarsest first.
//
// A closed list rather than a free parameter: it bounds the finest cell we will
// ever draw, and it keeps the number of distinct cacheable responses small.
// 0.25 km is the address-level tier — roughly a city block, and the point at
// which a lone sensor in a bin is locatable.
//
// The three coarse tiers exist for the national view. The client sizes a cell
// to 32 screen pixels, which at the zoom the country fits on a screen wants a
// bin around 45 km wide; with 15 km as the coarsest, that view was answered
// with bins a third of the size it asked for and the grid rendered as a field
// of specks. Coarse tiers also cost the least to build — a coarser bin means
// fewer of them.
var HexTiersKM = []float64{100, 50, 25, 15, 5, 2, 1, 0.5, 0.25}

// SnapResolutionKM maps a requested resolution onto the published tier nearest
// it in ratio, not in absolute difference: tiers are geometric, so 0.4 km is
// nearer 0.5 than 0.25 even though the gaps are 0.1 and 0.15.
//
// Anything unparseable or out of range lands on a real tier rather than an
// error, because a client that asks clumsily should still get a usable map —
// and it reads the tier it actually got back off the envelope.
func SnapResolutionKM(want float64) float64 {
	// Seeded with the default and an infinite error, which is also what handles
	// nonsense: zero, negative and infinite inputs all give a NaN or infinite
	// log, every `e < bestErr` is false, and the seed survives untouched. An
	// explicit guard above the loop would say the same thing twice.
	best, bestErr := HexResolutionKM, math.Inf(1)
	for _, t := range HexTiersKM {
		if e := math.Abs(math.Log(want / t)); e < bestErr {
			best, bestErr = t, e
		}
	}
	return best
}

// hexCountryUnknown is the code used for a bin whose sensors all predate the
// sensor.country_code column. Spelled out rather than left empty so a client
// never has to decide what "" means, and distinct from any real ISO code.
const hexCountryUnknown = "??"

// hexRefLat is the latitude the equirectangular projection is true at. Bulgaria
// spans 41.2–44.3°N; taking the middle keeps the east-west scale error under
// about 1.5 % across the country, which is a rounding error against a 15 km bin.
const hexRefLat = 42.75

const earthRadiusKM = 6371.0

// hexPayload carries both the aggregate grid and the point tier, because a
// client draws them through one path and a second envelope would only make it
// branch. resolution_km tells the two apart: above zero an entry is a bin, with
// a count and median values and no sensor identity; at zero an entry is one
// sensor and names it.
type hexPayload struct {
	GeneratedAt  time.Time  `json:"generated_at"`
	ResolutionKM float64    `json:"resolution_km"`
	Hexes        []hexEntry `json:"hexes"`
	// Coverage is how many sensors of each network have a usable reading for
	// each metric, country-wide and identical on every tier. The layer menu
	// says it about a metric the reader has not picked yet, which no per-cell
	// number can answer. Omitted when empty so a fixture-built payload does
	// not serialise a null.
	Coverage map[string]map[string]int `json:"coverage,omitempty"`

	// idx indexes Hexes for HexBody's viewport clip. Unexported, so a struct
	// literal built directly (as tests do) leaves it nil; HexBody falls back
	// to a linear walk in that case.
	idx *bboxIndex
}

// withoutGeneratedAt clears the build timestamp so identical bins hash
// identically across builds.
func (p hexPayload) withoutGeneratedAt() any {
	p.GeneratedAt = time.Time{}
	return p
}

var _ canonicalisable = hexPayload{}

type hexEntry struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
	// SensorID names a STATION (see stationKey), not a device — a station is
	// several devices at one key. Set when the bin holds exactly one station,
	// using pointsFrom's smallest-member tie-break; omitted for 2+.
	SensorID int64 `json:"sensor_id,omitempty"`
	// SensorIDByMetric names, per metric, the one station reporting it (its
	// smallest member id). A metric two stations report is absent; nil omitted.
	SensorIDByMetric map[string]int64   `json:"sensor_id_by_metric,omitempty"`
	N                int                `json:"n"`
	Country          string             `json:"country"`
	Values           map[string]float64 `json:"values"`
	// Source names the ONE network behind this entry: every sensor on the point
	// tier, and an aggregate bin that only one network reaches. Omitted on a bin
	// fed by both, which carries BySource instead — an entry cannot be both.
	Source string `json:"source,omitempty"`
	// BySource carries each network's own count and medians, so the browser can
	// answer a network toggle from the body it already holds rather than by
	// asking for a filtered one. Omitted on a single-network entry, where Values
	// already is that network's numbers.
	BySource map[string]sourceEntry `json:"by_source,omitempty"`
}

// bboxIndex buckets a slice of hexEntry onto the BBoxQuantumDegrees grid, so a
// viewport clip touches only the buckets the box overlaps instead of every
// entry. Unexported and carried alongside the source slice it indexes, never
// serialised.
//
// clip is correct for ANY box, quantised or not — HexBody and PointBody carry
// no precondition on the caller. A bucket's own range is closed only on its
// low edge, so an edge bucket can hold entries the box does not actually
// contain (or, off the quantum grid, miss entries it does); clip re-derives
// its bucket bounds with math.Floor and re-checks all four edges rather than
// assuming any of them are whole. See clip.
type bboxIndex struct {
	buckets map[[2]int][]int // bucket -> ascending indices into the source slice
	// minCol/maxCol/minRow/maxRow bound the occupied buckets. clip compares a
	// box's own bucket range against them to recognise a box that covers the
	// whole index — a country-sized viewport, say — and skip straight to a
	// linear walk, since every bucket would be touched anyway. Zero when
	// buckets is empty, which len(buckets) == 0 guards clip from trusting.
	minCol, maxCol, minRow, maxRow int
}

func bucketOf(lon, lat float64) [2]int {
	const q = BBoxQuantumDegrees
	return [2]int{int(math.Floor(lon / q)), int(math.Floor(lat / q))}
}

// buildBBoxIndex indexes entries once, at build time. The result is read-only
// afterwards, so it is safe to share across concurrent requests against the
// same Snapshot.
func buildBBoxIndex(entries []hexEntry) *bboxIndex {
	idx := &bboxIndex{buckets: make(map[[2]int][]int)}
	first := true
	for i, e := range entries {
		k := bucketOf(e.Lon, e.Lat)
		idx.buckets[k] = append(idx.buckets[k], i)
		if first {
			idx.minCol, idx.maxCol, idx.minRow, idx.maxRow = k[0], k[0], k[1], k[1]
			first = false
			continue
		}
		idx.minCol, idx.maxCol = min(idx.minCol, k[0]), max(idx.maxCol, k[0])
		idx.minRow, idx.maxRow = min(idx.minRow, k[1]), max(idx.maxRow, k[1])
	}
	return idx
}

// clipLinear walks entries in their original order, checking each against
// bb directly. What an unindexed clip does, and what clip itself falls back
// to once a box's bucket range covers the whole index: at that point the
// bucket bookkeeping — building idxs, sorting it — is pure overhead on top
// of a walk that touches every entry regardless.
func clipLinear(entries []hexEntry, bb BBox) []hexEntry {
	out := make([]hexEntry, 0, len(entries))
	for _, e := range entries {
		if bb.contains(e.Lon, e.Lat) {
			out = append(out, e)
		}
	}
	return out
}

// clip returns the entries bb.contains, in their original order.
//
// w0/s0/e0/n0 are the bucket columns/rows the box can touch, derived with
// the same math.Floor bucketOf keys entries with — so the walk visits every
// bucket bucketOf could have placed an entry in, on all four sides. Bucket
// bounds and box bounds agree only when the box is already on the quantum
// grid (what BBox.Quantise produces); off it, a bucket's range can extend
// past any of the box's four edges, so all four — not just the box's own
// upper ones — get a per-entry re-check rather than being taken whole. Only
// the strictly interior buckets, wholly inside the box on every axis, skip
// it.
func (idx *bboxIndex) clip(entries []hexEntry, bb BBox) []hexEntry {
	const q = BBoxQuantumDegrees
	w0 := int(math.Floor(bb.W / q))
	e0 := int(math.Floor(bb.E / q))
	s0 := int(math.Floor(bb.S / q))
	n0 := int(math.Floor(bb.N / q))

	// The box's own bucket range covers every occupied bucket: the walk
	// below would touch all of them anyway, so building and sorting idxs
	// only adds cost. A country-sized viewport takes this path.
	if len(idx.buckets) > 0 &&
		w0 <= idx.minCol && e0 >= idx.maxCol && s0 <= idx.minRow && n0 >= idx.maxRow {
		return clipLinear(entries, bb)
	}

	var idxs []int
	for i := w0; i <= e0; i++ {
		edgeCol := i == w0 || i == e0
		for j := s0; j <= n0; j++ {
			bucket, ok := idx.buckets[[2]int{i, j}]
			if !ok {
				continue
			}
			if edgeCol || j == s0 || j == n0 {
				for _, pos := range bucket {
					if bb.contains(entries[pos].Lon, entries[pos].Lat) {
						idxs = append(idxs, pos)
					}
				}
				continue
			}
			idxs = append(idxs, bucket...)
		}
	}
	sort.Ints(idxs)
	out := make([]hexEntry, len(idxs))
	for i, pos := range idxs {
		out[i] = entries[pos]
	}
	return out
}

// The hex bin and the area entry publish the same object under the same key.
type sourceEntry = SourceEntry

// sourceOf names the network a reading came from. A row written before the
// source column existed carries an empty Source and is sensor.community; the
// browser's sourcefilter.svelte.js applies the same rule to features.
func sourceOf(sr store.SensorReading) string {
	if sr.Source == "" {
		return "sensor.community"
	}
	return sr.Source
}

// coverageFrom counts, per network, how many sensors currently hold a usable
// reading for each metric. Zero counts are omitted rather than written as 0:
// the layer menu distinguishes "no station reports this" from "some do", and an
// explicit zero is the same fact as an absent key with an extra byte per metric.
func coverageFrom(sensors []store.SensorReading) map[string]map[string]int {
	cov := make(map[string]map[string]int, 2)
	for _, sr := range sensors {
		src := sourceOf(sr)
		per := cov[src]
		if per == nil {
			per = make(map[string]int, len(upstream.CanonicalMetrics()))
			cov[src] = per
		}
		for _, m := range upstream.CanonicalMetrics() {
			if _, ok := sr.Values[m]; ok {
				per[m]++
			}
		}
	}
	// A network we hold nothing for is absent, never an empty object: the layer
	// menu reads a present-but-empty entry as "does not measure this".
	for src, per := range cov {
		if len(per) == 0 {
			delete(cov, src)
		}
	}
	return cov
}

// HexGridOf reduces sensor positions to the distinct hexes they fall in, with
// each hex's centre. It is what the wind collector asks the met model about:
// the grid exists where the sensors are, so there is no fixed tiling to walk.
//
// Deduplicated and ordered by coordinate, because the result becomes a request
// URL — an unstable order would defeat any caching in front of it and make two
// identical fetches look different.
func HexGridOf(sensors []store.SensorReading) []HexCell {
	seen := make(map[axial]bool, len(sensors))
	coords := make([]axial, 0, len(sensors))
	for _, sr := range sensors {
		// The wind grid is asked about at the default resolution: the met model
		// is coarser than any tier here, so a finer grid would multiply the
		// upstream requests without giving the forecast anything new to say.
		c := hexOf(sr.Lon, sr.Lat, HexResolutionKM)
		if !seen[c] {
			seen[c] = true
			coords = append(coords, c)
		}
	}
	sort.Slice(coords, func(i, j int) bool {
		if coords[i].q != coords[j].q {
			return coords[i].q < coords[j].q
		}
		return coords[i].r < coords[j].r
	})

	cells := make([]HexCell, 0, len(coords))
	for _, c := range coords {
		lon, lat := hexCentre(c, HexResolutionKM)
		cells = append(cells, HexCell{Q: c.q, R: c.r, Lon: round4(lon), Lat: round4(lat)})
	}
	return cells
}

// bodyKey identifies one encoded answer: the snapped tier, the quantised box,
// and which of the two builders produced it. Comparable, so it is the map key
// itself rather than a string somebody has to keep in sync with it.
//
// An unclipped request leaves the box zero, which cannot collide with a clipped
// one: ParseBBox refuses a box whose west is not strictly left of its east.
type bodyKey struct {
	resKM      float64
	w, s, e, n float64
	point      bool
}

// bodyCacheMax is the entry bound. On overflow the map is cleared whole rather
// than evicted one entry at a time: a snapshot lives about five minutes, and a
// caller who fills this many distinct quantised boxes inside one is already
// past the point where an eviction policy is what protects us.
const bodyCacheMax = 256

// bodyCacheMaxBytes is the byte bound alongside bodyCacheMax. The entry count
// alone bounds nothing about size: a caller who picks the tier gets to pick
// the body size too, and 256 near-full-tier bodies at the finest tier run
// order 200 KB each. 32 MB keeps one snapshot's cache well inside a sane
// resident footprint even with four snapshots (live plus three windows) held
// at once.
const bodyCacheMaxBytes = 32 << 20

// bodyCache memoises the per-viewport encodes HexBody and PointBody would
// otherwise repeat on every request — a json.Marshal, a second marshal, a
// SHA-256 and a gzip at BestCompression each time.
//
// Safe because a *Snapshot is immutable once built and is replaced wholesale by
// the ingest cycle, so an entry computed from one can never go stale. Window
// returns a distinct *Snapshot per window, so the window needs no place in the
// key either.
type bodyCache struct {
	mu    sync.Mutex
	m     map[bodyKey]Body
	bytes int
}

// A nil cache means "do not cache" and never a panic: a Snapshot built by a
// struct literal, as the tests build them, has none.
func (c *bodyCache) get(k bodyKey) (Body, bool) {
	if c == nil {
		return Body{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.m[k]
	return b, ok
}

// bodySize is the retained heap a Body accounts for in the cache's byte
// budget: the JSON and the Gzip slices, plus the ETag string, which is what a
// held Body actually keeps alive.
func bodySize(b Body) int {
	return len(b.JSON) + len(b.Gzip) + len(b.ETag)
}

func (c *bodyCache) put(k bodyKey, b Body) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	size := bodySize(b)
	// Larger than the whole budget: serve it uncached rather than clear
	// everything else just to make room for one entry.
	if size > bodyCacheMaxBytes {
		return
	}
	if c.m == nil {
		c.m = make(map[bodyKey]Body)
	}
	// existing is 0 when k is not yet in the map, since bodySize of the zero
	// Body is 0 — so replacing a key never double-counts it.
	existing := bodySize(c.m[k])
	if len(c.m) >= bodyCacheMax || c.bytes-existing+size > bodyCacheMaxBytes {
		clear(c.m)
		c.bytes = 0
		existing = 0
	}
	c.m[k] = b
	c.bytes += size - existing
}

// HexBody answers a hex request at a given tier, optionally clipped to a
// viewport.
//
// The unfiltered default returns the body encoded once at build time; every
// other combination is encoded here, per request. That is deliberate: those
// responses are still a pure function of (snapshot, tier, box) with nothing
// per-caller in them, so they remain publicly cacheable under their own URL and
// carry their own ETag.
func (s *Snapshot) HexBody(resKM float64, bb BBox, clip bool) (Body, error) {
	// Snapped here rather than in the handler: this package owns the tier list,
	// so it is the only place that can guarantee a request lands on a tier that
	// exists. A caller that forgot to snap would silently fall through to the
	// default below and serve 15 km cells while claiming to answer 0.25.
	resKM = SnapResolutionKM(resKM)
	if !clip && resKM == HexResolutionKM {
		return s.Hexes, nil
	}
	p, ok := s.hexTiers[resKM]
	if !ok {
		return s.Hexes, nil
	}
	// Everything from here is encoded per request, and every such answer is a
	// pure function of (snapshot, tier, box) — so it is worth memoising. The
	// handler quantises the box before it arrives, which is what keeps the set
	// of keys finite.
	k := bodyKey{resKM: resKM}
	if clip {
		k.w, k.s, k.e, k.n = bb.W, bb.S, bb.E, bb.N
	}
	if b, ok := s.bodies.get(k); ok {
		return b, nil
	}

	out := p
	if clip {
		var hexes []hexEntry
		if p.idx != nil {
			hexes = p.idx.clip(p.Hexes, bb)
		} else {
			hexes = clipLinear(p.Hexes, bb)
		}
		out = hexPayload{GeneratedAt: p.GeneratedAt, ResolutionKM: p.ResolutionKM,
			Coverage: p.Coverage, Hexes: hexes}
	}
	b, err := encode(out)
	if err != nil {
		return Body{}, err
	}
	s.bodies.put(k, b)
	return b, nil
}

// PointResolutionKM is the resolution a caller names to ask for individual
// sensors rather than bins. Zero, because it is the limit of the tier list: a
// cell small enough to hold one device is that device.
const PointResolutionKM = 0.0

// The widest viewport the point tier will answer, per axis, in degrees.
const MaxPointBBoxDegrees = 2.0

// CellStatChangedAt is the instant the hex cell's summary statistic changed from
// mean to median.
//
// Published on /api/v1/meta so a chart can annotate the step rather than let a
// reader mistake it for a change in the air. It is a constant and not a config
// key because it is a fact about this build, not something an operator sets;
// there is no backfill behind it because a cell is recomputed from live
// readings every cycle and keeps no archive of its own.
var CellStatChangedAt = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

// PointBody answers the point tier: one entry per sensor inside the box, each
// naming its device.
//
// The box is REQUIRED, and the handler refuses the request without one. Every
// other tier may be asked country-wide because a bin is an aggregate; this tier
// is not, and an unbounded answer would be a downloadable registry of every
// device in the country rather than a map view. Requiring the box does not make
// the data secret — a determined caller can walk boxes — it makes the walk
// visible to the rate limiter instead of free in one request.
//
// Coordinates are passed through exactly as sensor.community served them. That
// upstream already applies its own fuzzing (exact_location: 0); republishing
// what is public is the decision that was taken, sharpening it is not.
func (s *Snapshot) PointBody(bb BBox) (Body, error) {
	k := bodyKey{resKM: PointResolutionKM, w: bb.W, s: bb.S, e: bb.E, n: bb.N, point: true}
	if b, ok := s.bodies.get(k); ok {
		return b, nil
	}
	var hexes []hexEntry
	if s.pointsIndex != nil {
		hexes = s.pointsIndex.clip(s.points, bb)
	} else {
		hexes = clipLinear(s.points, bb)
	}
	out := hexPayload{GeneratedAt: s.GeneratedAt, ResolutionKM: PointResolutionKM,
		Coverage: s.coverage, Hexes: hexes}
	b, err := encode(out)
	if err != nil {
		return Body{}, err
	}
	s.bodies.put(k, b)
	return b, nil
}

// pointsFrom turns sensors into point-tier entries, one per STATION, ordered by
// station id.
//
// A station is every sensor at one pair of coordinates, grouped and identified
// exactly as stationIDs does it for the sensor catalogue — so the cell a reader
// reaches by zooming is the same thing, under the same id, as the marker they
// reach by clicking. Both networks need it: a community site is a dust sensor
// and its climate twin, and an EEA site is one sensor id per pollutant, which
// put thirteen entries on two places in Sofia, each holding a single metric.
//
// The group key carries the network as well as the position. Two networks at
// one coordinate to the last digit is not a thing the data does, and if it ever
// did, an entry naming one network while holding the other's readings is the
// one outcome the toggles could not survive.
//
// Ordered by the station id so the payload is a function of the readings alone:
// the source slice arrives in whatever order the query returned, and reordering
// between cycles would churn every ETag without a single value having changed.
//
// N is the station's devices, the same count the aggregate tiers report.
func pointsFrom(sensors []store.SensorReading) []hexEntry {
	type station struct {
		entry hexEntry
		vals  map[string][]float64
	}

	order := make([]stationKey, 0, len(sensors))
	stations := make(map[stationKey]*station, len(sensors))
	for _, sr := range sensors {
		k := stationKeyOf(sr)
		st, seen := stations[k]
		if !seen {
			country := sr.Country
			if country == "" {
				country = hexCountryUnknown
			}
			st = &station{
				entry: hexEntry{
					Lon: sr.Lon, Lat: sr.Lat, SensorID: sr.SensorID,
					Country: country, Source: k.source,
				},
				vals: make(map[string][]float64, len(sr.Values)),
			}
			stations[k] = st
			order = append(order, k)
		}
		st.entry.N++
		// The smallest member id, so the station keeps one id whatever order the
		// rows arrive in and for as long as that member reports.
		if sr.SensorID < st.entry.SensorID {
			st.entry.SensorID = sr.SensorID
		}
		for m, v := range sr.Values {
			st.vals[m] = append(st.vals[m], v)
		}
	}

	out := make([]hexEntry, 0, len(order))
	for _, k := range order {
		st := stations[k]
		values := make(map[string]float64, len(st.vals))
		for _, m := range upstream.CanonicalMetrics() {
			if vs, ok := st.vals[m]; ok {
				values[m] = round1(median(vs))
			}
		}
		st.entry.Values = values
		out = append(out, st.entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SensorID < out[j].SensorID })
	return out
}

// BBox is a viewport in degrees: west, south, east, north.
type BBox struct{ W, S, E, N float64 }

// ParseBBox reads a "w,s,e,n" query value.
//
// Reports ok=false rather than an error for anything malformed, and the caller
// serves the unfiltered grid: a viewport is an optimisation, so a client that
// garbles one should see the whole country rather than a 400 and a blank map.
func ParseBBox(s string) (BBox, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return BBox{}, false
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return BBox{}, false
		}
		v[i] = f
	}
	b := BBox{W: v[0], S: v[1], E: v[2], N: v[3]}
	// An inverted or empty box would filter everything away and read as "no
	// sensors here", which is a lie about the ground rather than about the box.
	if b.W >= b.E || b.S >= b.N {
		return BBox{}, false
	}
	return b, true
}

// BBoxQuantumDegrees is the grid a clipped viewport is snapped to, in degrees.
const BBoxQuantumDegrees = 0.25

// Quantise widens the box to the enclosing quantum-grid cell.
//
// Widening and never narrowing: a narrowed box would drop bins the caller can
// see on their screen. The point is cardinality — the client sends raw float
// degrees, so without this every pan is a distinct URL that misses the edge
// cache and costs a fresh encode.
//
// The quantum is a power of two, so v/q and v*q only shift the exponent and an
// edge already on a grid line stays exactly where it is.
func (b BBox) Quantise() BBox {
	const q = BBoxQuantumDegrees
	return BBox{
		W: math.Floor(b.W/q) * q,
		S: math.Floor(b.S/q) * q,
		E: math.Ceil(b.E/q) * q,
		N: math.Ceil(b.N/q) * q,
	}
}

// contains reports whether a bin centre falls in the box.
//
// The bin CENTRE, not the bin: a cell straddling the edge is included only if
// its centre is inside. Testing the whole cell would make the answer depend on
// the resolution, so panning at one zoom and another would disagree about the
// same ground.
func (b BBox) contains(lon, lat float64) bool {
	return lon >= b.W && lon <= b.E && lat >= b.S && lat <= b.N
}

// Extent reports the box's width and height in degrees.
//
// ParseBBox rejects inverted and empty boxes, so both values are positive for
// any box a caller can reach this with.
func (b BBox) Extent() (lon, lat float64) {
	return b.E - b.W, b.N - b.S
}

// HexCell is one grid cell's identity and centre.
type HexCell struct {
	Q, R     int
	Lon, Lat float64
}

// axial is a hex grid coordinate. Two ints, so it is comparable and usable as a
// map key — the whole reason for binning in grid space rather than clustering.
type axial struct{ q, r int }

type hexBin struct {
	coord axial
	n     int
	// vals holds every sensor's reading for a metric, kept rather than summed
	// because the bin reports a MEDIAN. A running sum cannot produce one — the
	// middle of a set is not derivable from its total — so the values have to
	// survive until the bin closes.
	vals map[string][]float64
	// countries counts sensors per country code. A 15 km bin straddling a
	// border holds sensors from both, and the bin has to name one; the modal
	// value names the country most of the bin's data actually came from.
	countries map[string]int
	// bySource repeats vals per network. A network's median is not derivable
	// from the blended one, so a bin fed by both has to keep both sets.
	bySource map[string]*sourceBin
	// stations maps each stationKey in the bin to its smallest member id.
	// N cannot stand in: a device count of 2 is one routine station, not two.
	stations map[stationKey]int64
	// sensorID is the smallest member id seen across the whole bin. Only
	// meaningful at len(stations) == 1.
	sensorID int64
	// metricStations holds, per metric, the stationKeys that contributed a
	// value for it — the input to SensorIDByMetric's single-contributor test.
	metricStations map[string]map[stationKey]bool
}

type sourceBin struct {
	n    int
	vals map[string][]float64
}

// modalCountry returns the most common country in the bin, ties broken by code
// so the payload — and therefore its ETag — does not depend on map iteration
// order.
func (b *hexBin) modalCountry() string {
	best, bestN := "", 0
	for code, n := range b.countries {
		if n > bestN || (n == bestN && code < best) {
			best, bestN = code, n
		}
	}
	if best == "" {
		return hexCountryUnknown
	}
	return best
}

// hexPayloadFrom bins sensors onto a fixed pointy-top hex grid.
//
// The grid is anchored at (0, 0) in projected space, not at the data's centroid:
// an anchor derived from the data would shift every cycle as sensors come and
// go, so a bin's centre would wander and its ETag would churn even when no
// reading changed.
func hexPayloadFrom(now time.Time, sensors []store.SensorReading, resKM float64) hexPayload {
	bins := make(map[axial]*hexBin)
	for _, sr := range sensors {
		c := hexOf(sr.Lon, sr.Lat, resKM)
		b := bins[c]
		if b == nil {
			b = &hexBin{coord: c, vals: map[string][]float64{},
				countries: map[string]int{}, bySource: map[string]*sourceBin{},
				stations: map[stationKey]int64{}, sensorID: sr.SensorID,
				metricStations: map[string]map[stationKey]bool{}}
			bins[c] = b
		}
		b.n++
		sk := stationKeyOf(sr)
		// pointsFrom's tie-break, per station and for the bin overall, so a cell
		// and its point-tier marker agree.
		if id, ok := b.stations[sk]; !ok || sr.SensorID < id {
			b.stations[sk] = sr.SensorID
		}
		if sr.SensorID < b.sensorID {
			b.sensorID = sr.SensorID
		}
		if sr.Country != "" {
			b.countries[sr.Country]++
		}
		src := sourceOf(sr)
		sb := b.bySource[src]
		if sb == nil {
			sb = &sourceBin{vals: map[string][]float64{}}
			b.bySource[src] = sb
		}
		sb.n++
		for _, m := range upstream.CanonicalMetrics() {
			if v, ok := sr.Values[m]; ok {
				b.vals[m] = append(b.vals[m], v)
				sb.vals[m] = append(sb.vals[m], v)
				if b.metricStations[m] == nil {
					b.metricStations[m] = map[stationKey]bool{}
				}
				b.metricStations[m][sk] = true
			}
		}
	}

	ordered := make([]*hexBin, 0, len(bins))
	for _, b := range bins {
		ordered = append(ordered, b)
	}
	// Sorted by grid coordinate, so the payload — and therefore the ETag — is a
	// function of the readings alone. Iterating the map directly would reorder
	// the array on every build and invalidate every cached copy each cycle.
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].coord.q != ordered[j].coord.q {
			return ordered[i].coord.q < ordered[j].coord.q
		}
		return ordered[i].coord.r < ordered[j].coord.r
	})

	p := hexPayload{
		GeneratedAt:  now,
		ResolutionKM: resKM,
		Hexes:        make([]hexEntry, 0, len(ordered)),
	}
	for _, b := range ordered {
		lon, lat := hexCentre(b.coord, resKM)
		values := make(map[string]float64, len(b.vals))
		for m, vs := range b.vals {
			// A metric absent from every sensor in the bin is absent from the
			// bin, rather than present as zero: 0 µg/m³ is a reading.
			if len(vs) > 0 {
				values[m] = round1(median(vs))
			}
		}
		e := hexEntry{
			Lon:     round4(lon),
			Lat:     round4(lat),
			N:       b.n,
			Country: b.modalCountry(),
			Values:  values,
		}
		// One station names the bin; two or more do not. See hexEntry.SensorID.
		if len(b.stations) == 1 {
			e.SensorID = b.sensorID
		}
		// Per metric, one contributing station names it, whatever the bin as a
		// whole holds. See hexEntry.SensorIDByMetric.
		for m, sks := range b.metricStations {
			if len(sks) != 1 {
				continue
			}
			for sk := range sks {
				if e.SensorIDByMetric == nil {
					e.SensorIDByMetric = make(map[string]int64, len(b.metricStations))
				}
				e.SensorIDByMetric[m] = b.stations[sk]
			}
		}
		if len(b.bySource) == 1 {
			for src := range b.bySource {
				e.Source = src
			}
		} else {
			e.BySource = make(map[string]sourceEntry, len(b.bySource))
			for src, sb := range b.bySource {
				sv := make(map[string]float64, len(sb.vals))
				for m, vs := range sb.vals {
					if len(vs) > 0 {
						sv[m] = round1(median(vs))
					}
				}
				e.BySource[src] = sourceEntry{N: sb.n, Values: sv}
			}
		}
		p.Hexes = append(p.Hexes, e)
	}
	p.idx = buildBBoxIndex(p.Hexes)
	return p
}

// hexSizeOf is the hex circumradius that produces resKM centre-to-centre
// spacing on a pointy-top grid, where horizontal spacing is √3·size.
//
// Derived from the resolution rather than stored per tier so the two cannot
// drift apart: the drawn cell and the bin it came from are the same number.
func hexSizeOf(resKM float64) float64 { return resKM / math.Sqrt(3) }

// project converts lon/lat to kilometres east and north of (0°, 0°) under an
// equirectangular projection true at hexRefLat. Good enough for binning at 15 km
// over one country; it would not be for a global grid.
func project(lon, lat float64) (x, y float64) {
	x = earthRadiusKM * radians(lon) * math.Cos(radians(hexRefLat))
	y = earthRadiusKM * radians(lat)
	return x, y
}

func unproject(x, y float64) (lon, lat float64) {
	lon = degrees(x / (earthRadiusKM * math.Cos(radians(hexRefLat))))
	lat = degrees(y / earthRadiusKM)
	return lon, lat
}

// hexOf returns the axial coordinate of the hex containing a point, by the
// standard pixel-to-hex conversion followed by cube rounding.
func hexOf(lon, lat, resKM float64) axial {
	size := hexSizeOf(resKM)
	x, y := project(lon, lat)
	q := (math.Sqrt(3)/3*x - y/3) / size
	r := (2.0 / 3.0 * y) / size
	return cubeRound(q, r)
}

func hexCentre(c axial, resKM float64) (lon, lat float64) {
	size := hexSizeOf(resKM)
	x := size * (math.Sqrt(3)*float64(c.q) + math.Sqrt(3)/2*float64(c.r))
	y := size * (1.5 * float64(c.r))
	return unproject(x, y)
}

// cubeRound rounds fractional axial coordinates to the nearest hex centre.
//
// Rounding q and r independently does not work: hex centres do not form a
// rectangular lattice, so independent rounding lands outside the hex near its
// corners. Cube coordinates satisfy x+y+z = 0, and restoring that invariant by
// discarding whichever component moved furthest picks the right neighbour.
func cubeRound(q, r float64) axial {
	x, z := q, r
	y := -x - z
	rx, ry, rz := math.Round(x), math.Round(y), math.Round(z)
	dx, dy, dz := math.Abs(rx-x), math.Abs(ry-y), math.Abs(rz-z)
	switch {
	case dx > dy && dx > dz:
		rx = -ry - rz
	case dy > dz:
		// y is the discarded component, so rx and rz stand as rounded.
	default:
		rz = -rx - ry
	}
	return axial{q: int(rx), r: int(rz)}
}

func radians(d float64) float64 { return d * math.Pi / 180 }
func degrees(r float64) float64 { return r * 180 / math.Pi }

// median is the bin's summary statistic, replacing the mean it reported until
// CellStatChangedAt.
//
// The reason is that a bin is a handful of low-cost devices, not a sample: one
// sensor indoors beside a stove, or failing towards its ceiling, drags a mean of
// four far enough to recolour the cell. The median simply does not move for a
// single bad member, which is what a map of a neighbourhood should do.
//
// Sorts a copy, because the caller's slice is the bin's own record of what it
// saw and reordering it would be a side effect of asking a question.
func median(vs []float64) float64 {
	s := append([]float64(nil), vs...)
	sort.Float64s(s)
	mid := len(s) / 2
	// The even case averages the two middle values rather than taking either.
	// Picking one would make the statistic depend on which side we favour, and
	// a bin of two sensors — the common small case — would report one device's
	// reading while claiming to summarise both.
	if len(s)%2 == 0 {
		return (s[mid-1] + s[mid]) / 2
	}
	return s[mid]
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// round4 is roughly 11 m of longitude — far finer than the grid, but the value
// is a computed centre and truncating it harder would make neighbouring bins
// look unevenly spaced.
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
