# airbg.org — Phase 1 design

Date: 2026-08-07
Status: approved
Scope: Phase 1 only. Later phases listed in "Roadmap" for context; they are not part of this spec.

## 1. Purpose

Replace the legacy "Dusty Map" PHP application with a modern, self-hosted air quality map for Bulgaria, published at `airbg.org`. Data comes from the sensor.community citizen sensor network. The site must be free to run at community scale, resistant to denial-of-service and scraping, and extensible into the roadmap features without rearchitecting.

The legacy application is documented in `ANALYSIS.md`. It is not migrated; it is replaced. Its data is not migrated either — the same readings are available from the public sensor.community archive.

## 2. Roadmap and scope boundary

Phase 1 (this spec) delivers the map, sensor charts, area summary pages, quality scoring, i18n, and the security baseline.

Later phases, each getting its own spec, plan, and implementation cycle:

- **Phase 2** — weather forecast overlay, wind, UV; POI proximity layers (playgrounds, parks, stadiums, schools) from OpenStreetMap; humidity correction of PM readings.
- **Phase 3** — official national station data (ExEA / NIMH) as a second source with explicit provenance labelling; threshold notifications.
- **Phase 4** — pollution source attribution and dispersion analysis.

Phase 1 includes a layer registry and a per-reading quality flag specifically because retrofitting either later would be a rewrite. Everything else roadmap-related is deliberately excluded.

## 3. Decisions

| Decision | Choice |
|---|---|
| Backend | Go, single static binary |
| Frontend | Svelte 5 islands on Go-rendered HTML |
| Map library | MapLibre GL JS (vector tiles) |
| Charts | uPlot |
| Datastore | PostgreSQL 16 + PostGIS + TimescaleDB |
| Basemap | Self-hosted Protomaps PMTiles, Bulgaria extract |
| Hosting | Self-hosted Docker (Komodo / Compose), SigNoz observability |
| Edge | Cloudflare free tier, treated as removable |
| Data scope | Bulgaria only |
| Metrics | P1, P2, temperature, humidity, pressure, noise |
| Retention | Raw 30 days; hourly rollups 2 years |
| Backfill | One year of hourly data from the sensor.community archive |
| Bad readings | Shown greyed and marked, excluded from aggregates |
| Colour scale | EAQI default; EU limit values and WHO guidelines switchable |
| Area tiers | Cities, 28 oblasti, Sofia neighbourhoods |
| API | Browser endpoints open but constant-cost; separate key-gated partner API |
| Auth | None for visitors. No accounts, no cookies. |

## 4. System shape

Three deployable artefacts: one Go binary, one PostgreSQL instance, one `.pmtiles` file.

### 4.1 The `airbg` binary

Single container: distroless base, non-root user, read-only root filesystem, no shell. Four internal packages, each with one responsibility:

| Unit | Responsibility | Depends on |
|---|---|---|
| `ingest` | Poll `data.sensor.community/airrohr/v1/filter/country=BG` every 5 minutes, normalise to canonical readings, write raw rows | upstream API, Postgres |
| `quality` | Score each reading (range, stuck, spatial outlier); write a flag, never delete | Postgres |
| `snapshot` | After each cycle, build the tiered current-state payloads, hold in memory, pre-gzip | `quality` output |
| `api` | HTTP handlers and middleware | `snapshot`, Postgres |

A `backfill` subcommand in the same binary imports historical hourly data from the sensor.community archive on demand.

`ingest` and `api` share a process because they share the snapshot. Splitting them would require a message bus or shared cache purely to move ~40 KB between two processes on the same host. At Bulgaria's scale — roughly 900 sensors, one poll per five minutes — neither component ever needs independent scaling.

### 4.2 Datastore

One PostgreSQL instance with PostGIS and TimescaleDB replaces the legacy MongoDB plus InfluxDB pair. PostGIS answers spatial questions; TimescaleDB answers time-series questions; both operate on the same rows. The legacy geohash join key, which existed only because the two stores could not see each other, is eliminated.

