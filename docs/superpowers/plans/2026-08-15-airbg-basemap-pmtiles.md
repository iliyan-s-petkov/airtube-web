# Self-hosted PMTiles Basemap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the vendor-basemap configuration with a keyless, self-hosted Protomaps PMTiles basemap served from a third listener, deleting `AIRBG_BASEMAP_KEY` and leaving `AIRBG_DATABASE_URL` as the project's only secret.

**Architecture:** A new `internal/tiles` package serves three static artefacts (`bulgaria.pmtiles`, `style.json`, `glyphs/{fontstack}/{range}.pbf`) from a directory, over a listener of its own that holds no database pool, no snapshot, no limiter and no admission semaphore. Configuration gains a `tiles` section whose three keys are all-or-nothing; `basemap.style_url` and `AIRBG_BASEMAP_KEY` are deleted. The browser is told the style URL derived from `tiles.public_url`; with `tiles.*` empty the renderer emits an empty `data-basemap` and the map island's existing blank-basemap fallback — live, tested, and currently unreachable — becomes reachable again.

**Tech Stack:** Go 1.23 (`net/http`, `io/fs`, `os.DirFS`), MapLibre GL 6.3.0 + the `pmtiles` npm package, Vite, Vitest. No new Go dependencies.

## Global Constraints

- Module path is exactly `airbg.org`. Go dependencies stay limited to pgx/v5, pressly/goose/v3, testcontainers-go, yaml.v3, log/slog. **This plan adds no Go dependency.**
- No secrets in the repo. Configuration comes from `airbg.yaml` plus `AIRBG_*` environment variables only. After this plan `AIRBG_DATABASE_URL` is the only secret.
- **There are no defaults compiled into the binary.** A missing key is a startup error. `readRaw` refuses a schema with a nil leaf, so every new YAML key must be present in `airbg.yaml` — an empty string is a value, an absent key is an error.
- All SQL through `pgx` parameterised queries. No string-concatenated SQL anywhere, test helpers included. (This plan touches no SQL.)
- `www-root/` (the legacy PHP app) must not be touched.
- Never stage or commit `CLAUDE.md` — it is gitignored deliberately.
- No `Co-Authored-By: Claude` trailer and no "Generated with Claude Code" line in any commit message or PR body.
- Every new test must be **mutation-proven**: after it passes, change the value or code it claims to protect, watch the test fail, revert. A test that passes against both broken and fixed code is the defect this project spent a whole fix wave removing. Report the mutation and its failure output.
- Verification command set, run before every commit:
  ```bash
  gofmt -l . && go vet ./... && go vet -tags integration ./... && go test ./...
  ```
  Frontend tasks additionally run `cd web && npm test`.
- Comments explain *why*, in the voice of the surrounding code. Match the density already in `internal/server/server.go` and `internal/config/validate.go`.

---

## File Structure

**Created:**
- `internal/tiles/tiles.go` — the static-file handler: allowlist, method gate, CORS and cache headers.
- `internal/tiles/tiles_test.go` — its tests.
- `internal/tiles/testdata/` — a miniature tile directory used by the tests.
- `docs/tiles.md` — the manual tile-generation procedure.

**Modified:**
- `internal/config/schema.go` — drop `rawBasemap`, add `rawTiles`.
- `internal/config/resolve.go` — drop `Basemap`, add `Tiles` with its `Enabled()` and `StyleURL()` methods.
- `internal/config/validate.go` — drop the `basemap.style_url` block, add `validateTiles`.
- `internal/config/load.go` — drop `BasemapKeyEnv` and the `{key}` substitution; retarget the key-shaped entries in `secretKeys`.
- `internal/config/inert_test.go` — pin the new `tiles.*` values.
- `airbg.yaml` — drop `basemap:`, add `tiles:`, widen `listen.csp`'s comment.
- `internal/web/render.go` — `basemapStyleURL` sourced from `cfg.Tiles.StyleURL()`; `PageData.HasBasemap`.
- `internal/web/templates/base.gohtml`, `internal/i18n/en.json`, `internal/i18n/bg.json` — the ODbL basemap credit.
- `internal/server/server.go` — a third listener.
- `internal/server/server_test.go` — the separation test.
- `cmd/airbg/validate.go` — drop the `AIRBG_BASEMAP_KEY` presence row.
- `web/package.json`, `web/src/islands/map.js`, `web/src/islands/__tests__/map.test.js` — the `pmtiles` protocol and the error handler.
- `docs/configuration.md`, `.env.example` — the reference and the sample environment.

---

### Task 1: The `internal/tiles` handler

**Files:**
- Create: `internal/tiles/tiles.go`
- Create: `internal/tiles/tiles_test.go`
- Create: `internal/tiles/testdata/style.json`, `internal/tiles/testdata/bulgaria.pmtiles`, `internal/tiles/testdata/glyphs/NotoSans-Regular/0-255.pbf`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func NewHandler(dir string, allowOrigin string) (http.Handler, error)` in package `tiles` (import path `airbg.org/internal/tiles`). Task 4 calls it.

- [ ] **Step 1: Create the test fixture directory**

```bash
mkdir -p internal/tiles/testdata/glyphs/NotoSans-Regular
printf '{"version":8,"sources":{},"layers":[]}' > internal/tiles/testdata/style.json
printf 'PMTilesFAKEBODY0123456789' > internal/tiles/testdata/bulgaria.pmtiles
printf 'fakeglyphs' > internal/tiles/testdata/glyphs/NotoSans-Regular/0-255.pbf
```

These are placeholders, not real tiles: the handler serves bytes and never parses them, so a real 200 MB archive would only make the suite slow. `bulgaria.pmtiles` is 25 bytes exactly, which the range test relies on.

- [ ] **Step 2: Write the failing tests**

Create `internal/tiles/tiles_test.go`:

```go
package tiles_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"airbg.org/internal/tiles"
)

const origin = "https://airbg.org"

func handler(t *testing.T) http.Handler {
	t.Helper()
	h, err := tiles.NewHandler("testdata", origin)
	if err != nil {
		t.Fatalf("NewHandler error = %v, want nil", err)
	}
	return h
}

func do(t *testing.T, h http.Handler, method, target string, hdr http.Header) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestServesTheThreeArtefacts(t *testing.T) {
	h := handler(t)
	for _, path := range []string{
		"/style.json",
		"/bulgaria.pmtiles",
		"/glyphs/NotoSans-Regular/0-255.pbf",
	} {
		resp := do(t, h, http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
	}
}

// TestRejectsAnythingOutsideTheAllowlist. The directory is an operator-provided
// path; whatever else lands in it — a build log, a half-written temp file, the
// planetiler jar — must not become a public download.
func TestRejectsAnythingOutsideTheAllowlist(t *testing.T) {
	if err := os.WriteFile(filepath.Join("testdata", "notes.txt"), []byte("private"), 0o600); err != nil {
		t.Fatalf("seeding notes.txt: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join("testdata", "notes.txt")) })

	h := handler(t)
	if got := do(t, h, http.MethodGet, "/notes.txt", nil).StatusCode; got != http.StatusNotFound {
		t.Errorf("GET /notes.txt = %d, want 404", got)
	}
}

// TestPathTraversal. os.DirFS closes this structurally rather than by
// validating input, and the allowlist closes it a second time. Mutate by
// swapping os.DirFS for http.Dir AND widening the allowlist to see it fail.
func TestPathTraversal(t *testing.T) {
	h := handler(t)
	for _, target := range []string{
		"/../../etc/passwd",
		"/..%2f..%2fetc%2fpasswd",
		"/glyphs/../../style.json",
		"//etc/passwd",
	} {
		resp := do(t, h, http.MethodGet, target, nil)
		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET %s = 200, want a refusal", target)
		}
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h := handler(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		resp := do(t, h, method, "/style.json", nil)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s /style.json = %d, want 405", method, resp.StatusCode)
		}
		if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
			t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
		}
	}
	if got := do(t, h, http.MethodHead, "/style.json", nil).StatusCode; got != http.StatusOK {
		t.Errorf("HEAD /style.json = %d, want 200", got)
	}
}

// TestRangeRequest. Range support is not a nicety here: the PMTiles protocol
// reads a 300 MB archive exclusively through ranges. A handler that answered
// 200 with the whole body would send the entire file on the first tile.
func TestRangeRequest(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodGet, "/bulgaria.pmtiles", http.Header{"Range": []string{"bytes=8-11"}})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "FAKE" {
		t.Errorf("body = %q, want %q", body, "FAKE")
	}
	if got, want := resp.Header.Get("Content-Range"), "bytes 8-11/25"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
}

