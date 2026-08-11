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

### 7.1 The default series moves into the snapshot

Phase 2 serves `/api/v1/area/{slug}/series` from Postgres on every request. That
was acceptable when nothing called it. 3a calls it on **every area page load**,
which would make the chart the only database-backed page view on the site — see
§12 for why that collapses.

So 3a moves the one series 3a actually draws into the precomputed snapshot,
where it belongs:

- `snapshot.Snapshot` grows `AreaSeries map[string]Body`, keyed by slug, holding
  the `P2` / `24h` payload. Built, marshalled, gzipped and ETagged by
  `snapshot.Build` exactly like `AreaSensors`, once per ingest cycle.
- `handleAreaSeries` serves from the snapshot when
  `metric == "P2" && period == "24h"`, and falls through to the database for
  every other combination.
- The snapshot-served path spends **no** series-limiter token and issues no
  query. It stays `Cache-Control: private` — it is per-entity and enumerable, so
  `ObserveArea` must still see it.

This is not an optimisation bolted on late. The 24-hour area aggregate changes
once per ingest cycle, identical to every other payload already in the
snapshot; serving it from Postgres per request was the anomaly. It also means
3a's page-load fan-out is **zero** database queries, which is the property that
makes the answer to "does this scale to thousands of users" a simple yes.

Per-sensor series (3b) and non-default periods stay database-backed on purpose:
they are not precomputable without building a payload per sensor per period,
which is a cache larger than the data.

## 8. Rate-limit interaction

The series limiter is 1 rps / burst 10, keyed by IP prefix, and per-entity
responses are `private` so no shared cache absorbs anything. With §7.1 in place
3a's own page loads never touch that limiter — but `lib/api.js` still owns the
fetch policy, because 3b's fan-out will, and because the map's per-area sensor
requests are enumeration-counted:

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

With §7.1, 3a's steady-state fan-out is one snapshot read per area page and no
limiter token at all, so the current limit holds with room to spare. The Phase 2
note to re-check it against real fan-out comes due in **3b**, when the metric
switcher can issue up to seven metrics × four periods against the
database-backed fall-through path. Recorded here so it is not lost: 3b must
re-measure before shipping, and should consider whether the switcher's defaults
belong in the snapshot too.

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

**Go, `internal/{snapshot,api,server}`** — the capacity and hardening work:
- `Build` populates `AreaSeries` for every known slug, and a `P2`/`24h` request
  is answered with the snapshot's bytes while the pool records **zero**
  acquisitions. The zero-query assertion is the whole point of §7.1; a test that
  only checks the response body would pass with the DB path still live.
- A non-default period on the same slug still reaches Postgres and returns real
  data — the fall-through is not accidentally dead.
- With the semaphore filled, the next DB-backed request returns `503` with
  `Retry-After`, **immediately** rather than after a delay, and
  `airbg_admission_rejected_total` increases by one. Assert the delta, not the
  absolute count: `internal/metrics` counters are process-global.
- The limiting listener closes connection N+1 while the first N stay usable.
- **Bulkhead (§12.3a):** with `collectorPool` saturated by long-running queries,
  an API request still completes. The mutation to prove this is not inert:
  pointing both consumers back at one pool must make the test hang until the
  request's own timeout, not merely change a number.
- `Permissions-Policy` is present on an HTML response and denies `geolocation`.

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
| `internal/config/config.go` | modify | `AIRBG_BASEMAP_KEY`, `AIRBG_BASEMAP_STYLE_URL`, `AIRBG_MAX_DB_INFLIGHT`, `AIRBG_MAX_CONNS`, `AIRBG_DB_API_CONNS`, `AIRBG_DB_COLLECTOR_CONNS` |
| `Dockerfile` | create | multi-stage node → go → distroless |
| `.gitignore` | modify | `internal/web/dist/*`, `!.../dist/.keep`, `web/node_modules/` |
| `internal/i18n/{bg,en}.json` | modify | map/chart UI strings (legend, hint, 429 notice) |
| `internal/snapshot/{snapshot,build}.go` | modify | `AreaSeries map[string]Body` (§7.1) |
| `internal/api/series.go` | modify | snapshot fast path for `P2`/`24h`; DB fall-through |
| `internal/api/router.go` | modify | admission semaphore on DB-backed routes (§12.3) |
| `internal/server/server.go` | modify | limiting listener (§13.3) |
| `cmd/airbg/main.go` | modify | two pools: `apiPool`, `collectorPool` (§12.3a) |
| `internal/db/db.go` | modify | `Open` takes a max-conns override |

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
4. **Bundle weight.** MapLibre GL JS is the heaviest thing on the site by an
   order of magnitude (~250 KB gzipped). It is content-hashed and
   `immutable`, so the edge serves it and a repeat visitor pays nothing — but
   the first paint on a slow mobile connection is dominated by it. Measured at
   3a's end; if it is unacceptable, the lever is lazy-loading the map island on
   viewport intersection, not swapping libraries.