### 4.3 Basemap

`bulgaria.pmtiles` served over HTTP range requests. No tile server process, no render pipeline. Served from a hostname that is not proxied through Cloudflare (DNS-only) or from object storage, because Cloudflare's free tier restricts serving large volumes of non-HTML content. The file is portable, so this remains a configuration choice.

This also resolves a legacy violation: the old application pulled tiles directly from `tile.openstreetmap.org`, contrary to the OSM tile usage policy.

### 4.4 Edge

Cloudflare free tier proxies `airbg.org`, providing volumetric DDoS absorption, TLS termination, a managed WAF ruleset, and caching.

The edge is an accelerator, never a component. Constraints that keep it swappable:

1. No Cloudflare-only features. No Workers, no KV, no Access, no logic in Page Rules. Caching, rate limiting, and security headers are all implemented in the Go binary as well.
2. `CF-Connecting-IP` is trusted **only** when the connection's source address falls inside Cloudflare's published IP ranges. Otherwise the socket address is used. Unconditional trust of a forwarded-IP header allows trivial rate-limiter bypass.
3. The origin firewall permits only Cloudflare ranges, with the un-proxied path documented so DNS can be moved in one step.
4. `Cache-Control` and `ETag` are emitted by the application, so any CDN behaves correctly.

## 5. Data model

Coordinates are stored as `geography(Point, 4326)`, which is **(longitude, latitude)** — the inverse of the legacy `[lat, long]` convention. Reversing this silently relocates every Bulgarian sensor to the Indian Ocean; see the mandatory test in §11.

### 5.1 `sensor`

One row per upstream `sensor_id`.

- `sensor_id` — upstream identifier, primary key
- `sensor_type` — `SDS011`, `BME280`, `laerm`, …
- `location` — `geography(Point, 4326)`
- `first_seen`, `last_seen`, `active`

One physical device exposes several `sensor_id`s of different types at the same location. This is normal and must not be collapsed. The legacy application hid this by joining everything on a one-metre-precision geohash, which is why it could not attribute a reading to a device.

### 5.2 `reading`

TimescaleDB hypertable. The only high-volume table.

`(time, sensor_id, metric, value, quality)`

Long format — one row per metric, not one column per metric. The metric set spans disjoint device types, so a wide table would be mostly NULL. Long format also means Phase 2 metrics (UV, wind) are inserted rows rather than an `ALTER TABLE` against a hypertable with hundreds of millions of rows. Timescale's columnar compression makes the repeated `metric` and `sensor_id` values close to free.

`quality` is an enum: `ok`, `out_of_range`, `stuck`, `spatial_outlier`, `no_neighbours`.

Bad readings are flagged, never deleted. Flagging keeps them displayable (per the greyed-out decision), and allows the detector to be improved and history re-scored later.

Retention: raw rows dropped at 30 days.

### 5.3 `reading_hourly`

TimescaleDB continuous aggregate over `reading`: hourly bucket, `sensor_id`, `metric`, `avg`, `min`, `max`, `count`.

Computed over `quality = 'ok'` rows only. Flagged data is therefore structurally incapable of contaminating any published average.

Backfill writes here directly. Retention: 2 years.

### 5.4 `area`

- `slug`, `kind` (`city` | `oblast` | `neighbourhood`), `name_bg`, `name_en`, `geom` (polygon)

All three tiers share one table because all three are the same operation: point-in-polygon, then aggregate. Adding a tier is inserting rows.

Boundaries from OpenStreetMap (admin level 4 for oblasti, 8 for cities, 9/10 for Sofia districts). ODbL, so attribution appears in the footer alongside sensor.community attribution.

### 5.5 `area_sensor`

Materialised mapping from sensor to containing areas, computed when a sensor first appears. Sensors do not move, so this is never computed per request.

### 5.6 `api_key`

- `id`, `label`, `key_hash` (Argon2id — the key itself is never stored), `rate_limit`, `quota`, `created_at`, `revoked_at`

### 5.7 Coverage threshold

