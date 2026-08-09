package quality

import (
	"math"

	"airbg.org/internal/upstream"
)

// NeighbourRadiusMetres is the search radius for the spatial check (spec §6.3).
const NeighbourRadiusMetres = 15000.0

const earthRadiusMetres = 6371000.0

type Scored struct {
	Reading upstream.Reading
	Flag    Flag
}

// Score evaluates a whole poll batch. Neighbour comparison runs in memory over
// the batch rather than against the database: one poll returns every Bulgarian
// sensor at once, so the neighbourhood is already in hand.
//
// Checks run in order and the first failure wins: range, then stuck, then
// spatial. Every input reading appears in the output — readings are flagged,
// never dropped.
func Score(readings []upstream.Reading, hist *History) []Scored {
	// Group by metric so a sensor is only ever compared against the same
	// quantity, and so one bad metric cannot influence another.
	byMetric := make(map[string][]upstream.Reading)
	for _, r := range readings {
		byMetric[r.Metric] = append(byMetric[r.Metric], r)
	}

	// The reference population excludes out-of-range values: a sensor reporting
	// -999 must not drag the neighbourhood median it is being compared against.
	reference := make(map[string][]upstream.Reading, len(byMetric))
	for metric, group := range byMetric {
		valid := make([]upstream.Reading, 0, len(group))
		for _, r := range group {
			if InRange(metric, r.Value) {
				valid = append(valid, r)
			}
		}
		reference[metric] = valid
	}

	out := make([]Scored, 0, len(readings))
	for _, r := range readings {
		out = append(out, Scored{Reading: r, Flag: scoreOne(r, reference[r.Metric], hist)})
	}
	return out
}

func scoreOne(r upstream.Reading, population []upstream.Reading, hist *History) Flag {
	if !InRange(r.Metric, r.Value) {
		return FlagOutOfRange
	}

	hist.Observe(r.SensorID, r.Metric, r.Value)
	if hist.IsStuck(r.SensorID, r.Metric) {
		return FlagStuck
	}

	neighbours := make([]Neighbour, 0, 8)
	for _, other := range population {
		if other.SensorID == r.SensorID {
			continue
		}
		if haversineMetres(r.Lon, r.Lat, other.Lon, other.Lat) > NeighbourRadiusMetres {
			continue
		}
		neighbours = append(neighbours, Neighbour{Lon: other.Lon, Lat: other.Lat, Value: other.Value})
	}
	return SpatialCheck(r.Metric, r.Value, neighbours)
}

// haversineMetres returns great-circle distance. Accurate enough at the 15 km
// scale this is used for, and avoids a database round-trip per reading.
func haversineMetres(lon1, lat1, lon2, lat2 float64) float64 {
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	Δφ := (lat2 - lat1) * math.Pi / 180
	Δλ := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(Δφ/2)*math.Sin(Δφ/2) +
		math.Cos(φ1)*math.Cos(φ2)*math.Sin(Δλ/2)*math.Sin(Δλ/2)
	return 2 * earthRadiusMetres * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