5. **`/healthz` is on the private, loopback-bound listener.** Correct for a
   single-container deploy whose orchestrator probes locally; an external load
   balancer cannot reach it. Phase 4 decision, recorded here so it is not
   discovered during a deployment.

## 12. Load and capacity

The question this section answers: does the design hold at hundreds to
thousands of simultaneous visitors, and where does it break first.

### 12.1 What is already effectively free

Phase 2's read path is better than a typical JSON API by construction, and 3a
depends on that:

- `snapshot.Body` holds **pre-marshalled JSON, pre-gzipped bytes, and an ETag**,
  all computed once per ingest cycle. Serving a request is
  `atomic.Pointer.Load()` plus one write of an existing byte slice. No
  per-request `json.Marshal`, no per-request `gzip.Write`, no allocation
  proportional to payload size.
- `AreaSensors` is a prebuilt map keyed by slug, so even the per-area sensor
  payload — the hottest data path once users zoom in — is a map lookup.
- Aggregates (`/overview` both tiers, `/areas`, `/meta`, `/scales`) are
  `Cache-Control: public, max-age=150` with an ETag, so Cloudflare absorbs
  essentially all of that traffic and the origin sees roughly one request per
  path per 150 s per PoP.

Consequence: on the aggregate and sensor paths, thousands of concurrent visitors
is a bandwidth question, not a CPU or database question. No change needed.

### 12.2 Where it breaks: the database-backed series path

Phase 2 serves every `/series` request from Postgres. Two facts combine badly:

- The series limiter is **per IP prefix** — 1 rps, burst 10. It bounds what one
  client can do and says nothing about aggregate load.
- `db.Open` calls `pgxpool.ParseConfig` without setting `MaxConns`, so the pool
  defaults to `max(4, numCPU)` connections.

So N distinct client prefixes are collectively permitted N requests per second,
funnelled into a handful of connections. Excess requests block inside
`pool.Acquire`, each holding a goroutine and a socket, until the 30 s
`WriteTimeout` fires — at which point a large fraction of requests fail at once.
That is queue collapse, and the per-IP limiter never sees it coming because
every individual client is behaving perfectly.

Nothing exercised this in Phase 2 because nothing called `/series`. 3a would
have called it on **every area page view**.

Two changes close it:

1. **§7.1** — the series 3a actually draws comes out of the snapshot, so the
   normal page view makes zero queries. This is the fix that matters.
2. **§12.3** — a global cap on what remains, so the residual DB path fails fast
   instead of queueing.

### 12.3 Admission control on the database-backed routes

A counted semaphore, sized from config, wraps the handlers that can still reach
Postgres (the `/series` DB fall-through and `/locate`):

- Acquire is **non-blocking**. No slot free → `503` with `Retry-After: 2` and a
  `airbg_admission_rejected_total` increment. Never a queue.
- Size defaults to `pool_max_conns × 2` and is settable via
  `AIRBG_MAX_DB_INFLIGHT`, because the right number depends on the deployed
  Postgres, not on the code.

Failing 10 % of requests in 2 ms is a functioning site under load. Queueing 100 %
of them for 30 s is an outage that also looks like an outage to every user who
was only trying to read the map. The semaphore is what converts one into the
other, and it is why per-IP rate limiting alone is not capacity control.

`pool_max_conns` also gets set explicitly in the deployed `AIRBG_DATABASE_URL`
rather than left to a CPU-count default, so capacity is a stated number and not
a property of the container's core allocation.

### 12.3a Separate the collector's pool from the API's

`cmd/airbg/main.go` opens exactly one `pgxpool` and hands it to both the
collector and the request handlers. `db.AssignStatementTimeout` is **60 s**,
bounding the ingest cycle's `area × sensor` `ST_Covers` join.

So on every poll cycle the collector may legitimately hold connections for up to
a minute, out of a pool defaulting to `max(4, numCPU)`. Request handlers starve
behind it.

