package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"airbg.org/internal/api"
)

// A hex request never fails on its parameters. The resolution is snapped onto a
// published tier and a garbled viewport is discarded, so every one of these is a
// 200 with a usable grid rather than a 400 and a blank map — the opposite of the
// /overview tier parameter, which is an explicit 400 because a wrong tier means
// a frontend bug rather than a stale bookmark.
func TestHexesToleratesAnyParameters(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	urls := []string{
		"/api/v1/hexes",
		"/api/v1/hexes?resolution_km=0.25",
		"/api/v1/hexes?resolution_km=99999",
		"/api/v1/hexes?resolution_km=-1",
		"/api/v1/hexes?resolution_km=abc",
		"/api/v1/hexes?resolution_km=",
		"/api/v1/hexes?bbox=22,41,24,43",
		"/api/v1/hexes?bbox=nonsense",
		"/api/v1/hexes?bbox=24,43,22,41",
		"/api/v1/hexes?resolution_km=1&bbox=22,41,24,43",
	}
	for _, u := range urls {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, u, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", u, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", u)
		}
	}
}

// The response must stay shared-cacheable: neither parameter carries anything
// about the caller, so a proxy holding one copy per URL is correct.
func TestHexesStaysPubliclyCacheable(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/hexes?resolution_km=0.5&bbox=22,41,24,43", nil))

	if cc := rec.Header().Get("Cache-Control"); cc == "" || !containsPublic(cc) {
		t.Errorf("Cache-Control = %q, want a public directive", cc)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag on a hex response")
	}
}

func containsPublic(s string) bool {
	for i := 0; i+6 <= len(s); i++ {
		if s[i:i+6] == "public" {
			return true
		}
	}
	return false
}