// TestCORSHeaders. Tiles live on a different host from the application, so
// every fetch is cross-origin. Missing headers here produce a blank map with no
// server-side error — indistinguishable from a tile-generation mistake.
func TestCORSHeaders(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodGet, "/style.json", nil)
	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":   origin,
		"Access-Control-Allow-Headers":  "Range",
		"Access-Control-Expose-Headers": "Content-Range",
		"Cache-Control":                 "public, max-age=31536000, immutable",
		"X-Content-Type-Options":        "nosniff",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "*" {
		t.Error(`Access-Control-Allow-Origin is "*"; it must name the application origin`)
	}
}

func TestPreflight(t *testing.T) {
	h := handler(t)
	resp := do(t, h, http.MethodOptions, "/bulgaria.pmtiles", http.Header{
		"Origin":                        []string{origin},
		"Access-Control-Request-Method": []string{"GET"},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("preflight Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
}

// TestConstructorRejectsAnIncompleteDirectory. A mis-set tiles.dir must fail at
// startup. Discovering it from a blank map in production is the outcome this
// guards against.
func TestConstructorRejectsAnIncompleteDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := tiles.NewHandler(dir, origin); err == nil {
		t.Fatal("NewHandler on an empty directory returned nil error, want an error")
	}

	if err := os.WriteFile(filepath.Join(dir, "style.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := tiles.NewHandler(dir, origin)
	if err == nil {
		t.Fatal("NewHandler with style.json but no bulgaria.pmtiles returned nil error, want an error")
	}
	if got := fmt.Sprint(err); got == "" {
		t.Error("error message is empty")
	}
}

func TestConstructorRejectsEmptyArguments(t *testing.T) {
	if _, err := tiles.NewHandler("", origin); err == nil {
		t.Error("NewHandler with an empty dir returned nil error, want an error")
	}
	if _, err := tiles.NewHandler("testdata", ""); err == nil {
		t.Error("NewHandler with an empty allowOrigin returned nil error, want an error")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/tiles/`
Expected: FAIL — the package does not exist (`no Go files in .../internal/tiles`, or `undefined: tiles.NewHandler` once the file is stubbed).

- [ ] **Step 4: Write the implementation**

Create `internal/tiles/tiles.go`:

```go
// Package tiles serves the self-hosted basemap: one PMTiles archive, one style
// document, and the glyph atlases MapLibre needs for labels.
//
// It exists as its own package, behind its own listener, because it must hold
// nothing. No database pool, no snapshot, no rate limiter, no admission
// semaphore. A single map load issues dozens of range requests; routed through
// the application's middleware chain they would exhaust the 10/s API bucket on
// the first pan, and raising that bucket to fit them would raise it for the
// JSON API too — the precise thing the limit exists to prevent. Bulkheads
// again: only separate resources can bound one workload's effect on another's
// capacity.
package tiles

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// required names the files NewHandler insists on finding. Glyphs are checked as
// a directory rather than by name: the fontstack depends on what the style
// references, and enumerating them here would be a second place for that choice
// to live.
var required = []string{"style.json", "bulgaria.pmtiles"}

const glyphsDir = "glyphs"

// cacheControl: the artefacts are immutable by construction — regeneration
// changes the filename (see docs/tiles.md), so a cached copy can never be
// stale. Without this every visitor re-fetches hundreds of megabytes of ranges.
const cacheControl = "public, max-age=31536000, immutable"

// NewHandler serves dir's basemap artefacts, allowing cross-origin reads from
// allowOrigin. It returns an error rather than serving 404s if dir is not a
// tile directory, so a mis-set path fails at startup instead of producing a
// blank map in production that looks like a tile-generation mistake.
func NewHandler(dir string, allowOrigin string) (http.Handler, error) {
	if dir == "" {
		return nil, errors.New("tiles: dir is empty")
	}
	if allowOrigin == "" {
		// Not defaulted to "*": that would let any page on the internet read
		// the tiles, and the value we want is already known at startup.
		return nil, errors.New("tiles: allowOrigin is empty")
	}

	fsys := os.DirFS(dir)
	for _, name := range required {
		if _, err := fs.Stat(fsys, name); err != nil {
			return nil, fmt.Errorf("tiles: %s in %s: %w", name, dir, err)
		}
	}
	info, err := fs.Stat(fsys, glyphsDir)
	if err != nil {
		return nil, fmt.Errorf("tiles: %s in %s: %w", glyphsDir, dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("tiles: %s in %s is not a directory", glyphsDir, dir)
	}

	files := http.FileServerFS(fsys)
	return &handler{files: files, allowOrigin: allowOrigin}, nil
}

type handler struct {
	files       http.Handler
	allowOrigin string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set before any branch: a 404 and a 405 are cross-origin responses too,
	// and a browser that cannot read the status learns nothing from it.
	head := w.Header()
	head.Set("Access-Control-Allow-Origin", h.allowOrigin)
	head.Set("Access-Control-Allow-Headers", "Range")
	head.Set("Access-Control-Expose-Headers", "Content-Range")
	head.Set("Vary", "Origin")
	head.Set("X-Content-Type-Options", "nosniff")

	switch r.Method {
	case http.MethodGet, http.MethodHead:
	case http.MethodOptions:
		// Range turns every tile read into a preflighted request. Answering it
		// here rather than falling through keeps the file server from seeing a
		// method it does not handle.
		head.Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		head.Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		head.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !allowed(r.URL.Path) {
		http.NotFound(w, r)
		return
	}

	head.Set("Cache-Control", cacheControl)
	h.files.ServeHTTP(w, r)
}

// allowed is the allowlist. Two independent defences meet here: os.DirFS
// already makes an escape from the directory structurally impossible, and this
// makes anything else that happens to sit inside it — a build log, a
// half-written temp file — unreachable. path.Clean collapses "." and ".."
// segments so a traversal attempt is compared in its resolved form.
func allowed(p string) bool {
	p = strings.TrimPrefix(path.Clean(p), "/")
	for _, name := range required {
		if p == name {
			return true
		}
	}
	// glyphs/{fontstack}/{range}.pbf — exactly two segments below glyphs/.
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == glyphsDir && strings.HasSuffix(parts[2], ".pbf")
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/tiles/ -v`
Expected: PASS, every test.

- [ ] **Step 6: Mutation-prove the two security tests**

Run each mutation, confirm the named test fails, then revert:

1. In `allowed`, replace the body with `return true`. Expected: `TestRejectsAnythingOutsideTheAllowlist` FAILS (`GET /notes.txt = 200, want 404`).
2. In `ServeHTTP`, change `Access-Control-Allow-Origin` to `"*"`. Expected: `TestCORSHeaders` FAILS on both the value comparison and the explicit `"*"` check.
3. Delete the `default:` arm's `http.Error(...); return` so POST falls through. Expected: `TestOnlyGetAndHead` FAILS.

Record each mutation and the exact failure line in the task report.

- [ ] **Step 7: Verify and commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/tiles
git commit -m "tiles: serve the self-hosted basemap artefacts from their own handler"
```

---

### Task 2: Configuration — add `tiles`, delete `basemap`

**Files:**
- Modify: `internal/config/schema.go` (`Basemap *rawBasemap` at line 23; `rawBasemap` at lines 192-194)
- Modify: `internal/config/resolve.go` (`Basemap Basemap` at line 21; `type Basemap` at line 167; the `Basemap:` literal at line 276)
- Modify: `internal/config/validate.go` (`Validate` at line 62; the `basemap.style_url` block at the end of `validateFrontend`, lines 336-361)
- Modify: `internal/config/load.go` (`secretKeys` at line 24; `BasemapKeyEnv` at line 256; the substitution at line 278)
- Modify: `internal/web/render.go:78`, `internal/web/render_test.go:87` (compile-level only; Task 3 owns the rest)
- Modify: `internal/config/inert_test.go`
- Modify: `airbg.yaml` (`basemap:` at line 277; header comment at line 16; `listen.csp` at line 44)
- Modify: `cmd/airbg/validate.go:38`
- Test: `internal/config/validate_test.go`, `internal/config/inert_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces, in package `config`:
  ```go
  type Tiles struct {
      Addr      string
      Dir       string
      PublicURL string
  }
  func (t Tiles) Enabled() bool
  func (t Tiles) StyleURL() string   // "" when !Enabled()
  ```
  and `Config.Tiles Tiles`. `Config.Basemap` and `config.BasemapKeyEnv` no longer exist. Task 3 calls `cfg.Tiles.StyleURL()`; Task 4 reads `cfg.Tiles.Addr`, `cfg.Tiles.Dir` and `cfg.Listen.BaseURL`.

- [ ] **Step 1: Write the failing validation tests**

Append to `internal/config/validate_test.go`. Follow the file's existing helper for building a valid config — if it loads `airbg.yaml` and mutates, do the same; the assertions below only need a `config.Config` named `cfg` that validates cleanly before mutation.

```go
// TestTilesAllOrNothing. Two of three keys set is the shape that produces a
// running server with a map that silently fetches from nowhere.
func TestTilesAllOrNothing(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"addr only":       func(c *config.Config) { c.Tiles = config.Tiles{Addr: "127.0.0.1:8082"} },
		"dir only":        func(c *config.Config) { c.Tiles = config.Tiles{Dir: "/var/lib/airbg/tiles"} },
		"public_url only": func(c *config.Config) { c.Tiles = config.Tiles{PublicURL: "https://tiles.airbg.org"} },
		"missing dir": func(c *config.Config) {
			c.Tiles = config.Tiles{Addr: "127.0.0.1:8082", PublicURL: "https://tiles.airbg.org"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate returned nil, want an error: tiles.* is all-or-nothing")
			}
		})
	}
}

// TestTilesEmptyIsLegal. No tiles configured means no basemap: the map island
// renders markers over frontend.empty_basemap_colour. Local development must
// not need a 300 MB file.
func TestTilesEmptyIsLegal(t *testing.T) {
	cfg := validConfig(t)
	cfg.Tiles = config.Tiles{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with no tiles configured = %v, want nil", err)
	}
	if cfg.Tiles.Enabled() {
		t.Error("Enabled() = true with every key empty")
	}
	if got := cfg.Tiles.StyleURL(); got != "" {
		t.Errorf("StyleURL() = %q, want empty", got)
	}
}

// TestTilesHostMustBeInConnectSrc. MapLibre fetches the style, the glyphs and
// the .pmtiles ranges over fetch/XHR. A CSP that omits the host fails closed and
// the map is blank, with nothing in any server log to say why.
func TestTilesHostMustBeInConnectSrc(t *testing.T) {
	cfg := validConfig(t)
	cfg.Tiles = config.Tiles{
		Addr:      "127.0.0.1:8082",
		Dir:       "/var/lib/airbg/tiles",
		PublicURL: "https://tiles.airbg.org",
	}
	cfg.Listen.CSP = "default-src 'self'; connect-src 'self'"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want an error: connect-src omits tiles.airbg.org")
	}

	cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with the host in connect-src = %v, want nil", err)
	}
	if got, want := cfg.Tiles.StyleURL(), "https://tiles.airbg.org/style.json"; got != want {
		t.Errorf("StyleURL() = %q, want %q", got, want)
	}
}

// TestTilesAddrIsSeparate. Sharing a listener address with the application or
// the metrics listener is the "three listeners simplified back to two" mistake
// in configuration form.
func TestTilesAddrIsSeparate(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "127.0.0.1:9090"} {
		cfg := validConfig(t)
		cfg.Listen.Addr = "127.0.0.1:8080"
		cfg.Listen.MetricsAddr = "127.0.0.1:9090"
		cfg.Tiles = config.Tiles{
			Addr:      addr,
			Dir:       "/var/lib/airbg/tiles",
			PublicURL: "https://tiles.airbg.org",
		}
		cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate with tiles.addr = %q returned nil, want an error", addr)
		}
	}
}

// TestTilesPublicURLShape. The host reaches a Content-Security-Policy header
// assembled by concatenation, so anything but a plain absolute http(s) URL is
// rejected — the same rule the deleted basemap.style_url carried.
func TestTilesPublicURLShape(t *testing.T) {
	for name, u := range map[string]string{
		"no scheme": "tiles.airbg.org",
		"ftp":       "ftp://tiles.airbg.org",
		"userinfo":  "https://user:pass@tiles.airbg.org",
		"space":     "https://tiles airbg.org",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			cfg.Tiles = config.Tiles{Addr: "127.0.0.1:8082", Dir: "/var/lib/airbg/tiles", PublicURL: u}
			cfg.Listen.CSP = "default-src 'self'; connect-src 'self' " + u
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate with tiles.public_url = %q returned nil, want an error", u)
			}
		})
	}
}
```

If `validConfig(t)` does not already exist in that file, add it:

```go
// validConfig loads the committed configuration, so these tests mutate the
// values the service actually ships with rather than a second copy that drifts.
func validConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	return cfg
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/config/ -run TestTiles`
Expected: FAIL to compile — `cfg.Tiles undefined (type config.Config has no field or method Tiles)`.

- [ ] **Step 3: Add the raw schema**

In `internal/config/schema.go`, delete these lines:

```go
	Basemap   *rawBasemap   `yaml:"basemap"`
