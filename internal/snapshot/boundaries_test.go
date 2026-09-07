package snapshot

import (
	"encoding/json"
	"testing"

	"airbg.org/internal/store"
)

func testBoundaries() []store.AreaBoundary {
	return []store.AreaBoundary{
		{Slug: "plovdiv", NameBG: "Пловдив", NameEN: "Plovdiv",
			GeoJSON: []byte(`{"type":"Polygon","coordinates":[[[24.7,42.1],[24.8,42.1],[24.8,42.2],[24.7,42.1]]]}`)},
		{Slug: "sofia", NameBG: "София", NameEN: "Sofia",
			GeoJSON: []byte(`{"type":"Polygon","coordinates":[[[23.3,42.7],[23.4,42.7],[23.4,42.8],[23.3,42.7]]]}`)},
	}
}

func TestBoundaryPayloadIsAFeatureCollectionMapLibreCanTakeUnchanged(t *testing.T) {
	raw, err := json.Marshal(boundaryPayloadFrom(testBoundaries()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got struct {
		Type     string `json:"type"`
		Features []struct {
			Type       string `json:"type"`
			Properties struct {
				Slug   string `json:"slug"`
				NameBG string `json:"name_bg"`
				NameEN string `json:"name_en"`
			} `json:"properties"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != "FeatureCollection" {
		t.Fatalf("got type %q, want FeatureCollection", got.Type)
	}
	if len(got.Features) != 2 {
		t.Fatalf("got %d features, want 2", len(got.Features))
	}
	f := got.Features[0]
	if f.Type != "Feature" {
		t.Fatalf("got feature type %q, want Feature", f.Type)
	}
	if f.Properties.Slug != "plovdiv" {
		t.Fatalf("got slug %q, want plovdiv", f.Properties.Slug)
	}
	if f.Properties.NameBG != "Пловдив" || f.Properties.NameEN != "Plovdiv" {
		t.Fatalf("got names %q/%q", f.Properties.NameBG, f.Properties.NameEN)
	}
	// The geometry PostGIS produced, not one this package re-encoded.
	if f.Geometry.Type != "Polygon" || len(f.Geometry.Coordinates) == 0 {
		t.Fatalf("got geometry %q with %d bytes of coordinates", f.Geometry.Type, len(f.Geometry.Coordinates))
	}
}

// The outlines do not move, so nothing measured belongs in this payload. A
// build time or a reading would put a five-minute-fresh value behind an ETag
// that is meant never to change, and cost every reader a refetch per cycle.
func TestBoundaryPayloadCarriesNothingThatMovesWithTheClock(t *testing.T) {
	raw, err := json.Marshal(boundaryPayloadFrom(testBoundaries()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for k := range top {
		switch k {
		case "type", "features":
		default:
			t.Fatalf("payload carries unexpected top-level key %q", k)
		}
	}

	var got struct {
		Features []struct {
			Properties map[string]any `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, f := range got.Features {
		for k := range f.Properties {
			switch k {
			case "slug", "name_bg", "name_en":
			default:
				t.Fatalf("feature carries unexpected property %q", k)
			}
		}
	}
}

func TestBoundaryPayloadKeepsItsETagAcrossBuilds(t *testing.T) {
	first, err := encode(boundaryPayloadFrom(testBoundaries()))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	second, err := encode(boundaryPayloadFrom(testBoundaries()))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	if first.ETag != second.ETag {
		t.Fatalf("ETag moved between builds: %s then %s", first.ETag, second.ETag)
	}
}

func TestBoundaryPayloadIsEmptyRatherThanNullWithNoAreas(t *testing.T) {
	raw, err := json.Marshal(boundaryPayloadFrom(nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// "features":null would make a client that trusts the type crash on a
	// database with no areas imported yet.
	var got struct {
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Features == nil {
		t.Fatal("features is null; want an empty array")
	}
}
