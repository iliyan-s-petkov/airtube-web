# airbg.org Phase 3a Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the Phase 2 server-rendered pages into a working map — a MapLibre map and a uPlot chart mounted as islands into markup Go already emits, plus the build toolchain and asset pipeline that delivers them.

**Ships when:** a visitor loading `/` sees Bulgaria with coloured oblast markers, can click an area and zoom in to see individual sensors, and clicking through to `/area/{slug}` sees a 24-hour PM2.5 chart.

**Architecture:** Vite builds one entry module (`web/src/main.js`) into content-hashed chunks under `internal/web/dist/`, which Go embeds and serves immutably. The entry module walks `[data-island]` elements Phase 2 already emits and dynamically imports one module per island kind, so the index page downloads the map chunk and never the chart chunk. All configuration the browser needs — basemap style URL, default metric, translated UI strings — arrives as server-rendered `data-*` attributes on the island container, because the CSP has no `'unsafe-inline'` and never will. Every island sits *beside* server-rendered content, so a failed bundle degrades to the Phase 2 page rather than a blank div.

**Tech Stack:** Vite 7, Svelte 5 (island UI chrome only), MapLibre GL JS, uPlot, Vitest. Go 1.26 stdlib for embedding and serving. `//go:embed` over the built `dist` tree.

**Source spec:** `docs/superpowers/specs/2026-08-11-airbg-phase3a-frontend-design.md`.

## Scope boundary — read before starting

**Spec §7.1 is NOT in this plan.** Moving the default area series into the snapshot is Task 1 of the hardening plan, `docs/superpowers/plans/2026-08-11-airbg-hardening.md`, along with §12.3's admission control, §13.3's connection cap, §13.5's scoped statement timeout, and §13.2's `Permissions-Policy` / `Cross-Origin-Resource-Policy`. Do not implement any of those here. §12.3a (the two connection pools) already landed on master as `258c4dc`.