```
```go
type rawBasemap struct {
	StyleURL *string `yaml:"style_url"`
}
```

and add, in their place:

```go
	Tiles     *rawTiles     `yaml:"tiles"`
```

```go
// rawTiles has no key field, and must never grow one. The whole point of the
// self-hosted basemap is that there is no vendor to authenticate to; a key here
// would also route a credential through assignScalar's value-echoing parse
// errors, the same reason rawDatabase has no url field.
type rawTiles struct {
	Addr      *string `yaml:"addr"`
	Dir       *string `yaml:"dir"`
	PublicURL *string `yaml:"public_url"`
}
```

- [ ] **Step 4: Add the resolved type**

In `internal/config/resolve.go`, replace `Basemap   Basemap` (line 21) with `Tiles     Tiles`, delete `type Basemap struct {...}` (lines 167-172), and add:

```go
// Tiles configures the self-hosted basemap listener. All three keys or none:
// a partial setting starts a server whose map fetches from nowhere and says
// nothing about it. Validate enforces that.
type Tiles struct {
	// Addr is the third listener's address. It serves static files only — no
	// pool, no snapshot, no limiter — which is what makes it safe to expose
	// directly while the application port accepts only Cloudflare's ranges.
	Addr string
	// Dir holds bulgaria.pmtiles, style.json and glyphs/.
	Dir string
	// PublicURL is what the browser is told to fetch. One home for it: it
	// produces both the style URL handed to the map island and the origin the
	// CSP must allow, and two copies is how those two drift apart.
	PublicURL string
}

// Enabled reports whether a basemap is configured. Validate guarantees the
// three keys are all set or all empty, so testing one would do — testing all
// three keeps this honest if that guarantee is ever weakened.
func (t Tiles) Enabled() bool {
	return t.Addr != "" && t.Dir != "" && t.PublicURL != ""
}

// StyleURL is the MapLibre style document's URL, or empty when no basemap is
// configured. Empty is not a failure: the map island renders markers over
// frontend.empty_basemap_colour.
func (t Tiles) StyleURL() string {
	if !t.Enabled() {
		return ""
	}
	return strings.TrimSuffix(t.PublicURL, "/") + "/style.json"
}
```

Add `"strings"` to that file's imports (it currently imports only `"time"`). Then replace the `Basemap:` literal at the end of `resolve` with:

```go
		Tiles: Tiles{
			Addr:      *r.Tiles.Addr,
			Dir:       *r.Tiles.Dir,
			PublicURL: *r.Tiles.PublicURL,
		},
