# Phase 3a — airbg.org Frontend: Asset Pipeline, Map Island, Chart Island

**Date:** 2026-08-11
**Status:** design, awaiting user review
**Depends on:** Phase 2, merged at `43e1bf1` (JSON API, server-rendered BG/EN pages)
**Followed by:** Phase 3b (metric switcher, "sensors near me", per-sensor detail page)

## 1. Goal

Turn the Phase 2 server-rendered pages into a working map. Two interactive
components — a MapLibre map and a uPlot time-series chart — mounted as islands
into markup Go already renders, plus the build toolchain and asset pipeline that
delivers them.

Phase 3a ships when a visitor loading `/` sees Bulgaria with 28 coloured oblast
markers, can zoom into an area and see individual sensors, and clicking through
to `/area/{slug}` sees a 24-hour PM2.5 chart. Everything else is 3b.

## 2. Non-goals

- No metric switcher (3b). 3a renders one metric per surface: `P2` (PM2.5).
- No geolocation / "sensors near me" (3b). `/api/v1/locate` stays unconsumed.
- No per-sensor detail page (3b). Sensor markers are not clickable in 3a.
- No new Go dependency. `go.mod`'s direct require block stays byte-identical.
- No change to `www-root/` (the legacy PHP app).
- No server-side rendering of map or chart content. The no-JavaScript
  experience is exactly what Phase 2 already ships: the areas `<ul>` on the
  index and the aggregate values on the area page.

## 3. Constraints inherited from earlier phases

These are not up for renegotiation inside 3a; they shape every decision below.

| Constraint | Source | Consequence for 3a |
|---|---|---|
| API is non-public: no bbox, no unbounded list parameter | Phase 1 §7 | The map picks a *tier* by zoom, never a viewport query |
| Enumeration detected by *distinct* slugs per IP prefix | Phase 1 §7 | Panning must not fetch an area the user never looked at |
| Per-entity responses are `Cache-Control: private` | Phase 2 final review | Client-side caching is the frontend's job; no shared cache helps |
| Series routes limited to 1 rps, burst 10 | Phase 2 | Chart fan-out must be serialised and deduplicated |
| CSP has no `'unsafe-inline'`, no `'unsafe-eval'` | `internal/httpx/headers.go` | No inline `<script>`, no inline style attributes from JS that CSP would block; map style must be external JSON or fetched |
| Colours come from `/api/v1/scales`, never hardcoded | Phase 2 `scales.go` | A legislative band change is a one-file server edit, not a frontend release |
| Everything configurable via `AIRBG_*` env vars | user requirement | The basemap key is `AIRBG_BASEMAP_KEY`, never a literal |

## 4. Toolchain

### 4.1 Layout

```
web/                      # NOT served, NOT embedded — sources only
  package.json
  package-lock.json
  vite.config.js
  src/
    main.js               # island loader (the only Vite entry)
    islands/
      map.js
      chart.js
    lib/
      api.js              # fetch + client cache + in-flight dedup
      tier.js             # zoom -> source tier (pure)
      colour.js           # value + scale -> colour (pure)
      series.js           # series payload -> uPlot arrays (pure)
    islands/__tests__/    # Vitest, colocated by convention below
internal/web/
  dist/                   # Vite output — GIT-IGNORED except .keep
    .keep                 # committed, so //go:embed always has a match
  assets.go               # manifest resolution + template .Asset helper
```

`web/` is deliberately outside `internal/`: Go tooling walks `internal/` and a
`node_modules/` in there is a permanent source of noise.

### 4.2 The `dist/.keep` decision

`//go:embed all:dist` fails to compile if `dist/` is empty or absent. A
committed `dist/.keep` means:

- `go build ./...` and `go test ./...` work on a machine with no Node at all.
- With no `manifest.json` present, `assets.go` resolves to **no bundles**: the
  templates emit no `<script>` tags, the islands never load, and every existing
  Phase 2 page test still passes unchanged. Degradation is graceful and total.
- Build artifacts are never committed. `internal/web/dist/*` is gitignored with
  a `!internal/web/dist/.keep` exception.

