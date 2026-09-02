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

// kit builds a directory shaped like the real one: an OpenDesign project
// directory, holding the five allowlisted entries at the nesting the entry
// point's ../../tokens.css depends on, plus the working files the editor keeps
// beside them — the design contract, screenshots, scratch, and the
// .file-versions/ revision history.
//
// Shaped like the real thing on purpose. Both guards here were caught by
// mutation only because the fixture contains the things they exclude; a fixture
// holding nothing but the kit cannot fail either of them.
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
	// The allowlisted five, at the nesting the real kit uses: the entry point is
	// two levels down so its ../../tokens.css resolves to the served root.
	write("ui_kits/app/index.html", "<h1>kit</h1>")
	write("ui_kits/app/components/chart.html", "chart")
	write("tokens.css", ":root{}")
	write("colors_and_type.css", "body{}")
	write("components.css", ".c{}")
	write("assets/favicon.svg", "<svg/>")

	// Everything else the OpenDesign project directory really holds. None of it
	// is a credential; none of it is meant to be published either.
	write("CLAUDE.md", "instructions that are never committed")
	write("DESIGN.md", "the design contract")
	write("SKILL.md", "skill text")
	write("README.md", "readme")
	write("preview/scratch.html", "scratch")
	write("examples/one.html", "example")
	write("node-compile-cache/x.blob", "cache")
	write("image-2.png", "png")
	// The editor's per-file revision history — the only dotted directory in the
	// real tree, and the reason the dotted refusal is kept alongside the
	// allowlist rather than replaced by it.
	write(".file-versions/tokens.css/1", "an earlier revision")
	write("ui_kits/.file-versions/app/1", "an earlier revision")

	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatalf("Mkdir(empty) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "ui_kits", "empty"), 0o755); err != nil {
		t.Fatalf("Mkdir(ui_kits/empty) error = %v", err)
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
		if !strings.Contains(err.Error(), "ui_kits/app/index.html") {
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
	// Relative on purpose, and two levels deep on purpose: the entry point's
	// ../../tokens.css resolves to the served root, so a redirect to anything
	// shallower serves the page with every stylesheet missing.
	if got := rec.Header().Get("Location"); got != "ui_kits/app/" {
		t.Errorf("Location = %q, want %q", got, "ui_kits/app/")
	}
}

// net/http canonicalises an explicit .../index.html to its directory. Pinned
// because it is the one case where a request for a file that exists does not
// return it, and the redirect it emits is relative — which is what keeps it
// inside the mount, the same property the entry redirect depends on.
func TestAnExplicitIndexHTMLIsCanonicalised(t *testing.T) {
	h := serve(t, kit(t))
	rec := get(t, h, "/design-kit/ui_kits/app/index.html")
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
		{"/design-kit/ui_kits/app/", "<h1>kit</h1>"},
		{"/design-kit/ui_kits/app/components/chart.html", "chart"},
		// The three sheets the entry point loads as ../../*.css, plus assets/.
		// Served from the root, which is why the root has to be the served root.
		{"/design-kit/tokens.css", ":root{}"},
		{"/design-kit/colors_and_type.css", "body{}"},
		{"/design-kit/components.css", ".c{}"},
		{"/design-kit/assets/favicon.svg", "<svg/>"},
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

// The served root has to be the project root, because the entry point's
// ../../tokens.css resolves there. That directory is an OpenDesign project, not
// a curated public tree: it holds CLAUDE.md, the design contract, working
// screenshots and editor scratch, none of which anyone chose to publish.
//
// An allowlist, not a denylist, because the tool that edits this directory
// creates files nobody decided to create — so the set that should not be served
// grows on its own, while the set that should is five entries and changes only
// when someone means it to.
func TestOnlyTheAllowlistedRootsAreServed(t *testing.T) {
	h := serve(t, kit(t))
	for _, target := range []string{
		"/design-kit/CLAUDE.md",
		"/design-kit/DESIGN.md",
		"/design-kit/SKILL.md",
		"/design-kit/README.md",
		"/design-kit/image-2.png",
		"/design-kit/preview/scratch.html",
		"/design-kit/examples/one.html",
		"/design-kit/node-compile-cache/x.blob",
	} {
		rec := get(t, h, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404; a root outside the allowlist is being served", target, rec.Code)
		}
	}
}

// Kept alongside the allowlist rather than replaced by it. The allowlist bounds
// the first path segment; this bounds every one below it, which is where the
// editor's per-file revision history also lives (ui_kits/.file-versions/).
//
// Tested against a fixture that really contains dotted names at both depths: a
// guard tested against a tree with no dotfiles in it cannot fail, and the
// first-segment-only version of this guard passed every root-level case while
// still serving the nested ones.
func TestDottedSegmentsAreRefused(t *testing.T) {
	h := serve(t, kit(t))
	for _, target := range []string{
		"/design-kit/.file-versions/tokens.css/1",
		"/design-kit/.file-versions/",
		"/design-kit/ui_kits/.file-versions/app/1",
		"/design-kit/ui_kits/app/../../.file-versions/tokens.css/1",
		"/design-kit/./tokens.css",
		"/design-kit/ui_kits/../tokens.css",
	} {
		rec := get(t, h, target)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404; a dotted segment reached the file server", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "an earlier revision") {
			t.Errorf("GET %s served the editor's revision history", target)
		}
	}
}

// A listing enumerates the kit's internals for anyone who finds the route, and
// nothing in the kit links to one. http.ServeFileFS produces one when told not
// to redirect, so this is a guard against a real default, not a hypothetical.
func TestADirectoryWithoutAnIndexIsNotListed(t *testing.T) {
	h := serve(t, kit(t))
	for _, target := range []string{"/design-kit/ui_kits/empty/", "/design-kit/ui_kits/empty"} {
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
		h.ServeHTTP(rec, httptest.NewRequest(method, "/design-kit/ui_kits/app/", nil))
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
	rec := get(t, h, "/design-kit/ui_kits/app/")
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
