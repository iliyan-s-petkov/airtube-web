# Phase 3c — metric switcher, sensor panel, find-me: design

**Date:** 2026-08-16
**Precedes:** deployment (Phase 4)
**Follows:** Phase 3a (frontend), Phase 3b (configuration), the basemap/PMTiles branch

## Goal

Make the data the API already serves reachable from the map: switch between all
seven canonical metrics, open a per-sensor panel with its 24-hour history, and
open the map on the visitor's own area. Add the browser-level test tier the
project has never had, and put every existing test tier into CI.

No new API endpoint. No change to any rate-limit number. `www-root/` untouched.

## Why now

Three things the API already ships are inert:

- `/api/v1/locate` is fully built, documented and tested, and **nothing in the
  frontend references it**.
- `/api/v1/sensor/{id}/series` has no caller.
- `svelte` and `@sveltejs/vite-plugin-svelte` are installed and wired into
  `vite.config.js`; there is not one `.svelte` file in the repository.

Phase 3a's surfaces were a MapLibre map and a uPlot chart — both third-party
canvases that own their own rendering, so a component framework had nothing to
do. Phase 3c introduces the project's first UI of its own, which is what the
framework is for.

## Global constraints

Binding for every task in the resulting plan.

- **Two new devDependencies, and no others:** `jsdom` (component tests) and
  `@playwright/test` (Chromium only). No new runtime dependency. No new Go
  dependency. Every version pinned exactly, as `maplibre-gl`, `pmtiles` and
  `uplot` already are.
- **No defaults compiled in.** A missing config key is a startup error. The one
  new key (`frontend.unscaled_colour`) ships in `airbg.yaml`, is validated,
  appears in `validate-config`'s table, and is documented in
  `docs/configuration.md`.
- **No user-visible string literals in `web/src/`.** Every string reaches the
  frontend as a `data-t-*` attribute rendered by the server, per the pattern in
  `islands/chart.js`. `lib/literals.test.js` is extended to cover components.
- **No metric, colour, or threshold literal in JS.** The metric list comes from
  the server; band colours come from `/api/v1/scales`; paint values come from
  `data-*` attributes sourced from `frontend:` config.
- **Logic in plain JS, markup in components.** Anything with a decision in it
  lives in `web/src/lib/*.js` and is proven by pure unit tests. Components bind
  and render.
- **Every new test mutation-proven.** A test that passes with its subject
  deleted is not a test.
- No endpoint accepts a bounding box or an unbounded list parameter. Nothing in
  this phase changes that.
- `CLAUDE.md` is never staged. No Claude attribution in any commit or PR.

## Architecture

```
web/src/
  lib/                      pure, no DOM, no framework — where proofs live
    viewstate.js            parseHash / serialiseHash / metric fallback
    viewstate.svelte.js     $state store; thin runes wrapper over the above
    metrics.js              canonical list handling, hasScale(), units
    nearest.js              nearest area centroid to a coordinate
    api.js colour.js tier.js series.js        (existing, unchanged)
  components/               .svelte — markup and binding, no arithmetic
    SensorPanel.svelte
    MetricSwitcher.svelte
    Chart.svelte            wraps uPlot; replaces islands/chart.js
  islands/
    map.js                  MapLibre, imperative; subscribes to the store
    panel.js                mounts SensorPanel
    switcher.js             mounts MetricSwitcher
    chart.js                reduced to a mounter for Chart.svelte

```

**The two state modules are deliberately separate.** `viewstate.js` is
importable by Vitest with no jsdom and no Svelte compiler, so hash parsing and
metric fallback keep pure-function proofs. `viewstate.svelte.js` holds `$state`
and delegates every decision to the pure module.

**The map stays an island, not a component.** MapLibre owns its canvas and its
own event loop. It subscribes to the store and imperatively recolours markers.

**The chart's uPlot wiring moves into `components/Chart.svelte`**, so the panel
embeds a chart declaratively instead of hand-driving another island's
mount/destroy. `islands/chart.js` survives only as the four-line mounter the
`main.js` registry loads for `data-island="chart"`; the registry itself is
unchanged in shape and gains `panel` and `switcher` entries.

## The state model

**Grammar:** `#metric=<name>&sensor=<id>`, parsed with
`URLSearchParams(location.hash.slice(1))`. Both keys optional, order
irrelevant, unknown keys ignored — so a future `#period=` does not break old
links.

**Validation** (pure, in `lib/viewstate.js`):

- `metric` must be one of the seven canonical names. Anything else falls back to
  the server-supplied default read from `data-metric` (`series.default_metric`,
  `P2`). A *missing* `data-metric` attribute remains a loud failure, as
  `islands/map.js` already requires — the fallback covers a bad hash, never a
  bad server render.
- `sensor` must parse as a positive integer and must exist in the loaded sensor
  set. Otherwise the panel stays closed and the metric still applies: a bad
  sensor id must not take the rest of the hash down with it.

**Writing — two intents, two history behaviours:**

- **Metric switch → `replaceState`.** A view setting. Toggling metrics five
  times must not put five entries in the back stack.