An area publishes aggregate numbers only when at least 3 sensors with `quality = 'ok'` readings fall inside it. Below that, the page renders its map and sensor list but displays "insufficient coverage for a reliable average" instead of a value. Without this, deeper tiers manufacture confident-looking averages from single sensors.

## 6. Quality scoring

Runs inside the ingest cycle, before the snapshot is built. Three checks in order; first failure wins.

### 6.1 Range

Physical plausibility per metric:

| Metric | Valid range |
|---|---|
| P1, P2 | 0–1000 µg/m³ |
| temperature | −40…+60 °C |
| humidity | 0–100 % |
| pressure | 800–1100 hPa |
| noise | 25–120 dB(A) |

Failure → `out_of_range`. The SDS011 saturates near 999 µg/m³, so readings at the ceiling are meaningless rather than extreme.

### 6.2 Stuck

Bit-identical value for 12 or more consecutive cycles (about one hour) → `stuck`. Real sensors jitter; frozen ones repeat exactly.

Exemptions, which occur legitimately: humidity at exactly 0 or 100, PM at exactly 0.

### 6.3 Spatial outlier

Compare against neighbouring sensors within 15 km. Fewer than 3 neighbours → `no_neighbours`, which is **not** a failure: the reading displays normally and is included in aggregates. It records only that the check could not run.

With enough neighbours, compute the median and the median absolute deviation (MAD). Flag when the deviation exceeds `3.5 × 1.4826 × MAD`, subject to a per-metric floor so that an unusually tight neighbourhood does not flag normal variation.

Median and MAD are used rather than mean and standard deviation because both mean and standard deviation are dragged by the outlier being hunted — a single sensor stuck at 900 inflates the standard deviation enough to make itself appear normal. Median and MAD tolerate up to half the sample being invalid, which matters when several sensors on one street fail identically.

**The check is applied per metric, and this asymmetry is essential:**

- **Temperature, humidity, pressure** are smooth fields. Neighbouring points genuinely are similar, so a 22 °C reading among −10 °C neighbours is unambiguously faulty hardware. Floors: 1.5 °C, 8 %, 3 hPa.
- **PM is not a smooth field.** It is dominated by point sources; in Bulgarian winter, domestic solid-fuel heating is the dominant source, so genuine extreme local readings are the signal the site exists to report. Applying a symmetric outlier test to PM would systematically delete real pollution, worst during the worst episodes. PM therefore gets only a blunt guard: flag when a reading exceeds **both** 5× the neighbour median **and** 150 µg/m³ absolute. This catches a sensor stuck at 900 on a street reading 30; it does not touch a genuine 200 µg/m³ inversion event where neighbours are also elevated.

### 6.4 Properties

Scoring is a pure function of `(reading, neighbours, recent history)` — directly unit-testable and re-runnable over stored history when thresholds change.

**Deferred to Phase 2:** humidity correction of PM. Optical sensors over-read above roughly 70 % relative humidity because water droplets scatter light like particles. The artefact is real, but correcting it means selecting a correction model and publishing numbers that differ from sensor.community's own. That warrants its own decision. Phase 1 stores humidity so the correction remains possible.

## 7. API

### 7.1 Tiering

Data is served in three precomputed tiers, all built in the same ingest cycle and held in memory:

| Zoom | Endpoint | Payload |
|---|---|---|
| Country (z < 9) | `GET /api/v1/overview` | 28 oblasti; one aggregate value and coverage state each. ~4 KB. No sensor coordinates. |
| Regional (z 9–11) | `GET /api/v1/overview?tier=city` | Cities and Sofia districts, same shape. ~15 KB. |
| Local (z ≥ 11) | `GET /api/v1/area/{slug}/sensors` | Individual sensors for one area only. |

Tiering serves two purposes.

**Cartography:** 900 markers at country zoom overlap into an unreadable mass. A choropleth answers the question people actually have at that scale — where is it bad right now — which the legacy application could not answer at all.

