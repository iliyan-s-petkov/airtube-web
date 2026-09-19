package snapshot

import "encoding/json"

// Contract is the set of server constants the frontend generates from, written
// by `go run ./cmd/airbg contract` to web/src/lib/contract.json. Arrays and
// scalars only: a map would marshal in a different order on every run, which
// would make the generated file unstable across identical source.
type Contract struct {
	Spans      []ContractSpan   `json:"spans"`
	Windows    []ContractWindow `json:"windows"`
	LiveWindow string           `json:"live_window"`
	Hex        ContractHex      `json:"hex"`
}

// ContractSpan matches api.spanMeta's JSON shape byte for byte, so the
// build-time contract and the runtime /api/v1/meta answer speak one
// vocabulary. spanMeta uses "span" for the name; matched here rather than
// renamed there, because the API field is already shipped.
type ContractSpan struct {
	Span        string `json:"span"`
	StepSeconds int    `json:"step_seconds"`
	Frames      int    `json:"frames"`
}

type ContractWindow struct {
	Name    string `json:"name"`
	Seconds int    `json:"seconds"`
}

type ContractHex struct {
	TiersKM           []float64 `json:"tiers_km"`
	PointResolutionKM float64   `json:"point_resolution_km"`
	BBoxQuantumDeg    float64   `json:"bbox_quantum_deg"`
	MaxPointBBoxDeg   float64   `json:"max_point_bbox_deg"`
	RefLat            float64   `json:"ref_lat"`
	EarthRadiusKM     float64   `json:"earth_radius_km"`
}

// NewContract builds the contract from the values this package owns. Every
// slice is built by ranging a Go slice whose order is source order, never a
// map, which is what keeps the marshalled bytes identical across runs.
func NewContract() Contract {
	spans := make([]ContractSpan, 0, len(FrameSpecs))
	for _, s := range FrameSpecs {
		spans = append(spans, ContractSpan{
			Span:        s.Name,
			StepSeconds: int(s.Step.Seconds()),
			Frames:      int(s.Dur / s.Step),
		})
	}

	windows := make([]ContractWindow, 0, len(WindowSpecs))
	for _, w := range WindowSpecs {
		windows = append(windows, ContractWindow{Name: w.Name, Seconds: int(w.Dur.Seconds())})
	}

	tiers := make([]float64, len(HexTiersKM))
	copy(tiers, HexTiersKM)

	return Contract{
		Spans:      spans,
		Windows:    windows,
		LiveWindow: LiveWindow,
		Hex: ContractHex{
			TiersKM:           tiers,
			PointResolutionKM: PointResolutionKM,
			BBoxQuantumDeg:    BBoxQuantumDegrees,
			MaxPointBBoxDeg:   MaxPointBBoxDegrees,
			RefLat:            hexRefLat,
			EarthRadiusKM:     earthRadiusKM,
		},
	}
}

// Marshal renders the contract as the bytes cmd/airbg writes and
// TestContractJSONIsCurrent compares against: two-space indent, a trailing
// newline, no timestamp — so the file's identity is its content and nothing
// about when or where it ran.
func (c Contract) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
