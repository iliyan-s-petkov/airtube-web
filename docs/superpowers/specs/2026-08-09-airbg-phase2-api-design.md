# airbg.org Phase 2 — API and server-rendered pages

**Status:** approved 2026-08-09
**Predecessor:** [Phase 1 — data foundation](2026-08-07-airbg-phase1-design.md), merged to `master` as `d07bba8`
**Successor:** Phase 3 — Svelte islands, MapLibre, PMTiles

## 1. Relationship to the Phase 1 spec

Phase 1's spec already contains a full API design in its sections 7 (API), 8 (rate limiting and anti-scraping) and 9 (frontend). Those sections remain authoritative and are not restated here except where this document changes or extends them.

This document does three things:

1. Fixes the Phase 2 / Phase 3 boundary.
2. Resolves five things sections 7–9 assume but never specify: how partner keys are issued, where the geolocation data for `/api/v1/locate` comes from, whether Cloudflare is in fact the edge, how metrics are exported, and where translations live.
3. Specifies the internal structure — modules, snapshot lifecycle, caching, error shapes, tests — that the plan will be written against.

## 2. Scope

**In Phase 2:**

- Every browser endpoint in Phase 1 §7.2.
- The middleware chain in Phase 1 §7.5, including the Content-Security-Policy of §9.7. Phase 2's templates carry no inline script or style, so the policy ships with the first page rather than being retrofitted once Phase 3 has inline code to excuse.
- The rate limiting and enumeration detection in Phase 1 §8.2 and §8.3.
- The Go `html/template` page shell and the server-rendered area pages in Phase 1 §9.1 and §9.2, in Bulgarian and English, per §9.5.
- Metrics exposition.

Area pages are Go templates over the same queries the API already runs. Building them alongside the API is nearly free, and it means Phase 2 ends with something that can be opened in a browser and deployed, rather than something only reachable by `curl`. Phase 3 then adds interactivity to pages that already work.

**Deferred, with reasons:**

| Deferred | To | Reason |
|---|---|---|
| Partner API (`/api/partner/v1/`) | Phase 4 | No partner exists. Building it now means guessing at requirements a real consumer would supply. Phase 1's "API but not public" requirement is already satisfied by the browser endpoints being tiered, uncacheable-by-viewport, and rate-limited. |
| Humidity correction of PM | Later phase | Unchanged from Phase 1 §6.4: selecting a correction model and publishing numbers that differ from sensor.community's own warrants its own decision. |
| Wind, UV, weather overlays; OSM POI proximity | Later phase | Unchanged from Phase 1. |
| Svelte islands, MapLibre, PMTiles, uPlot charts | Phase 3 | |

The `/api/partner/v1/` path prefix **is** reserved in Phase 2: any request under it returns `501 Not Implemented`. Reserving it costs one route and guarantees no browser endpoint added later collides with it.

Note for reviewers: the `api_key` table already exists, created by Phase 1's migration `00004_areas.sql` (Phase 1 §5.6). Deferring the partner API means no Phase 2 code reads or writes it. The table is not dropped — dropping and recreating it would churn the migration history for no gain.

## 3. Decisions taken on 2026-08-09

### 3.1 Edge: Cloudflare, proxied

`airbg.org` sits behind Cloudflare's proxy. This settles Phase 1 §8.1, which assumed it.

The consequence that matters is client-IP derivation. Every control in §8.2 and §8.3 keys on the client IP, and behind a proxy that value arrives in a request header — which is attacker-controlled. An origin that trusts `CF-Connecting-IP` unconditionally has no rate limiting at all: a scraper sets a fresh fabricated address per request and never touches the same bucket twice.

**Rule:** `CF-Connecting-IP` is honoured **only** when the TCP peer address falls inside Cloudflare's published IPv4 or IPv6 ranges. Otherwise the socket peer address is used and the header is ignored entirely. The ranges are embedded in the binary and overridable by configuration, so they can be refreshed without a rebuild.

This is the same diagnostic frame Phase 1 used repeatedly: a safety mechanism must not sit downstream of the failure it guards. A rate limiter keyed on a spoofable value is not a rate limiter.

Two further consequences, recorded so deployment does not rediscover them:

- `bulgaria.pmtiles` must be served from a DNS-only (non-proxied) hostname or object storage. Cloudflare's terms do not permit proxying large non-HTML assets on the free plan.
- Cloudflare terminates TLS. The origin does not need a public certificate, but it must not be reachable except through Cloudflare, or the header-trust rule above is the only thing standing between a direct-to-origin scraper and an unlimited request rate.

### 3.2 Geolocation: Cloudflare visitor-location headers

