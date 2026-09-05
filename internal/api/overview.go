package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"airbg.org/internal/snapshot"
	"airbg.org/internal/upstream"
)

// Attribution strings. Both are licence obligations under ODbL, not niceties.
const (
	DataAttribution     = "Data from sensor.community contributors, ODbL 1.0"
	BoundaryAttribution = "Boundaries © OpenStreetMap contributors, ODbL 1.0"
)

// handleOverview serves one choropleth tier.
//
// There is deliberately no bounding-box parameter. The tier is the ONLY spatial
// control a caller has, and that is the whole anti-extraction design from Phase
// 1 §7.1: a bbox would let a scraper walk the country in a loop, and no rate
// limit distinguishes that from normal panning.
func (d Deps) handleOverview(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	dataMaxAge := int(d.Config.Cache.DataMaxAge.Seconds())
	switch r.URL.Query().Get("tier") {
	case "", "country":
		serveBody(w, r, snap.Overview, cachePublic, dataMaxAge)
	case "city":
		serveBody(w, r, snap.OverviewCity, cachePublic, dataMaxAge)
	default:
		// Explicit 400 rather than falling back to the country tier: quietly
		// answering a different question than the one asked hides frontend bugs
		// and makes the API's contract untestable.
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "tier" parameter must be "country" or "city".`)
	}
}

func (d Deps) handleAreas(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}
	serveBody(w, r, snap.Areas, cachePublic, int(d.Config.Cache.DataMaxAge.Seconds()))
}

// handleHexes serves the aggregate hex grid, at a requested resolution and
// optionally clipped to a viewport.
//
// Both parameters were once refused. The resolution is now accepted because it
// is snapped onto a closed list of tiers that share one nested lattice, so a
// caller cannot invent a finer grid than we publish and cannot learn anything
// by comparing tiers — see snapshot.HexResolutionKM for why that reasoning
// changed, and what it does and does not buy.
//
// Neither parameter carries anything per-caller, so the response stays public:
// two callers asking the same question get the same bytes and the same ETag.
//
// The viewport is a bandwidth optimisation on every tier but one. At
// resolution_km=0 it becomes a requirement, because that tier serves individual
// sensors with their ids rather than bins, and the box is what keeps a bulk
// download a walk the rate limiter can see rather than a single request.
func (d Deps) handleHexes(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	// Parsed, not validated: HexBody snaps whatever it is given onto a published
	// tier, so an out-of-range number needs no handling here and an unparseable
	// one just means the caller named no resolution.
	res := snapshot.HexResolutionKM
	if f, err := strconv.ParseFloat(r.URL.Query().Get("resolution_km"), 64); err == nil {
		res = f
	}
	bb, clip := snapshot.ParseBBox(r.URL.Query().Get("bbox"))

	// The point tier is the one request that is refused rather than snapped.
	// Everywhere else a clumsy parameter still yields a usable map, because the
	// answer is an aggregate and being handed a coarser one costs the caller
	// nothing. Here it would be the opposite mistake: falling through would
	// serve every sensor in the country, ids attached, to a caller who asked for
	// a viewport. See snapshot.PointBody.
	if res == snapshot.PointResolutionKM {
		if !clip {
			writeError(w, http.StatusBadRequest, "bad_request",
				`A "bbox" of "w,s,e,n" is required at resolution_km=0.`)
			return
		}
		body, err := snap.PointBody(bb)
		if err != nil {
			writeUnavailable(w)
			return
		}
		serveBody(w, r, body, cachePublic, int(d.Config.Cache.DataMaxAge.Seconds()))
		return
	}

	body, err := snap.HexBody(res, bb, clip)
	if err != nil {
		writeUnavailable(w)
		return
	}
	serveBody(w, r, body, cachePublic, int(d.Config.Cache.DataMaxAge.Seconds()))
}

// handleWind serves the forecast overlay.
//
// An empty body is 503, not 200 with no vectors: the layer is optional and
// externally sourced, so "we have no forecast" is a real state, and a client
// that cannot tell it apart from "the wind is nowhere" would draw a calm map
// over a windy country. See docs/wind-overlay.md.
func (d Deps) handleWind(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil || snap.Wind.JSON == nil {
		writeUnavailable(w)
		return
	}
	serveBody(w, r, snap.Wind, cachePublic, int(d.Config.Cache.DataMaxAge.Seconds()))
}

type metaBody struct {
	GeneratedAt         time.Time `json:"generated_at"`
	CoverageThreshold   int       `json:"coverage_threshold"`
	Metrics             []string  `json:"metrics"`
	AreaCount           int       `json:"area_count"`
	CoveredAreaCount    int       `json:"covered_area_count"`
	CellStatistic       string    `json:"cell_statistic"`
	CellStatChangedAt   time.Time `json:"cell_statistic_changed_at"`
	Attribution         string    `json:"attribution"`
	BoundaryAttribution string    `json:"boundary_attribution"`
	Disclaimer          string    `json:"disclaimer"`
}

// handleMeta tells a client how to interpret everything else: when the data was
// built, what the coverage rule is, which metrics exist, and who to credit.
//
// covered_area_count next to area_count is the honest pair. Reporting only the
// total would let a UI imply the whole country is measured when most oblasti sit
// below the 3-sensor threshold.
func (d Deps) handleMeta(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	covered := 0
	for _, m := range snap.KnownSlugs {
		if m.Covered {
			covered++
		}
	}

	body, err := json.Marshal(metaBody{
		GeneratedAt:         snap.GeneratedAt,
		CoverageThreshold:   d.Config.Store.CoverageThreshold,
		Metrics:             upstream.CanonicalMetrics(),
		AreaCount:           len(snap.KnownSlugs),
		CoveredAreaCount:    covered,
		CellStatistic:       "median",
		CellStatChangedAt:   snapshot.CellStatChangedAt,
		Attribution:         DataAttribution,
		BoundaryAttribution: BoundaryAttribution,
		Disclaimer: "Low-cost sensor readings are indicative and are not " +
			"reference-method measurements.",
	})
	if err != nil {
		// Marshalling fixed-shape structs cannot realistically fail, but
		// swallowing the error would send a 200 with an empty body.
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setCacheControl(w.Header(), cachePublic, int(d.Config.Cache.DataMaxAge.Seconds()))
	_, _ = w.Write(body)
}

func (d Deps) handleScales(w http.ResponseWriter, r *http.Request) {
	body, err := json.Marshal(Scales())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setCacheControl(w.Header(), cachePublic, int(d.Config.Cache.ScalesMaxAge.Seconds()))
	_, _ = w.Write(body)
}
