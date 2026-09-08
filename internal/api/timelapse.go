package api

import (
	"net/http"
	"strings"

	"airbg.org/internal/snapshot"
	"airbg.org/internal/upstream"
)

// handleTimelapse serves one animation: a metric's grid over a published span,
// as a cell list and a frame per step.
//
// No bounding box, for the same reason /overview has none — a box would let a
// caller walk the country — and no arbitrary start or end. Both parameters name
// a member of a closed list, so every distinct URL is one of a few dozen bodies
// prepared at build time and shared by every reader, which is what keeps the
// response publicly cacheable and the database out of the request path.
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

	// A known metric and a known span with no body is a cycle that has not
	// built one yet, which is the 503 case and not the 400 one: the caller
	// asked a question we publish, and the answer is not ready.
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