`/api/v1/locate` reads Cloudflare's **Add visitor location headers** managed transform, which injects `cf-ipcity`, `cf-region`, `cf-region-code`, `cf-iplatitude` and `cf-iplongitude` into the request. The handler snaps the latitude/longitude to the containing area using the same `ST_Covers` query Phase 1 already has, falling back to `cf-region` matched against oblast names, then to the whole-country view.

Verified against Cloudflare's Managed Transforms reference (retrieved 2026-08-09): this transform carries no plan restriction, unlike *Add bot protection headers* and *Add "True-Client-IP" header*, both of which are explicitly marked Enterprise-only. It is therefore available on the free plan.

**Conflict to configure around:** *Add visitor location headers* is incompatible with *Remove visitor IP headers*. Both must not be enabled.

The rejected alternative was MaxMind GeoLite2-City. It works offline and identically in development, but it requires a MaxMind account, a weekly update job to satisfy the 30-day freshness term of its licence, a file that must never be committed, and it ends the property that the binary is self-sufficient. Cloudflare's headers cost none of that.

These headers are subject to the same trust rule as §3.1: they are read only when the peer is a Cloudflare address. From any other peer they are ignored and `/locate` returns the country-level fallback.

### 3.3 Partner keys: not issued in Phase 2

See §2. When the partner API is built, keys will be issued by a CLI subcommand requiring shell access to the server, not by any HTTP endpoint — there is no account system to authenticate a self-service request against.

### 3.4 Metrics: hand-rolled Prometheus exposition, no new dependency

Phase 1 established that no third-party dependency is added to this project. Both `prometheus/client_golang` and the OpenTelemetry SDK would break that, and neither is necessary: the Prometheus text exposition format is line-based and small.

`internal/metrics` holds atomic counters and gauges and renders them at `GET /metrics`. That endpoint binds to a **separate listener on a separate port**, not the public mux, so it is never routed through Cloudflare and never publicly reachable. Any scraper — Prometheus, SigNoz's OTel collector, Grafana Agent — can consume it without a code change, which keeps Phase 1 §8.5's SigNoz intent intact without coupling the binary to it.

Counters required at minimum: requests by route and status, rate-limit trips by scope, enumeration trips, snapshot builds and build duration, snapshot age, upstream poll outcomes, i18n missing-key events.

An enumeration detector with no counter is indistinguishable from one that never fires. The metrics are how §8.3 is known to work at all.

### 3.5 i18n: embedded JSON catalogues

Phase 1 §9.5 already settles the mechanism — Bulgarian by default, English under `/en/`, UI strings from JSON catalogues compiled into the binary. Phase 2 adds only the two details §9.5 leaves open: where language is read from, and what a missing key does.

`internal/i18n` embeds `bg.json` and `en.json` and exposes a `T` function to templates. Language is derived from the URL path (`/` is Bulgarian, `/en/` is English) — never from a cookie or an `Accept-Language` header, because either would vary the response and fragment a cache that is otherwise one shared entry per page.

A key missing from a non-default catalogue falls back to the Bulgarian string and increments a metric. It never renders an empty string, and it never panics: a missing translation should degrade to the wrong language, not to a blank page.

## 4. Architecture

One process, two subsystems sharing a snapshot — the shape Phase 1 §4.1 anticipated. Routing is `net/http`'s pattern matching (`GET /api/v1/area/{slug}/sensors`), which removes any need for a router dependency.

```
internal/snapshot/   atomic.Pointer[Snapshot]; rebuilt at the end of each ingest cycle
internal/api/        JSON handlers, one file per resource group
internal/httpx/      middleware chain: recover → clientIP → ratelimit → enumeration → headers
internal/ratelimit/  sharded token buckets with TTL eviction; IPv6 /64 keying
internal/metrics/    atomic counters; Prometheus text exposition on a private listener
internal/web/        html/template page shell and area pages, embedded
internal/i18n/       BG/EN catalogues, T funcmap
```

Files are split by responsibility, and each of these units can be understood and tested without reading the others. `internal/httpx` in particular must not know what the handlers do; it operates on `http.Handler` alone.

## 5. Snapshot lifecycle

Phase 1 §7.2 specifies two serving strategies in one sentence — "served from memory or from narrow indexed queries" — and the split is the right one:

- **From memory:** `/overview`, `/overview?tier=city`, `/area/{slug}/sensors`, `/areas`, `/meta`. Each has a bounded number of distinct responses, all derivable from one ingest cycle.
- **From indexed queries:** `/sensor/{id}/series?period=`, `/area/{slug}?period=`. The number of distinct responses is unbounded, so caching them in memory does not terminate; each is a single indexed range scan under the pool's default statement timeout.