This plan owns exactly the frontend-coupled work: §4 (toolchain, `assets.go`, asset serving, Dockerfile), §5 (island loader), §6 (map island, including §6.5's `CSP(basemapHost)` and the two basemap env vars), §7 minus §7.1 (chart island), §8 (the client-side fetch policy in `lib/api.js`), §13.1 (npm supply chain).

**The chart island depends on hardening Task 1 for its performance property, not for its correctness.** `/api/v1/area/{slug}/series?metric=P2&period=24h` already works today from Postgres; the hardening plan makes it a snapshot read. The chart island consumes the same JSON either way, so the two plans can be implemented in either order. Task 7 below notes the one place this matters.

## Global Constraints

- **No new Go dependency, ever.** `go.mod`'s direct require block must stay byte-identical. Permitted: `pgx/v5 v5.10.0`, `pressly/goose/v3 v3.27.3`, `testcontainers-go v0.44.0`.
- **Go testing is stdlib `testing` only.** No testify, no ginkgo. Hand-written `if got != want { t.Errorf("X = %v, want %v", got, want) }`. Table-driven subtests via `t.Run`. `t.Setenv` for config isolation.
- **JS testing is Vitest, pure logic only.** No jsdom component renders, no browser automation. A test that mounts MapLibre in jsdom asserts that jsdom stubs work, not that the map does.
- **Every load-bearing Go property must be mutation-proven.** Break the production line, quote the real failure, revert, confirm `git diff` is clean. A mutation of *test* code proves nothing. When a mutation genuinely comes out inert, say so plainly instead of hunting a cleverer one. **The manifest-absent test is the one most likely to be inert** (spec §9) — it must fail when `LoadAssets` is changed to return a hardcoded entry on a missing manifest.
- **Never revert a mutation with `git checkout`.** Copy the file aside (`cp x.go /tmp/x.go.orig`), restore from the copy, verify `git diff` byte-identical. `git checkout` has destroyed uncommitted work on this project once already.
- **npm supply chain (§13.1), binding on every task that touches `web/`:**
  - `package-lock.json` is committed. Every direct dependency is pinned to an exact version — no `^`, no `~`.
  - Installs use `npm ci --ignore-scripts`. A postinstall script is arbitrary code execution at build time from a transitive package nobody reviewed.
  - `npm audit --audit-level=high` must exit 0. Wire it into the build so it fails the build, not into a comment.
  - The Node stage of the Dockerfile is discarded. No Node, no `node_modules`, and no npm-sourced code other than the built bundle reaches the runtime image.
- **CSP stays free of `'unsafe-inline'` and `'unsafe-eval'`.** No inline `<script>`, no inline `style=` written from JS, no `new Function`. If a MapLibre upgrade ever needs more, that is a reviewed diff to the constant — never an `'unsafe-*'` allowance.
- **Colours come from `/api/v1/scales`.** No hex colour for a data band may appear anywhere in `web/src/`. The only permitted literal colours are the no-data grey and chrome (borders, text), which are not band colours.
- **No secrets in the repo.** Configuration from `AIRBG_*` environment variables only. The basemap key is `AIRBG_BASEMAP_KEY`. **The Google Maps key in `www-root/` is closed — inherited legacy, not the user's, no action.**
- **`www-root/`** is the legacy PHP app. Never modify it.
- **Image tags must be current-major as of 2026-08**, per the user's standing requirement. Verify `node:24-alpine` and `golang:1.26-alpine` are still current before writing the Dockerfile; if a newer major exists, use it and say so.
- **Commits:** no `Co-Authored-By: Claude` trailer, no "Generated with Claude Code" line. `CLAUDE.md` is gitignored and must never be staged, not even with `git add -f`.
- **`git log` needs `--no-show-signature`** in this repo.
- `go test ./...` starts testcontainers; only `internal/server/e2e_test.go` carries `//go:build integration`.
- Two files are gofmt-unclean on master from Phase 2 (`internal/httpx/chain.go`, `internal/web/render.go`). Both are modified by this plan. **Run `gofmt -w` on any file you touch** and let that ride along in the task's commit.

## File structure

| Path | Change | Responsibility | Task |
|---|---|---|---|
| `web/package.json`, `web/package-lock.json`, `web/vite.config.js` | create | toolchain, exact pins | 1 |
| `.gitignore` | modify | `internal/web/dist/*`, `!.../dist/.keep`, `web/node_modules/` | 1 |
| `internal/web/dist/.keep` | create | keeps `//go:embed` compiling with no Node | 1 |
| `internal/web/assets.go` | create | Vite manifest resolution, `Assets` type | 1 |
| `internal/web/render.go` | modify | `Assets` on `PageData`, loaded once at construction | 1 |
| `internal/web/templates/base.gohtml` | modify | conditional `<script type="module">` / `<link rel="stylesheet">` | 1 |
| `cmd/airbg/main.go` | modify | one startup log line naming the manifest state | 1 |
| `internal/web/pages.go` | modify | `/static/build/` immutable route; 404 instead of directory listing | 2 |
| `internal/httpx/headers.go` | modify | `CSPValue` → `CSP(basemapHost)`; `SecurityHeaders` takes the built policy | 3 |
| `internal/httpx/chain.go` | modify | `Chain.CSP` carries the policy through `Wrap` | 3 |
| `internal/config/config.go` | modify | `AIRBG_BASEMAP_STYLE_URL`, `AIRBG_BASEMAP_KEY`, derived `BasemapHost` | 3 |
| `internal/server/server.go` | modify | pass the built CSP into `Chain`; basemap style URL into the renderer | 3 |
| `web/src/lib/tier.js`, `colour.js`, `series.js` | create | pure logic | 4 |
| `web/src/lib/api.js` | create | fetch policy: in-flight dedup, client cache, 429 retry | 5 |
| `web/src/main.js` | create | island loader | 6 |
| `web/src/islands/map.js` | create | MapLibre lifecycle, tier switching, layers | 6 |
| `internal/web/templates/index.gohtml`, `area.gohtml` | modify | `data-metric`, basemap and i18n data attributes | 6, 7 |
| `internal/i18n/bg.json`, `en.json` | modify | map and chart UI strings | 6, 7 |
| `web/src/islands/chart.js` | create | uPlot lifecycle | 7 |
| `Dockerfile` | create | multi-stage node → go → distroless | 8 |

---

### Task 1: Toolchain, `assets.go`, and the conditional script tag

**Why this is one task and not three:** the manifest format, the Go type that reads it, and the template that emits its output are a single seam. Splitting them would leave a reviewer approving an `Assets` type nothing calls, and the property worth gating on — a page that emits the hashed script tag when a manifest exists and no tag at all when it does not — is only observable end to end.

**Files:**
- Create: `web/package.json`, `web/vite.config.js`, `web/src/main.js` (a stub, fleshed out in Task 6), `internal/web/dist/.keep`, `internal/web/assets.go`, `internal/web/assets_test.go`
- Modify: `.gitignore`, `internal/web/render.go`, `internal/web/templates/base.gohtml`, `cmd/airbg/main.go`
- Test: `internal/web/assets_test.go`, and append to `internal/web/render_test.go`

**Interfaces:**
- Produces:
  - `func web.LoadAssets() (a Assets, found bool)`
  - `func (a Assets) Script(entry string) string` — `""` when unknown
  - `func (a Assets) Style(entry string) string`
  - `web.PageData.Assets Assets`
  - `web.NewRenderer(cat, holder, baseURL)` keeps its signature — the `Assets` value is loaded inside it, not passed in.
- Consumes: nothing new.

**Design notes — read before writing code.**

1. **`dist/.keep` is load-bearing.** `//go:embed all:dist` fails to *compile* when `dist/` is absent or empty. A committed `.keep` means `go build ./...` and `go test ./...` work on a machine with no Node at all, and with no manifest present `assets.go` resolves to no bundles: templates emit no `<script>`, and every Phase 2 page test passes unchanged. Use `all:dist` — the plain `embed dist` form skips files beginning with `_` or `.`, which would exclude both `.keep` and Vite's `.vite/manifest.json`.
2. **Two manifest locations.** Vite moved the manifest to `dist/.vite/manifest.json` in v5. Look there first, fall back to `dist/manifest.json`, and let the test pin whichever the pinned Vite actually emits (spec §11.3).
3. **A missing manifest is not an error.** `LoadAssets` returns `(Assets{}, false)`. The zero `Assets` is valid and resolves nothing. This is what makes the no-Node path total rather than partial.
4. **`web/` sits outside `internal/` deliberately.** Go tooling walks `internal/`, and a `node_modules/` in there is a permanent source of noise.

- [ ] **Step 1: Create the npm project with exact pins**

`web/package.json` — note every version is exact, no range operators:

```json
{
  "name": "airbg-web",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "npm audit --audit-level=high && vite build",
    "test": "vitest run"
  },
  "dependencies": {
    "maplibre-gl": "5.9.0",
    "uplot": "1.6.32"
  },
  "devDependencies": {
    "svelte": "5.42.2",
    "vite": "7.1.14",
    "vitest": "3.2.4",
    "@sveltejs/vite-plugin-svelte": "6.2.1"
  }
}
```

Before writing this file, check the current version of each package (`npm view <pkg> version`) and use what you find — the numbers above are the shape, and a stale pin is worse than a checked one. Record the versions you actually pinned in the commit message.

`npm run build` runs the audit first, so a high-severity advisory fails the build rather than printing a warning nobody reads.

- [ ] **Step 2: Install and commit the lockfile**

```bash
cd web && npm install --ignore-scripts && npm audit --audit-level=high
```

`npm install` (not `ci`) is correct exactly once, to generate the lockfile. Every later install is `npm ci --ignore-scripts`. If the audit reports a high-severity advisory, resolve it now — a pin you cannot audit clean is not a pin you can ship.

- [ ] **Step 3: Write the Vite config**

`web/vite.config.js`:

```js
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default {
  root: 'web',
  plugins: [svelte()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    // Load-bearing: hashed filenames are how the app gets
    // `Cache-Control: immutable` without ever serving a stale bundle.
    // Without the manifest, Go cannot know the hashed name.
    manifest: true,
    rollupOptions: { input: 'src/main.js' },
  },
}
```

`outDir` is relative to `root`, and `input` likewise. Since `root: 'web'` and the config file itself lives in `web/`, run Vite from the repo root or adjust — verify by building in Step 5 and checking where the files land.

- [ ] **Step 4: Create the entry stub and the embed anchor**

`web/src/main.js` — a real loader lands in Task 6; this is enough to produce a bundle:

```js
// Island loader. Task 6 fills in the registry; this stub exists so the
// toolchain can be verified end to end before any island exists.
export {}
```

`internal/web/dist/.keep` — empty file. Then `.gitignore` gains:

```
# Vite output. Built by `npm run build` in web/, embedded by internal/web.
# .keep is committed so //go:embed always has a match — see the 3a spec §4.2.
internal/web/dist/*
!internal/web/dist/.keep
web/node_modules/
```

- [ ] **Step 5: Build once and record the real manifest shape**

```bash
cd web && npm run build && find ../internal/web/dist -type f | head -20 && cat ../internal/web/dist/.vite/manifest.json
```

Expected: a hashed `assets/main-<hash>.js`, and a manifest keyed by the source path (`"src/main.js"`) whose entry has `"file"`, `"isEntry": true`, and possibly `"css": [...]`. **Write down the exact key and field names you see** — the test fixture in Step 6 must match them, and this is the one thing the plan cannot know in advance because it depends on the installed Vite version.

- [ ] **Step 6: Write the failing `assets.go` tests**

Create `internal/web/assets_test.go`. Adjust the fixture to the shape you recorded in Step 5:

```go
package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadAssetsResolvesTheEntryFromTheManifest is the test that catches a
// manifest-format change on a Vite upgrade. The hashed filename is not
// knowable from Go; the manifest is the only contract between the two build
// systems, and a silently renamed field means a page that ships no script tag
// with no error anywhere.
func TestLoadAssetsResolvesTheEntryFromTheManifest(t *testing.T) {
	a, found := LoadAssets()
	if !found {
		t.Skip("no manifest embedded; run `npm run build` in web/ to exercise this path")
	}
	got := a.Script("main")
	if got == "" {
		t.Fatal(`Script("main") = "", want the hashed path from the manifest`)
	}
	if !strings.HasPrefix(got, "/static/build/") {
		t.Errorf("Script(\"main\") = %q, want a /static/build/ prefix", got)
	}
	if strings.Contains(got, "..") {
		t.Errorf("Script(\"main\") = %q, contains a traversal segment", got)
	}
}

// TestParseManifestReadsTheViteShape works on a fixture rather than the
// embedded tree, so it runs on a machine with no Node and pins the field names
// independently of whether anyone has built.
func TestParseManifestReadsTheViteShape(t *testing.T) {
	const fixture = `{
	  "src/main.js": {
	    "file": "assets/main-DEADBEEF.js",
	    "name": "main",
	    "src": "src/main.js",
	    "isEntry": true,
	    "css": ["assets/main-CAFEBABE.css"]
	  }
	}`
	a, err := parseManifest([]byte(fixture))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	if got, want := a.Script("main"), "/static/build/assets/main-DEADBEEF.js"; got != want {
		t.Errorf("Script(\"main\") = %q, want %q", got, want)
	}
	if got, want := a.Style("main"), "/static/build/assets/main-CAFEBABE.css"; got != want {
		t.Errorf("Style(\"main\") = %q, want %q", got, want)
	}
}

// TestUnknownEntryResolvesToEmpty. The template guards on the empty string, so
// an unknown entry must be "" and never a plausible-looking guessed path: a
// guessed path is a 404 and a broken page, while "" is the fallback the whole
// .keep design exists to preserve.
func TestUnknownEntryResolvesToEmpty(t *testing.T) {
	a, _ := parseManifest([]byte(`{"src/main.js":{"file":"assets/main-X.js","name":"main","isEntry":true}}`))
	if got := a.Script("chart"); got != "" {
		t.Errorf("Script(\"chart\") = %q, want \"\"", got)
	}
	if got := a.Style("main"); got != "" {
		t.Errorf("Style(\"main\") = %q, want \"\" (the fixture declares no css)", got)
	}
	var zero Assets
	if got := zero.Script("main"); got != "" {
		t.Errorf("zero Assets Script = %q, want \"\" — the zero value must resolve nothing", got)
	}
}

// TestLoadAssetsWithNoManifestReportsNotFound is the graceful-degradation
// contract. Deliberately does not assert on the embedded tree, which may or may
// not contain a build: it drives parseManifest's caller through the
// missing-file path directly.
func TestLoadAssetsWithNoManifestReportsNotFound(t *testing.T) {
	dir := t.TempDir()
	// An empty directory stands in for a dist tree with only .keep in it.
	if err := os.WriteFile(filepath.Join(dir, ".keep"), nil, 0o644); err != nil {
		t.Fatalf("write .keep: %v", err)
	}
	a, found := loadAssetsFrom(os.DirFS(dir))
	if found {
		t.Error("found = true with no manifest present")
	}
	if got := a.Script("main"); got != "" {
		t.Errorf("Script(\"main\") = %q, want \"\" when no manifest exists", got)
	}
}
```

- [ ] **Step 7: Run to verify they fail**

Run: `go test ./internal/web/ -run 'Assets|Manifest|UnknownEntry' -count=1`
Expected: FAIL, `undefined: LoadAssets`.

- [ ] **Step 8: Implement `assets.go`**

```go
package web

import (
	"encoding/json"
	"io/fs"
	"path"
)

// assetPrefix is where Routes serves the embedded dist tree. The manifest holds
// paths relative to dist, so every resolved value is this plus that.
const assetPrefix = "/static/build/"

// manifestPaths are the two places Vite has put its manifest. v5 moved it into
// .vite/; the older location is kept so a downgrade is a config change rather
// than a silent loss of every script tag.
var manifestPaths = []string{".vite/manifest.json", "manifest.json"}

// Assets resolves logical entry names to hashed, served paths.
//
// The zero value is valid and resolves nothing. That is the whole graceful
// degradation story: with no manifest, the templates emit no <script>, the
// islands never load, and the page is exactly the Phase 2 page — which works.
type Assets struct {
	scripts map[string]string // entry name -> "/static/build/<hashed>.js"
	styles  map[string]string
}

// LoadAssets reads the Vite manifest out of the embedded dist tree.
//
// A missing manifest is NOT an error: it returns an empty Assets and
// found=false, so a build made on a machine with no Node serves the
// no-JavaScript site rather than failing to start. cmd/airbg logs which of the
// two happened, because otherwise a developer who forgot `npm run build` has no
// way to discover it.
func LoadAssets() (Assets, bool) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return Assets{}, false
	}
	return loadAssetsFrom(sub)
}

// loadAssetsFrom is LoadAssets over any filesystem, so a test can drive the
// missing-manifest path without depending on whether anyone has built.
func loadAssetsFrom(fsys fs.FS) (Assets, bool) {
	for _, p := range manifestPaths {
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		a, err := parseManifest(raw)
		if err != nil {
			// A manifest that exists but does not parse is a real problem and
			// must not masquerade as "no manifest" — but it still must not stop
			// the process, or a bad build takes the site down instead of
			// degrading it. Reported as not-found so the startup line says so.
			return Assets{}, false
		}
		return a, true
	}
	return Assets{}, false
}

// manifestEntry is the subset of Vite's manifest shape this needs. Extra fields
// are ignored, so a Vite version that adds keys does not break the parse.
type manifestEntry struct {
	File    string   `json:"file"`
	Name    string   `json:"name"`
	IsEntry bool     `json:"isEntry"`
	CSS     []string `json:"css"`
}

// parseManifest maps entry NAMES to served paths.
//
// Keyed by Name rather than by the manifest's own key: the key is a source path
// ("src/main.js") that would put a directory layout into every template, while
// Name is the logical entry ("main") the templates already want to say. Only
// isEntry records are exposed — the rest are shared chunks the browser reaches
// through the entry's own imports, and emitting a script tag for one would load
// it twice.
func parseManifest(raw []byte) (Assets, error) {
	var manifest map[string]manifestEntry
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Assets{}, err
	}
	a := Assets{
		scripts: make(map[string]string),
		styles:  make(map[string]string),
	}
	for _, e := range manifest {
		if !e.IsEntry || e.Name == "" || e.File == "" {
			continue
		}
		a.scripts[e.Name] = assetPrefix + path.Clean(e.File)
		if len(e.CSS) > 0 {
			a.styles[e.Name] = assetPrefix + path.Clean(e.CSS[0])
		}
	}
	return a, nil
}

// Script returns the served path for an entry, or "" if unknown.
//
// "" rather than a guessed path, because the template guards on emptiness: a
// guess would emit a <script src> that 404s, which is a broken page, where ""
// is the server-rendered fallback.
func (a Assets) Script(entry string) string { return a.scripts[entry] }

// Style likewise, for the entry's bundled CSS.
func (a Assets) Style(entry string) string { return a.styles[entry] }
```

In `internal/web/render.go`, add the embed beside the existing two:

```go
// all:dist rather than dist — the plain form skips files beginning with "." or
// "_", which would exclude both the committed .keep and Vite's
// .vite/manifest.json, and the embed would then fail to compile on a clean
// checkout for a reason with no obvious connection to either.
//
//go:embed all:dist
var distFS embed.FS
```

Add `Assets Assets` to `PageData` (with a comment pointing at the `.keep` rationale), add an `assets Assets` field to `Renderer`, set it in `NewRenderer` via `rr.assets, _ = LoadAssets()`, and set `Assets: rr.assets` in `newPageData`. Parsed once at construction, like the templates.

- [ ] **Step 9: Add the conditional tags to the base template**

In `internal/web/templates/base.gohtml`, replace the stylesheet line with:

```gohtml
<link rel="stylesheet" href="/static/app.css">
{{with .Assets.Style "main"}}<link rel="stylesheet" href="{{.}}">
{{end}}</head>
```

and add before `</body>`:

```gohtml
{{with .Assets.Script "main"}}<script type="module" src="{{.}}"></script>
{{end}}</body>
```

`{{with}}` rather than `{{if}}`: it both guards on emptiness and rebinds the dot to the path, so the value cannot drift from the guard. The script goes last and is `type="module"` — modules defer by default, so it never blocks first paint, and the server-rendered content is already in the DOM when the island mounts.

- [ ] **Step 10: Add the startup log line**

In `cmd/airbg/main.go`'s `runServe`, after the logger exists and before `server.New`:

```go
	// One line, so a developer who ran `go run ./cmd/airbg` without building the
	// frontend discovers it in one second rather than wondering why the map is
	// missing. The no-manifest path is a supported mode, not an error — hence
	// Info, not Warn.
	if assets, found := web.LoadAssets(); found {
		log.Info("assets", "state", "loaded", "script", assets.Script("main"))
	} else {
		log.Info("assets", "state", "no manifest — serving without islands (run 'npm run build' in web/)")
	}
```

Match the surrounding call's logger variable name and field style; read the neighbouring lines first.

- [ ] **Step 11: Add the page-level tests**

Append to `internal/web/render_test.go`. Read the file first and reuse its existing renderer construction and request helpers:

```go
// TestPageEmitsTheHashedScriptWhenAManifestExists is the seam Vitest cannot
// see: whether the path Go resolved is the path the browser is told to fetch.
func TestPageEmitsTheHashedScriptWhenAManifestExists(t *testing.T) {
	rr := newTestRenderer(t)
	// Substitute a fixture manifest rather than depending on a real build, so
	// the assertion holds on a machine with no Node.
	rr.assets = mustParseFixtureManifest(t)

	body := renderIndex(t, rr)
	want := `<script type="module" src="/static/build/assets/main-DEADBEEF.js"></script>`
	if !strings.Contains(body, want) {
		t.Errorf("page does not contain %s\n---\n%s", want, body)
	}
}

// TestPageEmitsNoScriptWithoutAManifest is the graceful-degradation gate, and
// per the spec it is the assertion most at risk of being inert — it passes both
// when the code is right and when LoadAssets is never called at all. The
// fallback-content assertion is what gives it teeth: a page with no script AND
// no content would be a blank page, which is the failure this is guarding
// against, not the success.
func TestPageEmitsNoScriptWithoutAManifest(t *testing.T) {
	rr := newTestRenderer(t)
	rr.assets = Assets{} // the zero value: no manifest

	body := renderIndex(t, rr)
	if strings.Contains(body, "<script") {
		t.Errorf("page contains a script tag with no manifest:\n%s", body)
	}
	if !strings.Contains(body, `data-island="map"`) {
		t.Error("page lost its island placeholder")
	}
	if !strings.Contains(body, "<ul") && !strings.Contains(body, "<li") {
		t.Errorf("page has no server-rendered area list to fall back to:\n%s", body)
	}
}
```

Write `mustParseFixtureManifest` from the Step 6 fixture, and `renderIndex` from whatever `render_test.go` already does. If the index template's fallback markup is not a `<ul>`, assert on whatever it actually is — read the template.

- [ ] **Step 12: Run everything**

Run: `gofmt -l internal/web/ && go vet ./internal/web/ && go test ./internal/web/ -count=1` then `go test ./... -count=1`
Expected: `gofmt -l` prints nothing (you fixed the pre-existing `render.go`), all tests pass.

- [ ] **Step 13: Mutation-prove it**

Four mutations, one at a time, each reverted from a `cp` backup:

1. In `parseManifest`, key by the manifest key instead of `e.Name` (`a.scripts[k] = ...`). Expected: `Script("main") = "", want "/static/build/assets/main-DEADBEEF.js"`.
2. Drop the `if !e.IsEntry` guard and add a non-entry chunk to the fixture. Expected: whichever assertion you added for it; if the fixture has only an entry, this mutation is inert — extend the fixture with a `{"file":"assets/shared-X.js","name":"shared"}` record and an assertion that `Script("shared") == ""`, then re-run.
3. Change `LoadAssets`'s not-found return to `Assets{scripts: map[string]string{"main": "/static/build/main.js"}}, true` — the exact hardcoded-entry mutation the spec names. Expected: `TestPageEmitsNoScriptWithoutAManifest` must fail with `page contains a script tag with no manifest`. **If it does not fail, the test is inert and must be fixed before this task is complete** — that is the spec's explicit warning.
4. Change `//go:embed all:dist` to `//go:embed dist`. Expected: a compile failure (`pattern dist: cannot embed directory dist: contains no embeddable files`), which is the real proof that `all:` is load-bearing.

Quote each real failure.

- [ ] **Step 14: Commit**

```bash
git add web/package.json web/package-lock.json web/vite.config.js web/src/main.js \
        internal/web/dist/.keep internal/web/assets.go internal/web/assets_test.go \
        internal/web/render.go internal/web/render_test.go \
        internal/web/templates/base.gohtml cmd/airbg/main.go .gitignore
git status --short   # confirm no dist/ artifact and no CLAUDE.md staged
git commit -m "feat(web): build the frontend with Vite and embed the hashed bundles"
```

Record the pinned dependency versions in the commit body.

---

### Task 2: Serve the built assets immutably, and stop listing directories

**Why:** hashed filenames are worthless without immutable caching — that is the entire reason `manifest: true` exists. And `internal/web/pages.go` currently registers a bare `http.FileServer`, which serves a directory index for any path resolving to a directory. That was a deferred Phase 2 minor; with a `dist/` tree behind it, a listing enumerates every chunk, which is free reconnaissance and needless surface.

**Files:**
- Modify: `internal/web/pages.go`
- Test: `internal/web/pages_test.go` (create if absent)

**Interfaces:** produces two new unexported helpers in package `web`; no exported signature changes.

- [ ] **Step 1: Write the failing tests**

```go
// TestBuildAssetsAreImmutablyCacheable. A content-hashed filename can be cached
// forever by definition — its content cannot change without its name changing.
// Without this header the hash buys nothing: the browser revalidates every
// bundle on every navigation, which is the cost the whole manifest mechanism
// exists to avoid.
func TestBuildAssetsAreImmutablyCacheable(t *testing.T) {
	mux := newTestRenderer(t).Routes()

	// Any real file under the embedded dist tree. .keep is always present, which
	// is what makes this test runnable with no Node.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/build/.keep", nil))
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
	mux := newTestRenderer(t).Routes()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
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

// TestStaticDirectoriesAre404NotListings. A listing enumerates every chunk and
// every asset for free. net/http's FileServer does this by default, so the
// absence of a wrapper is the bug.
func TestStaticDirectoriesAre404NotListings(t *testing.T) {
	mux := newTestRenderer(t).Routes()

	for _, p := range []string{"/static/", "/static/build/"} {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body:\n%s", rec.Code, rec.Body)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/web/ -run 'Immutabl|HandWrittenStatic|Directories' -count=1`
Expected: FAIL — `Cache-Control = "", want "public, max-age=31536000, immutable"`, and the directory cases return 200 with a listing.

- [ ] **Step 3: Implement**

In `internal/web/pages.go`, replace the single `mux.Handle("GET /static/", ...)` with:

```go
	// Content-hashed bundles: cacheable forever, because the name changes when
	// the content does. This is the payoff for `manifest: true` in the Vite
	// config; without it the hashing buys nothing.
	mux.Handle("GET /static/build/", http.StripPrefix("/static/build/",
		cacheControl(noDirList(http.FileServer(http.FS(distSubFS()))), immutableCacheControl)))

	// Hand-written CSS keeps a stable name, so it gets a short TTL instead. An
	// immutable header here would pin an edited stylesheet in every visitor's
	// browser for a year.
	mux.Handle("GET /static/", cacheControl(noDirList(http.FileServer(http.FS(staticFS))), staticCacheControl))
```

with the helpers and constants:

```go
const (
	immutableCacheControl = "public, max-age=31536000, immutable"
	staticCacheControl    = "public, max-age=3600"
)

// distSubFS strips the "dist" prefix so /static/build/assets/x.js maps to
// dist/assets/x.js. The error is impossible — the directory is embedded, so it
// exists — and an empty FS would serve 404s rather than panic, which is the
// correct degradation for a path that only serves optional bundles.
func distSubFS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return distFS
	}
	return sub
}

// cacheControl sets the header before delegating, so a FileServer that writes
// its own headers cannot clobber it.
func cacheControl(next http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// noDirList turns a request for a directory into a 404 before the FileServer
// can render an index of it.
//
// Checked by path shape rather than by stat-ing the filesystem: a trailing
// slash is the only way http.FileServer serves a listing (it redirects
// "/dir" to "/dir/" first), so refusing the slash refuses the listing without a
// second filesystem lookup. An empty path — the prefix-stripped form of
// "/static/build/" — is the root directory and gets the same treatment.
func noDirList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

Add `io/fs` and `strings` to the imports. Note the ordering subtlety: `noDirList` must sit *inside* `StripPrefix` for `/static/build/` so it sees the stripped path — verify against the test rather than by reading.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/web/ -count=1` then `go test ./... -count=1`
Expected: PASS. A Phase 2 test may assert on `/static/` behaviour; if one breaks because it expected a listing, fix the assertion and say so.

- [ ] **Step 5: Mutation-prove it**

1. Change `immutableCacheControl` to `staticCacheControl` at the build route. Expected: `Cache-Control = "public, max-age=3600", want "public, max-age=31536000, immutable"`.
2. Remove the `noDirList` wrapper from the `/static/` route only. Expected: `TestStaticDirectoriesAre404NotListings/static/` fails with `status = 200`.
3. Move `cacheControl` outside `noDirList`. Expected: inert — a 404 carrying a cache header is harmless, and no test asserts it. Report it as inert.

- [ ] **Step 6: Commit**

```bash
git add internal/web/pages.go internal/web/pages_test.go
git commit -m "feat(web): serve hashed bundles immutably and 404 directory paths"
```

---

### Task 3: Basemap configuration and `CSP(basemapHost)`

**Why:** the map needs a tile vendor, the vendor's host must be in `connect-src`, and the two must not be able to drift apart. Deriving the CSP host from the style URL at startup means a vendor switch is one env var and cannot leave the policy pointing at the old host. The key is public by nature — it ships in a URL the browser fetches — so domain restriction at the vendor is the only real control, and that is a Phase 4 deployment step, not something code can enforce.

**Files:**
- Modify: `internal/httpx/headers.go`, `internal/httpx/headers_test.go`, `internal/httpx/chain.go`, `internal/httpx/chain_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/web/render.go` (carry the resolved style URL to the templates)
- Modify: `README.md`, `.env.example`

**Interfaces:**
- Produces:
  - `func httpx.CSP(basemapHost string) string` — `CSP("")` is byte-identical to today's `CSPValue`
  - `httpx.Chain.CSP string`
  - `func httpx.SecurityHeaders(next http.Handler, csp string) http.Handler` — the policy is now a parameter
  - `config.Config.BasemapStyleURL string` (with `{key}` already substituted), `config.Config.BasemapHost string`
  - `web.NewRenderer(cat, holder, baseURL, basemapStyleURL string)` — one added parameter
  - `server.Options.BasemapStyleURL string`, `server.Options.CSP string`
- Consumes: `config.envOr`, the existing `CSPValue` string as the `CSP("")` fixture.

**Design note.** `CSPValue` is currently a package constant read directly inside `SecurityHeaders`. Making the policy a parameter is the whole change: a per-process value cannot be a constant, and threading it through `Chain` keeps the composition order in one place. Keep `CSPValue` as an exported constant — it becomes the pinned baseline that `CSP("")` must equal, which is exactly the test the spec asks for.

- [ ] **Step 1: Write the failing CSP tests**

Append to `internal/httpx/headers_test.go`:

```go
// TestCSPWithNoBasemapIsUnchanged is the safety net for the whole refactor. A
// deployment with no basemap must get byte-for-byte the Phase 2 policy — so
// turning a constant into a function provably changed nothing for anyone not
// using the new feature.
func TestCSPWithNoBasemapIsUnchanged(t *testing.T) {
	if got := CSP(""); got != CSPValue {
		t.Errorf("CSP(\"\") differs from CSPValue:\n got: %s\nwant: %s", got, CSPValue)
	}
}

// TestCSPWidensExactlyTwoDirectives. Widening a policy by string surgery is how
// object-src 'none' or frame-ancestors 'none' silently disappears, so the test
// compares directive by directive rather than asserting a substring.
func TestCSPWidensExactlyTwoDirectives(t *testing.T) {
	base := directives(t, CSP(""))
	wide := directives(t, CSP("tiles.example"))

	if len(base) != len(wide) {
		t.Fatalf("directive count changed: %d -> %d", len(base), len(wide))
	}
	for name, baseVal := range base {
		wideVal, ok := wide[name]
		if !ok {
			t.Errorf("directive %q disappeared when widening", name)
			continue
		}
		switch name {
		case "connect-src":
			if wideVal != "'self' https://tiles.example" {
				t.Errorf("connect-src = %q, want \"'self' https://tiles.example\"", wideVal)
			}
		case "img-src":
			if !strings.Contains(wideVal, "https://tiles.example") {
				t.Errorf("img-src = %q, missing the basemap host", wideVal)
			}
			// data: and blob: are what MapLibre needs for canvas sprites and
			// worker-produced tiles; widening must not drop them.
			for _, scheme := range []string{"data:", "blob:"} {
				if !strings.Contains(wideVal, scheme) {
					t.Errorf("img-src = %q, lost %s", wideVal, scheme)
				}
			}
		default:
			if wideVal != baseVal {
				t.Errorf("directive %q changed from %q to %q; only connect-src and img-src may widen", name, baseVal, wideVal)
			}
		}
	}
	// The directives that make the policy worth having, pinned by name so a
	// future edit cannot quietly drop one.
	for _, name := range []string{"object-src", "base-uri", "form-action", "frame-ancestors", "script-src", "worker-src"} {
		if _, ok := wide[name]; !ok {
			t.Errorf("widened policy has no %s directive", name)
		}
	}
}

// TestSecurityHeadersUsesTheSuppliedPolicy. Without this the parameter could be
// ignored and every other test here would still pass, since they all call CSP
// directly.
func TestSecurityHeadersUsesTheSuppliedPolicy(t *testing.T) {
	const custom = "default-src 'none'"
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), custom).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != custom {
		t.Errorf("Content-Security-Policy = %q, want %q", got, custom)
	}
}

// TestSecurityHeadersFallsBackWhenGivenNoPolicy fails closed. An empty CSP
// header is worse than a wrong one: it is a page with no policy at all, and the
// most likely cause is a new call site that forgot the argument.
func TestSecurityHeadersFallsBackWhenGivenNoPolicy(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "").
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != CSPValue {
		t.Errorf("Content-Security-Policy = %q, want the CSPValue baseline", got)
	}
}

// directives splits a policy into name -> value. Fails the test on a malformed
// policy rather than returning a partial map that would make later assertions
// lie.
func directives(t *testing.T, policy string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, part := range strings.Split(policy, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, " ")
		if !ok {
			t.Fatalf("directive %q has no value", part)
		}
		out[name] = value
	}
	return out
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/httpx/ -run CSP -count=1`
Expected: FAIL, `undefined: CSP`.

- [ ] **Step 3: Implement `CSP`**

In `internal/httpx/headers.go`, keep `CSPValue` and add:

```go
// CSP builds the policy, widening connect-src and img-src by the basemap host.
//
// An empty host yields exactly CSPValue, byte for byte — pinned by
// TestCSPWithNoBasemapIsUnchanged, so a deployment with no basemap is provably
// unaffected by this function existing.
//
// Built by assembling named directives rather than by string-replacing inside
// CSPValue. Substring surgery on a policy is how `object-src 'none'` silently
// disappears: the edit that drops it looks like the edit that widens
// connect-src, and nothing fails.
//
// The host is a bare hostname (optionally with a port), taken from the basemap
// style URL's origin at startup — never from a request. https:// is prepended
// unconditionally: a tile vendor reached over plain HTTP would be a
// mixed-content error in the browser long before the CSP mattered.
func CSP(basemapHost string) string {
	connect := "'self'"
	img := "'self' data: blob:"
	if basemapHost != "" {
		connect += " https://" + basemapHost
		img += " https://" + basemapHost
	}
	return "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self'; " +
		"img-src " + img + "; " +
		"font-src 'self'; " +
		"connect-src " + connect + "; " +
		"worker-src 'self' blob:; " +
		"object-src 'none'; " +
		"base-uri 'none'; " +
		"form-action 'none'; " +
		"frame-ancestors 'none'"
}
```

Change `SecurityHeaders` to take the policy:

```go
func SecurityHeaders(next http.Handler, csp string) http.Handler {
	// Fail closed: an empty policy means a caller forgot the argument, and a
	// response with no CSP at all is a worse outcome than one with a policy
	// that does not know about the basemap.
	if csp == "" {
		csp = CSPValue
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe("securityHeaders")
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		// ... the rest unchanged
	})
}
```

In `internal/httpx/chain.go`, add to `Chain`:

```go
	// CSP is the policy SecurityHeaders sets. Per-process rather than constant,
	// because it is widened by the configured basemap host. Empty falls back to
	// CSPValue.
	CSP string
```

and in `Wrap`, `h = SecurityHeaders(h, c.CSP)`.

- [ ] **Step 4: Add the config**

In `internal/config/config.go`, add to `Config`:

```go
	// BasemapStyleURL is the MapLibre style JSON URL with AIRBG_BASEMAP_KEY
	// already substituted for its {key} placeholder. Empty means no basemap:
	// the map renders data markers over a plain background, so local
	// development needs no vendor account.
	//
	// The key is PUBLIC by nature — it ships in a URL the browser fetches.
	// Domain restriction at the vendor is the only control, and it is a Phase 4
	// deployment step, not something this process can enforce.
	BasemapStyleURL string

	// BasemapHost is BasemapStyleURL's hostname, used to widen the CSP. Derived
	// here rather than configured separately so a vendor switch cannot leave the
	// policy pointing at the old host.
	BasemapHost string
```

and in `Load()`, after the `BaseURL` block:

```go
	style := strings.TrimSpace(os.Getenv("AIRBG_BASEMAP_STYLE_URL"))
	if style != "" {
		// The key is substituted here, once, at startup. A template left
		// unsubstituted would reach the browser with a literal {key} in it and
		// fail every tile request with a vendor error nobody would connect to a
		// missing env var.
		style = strings.ReplaceAll(style, "{key}", os.Getenv("AIRBG_BASEMAP_KEY"))

		u, err := url.Parse(style)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return Config{}, fmt.Errorf("config: AIRBG_BASEMAP_STYLE_URL must be an absolute https URL (got %q)", style)
		}
		cfg.BasemapStyleURL = style
		cfg.BasemapHost = u.Host
	}
```

Requiring `https` is deliberate: an `http` tile source is a mixed-content failure in every browser, so accepting it would ship a map that cannot work.

Add both variables to `clearEnv` in `config_test.go`, plus:

```go
// TestBasemapKeyIsSubstitutedIntoTheStyleURL. An unsubstituted {key} reaches the
// browser and fails every tile request with a vendor error that looks nothing
// like "you forgot an environment variable".
func TestBasemapKeyIsSubstitutedIntoTheStyleURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_BASEMAP_STYLE_URL", "https://tiles.example/style.json?key={key}")
	t.Setenv("AIRBG_BASEMAP_KEY", "s3cret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.BasemapStyleURL, "https://tiles.example/style.json?key=s3cret"; got != want {
		t.Errorf("BasemapStyleURL = %q, want %q", got, want)
	}
	if got, want := cfg.BasemapHost, "tiles.example"; got != want {
		t.Errorf("BasemapHost = %q, want %q", got, want)
	}
}

// TestNoBasemapConfiguredIsNotAnError. Local development must work with no
// vendor account: the map renders markers over a plain background.
func TestNoBasemapConfiguredIsNotAnError(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BasemapStyleURL != "" || cfg.BasemapHost != "" {
		t.Errorf("BasemapStyleURL = %q, BasemapHost = %q, want both empty", cfg.BasemapStyleURL, cfg.BasemapHost)
	}
}

// TestRejectsNonHTTPSBasemapURL. An http tile source is a mixed-content failure
// in every browser, so accepting it would ship a map that cannot work.
func TestRejectsNonHTTPSBasemapURL(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"plain http", "http://tiles.example/style.json"},
		{"no scheme", "tiles.example/style.json"},
		{"no host", "https:///style.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
			t.Setenv("AIRBG_BASEMAP_STYLE_URL", tc.value)

			if _, err := Load(); err == nil {
				t.Errorf("Load() accepted AIRBG_BASEMAP_STYLE_URL=%q", tc.value)
			} else if !strings.Contains(err.Error(), "AIRBG_BASEMAP_STYLE_URL") {
				t.Errorf("error does not name the variable: %v", err)
			}
		})
	}
}
```

- [ ] **Step 5: Thread it through server and renderer**

`internal/server/server.go`: add `BasemapStyleURL string` and `CSP string` to `Options`, pass `CSP: opts.CSP` into the `httpx.Chain` literal, and pass the style URL as `web.NewRenderer`'s new fourth argument. `internal/web/render.go`: `NewRenderer` takes `basemapStyleURL string`, stores it on `Renderer`, and `newPageData` sets a new `PageData.BasemapStyleURL` field. `cmd/airbg/main.go`: pass `CSP: httpx.CSP(cfg.BasemapHost)` and `BasemapStyleURL: cfg.BasemapStyleURL`.

Building the policy in `main` rather than inside `server.New` keeps `server` from needing to know how a policy is assembled, and makes the one place the host reaches the CSP visible in the wiring.

- [ ] **Step 6: Add the end-to-end header assertion**

Append to `internal/server/e2e_test.go` (the `//go:build integration` file):

```go
// TestConfiguredBasemapReachesTheResponsePolicy is the wiring test: CSP() being
// correct is worthless if the value never reaches a response. Nothing else in
// the suite crosses config -> main -> server -> Chain -> SecurityHeaders.
func TestConfiguredBasemapReachesTheResponsePolicy(t *testing.T) {
	srv := newTestServer(t, func(o *server.Options) {
		o.CSP = httpx.CSP("tiles.example")
	})

	resp := get(t, srv, "/")
	got := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(got, "connect-src 'self' https://tiles.example") {
		t.Errorf("Content-Security-Policy = %q, missing the basemap host in connect-src", got)
	}
}
```

Adapt to however `e2e_test.go` actually builds its server and issues requests — read it first; the helper names above are illustrative and the real ones must be used.

- [ ] **Step 7: Document the variables**

`README.md`: two rows in the config table (`AIRBG_BASEMAP_STYLE_URL` no, empty; `AIRBG_BASEMAP_KEY` no, empty), and a short paragraph stating plainly that the key is public, that domain restriction at the vendor is the only control, that free tiers run around 100k tile requests a month and a public map will exceed that, and that visitor IPs go to the vendor and belong in the privacy note. `.env.example`: both variables, commented out, with the `{key}` placeholder shown.

- [ ] **Step 8: Run everything**

Run: `gofmt -l internal/httpx internal/config internal/server internal/web && go vet ./... && go test ./... -count=1` then `go test -tags=integration ./internal/server/ -count=1`
Expected: PASS. `gofmt -l` must print nothing — you have now touched the second pre-existing unclean file, `chain.go`.

- [ ] **Step 9: Mutation-prove it**

1. In `CSP`, change `"object-src 'none'; "` to `""`. Expected: `TestCSPWidensExactlyTwoDirectives` fails with `widened policy has no object-src directive` AND `TestCSPWithNoBasemapIsUnchanged` fails — the baseline test catches it too, which is the point of keeping `CSPValue`.
2. In `CSP`, widen `script-src` by the host as well. Expected: `directive "script-src" changed from "'self'" to "'self' https://tiles.example"; only connect-src and img-src may widen`.
3. In `SecurityHeaders`, ignore the parameter and set `CSPValue`. Expected: `TestSecurityHeadersUsesTheSuppliedPolicy` fails, and so does the e2e wiring test.
4. Remove the `strings.ReplaceAll` key substitution. Expected: `BasemapStyleURL = "https://tiles.example/style.json?key={key}", want ".../style.json?key=s3cret"`.
5. Drop the `u.Scheme != "https"` check. Expected: `Load() accepted AIRBG_BASEMAP_STYLE_URL="http://tiles.example/style.json"`.

- [ ] **Step 10: Commit**

```bash
git add internal/httpx/ internal/config/ internal/server/ internal/web/render.go README.md .env.example
git commit -m "feat(httpx): derive the CSP basemap host from the configured style URL"
```

---

### Task 4: The pure JavaScript logic

**Why first among the JS tasks:** tier selection, colour banding and the time conversion are the three places where a silent off-by-one produces a wrong answer that looks right. All three are pure functions testable without a DOM, which is the only kind of JS test this project keeps.

**Files:**
- Create: `web/src/lib/tier.js`, `web/src/lib/colour.js`, `web/src/lib/series.js`
- Test: `web/src/lib/__tests__/tier.test.js`, `colour.test.js`, `series.test.js`

**Interfaces:**
- Produces:
  - `tierFor(zoom) -> 'country' | 'city' | 'sensors'`
  - `colourFor(value, bands) -> string` (a CSS colour), and `NO_DATA_COLOUR`
  - `toUplotData({t, v}) -> [number[], number[]]` (x in epoch **seconds**)

- [ ] **Step 1: Write the failing tests**

`web/src/lib/__tests__/tier.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { tierFor } from '../tier.js'

// The boundaries are <, not <=. An off-by-one here silently changes which
// endpoint a whole zoom level hits — and at z=11 that is the difference between
// one cached aggregate and a per-area request that spends enumeration budget.
// Each boundary is asserted explicitly rather than sampled.
describe('tierFor', () => {
  it('serves the country aggregate below zoom 9', () => {
    expect(tierFor(0)).toBe('country')
    expect(tierFor(8.99)).toBe('country')
  })
  it('serves the city aggregate from 9 up to but not including 11', () => {
    expect(tierFor(9)).toBe('city')
    expect(tierFor(10.99)).toBe('city')
  })
  it('serves individual sensors from 11 up', () => {
    expect(tierFor(11)).toBe('sensors')
    expect(tierFor(18)).toBe('sensors')
  })
})
```

`web/src/lib/__tests__/colour.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { colourFor, NO_DATA_COLOUR } from '../colour.js'

// Bands as /api/v1/scales serves them: ascending, inclusive `upper`, and the
// top band's upper is null rather than a sentinel a caller could plot.
const BANDS = [
  { label: 'Good', upper: 5, colour: '#50f0e6' },
  { label: 'Fair', upper: 10, colour: '#50ccaa' },
  { label: 'Moderate', upper: 20, colour: '#f0e641' },
  { label: 'Extremely poor', upper: null, colour: '#7d2181' },
]

describe('colourFor', () => {
  it('picks the band containing the value', () => {
    expect(colourFor(3, BANDS)).toBe('#50f0e6')
    expect(colourFor(7, BANDS)).toBe('#50ccaa')
  })

  // `upper` is INCLUSIVE. "Exactly 50 µg/m³" landing in the wrong band is the
  // kind of bug nobody notices and a regulator would.
  it('treats upper as inclusive', () => {
    expect(colourFor(5, BANDS)).toBe('#50f0e6')
    expect(colourFor(10, BANDS)).toBe('#50ccaa')
  })

  it('falls to the open top band above every finite upper', () => {
    expect(colourFor(500, BANDS)).toBe('#7d2181')
  })

  // No data is not "good". Returning the first band would paint an area with no
  // readings the same colour as the cleanest air in the country.
  it('returns the no-data colour for null and undefined', () => {
    expect(colourFor(null, BANDS)).toBe(NO_DATA_COLOUR)
    expect(colourFor(undefined, BANDS)).toBe(NO_DATA_COLOUR)
  })

  it('returns the no-data colour when there are no bands', () => {
    expect(colourFor(10, [])).toBe(NO_DATA_COLOUR)
    expect(colourFor(10, undefined)).toBe(NO_DATA_COLOUR)
  })
})
```

`web/src/lib/__tests__/series.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { toUplotData } from '../series.js'

describe('toUplotData', () => {
  // uPlot wants x in epoch SECONDS. Handing it milliseconds is the classic way
  // a chart silently renders every point in 1970 with no error anywhere.
  it('converts RFC3339 timestamps to epoch seconds', () => {
    const [xs, ys] = toUplotData({ t: ['2026-08-11T00:00:00Z'], v: [12.5] })
    expect(xs).toEqual([1786492800])
    expect(ys).toEqual([12.5])
    // Guards the units directly: a millisecond value is three orders of
    // magnitude larger than any plausible epoch-second value.
    expect(xs[0]).toBeLessThan(1e11)
  })

  // uPlot given [[], []] must render an empty frame. Given null it throws, and
  // the whole chart island disappears with a console error.
  it('returns two empty arrays for an empty series', () => {
    expect(toUplotData({ t: [], v: [] })).toEqual([[], []])
    expect(toUplotData(undefined)).toEqual([[], []])
    expect(toUplotData({})).toEqual([[], []])
  })

  it('drops trailing values with no matching timestamp', () => {
    const [xs, ys] = toUplotData({ t: ['2026-08-11T00:00:00Z'], v: [1, 2, 3] })
    expect(xs).toHaveLength(1)
    expect(ys).toHaveLength(1)
  })
})
```

Verify the expected epoch value in the first test by computing it rather than trusting the number above: `node -e "console.log(Date.parse('2026-08-11T00:00:00Z')/1000)"`.

- [ ] **Step 2: Run to verify they fail**

Run: `cd web && npx vitest run`
Expected: FAIL — `Failed to resolve import "../tier.js"`.

- [ ] **Step 3: Implement the three modules**

`web/src/lib/tier.js`:

```js
// Which data source a zoom level may read. This is the anti-scraping design
// expressed client-side: the map picks a TIER, never a viewport query, because
// no endpoint accepts a bounding box.
//
// Boundaries are `<`, not `<=`, and each one is pinned by its own assertion. At
// z=11 an off-by-one is the difference between one cached country aggregate and
// a per-area request that spends enumeration budget.
export function tierFor(zoom) {
  if (zoom < 9) return 'country'
  if (zoom < 11) return 'city'
  return 'sensors'
}
```

`web/src/lib/colour.js`:

```js
// The colour for "we have no usable reading". Neutral grey, deliberately not a
// band colour: an area below the coverage threshold, or one with no data at all,
// must not be paintable as clean air.
//
// This and chrome colours are the only literal colours permitted in web/src —
// every band colour comes from /api/v1/scales, so a legislative change is a
// one-file server edit rather than a frontend release.
export const NO_DATA_COLOUR = '#9ca3af'

// colourFor picks the first band whose inclusive upper bound is at or above the
// value. bands come verbatim from /api/v1/scales, ascending, with the top band's
// upper === null meaning "open ended".
export function colourFor(value, bands) {
  if (value === null || value === undefined || Number.isNaN(value)) return NO_DATA_COLOUR
  if (!bands || bands.length === 0) return NO_DATA_COLOUR
  for (const band of bands) {
    // upper is INCLUSIVE: a value exactly on a boundary belongs to the lower
    // band. `upper == null` catches both null and undefined and is the open top.
    if (band.upper == null || value <= band.upper) return band.colour
  }
  // Reachable only if the scale has no open top band, which would be a server
  // bug. Grey rather than the last band's colour: better to show "unknown" than
  // to assert a band the scale does not actually claim.
  return NO_DATA_COLOUR
}
```

`web/src/lib/series.js`:

```js
// uPlot wants [xs, ys] with x in epoch SECONDS, so the only transform is a
// divide by 1000. Handing it milliseconds renders every point in 1970 and
// throws no error, which is why the unit is asserted directly in the test.
export function toUplotData(body) {
  const times = body?.t ?? []
  const values = body?.v ?? []
  // Truncated to the shorter of the two rather than padded. A payload with
  // mismatched column lengths is a server bug; plotting a value against a
  // missing timestamp would invent a data point.
  const n = Math.min(times.length, values.length)
  const xs = new Array(n)
  const ys = new Array(n)
  for (let i = 0; i < n; i++) {
    xs[i] = Date.parse(times[i]) / 1000
    ys[i] = values[i]
  }
  return [xs, ys]
}
```

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run`
Expected: PASS, 3 files.

- [ ] **Step 5: Mutation-prove the three that matter**

JS mutations follow the same rule as Go ones — break it, quote the real failure, revert:

1. `tier.js`: `zoom < 11` → `zoom <= 11`. Expected: `expect(tierFor(11)).toBe('sensors')` fails with `expected 'city' to be 'sensors'`.
2. `colour.js`: `value <= band.upper` → `value < band.upper`. Expected: the inclusive-boundary test fails with `expected '#50ccaa' to be '#50f0e6'`.
3. `colour.js`: return `bands[0].colour` for a null value. Expected: the no-data test fails with `expected '#50f0e6' to be '#9ca3af'`.
4. `series.js`: drop the `/ 1000`. Expected: `expected 1786492800000 to be 1786492800`, and the `toBeLessThan(1e11)` guard fails too.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/ web/src/lib/__tests__/
git commit -m "feat(web): add the pure tier, colour and series logic with tests"
```

---

### Task 5: `lib/api.js` — the client-side fetch policy

**Why:** per-entity responses are `Cache-Control: private`, so no shared cache absorbs anything and client-side caching is the frontend's job. The series limiter is 1 rps with a burst of 10, and the map's per-area sensor requests are enumeration-counted, so a single pinch-zoom gesture emitting a dozen `moveend` events must not become a dozen requests. With hardening Task 1 in place, 3a's own page loads never touch the series limiter — but this module still owns the policy, because 3b's metric switcher will.

**Files:**
- Create: `web/src/lib/api.js`
- Test: `web/src/lib/__tests__/api.test.js`

**Interfaces:**
- Produces: `getJSON(url, { retryOn429 = true } = {}) -> Promise<any>`, `clearCache()` (test-only seam), `RATE_LIMIT_RETRY_MS`
- Consumes: `globalThis.fetch`

- [ ] **Step 1: Write the failing tests**

`web/src/lib/__tests__/api.test.js`:

```js
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { getJSON, clearCache, RATE_LIMIT_RETRY_MS } from '../api.js'

function jsonResponse(body, status = 200) {
  return { ok: status >= 200 && status < 300, status, json: async () => body }
}

let fetchMock
beforeEach(() => {
  clearCache()
  fetchMock = vi.fn()
  globalThis.fetch = fetchMock
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

// In-flight dedup. Two map layers wanting the same overview during one zoom
// gesture must cost one request, not two: the second would spend a limiter token
// and, on a per-area endpoint, an enumeration observation for an area the user
// looked at once.
it('deduplicates concurrent requests for the same URL', async () => {
  fetchMock.mockResolvedValue(jsonResponse({ areas: [] }))

  const [a, b] = await Promise.all([getJSON('/api/v1/overview'), getJSON('/api/v1/overview')])

  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(a).toBe(b) // the same resolved value, not merely an equal one
})

// Client cache. Zooming out to z<9 and back in must re-render from memory with
// zero requests — which is also why the map caches the overview across tier
// changes rather than refetching per zoom event.
it('serves a repeat request from cache with no fetch', async () => {
  fetchMock.mockResolvedValue(jsonResponse({ areas: [1] }))

  await getJSON('/api/v1/overview')
  const second = await getJSON('/api/v1/overview')

  expect(fetchMock).toHaveBeenCalledTimes(1)
  expect(second).toEqual({ areas: [1] })
})

// A 429 is retried ONCE after a fixed delay. Not exponential backoff: a page
// that keeps retrying under a limiter is the storm the limiter exists to stop.
it('retries a 429 exactly once and then succeeds', async () => {
  fetchMock
    .mockResolvedValueOnce(jsonResponse({ error: 'rate_limited' }, 429))
    .mockResolvedValueOnce(jsonResponse({ areas: [2] }))

  const pending = getJSON('/api/v1/overview')
  await vi.advanceTimersByTimeAsync(RATE_LIMIT_RETRY_MS)

  await expect(pending).resolves.toEqual({ areas: [2] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

it('gives up after one retry and rejects', async () => {
  fetchMock.mockResolvedValue(jsonResponse({ error: 'rate_limited' }, 429))

  const pending = getJSON('/api/v1/overview')
  const assertion = expect(pending).rejects.toThrow(/429/)
  await vi.advanceTimersByTimeAsync(RATE_LIMIT_RETRY_MS)
  await assertion

  expect(fetchMock).toHaveBeenCalledTimes(2)
})

// A failure must not be cached. Caching it would make one transient 503 mean a
// permanently empty map for the rest of the page's life.
it('does not cache a failure', async () => {
  fetchMock.mockResolvedValueOnce(jsonResponse({}, 503))
  await expect(getJSON('/api/v1/overview', { retryOn429: false })).rejects.toThrow()

  fetchMock.mockResolvedValueOnce(jsonResponse({ areas: [3] }))
  await expect(getJSON('/api/v1/overview')).resolves.toEqual({ areas: [3] })
  expect(fetchMock).toHaveBeenCalledTimes(2)
})
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd web && npx vitest run api`
Expected: FAIL — `Failed to resolve import "../api.js"`.

- [ ] **Step 3: Implement**

`web/src/lib/api.js`:

```js
// The client-side fetch policy.
//
// Per-entity API responses are `Cache-Control: private`, so no shared cache
// absorbs anything and this module is the only cache there is. The series
// endpoints are limited to 1 rps with a burst of 10, and per-area requests are
// counted toward an enumeration budget by DISTINCT area — so a duplicate
// request is not merely wasteful, it spends a budget the user did not intend to
// spend.

// A 429 is retried once after a fixed delay. Deliberately not exponential
// backoff: under a limiter, a page that keeps retrying is the storm the limiter
// exists to stop.
export const RATE_LIMIT_RETRY_MS = 2000

// Keyed by URL, which already encodes (endpoint, entity, metric, period) — the
// four things that identify a distinct response. Lives for the page's lifetime;
// a reload is the invalidation.
const cache = new Map()
const inFlight = new Map()

// clearCache is a test seam. Nothing in the app calls it: a user who wants fresh
// data reloads, and the page's own Cache-Control TTL bounds staleness.
export function clearCache() {
  cache.clear()
  inFlight.clear()
}

export function getJSON(url, { retryOn429 = true } = {}) {
  if (cache.has(url)) return Promise.resolve(cache.get(url))

  // Concurrent callers await the SAME promise rather than each starting a
  // request. Without this, one pinch-zoom gesture's dozen moveend events become
  // a dozen requests and burn the whole burst.
  const pending = inFlight.get(url)
  if (pending) return pending

  const promise = fetchOnce(url, retryOn429)
    .then((body) => {
      cache.set(url, body)
      return body
    })
    .finally(() => {
      // Cleared on failure too, so a transient error is retryable rather than
      // permanently poisoning this URL for the page's lifetime.
      inFlight.delete(url)
    })

  inFlight.set(url, promise)
  return promise
}

async function fetchOnce(url, retryOn429) {
  let response = await fetch(url, { headers: { Accept: 'application/json' } })

  if (response.status === 429 && retryOn429) {
    await delay(RATE_LIMIT_RETRY_MS)
    response = await fetch(url, { headers: { Accept: 'application/json' } })
  }
  if (!response.ok) {
    // Thrown, not returned as a sentinel: the caller's catch is what leaves the
    // server-rendered fallback in place, and a sentinel would be rendered as
    // data by a caller that forgot to check.
    throw new Error(`${url}: HTTP ${response.status}`)
  }
  return response.json()
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
```

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run`
Expected: PASS, 4 files.

- [ ] **Step 5: Mutation-prove it**

1. Remove the `inFlight` lookup (always start a new request). Expected: `expected "spy" to be called 1 times, but got 2 times` in the dedup test.
2. Move `cache.set` before the `response.ok` check — i.e. cache failures. Expected: the do-not-cache-a-failure test fails.
3. Change `retryOn429` handling to loop until success. Expected: the give-up test hangs or exceeds its retry count; if the fake timers make it hang rather than fail, note that plainly and assert the call count instead.
4. Delete the `.finally(() => inFlight.delete(url))`. Expected: inert for these tests — every one either succeeds once or fails once, so nothing observes the leak. Report it as inert and note that the cleanup is there for the retry-after-failure path the map exercises.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/api.js web/src/lib/__tests__/api.test.js
git commit -m "feat(web): add the client fetch policy with dedup, cache and 429 retry"
```

---

### Task 6: The island loader and the map island

**Why this is one task:** the loader is nine lines and exists only to mount islands; reviewing it alone would gate on nothing. The map is where the tier rule, the click-to-select rule and the colour rule meet, and it is the deliverable a visitor actually sees.

**Files:**
- Modify: `web/src/main.js`
- Create: `web/src/islands/map.js`
- Modify: `internal/web/templates/index.gohtml`, `internal/web/templates/area.gohtml`
- Modify: `internal/i18n/bg.json`, `internal/i18n/en.json`
- Modify: `internal/web/render.go` (a `MapConfig` accessor for the templates)
- Test: `internal/web/render_test.go` (append)

**Interfaces:**
- Consumes: `tierFor`, `colourFor`, `NO_DATA_COLOUR`, `getJSON`; `PageData.BasemapStyleURL` from Task 3.
- Produces: `mount(el)` exported from `web/src/islands/map.js`.

**Design notes — read before writing code.**

1. **Configuration reaches JS as `data-*` attributes, never as an inline script.** The CSP has no `'unsafe-inline'` and never will, so a `<script>window.CONFIG = …</script>` is not an option and a `<script type="application/json">` block is a needless argument about whether CSP covers it. Server-rendered attributes on the island container are unambiguous, escaped by `html/template`, and already how `data-zoom` / `data-lon` / `data-lat` work.
2. **Translated strings come from the server too.** The islands need the legend labels, the `z≥11` hint and the 429 notice in the reader's language. Go owns the catalogue; duplicating it in JS would guarantee drift. So they arrive as `data-t-*` attributes.
3. **MapLibre and uPlot are driven imperatively from inside `mount`.** Wrapping their APIs in Svelte reactive statements is a known source of double-initialisation bugs and buys nothing. Svelte 5 is available for island UI chrome (the legend), and if the chrome turns out to be simpler as plain DOM, use plain DOM and say so — an unused dependency is a cost, and the plan does not mandate using Svelte for its own sake.
4. **The slug rule is the anti-enumeration property, not a UX preference.** At `z ≥ 11` the map needs a slug and must never derive one from the viewport — that would be a client-side bbox query. On `/area/{slug}` the slug is fixed from `data-slug`. On `/` it is the last overview feature the user clicked; with none clicked, the map shows city aggregates plus the hint. A scraper must click 28 oblasti and then every city marker, and `ObserveArea` sees each one.
5. **Debounce `moveend` by 250 ms** before any tier change fires a request, or one pinch-zoom gesture emits a dozen events and burns the burst.

- [ ] **Step 1: Add the i18n strings**

Add to both `internal/i18n/bg.json` and `internal/i18n/en.json` (Bulgarian is the default language; write real Bulgarian, and if you are unsure of a term, use the wording already in the file as a guide rather than inventing register):

| Key | EN |
|---|---|
| `map.legend.title` | `Air quality` |
| `map.legend.no_data` | `Not enough data` |
| `map.hint.select_area` | `Select an area to see individual sensors` |
| `map.error.rate_limited` | `Data is rate limited, retrying` |
| `map.error.unavailable` | `Map data is unavailable right now` |
| `chart.title` | `PM2.5, last 24 hours` |
| `chart.empty` | `No readings in the last 24 hours` |
| `chart.axis.value` | `µg/m³` |

If `internal/i18n` has a test asserting both files carry the same key set, it will catch a missed translation — check for one, and if none exists, add it: a missing key silently renders as the key itself, which reaches a reader as `map.hint.select_area`.

- [ ] **Step 2: Add the data attributes to the templates**

`internal/web/templates/index.gohtml` — replace the map div with:

```gohtml
<div id="map" data-island="map"
     data-zoom="7" data-lon="25.4858" data-lat="42.7339"
     data-metric="P2"
     data-basemap="{{.BasemapStyleURL}}"
     data-t-legend="{{.T "map.legend.title"}}"
     data-t-no-data="{{.T "map.legend.no_data"}}"
     data-t-hint="{{.T "map.hint.select_area"}}"
     data-t-rate-limited="{{.T "map.error.rate_limited"}}"
     data-t-unavailable="{{.T "map.error.unavailable"}}"></div>
```

`internal/web/templates/area.gohtml` — the same attribute block on `#area-map`, keeping its existing `data-slug`, `data-zoom`, `data-lon`, `data-lat`. `data-metric="P2"` exists so 3b's switcher has a seam to write into and 3a has no magic constant in JS.

`html/template` escapes each value for the attribute context automatically, so a translated string containing a quote cannot break out. Do not add manual escaping.

- [ ] **Step 3: Write the loader**

`web/src/main.js`:

```js
// Island loader: one entry module, one pass over the document.
//
// A registry of DYNAMIC imports rather than static ones, so Rollup splits each
// island into its own chunk and the index page downloads the map chunk and never
// the chart chunk. A `for` loop over [data-island] rather than per-page entry
// points, because Phase 2 already emits the attributes and the server stays
// ignorant of which bundles exist.
const ISLANDS = {
  map: () => import('./islands/map.js'),
  chart: () => import('./islands/chart.js'),
}

for (const el of document.querySelectorAll('[data-island]')) {
  const load = ISLANDS[el.dataset.island]
  if (!load) continue // unknown island: leave the server-rendered fallback

  // A failed load is swallowed on purpose. Every island's container sits BESIDE
  // server-rendered content, never replacing it, so a broken bundle degrades to
  // the Phase 2 page instead of a blank div. Logged, not silent, so a developer
  // sees it in the console.
  load()
    .then((m) => m.mount(el))
    .catch((err) => console.error('island failed:', el.dataset.island, err))
}
```

- [ ] **Step 4: Write the map island**

`web/src/islands/map.js`. This is the largest single file in the plan; the structure below is required, and the MapLibre API calls must be checked against the version you pinned rather than trusted from here:

```js
import maplibregl from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { tierFor } from '../lib/tier.js'
import { colourFor, NO_DATA_COLOUR } from '../lib/colour.js'
import { getJSON } from '../lib/api.js'

// Debounce before any tier change fires a request. One pinch-zoom gesture emits
// a dozen moveend events; undebounced, that is a dozen requests and the whole
// burst.
const MOVE_DEBOUNCE_MS = 250

const SOURCE_ID = 'airbg-data'
const LAYER_ID = 'airbg-markers'

export function mount(el) {
  const cfg = readConfig(el)
  const map = new maplibregl.Map({
    container: el,
    // An unset style URL is not fatal: the map renders data markers over a
    // plain background, so local development needs no vendor account.
    style: cfg.basemap ? cfg.basemap : blankStyle(),
    center: [cfg.lon, cfg.lat],
    zoom: cfg.zoom,
    attributionControl: { compact: true },
  })

  // On /area/{slug} the slug is fixed, one area, ever. On / it starts empty and
  // is only ever set by a deliberate click — never derived from the viewport,
  // which would be a client-side bbox query and is exactly what the API's
  // no-bbox rule forbids.
  const state = { slug: cfg.slug, tier: null, scales: null }

  map.on('load', async () => {
    map.addSource(SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: LAYER_ID,
      type: 'circle',
      source: SOURCE_ID,
      paint: {
        // Colour is resolved per feature in JS and carried on the feature, so
        // the band table stays the server's business. A MapLibre `step`
        // expression built from the scales would work too, but it would put the
        // band thresholds into the style — a second place they could drift.
        'circle-color': ['get', 'colour'],
        'circle-radius': ['interpolate', ['linear'], ['zoom'], 5, 5, 12, 9],
        'circle-stroke-width': 1,
        'circle-stroke-color': '#ffffff',
      },
    })

    // Fetched once per page load and Cache-Control: public, so it costs nothing
    // on a repeat visit.
    state.scales = await getJSON('/api/v1/scales').catch(() => null)

    await refresh(map, state, cfg)
  })

  map.on('moveend', debounce(() => refresh(map, state, cfg), MOVE_DEBOUNCE_MS))

  // Clicking an aggregate marker is what selects an area — the deliberate act
  // the enumeration budget is denominated in.
  map.on('click', LAYER_ID, (e) => {
    const slug = e.features?.[0]?.properties?.slug
    if (!slug) return
    state.slug = slug
    refresh(map, state, cfg)
  })
}

// refresh fetches the tier the current zoom permits and repaints.
async function refresh(map, state, cfg) {
  const tier = tierFor(map.getZoom())

  // The sensor tier needs a slug and must not invent one. With none selected,
  // fall back to the city aggregate and show the hint — a real friction cost,
  // accepted so that enumeration breadth is bounded by deliberate clicks rather
  // than by pan distance.
  const effective = tier === 'sensors' && !state.slug ? 'city' : tier
  showHint(map, effective !== tier ? cfg.t.hint : '')

  const url = urlFor(effective, state.slug)
  // Unchanged tier and slug: nothing to do. getJSON would serve from cache
  // anyway, but repainting the same features on every moveend is visible churn.
  const key = `${effective}:${state.slug ?? ''}`
  if (key === state.tier) return

  let body
  try {
    body = await getJSON(url)
  } catch (err) {
    showHint(map, cfg.t.unavailable)
    console.error('map data:', err)
    return
  }
  state.tier = key

  const features = effective === 'sensors'
    ? sensorFeatures(body, cfg.metric, state.scales)
    : areaFeatures(body, cfg.metric, state.scales)
  map.getSource(SOURCE_ID).setData({ type: 'FeatureCollection', features })
}

function urlFor(tier, slug) {
  if (tier === 'country') return '/api/v1/overview'
  if (tier === 'city') return '/api/v1/overview?tier=city'
  return `/api/v1/area/${encodeURIComponent(slug)}/sensors`
}

// areaFeatures maps the choropleth payload straight onto point features.
//
// covered === false renders in the neutral no-data grey with no value label.
// Fewer than three distinct sensors is not data, and drawing it in a band colour
// would imply a confidence the pipeline explicitly refuses.
function areaFeatures(body, metric, scales) {
  const bands = bandsFor(scales, metric)
  return (body?.areas ?? []).map((a) => ({
    type: 'Feature',
    geometry: { type: 'Point', coordinates: [a.lon, a.lat] },
    properties: {
      slug: a.slug,
      colour: a.covered ? colourFor(a.values?.[metric], bands) : NO_DATA_COLOUR,
      value: a.covered ? a.values?.[metric] ?? null : null,
      sensor_count: a.sensor_count,
    },
  }))
}

// sensorFeatures reads the COLUMNAR payload: parallel arrays, each metric a
// sibling key of the fixed columns. That shape was chosen in Phase 2 precisely
// for this consumer, so it maps onto features with no reshaping.
//
// A null in a metric column means the sensor does not report that metric, which
// is distinct from reporting zero and must stay distinct.
function sensorFeatures(body, metric, scales) {
  const bands = bandsFor(scales, metric)
  const s = body?.sensors ?? {}
  const ids = s.id ?? []
  const column = s[metric] ?? []
  const features = []
  for (let i = 0; i < ids.length; i++) {
    const value = column[i] ?? null
    features.push({
      type: 'Feature',
      geometry: { type: 'Point', coordinates: [s.lon[i], s.lat[i]] },
      properties: {
        id: ids[i],
        colour: colourFor(value, bands),
        value,
        quality: s.quality?.[i] ?? '',
      },
    })
  }
  return features
}

// bandsFor picks the scale table for one metric. The scales endpoint returns an
// array of tables; matching on `metric` rather than on array position means a
// reordered response cannot silently recolour the map.
function bandsFor(scales, metric) {
  if (!Array.isArray(scales)) return []
  return scales.find((s) => s.metric === metric)?.bands ?? []
}

function readConfig(el) {
  const d = el.dataset
  return {
    slug: d.slug || null,
    zoom: Number(d.zoom ?? 7),
    lon: Number(d.lon ?? 25.4858),
    lat: Number(d.lat ?? 42.7339),
    metric: d.metric || 'P2',
    basemap: d.basemap || '',
    // Strings come from the server, not from a JS catalogue: Go owns the
    // catalogue, and a second copy here would drift on the first edit.
    t: {
      legend: d.tLegend || '',
      noData: d.tNoData || '',
      hint: d.tHint || '',
      rateLimited: d.tRateLimited || '',
      unavailable: d.tUnavailable || '',
    },
  }
}

function emptyCollection() {
  return { type: 'FeatureCollection', features: [] }
}

// blankStyle is a valid MapLibre style with no tile sources, used when no
// basemap is configured. Data markers still render, over a plain background.
function blankStyle() {
  return { version: 8, sources: {}, layers: [{ id: 'bg', type: 'background', paint: { 'background-color': '#eef2f5' } }] }
}

function debounce(fn, ms) {
  let timer
  return (...args) => {
    clearTimeout(timer)
    timer = setTimeout(() => fn(...args), ms)
  }
}
```

`showHint` and the legend are the island's chrome. Implement them as a small DOM helper or a Svelte 5 component, whichever is simpler once you see the markup — but **set classes, never inline `style` attributes**, and put the rules in `internal/web/static/app.css`. A CSP with no `'unsafe-inline'` covers `style-src`, so inline styles written from JS are blocked; that is a real constraint, not a style preference.

- [ ] **Step 5: Build and verify by hand**

```bash
cd web && npm run build && cd .. && go build ./... && go run ./cmd/airbg serve
```

This needs a database — reuse whatever local Postgres the project's compose file starts. Load `/` and check: markers appear, the console is clean, no CSP violation is reported, zooming past 11 with nothing selected shows the hint, clicking an oblast then zooming past 11 loads sensors. **Record what you actually saw**, including any CSP violation, in the task report. This is the only step in the plan that a test cannot replace, because the thing being verified is that MapLibre renders.

- [ ] **Step 6: Add the Go-side attribute test**

Append to `internal/web/render_test.go`:

```go
// TestMapIslandCarriesItsConfiguration. The island reads all of this from
// data-* attributes because the CSP forbids an inline script; a missing
// attribute is a map that silently falls back to a default nobody chose.
func TestMapIslandCarriesItsConfiguration(t *testing.T) {
	rr := newTestRendererWithBasemap(t, "https://tiles.example/style.json?key=k")

	body := renderIndex(t, rr)
	for _, want := range []string{
		`data-metric="P2"`,
		`data-basemap="https://tiles.example/style.json?key=k"`,
		`data-t-hint="`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index page missing %s", want)
		}
	}
}