**Cost control:** no single request returns the dataset. Full extraction requires enumerating areas, which is slow, visibly patterned in logs, and rate-limitable.

This does **not** make the data confidential, and the spec does not claim otherwise. sensor.community already publishes every sensor's coordinates and readings, plus daily archive CSVs; anyone wanting the dataset obtains it more easily from upstream. Tiering protects the server's cost profile, not the data.

### 7.2 Browser endpoints

Unauthenticated, because any endpoint the browser calls is by construction callable by anyone. Embedding a credential in frontend JavaScript would be security theatre.

| Endpoint | Returns |
|---|---|
| `GET /api/v1/overview[?tier=]` | Area choropleth data |
| `GET /api/v1/area/{slug}/sensors` | Sensors within one area |
| `GET /api/v1/sensor/{id}/series?period=24h\|7d\|30d\|1y` | Chart data; ≤30 d from `reading`, beyond from `reading_hourly` |
| `GET /api/v1/area/{slug}?period=` | Area summary |
| `GET /api/v1/areas` | Area list for navigation and sitemap |
| `GET /api/v1/meta` | Scale tables (EAQI, EU, WHO), metric definitions, build version |
| `GET /api/v1/locate` | Coarse visitor area; `Cache-Control: private, no-store` |

All except `/locate` are served from memory or from narrow indexed queries, carry `ETag` and `Cache-Control`, and take no parameter that lets a caller increase server work.

The absence of a bounding-box parameter is deliberate. `/overview` has exactly one response, so it is a single globally shared cache entry that the edge serves without consulting the origin. The legacy `location.php?bounds=` is the opposite: every distinct viewport produced an uncached, unbounded InfluxQL query.

### 7.3 Payload format

Columnar rather than row-per-sensor:

```json
{
  "generated_at": "2026-08-07T12:05:00Z",
  "sensors": {
    "id":      [12345, 12346],
    "type":    ["SDS011", "BME280"],
    "lon":     [23.3219, 23.3241],
    "lat":     [42.6977, 42.6981],
    "area":    ["sofia-lozenets", "sofia-lozenets"],
    "quality": [0, 0],
    "P1":      [24.3, null],
    "P2":      [16.1, null]
  }
}
```

Row-per-sensor repeats every key name once per sensor. Columnar names each field once — roughly 40 % smaller before compression, and gzip compresses it better because same-typed values are adjacent. It also matches the typed arrays MapLibre consumes.

### 7.4 Partner API

Separate path prefix `/api/partner/v1/`. Genuinely private: no published documentation, no signup, keys issued individually.

`GET /api/partner/v1/readings` with bounded `from`/`to`, sensor and metric filters, cursor pagination, and a hard row cap per request. This is where expensive and unbounded queries live.

`Authorization: Bearer <key>`; lookup by Argon2id hash; per-key rate limit, quota, and access log; revocable.

### 7.5 Middleware

Applied to every request: strict Content-Security-Policy (no inline script, no `unsafe-eval`), HSTS, `X-Content-Type-Options: nosniff`, `frame-ancestors 'none'`, per-IP rate limiting keyed on the verified client IP, request body cap, read/write/idle timeouts, and a per-handler context deadline so no request can pin a goroutine indefinitely. Panics are recovered and logged, never surfaced.

No cookies anywhere. Browser endpoints are stateless and anonymous, so there is no session, no CSRF surface, and no cookie-consent obligation. Language preference lives in `localStorage`.

All database access uses `pgx` with parameterised queries. No SQL is assembled by string concatenation anywhere, which eliminates the legacy's InfluxQL injection class by construction rather than by validation.

## 8. Rate limiting and anti-scraping

Three layers.

### 8.1 Edge

One Cloudflare rate-limiting rule set well above normal human use, as a volumetric backstop. Bot Fight Mode enabled. Known-abusive ASNs challenged.

### 8.2 Origin token buckets

In-process, sharded map with TTL eviction so memory remains bounded.