- **Sensor open/close → `pushState`.** A destination. Back closes the panel,
  Back again leaves the page — which is what makes `#sensor=` behave like a link
  on mobile, where Back is the primary gesture.

Both write the whole hash, so the two keys can never disagree with what is
rendered.

**Clean URLs:** when the metric equals the default and no sensor is open, the
hash is removed entirely rather than written as `#metric=P2`.

**Reading back:** one `hashchange` listener in `viewstate.svelte.js` re-parses
and assigns to `$state`. Programmatic writes set a flag so the echo is ignored.
Consumers react to the store, never to the event — exactly one parse exists in
the system.

**Rate-limit facts this model respects** (`airbg.yaml`):

- `ratelimit.enumerate.sensors_per_window: 40` per hour, counted by *distinct*
  sensor. The panel therefore renders from data already in memory and treats the
  chart as an enhancement.
- `ratelimit.series: 1/s, burst 10`. Switching metric with a panel open refetches
  that sensor's series (same sensor, no new enumeration cost, cached per URL by
  `lib/api.js`); switching with the panel closed issues no request at all.

## The metric switcher

`MetricSwitcher.svelte`, mounted in its own island container beside the map.
The seven metrics arrive as a server-rendered `data-metrics` attribute sourced
from `upstream.CanonicalMetrics()` — the same list `/api/v1/meta` publishes, so
the frontend never carries its own copy. Labels and units arrive as `data-t-*`.

Clicking writes `metric` to the store; the store writes the hash; map, legend
and panel react.

### Three greys, three meanings

`/api/v1/scales` publishes bands for **P1 and P2 only**. Today `bandsFor()`
returns `[]` for any other metric and `colourFor()` then paints every marker
`frontend.no_data_colour` — the same grey used for a missing reading, and the
same grey shown when the scales endpoint itself fails. Switching to temperature
would produce a uniformly grey map indistinguishable from breakage.

| State | Scope | Rendering |
|---|---|---|
| No reading for this sensor/area | per marker | `frontend.no_data_colour` (unchanged) |
| Metric has no air-quality scale | whole map | `frontend.unscaled_colour` (new) + legend note |
| Scales failed to load | whole map | existing `showError` path (unchanged) |

`frontend.unscaled_colour` is a paint value handed to a GL layer, so it belongs
in `frontend:` by that block's own stated rule: if CSS can style it, it belongs
in `theme.css`; if JS passes it to a canvas or a GL layer, it belongs in config.

**Which metrics are scaled is derived, never hardcoded.** `hasScale(metric)` in
`lib/metrics.js` reads the `/api/v1/scales` response: a metric is scaled if and
only if the server publishes bands for it. Adding pressure bands server-side
would colour pressure with no frontend change.

**Legend:** for a scaled metric, the band table it shows today; for an unscaled
metric, a single swatch of `unscaled_colour` plus server-supplied text to the
effect of "no air-quality scale for this metric — values shown on click". The
legend is what stops a uniformly coloured map from reading as a bug.

**Cost:** switching metric repaints from the payload already loaded —
`/api/v1/area/{slug}/sensors` carries every metric's current value. No request,
no enumeration spend, unless a panel is open.

## The sensor panel and `#sensor=`

`SensorPanel.svelte`, mounted into a server-rendered container beside the map —
never replacing server-rendered content, per the islands rule. Desktop: side
panel. Mobile: bottom sheet. Not a MapLibre popup: a popup cannot hold a chart,
cannot be deep-linked, and moves with the map.

**Load order:**

1. **Immediately, from memory** — the columnar `/api/v1/area/{slug}/sensors`
   payload the map already holds: id, type, quality flag, and the current value
   for **all seven metrics**. This is where the five unscaled metrics become
   readable.
2. **Then, asynchronously** — `/api/v1/sensor/{id}/series` for the selected
   metric, rendered by `Chart.svelte`.

**The quality flag is shown, not hidden.** `internal/quality/flag.go` defines
five values. `ok` and `no_neighbours` display normally — `no_neighbours` means
the spatial check could not run and is explicitly not a failure. `out_of_range`,
`stuck` and `spatial_outlier` get a visible marker and server-supplied
explanatory text. The project's rule is "bad readings are flagged, never
discarded"; the panel is the first surface where a visitor can see why a marker
is greyed out.

**`#sensor=` is honoured only on `/area/{slug}`.** On the home page it is
ignored. There is no `/api/v1/sensor/{id}` metadata endpoint, so a sensor's
position, type and quality are knowable only from an area's sensor payload. A
deep link that rendered a chart but could not place the marker would be worse
than one that is not honoured, and building the endpoint is scope this phase
declines.

**Cold deep-link sequence** on `/area/sofia#sensor=1234`: load the area's
sensors → if `1234` is present, select it, centre at sensor tier, open the
panel, fetch its series → if absent (retired sensor, wrong area, junk id), leave
the panel closed and show a server-supplied "sensor not found in this area"
hint, with the rest of the hash still applied.

**Close** clears `sensor` from the hash via `pushState`.

