package snapshot

import (
	"testing"
	"time"
)

// TestZeroGeneratedAtCoversEveryPayloadType builds one instance of every
// payload type encode() can be handed, twice, with identical data and a
// different GeneratedAt. If a type's withoutGeneratedAt does not clear the
// timestamp — or is missing entirely, which is a compile error rather than a
// case reaching this loop — the two ETags diverge.
//
// This is a table over concrete constructors rather than one type-switch
// input, so adding a payload type here is required for it to be exercised at
// all: there is no default branch left to fall through silently.
func TestZeroGeneratedAtCoversEveryPayloadType(t *testing.T) {
	t1 := time.Unix(1_800_000_000, 0).UTC()
	t2 := time.Unix(1_800_000_300, 0).UTC()
	// dataTime stands in for a payload's own data-carried timestamps —
	// ValidAt, Times, a frame's T — which must NOT move between t1 and t2:
	// only the build timestamp does. Fixed so a case that forgets and hangs
	// data off `now` instead fails honestly rather than by coincidence.
	dataTime := time.Unix(1_700_000_000, 0).UTC()

	cases := map[string]func(now time.Time) canonicalisable{
		"areaPayload": func(now time.Time) canonicalisable {
			return areaPayload{
				GeneratedAt: now,
				Areas:       []areaPayloadEntry{{Slug: "sofia", Kind: "oblast", Values: map[string]float64{"P1": 12}}},
			}
		},
		"sensorPayload": func(now time.Time) canonicalisable {
			return sensorPayload{
				GeneratedAt: now,
				Sensors:     sensorColumns{ID: []int64{1}, Type: []string{"SDS011"}},
			}
		},
		"hexPayload": func(now time.Time) canonicalisable {
			return hexPayload{
				GeneratedAt: now, ResolutionKM: HexResolutionKM,
				Hexes: []hexEntry{{Lon: 23.3, Lat: 42.6, N: 1}},
			}
		},
		"boundaryPayload": func(now time.Time) canonicalisable {
			// Carries no GeneratedAt at all; withoutGeneratedAt has nothing to
			// clear, but it must still exist and be called through the
			// interface, or this case would not compile.
			return boundaryPayload{Type: "FeatureCollection"}
		},
		"windPayload": func(now time.Time) canonicalisable {
			return windPayload{GeneratedAt: now, ValidAt: dataTime, Model: "ecmwf_ifs025"}
		},
		"SeriesPayload": func(now time.Time) canonicalisable {
			// Also carries no GeneratedAt; Times are the payload's own data,
			// not the build timestamp, and must not be cleared.
			return SeriesPayload{Slug: "sofia", Metric: "P2", Times: []time.Time{dataTime}, Values: []float64{1}}
		},
		"timelapsePayload": func(now time.Time) canonicalisable {
			return timelapsePayload{
				GeneratedAt: now, Metric: "P2", Span: "24h",
				Frames: []timelapseFrame{{T: dataTime}},
			}
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			a, err := encode(build(t1))
			if err != nil {
				t.Fatalf("encode a: %v", err)
			}
			b, err := encode(build(t2))
			if err != nil {
				t.Fatalf("encode b: %v", err)
			}
			if a.ETag != b.ETag {
				t.Errorf("%s: ETag changed between identical data at two build times (%s vs %s); withoutGeneratedAt is not clearing the timestamp", name, a.ETag, b.ETag)
			}
		})
	}
}
