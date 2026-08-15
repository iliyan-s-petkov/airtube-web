# Self-hosted basemap: reconciling the configuration with the Phase 1 architecture

**Date:** 2026-08-15
**Status:** approved, ready for planning
**Supersedes:** the vendor-basemap assumption in `airbg.yaml` (`basemap.style_url`, `AIRBG_BASEMAP_KEY`)

## Why this exists

The Phase 1 design chose a self-hosted Protomaps PMTiles basemap, Bulgaria extract, served
over HTTP range requests: "one Go binary, one PostgreSQL instance, one `.pmtiles` file". That
choice also fixed a legacy violation — the old PHP application pulled tiles directly from
`tile.openstreetmap.org`, contrary to the OSM tile usage policy.

None of it was implemented. What shipped instead is a vendor-shaped configuration:

```yaml
basemap:
  style_url: "https://tiles.example.org/styles/basic/style.json?key={key}"
```

with `AIRBG_BASEMAP_KEY` substituted into `{key}`. Phase 3b faithfully moved that value into
`airbg.yaml`; it did not invent it, and nobody had reconciled it against the spec. The result
is a configuration layer that assumes a tile vendor and an API key the architecture does not
want.

Google is not a candidate and never was. The legacy application used Google for *geocoding*
(`geo2addr.class.php` reverse-geocoded Bulgarian sensors into street addresses, billed per
call), not for tiles; the rewrite replaced that entirely with PostGIS `ST_Covers` against
imported boundaries. Separately, Google's terms forbid using its tiles with a non-Google
renderer, which rules them out for both Leaflet and MapLibre.

### The live contradiction this also settles

`web/src/islands/map.js:26-28` documents and tests a blank-basemap fallback: an unset style URL
is not fatal, the map renders markers over a plain background, and local development needs no
vendor account. `internal/config/validate.go` now **rejects** an empty `style_url` at startup.
The fallback is live, tested, and unreachable through configuration. This design makes it
reachable again by giving it a meaning: no tiles configured means no basemap.

## Goals

1. A keyless, self-hosted basemap, with no third-party request from a visitor's browser.
2. `AIRBG_BASEMAP_KEY` deleted, leaving `AIRBG_DATABASE_URL` as the project's only secret.
3. Tile traffic that cannot consume the API's rate-limit budget, and cannot starve it.
4. No weakening of the anti-scraping posture, whose foundation is that the origin is
   unreachable except through Cloudflare.

## Non-goals

- Changing the ingress design for the application. Cloudflare continues to front the app.
- Automating tile generation in CI. The inputs are hundreds of megabytes and the cadence is a
  few times a year.
- Supporting a vendor basemap as an alternative code path. A second path is a second thing to
  keep correct, and Phase 3b spent a fix wave deleting configuration that nothing read.

## Architecture

### Three static artefacts

Generated offline, shipped as files, served as-is:

| Artefact | What it is |
|---|---|
| `bulgaria.pmtiles` | Protomaps basemap, Bulgaria extract, ~150–300 MB |
| `glyphs/{fontstack}/{range}.pbf` | Font atlases MapLibre needs to render labels |
| `style.json` | References the `pmtiles://` source, the glyphs, and the layer styling |

Self-hosting the glyphs is not optional polish: fetching them from a public endpoint would
reintroduce exactly the third-party request this design exists to remove.

### A third listener

`serve` today opens two listeners — public (middleware chain, pages, JSON API) and private
(`/metrics`, `/healthz`). This adds a third, for tiles only.

Separate listeners rather than a path prefix, for the same reason the private listener is
separate: a prefix is one routing mistake away from the wrong outcome. For `/metrics` that
outcome is exposing throttling counters to a scraper. For tiles it is either the middleware
chain rate-limiting tiles into uselessness, or an exemption that turns out to cover more than
intended.

