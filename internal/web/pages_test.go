package web_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestBuildAssetsAreImmutablyCacheable. A content-hashed filename can be cached
// forever by definition — its content cannot change without its name changing.
// Without this header the hash buys nothing: the browser revalidates every
// bundle on every navigation, which is the cost the whole manifest mechanism
// exists to avoid.
func TestBuildAssetsAreImmutablyCacheable(t *testing.T) {
	rr := renderer(t, nil)

	// Any real file under the embedded dist tree. .keep is always present, which
	// is what makes this test runnable with no Node.
	rec := fetch(t, rr, "/static/build/.keep")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=31536000, immutable"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
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