The tradeoff, stated plainly: a developer who runs `go run ./cmd/airbg` without
first running `npm run build` gets the no-JavaScript site and no error telling
them why. Mitigated by a startup log line — see §4.4.

### 4.3 Vite configuration

```js
// web/vite.config.js
export default {
  root: 'web',
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    manifest: true,              // emits dist/.vite/manifest.json
    rollupOptions: { input: 'src/main.js' },   // relative to root
  },
}
```

`manifest: true` is load-bearing: hashed filenames are how the app gets
`Cache-Control: public, max-age=31536000, immutable` on assets without ever
serving a stale bundle. Without the manifest, Go cannot know the hashed name.

Node dependencies (dev): `vite`, `@sveltejs/vite-plugin-svelte`, `svelte`,
`vitest`. Runtime: `maplibre-gl`, `uplot`. Pinned exactly in
`package-lock.json`, which IS committed.

Svelte 5 is the component framework for the islands' UI chrome (legend, chart
header). MapLibre and uPlot own their own DOM subtrees and are driven
imperatively from inside the island's `onMount` — wrapping their APIs in
reactive statements is a known source of double-initialisation bugs and buys
nothing here.

### 4.4 `assets.go`

```go
// internal/web/assets.go
package web

// Assets resolves logical entry names to hashed, served paths.
// Zero value is valid and resolves nothing — see the .keep rationale.
type Assets struct {
    scripts map[string]string // entry name -> "/static/build/<hashed>.js"
    styles  map[string]string // entry name -> "/static/build/<hashed>.css"
}

// LoadAssets reads the Vite manifest out of the embedded dist tree.
// A missing manifest is NOT an error: it returns an empty Assets and
// reports found=false, so a no-Node build serves the no-JavaScript site.
func LoadAssets() (a Assets, found bool)

// Script returns the served path for an entry, or "" if unknown.
func (a Assets) Script(entry string) string
// Style likewise.
func (a Assets) Style(entry string) string
```

`cmd/airbg` logs exactly one line at startup: either
`assets: loaded N entries from embedded manifest` or
`assets: no manifest found — serving without islands (run 'npm run build' in web/)`.
That line is how a developer discovers §4.2's tradeoff in one second.

Templates gain `{{if .Assets.Script "main"}}<script type="module"
src="{{.Assets.Script "main"}}"></script>{{end}}` in the base layout. `PageData`
grows one field, `Assets Assets`, set by `Renderer.render` from a value the
`Renderer` holds — parsed once at construction, like the templates.

### 4.5 Asset serving

`internal/web/pages.go` currently registers `GET /static/` on a FileServer over
the embedded `static` dir with no cache headers and directory listing enabled
(a Phase 2 deferred minor). 3a fixes both, because hashed assets are worthless
without immutable caching:

- `GET /static/build/` serves the embedded `dist` tree with
  `Cache-Control: public, max-age=31536000, immutable`. Safe precisely because
  every filename is content-hashed.
- `GET /static/` keeps serving hand-written CSS with `public, max-age=3600`.
- Both wrap the FileServer so a request whose path resolves to a directory
  returns 404 rather than a listing.

### 4.6 Release build

Multi-stage `Dockerfile`:

1. `node:24-alpine` — `npm ci` then `npm run build`, producing
   `internal/web/dist/`.
2. `golang:1.26-alpine` — copies the repo plus stage 1's `dist/`, runs
   `go build`. The embed picks up the real bundles.
3. `gcr.io/distroless/static:nonroot` — the binary alone.

Image tags are current-major as of 2026-08 per the user's standing requirement;
they get re-checked at Phase 4 (deployment), not pinned by digest here.

## 5. The island loader

One entry module, one pass over the document:

```js
// web/src/main.js
const ISLANDS = {
  map:   () => import('./islands/map.js'),
  chart: () => import('./islands/chart.js'),
}

for (const el of document.querySelectorAll('[data-island]')) {
  const load = ISLANDS[el.dataset.island]
  if (!load) continue                   // unknown island: leave the fallback
  load().then(m => m.mount(el)).catch(() => {})  // failure leaves the fallback
}
```

