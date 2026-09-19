package snapshot

import "airbg.org/internal/store"

// stationKey identifies one physical site a sensor stands at: the exact
// published coordinate AND the network that published it.
//
// Source is part of the key because the two networks are not one address
// book: sensor.community coordinates come from whoever registered the device,
// EEA coordinates come from the official station survey, and the two are
// never the same reading of the same hardware. A community sensor and an
// official station that land on the same coordinate are two independent
// devices operated by two different bodies, not two ids for one box — and
// pointsFrom already treats them that way (see its "two networks at one
// coordinate" comment). stationIDs must agree, or the sensor catalogue and
// the point tier report a different station count for the same ground.
type stationKey struct {
	lon, lat float64
	source   string
}

// stationKeyOf derives sr's station identity, in the one place both
// stationIDs (build.go) and pointsFrom (hexes.go) now read it from.
func stationKeyOf(sr store.SensorReading) stationKey {
	return stationKey{lon: sr.Lon, lat: sr.Lat, source: sourceOf(sr)}
}