The rate-limiting problem is concrete and is the reason a shared listener does not work. A
single map load issues dozens of HTTP range requests. Routed through the public chain they
would exhaust the 10/s, burst-60 bucket on the first pan. Raising the bucket to accommodate
them would raise it for the JSON API too — the precise thing the limit exists to prevent.

The tiles listener holds no database pool, no snapshot, no limiter, no admission semaphore and
no enumeration check. It cannot reach anything stateful. That is what makes it safe to expose
directly, and it is the same bulkhead argument as `db.OpenPair`: only separate resources can
bound one workload's effect on another's capacity.

### Deployment posture

This part is load-bearing, not advisory. Serving tiles from the origin means a hostname that
resolves to the origin IP, and the anti-scraping design depends on that IP being unknown:
`CF-Connecting-IP` is attacker-controlled on a direct connection, and every limiter keys off
it.

- The **application port accepts connections only from Cloudflare's published IP ranges**,
  enforced by a packet filter. `listen.trusted_proxy_cidrs` is not sufficient on its own — it
  governs header parsing, not who may connect.
- The **tiles port accepts the world**, on a DNS-only hostname.

With the filter in place, discovering the origin IP yields tiles and nothing else. Without it,
this design weakens the system, so the firewall rule ships as part of the deployment
documentation and is named in the operator checklist.

## Configuration

Delete `basemap.style_url` and `AIRBG_BASEMAP_KEY`. The reasoning matches the fix wave that
deleted `ratelimit.api.retry_after`: a key describing a vendor the architecture does not use is
worse than no key, because it reads as a supported option.

New section:

```yaml
tiles:
  addr: "127.0.0.1:8082"                  # third listener; empty disables tiles
  dir: "/var/lib/airbg/tiles"             # holds bulgaria.pmtiles, style.json, glyphs/
  public_url: "https://tiles.airbg.org"   # what the browser is told to fetch
```

`public_url` is the single home for that value. It produces both the style URL handed to the
map island and the origin the CSP must allow, rather than being written twice — the same
argument the config already makes for the national fallback view, where "two copies is how the
two views drift apart".

Concretely, the renderer's `BasemapStyleURL` — already rendered into `data-basemap` on both
`index.gohtml` and `area.gohtml` — becomes `tiles.public_url` + `/style.json`, and is the empty
string when `tiles.*` is unset. The templates and the island's `readConfig` need no change:
they already treat `data-basemap` as "a style URL, possibly empty".

### Two checked couplings

Both fail at startup, because both fail *silently* at runtime.

1. **`listen.csp`'s `connect-src` must contain `tiles.public_url`'s host.** MapLibre fetches
   the style, the glyphs and the `.pmtiles` ranges over `fetch`/XHR. A CSP that omits the host
   fails closed and the map is blank — indistinguishable from a tile-generation mistake.
2. **`tiles.addr`, `tiles.dir` and `tiles.public_url` are all-or-nothing.** Any one set
   requires all three.

### Empty means no basemap

Leaving every `tiles.*` key empty is legal and starts two listeners, exactly as today. The map
island renders markers over `empty_basemap_colour`. This restores the fallback that `map.js`
documents and tests, and keeps the property the old vendor-key comment was protecting: local
development needs no account, and now no 300 MB file either.

New values are pinned in `internal/config/inert_test.go` alongside the rest. These are new
rather than moved, so the pin records a decision rather than proving a non-change.

## Components

### `internal/tiles` (new)

```go
func NewHandler(dir string, allowOrigin string) (http.Handler, error)
```

Serves a fixed allowlist — `style.json`, `bulgaria.pmtiles`, `glyphs/{fontstack}/{range}.pbf` —
over `http.FS(os.DirFS(dir))`. Using `os.DirFS` closes path traversal structurally, rather than
by validating input. `GET` and `HEAD` only; anything else is 405. Range support, `ETag` and
`Content-Type` come from `http.ServeContent` beneath `http.FileServer`.

The constructor returns an error if `dir` lacks any expected file, so a mis-set path is a
startup failure rather than a production blank map.