| Scope | Limit |
|---|---|
| Global per IP | 60 req/min, burst 120 |
| `/overview*` | 30 req/min |
| `/area/{slug}/sensors` | 20 req/min |
| `/sensor/{id}/series` | 30 req/min |
| Partner keys | Per-key quota, independent of IP |

**Rate limits are keyed on the IPv6 `/64` prefix, not the full address** (and `/48` for larger allocations). A single IPv6 host is routinely allocated a `/64` — 2^64 addresses. Per-address limiting against an IPv6 client is not rate limiting at all; the client rotates source addresses at no cost and never hits the same bucket twice. This defeat is invisible when testing over IPv4.

### 8.3 Enumeration detection

Volume limits do not catch a patient scraper issuing one request every few seconds. Breadth does.

Per IP prefix per hour, count **distinct** area slugs and **distinct** sensor IDs requested. Thresholds: roughly 12 distinct areas, roughly 40 distinct sensors. Exceeding them degrades responses to `429` with `Retry-After`.

Tracking uses a capped set or small HyperLogLog per prefix — tens of bytes, TTL-evicted hourly. No request logging is required to detect breadth.

### 8.4 Escalation and collateral damage

Abusive prefixes that persist past `429` receive a Cloudflare managed challenge.

IPv4 addresses are never hard-banned. Bulgarian mobile networks use CGNAT, so a single address can front thousands of legitimate users; banning it removes a carrier's subscribers. Degradation is recoverable, bans are collateral damage.

Proof-of-work interstitials are explicitly deferred: they tax every legitimate visitor and are a response to demonstrated abuse, not a default.

### 8.5 Observability and intent

Rate-limit decisions, `429` counts, and enumeration trips emit metrics to SigNoz, with an alert on sustained enumeration.

`robots.txt` disallows `/api/`, and terms are published. Neither has enforcement value, but both establish intent should action ever be needed.

## 9. Frontend

### 9.1 Islands, not an SPA

Go's `html/template` renders the page shell and all area pages. Svelte hydrates only the interactive parts.

Area pages exist to be found by search — someone searching "въздух Пловдив" must reach them. An SPA serves crawlers an empty container, and fixing that properly requires a Node SSR process alongside the Go binary, doubling the runtime and destroying the single-binary property. Server-rendering from Go costs nothing extra, since templates and the Svelte build compile into the same binary, and yields indexable HTML, immediate first paint, and pages that work without JavaScript.

### 9.2 Components

- **Map island** — MapLibre GL with the PMTiles protocol registered against `bulgaria.pmtiles`. Two sources: choropleth fill from `/overview` at low zoom, point layer from `/area/{slug}/sensors` at z ≥ 11, swapped on zoom.
- **Layer registry** — a declarative list; each entry declares metric, scale, legend, and zoom behaviour. Phase 1 registers PM, noise, and climate. Phase 2's wind and UV register identically. Noise makes the registry load-bearing from day one: separate hardware, separate locations, a different physical unit, and therefore its own scale and legend.
- **Legend** — reads scale tables from `/api/v1/meta`; switches EAQI / EU limit values / WHO guidelines with no reload. All three are presentation over the same stored µg/m³.
- **Sensor panel** — uPlot chart, period selector (24 h / 7 d / 30 d / 1 y), quality badge when flagged.
- **Area pages** — server-rendered: current value, trend, sensor list, exceedance count, or the insufficient-coverage state.

The legacy `value / 125` linear colour ramp is removed. It corresponds to no standard and rendered 30 µg/m³ as mild when EAQI already classifies that as poor.

### 9.3 URLs

```
/                          map, Bulgarian
/en/                       English
/map#11/42.6977/23.3219    zoom and centre in the fragment
/sensor/{id}
/city/{slug}
/oblast/{slug}
/kvartal/{slug}
```

Map state lives in the URL fragment, so it never reaches the server and never fragments the cache.

### 9.4 Initial view resolution

Cascade; first match wins:

1. **URL fragment** — a shared permalink always wins.
2. **Last view from `localStorage`**.
3. **Browser geolocation, only if already granted.** Query the Permissions API; if state is `granted`, resolve silently. If `prompt`, do **not** prompt on load — render the fallback and offer a "locate me" control.
4. **Coarse IP lookup** → the visitor's oblast or city, snapped to that area's centroid and natural zoom.
5. **Whole country** choropleth.

No permission prompt fires on load. Browsers penalise prompts without user intent, and a modal is a poor first impression on a page that has nothing to show behind it. Rendering the country view immediately and offering a control makes the map useful in zero clicks and precise in one.

IP-based personalisation must not poison the cache: the HTML is byte-identical for every visitor, and resolution happens client-side against `/api/v1/locate`, a few dozen bytes marked `private, no-store`. Every other response stays globally shared.

The IP lookup uses a self-hosted MaxMind GeoLite2 City database compiled into the deployment, so visitor addresses never leave the infrastructure. No third-party geolocation service is called.

Resolution is deliberately coarse — an area centroid, never a derived latitude/longitude, never zoomed past city level. IP geolocation is frequently wrong by tens of kilometres, so a precise-looking pin would be confidently misleading. Browser geolocation, being consented and accurate, may zoom to neighbourhood level.

Locate lookups are not logged and the resolved area is not persisted server-side. `CF-IPCountry` skips the lookup for non-Bulgarian visitors, who receive the country view.

### 9.5 Internationalisation

Bulgarian is the default; English lives under `/en/`. UI strings come from JSON message catalogues compiled into the binary.

Map labels render from the vector tiles' `name:bg` field, so the basemap translates with the interface. This is the payoff for choosing vector tiles: raster tiles bake labels into the image, so bilingual labels would require a second tile set.

### 9.6 Accessibility

EAQI colours are the published ones, but colour never carries meaning alone. Every band displays its value and label, choropleth regions carry patterns as a second channel, and flagged sensors are marked by icon as well as by grey. Roughly 8 % of men have some colour vision deficiency, along precisely the red–green axis air quality scales use.

### 9.7 Content-Security-Policy

No inline scripts or styles; hashes emitted at build time. This eliminates the legacy's stored-XSS class, where upstream-controlled street and city strings were concatenated into `.html()`.

## 10. Failure handling

Principle: **the map stays up when everything behind it is broken.**

**Upstream unavailable.** The cycle logs, backs off exponentially, and the previous snapshot remains in memory unchanged. Every payload carries `generated_at`; once older than 15 minutes the UI displays "data as of HH:MM". Never serve empty, never serve zeros.

**Bad snapshot build.** A new snapshot is validated before replacing the live one — non-empty, sensor count within a sane band of the previous cycle, coordinates inside Bulgaria's bounding box. On failure it is discarded, the previous snapshot continues to serve, and an alert fires. Because publication is a pointer swap gated on validation, a corrupt upstream response cannot take the map down.

**Database unavailable.** The snapshot is already in memory, so the map continues to work in full. Only charts, area pages, and the partner API degrade — `503` with `Retry-After`, and the UI reports that history is temporarily unavailable while the live map is untouched.

This inverts the legacy failure mode, where MongoDB or InfluxDB being slow produced a blank page and PHP workers accumulating on timeouts. Here the critical path is a byte slice in memory, making PostgreSQL an optional dependency for the primary experience. Visitor load also cannot slow ingest, and slow ingest cannot slow visitors.

**Partial ingest.** A malformed reading fails that reading only; per-sensor errors are collected and counted while the remainder is written. This directly addresses the legacy's worst defect, where one unquoted field value (`signal: "-78 dBm"`) invalidated an entire batched line-protocol write and silently discarded a whole poll cycle.

**Backfill** is idempotent and resumable: upsert by `(sensor_id, bucket, metric)`, progress checkpointed per day, safe to re-run over any range.

**Error responses** carry a generic message and a correlation ID. Details go to logs only — no stack traces, no driver errors, no SQL. The legacy throws raw InfluxDB error bodies to the browser.

**Tiles unavailable.** MapLibre renders sensor data over a blank background rather than failing.

