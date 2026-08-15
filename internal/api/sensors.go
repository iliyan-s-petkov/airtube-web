package api

import (
	"net/http"
	"strconv"

	"airbg.org/internal/config"
	"airbg.org/internal/httpx"
	"airbg.org/internal/metrics"
)

var enumerationTrips = metrics.CounterVec(
	"airbg_enumeration_trips_total",
	"Requests refused by the enumeration-breadth check, by dimension.",
	"dimension")

// handleAreaSensors serves the sensor detail for one area.
//
// This is the only endpoint that returns sensor coordinates, which makes it the
// one worth extracting — so it carries the breadth check.
func (d Deps) handleAreaSensors(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	slug := r.PathValue("slug")

	// Validate against the snapshot's known slugs BEFORE observing. Counting an
	// unknown slug would let a caller exhaust their own area budget with
	// garbage, and — worse — would make the breadth counter trivially
	// pollutable by anyone wanting to trip a shared CGNAT address on purpose.
	body, ok := snap.AreaSensors[slug]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such area.")
		return
	}

	// The check runs before anything is written. A refusal that has already
	// sent the payload has leaked precisely what it was withholding.
	if !d.Breadth.ObserveArea(httpx.BucketKeyFrom(r.Context()), slug) {
		enumerationTrips.With("area").Inc()
		writeTooManyAreas(w, d.Config.RateLimit.Enumerate)
		return
	}

	serveBody(w, r, body, cachePrivate, int(d.Config.Cache.DataMaxAge.Seconds()))
}

// writeTooManyAreas answers an enumeration trip.
//
// The message says what happened without naming the threshold: publishing the
// exact limit tells a scraper precisely how to pace itself just under it.
// Retry-After is generous because the window is an hour and the alternative —
// a tight retry — invites a client to hammer the refusal.
func writeTooManyAreas(w http.ResponseWriter, cfg config.Enumerate) {
	w.Header().Set("Retry-After", strconv.Itoa(int(cfg.RetryAfter.Seconds())))
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		"Too many different areas requested. Please slow down.")
}

func writeTooManySensors(w http.ResponseWriter, cfg config.Enumerate) {
	w.Header().Set("Retry-After", strconv.Itoa(int(cfg.RetryAfter.Seconds())))
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		"Too many different sensors requested. Please slow down.")
}
