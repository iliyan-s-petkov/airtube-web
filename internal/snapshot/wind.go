package snapshot

import (
	"time"

	"airbg.org/internal/store"
)

// windPayload is the forecast overlay. Every field above Vectors exists to keep
// the layer honest about what it is. See docs/wind-overlay.md.
type windPayload struct {
	GeneratedAt time.Time `json:"generated_at"`
	// ValidAt is the forecast hour, which is not GeneratedAt: a build at 14:20
	// serves the 14:00 forecast.
	ValidAt time.Time `json:"valid_at"`
	// Model and ModelResolutionDeg are rendered in the overlay's label. The
	// model's grid is coarser than ours, so neighbouring arrows repeat; naming
	// the grid is what tells a reader that is upsampling and not real
	// uniformity.
	Model              string       `json:"model"`
	ModelResolutionDeg float64      `json:"model_resolution_deg"`
	ResolutionKM       float64      `json:"resolution_km"`
	Forecast           bool         `json:"forecast"`
	Vectors            []windVector `json:"vectors"`
}

type windVector struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
	// SpeedMS is metres per second, and DirectionDeg is the meteorological
	// convention Open-Meteo reports: the direction the wind blows FROM. The
	// arrow points the opposite way, which is the renderer's job and is pinned
	// there rather than converted here — one convention, named once.
	SpeedMS      float64 `json:"speed_ms"`
	DirectionDeg float64 `json:"direction_deg"`
}

func windPayloadFrom(now, validAt time.Time, model string, modelResDeg float64, vs []store.WindVector) windPayload {
	p := windPayload{
		GeneratedAt:        now,
		ValidAt:            validAt,
		Model:              model,
		ModelResolutionDeg: modelResDeg,
		ResolutionKM:       HexResolutionKM,
		// A constant true in the payload, so a client cannot render this layer
		// without having been told what it is.
		Forecast: true,
		Vectors:  make([]windVector, 0, len(vs)),
	}
	for _, v := range vs {
		// The default resolution, matching HexGridOf: these coordinates were
		// asked of the met model on that grid, so reading them back on a finer
		// one would move the arrows off the cells they describe.
		lon, lat := hexCentre(axial{q: v.Q, r: v.R}, HexResolutionKM)
		p.Vectors = append(p.Vectors, windVector{
			Lon:          round4(lon),
			Lat:          round4(lat),
			SpeedMS:      round1(v.SpeedMS),
			DirectionDeg: round1(v.Direction),
		})
	}
	return p
}
