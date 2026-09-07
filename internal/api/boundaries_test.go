package api_test

import (
	"net/http"
	"testing"

	"airbg.org/internal/snapshot"
)

func withBoundaries(t *testing.T, json string) *snapshot.Snapshot {
	t.Helper()
	snap := fixture(t)
	snap.Boundaries = snapshot.Body{
		JSON: []byte(json),
		Gzip: []byte("gzipped-" + json),
		ETag: `"boundaries"`,
	}
	return snap
}

func TestBoundariesServesTheOutlines(t *testing.T) {
	body := `{"type":"FeatureCollection","features":[]}`
	rec := serve(t, deps(t, withBoundaries(t, body)), get("/api/v1/boundaries", "203.0.113.40"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != body {
		t.Errorf("body = %q, want the boundaries payload", got)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag; the outlines are the one payload a reader can cache for good")
	}
}

// A snapshot built before the outlines could be read must not answer 200 with
// an empty collection: a client cannot tell that apart from a country with no
// provinces, and would cache the emptiness.
func TestBoundariesIsUnavailableRatherThanEmpty(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/boundaries", "203.0.113.41"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 with no outlines built", rec.Code)
	}
}

// The outlines are one fixed national payload, like the country tier: no bbox,
// no slug, nothing that turns it into a walkable index.
func TestBoundariesTakesNoParameters(t *testing.T) {
	body := `{"type":"FeatureCollection","features":[]}`
	d := deps(t, withBoundaries(t, body))

	plain := serve(t, d, get("/api/v1/boundaries", "203.0.113.42"))
	filtered := serve(t, d, get("/api/v1/boundaries?bbox=22,41,28,44&slug=sofia", "203.0.113.43"))

	if plain.Body.String() != filtered.Body.String() {
		t.Error("a query parameter changed the answer; the outlines must be one fixed payload")
	}
}