// TestNoBasemapRendersAnEmptyAttribute. Empty rather than absent, so the island
// reads "" and falls back to its blank style instead of reading undefined.
func TestNoBasemapRendersAnEmptyAttribute(t *testing.T) {
	rr := newTestRendererWithBasemap(t, "")
	body := renderIndex(t, rr)
	if !strings.Contains(body, `data-basemap=""`) {
		t.Errorf("index page has no empty data-basemap attribute:\n%s", body)
	}
}
```

- [ ] **Step 7: Run everything**

Run: `cd web && npx vitest run && cd .. && gofmt -l internal/web && go test ./internal/web/ ./internal/i18n/ -count=1 && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 8: Mutation-prove the Go side and reason about the JS side**

1. Remove `data-metric="P2"` from `index.gohtml`. Expected: `index page missing data-metric="P2"`.
2. Remove one of the new keys from `en.json`. Expected: the i18n parity test fails; if there is none, you added one in Step 1 and it must fail here.
3. State plainly in the report that `map.js`'s tier-switching, click-to-select and debounce logic is **not** covered by an automated test — only `tierFor` and `colourFor` are, and the integration is verified by the hand check in Step 5. That is the spec's deliberate choice (no jsdom map mounts), and it must be reported as a coverage gap rather than implied to be tested.

