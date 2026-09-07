package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"airbg.org/internal/httpx"
)

// handleSensorLocate resolves one sensor id to a position and an area.
//
// It is what makes /en/#sensor=11338 open on that sensor: the fragment never
// reaches the server, so the page can only ask afterwards, and until this
// existed there was nothing to ask. Answered from the snapshot — no query, so
// the endpoint cannot be turned into a per-id table walk.
//
// Metered by the same breadth budget as the series endpoint, and observed
// BEFORE the lookup for the same reason: a refusal must not first reveal
// whether the sensor exists.
func (d Deps) handleSensorLocate(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "The sensor id must be a positive integer.")
		return
	}

	if !d.Breadth.ObserveSensor(httpx.BucketKeyFrom(r.Context()), id) {
		enumerationTrips.With("sensor").Inc()
		writeTooManySensors(w, d.Config.RateLimit.Enumerate)
		return
	}

	loc, ok := snap.SensorLocations[id]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such sensor.")
		return
	}

	encoded, err := json.Marshal(loc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setCacheControl(w.Header(), cachePrivate, int(d.Config.Cache.DataMaxAge.Seconds()))
	_, _ = w.Write(encoded)
}
