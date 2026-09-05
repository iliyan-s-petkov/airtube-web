package snapshot

import (
	"math"
	"sort"
	"strconv"
	"strings"
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
var HexTiersKM = []float64{15, 5, 2, 1, 0.5, 0.25}

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

// hexPayload is the aggregate map tier: a fixed grid of bins, each carrying a
// count and mean values, and no sensor identity at all. It is the coarsest of
// the three spatial tiers — coarser than the area choropleth, which at least
// names its areas.
type hexPayload struct {
	GeneratedAt  time.Time  `json:"generated_at"`
	ResolutionKM float64    `json:"resolution_km"`
	Hexes        []hexEntry `json:"hexes"`
}

type hexEntry struct {
	Lon     float64            `json:"lon"`
	Lat     float64            `json:"lat"`
	N       int                `json:"n"`
	Country string             `json:"country"`
	Values  map[string]float64 `json:"values"`
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
	if !clip {
		return encode(p)
	}
	out := hexPayload{GeneratedAt: p.GeneratedAt, ResolutionKM: p.ResolutionKM,
		Hexes: make([]hexEntry, 0, len(p.Hexes))}
	for _, h := range p.Hexes {
		if bb.contains(h.Lon, h.Lat) {
			out.Hexes = append(out.Hexes, h)
		}
	}
	return encode(out)
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

// contains reports whether a bin centre falls in the box.
//
// The bin CENTRE, not the bin: a cell straddling the edge is included only if
// its centre is inside. Testing the whole cell would make the answer depend on
// the resolution, so panning at one zoom and another would disagree about the
// same ground.
func (b BBox) contains(lon, lat float64) bool {
	return lon >= b.W && lon <= b.E && lat >= b.S && lat <= b.N
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
	sums  map[string]float64
	count map[string]int
	// countries counts sensors per country code. A 15 km bin straddling a
	// border holds sensors from both, and the bin has to name one; the modal
	// value names the country most of the bin's data actually came from.
	countries map[string]int
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
			b = &hexBin{coord: c, sums: map[string]float64{},
				count: map[string]int{}, countries: map[string]int{}}
			bins[c] = b
		}
		b.n++
		if sr.Country != "" {
			b.countries[sr.Country]++
		}
		for _, m := range upstream.CanonicalMetrics() {
			if v, ok := sr.Values[m]; ok {
				b.sums[m] += v
				b.count[m]++
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
		values := make(map[string]float64, len(b.sums))
		for m, sum := range b.sums {
			// A metric absent from every sensor in the bin is absent from the
			// bin, rather than present as zero: 0 µg/m³ is a reading.
			if n := b.count[m]; n > 0 {
				values[m] = round1(sum / float64(n))
			}
		}
		p.Hexes = append(p.Hexes, hexEntry{
			Lon:     round4(lon),
			Lat:     round4(lat),
			N:       b.n,
			Country: b.modalCountry(),
			Values:  values,
		})
	}
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
		ry = -rx - rz
	default:
		rz = -rx - ry
	}
	return axial{q: int(rx), r: int(rz)}
}

func radians(d float64) float64 { return d * math.Pi / 180 }
func degrees(r float64) float64 { return r * 180 / math.Pi }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// round4 is roughly 11 m of longitude — far finer than the grid, but the value
// is a computed centre and truncating it harder would make neighbouring bins
// look unevenly spaced.
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
