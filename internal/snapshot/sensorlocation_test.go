package snapshot

import (
	"testing"

	"airbg.org/internal/store"
)

func TestSensorLocationsPickTheSmallestArea(t *testing.T) {
	known := map[string]AreaMeta{
		"sofia":          {Slug: "sofia", Kind: "oblast"},
		"sofia-city":     {Slug: "sofia-city", Kind: "city"},
		"lozenets":       {Slug: "lozenets", Kind: "neighbourhood"},
		"plovdiv-oblast": {Slug: "plovdiv-oblast", Kind: "oblast"},
	}
	locs := sensorLocationsFrom([]store.SensorReading{
		// Neither first nor last in the slice, so neither "take the first" nor
		// "take the last" can pass this by accident: the slugs arrive
		// alphabetically ordered, which says nothing about how big an area is.
		{SensorID: 11338, Lon: 23.31, Lat: 42.69, AreaSlugs: []string{"sofia", "lozenets", "sofia-city"}},
		{SensorID: 22, Lon: 24.75, Lat: 42.14, AreaSlugs: []string{"plovdiv-oblast"}},
	}, known)

	got, ok := locs[11338]
	if !ok {
		t.Fatalf("sensor 11338 missing from %v", locs)
	}
	if got.Slug != "lozenets" {
		t.Errorf("slug = %q, want lozenets", got.Slug)
	}
	if got.Lon != 23.31 || got.Lat != 42.69 {
		t.Errorf("position = %v,%v, want 23.31,42.69", got.Lon, got.Lat)
	}
	if locs[22].Slug != "plovdiv-oblast" {
		t.Errorf("slug for the single-area sensor = %q, want plovdiv-oblast", locs[22].Slug)
	}
}

// A sensor whose areas are all unknown to the snapshot still resolves, without
// a slug: the deep link can fly to the point, and adopting a slug no endpoint
// would serve is worse than adopting none.
func TestSensorLocationsKeepASensorWithNoKnownArea(t *testing.T) {
	locs := sensorLocationsFrom([]store.SensorReading{
		{SensorID: 7, Lon: 25.0, Lat: 43.0, AreaSlugs: []string{"gone"}},
		{SensorID: 8, Lon: 25.1, Lat: 43.1},
	}, map[string]AreaMeta{"sofia": {Slug: "sofia", Kind: "oblast"}})

	for _, id := range []int64{7, 8} {
		got, ok := locs[id]
		if !ok {
			t.Fatalf("sensor %d missing", id)
		}
		if got.Slug != "" {
			t.Errorf("sensor %d slug = %q, want empty", id, got.Slug)
		}
		if got.SensorID != id {
			t.Errorf("id = %d, want %d", got.SensorID, id)
		}
	}
}