- [ ] **Step 9: Commit**

```bash
git add web/src/main.js web/src/islands/map.js internal/web/templates/ internal/i18n/ \
        internal/web/render.go internal/web/render_test.go internal/web/static/app.css
git commit -m "feat(web): mount the MapLibre island with zoom-tiered data sources"
```

---

### Task 7: The chart island

**Why:** it is the second half of the visitor-facing deliverable, and it is small — one series, one metric, one period. Period and metric selectors are 3b.

**Files:**
- Create: `web/src/islands/chart.js`
- Modify: `internal/web/templates/area.gohtml`
- Test: covered by `series.test.js` (Task 4) plus the hand check below

**Interfaces:** consumes `toUplotData`, `getJSON`; produces `mount(el)`.

**Note on hardening Task 1.** The chart requests `/api/v1/area/{slug}/series?metric=P2&period=24h`. That endpoint works today from Postgres and will work from the snapshot once the hardening plan's Task 1 lands. The JSON is identical either way, so this task does not depend on that one — but if the hardening task has **not** landed when you implement this, say so in your report, because it means every area page view is a database query and the capacity claim in the spec is not yet true.

- [ ] **Step 1: Add the chart's data attributes**

In `internal/web/templates/area.gohtml`:

```gohtml
<div id="chart" data-island="chart"
     data-slug="{{.Area.Slug}}"
     data-metric="P2"
     data-period="24h"
     data-t-title="{{.T "chart.title"}}"
     data-t-empty="{{.T "chart.empty"}}"
     data-t-value="{{.T "chart.axis.value"}}"></div>
```

