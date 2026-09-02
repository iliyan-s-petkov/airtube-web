package server_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"airbg.org/internal/config"
	"airbg.org/internal/server"
	"airbg.org/internal/snapshot"

	"airbg.org/internal/i18n"
)

// kitDir writes a miniature design kit shaped like the real OpenDesign project
// directory: the entry point at the nesting its ../../tokens.css depends on,
// and beside it the working files the editor keeps there — which is what the
// root allowlist and the dotted-segment refusal exist to keep unreachable.
func kitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"ui_kits/app/index.html": "<h1>kit</h1>",
		"tokens.css":             ":root{}",
		"CLAUDE.md":              "instructions that are never committed",
		".file-versions/x/1":     "an earlier revision",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// noRedirect is a client that reports redirects rather than following them.
// The entry redirect's target is the thing under test, and a following client
// would report only where it landed.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// The shipped configuration leaves design_kit.dir empty, so the route must not
// exist by default. This is the whole reason the kit is a disk path rather than
// go:embed: an embedded kit cannot be switched off, and "off in production" is
// the state that matters.
func TestTheDesignKitRouteIsAbsentByDefault(t *testing.T) {
	public, _ := running(t)
	resp, err := noRedirect().Get("http://" + public + "/design-kit/")
	if err != nil {
		t.Fatalf("GET /design-kit/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusOK {
		t.Errorf("status = %d; the design-kit route exists on the shipped configuration, which leaves design_kit.dir empty", resp.StatusCode)
	}
}

func TestTheDesignKitRouteServesTheKitWhenConfigured(t *testing.T) {
	dir := kitDir(t)
	public, _, _ := start(t, "", func(c *config.Config) { c.DesignKit.Dir = dir })
	client := noRedirect()

	t.Run("the root redirects to the entry point", func(t *testing.T) {
		resp, err := client.Get("http://" + public + "/design-kit/")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusFound)
		}
		// Resolved against the request URI, so the mount prefix is preserved
		// without the handler — which runs under StripPrefix — knowing it.
		loc, err := resp.Location()
		if err != nil {
			t.Fatalf("Location: %v", err)
		}
		if loc.Path != "/design-kit/ui_kits/app/" {
			t.Errorf("Location = %q, want %q", loc.Path, "/design-kit/ui_kits/app/")
		}
	})

	t.Run("the entry point is served", func(t *testing.T) {
		resp, err := client.Get("http://" + public + "/design-kit/ui_kits/app/")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if !strings.Contains(string(body), "<h1>kit</h1>") {
			t.Errorf("body = %q, want the kit's entry point", body)
		}
	})

	// The reason the kit rides the public listener rather than getting its own:
	// same origin means the site's CSP covers it. That only holds if the route
	// is registered on the mux the chain wraps. Registering it outside the chain
	// would still serve the kit — and would serve it with no CSP, no
	// X-Frame-Options and no rate limit, with no other symptom.
	t.Run("the route is inside the middleware chain", func(t *testing.T) {
		resp, err := client.Get("http://" + public + "/design-kit/ui_kits/app/")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		csp := resp.Header.Get("Content-Security-Policy")
		if csp == "" {
			t.Fatal("no Content-Security-Policy on the design-kit route; it is being served outside the middleware chain")
		}
		// Pinned against the configured policy rather than a literal, so this
		// cannot drift into asserting a policy the site does not serve.
		if want := testConfig(t).Listen.CSP; csp != want {
			t.Errorf("CSP = %q, want the site's own policy %q", csp, want)
		}
	})

	// Defence in depth behind "do not point design_kit.dir at a repo root".
	// Checked here as well as in internal/designkit because the guard is only
	// worth anything if it survives the real mount, prefix stripping and mux
	// path cleaning that sit in front of it.
	t.Run("only the allowlisted roots are reachable", func(t *testing.T) {
		for _, path := range []string{
			"/design-kit/CLAUDE.md",
			"/design-kit/.file-versions/x/1",
			"/design-kit/ui_kits/../.file-versions/x/1",
		} {
			resp, err := client.Get("http://" + public + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Errorf("GET %s status = 200; something outside the allowlist is served over HTTPS", path)
			}
			if strings.Contains(string(body), "never committed") || strings.Contains(string(body), "earlier revision") {
				t.Errorf("GET %s served a file that is not part of the kit", path)
			}
		}
	})
}

// A design_kit.dir that is not a kit must stop the process at startup, where an
// operator is watching, rather than 404 at the first request days later.
func TestABadDesignKitDirIsAStartupError(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	cfg := testConfig(t)
	cfg.DesignKit.Dir = t.TempDir() // exists, but holds no ui_kits/app/index.html
	holder := snapshot.NewHolder(cfg.Series)

	if _, err := server.New(server.Options{Config: cfg, Catalogue: cat, Snapshots: holder}); err == nil {
		t.Fatal("server.New error = nil, want an error naming the missing entry point")
	}
}
