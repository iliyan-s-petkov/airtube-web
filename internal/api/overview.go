package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"airbg.org/internal/store"
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

	switch r.URL.Query().Get("tier") {
	case "", "country":
		serveBody(w, r, snap.Overview, dataMaxAge)
	case "city":
		serveBody(w, r, snap.OverviewCity, dataMaxAge)
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
	serveBody(w, r, snap.Areas, dataMaxAge)
}

type metaBody struct {
	GeneratedAt         time.Time `json:"generated_at"`
	CoverageThreshold   int       `json:"coverage_threshold"`
	Metrics             []string  `json:"metrics"`
	AreaCount           int       `json:"area_count"`
	CoveredAreaCount    int       `json:"covered_area_count"`
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
		CoverageThreshold:   store.CoverageThreshold,
		Metrics:             upstream.CanonicalMetrics(),
		AreaCount:           len(snap.KnownSlugs),
		CoveredAreaCount:    covered,
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
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(dataMaxAge))
	_, _ = w.Write(body)
}

func (d Deps) handleScales(w http.ResponseWriter, r *http.Request) {
	body, err := json.Marshal(Scales())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(scalesMaxAge))
	_, _ = w.Write(body)
}