```

and update the doc comment above `resolve`, deleting the sentence "`Database.URL` and `Basemap.Key` are populated by Task 7 (LoadFile) from environment variables; `Basemap.StyleURL` arrives with `{key}` already substituted." Replace with: "`Database.URL` is populated by LoadFile from the environment."

- [ ] **Step 5: Replace the validation**

In `internal/config/validate.go`, delete everything from `if c.Basemap.StyleURL == "" {` to the end of `validateFrontend` (lines 336-361), leaving `validateFrontend` ending at the `ZoomCity >= ZoomSensor` check. Add `c.validateTiles(&p)` to `Validate`, after `c.validateFrontend(&p)`. Then add:

```go
// validateTiles checks the two couplings the self-hosted basemap depends on.
// Both fail silently at runtime — a blank map and no server-side error — so
// both fail loudly at startup instead.
func (c Config) validateTiles(p *problems) {
	set := map[string]string{
		"tiles.addr":       c.Tiles.Addr,
		"tiles.dir":        c.Tiles.Dir,
		"tiles.public_url": c.Tiles.PublicURL,
	}
	var empty, filled []string
	for path, v := range set {
		if v == "" {
			empty = append(empty, path)
		} else {
			filled = append(filled, path)
		}
	}
	if len(filled) == 0 {
		// No basemap configured. Legal: the map renders markers over
		// frontend.empty_basemap_colour, and local development needs neither a
		// vendor account nor a 300 MB file.
		return
	}
	if len(empty) > 0 {
		sort.Strings(empty)
		p.addf("tiles.* is all-or-nothing; %s is set but %s is empty",
			strings.Join(sorted(filled), ", "), strings.Join(empty, ", "))
		return
	}

	if len(c.Tiles.Addr) > maxHostLength || !hostPattern.MatchString(c.Tiles.Addr) {
		p.addf("tiles.addr = %q, must be host:port", c.Tiles.Addr)
	}
	// A third listener that shares an address with either of the other two is
	// the "three listeners simplified back to two" mistake, in configuration.
	if c.Tiles.Addr == c.Listen.Addr {
		p.addf("tiles.addr and listen.addr are both %q; the tiles listener must be separate", c.Tiles.Addr)
	}
	if c.Tiles.Addr == c.Listen.MetricsAddr {
		p.addf("tiles.addr and listen.metrics_addr are both %q; the tiles listener must be separate", c.Tiles.Addr)
	}

	u, err := url.Parse(c.Tiles.PublicURL)
	switch {
	case err != nil:
		p.addf("tiles.public_url is not a URL: %s", parseErrorReason(err))
	case u.Scheme != "http" && u.Scheme != "https":
		p.addf("tiles.public_url must use http or https")
	case u.User != nil:
		// Userinfo would put a credential in a URL the browser fetches, and the
		// host below is concatenated into a CSP header.
		p.addf("tiles.public_url must not contain userinfo")
	case len(u.Host) > maxHostLength:
		p.addf("tiles.public_url host is %d bytes, must be at most %d", len(u.Host), maxHostLength)
	case !hostPattern.MatchString(u.Host):
		p.addf("tiles.public_url host = %q is not a valid hostname", u.Host)
	default:
		// MapLibre fetches the style, the glyphs and the .pmtiles ranges over
		// fetch/XHR. A connect-src that omits this host fails closed: a blank
		// map, and nothing anywhere on the server to say why.
		// Match whole source expressions, never a substring of the directive:
		// "not-tiles.airbg.org" contains "tiles.airbg.org", so containment would
		// accept a policy that blocks the very host it is checking for. Both the
		// bare host and scheme://host are valid CSP source expressions.
		origin := u.Scheme + "://" + u.Host
		found := false
		for _, tok := range strings.Fields(connectSrc(c.Listen.CSP)) {
			if tok == origin || tok == u.Host {
				found = true
				break
			}
		}
		if !found {
			p.addf("listen.csp's connect-src does not allow %q, so the browser cannot fetch the basemap from tiles.public_url", u.Host)
		}
	}
}

// sorted returns a sorted copy, so a problem message reads the same on every
// run. Ranging a map is deliberately unordered in Go.
func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// connectSrc extracts the connect-src directive from a CSP, or default-src when
// connect-src is absent — the fallback the browser itself applies.
func connectSrc(csp string) string {
	var fallback string
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if name, rest, ok := strings.Cut(directive, " "); ok {
			switch name {
			case "connect-src":
				return rest
			case "default-src":
				fallback = rest
			}
		}
	}
	return fallback
}
```

Add `"sort"` to that file's imports.

- [ ] **Step 6: Delete the basemap key from the loader**

In `internal/config/load.go`:

- Delete `BasemapKeyEnv  = "AIRBG_BASEMAP_KEY"` and rewrite the const block's comment to name one secret:
  ```go
  const (
  	// DatabaseURLEnv is env-only by design: it is a credential, and
  	// airbg.yaml is committed. It is the project's only secret — the basemap
  	// is self-hosted and has no vendor to authenticate to.
  	DatabaseURLEnv = "AIRBG_DATABASE_URL"
  )
  ```
- In `LoadFile`, delete these two lines:
  ```go
  	cfg.Basemap.Key = os.Getenv(BasemapKeyEnv)
  	// The style URL is templated so the key never appears in the committed file.
  	cfg.Basemap.StyleURL = strings.ReplaceAll(cfg.Basemap.StyleURL, "{key}", cfg.Basemap.Key)
  ```
  Drop `"strings"` from the imports if nothing else in the file uses it.
- **Retarget, do not delete**, the key-shaped entries in `secretKeys`. The rejection is still worth having — it is what stops a future contributor writing a credential into the committed file — but it can no longer point at a variable that does not exist:
  ```go
  var secretKeys = map[string]string{
  	"database_url": "AIRBG_DATABASE_URL",
  	"dsn":          "AIRBG_DATABASE_URL",
  	"password":     "AIRBG_DATABASE_URL",
  	"key":          "an environment variable",
  	"api_key":      "an environment variable",
  	"secret":       "an environment variable",
  	"token":        "an environment variable",
  }
  ```
  (`basemap_key` goes: it named a variable that no longer exists, and `key` already covers the shape.)

In `cmd/airbg/validate.go`, delete line 38 (`fmt.Fprintf(w, "%s\t%s\n", config.BasemapKeyEnv, presence(cfg.Basemap.Key))`) and its test expectation in `cmd/airbg/validate_test.go` if one names `AIRBG_BASEMAP_KEY`.

- [ ] **Step 7: Keep the tree compiling**

Deleting `Config.Basemap` breaks `internal/web`, which still reads it. Make the
two minimum changes here so this task's own `go test ./...` gate can pass; Task 3
owns the comments, the derivation tests and the footer attribution.

In `internal/web/render.go`, change line 78:

```go
		basemapStyleURL: cfg.Tiles.StyleURL(),
```

In `internal/web/render_test.go:87`, replace `cfg.Basemap.StyleURL = basemapStyleURL` with:

```go
	if basemapStyleURL != "" {
		cfg.Tiles = config.Tiles{
			Addr:      "127.0.0.1:8082",
			Dir:       "/var/lib/airbg/tiles",
			PublicURL: strings.TrimSuffix(basemapStyleURL, "/style.json"),
		}
	}
```

That helper's parameter still reads as a style URL, which Task 3 renames. Leave
the existing assertions alone: they pass a full style URL and expect it rendered
verbatim, and the `TrimSuffix` above preserves exactly that. Add `"strings"` and
`"airbg.org/internal/config"` to the test file's imports if absent.

- [ ] **Step 8: Update `airbg.yaml`**

Delete the `basemap:` block (lines 277-280 — the section header, its comment, and `style_url`). Add, at the same position:

```yaml
# The self-hosted basemap. Empty means no basemap: the map renders sensor
# markers over frontend.empty_basemap_colour and starts two listeners instead
# of three. That is the correct setting for local development — no vendor
# account, and no 300 MB file to download before the site runs.
#
# Set all three or none; a partial setting is a startup error, because a map
# that fetches from nowhere reports nothing. Generating the artefacts is a
# manual, few-times-a-year procedure: see docs/tiles.md.
#
# tiles.addr is a THIRD listener. It holds no database pool, no snapshot, no
# rate limiter and no admission semaphore, which is what makes it safe to
# expose directly while the application port accepts connections only from
# Cloudflare's ranges. Do not merge it into listen.addr: a single map load
# issues dozens of range requests and would exhaust the API bucket on the
# first pan.
#
# tiles.public_url's host MUST appear in listen.csp's connect-src, or the
# browser refuses every basemap fetch and the map is silently blank.
tiles:
  addr: ""        # e.g. "127.0.0.1:8082"
  dir: ""         # e.g. "/var/lib/airbg/tiles" — holds bulgaria.pmtiles, style.json, glyphs/
  public_url: ""  # e.g. "https://tiles.airbg.org"