Why a registry of dynamic imports rather than static ones: Rollup splits each
island into its own chunk, so the index page downloads the map island and never
the chart. Why a `for` loop over `[data-island]` rather than per-page entry
points: Phase 2 already emitted the attributes, and the server stays ignorant
of which bundles exist.

A failed island load is swallowed on purpose. Every island's container sits
*beside* server-rendered content, never replacing it, so a broken bundle
degrades to the Phase 2 page instead of a blank div.

Placeholders Phase 2 already ships, consumed as-is:

```html
<!-- index.gohtml -->
<div id="map" data-island="map" data-zoom="7" data-lon="25.4858" data-lat="42.7339"></div>

<!-- area.gohtml -->
<div id="chart" data-island="chart" data-slug="{{.Area.Slug}}"></div>
<div id="area-map" data-island="map"
     data-slug="{{.Area.Slug}}" data-zoom="{{.Area.Zoom}}"
     data-lon="{{.Area.Lon}}" data-lat="{{.Area.Lat}}"></div>
```

3a adds no new data attributes beyond `data-metric="P2"` on both, so 3b's
switcher has a seam to write into and 3a has no magic constant in JS.

## 6. Map island

### 6.1 Tier selection

The single most important rule in the frontend, because it is the anti-scraping
design expressed client-side. From Phase 1 §7:

| Zoom | Source | Endpoint | Size |
|---|---|---|---|
| `z < 9` | country aggregate | `/api/v1/overview` | ~4 KB |
| `9 ≤ z < 11` | city aggregate | `/api/v1/overview?tier=city` | ~15 KB |
| `z ≥ 11` | individual sensors | `/api/v1/area/{slug}/sensors` | per-area |

```js
// web/src/lib/tier.js — pure, unit-tested
export function tierFor(zoom) {
  if (zoom < 9) return 'country'
  if (zoom < 11) return 'city'
  return 'sensors'
}
```

The boundaries are `<`, not `<=`, and the test asserts each of 8.99, 9, 10.99,
11 explicitly. An off-by-one here silently changes which endpoint a whole zoom
level hits — and at `z=11` that is the difference between one cached aggregate
and a per-area request that spends enumeration budget.

### 6.2 Which area's sensors

At `z ≥ 11` the map needs a slug. It does **not** derive one from the viewport —
that would be a client-side bbox query, exactly what §3 forbids. Instead:

- On `/area/{slug}`, the slug comes from `data-slug`. Fixed, one area, ever.
- On `/`, the slug comes from the *last overview feature the user clicked or
  tapped*. Zooming past 11 without having selected an area shows the city
  aggregate markers plus a hint ("select an area to see individual sensors").

This is a deliberate UX cost: a user who pinch-zooms into their neighbourhood
sees aggregates until they click a marker. It buys the property that
enumeration breadth is bounded by *deliberate user clicks*, not by pan
distance. A scraper must click 28 oblasti and then every city marker, and the
`ObserveArea` counter sees each one.

### 6.3 Rendering

Aggregate tiers render as MapLibre GeoJSON circle layers built from
`areaPayloadEntry` (`lon`, `lat`, `values`, `covered`, `sensor_count`). The
sensor tier renders from `sensorColumns` — parallel typed arrays (`id`, `lon`,
`lat`, `quality`, `metrics`) that map straight onto features with no reshaping.
That columnar shape was chosen in Phase 2 precisely for this consumer.

`covered == false` areas render in a distinct neutral grey with no value label.
Fewer than 3 distinct sensors is not data; drawing it in a band colour would
imply a confidence the pipeline explicitly refuses.

### 6.4 Colour

```js
// web/src/lib/colour.js — pure, unit-tested
// bands come verbatim from /api/v1/scales; upper === null is the open top band.
export function colourFor(value, bands) { /* first band whose upper > value */ }
```

Tests pin: a value below the first upper, a value exactly *on* a band boundary,
a value above every finite upper (falls to the `upper: null` band), and
`null`/`undefined` (returns the no-data colour, not the first band). The
boundary case matters because "exactly 50 µg/m³" appearing in the wrong band is
the kind of bug nobody notices and a regulator would.

