package web_test

import (
	"net/http"
	"strings"
	"testing"

	"airbg.org/internal/web"
)

// TestBuildAssetsAreImmutablyCacheable. A content-hashed filename can be cached
// forever by definition — its content cannot change without its name changing.
// Without this header the hash buys nothing: the browser revalidates every
// bundle on every navigation, which is the cost the whole manifest mechanism
// exists to avoid.
//
// Asserted against the real hashed entry bundle, resolved through the manifest,
// rather than against dist/.keep: .keep is now (correctly) a 404, because
// noDirList refuses every dot-prefixed path segment. That makes this test
// dependent on a build having been run, hence the skip — the header decision
// itself is pinned build-independently in pages_internal_test.go.
func TestBuildAssetsAreImmutablyCacheable(t *testing.T) {
	assets, found := web.LoadAssets()
	if !found {
		t.Skip("no manifest embedded; run `npm run build` in web/ to exercise this path")
	}
	script := assets.Script("main")
	if script == "" {
		t.Fatal(`Script("main") = "", want the hashed entry path`)
	}

	rec := fetch(t, renderer(t, nil), script)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d, want 200", script, rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

// TestDotPrefixedBuildPathsAre404. `//go:embed all:dist` embeds dotfiles by
// design (that is what makes dist/.keep match, and a pattern matching nothing
// is a compile error), which also embedded Vite's .vite/manifest.json — served
// at a fixed URL, listing the entire chunk graph, under a one-year immutable
// Cache-Control whose content changes on every build.
//
// The hashed-asset half of this test is the more important one: it is the
// regression a broader match ("path contains a dot") would cause, and it would
// 404 every bundle the app loads.
func TestDotPrefixedBuildPathsAre404(t *testing.T) {
	rr := renderer(t, nil)

	for _, p := range []string{
		"/static/build/.vite/manifest.json",
		"/static/build/.keep",
	} {
		t.Run(p, func(t *testing.T) {
			rec := fetch(t, rr, p)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body:\n%s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), `"file"`) {
				t.Errorf("the build manifest was served:\n%s", rec.Body)
			}
		})
	}

	assets, found := web.LoadAssets()
	if !found {
		t.Skip("no manifest embedded; cannot check that a hashed asset still resolves")
	}
	script := assets.Script("main")
	rec := fetch(t, rr, script)
	if rec.Code != http.StatusOK {
		t.Errorf("GET %s: status = %d, want 200 — the dot-segment check must not "+
			"catch hashed filenames, which contain dots but never at the start of a segment",
			script, rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Errorf("GET %s: Cache-Control = %q, want %q", script, got, want)
	}
}

// TestHandWrittenStaticIsCacheableButNotImmutable. app.css has a stable name,
// so immutable would pin an edited stylesheet in every visitor's browser for a
// year.
func TestHandWrittenStaticIsCacheableButNotImmutable(t *testing.T) {
	rr := renderer(t, nil)

	rec := fetch(t, rr, "/static/app.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=3600"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Error("app.css is marked immutable; its filename is not content-hashed")
	}
}

// TestMapLibreWorkerFilesAreNotImmutablyCached. These two files are unhashed
// by necessity (MapLibre resolves them itself at runtime, so their URL must
// stay fixed across a version bump), which means the usual immutable header
// would let a stale pair survive for up to a year after a bump — silently,
// since a mismatched worker produces no console error, just a map that never
// finishes loading. A short, revalidating TTL bounds that instead.
func TestMapLibreWorkerFilesAreNotImmutablyCached(t *testing.T) {
	rr := renderer(t, nil)

	for _, name := range []string{"maplibre-gl-worker.mjs", "maplibre-gl-shared.mjs"} {
		t.Run(name, func(t *testing.T) {
			rec := fetch(t, rr, "/static/build/assets/"+name)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (has `npm run build` been run in web/?)", rec.Code)
			}
			if got, want := rec.Header().Get("Cache-Control"), "public, max-age=300, must-revalidate"; got != want {
				t.Errorf("Cache-Control = %q, want %q", got, want)
			}
		})
	}
}

// TestStaticDirectoriesAre404NotListings. A listing enumerates every chunk and
// every asset for free. net/http's FileServer does this by default, so the
// absence of a wrapper is the bug.
func TestStaticDirectoriesAre404NotListings(t *testing.T) {
	rr := renderer(t, nil)

	for _, p := range []string{"/static/", "/static/build/"} {
		t.Run(p, func(t *testing.T) {
			rec := fetch(t, rr, p)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body:\n%s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "<a href=") {
				t.Errorf("response body contains a directory listing:\n%s", rec.Body)
			}
		})
	}
}