```

The keys must be present with empty values, not absent: `readRaw` refuses a schema with a nil leaf, and this project keeps no defaults.

At line 16, replace the `AIRBG_BASEMAP_KEY` line in the header comment with nothing, and adjust the surrounding text so it names one secret. In the `listen.csp` comment block, append:

```
  # connect-src stays 'self' because tiles.* ships empty. Configuring a
  # basemap means adding tiles.public_url's origin here — validation refuses
  # to start otherwise, since a CSP-blocked fetch produces a blank map and no
  # error anywhere on the server.
```

- [ ] **Step 9: Pin the new values in `inert_test.go`**

In `internal/config/inert_test.go`'s `strings` subtest, delete any `basemap.style_url` row and add:

```go
		{"tiles.addr", cfg.Tiles.Addr, ""},
		{"tiles.dir", cfg.Tiles.Dir, ""},
		{"tiles.public_url", cfg.Tiles.PublicURL, ""},
```

Match the subtest's existing row struct — if it uses named fields or a map, follow that shape rather than the positional literal above. Add a comment on the group:

```go
		// The tiles keys are NEW, not moved: this pin records the decision that
		// the shipped configuration has no basemap, rather than proving a
		// non-change. Configuring one is a deployment step (docs/tiles.md).
```

- [ ] **Step 10: Run the tests**

Run: `go test ./internal/config/ ./cmd/airbg/ ./internal/web/ -v 2>&1 | tail -40`
Expected: PASS, including all five `TestTiles*` tests.

- [ ] **Step 11: Mutation-prove the couplings**

1. In `airbg.yaml`, set `tiles.addr: "127.0.0.1:8082"` and leave the other two empty. Run `go test ./internal/config/`. Expected: FAIL — `LoadFile` returns the all-or-nothing error, and `validConfig`/`TestShippedValuesMatchPhase2Behaviour` fail. Revert.
2. In `validateTiles`, delete the `connect-src` check. Run `go test ./internal/config/ -run TestTilesHostMustBeInConnectSrc`. Expected: FAIL. Revert.
3. In `Tiles.StyleURL`, drop the `!t.Enabled()` guard so it always concatenates. Run `go test ./internal/config/ -run TestTilesEmptyIsLegal`. Expected: FAIL — `StyleURL() = "/style.json", want empty`. Revert.

Record each mutation and its failure output.

- [ ] **Step 12: Verify and commit**

```bash
gofmt -l . && go vet ./... && go vet -tags integration ./... && go test ./...
git add internal/config airbg.yaml cmd/airbg internal/web
git commit -m "config: replace the vendor basemap with a self-hosted tiles section"
```

---

### Task 3: The renderer's style URL

**Files:**
- Modify: `internal/web/render.go:40,52-58,78,123-125`
- Modify: `internal/web/templates/base.gohtml:31`
- Modify: `internal/i18n/en.json:37`, `internal/i18n/bg.json:37`
- Test: `internal/web/render_test.go:78-90`

**Interfaces:**
- Consumes: `config.Tiles.StyleURL() string` from Task 2.
- Produces: `PageData.BasemapStyleURL` unchanged in name and type, plus `func (d PageData) HasBasemap() bool`. `internal/web/templates/index.gohtml:12` and `area.gohtml:40` need no change — they already render `data-basemap="{{.BasemapStyleURL}}"` and already treat it as "a style URL, possibly empty".

- [ ] **Step 1: Update the failing test**

In `internal/web/render_test.go`, `newTestRendererWithBasemap` currently sets `cfg.Basemap.StyleURL = basemapStyleURL` (line 87). Replace that line with a `tiles` configuration, and rename the helper so its parameter reads honestly:

```go
// newTestRendererWithTiles builds a renderer whose basemap is the given
// public URL, or none when it is empty — the two states the templates must
// render differently.
func newTestRendererWithTiles(t *testing.T, publicURL string) *web.Renderer {
	t.Helper()
	cfg := testConfig(t)
	if publicURL != "" {
		cfg.Tiles = config.Tiles{
			Addr:      "127.0.0.1:8082",
			Dir:       "/var/lib/airbg/tiles",
			PublicURL: publicURL,
		}
	}
	// ... the rest of the existing helper body, unchanged
}
```

Update its two call sites to pass a public URL rather than a style URL, and add:

```go
// TestBasemapStyleURLIsDerivedFromTilesPublicURL. The style URL is not a
// separate configuration value: writing it twice is how the CSP host and the
// URL the browser fetches drift apart.
func TestBasemapStyleURLIsDerivedFromTilesPublicURL(t *testing.T) {
	rr := newTestRendererWithTiles(t, "https://tiles.airbg.org")
	body := renderIndex(t, rr) // use whatever this file already uses to render a page to a string
	if !strings.Contains(body, `data-basemap="https://tiles.airbg.org/style.json"`) {
		t.Errorf("rendered page does not carry the derived style URL:\n%s", body)
	}
}

// TestNoTilesRendersAnEmptyBasemapAttribute. Empty is the map island's signal
// to fall back to a flat colour — a fallback that was live, tested and
// unreachable for as long as validation rejected an empty style URL.
func TestNoTilesRendersAnEmptyBasemapAttribute(t *testing.T) {
	rr := newTestRendererWithTiles(t, "")
	body := renderIndex(t, rr)
	if !strings.Contains(body, `data-basemap=""`) {
		t.Errorf("rendered page does not carry an empty basemap attribute:\n%s", body)
	}
}
```

If the file has no `renderIndex` helper, render through whatever entry point the existing basemap tests use and assert on that output instead — do not invent a new rendering path.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/web/`
Expected: FAIL — `newTestRendererWithTiles` and the two new tests are undefined.

- [ ] **Step 3: Implement**

`internal/web/render.go:78` already reads `basemapStyleURL: cfg.Tiles.StyleURL(),`
— Task 2 made that change so the tree would compile. Confirm it, do not repeat it.

Rewrite the `NewRenderer` doc comment's basemap paragraph (lines 52-58) to:

```go
// The basemap style URL is derived from config.Tiles, which is empty when no
// basemap is configured — the map then renders data markers over a plain
// background instead. Derived once, here, because the same tiles.public_url
// also produces the CSP origin the browser must be allowed to fetch from, and
// two copies is how those two drift apart.
```

Rewrite the `PageData.BasemapStyleURL` comment (lines 123-125) to:

```go
	// BasemapStyleURL is the self-hosted MapLibre style document's URL, or
	// empty when no basemap is configured. See config.Tiles.StyleURL.
	BasemapStyleURL string
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/web/ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Write the failing attribution test**

OpenStreetMap data is ODbL. The footer already credits sensor.community for the
data and OpenStreetMap for the boundaries (`internal/i18n/en.json:36-37`,
`internal/web/templates/base.gohtml:30-31`); the basemap needs its own credit,
and only when a basemap is actually served. This is a licence obligation, not
presentation.

Add to `internal/web/render_test.go`:

```go
// TestBasemapAttribution. ODbL requires the credit wherever the tiles are
// shown — and requires it nowhere when no tiles are shown, so the footer does
// not claim a basemap the page does not render.
func TestBasemapAttribution(t *testing.T) {
	with := renderIndex(t, newTestRendererWithTiles(t, "https://tiles.airbg.org"))
	if !strings.Contains(with, "Protomaps") {
		t.Errorf("page with a basemap does not credit Protomaps:\n%s", with)
	}
	without := renderIndex(t, newTestRendererWithTiles(t, ""))
	if strings.Contains(without, "Protomaps") {
		t.Error("page with no basemap credits Protomaps anyway")
	}
}
```

- [ ] **Step 6: Implement the attribution**

Add the string to both catalogues — `internal/i18n/i18n_test.go` asserts the two
files carry identical key sets, so adding to one alone fails the suite:

`internal/i18n/en.json`, after `"footer.boundaries"`:
```json
  "footer.basemap": "Basemap © OpenStreetMap contributors, ODbL 1.0, tiles by Protomaps",
