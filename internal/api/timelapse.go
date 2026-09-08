package api

import (
	"net/http"
	"strings"

	"airbg.org/internal/snapshot"
	"airbg.org/internal/upstream"
)

// handleTimelapse serves one animation: a metric's grid over a published span.
// No bounding box and no arbitrary start or end — both parameters name a member
// of a closed list, so every URL is one of a few dozen prepared bodies.
func (d Deps) handleTimelapse(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	if metric == "" {
		metric = d.Snapshots.DefaultMetric()
	}
	if !upstream.IsCanonicalMetric(metric) {
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "metric" parameter must name a published metric.`)
		return
	}

	span := strings.TrimSpace(r.URL.Query().Get("span"))
	if span == "" {
		span = snapshot.FrameSpecs[0].Name
	}
	if !snapshot.KnownSpan(span) {
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "span" parameter must be one of `+spanNames()+`.`)
		return
	}

	// A published pair with no body is a cycle that has not built one: 503, not 400.
	body, ok := snap.TimelapseBody(metric, span)
	if !ok {
		writeUnavailable(w)
		return
	}
	serveBody(w, r, body, cachePublic, int(d.Config.Cache.DataMaxAge.Seconds()))
}

func spanNames() string {
	names := make([]string, 0, len(snapshot.FrameSpecs))
	for _, s := range snapshot.FrameSpecs {
		names = append(names, `"`+s.Name+`"`)
	}
	return strings.Join(names, ", ")
}