- [ ] **Step 2: Write the island**

`web/src/islands/chart.js`:

```js
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { toUplotData } from '../lib/series.js'
import { getJSON } from '../lib/api.js'

export async function mount(el) {
  const cfg = {
    slug: el.dataset.slug,
    metric: el.dataset.metric || 'P2',
    period: el.dataset.period || '24h',
    title: el.dataset.tTitle || '',
    empty: el.dataset.tEmpty || '',
    valueLabel: el.dataset.tValue || '',
  }
  if (!cfg.slug) return // nothing to draw; leave the server-rendered aggregate

  const url = `/api/v1/area/${encodeURIComponent(cfg.slug)}/series` +
    `?metric=${encodeURIComponent(cfg.metric)}&period=${encodeURIComponent(cfg.period)}`

  let body
  try {
    body = await getJSON(url)
  } catch (err) {
    // The area page already shows the current aggregate value server-side, so a
    // failed chart leaves a complete page rather than a hole.
    console.error('chart data:', err)
    return
  }

  const data = toUplotData(body)
  if (data[0].length === 0) {
    // uPlot given [[], []] renders an empty frame rather than throwing, but an
    // empty frame with no explanation reads as a broken chart. Say why.
    el.textContent = cfg.empty
    return
  }

  new uPlot({
    title: cfg.title,
    width: el.clientWidth || 600,
    height: 240,
    // Epoch SECONDS — see lib/series.js. uPlot's default x scale is time, so
    // handing it milliseconds plots every point in 1970 with no error.
    series: [
      {},
      { label: cfg.valueLabel, stroke: '#2563eb', width: 2 },
    ],
    scales: { x: { time: true } },
  }, data, el)

  // Redrawn on resize rather than left at its initial width: the container is
  // fluid, and a chart that keeps its first-paint width is visibly wrong after
  // a phone rotates.
  const observer = new ResizeObserver(() => {
    // Left as a follow-up if uPlot's setSize proves fiddly; note it in the
    // report rather than shipping a half-working resize.
  })
  observer.observe(el)
}
```