```

`internal/i18n/bg.json`, at the same position:
```json
  "footer.basemap": "Картна основа © сътрудниците на OpenStreetMap, ODbL 1.0, плочки от Protomaps",
```

Add a `HasBasemap` method to `PageData` in `internal/web/render.go`, beside the
existing `BasemapStyleURL` field:

```go
// HasBasemap reports whether the page renders basemap tiles, which is what
// makes the footer's ODbL credit required — and, when false, wrong.
func (d PageData) HasBasemap() bool { return d.BasemapStyleURL != "" }
```

And in `internal/web/templates/base.gohtml`, after line 31:

```gotemplate
  {{if .HasBasemap}}<p>{{.T "footer.basemap"}}</p>{{end}}
```

- [ ] **Step 7: Run to verify it passes**

Run: `go test ./internal/web/ ./internal/i18n/ -v 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 8: Mutation-prove**

1. Change `cfg.Tiles.StyleURL()` to `cfg.Tiles.PublicURL`. Run `go test ./internal/web/ -run TestBasemapStyleURLIsDerivedFromTilesPublicURL`. Expected: FAIL — the page carries `https://tiles.airbg.org`, not `.../style.json`. Revert.
2. Remove the `{{if .HasBasemap}}` guard so the credit always renders. Run `go test ./internal/web/ -run TestBasemapAttribution`. Expected: FAIL — `page with no basemap credits Protomaps anyway`. Revert.

- [ ] **Step 9: Verify and commit**

```bash
gofmt -l . && go vet ./... && go test ./...
git add internal/web internal/i18n
git commit -m "web: derive the basemap style URL from the tiles public URL"
```

---

### Task 4: The third listener

**Files:**
- Modify: `internal/server/server.go` (package doc lines 1-8; `Server` struct line 42; `New` line 65; `Run` line 171; `shutdown` line 239)
- Test: `internal/server/server_test.go` (`running` at line 49; new test after `TestMetricsAreNotOnThePublicListener` at line 137)

**Interfaces:**
- Consumes: `tiles.NewHandler(dir, allowOrigin string) (http.Handler, error)` from Task 1; `config.Tiles` with `Enabled()` from Task 2.
- Produces: no exported signature change. `server.New` still takes `server.Options` and returns `(*Server, error)`; `Run(ctx)` still blocks until cancellation. `cmd/airbg/main.go` needs no change — it already passes the whole `config.Config`.

- [ ] **Step 1: Write the failing test**

In `internal/server/server_test.go`, generalise `running` to return three addresses. Replace its signature and the address block:

```go
// running starts a server with two listeners. tilesDir empty means no basemap,
// which is the shipped configuration; runningWithTiles covers the other state.
func running(t *testing.T) (public, private string) {
	pub, priv, _ := start(t, "")
	return pub, priv
}

func runningWithTiles(t *testing.T, tilesDir string) (public, private, tiles string) {
	return start(t, tilesDir)
}

func start(t *testing.T, tilesDir string) (public, private, tilesAddr string) {
	t.Helper()

	// ... the existing i18n.Load / testConfig / holder.Store body, unchanged ...

	public, private = free(t), free(t)
	cfg.Listen.Addr = public
	cfg.Listen.MetricsAddr = private
	cfg.Listen.BaseURL = "http://" + public
	if tilesDir != "" {
		tilesAddr = free(t)
		cfg.Tiles = config.Tiles{
			Addr:      tilesAddr,
			Dir:       tilesDir,
			PublicURL: "http://" + tilesAddr,
		}
	}

	// ... the existing server.New / go srv.Run / t.Cleanup / waitReady body, unchanged ...
	return public, private, tilesAddr
}
```

Note the test bypasses `Validate` — it mutates an already-loaded `config.Config` — so the CSP coupling does not need satisfying here. That is deliberate: this test is about listener separation, and Task 2 already proves the coupling.

Then add:

```go
// tilesDir writes a miniature tile directory, so these tests need no
// 300 MB artefact. The handler serves bytes and never parses them.
func tilesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "glyphs", "NotoSans-Regular"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"style.json":                         `{"version":8,"sources":{},"layers":[]}`,
		"bulgaria.pmtiles":                   "PMTilesFAKEBODY0123456789",
		"glyphs/NotoSans-Regular/0-255.pbf":  "fakeglyphs",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestTilesAreNotOnThePublicListener. This is the test that catches a later
// "simplification" of three listeners back into two. Serving style.json from
// the public listener would put dozens of range requests per map load through
// the 10/s API bucket — and any exemption carved out for them is one routing
// mistake away from covering more than intended.
func TestTilesAreNotOnThePublicListener(t *testing.T) {
	public, private, tiles := runningWithTiles(t, tilesDir(t))

	if got := get(t, tiles, "/style.json").StatusCode; got != http.StatusOK {
		t.Errorf("GET /style.json on the tiles listener = %d, want 200", got)
	}
	if got := get(t, public, "/style.json").StatusCode; got == http.StatusOK {
		t.Error("/style.json is reachable on the public listener")
	}
	if got := get(t, private, "/style.json").StatusCode; got == http.StatusOK {
		t.Error("/style.json is reachable on the private listener")
	}
	// The converse, so a future refactor cannot satisfy this test by pointing
	// all three addresses at one mux that happens to 404 the wrong paths.
	if got := get(t, tiles, "/api/v1/overview").StatusCode; got == http.StatusOK {
		t.Error("the API is reachable on the tiles listener")
	}
	if got := get(t, tiles, "/metrics").StatusCode; got == http.StatusOK {
		t.Error("/metrics is reachable on the tiles listener")
	}
}

// TestNoTilesStartsTwoListeners. The shipped configuration has no basemap, and
// it must not open a third socket or fail to start.
func TestNoTilesStartsTwoListeners(t *testing.T) {
	public, private := running(t)
	if got := get(t, public, "/").StatusCode; got != http.StatusOK {
		t.Errorf("GET / = %d, want 200", got)
	}
	if got := get(t, private, "/healthz").StatusCode; got != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", got)
	}
}

// TestABadTilesDirIsAStartupError. Discovering a mis-set path from a blank map
// in production is the outcome this refuses.
func TestABadTilesDirIsAStartupError(t *testing.T) {
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	cfg := testConfig(t)
	holder := snapshot.NewHolder(cfg.Series)
	cfg.Tiles = config.Tiles{
		Addr:      "127.0.0.1:0",
		Dir:       filepath.Join(t.TempDir(), "does-not-exist"),
		PublicURL: "http://127.0.0.1:8082",
	}
	if _, err := server.New(server.Options{Config: cfg, Catalogue: cat, Snapshots: holder}); err == nil {
		t.Fatal("server.New with a missing tiles.dir returned nil error, want an error")
	}
}
```

Add `"os"` to the test file's imports (`filepath` and `config` are already there).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/server/ -run 'TestTiles|TestNoTiles|TestABadTiles'`
Expected: FAIL — `cfg.Tiles undefined` before Task 2 lands, or `/style.json` 404 on the tiles address once it compiles.

- [ ] **Step 3: Implement the third listener**

In `internal/server/server.go`:

Rewrite the package doc:

```go
// Package server assembles the listeners.
//
// Up to three, never one: the public listener carries the middleware chain and
// the public routes; the private listener carries /metrics and /healthz; the
// tiles listener carries the self-hosted basemap, and only when one is
// configured. Separate listeners rather than path prefixes, because a prefix is
// one routing mistake away from the wrong outcome — for /metrics, exposing the
// counters that tell a scraper whether it is being throttled; for tiles, either
// rate-limiting a map load into uselessness or carving out an exemption that
// covers more than intended.
package server
```

Add the field to `Server`, after `private`:

```go
	// tiles is nil when no basemap is configured, which is the shipped
	// configuration. Nil means two listeners, exactly as before.
	tiles *http.Server
```

In `New`, after the `privateMux(opts)` server literal is built (i.e. after the `s := &Server{...}` block), add:

```go
	if opts.Config.Tiles.Enabled() {
		// The application's own origin, not "*": the tiles are on a different
		// host, so every fetch is cross-origin, and "*" would let any page on
		// the internet read them.
		h, err := tiles.NewHandler(opts.Config.Tiles.Dir, opts.Config.Listen.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("server: tiles: %w", err)
		}
		s.tiles = &http.Server{
			Addr:    opts.Config.Tiles.Addr,
			Handler: h,
			// The same timeouts as the other two. A range request for a few
			// kilobytes is not a slow request, and a client that cannot finish
			// one inside the write timeout is not a client we serve.
			ReadHeaderTimeout: opts.Config.Timeouts.ReadHeader,
			ReadTimeout:       opts.Config.Timeouts.Read,
			WriteTimeout:      opts.Config.Timeouts.Write,
			IdleTimeout:       opts.Config.Timeouts.Idle,
			ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		}
	}
	return s, nil
```

Import `"airbg.org/internal/tiles"`.

Rewrite `Run`'s goroutine block:

```go
func (s *Server) Run(ctx context.Context) error {
	// Buffered for every listener that can send, so a goroutine whose listener
	// dies during shutdown never blocks forever on an unread channel.
	errCh := make(chan error, 3)

	go func() { errCh <- s.servePublic() }()
	go func() { errCh <- listen(s.private) }()
	if s.tiles != nil {
		go func() { errCh <- listen(s.tiles) }()
	}
	// ... the rest unchanged
```

Rewrite `shutdown`:

```go
	s.log.Info("shutting down")
	err := s.public.Shutdown(ctx)
	if perr := s.private.Shutdown(ctx); err == nil {
		err = perr
	}
	if s.tiles != nil {
		if terr := s.tiles.Shutdown(ctx); err == nil {
			err = terr
		}
	}
	return err
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/server/ -v 2>&1 | tail -30`
Expected: PASS, all listeners tests included.

- [ ] **Step 5: Mutation-prove the separation**

1. In `New`, mount the tiles handler on the public mux instead — `root.Handle("/style.json", h)` — and drop the third `http.Server`. Run `go test ./internal/server/ -run TestTilesAreNotOnThePublicListener`. Expected: FAIL — `/style.json is reachable on the public listener`. Revert.
2. Change `errCh := make(chan error, 3)` back to `2` and run `go test ./internal/server/ -race -count=2`. Note whether anything catches it; if nothing does, say so in the report rather than claiming the buffer size is proven. (It is a leak, not a failure — worth recording as a known limit of the test.)
3. Delete the `s.tiles.Shutdown` block. Run `go test ./internal/server/ -run TestTilesAreNotOnThePublicListener -count=1`. Expected: the `t.Cleanup` "Run did not return within 10s" path or a leaked port; record whichever occurs.

- [ ] **Step 6: Verify and commit**

```bash
gofmt -l . && go vet ./... && go vet -tags integration ./... && go test ./...
git add internal/server
git commit -m "server: serve the self-hosted basemap from a third listener"
```

---

### Task 5: The frontend PMTiles protocol

**Files:**
- Modify: `web/package.json` (dependencies, line 9-12)
- Modify: `web/src/islands/map.js` (imports lines 6-7; `mount` line 21)
- Test: `web/src/islands/__tests__/map.test.js`

**Interfaces:**
- Consumes: the `data-basemap` attribute, which Task 3 now fills with `<public_url>/style.json` or leaves empty. `readConfig` and `mapStyle` keep their present shapes and signatures — `mapStyle(cfg)` still returns `cfg.basemap ? cfg.basemap : blankStyle(cfg.emptyBasemapColour)`.
- Produces: nothing other tasks consume.

- [ ] **Step 1: Add the dependency**

```bash
cd web && npm install --save-exact pmtiles
```

Confirm `web/package.json`'s `dependencies` now reads (version whatever npm resolved — pin it exactly, as `maplibre-gl` is):

```json
  "dependencies": {
    "maplibre-gl": "6.3.0",
    "pmtiles": "<resolved version>"
  },
```

**Deliberately not added:** the Protomaps style theme package. `style.json` is generated offline and shipped as a static file, so the runtime bundle carries no theming code and the style can be edited without a rebuild.

- [ ] **Step 2: Write the failing test**

Append to `web/src/islands/__tests__/map.test.js`:

```js
import { registerProtocols, mapStyle, blankStyle } from '../map.js'

// The pmtiles:// protocol must be registered before any style referencing it
// loads, and exactly once — MapLibre's addProtocol is global, and a second
// registration for the same scheme replaces the first silently.
describe('registerProtocols', () => {
  it('registers pmtiles exactly once across repeated calls', () => {
    const seen = []
    const add = (scheme, fn) => seen.push([scheme, typeof fn])
    registerProtocols(add)
    registerProtocols(add)
    expect(seen).toEqual([['pmtiles', 'function']])
  })
})

describe('mapStyle with a self-hosted basemap', () => {
  it('passes the style URL through untouched', () => {
    const url = 'https://tiles.airbg.org/style.json'
    expect(mapStyle({ basemap: url, emptyBasemapColour: '#eef2f5' })).toBe(url)
  })

  it('falls back to a flat colour when no basemap is configured', () => {
    expect(mapStyle({ basemap: '', emptyBasemapColour: '#eef2f5' }))
      .toEqual(blankStyle('#eef2f5'))
  })
})
```

- [ ] **Step 3: Run to verify it fails**

Run: `cd web && npm test -- map`
Expected: FAIL — `registerProtocols is not a function`.

- [ ] **Step 4: Implement**

In `web/src/islands/map.js`, change the imports (lines 6-7) to:

```js
import { Map as MapLibreMap, addProtocol } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { Protocol } from 'pmtiles'
```

Add, near `mapStyle`:

```js
// registerProtocols teaches MapLibre to read pmtiles:// URLs, which is how
// style.json references the single 300 MB archive: the protocol turns each tile
// read into an HTTP range request, so a visitor transfers only the ranges their
// viewport needs.
//
// Idempotent, and takes `add` as a parameter, because MapLibre's addProtocol is
// global module state: registering twice would silently replace the first
// handler, and a test cannot observe a global it cannot inject into.
let protocolsRegistered = false
export function registerProtocols(add = addProtocol) {
  if (protocolsRegistered) return
  protocolsRegistered = true
  add('pmtiles', new Protocol().tile)
}
```

In `mount(el)`, call it before constructing the map:

```js
export function mount(el) {
  const cfg = readConfig(el)
  registerProtocols()
  const chrome = mountChrome(el, cfg)
  const map = new MapLibreMap({
    container: el,
    // An unset style URL is not fatal: the map renders data markers over a
    // plain background, so local development needs no tile artefacts.
    style: mapStyle(cfg),
    center: [cfg.lon, cfg.lat],
    zoom: cfg.zoom,
    attributionControl: { compact: true },
  })
```

Add the error handler immediately after the constructor, before the existing `map.on('load', ...)`:

```js
  // A style-load failure must not take the sensor markers down with it: tiles
  // unavailable degrades to a blank background, it never fails the page. Logged
  // once rather than per failed tile, because a missing archive produces one
  // error per range request.
  let errorLogged = false
  map.on('error', (e) => {
    if (errorLogged) return
    errorLogged = true
    console.warn('basemap unavailable, rendering markers only', e?.error?.message ?? e)
  })
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd web && npm test`
Expected: PASS — the existing suite plus the new tests. The pre-existing `mapStyle`/`blankStyle` tests must pass unchanged; that they do is the check that this change is additive.

- [ ] **Step 6: Mutation-prove**

1. Remove the `if (protocolsRegistered) return` guard. Run `npm test -- map`. Expected: FAIL — `seen` has two entries.
2. Change `add('pmtiles', ...)` to `add('pmtiles2', ...)`. Expected: FAIL.

Revert both.

- [ ] **Step 7: Build and commit**

```bash
cd web && npm run build && cd ..
gofmt -l . && go test ./...
git add web/package.json web/package-lock.json web/src/islands/map.js web/src/islands/__tests__/map.test.js
git commit -m "web: read the self-hosted basemap through the pmtiles protocol"
```

(If `npm run build` writes a manifest that is committed, include it in the `git add`. Do not commit `web/node_modules` or `web/dist` unless the repo already tracks them.)

---

### Task 6: Documentation

**Files:**
- Create: `docs/tiles.md`
- Modify: `docs/configuration.md` (§4 "The two env-only secrets", §9 "The checked couplings", §11)
- Modify: `.env.example` (the `--- Basemap ---` section at the end)

**Interfaces:**
- Consumes: the final `tiles.*` key names and the two couplings from Task 2; the deployment posture from the spec.
- Produces: nothing code depends on.

