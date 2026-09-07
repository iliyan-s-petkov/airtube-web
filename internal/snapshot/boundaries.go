package snapshot

import (
	"encoding/json"

	"airbg.org/internal/store"
)

// boundaryPayload is the province outlines, shaped as GeoJSON so a client can
// hand it to a map source unchanged.
//
// It carries no build time and no readings. The outlines do not move, so two
// builds produce identical bytes and a reader who has them cached revalidates
// with a 304 instead of refetching them every snapshot cycle.
type boundaryPayload struct {
	Type     string            `json:"type"`
	Features []boundaryFeature `json:"features"`
}

type boundaryFeature struct {
	Type       string             `json:"type"`
	Properties boundaryProperties `json:"properties"`
	// The geometry PostGIS produced, passed through verbatim.
	Geometry json.RawMessage `json:"geometry"`
}

type boundaryProperties struct {
	Slug   string `json:"slug"`
	NameBG string `json:"name_bg"`
	NameEN string `json:"name_en"`
}

func boundaryPayloadFrom(bs []store.AreaBoundary) boundaryPayload {
	p := boundaryPayload{
		Type:     "FeatureCollection",
		Features: make([]boundaryFeature, 0, len(bs)),
	}
	for _, b := range bs {
		p.Features = append(p.Features, boundaryFeature{
			Type: "Feature",
			Properties: boundaryProperties{
				Slug:   b.Slug,
				NameBG: b.NameBG,
				NameEN: b.NameEN,
			},
			Geometry: json.RawMessage(b.GeoJSON),
		})
	}
	return p
}