Resolve the `ResizeObserver` body while implementing — `uPlot`'s `setSize({width, height})` is the call — or drop the observer entirely and report that resize is not handled. Do not ship the empty callback above.

- [ ] **Step 3: Build and verify by hand**

```bash
cd web && npm run build && cd .. && go run ./cmd/airbg serve
```

Load `/area/<a slug with data>` and check: the chart draws, the x axis shows the last 24 hours (**not 1970** — that is the units bug the test guards and the visual check confirms), the console is clean, no CSP violation. Then load an area with no recent readings and confirm the empty-state string appears rather than a blank box. Record what you saw.

- [ ] **Step 4: Run everything**

Run: `cd web && npx vitest run && cd .. && go test ./... -count=1` then `go test -tags=integration ./internal/server/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/islands/chart.js internal/web/templates/area.gohtml
git commit -m "feat(web): draw the 24-hour PM2.5 chart with uPlot"
```

---

### Task 8: The release image

**Why last:** it is the one artifact that can only be written once the build it wraps is known to work. Doing it earlier means debugging the Dockerfile and the toolchain at the same time.

**Files:**
- Create: `Dockerfile`, `.dockerignore` (check first — the repo already has one from an earlier commit; extend rather than replace)
- Modify: `README.md`

- [ ] **Step 1: Check the base image tags are current**