At the end of each ingest cycle, the collector builds every in-memory tier, serialises each to JSON once, gzips each once, computes a SHA-256 of each body for its ETag, and publishes the whole thing with a single `atomic.Pointer.Store`. Handlers `Load()` the pointer and write precomputed bytes. The hot path takes no lock, allocates nothing beyond the response write, and never touches Postgres.

Building in the ingest cycle rather than on request also makes the API's freshness equal to ingest's, which is honest: `generated_at` is when the data was collected, not when a cache filled.

Before the first cycle completes, the pointer is nil and memory-backed endpoints return `503`. This is stated explicitly because the alternative — serving an empty country as though it were measured — is the failure mode Phase 1 kept finding: a bug that reports success.

## 6. Caching

ETags are computed **per response body**, not from `generated_at`. Deriving them from the timestamp would give two tiers built in the same cycle the same ETag while their bytes differ, so a client that had fetched `/overview` would receive a spurious `304` for `/overview?tier=city`. Hashing the body cannot have that failure.

`Cache-Control` on memory-backed endpoints is `public, max-age=<seconds until the next expected ingest cycle, floored at 30>, stale-while-revalidate=600`.

A fixed `max-age=300` against a 5-minute poll is wrong: set at 299 seconds into a cycle, it serves stale data for nearly two cycles. Computing the remaining time bounds staleness to one cycle. `stale-while-revalidate` lets Cloudflare keep serving during an ingest hiccup rather than stampeding the origin with revalidations.

Series endpoints carry `public, max-age=300`. `/locate` alone carries `private, no-store`.

`Vary: Accept-Encoding` only. Nothing varies on cookie (there are none) or on language (it is path-derived), so cache keys stay clean and every response but `/locate` remains globally shared.

## 7. Error handling

Four shapes, and no others:

| Status | Cause |
|---|---|
| `400` | Unknown slug, period outside the allowlist, malformed sensor ID |
| `404` | Well-formed but absent |
| `429` | Rate-limit or enumeration trip; always carries `Retry-After` |
| `503` | Snapshot not yet built |

Slugs are validated against the snapshot's known set and periods against a fixed allowlist before any query runs — so no caller-supplied string reaches SQL as text. All database access remains `pgx` with bind parameters; no SQL is assembled by concatenation anywhere, which is the constraint that eliminates the legacy application's InfluxQL injection class by construction.

Bodies are `{"error":"<code>","message":"<human text>"}` with a fixed code set. No internal detail, no SQL text, no stack trace. Panics are recovered in middleware, logged with route and verified client IP, and returned as `500` with a generic body.

## 8. Testing

Three layers, following the pattern Phase 1 established. The governing rule is unchanged: **every test must fail if its fix is removed.** An assertion downstream of a filter is a tautology.

**Unit, no container:**

- Rate limiter. Includes a test that rotates source addresses within a single IPv6 `/64` and asserts the bucket **does** trip. This is Phase 2's equivalent of Phase 1's coordinate-swap detector: the defeat it guards against is invisible when testing over IPv4, so it must be asserted deliberately.
- Client-IP derivation. `CF-Connecting-IP` from a Cloudflare peer is honoured; the same header from a non-Cloudflare peer is ignored and the socket address is used. Both directions asserted — a test that only checks the trusted case would pass against an implementation that trusts everyone.
- Visitor-location headers, same trust matrix.
- Enumeration counter: distinct-slug and distinct-sensor thresholds, TTL eviction, memory bound.
- ETag stability: identical snapshots yield identical ETags; a changed value yields a different one; two tiers of one cycle yield different ones.

**Integration, testcontainers against migrated pg18:**

- Every endpoint against seeded readings: payload shape, `304` on matching `If-None-Match`, `503` before the first snapshot, `400`/`404` boundaries.
- Area page rendering in both languages.

**Golden files:**

- Columnar JSON for a fixed seed, so a field rename or a column-order change is a visible diff in review rather than a silent break in Phase 3.

## 9. Risks

- **Direct-to-origin access defeats §3.1.** If the origin is reachable other than through Cloudflare, the header-trust rule is the only remaining control and a scraper simply connects directly. Deployment (item 4) must firewall the origin to Cloudflare's ranges or use a tunnel. Recorded here because the API code cannot enforce it.
- **Snapshot memory.** Roughly 900 sensors across three tiers plus gzipped copies is small — expected well under 10 MB — but it is held twice during a rebuild. Measured, not assumed, in the integration tests.
- **Cloudflare city-level accuracy in Bulgaria is unverified.** If it proves poor, `/locate` degrades to oblast or country. It is a first-impression nicety, not a correctness requirement, and the §9.4 cascade already tolerates its absence.