- [ ] **Step 1: Write `docs/tiles.md`**

```markdown
# Generating the basemap

The basemap is three static files, generated offline a few times a year and
served as-is by the tiles listener. Nothing here runs in CI: the inputs are
hundreds of megabytes and the cadence is seasonal.

| File | What it is |
|---|---|
| `bulgaria.pmtiles` | Protomaps basemap, Bulgaria extract, ~150–300 MB |
| `glyphs/{fontstack}/{range}.pbf` | Font atlases MapLibre needs to render labels |
| `style.json` | References the `pmtiles://` source, the glyphs, and the layer styling |

Self-hosting the glyphs is not polish. Fetching them from a public endpoint
would reintroduce exactly the third-party request from a visitor's browser that
this design exists to remove.

## Pinned tool versions

- `planetiler` 0.8.3 (`planetiler.jar`, from the GitHub release)
- Java 21 or newer
- `font-maker` (or `build-glyphs` from `fontnik`) for the glyph PBFs
- Noto Sans Regular and Noto Sans Medium, from the Google Fonts release

Pin these. A basemap regenerated with a different planetiler produces different
layer names, and `style.json` references layer names.

## 1. The extract

Download the Bulgaria extract from Geofabrik:

    https://download.geofabrik.de/europe/bulgaria-latest.osm.pbf

## 2. The archive

    java -Xmx8g -jar planetiler.jar \
      --osm-path=bulgaria-latest.osm.pbf \
      --output=bulgaria-YYYYMMDD.pmtiles \
      --force

The date suffix is what keeps `Cache-Control: immutable` honest: regeneration
changes the filename, so a cached copy can never be stale. Deploy by writing the
new file, updating `style.json`'s source URL, and only then removing the old one.

## 3. The glyphs

    font-maker Noto_Sans/NotoSans-Regular.ttf glyphs/NotoSans-Regular
    font-maker Noto_Sans/NotoSans-Medium.ttf  glyphs/NotoSans-Medium

Generate a fontstack for every `text-font` the style references, and no more.

## 4. The style

Start from a pinned Protomaps theme and set:

- `sources.protomaps.url` to `pmtiles://<tiles.public_url>/bulgaria-YYYYMMDD.pmtiles`
- `glyphs` to `<tiles.public_url>/glyphs/{fontstack}/{range}.pbf`
- every label layer's `text-field` to `["coalesce", ["get", "name:bg"], ["get", "name"]]`,
  so the basemap follows the interface language
- `attribution` to `© OpenStreetMap contributors, © Protomaps`

The attribution is a licence obligation, not presentation: OpenStreetMap data is
ODbL. The page footer must carry the same credit.

## 5. Install

Lay the three artefacts out under `tiles.dir`:

    /var/lib/airbg/tiles/
      style.json
      bulgaria.pmtiles
      glyphs/NotoSans-Regular/0-255.pbf
      ...

`bulgaria.pmtiles` is the name the handler serves; symlink the dated file to it,
or rename on install. The handler refuses to start if any of `style.json`,
`bulgaria.pmtiles` or `glyphs/` is missing, so a mis-set `tiles.dir` is a
startup failure rather than a blank map nobody notices.

## 6. The firewall rule

This is load-bearing, not advisory. Serving tiles from the origin means a
hostname that resolves to the origin IP, and the anti-scraping design depends on
that IP being unknown: `CF-Connecting-IP` is attacker-controlled on a direct
connection, and every rate limiter keys off it.

- The **application port** (`listen.addr`) accepts connections only from
  Cloudflare's published IP ranges, enforced by a packet filter.
  `listen.trusted_proxy_cidrs` is not sufficient on its own — it governs header
  parsing, not who may connect.
- The **tiles port** (`tiles.addr`) accepts the world, on a DNS-only hostname.

With the filter in place, discovering the origin IP yields tiles and nothing
else. Without it, self-hosting the tiles weakens the system.
```

- [ ] **Step 2: Update `docs/configuration.md`**

- Retitle §4 to `## 4. The one env-only secret` and delete every mention of `AIRBG_BASEMAP_KEY` and `basemap.style_url`. Keep the paragraph explaining why `database.url` is env-only.
- In §9 "The checked couplings", add the two new ones with the same table/prose shape the section already uses:
  - `tiles.public_url`'s host must appear in `listen.csp`'s `connect-src`. MapLibre fetches the style, the glyphs and the `.pmtiles` ranges over `fetch`/XHR; a CSP that omits the host fails closed and the map is blank, with nothing on the server to say why.
  - `tiles.addr`, `tiles.dir` and `tiles.public_url` are all-or-nothing, and `tiles.addr` must differ from both `listen.addr` and `listen.metrics_addr`.
- Add a short section after §9:
  ```markdown
  ## The basemap

  `tiles.*` empty is legal and is what this repository ships: two listeners, and
  a map that renders sensor markers over `frontend.empty_basemap_colour`. Local
  development needs no vendor account and no 300 MB file.

  Setting all three keys starts a third listener serving the self-hosted
  Protomaps artefacts. It holds no database pool, no snapshot, no rate limiter
  and no admission semaphore — that is what makes it safe to expose directly
  while the application port accepts only Cloudflare's ranges. Generating the
  artefacts: `docs/tiles.md`.
  ```
- In §11, remove any `basemap.style_url` row and add the three `tiles.*` keys with their shipped value (empty) — matching whatever row format that section already uses.

- [ ] **Step 3: Update `.env.example`**

Delete the whole `# --- Basemap ---` section (its comment block, `AIRBG_BASEMAP_STYLE_URL` and `AIRBG_BASEMAP_KEY`). Replace with:

```
# --- Basemap ---
#
# The basemap is self-hosted: three static files generated offline (see
# docs/tiles.md) and served by a third listener. There is no vendor and no key —
# AIRBG_DATABASE_URL above is this project's only secret.
#
# All three keys or none. Empty, as shipped, means no basemap: the map renders
# sensor markers over frontend.empty_basemap_colour and the binary starts two
# listeners instead of three. That is the right setting for local development.
#
# Setting these also requires adding AIRBG_TILES_PUBLIC_URL's origin to
# listen.csp's connect-src, or the browser refuses every basemap fetch and the
# map is silently blank. Validation refuses to start rather than let that ship.
#
# The tiles port is the one port meant to accept the world. The application port
# must accept only Cloudflare's ranges, enforced by a packet filter — see
# docs/tiles.md §6. Skipping that rule is what turns self-hosted tiles into a
# way to find the origin IP.
# AIRBG_TILES_ADDR=127.0.0.1:8082
# AIRBG_TILES_DIR=/var/lib/airbg/tiles
# AIRBG_TILES_PUBLIC_URL=https://tiles.airbg.org
```

Also update the earlier paragraph that lists rejected secret-shaped keys: it currently reads "dsn, password, api_key, secret, token, key, basemap_key" — drop `basemap_key`, since `secretKeys` no longer carries it.

- [ ] **Step 4: Check the docs against the code**

Run:

```bash
grep -rn "BASEMAP\|basemap.style_url\|basemap_key" --include="*.go" --include="*.md" --include="*.yaml" --include="*.example" . | grep -v www-root | grep -v docs/superpowers
```

Expected: only `empty_basemap_colour`, `BasemapStyleURL`, `basemapStyleURL` and prose about "the basemap". Any surviving `AIRBG_BASEMAP_KEY` or `basemap.style_url` is a miss — fix it.

- [ ] **Step 5: Full verification and commit**

```bash
gofmt -l . && go vet ./... && go vet -tags integration ./... && go test ./...
cd web && npm test && cd ..
git add docs .env.example
git commit -m "docs: document the self-hosted basemap and drop the vendor key"
```

---

## Verification of the whole branch

Before finishing:

```bash
gofmt -l .
go vet ./... && go vet -tags integration ./...
go test ./...
go test -tags integration ./...        # requires Docker
cd web && npm test && npm run build && cd ..
AIRBG_CONFIG=./airbg.yaml AIRBG_DATABASE_URL=postgres://u:p@localhost:5432/airbg go run ./cmd/airbg validate-config
```

`validate-config`'s output must no longer mention `AIRBG_BASEMAP_KEY`, and must show the three `tiles.*` keys empty.