This is a worse failure than §12.2 in one specific way: it needs **no traffic
and no attacker**. It happens on a schedule, by design, and every control
already in place — the per-IP limiter, the admission semaphore — sees a
perfectly healthy system, because it is one.

3a opens **two pools** from the same `AIRBG_DATABASE_URL`:

- `apiPool` — sized `AIRBG_DB_API_CONNS` (default 8). Serves request handlers.
  Never touched by ingest.
- `collectorPool` — sized `AIRBG_DB_COLLECTOR_CONNS` (default 4). Serves the
  poll cycle, backfill and area maintenance, and is the only pool where a 60 s
  statement timeout is reachable.

This is a bulkhead, and it is the mechanism the design was missing. Rate
limiting bounds one client; the semaphore bounds the crowd; the bulkhead stops
two *different workloads* from consuming each other's capacity. None of the
three substitutes for another.

### 12.4 What still needs measuring, and when

Load testing belongs in Phase 4 against real infrastructure, not in 3a against
a laptop. What 3a owes Phase 4 is the numbers to test against, recorded here:

| Path | Origin cost per request | Expected origin RPS at 1000 concurrent users |
|---|---|---|
| `/`, `/area/{slug}` (HTML) | template execute, `public, max-age=150` | low — edge-absorbed |
| `/static/build/*` | embed read, `immutable` | ~0 after first fill |
| `/api/v1/overview*`, `/areas`, `/meta`, `/scales` | pointer load + write | ~0 — edge-absorbed |
| `/api/v1/area/{slug}/sensors` | map lookup + write, `private` | one per zoom-in per user |
| `/api/v1/area/{slug}/series` (P2/24h) | map lookup + write, `private` | one per area page view |
| `/api/v1/area/{slug}/series` (other) | **Postgres query** | bounded by §12.3 |

The only row with an unbounded shape is the last one, and it is now the only row
with a hard cap in front of it.

## 13. Security hardening

§3 lists what earlier phases already enforce. This section covers what Phase 3a
newly puts at risk, because a frontend build step is a materially different
attack surface from a stdlib-only Go binary.

### 13.1 The npm supply chain — the largest new surface

Until now the project's dependency rule has been absolute: no new Go dependency,
ever. 3a breaks that shape deliberately, and it is worth naming precisely what
is being accepted. `maplibre-gl`, `uplot`, `svelte`, `vite` and `vitest` bring
in hundreds of transitive packages, whose build-time code runs on the build
machine and whose output is **embedded in the Go binary and served from the
site's own origin under a CSP that trusts `'self'`**. A malicious package does
not need to escape a sandbox; it only needs to write into the bundle.

Controls, all in scope for 3a:

- `package-lock.json` is committed, and the build uses `npm ci` — never
  `npm install`, which is permitted to resolve differently.
- **`npm ci --ignore-scripts`.** Lifecycle scripts (`preinstall`, `postinstall`)
  are the actual mechanism of nearly every published npm compromise. None of
  the five direct dependencies needs one; if a future dependency does, that is
  a reviewed decision, not a default.
- Direct dependencies are pinned to exact versions — no `^`, no `~`.
- `npm audit --audit-level=high` runs in the build stage and fails it. A
  vulnerability advisory that lands after the lockfile was written should break
  the build, not ship.
- The Node stage produces `dist/` and nothing else; it does not appear in the
  final image, so no Node runtime, no `node_modules`, and no npm-installed
  binary reaches production.

Residual risk, stated rather than hidden: a compromised release of a pinned
dependency that passes `npm audit` at build time still lands in the bundle. The
mitigation available to a project this size is a small, boring dependency set
and no automatic updates. Five direct dependencies is a deliberate ceiling.

### 13.2 Response headers

- **`Permissions-Policy` is added now**, denying everything the site does not
  use: `geolocation=(), camera=(), microphone=(), payment=(), usb=()`. 3b opens
  `geolocation=(self)` as a reviewed one-line change when the locate button
  ships. This is the header that keeps a compromised bundle from silently
  reaching for device capabilities, which is exactly the §13.1 failure mode.
- **CSP stays free of `'unsafe-inline'` and `'unsafe-eval'`.** MapLibre needs
  `worker-src 'self' blob:`, which Phase 1 already anticipated and put in
  `CSPValue`. Nothing in 3a widens the policy except the basemap host, and
  `CSP("")` is tested byte-identical to Phase 2's constant (§9).