Headers it must set that the application chain does not:

- **`Access-Control-Allow-Origin`**, set to the configured application origin — not `*`. Tiles
  are on a different host, so every fetch is cross-origin. With
  `Access-Control-Allow-Headers: Range` and `Access-Control-Expose-Headers: Content-Range`, so
  range requests survive preflight. Omitting these is the second way to produce a silently
  blank map.
- **`Cache-Control: public, max-age=31536000, immutable`** and `X-Content-Type-Options:
  nosniff`. The file is immutable by construction: regeneration changes the filename.

### Frontend

One new dependency, `pmtiles`, to register the protocol:

```js
import { Map as MapLibreMap, addProtocol } from 'maplibre-gl'
import { Protocol } from 'pmtiles'
addProtocol('pmtiles', new Protocol().tile)
```

`mapStyle(cfg)` keeps its present shape — configured style URL, else
`blankStyle(emptyBasemapColour)` — so the existing fallback logic and its tests survive
unchanged. That is the entire frontend change, and its additive shape is the signal that the
design fits the code that is already there.

The island also gains a `map.on('error')` handler that logs once and continues, so a style-load
failure cannot throw and take the sensor markers down with it.

**Deliberately not a dependency:** the Protomaps style theme. `style.json` is generated offline
during the tile build and shipped as a static file, so the runtime bundle carries no theming
package and the style can be edited without a rebuild.

### Data flow

Page → style URL from `tiles.public_url` → MapLibre fetches `style.json` → the PMTiles protocol
issues HTTP range requests against `bulgaria.pmtiles` → glyphs fetched per fontstack. Four
kinds of request, all to the tiles listener, none touching the application.

A visitor transfers only the ranges their viewport needs — a few megabytes per session — then
caches for a year.

## Failure modes

| Situation | Behaviour |
|---|---|
| `tiles.dir` missing or incomplete | Startup error |
| Partial `tiles.*` configuration | Startup error |
| `listen.csp` omits the tiles host | Startup error |
| `tiles.*` entirely empty | Two listeners, blank basemap, markers render |
| File unreadable at runtime (volume unmounted) | 404 → markers render over a blank background |

The last row is an existing Phase 1 promise: tiles unavailable must degrade, never fail the
page.

## Testing

`internal/tiles` is pure and container-free, so every test below is cheap and can be
mutation-proven — the standard this project holds tests to after Phase 3b shipped several that
passed while inert.

- Path traversal (`../../etc/passwd` and encoded variants) → 404. Proven by reverting to
  `http.Dir` and watching the test fail.
- `POST`/`PUT` → 405.
- Range request → 206, correct byte slice, correct `Content-Range`.
- CORS headers present and not `*`.
- Constructor errors when the directory is missing any expected file.
- **`style.json` 404s on the public listener.** This mirrors the existing `/metrics` separation
  test and is what would catch a later "simplification" of three listeners back into two.
- Configuration: each coupling rejected at startup; all-empty starts two listeners.

Frontend tests carry over unchanged, which is itself the check that the change is additive.

## Tile generation

A documented manual procedure in `docs/tiles.md`, with exact commands and pinned tool versions:
Geofabrik Bulgaria extract → `planetiler` → `bulgaria.pmtiles`; glyph PBFs from Noto Sans;
`style.json` from a pinned Protomaps theme with `name:bg` labels, so the basemap follows the
interface language. Output filenames carry a date suffix, which is what keeps the immutable
caching honest.

## Attribution

OpenStreetMap data is ODbL. The footer and the style's `attribution` field must credit
OpenStreetMap contributors and Protomaps. This is a licence obligation, not presentation.

## Open questions for deployment

- Where the tiles directory lives relative to the container: baked into the image (simple, but
  a ~300 MB image) or mounted as a volume (smaller image, one more thing to provision). The
  configuration supports either; the deployment phase decides.
- Whether `tiles.airbg.org` gets its own certificate or a wildcard.