**A failed series fetch is not a failed panel.** A 429 from either bucket leaves
everything from step 1 on screen and puts the server's `Retry-After` into a
message in the chart's place. `lib/api.js` retries a 429 once and gives up past
its 30-second cap, so a 900-second enumeration lockout surfaces as a message
rather than a hang.

## Opening view and find-me

**On load, home page only.** The map mounts immediately at the server-templated
national view (`frontend.default_lon/lat/zoom`) — never a blank canvas — then
fetches `/api/v1/locate` once:

- `source: "geoip"` → adopt the returned `slug`, `lon`, `lat`, `zoom`. Adopting
  the slug is the important half: `refresh()` refuses the sensor tier without a
  slug and "must not invent one", so the slug is what makes sensors near the
  visitor reachable without inventing one or accepting a bounding box.
- `source: "default"` → no camera move (the response *is* the current view) and a
  server-supplied hint that the whole country is shown. `locate.go` returns this
  identically for a visitor abroad, an undeterminable location, and any
  deployment without Cloudflare — which is the normal local-development path.

**No-flicker rule:** if the response arrives before the map's `load` event,
`jumpTo`; after it, `easeTo`. A camera that teleports after the visitor has
started looking is disorienting; an animation before first paint is wasted.

**On `/area/{slug}` nothing auto-locates.** The URL already names the area.

**The find-me button** requests precise coordinates from the browser, with a
timeout. On success the map centres there and the visitor's area is resolved
**client-side** by nearest area centroid from the overview payload already in
memory (`lib/nearest.js`, pure). The precise coordinates never leave the
browser: `/api/v1/locate` reads CDN headers only and has no coordinate
parameter. On denial, unsupported, or timeout: the view is left alone and a
server-supplied message explains why. Nothing is stored — no cookie, no
localStorage — matching `locate.go`'s "no IP stored, no cookie" posture.

**Cost:** one extra `/api/v1/locate` per home-page load. It is
`Cache-Control: private`, and when CDN headers are absent or the admission pool
is full it answers from the snapshot without touching the database.

## Testing

**Tier 1 — pure unit (Vitest, no jsdom).** `viewstate.js`, `metrics.js`,
`nearest.js`, plus the existing pure modules. Unknown metric falls back to the
server default; bad sensor id does not take the metric with it; the clean-URL
rule; nearest-centroid selection. `main.js`'s "pure-logic-only, no jsdom"
comment stays true of this tier.

**Tier 2 — component (Vitest + jsdom).** Mounts components with Svelte's own
`mount`; no testing-library. Asserts wiring, not arithmetic: clicking a metric
writes the store; `stuck` renders the warning while `no_neighbours` does not; a
429 leaves current values on screen and puts the message in the chart's place.

**Tier 3 — E2E (Playwright, real binary, real Postgres).** The seams no unit
test reaches: the server-rendered fallback surviving with JS disabled; deep-link
`#sensor=` restore; Back closing the panel; both auto-locate branches; find-me
with permission granted and denied.

**How the E2E stack starts.** A build-tagged Go command starts a
PostGIS+TimescaleDB container through the **existing `internal/testsupport`
helpers**, runs migrations, seeds a fixed fixture dataset, starts the real
server, prints its base URL, and waits for SIGTERM. Playwright's `webServer`
config runs it. One seeding path, shared with the Go integration suite — no
second copy of fixture data to drift.

With no CDN headers present, `/api/v1/locate` returns `source: "default"`
deterministically, so the opening view is stable in tests for free; the geoip
branch is exercised by injecting the headers. The precise-location path is
driven by Playwright's `grantPermissions` and `setGeolocation`.

## CI

Today's workflow runs Go unit tests only. Three jobs are added:

| Job | Runs |
|---|---|
| `web` | `npm ci`, `npm test`, `npm run build` (already includes `npm audit --audit-level=high`) |
| `integration` | `go vet -tags integration ./...`, `go test -tags integration ./...` |
| `e2e` | `npx playwright install --with-deps chromium`, then the Playwright suite |

The `integration` job closes a Phase 3b follow-up: `go vet ./...` does not
compile files behind `//go:build integration`, so `internal/server/e2e_test.go`
has never been vetted in CI.

## Out of scope

- **No new API endpoint.** `/api/v1/sensor/{id}` metadata stays unbuilt, and
  `#sensor=` is area-page-only because of it.
- **No scale switcher** between `eaqi` and `eu_limit`. `bandsFor()` continues to
  take the first scale matching the metric.
- **No rate-limit retuning.** The 40-distinct-sensors-per-hour and
  12-distinct-areas-per-hour budgets are unchanged here.
- **No `www-root/` changes.**

## Risks carried to the deployment phase

- `ratelimit.enumerate.sensors_per_window: 40` is reachable by an engaged
  visitor clicking around a city, and the failure is a 15-minute lockout on
  charts. This design degrades correctly — the panel and current values keep
  working, only history is refused — but the number may want revisiting. It
  joins the already-recorded 12-areas-per-hour risk.
- Auto-locate adds one `/api/v1/locate` request per home-page load. It is cheap
  and snapshot-served, but it is a per-visitor request on the busiest page.