**Timeouts everywhere** — HTTP read, write, and idle; per-handler context deadline; PostgreSQL statement timeout; upstream client timeout. The legacy sets none, so a hung datastore hangs a worker indefinitely.

**Alerting to SigNoz:** snapshot age, ingest cycle success rate, per-sensor error rate, `429` and enumeration counts, database latency, and quality-flag rate. A spike in the last of these indicates either a real pollution episode or a systemic sensor problem; both are worth knowing.

## 11. Testing

The legacy has no tests, no CI, and no dependency manifest.

### 11.1 Unit

- **Quality scorer** — golden cases: a 22 °C reading among −10 °C neighbours (must flag); a PM spike where neighbours are also elevated (must **not** flag); a stuck sensor; sparse neighbours yielding `no_neighbours`; humidity at exactly 100 %.
- **Scale mapping** — exact boundary values for all three scales. An off-by-one at a band edge changes a region's colour.
- **Snapshot builder** — flagged readings present in output, absent from aggregates.
- **Rate limiter** — including IPv6 `/64` grouping explicitly. The §8.2 failure mode is invisible unless tested directly.

The quality scorer is the strongest candidate for property-based testing. Invariants that must hold for any input: scoring is deterministic; a reading equal to its neighbours' median is never flagged; adding a neighbour equal to the median never causes a previously-`ok` reading to flag. Generated inputs explore combinations no one would write by hand, and this is the component where a subtle threshold bug silently deletes real pollution data.

### 11.2 Integration

Real PostgreSQL with PostGIS and TimescaleDB, in containers in CI.

- The continuous aggregate matches a hand-computed hourly mean over the same rows.
- Retention drops raw rows at 30 days while rollups survive.
- **Coordinate-order test:** insert a sensor at Sofia's real coordinates and assert it resolves to the Sofia polygon. A latitude/longitude swap places it in the Indian Ocean, so this single assertion permanently guards the §5 hazard.

### 11.3 Contract

Real sensor.community responses recorded as golden files and replayed, so upstream schema drift fails a test rather than silently emptying the map.

### 11.4 End-to-end (Playwright)

Map loads and renders the country choropleth; zooming past 11 switches to sensor points; a permalink fragment restores the exact view; the language switch changes both UI and map labels; a flagged sensor renders grey with its badge; the site works with geolocation denied.

### 11.5 Security

Asserted in CI: security headers present; the partner endpoint rejects absent, malformed, and revoked keys; rate limits return `429`; error responses contain no stack trace, driver text, or SQL. Plus `go vet`, `staticcheck`, `gosec`, `govulncheck`, Trivy on the container image, and an SBOM per build.

### 11.6 Load

A k6 run confirming the design's central claim: `/overview` latency stays flat from 10 to 10,000 requests per second with zero database queries. If it is not flat, §7's cost model is wrong, and that must be discovered before launch rather than during an incident.

## 12. Operational prerequisites

- **Rotate the leaked Google Maps API key** committed at `lib/geo2addr.class.php:39` in git history. Removal does not un-leak a key already published; it must be rotated in Google Cloud Console and the old one revoked. Phase 1 does not use Google Geocoding at all — reverse geocoding is replaced by PostGIS polygon containment — so no replacement key is required.
- Secrets supplied by environment or file, never committed. No secret appears in the repository.
- `bulgaria.pmtiles` generated from an OSM Bulgaria extract and regenerated on a manual cadence.
- OSM administrative boundaries imported for cities, the 28 oblasti, and Sofia districts.
- Attribution in the footer: sensor.community (data), OpenStreetMap contributors (boundaries and basemap), Protomaps.

## 13. Deliberate exclusions

- No user accounts, sessions, or cookies.
- No public API and no published API documentation.
- No humidity correction of PM values.
- No noise-specific analysis beyond display on its own layer.
- No migration of legacy MongoDB or InfluxDB data; backfill comes from the upstream archive.
- No redirects from legacy URLs; `airbg.org` is a cold launch.
