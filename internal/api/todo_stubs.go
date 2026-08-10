package api

import "net/http"

// Temporary. Task 12 replaces handleAreaSeries and handleSensorSeries; Task 13
// replaces handleLocate. Delete this file in Task 13.
func (d Deps) handleAreaSeries(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented.")
}

func (d Deps) handleSensorSeries(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented.")
}

func (d Deps) handleLocate(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented.")
}
