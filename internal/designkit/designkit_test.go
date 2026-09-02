package designkit_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airbg.org/internal/designkit"
)

// kit builds a directory shaped like the real one: an app/ holding the entry
// point, a sibling token sheet two levels up from it (the nesting the kit's
// ../../tokens.css depends on), and a .git directory — the thing a no-build kit
// plausibly ships next to its files and the reason the dotfile guard exists.
func kit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", full, err)
		}
	}
	write("app/index.html", "<h1>kit</h1>")
	write("tokens.css", ":root{}")
	write("app/nested/index.html", "nested")
	write(".git/config", "[core]\n\trepositoryformatversion = 0\n")
	write(".git/objects/ab/cdef", "a commit that was later removed")
	// Dotted names below the served root as well as at it. A guard that only
	// inspects the first path segment passes every .git case above and still
	// serves these, which is the difference between "the kit is not a repo root"
	// and "no dotfile anywhere in the tree is reachable".
	write("app/.env", "SECRET=later removed")
	write("app/.git/config", "[core]\n\trepositoryformatversion = 0\n")
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatalf("Mkdir(empty) error = %v", err)
	}
	return root
}

func serve(t *testing.T, dir string) http.Handler {
	t.Helper()
	h, err := designkit.NewHandler(dir)
	if err != nil {
		t.Fatalf("NewHandler(%s) error = %v, want nil", dir, err)
	}
	// The same StripPrefix the server applies, so these tests exercise the paths
	// the handler really sees rather than a shape only the test produces.
	return http.StripPrefix("/design-kit", h)
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// A directory that is not a kit is a startup error, not a route that 404s. The
// mis-set path this catches — pointing one level too high or too low in the kit
// tree — is otherwise silent until someone opens the page, which is days later
// and looks like the kit is broken rather than the config.
func TestABadDesignKitDirIsAStartupError(t *testing.T) {
	t.Run("empty dir", func(t *testing.T) {
		if _, err := designkit.NewHandler(""); err == nil {
			t.Fatal("NewHandler(\"\") error = nil, want an error")
		}
	})
	t.Run("no entry point", func(t *testing.T) {
		dir := t.TempDir()
		_, err := designkit.NewHandler(dir)
		if err == nil {
			t.Fatal("NewHandler(empty dir) error = nil, want an error")
		}
		if !strings.Contains(err.Error(), "app/index.html") {
			t.Errorf("error = %q, want it to name the missing entry point", err)
		}
	})
	t.Run("dir does not exist", func(t *testing.T) {
		if _, err := designkit.NewHandler(filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Fatal("NewHandler(absent) error = nil, want an error")
		}
	})
}

func TestTheRootRedirectsToTheEntryPoint(t *testing.T) {
	h := serve(t, kit(t))
	rec := get(t, h, "/design-kit/")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	// Relative on purpose: it resolves to /design-kit/app/, which keeps app/ two
	// levels below the served root so ../../tokens.css still resolves.
	if got := rec.Header().Get("Location"); got != "app/" {
		t.Errorf("Location = %q, want %q", got, "app/")
	}
}

// net/http canonicalises an explicit .../index.html to its directory. Pinned
// because it is the one case where a request for a file that exists does not
// return it, and the redirect it emits is relative — which is what keeps it
// inside the mount, the same property the entry redirect depends on.
func TestAnExplicitIndexHTMLIsCanonicalised(t *testing.T) {
	h := serve(t, kit(t))
	rec := get(t, h, "/design-kit/app/index.html")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMovedPermanently)
	}
	if got := rec.Header().Get("Location"); strings.HasPrefix(got, "/") {
		t.Errorf("Location = %q, want a relative target; an absolute one would leave the /design-kit/ mount", got)
	}
}

func TestFilesAndDirectoryIndexesAreServed(t *testing.T) {
	h := serve(t, kit(t))
	for _, tc := range []struct{ target, want string }{
		{"/design-kit/app/", "<h1>kit</h1>"},
		{"/design-kit/tokens.css", ":root{}"},
		{"/design-kit/app/nested/", "nested"},
	} {
		rec := get(t, h, tc.target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", tc.target, rec.Code)
			continue
		}
		if got := rec.Body.String(); got != tc.want {
			t.Errorf("GET %s body = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// The failure this prevents is a full history disclosure, not a missing page:
// .git/objects/ holds every commit, so anything ever committed to the kit and
// later removed is retrievable from a route reachable off the public site.
//
// Pointing design_kit.dir below a repo root is the primary control. This is the
// second one, and it is tested against a fixture that really does contain a
// .git — a guard tested against a directory with no dotfiles in it cannot fail.
func TestDottedSegmentsAreRefused(t *testing.T) {
	h := serve(t, kit(t))
	for _, target := range []string{
		"/design-kit/.git/config",
		"/design-kit/.git/objects/ab/cdef",
		"/design-kit/.git/",
		"/design-kit/app/../.git/config",
		"/design-kit/./tokens.css",
		"/design-kit/app/../tokens.css",
		// Below the served root, not at it: these are what a first-segment-only
		// guard would still serve.
		"/design-kit/app/.env",
		"/design-kit/app/.git/config",
	} {
		rec := get(t, h, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404; a dotted segment reached the file server", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "repositoryformatversion") ||
			strings.Contains(rec.Body.String(), "later removed") {
			t.Errorf("GET %s served content from a dotted path", target)
		}
	}
}

// A listing enumerates the kit's internals for anyone who finds the route, and
// nothing in the kit links to one. http.ServeFileFS produces one when told not
// to redirect, so this is a guard against a real default, not a hypothetical.
func TestADirectoryWithoutAnIndexIsNotListed(t *testing.T) {
	h := serve(t, kit(t))
	for _, target := range []string{"/design-kit/empty/", "/design-kit/empty"} {
		rec := get(t, h, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", target, rec.Code)
		}
	}
}

func TestOnlyReadMethodsAreAllowed(t *testing.T) {
	h := serve(t, kit(t))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, "/design-kit/app/", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want %d", method, rec.Code, http.StatusMethodNotAllowed)
		}
		if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
			t.Errorf("%s Allow = %q, want %q", method, got, "GET, HEAD")
		}
	}
}

// Short and revalidating, because the kit's assets are versioned by a
// hand-maintained ?v=NN rather than by content. An immutable header here would
// strand a reviewer on a stale kit with no server-side way to clear it.
func TestServedFilesRevalidate(t *testing.T) {
	h := serve(t, kit(t))
	rec := get(t, h, "/design-kit/app/")
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "must-revalidate") {
		t.Errorf("Cache-Control = %q, want it to revalidate", got)
	}
	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q, must not be immutable; kit assets are not content-addressed", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
}