```bash
docker run --rm node:24-alpine node --version
docker run --rm golang:1.26-alpine go version
```

Per the user's standing requirement, image tags must be current, not older builds. If a newer major exists for either, use it and say which in the commit message. Confirm `gcr.io/distroless/static:nonroot` is still the right runtime tag.

- [ ] **Step 2: Read the existing `.dockerignore`**

An earlier commit added one to shrink the build context. It must now **not** exclude `web/` (stage 1 needs the sources) and **should** exclude `web/node_modules/` and `internal/web/dist/` — the image builds both itself, and copying a developer's stale `dist/` in would embed bundles that do not match the sources.

- [ ] **Step 3: Write the Dockerfile**

```dockerfile
# Stage 1: the frontend. Discarded entirely — no Node, no node_modules, and no
# npm-sourced code other than the built bundle reaches the runtime image.
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
# `ci` not `install`: it installs exactly the committed lockfile and fails if
# package.json and the lockfile disagree. `--ignore-scripts` because a
# postinstall hook is arbitrary code execution at build time from a transitive
# package nobody reviewed.
RUN npm ci --ignore-scripts
COPY web/ ./
# `npm run build` runs `npm audit --audit-level=high` first, so a high-severity
# advisory FAILS THE BUILD rather than printing a warning nobody reads.
RUN npm run build

# Stage 2: the binary. Embeds stage 1's output.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copied after `COPY . .` so it overwrites the .keep-only dist tree from the
# repo. Without this the embed picks up an empty tree and the image serves the
# no-JavaScript site — the exact failure the .keep design makes silent.
COPY --from=web /src/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /airbg ./cmd/airbg

# Stage 3: the binary alone. distroless:static has no shell, no package manager
# and no libc — nothing for an RCE to pivot into.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /airbg /airbg
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/airbg"]
CMD ["serve"]
```

