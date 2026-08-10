package api

import "net/http"

// Temporary. Task 13 replaces handleLocate and deletes this file.
func (d Deps) handleLocate(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented.")
}
