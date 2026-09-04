package snapshot

import (
	"math"
	"sort"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// HexResolutionKM is the centre-to-centre spacing of the hex grid, in
// kilometres. It is a constant and never a query parameter: a caller who could
// choose the resolution could ask for one metre and get the sensor list back,
// which is exactly the extraction the tiered API exists to prevent (Phase 1
// §7.1). Every visitor gets the same grid, so the response stays publicly
// cacheable.
const HexResolutionKM = 15.0

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
func hexPayloadFrom(now time.Time, sensors []store.SensorReading) hexPayload {
	bins := make(map[axial]*hexBin)
	for _, sr := range sensors {
		c := hexOf(sr.Lon, sr.Lat)
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
		ResolutionKM: HexResolutionKM,
		Hexes:        make([]hexEntry, 0, len(ordered)),
	}
	for _, b := range ordered {
		lon, lat := hexCentre(b.coord)
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

// hexSizeKM is the hex circumradius that produces HexResolutionKM centre-to-
// centre spacing on a pointy-top grid, where horizontal spacing is √3·size.
var hexSizeKM = HexResolutionKM / math.Sqrt(3)

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
func hexOf(lon, lat float64) axial {
	x, y := project(lon, lat)
	q := (math.Sqrt(3)/3*x - y/3) / hexSizeKM
	r := (2.0 / 3.0 * y) / hexSizeKM
	return cubeRound(q, r)
}

func hexCentre(c axial) (lon, lat float64) {
	x := hexSizeKM * (math.Sqrt(3)*float64(c.q) + math.Sqrt(3)/2*float64(c.r))
	y := hexSizeKM * (1.5 * float64(c.r))
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
