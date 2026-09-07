package api

import (
	"net/http"
	"strings"

	"airbg.org/internal/snapshot"
)

// windowParam is the query parameter that picks an averaging window. One name
// shared by every endpoint that honours it, so a client can carry the reader's
// choice across requests without a per-endpoint spelling.
const windowParam = "window"

// windowed resolves ?window= against the snapshot, or answers 400 and reports
// false.
//
// Refused rather than silently ignored, for the reason the tier parameter is:
// a reader who asked for a week's average and was handed the last five minutes
// has no way to see that they were. An unknown window is a client bug, and a
// 400 is what makes it visible instead of making the map quietly wrong.
func windowed(w http.ResponseWriter, r *http.Request, snap *snapshot.Snapshot) (*snapshot.Snapshot, bool) {
	name := strings.TrimSpace(r.URL.Query().Get(windowParam))
	if !snapshot.KnownWindow(name) {
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "window" parameter must be one of `+windowNames()+`, or absent for the current reading.`)
		return nil, false
	}
	return snap.Window(name), true
}

func windowNames() string {
	names := make([]string, 0, len(snapshot.WindowSpecs))
	for _, s := range snapshot.WindowSpecs {
		names = append(names, `"`+s.Name+`"`)
	}
	return strings.Join(names, ", ")
}
