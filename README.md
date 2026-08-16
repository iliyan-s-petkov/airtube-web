# airbg.org

Air quality map for Bulgaria, using data from the sensor.community citizen
sensor network.

This repository is a rewrite of the legacy PHP application. See
`docs/superpowers/specs/2026-08-07-airbg-phase1-design.md` for the design and
`ANALYSIS.md` for the audit of the code it replaces.

## Running locally

`docker-compose.yml` is for local development only — it publishes Postgres on
the host and carries none of the hardening a production deployment needs. Do
not use it in production.

No credential is hardcoded in it: every value comes from the environment with a
development-only fallback, so `docker compose up` works with no setup. Copy
`.env.example` to `.env` to override any of them. `.env` is gitignored.

```bash
docker compose up -d db
export AIRBG_DATABASE_URL='postgres://airbg:airbg@localhost:5432/airbg?sslmode=disable'
export AIRBG_CONFIG="$PWD/airbg.yaml"
go run ./cmd/airbg migrate
go run ./cmd/airbg import-areas data/boundaries/bulgaria.geojson country
go run ./cmd/airbg collect
```

`AIRBG_CONFIG` must name the path to `airbg.yaml`; there is no fallback path.
See `docs/configuration.md` for the full configuration reference.

The `import-areas` step is not optional — see the prerequisite note below.

## Subcommands

| Command | Purpose |
|---|---|
| `migrate` | Apply schema migrations |
| `collect` | Poll sensor.community on a loop, score, and store |
| `serve` | Run the poller and the HTTP server in one process (see Serving below) |
| `import-areas <file.geojson> <city\|oblast\|neighbourhood\|country>` | Load boundaries and assign sensors |
| `backfill <sensor_id> <archive-csv-path>` | Load a sensor.community archive CSV into `reading_hourly`; refuses unless the sensor is known and inside the `country` boundary |
| `purge-outside-boundary` | Delete sensors (and their stored readings) outside the `country` boundary, plus readings orphaned from any sensor row; refuses to run if no `country` boundary is imported |

**Importing a `country`-kind boundary is a hard prerequisite for ingesting anything.**
`collect` filters every incoming sensor against the `area.kind = 'country'`
boundary instead of trusting upstream's self-reported `country`
field, which is unreliable (see Known limitations). Until `import-areas
<file.geojson> country` has been run at least once, `collect` fails closed:
it polls upstream successfully but stores zero rows, every cycle, logging an
ERROR that names the exact remedy command. Nothing else in the system's
normal signals (the rollup backlog stays at 0, there are no other errors)
will look unusual, so this is easy to miss if the import step is skipped.

`import-areas` rejects a file outright — importing nothing at all — if any
feature's geometry fails `ST_IsValid` or is empty. `"coordinates": []` is the
case worth knowing about: it produces `MULTIPOLYGON EMPTY`, which is not NULL,
so it would insert happily and then match no point on earth. As a `country`
boundary that means `collect` reports the boundary present and still stores
nothing, cycle after cycle. Invalid geometry is not repaired with
`ST_MakeValid`, because a silently repaired national outline is a polygon you
never supplied and cannot inspect; fix the source file instead.

`backfill` applies the same value ranges as live ingest and drops non-finite
values (`nan`, `inf`) and out-of-range sentinels such as `-999` before
bucketing, logging a count of what it dropped at WARN — or ERROR if half or
more of the file was rejected. Nothing ever rewrites a historical bucket and
`reading_hourly` is retained for two years, so a single poisoned cell would
otherwise be permanent.

`purge-outside-boundary` also deletes readings whose `sensor_id` has no `sensor`
row. `reading` is a hypertable with no foreign key to `sensor`, so such rows are
possible; they are reported separately from foreign sensors, because they mean
something different (readings written for a sensor that was never ingested).

## Configuration

Configuration comes from `airbg.yaml` (committed, no secrets) plus `AIRBG_*`
environment variables that override individual keys and the two secrets that
are environment-only. There are no defaults compiled into the binary — a
missing key is a startup error.

