package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// stubSource satisfies api.DataSource without a database. The api package's
// tests must not need a container: they are about HTTP semantics, and a
// container per test would make them slow enough to be skipped.
type stubSource struct {
	slug   string
	points []store.Point
	err    error
}

func (s stubSource) AreaAtPoint(_ context.Context, _, _ float64) (string, error) {
	return s.slug, s.err
}

func (s stubSource) SensorSeries(_ context.Context, _ int64, _ string, _ time.Time, _ bool) ([]store.Point, error) {
	return s.points, s.err
}

func (s stubSource) AreaSeries(_ context.Context, _, _ string, _ time.Time, _ bool) ([]store.Point, error) {
	return s.points, s.err
}

func deps(t *testing.T, snap *snapshot.Snapshot) api.Deps {
	t.Helper()
	h := snapshot.NewHolder()
	if snap != nil {
		h.Store(snap)
	}
	return api.Deps{
		Snapshots: h,
		Breadth:   ratelimit.NewBreadth(ratelimit.DistinctAreaLimit, ratelimit.DistinctSensorLimit, time.Hour),
		Store:     stubSource{slug: "sofia"},
		BaseURL:   "https://airbg.org",
		// An explicit per-test series bucket. Left nil, NewRouter substitutes the
		// process-wide default, and every test in the binary would then share one
		// bucket — so a test that spends its burst would 429 an unrelated test
		// using the same client IP. The nil path is covered deliberately by
		// TestNilSeriesLimiterStillFailsClosed.
		SeriesLimiter: api.NewSeriesLimiter(),
	}
}

// fixture builds a minimal but complete snapshot: one known area with a body.
func fixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	body := func(s string) snapshot.Body {
		return snapshot.Body{JSON: []byte(s), Gzip: []byte("gzipped-" + s), ETag: `"` + s + `"`}
	}
	return &snapshot.Snapshot{
		GeneratedAt:  time.Unix(1_800_000_000, 0).UTC(),
		Overview:     body(`{"areas":[{"slug":"sofia"}]}`),
		OverviewCity: body(`{"areas":[{"slug":"sofia-center"}]}`),
		Areas:        body(`{"areas":[{"slug":"sofia"}]}`),
		AreaSensors: map[string]snapshot.Body{
			"sofia": body(`{"sensors":{"id":[1]}}`),
		},
		KnownSlugs: map[string]snapshot.AreaMeta{
			"sofia": {Slug: "sofia", Kind: "oblast", NameBG: "София", NameEN: "Sofia",
				CentroidLon: 23.32, CentroidLat: 42.69, DefaultZoom: 9, Covered: true, SensorCount: 5},
		},
	}
}

// TestErrorResponsesShareOneShape. A client cannot handle failures it cannot
// parse. More importantly, an envelope that sometimes carries a Go error string
// leaks internals — so message is always a fixed human sentence and code is
// always a fixed machine token.
func TestErrorResponsesShareOneShape(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	cases := []struct {
		path   string
		status int
		code   string
	}{
		{"/api/v1/area/nope/sensors", http.StatusNotFound, "not_found"},
		{"/api/v1/sensor/abc/series", http.StatusBadRequest, "bad_request"},
		{"/api/partner/v1/anything", http.StatusNotImplemented, "not_implemented"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))

		if rec.Code != c.status {
			t.Errorf("%s: status = %d, want %d (body: %s)", c.path, rec.Code, c.status, rec.Body.String())
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type = %q, want application/json", c.path, ct)
		}

		var got struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Errorf("%s: body is not JSON: %v (%s)", c.path, err, rec.Body.String())
			continue
		}
		if got.Error != c.code {
			t.Errorf("%s: error = %q, want %q", c.path, got.Error, c.code)
		}
		if got.Message == "" {
			t.Errorf("%s: message is empty", c.path)
		}
	}
}

// TestUnknownSlugIsNotFoundNotEmpty: an unknown slug must be 404, never a 200
// with an empty list. Serving 200-with-nothing for a typo is the same class of
// bug as reporting success while storing nothing — the caller cannot tell "no
// such place" from "nothing measured here".
func TestUnknownSlugIsNotFoundNotEmpty(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/area/atlantis/sensors", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown slug", rec.Code)
	}
}

func TestETagProduces304(t *testing.T) {
	snap := fixture(t)
	mux := api.NewRouter(deps(t, snap))

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first response")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	mux.ServeHTTP(second, req)

	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carries a %d-byte body; RFC 9110 forbids one", second.Body.Len())
	}
	if second.Header().Get("ETag") != etag {
		t.Error("the 304 does not repeat the ETag; a client cannot then revalidate again")
	}
}

// TestStaleETagIsIgnored: a client holding an old ETag must get fresh data, not
// a 304. A helper that answered 304 for any If-None-Match at all would pin every
// returning visitor to whatever they first saw — permanently stale, and
// invisible in tests that only ever send a matching ETag.
func TestStaleETagIsIgnored(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.Header.Set("If-None-Match", `"something-else"`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a non-matching If-None-Match", rec.Code)
	}
}

// TestGzipIsServedOnlyWhenAccepted. Sending gzip to a client that did not
// advertise it produces unreadable bytes under a 200 — a corrupt success.
func TestGzipIsServedOnlyWhenAccepted(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	plain := httptest.NewRecorder()
	mux.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))
	if enc := plain.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q with no Accept-Encoding, want empty", enc)
	}
	if plain.Body.String() != `{"areas":[{"slug":"sofia"}]}` {
		t.Errorf("plain body = %q", plain.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	zipped := httptest.NewRecorder()
	mux.ServeHTTP(zipped, req)
	if enc := zipped.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Content-Encoding = %q with Accept-Encoding: gzip, want gzip", enc)
	}
	if got := zipped.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Error("Vary does not list Accept-Encoding; a shared cache would then serve gzip to a client that cannot decode it")
	}
}

// TestNoSnapshotIs503: before the first ingest cycle the service has no data.
// It must say so, not serve an empty country as though it had been measured.
func TestNoSnapshotIs503(t *testing.T) {
	mux := api.NewRouter(deps(t, nil))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 before the first snapshot", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("the 503 has no Retry-After")
	}
}

// TestPostIsMethodNotAllowed. Go 1.22+ ServeMux gives this for free from a
// method-qualified pattern; the test pins that the patterns ARE method-qualified.
func TestPostIsMethodNotAllowed(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/overview", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