`/api/v1/scales` is fetched once per page load and is `Cache-Control: public`,
so it costs nothing on repeat visits.

### 6.5 Basemap

Tiles come from a hosted vendor (MapTiler / Protomaps), chosen by the user over
self-hosted PMTiles. Consequences, stated because they are real:

- The key is **public** — it ships in the style URL the browser fetches. Domain
  restriction at the vendor is the only control.
- Free tiers run around 100k tile requests/month. A public map will pass that.
  Phase 4 has to either budget for a paid tier or revisit self-hosting.
- Visitor IPs go to the vendor. That belongs in the privacy note.

Mechanics: two env vars, so switching vendor is a config change and not a code
change:

- `AIRBG_BASEMAP_STYLE_URL` — the style JSON URL, with a literal `{key}`
  placeholder. Empty by default.
- `AIRBG_BASEMAP_KEY` — substituted into that placeholder. Empty by default.

Both are read at startup, substituted server-side, and reach the browser only
through `PageData` — never a committed file. `config.go` derives the CSP
`connect-src` host from the style URL's origin at startup, so a vendor switch
cannot leave the CSP pointing at the old host. CSP `connect-src` widens
from `'self'` to `'self' https://<vendor-host>` and nothing else; `img-src`
already allows `data:` and `blob:`, which is what MapLibre needs.

`CSPValue` therefore stops being a bare constant and becomes the output of:

```go
// CSP builds the policy, widening connect-src and img-src by the basemap
// host. An empty host yields exactly today's CSPValue, byte for byte —
// pinned by a test, so a deployment with no basemap is provably unchanged.
func CSP(basemapHost string) string
```

An unset style URL is not fatal — the map renders data markers over a plain
background, so local development needs no vendor account.

## 7. Chart island

uPlot, fed `seriesBody` directly:

```go
type seriesBody struct {
    SensorID *int64      `json:"sensor_id,omitempty"`
    Slug     string      `json:"slug,omitempty"`
    Metric   string      `json:"metric"`
    Period   string      `json:"period"`
    Hourly   bool        `json:"hourly"`
    Times    []time.Time `json:"t"`
    Values   []float64   `json:"v"`
}
```

uPlot wants `[xs, ys]` with x as epoch **seconds**. So the only transform is
`t.map(s => Date.parse(s) / 1000)` — `lib/series.js`, pure, tested for the
empty-series case (uPlot given `[[], []]` must render an empty frame, not
throw) and for the seconds-not-milliseconds conversion, which is the classic
way a chart silently renders every point in 1970.

3a draws one series: `P2`, period `24h`, for the area in `data-slug`. Period
and metric selectors are 3b.

## 8. Rate-limit interaction

The series limiter is 1 rps / burst 10, keyed by IP prefix, and per-entity
responses are `private` so no shared cache absorbs anything. `lib/api.js` is
therefore not a convenience wrapper; it is the component that keeps the site
usable:

- **In-flight dedup.** One outstanding request per `(endpoint, entity, metric,
  period)` key. Concurrent callers await the same promise.
- **Client cache.** Successful responses cached in memory for the page's
  lifetime, keyed identically. Zooming out to `z<9` and back in re-renders from
  cache with zero requests — which is also why the map must cache the overview
  across tier changes rather than refetching per zoom event.
- **Debounce.** Map `moveend` is debounced 250 ms before any tier change fires
  a request. Without it a single pinch-zoom gesture emits a dozen `moveend`
  events and burns the burst.