See **[`docs/configuration.md`](docs/configuration.md)** for the full
reference: the two layers, the `AIRBG_CONFIG` path variable, the mechanical
override rule, the renamed variables (a breaking change from earlier
versions), `airbg validate-config`, the two colour homes, and the checked
cross-field couplings.

The one secret, environment-only and never written to `airbg.yaml`:

| Variable | Holds |
|---|---|
| `AIRBG_DATABASE_URL` | PostgreSQL connection string |

The basemap is self-hosted (no vendor, no key) — see the `tiles.*` keys below
and **[`docs/tiles.md`](docs/tiles.md)** for how the artefacts are generated.

## Serving

`airbg serve` runs the poller and the HTTP server **in one process**, not two.
The published snapshot (`internal/snapshot`) lives in that process's memory —
every API and page response is served from it directly, with no per-request
database query — so the poller that rebuilds the snapshot and the server that
answers requests from it have to share an address space. Splitting them into
separate processes would mean shipping the snapshot over the network on every
rebuild for no benefit.

`serve` opens **two listeners always, and a third when the basemap is
configured**:

- The **public listener** (`listen.addr`) carries the middleware chain —
  rate limiting, the enumeration-breadth check, security headers — and the
  actual pages and JSON API.
- The **private listener** (`listen.metrics_addr`) carries only `/metrics` and
  `/healthz`.
- The **tiles listener** (`tiles.addr`), only when `tiles.*` is configured,
  serves the self-hosted basemap artefacts. It holds no database pool, no
  snapshot, no rate limiter and no admission semaphore — that bulkhead is what
  makes it safe to expose directly, on a DNS-only hostname, while the public
  listener accepts connections only from Cloudflare's published ranges. See
  **[`docs/tiles.md`](docs/tiles.md)**.

They are separate listeners rather than a path prefix on one mux, because a
prefix is one routing mistake away from exposing the counters that tell a
scraper whether it is being throttled. `/metrics` reports request volumes,
enumeration trips, and internal error rates — exactly the reconnaissance the
anti-extraction design exists to deny — so it must never be reachable from the
public listener.

`serve` also opens **two connection pools**, not one. This is a bulkhead, and
the failure it prevents needs no traffic and no attacker: `area.AssignSensors`
runs under a 60s statement timeout on every poll cycle, so the collector may
legitimately hold a connection for a minute. While both workloads shared one
pool of `max(4, numCPU)` connections, request handlers blocked inside
`pgxpool.Acquire` behind the poll cycle **on a schedule** — and every control in
place saw a healthy system, because it was one. Rate limiting bounds one client
and admission control bounds the crowd; neither can bound one workload's effect
on another's capacity. Only separate pools can (`db.OpenPair`).

The sizes are stated numbers rather than pgxpool's `max(4, numCPU)` default, so
deployed capacity is a decision and not a side effect of the container's core
allocation. Their **sum** is what Postgres sees from one instance, so raising
either is a decision about the database's `max_connections`. Zero and negative
values are rejected at startup: pgxpool reads `MaxConns <= 0` as "use the
default", so a `0` waved through would look like an explicit choice and silently
become the host's core count instead.

These `airbg.yaml` keys (or their `AIRBG_*` environment override — see
`docs/configuration.md` for the naming rule and the table of names renamed by
Phase 3b) configure serving:

| Key | Shipped value | Notes |
|---|---|---|
| `listen.addr` | `127.0.0.1:8080` | Public HTTP listener. Keep this on loopback and reach it through a Cloudflare tunnel — binding `0.0.0.0` exposes the origin directly, and a client that reaches the origin directly is covered by no Cloudflare protection, only by the in-process token buckets. |
| `listen.metrics_addr` | `127.0.0.1:9090` | Private listener for `/metrics` and `/healthz`. Never route this publicly. |
| `listen.trusted_proxy_cidrs` | *(empty)* | Peer ranges whose `CF-Connecting-IP` header is believed. **Empty means trust nobody** — the correct value for local development and for any origin not behind Cloudflare. Setting this while the origin is also directly reachable lets anyone who can reach it spoof their client IP and bypass every rate limit; restrict the origin first, then set this. |
| `listen.base_url` | `http://localhost:8080` | Public origin, used for canonical and hreflang links. Must be absolute. |
| `database.api_conns` | `8` | Connections available to request handlers. This is the API's real concurrency ceiling for anything that touches the database. |
| `database.collector_conns` | `4` | Connections available to the poller and the snapshot publisher — the side of the bulkhead allowed to be slow. |
| `database.max_inflight` | `16` | Caps how many requests may be inside a database query at once, **across every client** — a rate limiter only bounds one client, so a crowd of individually well-behaved clients could otherwise collectively queue more concurrent work than the pool can serve, piling up inside `pgxpool.Acquire` until `WriteTimeout` fires. Refusals on the two series routes answer `503` with `Retry-After: 2`, never `429` — the caller did nothing wrong, so it must not be told to back off as if it had. `/locate` instead degrades: it skips the lookup and returns the national default view with `200`, exactly as it does when the lookup fails, because a request that has a usable answer without any query must not be made less available by a capacity control. Either way the refusal is counted in `airbg_admission_rejected_total`. |
| `listen.max_conns` | `4096` | Caps how many connections **each internet-facing listener** — public, and tiles when `tiles.*` is set — holds open at once. This bounds sockets, not requests: nothing else in the process stops tens of thousands of mostly-idle connections from exhausting file descriptors and goroutines before a single request completes, so no rate limiter or admission cap ever sees them. Over-cap connections are accepted and closed immediately, never queued. Deliberately overlaps with Cloudflare's own protection — the origin being reachable only through Cloudflare is an unverified assumption, and a control that only works when that assumption holds is not a control. On the tiles listener that assumption is not merely unverified but **false by design**, and file descriptors are process-wide, so leaving that socket uncapped would let it exhaust them and take the public listener's `Accept` with it. One key for both: an int cannot ship "empty" alongside its all-or-nothing `tiles.*` neighbours, and a `tiles.max_conns` shipped as `0` would mean no cap at all. The private listener is never capped: a flood that also blinded `/metrics` would remove the one instrument an operator needs during the flood. |
| `tiles.addr` | *(empty)* | The tiles listener's bind address, e.g. `127.0.0.1:8082`. Must differ from `listen.addr` and `listen.metrics_addr`. |
| `tiles.dir` | *(empty)* | Directory holding the self-hosted basemap artefacts: `style.json`, the PMTiles archive named by `tiles.archive`, and `glyphs/`. The handler refuses to start if any is missing, so a mis-set path is a startup failure, not a blank map in production. |
| `tiles.public_url` | *(empty)* | The origin the browser fetches tiles from, e.g. `https://tiles.airbg.org`. An origin, not a URL prefix: a path, query or fragment is rejected at startup. Its host must also appear in `listen.csp`'s `connect-src`, checked at load time — see `docs/configuration.md`. |
| `tiles.archive` | *(empty)* | The PMTiles filename inside `tiles.dir`, e.g. `bulgaria-20260815.pmtiles`, and the only archive name the handler will serve. It must match the name written into `style.json`'s `pmtiles://` source. Dated because tile responses carry `Cache-Control: immutable` for a year: regeneration must change the filename, or returning visitors keep the old basemap. Must be a plain filename — no path separator. |

`tiles.*` ships **empty**: no vendor, no key, and this is a supported
configuration — two listeners, a map that still renders sensor markers over
`frontend.empty_basemap_colour`. Setting all four (never fewer) starts the
third listener and serves the basemap generated per **[`docs/tiles.md`](docs/tiles.md)**.
Self-hosting tiles from the origin only stays safe because the public listener
is firewalled to Cloudflare's ranges — see `docs/tiles.md`'s firewall-rule
section.

Endpoints:

| Method | Path | Listener |
|---|---|---|
| GET | `/`, `/en/`, `/areas`, `/en/areas` | public |
| GET | `/area/{slug}`, `/en/area/{slug}` | public |
| GET | `/api/v1/overview` | public |
| GET | `/api/v1/areas` | public |
| GET | `/api/v1/meta` | public |
| GET | `/api/v1/scales` | public |
| GET | `/api/v1/area/{slug}/sensors` | public |
| GET | `/api/v1/area/{slug}/series` | public |
| GET | `/api/v1/sensor/{id}/series` | public |
| GET | `/api/v1/locate` | public |
| GET | `/metrics` | private only |
| GET | `/healthz` | private only |

### Why there is no bounding-box endpoint

No endpoint accepts a bounding box or a coordinate window. The API is tiered
instead: a country-level overview, a city-level overview, and per-area detail
that must be requested one named area at a time. This is the anti-extraction
design — a bbox parameter would let one request return the whole country at
full resolution, and no rate limit can distinguish that request from a
legitimate one. Bulk extraction therefore requires enumerating areas, which is
what the breadth counters detect: they count *distinct* areas and sensors per
client, not request volume, so a reader refreshing one city forever is never
throttled while a crawler walking every area trips within a dozen requests.

If a future change adds a bbox parameter, this entire defence is gone. The test
`TestOverviewTakesNoBoundingBox` exists to make that change fail loudly.

### Why per-entity responses are not edge-cacheable

`Cache-Control` visibility is part of that defence, not a performance setting.
Responses keyed by a **slug or a sensor ID** — `/api/v1/area/{slug}/sensors` and
both `/series` endpoints — are sent `private, max-age=…`, so only the requesting
client's own browser may store them. The aggregate responses that every visitor
asks for identically — `/api/v1/overview`, `/api/v1/areas`, `/api/v1/meta`,
`/api/v1/scales` — are `public`, and edge-caching those is doing real
denial-of-service work.

The reason for the split: the breadth counter only sees requests that reach the
origin. If a per-entity response were `public`, a shared or edge cache would
serve a warmed slug without `ObserveArea` ever being called — so a scraper's
distinct-slug count would not grow for warm slugs, and a client that had
*already* tripped the limit and was being answered 429 by the origin could still
read every warm area straight out of the edge. `private` guarantees that a
request for a *different* entity always reaches the origin and is counted, while
still letting a normal reader's repeat views come from their own browser cache.

The same reasoning is why `max-age` on the `/series` endpoints scales with the
requested period (150 s for `24h` up to 3 h for `1y`), and why those two routes
carry a **second, tighter token bucket** (1 rps, burst 10) on top of the global
one. They are the only endpoints that reach PostgreSQL, and the breadth counter
cannot bound them: it counts *distinct* slugs and sensor IDs, so replaying one
`?period=1y` request costs it nothing. Both values come from `ratelimit.series`
in `airbg.yaml`. Refusals by that bucket are
counted separately as `airbg_series_rate_limited_total`, labelled by dimension
(`sensor`/`area`) — the global `airbg_http_rate_limited_total` cannot show them,
because it is incremented outside the mux by a different bucket.

Rendered error pages (`404`, `503`) are `no-store`, decided from the response
status inside `render` rather than set by the caller. A cached `503` is the one
that hurts: a transient no-snapshot window would otherwise be pinned at the edge
and served to every visitor for 150 s after the origin recovered.

Raising these to `public`, or adding a Cloudflare Cache Rule that caches
`/api/v1/area/*/sensors` or `/api/v1/sensor/*`, silently reopens that hole. Cache
hit rate on those paths is not a metric to optimise.
`TestOverviewIsPubliclyCacheableAndPerEntityIsNot` pins the distinction.

## Database

PostgreSQL 18 with **both PostGIS and TimescaleDB** is required. Use the
`timescale/timescaledb-ha:pg18` image, as in `docker-compose.yml` — the plain
`timescaledb` image does not include PostGIS, and the app will fail to start
against it (area boundary storage and sensor assignment depend on PostGIS
geometry types).