- [ ] **Step 4: Build the image and verify the bundles are embedded**

```bash
docker build -t airbg:dev .
docker run --rm -e AIRBG_DATABASE_URL=postgres://invalid/db airbg:dev serve 2>&1 | head -5
```

The run will fail to reach a database — that is fine. What matters is the **`assets:` log line from Task 1 Step 10**: it must say `state=loaded` with a hashed script path. If it says `no manifest`, the `COPY --from=web` did not land and the image would ship the no-JavaScript site. That log line is the whole reason it exists.

- [ ] **Step 5: Document the build**

Add a short README section: `docker build -t airbg .` produces the release image; local development is `cd web && npm ci --ignore-scripts && npm run build` once, then `go run ./cmd/airbg serve`; skipping the npm build gives the working no-JavaScript site and one log line saying so.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore README.md
git commit -m "build: add the multi-stage node to go to distroless image"
```

---

## Self-review

**Spec coverage.** §4.1 layout → Task 1 (with `lib/` tests under `web/src/lib/__tests__/` rather than `web/src/islands/__tests__/`; the spec's §4.1 tree and its §9 list disagreed about where they go, and colocating with the code under test is the resolution). §4.2 `.keep` → Task 1. §4.3 Vite config → Task 1. §4.4 `assets.go` and the startup line → Task 1. §4.5 asset serving → Task 2. §4.6 release build → Task 8. §5 loader → Task 6. §6.1 tiers → Task 4 (`tier.js`) + Task 6 (use). §6.2 slug rule → Task 6. §6.3 rendering → Task 6. §6.4 colour → Task 4 + Task 6. §6.5 basemap and `CSP(basemapHost)` → Task 3. §7 chart → Task 7, with `seriesBody`'s shape consumed via `toUplotData` in Task 4. §8 fetch policy → Task 5. §9 testing → distributed, with the Go-side manifest tests in Task 1 and the CSP-equality test in Task 3. §10 file table → the File structure table above, minus the rows the hardening plan owns. §13.1 npm supply chain → Global Constraints, enforced in Task 1 (exact pins, committed lockfile, audit wired into `npm run build`) and Task 8 (`npm ci --ignore-scripts`, discarded stage).

**Deliberately not covered here, with the reason stated in the Scope boundary:** §7.1, §12.2, §12.3, §13.2's two headers, §13.3, §13.5 — all in the hardening plan. §12.3a — already on master.

**Two places this plan resolves a spec ambiguity rather than inheriting it.**
1. **How translated strings and the basemap URL reach JS.** The spec requires both (§6.5, §10's i18n row) and forbids inline scripts (§3), but never says how they cross the boundary. Resolved as server-rendered `data-*` attributes on the island container, matching how `data-zoom` already works. Stated in Task 6's design notes so a reviewer sees it as a decision, not an accident.
2. **Where the Vitest files live.** §4.1's tree says `src/islands/__tests__/`; §9's list is all `lib/` modules. Resolved as `web/src/lib/__tests__/`, colocated with the code under test, because every test the spec actually enumerates is a `lib/` test — §9 explicitly rules out component-render tests, so `islands/__tests__/` would be an empty directory.

**Type consistency.** `Assets`, `LoadAssets`, `Script`, `Style` are named identically in Tasks 1, 2 and 6. `CSP(basemapHost string) string` and `SecurityHeaders(next, csp)` are used with those exact signatures in Tasks 3 and 6. `web.NewRenderer` gains exactly one parameter, in Task 3, and every later reference assumes four arguments. `tierFor`, `colourFor`, `NO_DATA_COLOUR`, `toUplotData`, `getJSON`, `clearCache` and `RATE_LIMIT_RETRY_MS` are defined in Tasks 4 and 5 and consumed under those names in Tasks 6 and 7. `PageData.BasemapStyleURL` is added in Task 3 and read by the template in Task 6.

**Honesty about coverage.** Task 6 Step 8.3 and Task 7 Step 3 require the implementer to report, in writing, that the map and chart islands' integration is verified by a hand check and not by an automated test. That is the spec's deliberate choice — a test that mounts MapLibre in jsdom asserts that jsdom stubs work — but a plan that left it implicit would read as though the islands were tested. Three steps (Task 2 Step 5.3, Task 5 Step 5.4, Task 1 Step 13.2) predict a likely-inert mutation and instruct the implementer to report it as inert rather than manufacture a proof.
