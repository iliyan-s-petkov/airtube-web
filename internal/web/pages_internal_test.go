package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// noopHandler always answers 200, so these tests exercise only the
// Cache-Control decision in buildAssetCacheControl, independent of whether a
// real build has been run and independent of what's actually embedded in
// dist/.
var noopHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// TestNoDirListRefusesDotPrefixedSegments pins the dot-segment rule
// build-independently: noopHandler answers 200 for anything that gets through,
// so a 200 here means the path was ALLOWED and a 404 means it was refused,
// regardless of what is embedded in dist/.
//
// The allowed half matters more than the refused half. Rejecting "any path
// containing a dot" would refuse every content-hashed bundle name and take the
// whole application offline, so the hashed cases below are the regression this
// implementation's shape exists to prevent.
func TestNoDirListRefusesDotPrefixedSegments(t *testing.T) {
	h := noDirList(noopHandler)

	// Paths are in the prefix-stripped form the /static/build/ chain sees, plus
	// the unstripped form the /static/ chain sees.
	for _, tc := range []struct {
		path string
		want int
	}{
		{".vite/manifest.json", http.StatusNotFound},
		{".keep", http.StatusNotFound},
		{"assets/.hidden.js", http.StatusNotFound},
		{"/static/.env", http.StatusNotFound},
		{"assets/main-BFfKsolS.js", http.StatusOK},
		{"assets/map-CKRTiAqP.css", http.StatusOK},
		{"assets/maplibre-gl-worker.mjs", http.StatusOK},
		{"/static/app.css", http.StatusOK},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(tc.path, "/"), nil)
			req.URL.Path = tc.path // exactly what the wrapper receives, stripped or not
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Errorf("%q: status = %d, want %d", tc.path, rec.Code, tc.want)
			}
		})
	}
}

// TestBuildAssetCacheControlPicksTheUnhashedMapLibreFiles is mutation target 1
// from the fix-round review: delete the special case in buildAssetCacheControl
// (or empty out mapLibreUnhashedAssets) and this fails, because every request
// falls through to immutableCacheControl.
func TestBuildAssetCacheControlPicksTheUnhashedMapLibreFiles(t *testing.T) {
	h := buildAssetCacheControl(noopHandler)

	for _, name := range mapLibreUnhashedAssets {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/assets/"+name, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if got, want := rec.Header().Get("Cache-Control"), shortRevalidateCacheControl; got != want {
				t.Errorf("Cache-Control = %q, want %q", got, want)
			}
		})
	}
}

// TestBuildAssetCacheControlLeavesHashedAssetsImmutable is mutation target 2
// from the fix-round review — the one that matters more, per the reviewer,
// because it is the regression the exact-basename match exists to prevent.
// A hashed ".mjs" chunk is hypothetical today (Vite currently emits .js/.css
// for this project's own bundles, never .mjs), but the match must not assume
// that stays true — matching by extension or by a "maplibre-gl" prefix would
// both happen to leave today's real assets alone while still being wrong in
// principle, so this asserts against the shape of the match itself, not
// against what happens to exist in dist/ right now.
func TestBuildAssetCacheControlLeavesHashedAssetsImmutable(t *testing.T) {
	h := buildAssetCacheControl(noopHandler)

	for _, path := range []string{
		"/assets/main-DEADBEEF.js",
		"/assets/map-DEADBEEF.css",
		"/assets/chunk-DEADBEEF.mjs", // a hypothetical future hashed .mjs chunk
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if got, want := rec.Header().Get("Cache-Control"), immutableCacheControl; got != want {
				t.Errorf("Cache-Control = %q, want %q", got, want)
			}
		})
	}
}