- **`Cross-Origin-Resource-Policy: same-origin`** joins the existing
  `Cross-Origin-Opener-Policy`, so the JSON payloads cannot be pulled into a
  third-party document context.

### 13.3 Connection-level limits

Phase 2's timeouts bound how long a single request may take. They do not bound
how many sockets a host may hold open, so file-descriptor exhaustion is
currently unaddressed: 50 000 idle connections that each send one byte of a
request header every 4 s cost almost nothing to create and defeat nothing that
is currently in place.

3a adds a limiting `net.Listener` wrapping the public listener: a counted
accept, with the connection closed immediately when the count is exceeded. Cap
from `AIRBG_MAX_CONNS`, defaulting to 4096. Hand-rolled in ~30 lines rather than
taking `golang.org/x/net/netutil` — the no-new-Go-dependency rule holds.

This overlaps with Cloudflare's own protection, and that overlap is the point:
per §11 and Phase 4, the origin being reachable only through Cloudflare is an
*unverified assumption* today. A control that only works when the assumption
holds is not a control.

### 13.4 The third-party basemap

Accepted by the user with the tradeoffs stated (§6.5); recorded here as a
security fact rather than re-litigated. The vendor serves a style JSON that may
reference sprite, glyph and tile URLs of its choosing. CSP constrains those to
the vendor's host, so a vendor compromise means the vendor learns which tiles a
visitor requests — it does not give script execution on the origin. That is the
correct boundary, and it is the reason the vendor host is added to `connect-src`
and `img-src` only, never to `script-src`.

### 13.5 Mechanisms considered and deliberately not implemented

Written down because each one is a thing a reviewer will reasonably ask for, and
an undocumented absence looks like an oversight.

**CORS — none, on purpose.** There are no `Access-Control-*` headers anywhere in
the project and none should be added. Two reasons. First, the frontend is
same-origin, so it never needs them. Second, and more importantly, CORS is
routinely mistaken for a server-side control: the absence of
`Access-Control-Allow-Origin` prevents other-origin **browser JavaScript** from
reading a response, and prevents nothing else. `curl`, a scripted scraper and a
server-side fetch are all entirely unaffected. It is not an anti-extraction
mechanism and cannot be made into one. Adding a permissive ACAO would be a
downgrade with no compensating benefit. The header doing real work in this space
is `Cross-Origin-Resource-Policy` (§13.2).

**Circuit breaker on the upstream API — not implemented.** The poll runs once
per interval (floor 30 s, deployed in minutes), single-threaded, with an
`http.Client` timeout. Failure already degrades correctly: the snapshot is
last-known-good behind an atomic pointer, and `airbg_snapshot_age_seconds` makes
staleness observable and alertable. A breaker in front of a call made once every
few minutes protects nothing. Relatedly, the collector does **not** retry a
failed poll, and should not start: retries amplify load against a struggling
upstream and the next cycle is minutes away regardless.

**Circuit breaker on Postgres — not implemented; a scoped timeout instead.** The
admission semaphore (§12.3) already sheds load and fails fast. The only thing a
breaker would add is that, while Postgres is unreachable, the in-flight slots
fail immediately rather than each paying the full statement timeout. Real but
small, and achievable with less machinery: the series fall-through query gets a
short scoped statement timeout rather than the pool default. Same effect, no
breaker state to get wrong.

**CSRF protection — not applicable.** No state-changing endpoint, no cookies, no
sessions; every route is a GET. There is nothing to forge.

**HTTP/2 concurrent-stream limits — not applicable yet.** TLS terminates at
Cloudflare and the origin speaks HTTP/1.1. If the origin ever serves h2c
directly, `MaxConcurrentStreams` becomes relevant and this decision must be
revisited.

**mTLS or request signing between Cloudflare and the origin — Phase 4.** The
origin-lock decision is the actual control; implementing this here would be a
second, weaker copy of it.

### 13.6 What 3a does not change

No authentication (Phase 1 decision: anti-extraction is tiering, not
authentication). No secret reaches the browser except the basemap key, which is
public by the vendor's design and restricted by domain at the vendor. No SQL is
constructed in 3a — the snapshot fast path issues no query at all, and the
fall-through path is Phase 2's existing parameterised query, unmodified.
4. **CSP and MapLibre workers.** `worker-src 'self' blob:` is already in
   `CSPValue`, which was written in Phase 1 anticipating exactly this. If a
   MapLibre upgrade needs more, the CSP change is a reviewed diff, never an
   `'unsafe-*'` allowance.
