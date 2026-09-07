package snapshot

import "airbg.org/internal/store"

// SensorLocation is where one sensor is and which area page owns it.
//
// It exists for the deep link: /en/#sensor=11338 carries the id in a fragment,
// which never reaches the server, so a page opened on that URL has nothing but
// an id and would otherwise draw the whole country. No values here — the
// existing endpoints serve those; this answers only "where do I point".
type SensorLocation struct {
	SensorID int64   `json:"id"`
	Lon      float64 `json:"lon"`
	Lat      float64 `json:"lat"`
	// Slug is the smallest known area containing the sensor, or empty when
	// none of its areas are in this snapshot.
	Slug string `json:"slug"`
}

// areaKindRank ranks an area kind by how tightly it wraps a sensor: a sensor
// sits in an oblast and usually in a city and a neighbourhood too, and the deep
// link wants the smallest, because that is the page whose sensor list holds it.
var areaKindRank = map[string]int{"oblast": 1, "city": 2, "neighbourhood": 3}

func sensorLocationsFrom(sensors []store.SensorReading, known map[string]AreaMeta) map[int64]SensorLocation {
	out := make(map[int64]SensorLocation, len(sensors))
	for _, sr := range sensors {
		loc := SensorLocation{SensorID: sr.SensorID, Lon: sr.Lon, Lat: sr.Lat}
		best := 0
		for _, slug := range sr.AreaSlugs {
			meta, ok := known[slug]
			if !ok {
				continue
			}
			if rank := areaKindRank[meta.Kind]; rank > best {
				best, loc.Slug = rank, slug
			}
		}
		out[sr.SensorID] = loc
	}
	return out
}