- **429 handling.** A 429 is surfaced as a quiet inline notice ("data is rate
  limited, retrying") and retried once after 2 s. No exponential-backoff
  storm; no silent blank chart.

3a's fan-out is exactly one series request per area page, so the current limit
holds. The Phase 2 note to re-check the limit against real fan-out comes due in
**3b**, when the metric switcher can issue up to seven, and it is recorded here
so it is not lost: 3b must re-measure before shipping.

## 9. Testing

Two suites, split by what they can actually prove.

**Vitest, `web/`** — pure logic only, no DOM, no network:
- `tier.js`: the four boundary zooms in §6.1.
- `colour.js`: the five cases in §6.4.
- `series.js`: empty series, and epoch-seconds conversion.
- `api.js`: in-flight dedup (two concurrent calls → one fetch), cache hit (second
  call → zero fetches), 429 retried once then given up.

No component-render tests and no browser automation in 3a. A test that mounts
MapLibre in jsdom asserts that jsdom stubs work, not that the map does.

**Go, `internal/web`, `-tags=integration`** — the seam Vitest cannot see:
- With a manifest present (a fixture manifest, not a real build), the rendered
  page contains a `<script type="module">` whose `src` is the hashed path from
  that manifest. This is the test that catches a manifest-format change on a
  Vite upgrade.
- With no manifest, the page renders with **no** `<script>` tag and still
  contains its server-rendered fallback content.
- The response's `Content-Security-Policy` header permits the script source the
  page actually references — i.e. the shipped bundles are same-origin and the
  CSP is not decorative.
- `/static/build/<hashed>` responds `200` with the immutable `Cache-Control`;
  `/static/build/` responds `404`, not a listing.
- `CSP("")` equals the Phase 2 `CSPValue` string byte for byte, and
  `CSP("tiles.example")` differs from it in exactly the `connect-src` and
  `img-src` directives — so widening the policy can never silently drop
  `object-src 'none'` or a `frame-ancestors` clause.

Per this project's standing rule, each of those Go assertions gets mutation-
proven: break the production line, quote the real failure, revert, confirm
`git diff` is empty. The manifest-absent test is the one most likely to be
inert — it must fail when `LoadAssets` is changed to return a hardcoded entry
on a missing manifest.

## 10. Files

| Path | Change | Responsibility |
|---|---|---|
| `web/package.json`, `package-lock.json`, `vite.config.js` | create | toolchain |
| `web/src/main.js` | create | island loader |
| `web/src/islands/map.js` | create | MapLibre lifecycle, tier switching, layers |
| `web/src/islands/chart.js` | create | uPlot lifecycle |
| `web/src/lib/{tier,colour,series,api}.js` | create | pure logic + fetch policy |
| `internal/web/assets.go` | create | manifest resolution, `Assets` type |
| `internal/web/dist/.keep` | create | keeps `//go:embed` compiling |
| `internal/web/render.go` | modify | `Assets` on `PageData`; load at construction |
| `internal/web/pages.go` | modify | `/static/build/` route, cache headers, no listing |
| `internal/web/templates/base.gohtml` | modify | conditional `<script>`/`<link>` |
| `internal/web/templates/{index,area}.gohtml` | modify | add `data-metric="P2"` |
| `internal/httpx/headers.go` | modify | `CSPValue` → `CSP(basemapHost)`; `SecurityHeaders` takes the built policy |
| `internal/config/config.go` | modify | `AIRBG_BASEMAP_KEY`, `AIRBG_BASEMAP_STYLE_URL` |
| `Dockerfile` | create | multi-stage node → go → distroless |
| `.gitignore` | modify | `internal/web/dist/*`, `!.../dist/.keep`, `web/node_modules/` |
| `internal/i18n/{bg,en}.json` | modify | map/chart UI strings (legend, hint, 429 notice) |

## 11. Risks

1. **The `z≥11`-needs-a-click UX** (§6.2) is the weakest point of the design.
   It is a real friction cost accepted to preserve the enumeration bound. If it
   tests badly with real users, the fix is a server-side "which area contains
   this point" endpoint that is itself rate-limited — not a client-side bbox
   query.
2. **Basemap quota** (§6.5). A successful launch breaks the free tier. Phase 4
   decision, flagged now.
3. **Vite manifest path.** Vite moved the manifest to `dist/.vite/manifest.json`
   in v5. `assets.go` looks for it there and falls back to `dist/manifest.json`;
   the manifest-present test pins whichever one the pinned Vite emits.
4. **CSP and MapLibre workers.** `worker-src 'self' blob:` is already in
   `CSPValue`, which was written in Phase 1 anticipating exactly this. If a
   MapLibre upgrade needs more, the CSP change is a reviewed diff, never an
   `'unsafe-*'` allowance.