`reading` (raw readings) has a 30-day retention policy. `reading_hourly` (the
hourly rollup) retains 2 years and is a plain hypertable, deliberately not a
continuous aggregate — the rollup is written by the ingest daemon itself, not
computed by TimescaleDB.

## Tests

```bash
go test ./... -race
```

Integration tests start real PostgreSQL containers via testcontainers, so Docker
must be running. The first run pulls `timescale/timescaledb-ha:pg18`.

To check the live upstream contract:

```bash
AIRBG_LIVE_TEST=1 go test ./internal/ingest/ -run TestUpstreamContractLive
```

This test makes a real network call to data.sensor.community and is skipped
unless `AIRBG_LIVE_TEST=1` is set.

## Container image

```bash
docker build -t airbg .
```

Produces a distroless, non-root image with a single static binary as its
entrypoint (default command `serve`; run the collector separately with
`docker run ... airbg collect`). No shell, no package manager.

The build is multi-stage: a `node:26-alpine` stage runs `npm ci
--ignore-scripts` and `npm run build` inside `web/` to produce the Vite
bundle, a `golang:1.26` stage embeds that bundle and compiles the binary, and
the final stage is `gcr.io/distroless/static-debian13:nonroot` with nothing
but the binary in it. Nothing from `node_modules` or the Node toolchain
reaches the runtime image.

For local development, build the frontend once and then run the server
directly:

```bash
cd web && npm ci --ignore-scripts && npm run build
cd ..
go run ./cmd/airbg serve
```

Skipping the `npm run build` step is a supported, if degraded, mode: the
server starts fine and serves the same pages without the map or chart
islands (no JavaScript, no CSS from the bundle), and logs one line at
startup — `assets state="no manifest — serving without islands (run 'npm
run build' in web/)"` — so the gap is discoverable rather than silent.

## Known limitations

The three limitations previously listed here — no rollup watermark, untrusted
upstream `country`, and an 800 hPa pressure floor — have all been fixed. The
rollup now advances a transactional watermark and drains its backlog, alerting
at ERROR long before the 30-day raw retention could delete unaggregated rows;
sensors are filtered by `ST_Covers` against an imported national boundary
rather than by the self-declared `country` field; and the pressure floor is
650 hPa (~3600 m), above any Bulgarian sensor site.

What remains, that an operator should be aware of:

- **The national boundary must be imported before the first `collect`.** An
  authoritative outline now ships at `data/boundaries/bulgaria.geojson`
  (Natural Earth 1:10m, public domain), so this is one command rather than a
  sourcing exercise — but it is still a manual step, and skipping it means the
  collector stores nothing while otherwise looking healthy. Do not substitute
  the fixture under `internal/area/testdata/`: it is a crude hand-authored
  polygon, wrong along the eastern border, and exists only for tests.
- **Sensors ingested before the boundary filter existed are not removed
  automatically.** Rows stored while upstream `country` was still trusted
  persist, including foreign sensors. Run `purge-outside-boundary` once after
  importing the national boundary to delete them. It is deliberately never
  automatic — it deletes stored data, so it is an explicit operator action,
  and it refuses to run when no national boundary is present.
- **The container test suite can flake under load.** A single transient
  failure in `internal/store` has been observed during a full `-race` run,
  self-resolving on rerun. Suspected testcontainers resource contention
  rather than a code defect; worth watching if it recurs in CI.
- The origin must be unreachable except through Cloudflare. `listen.trusted_proxy_cidrs`
  makes the origin believe `CF-Connecting-IP` from those ranges; it cannot stop a
  client that reaches the origin some other way from being rate-limited as itself.
  A directly reachable origin with no network restriction means one attacker with
  many source addresses bypasses the per-client limits entirely. Restrict at the
  network layer (tunnel, firewall, or origin-pull authentication) — the header
  trust setting is not a substitute.

## Data and attribution

- Sensor data: [sensor.community](https://sensor.community/)
- Boundaries: © OpenStreetMap contributors, ODbL
