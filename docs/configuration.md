# Configuration

`airbg` takes every operational constant from configuration — there are no
defaults in code. This document is the operator-facing reference; the
authoritative record of *why* each shipped value is what it is lives in
`airbg.yaml` itself (as comments next to each key) and, for the small set of
values Phase 3b pinned against pre-sweep behaviour, in
`internal/config/inert_test.go`.

## 1. The two layers

Configuration is resolved in exactly two layers, in this order:

1. **The file** named by `AIRBG_CONFIG` (see below) — a YAML document
   matching the schema in `internal/config/schema.go`.
2. **The environment** — `AIRBG_*` variables that override individual keys
   from the file (see §3), plus the two secrets that are environment-only
   (§4).

There is no third layer and no compiled-in fallback. A key that is present in
neither the file nor the environment is a **startup error**, not a zero
value — for most of these keys, zero is the dangerous setting (an unlimited
listener, a disabled rate limit, an unbounded query). `internal/config/load.go`
collects every missing key at once and reports the whole list in one error, so
fixing configuration is not a one-restart-per-key exercise.

## 2. Where the file comes from

`AIRBG_CONFIG` must name the path to the config file. There is **no fallback
path** — the loader does not guess `./airbg.yaml` or `/etc/airbg/airbg.yaml`
on its own. Guessing a path is a default in disguise, and this project keeps
none. If `AIRBG_CONFIG` is unset, every subcommand that loads configuration
(`serve`, `collect`, `validate-config`, etc.) fails immediately with:

```
config: AIRBG_CONFIG is not set; it must name the airbg.yaml to load
```

## 3. The override rule

Any key in the file can be overridden by an environment variable. The rule is
mechanical, with no lookup table and no exceptions among overridable keys:

**`AIRBG_` + the full dotted key path, uppercased, with `.` replaced by `_`.**

Four worked examples:

```
listen.addr                      → AIRBG_LISTEN_ADDR
listen.metrics_addr              → AIRBG_LISTEN_METRICS_ADDR
upstream.poll_interval           → AIRBG_UPSTREAM_POLL_INTERVAL
quality.ranges.pressure.min      → AIRBG_QUALITY_RANGES_PRESSURE_MIN
```

The rule is true by construction, not by a hand-maintained table that can
drift: `internal/config/load.go`'s `applyEnv` walks the same struct tags the
YAML decoder uses to derive each variable name.

## 4. The two env-only secrets

Two values are **never** read from the file, because they are credentials and
`airbg.yaml` is committed to the repository:

| Variable | Holds |
| --- | --- |
| `AIRBG_DATABASE_URL` | The PostgreSQL connection string |
| `AIRBG_BASEMAP_KEY` | The tile-vendor API key substituted into `basemap.style_url`'s `{key}` placeholder |

Writing either of these into `airbg.yaml` is a **hard rejection at load time**,
not a silently-ignored value. So is any recognisable secret-shaped key name, at
any depth: `database_url`, `dsn`, `password`, `basemap_key`, `api_key`,
`secret`, `token`, and bare **`key`**. One further rejection is by full path
rather than by name — `database.url` — because `url` is legitimate under
`upstream` and `basemap` but never under `database`.

This is enforced by `internal/config/load.go`'s `rejectSecrets`, which walks
every key in the parsed document before the schema is even decoded, so a
credential pasted into the committed file fails the very first load rather than
being merely unused. The error names the environment variable the value should
have gone to; it never echoes the value.

`AIRBG_BASEMAP_KEY` is substituted into `basemap.style_url`'s `{key}`
placeholder, and that URL is **mandatory and must be non-empty** — an empty
`basemap.style_url` is a startup error, not "run without a basemap" — because
its host is concatenated into the Content-Security-Policy, so it must also be
an absolute http(s) URL with a plain host or `host:port` and no userinfo.

## 5. What cannot be overridden from the environment

`series.periods` cannot be set or adjusted via `AIRBG_*`. There is no sane
environment-variable name for "the third list entry's `window` field" — a list
of structured records belongs in the file, not flattened into a variable name.
To change a period's window, cache lifetime, or add/remove a period entirely,
edit `airbg.yaml` directly.

## 6. The renamed variables

Phase 3b's cutover to `airbg.yaml` renamed most existing `AIRBG_*` variables to
match the mechanical rule in §3. This is a **breaking change**: the old names
are no longer read by the loader at all, silently or otherwise.

| old | new |
| --- | --- |
| `AIRBG_LISTEN_ADDR` | `AIRBG_LISTEN_ADDR` (unchanged) |
| `AIRBG_METRICS_ADDR` | `AIRBG_LISTEN_METRICS_ADDR` |
| `AIRBG_BASE_URL` | `AIRBG_LISTEN_BASE_URL` |
| `AIRBG_POLL_INTERVAL` | `AIRBG_UPSTREAM_POLL_INTERVAL` |
| `AIRBG_UPSTREAM_URL` | `AIRBG_UPSTREAM_URL` (unchanged) |
| `AIRBG_DB_API_CONNS` | `AIRBG_DATABASE_API_CONNS` |
| `AIRBG_DB_COLLECTOR_CONNS` | `AIRBG_DATABASE_COLLECTOR_CONNS` |
| `AIRBG_MAX_CONNS` | `AIRBG_LISTEN_MAX_CONNS` |
| `AIRBG_MAX_DB_INFLIGHT` | `AIRBG_DATABASE_MAX_INFLIGHT` |
| `AIRBG_TRUSTED_PROXY_CIDRS` | `AIRBG_LISTEN_TRUSTED_PROXY_CIDRS` |
| `AIRBG_BASEMAP_STYLE_URL` | `AIRBG_BASEMAP_STYLE_URL` (unchanged) |

`AIRBG_DATABASE_URL` and `AIRBG_BASEMAP_KEY` (§4) and `AIRBG_CONFIG` (§2) are
new names with no predecessor. `AIRBG_LIVE_TEST` is a test-only switch, not
configuration, and is unaffected by any of this.

## 7. `airbg validate-config`

Run `airbg validate-config` to load configuration exactly as `serve` and
`collect` would, then report the result and exit. It is meant to be a deploy
gate: wire it into CI or a pre-deploy hook and treat a non-zero exit as "do
not deploy."

- **Exit 0**: configuration loaded and passed every check in
  `Config.Validate()`. Stdout is a tab-aligned table of the operationally
  significant values (listener addresses, pool sizes, poll interval, cache
  age, rate limits, coverage threshold) plus a `configuration is valid` line.
- **Exit 1**: either the file could not be read/parsed, a required key was
  missing, or a semantic check failed (see §9). The problem is printed to
  stderr.

It **never prints a secret**. `AIRBG_DATABASE_URL` and `AIRBG_BASEMAP_KEY` are
reported only as `(set)` or `(not set)` — never their value — so the command
is safe to run in a CI log. This also holds for the failure path: a basemap
style URL that fails to parse is reported by reason only (e.g. "invalid
port"), never by echoing the URL itself, because `AIRBG_BASEMAP_KEY` has
already been substituted into that URL's query string by the time validation
runs.

## 8. The two colour homes

Colour values live in exactly one of two places, split by a mechanical rule:

- **`internal/web/static/theme.css`** — anything a CSS rule can style: page
  chrome, buttons, text, borders, backgrounds behind HTML elements.
- **`frontend.*` in `airbg.yaml`** — the four paint values that are handed
  directly to WebGL (MapLibre) layers and the chart `<canvas>`, which CSS
  cannot reach at all: `frontend.no_data_colour`, `frontend.marker_stroke_colour`,
  `frontend.empty_basemap_colour`, `frontend.chart_line_colour`.

The boundary is mechanical, not aesthetic: if CSS can style it, it belongs in
`theme.css`; if JavaScript passes it to a canvas or a GL layer, it belongs in
`frontend.*`.

**Band colours are in neither place.** The EAQI/EU-limit/WHO scale bands come
from `/api/v1/scales` at request time, because they are legislation, not a
visual constant — a legislative change to the bands is a one-file server
edit, not a config or CSS change.

## 9. The checked couplings

Several values are related to each other in ways `Config.Validate()` checks
at load time, so a misconfiguration fails at startup rather than in
production:

| Coupling | Why |
| --- | --- |
| `listen.addr` ≠ `listen.metrics_addr` | Sharing the address would expose `/metrics` on the public listener — handing an attacker the exact counters that show whether their probing is being rate limited. |
| `cache.data_max_age` ≤ `upstream.poll_interval / 2` | A client caching longer than half an ingest cycle can be shown a reading that has already been superseded by the next poll. |
| `upstream.poll_interval` ≥ `upstream.min_poll_interval` | Polling the volunteer-run public sensor.community API faster than the floor is abusive; the floor is enforced, not advisory. |
| `series.default_window` matches a configured `series.periods[].window` | The snapshot serves the default window without touching the database. If no period shares that window, the snapshot would answer a question no period actually asks. |
| `database.statement_timeouts.series` ≤ `.default` | `/series` is the most expensive public query and runs while holding a scarce admission slot; its budget must be the tighter one, never looser than the pool default. |
| `frontend.zoom_city` < `frontend.zoom_sensor` | The map has three tiers — country, then city, then sensor — and the zoom thresholds must preserve that order or a zoom level would resolve to no tier or the wrong one. |
| `listen.csp` must not contain `unsafe-inline` or `unsafe-eval` | Either directive makes the Content-Security-Policy decorative; an inline-script allowance is the single most common way a CSP stops mitigating XSS. Making the policy configurable must not make it disableable. |

All of these are enforced in `internal/config/validate.go`; a violation is
reported alongside every other problem in one `config: N problem(s)` error,
not one restart at a time.

## 10. Rate limiting: eviction intervals and the two `Retry-After`s

There are three limiters, and they are configured asymmetrically on purpose.

**`evict_interval` is per limiter.** It is how often a limiter's key map is
swept for idle entries, and bounding that map is a memory-exhaustion defence,
not housekeeping: every distinct client IP creates an entry, so an unswept map
is what an attacker grows. Each limiter therefore sweeps on **its own**
configured interval, in `internal/server/server.go`'s `startEvicting`.

There is exactly one exception, and it is deliberate: the **enumerate/breadth
limiter has no `evict_interval` key of its own** and is swept on
`ratelimit.api.evict_interval`. If a key is ever added under
`ratelimit.enumerate`, `startEvicting` must be changed to use it — an
undocumented shared value is how `ratelimit.series.evict_interval` came to be
configured but ignored.

**`retry_after` is not one thing.** The two values that exist answer different
questions, and one more is deliberately absent:

| Key | Status code | Meaning |
| --- | --- | --- |
| *(none under `ratelimit.api`)* | 429 | Not configurable, and not an oversight. The hint on a rate-limit refusal is **computed** from the refusing client's own token deficit, so it names the moment a token actually exists instead of a fixed guess. A configured value here could only be wrong. |
| `ratelimit.series.retry_after` | 503 | The hint on the series routes' *admission* refusal — the database in-flight cap (`database.max_inflight`) is full. An admission decision, not a rate-limit one: no bucket rejected the request, so nothing about it is computable from a token deficit. |
| `ratelimit.enumerate.retry_after` | 429 | The breadth limiter's hint. Fixed because that limiter is a fixed window, not a token bucket, so the wait is the window, not a deficit. |

A fourth `Retry-After` exists in the code and is **not** configuration:
`internal/api/router.go`'s data-not-ready 503, sent when the store has no
usable data yet. It is about ingest state, not about limits.

## 11. Values that used to be constants

Phase 3b moved the last operational constants out of Go and JavaScript. They
behave exactly as before; only their home changed, and `internal/config/inert_test.go`
pins each one to the value of the constant it replaced.

| Key | Consumed by | Meaning |
| --- | --- | --- |
| `quality.pm_ratio_threshold` | `internal/quality/spatial.go` | A PM reading is flagged as a spatial outlier only if it exceeds this multiple of its neighbours' median. Guards against flagging a real, genuinely regional pollution episode. |
| `quality.pm_absolute_threshold` | `internal/quality/spatial.go` | …**and** exceeds this absolute µg/m³ value. Both guards must trip; either alone spares the reading. Raising either makes the flagging more permissive. |
| `quality.smooth_field_floors.{temperature,humidity,pressure}` | `internal/quality/spatial.go` | Minimum deviation, in each metric's own unit, below which a reading is never flagged however tight its neighbours agree. Without a floor, a cluster of identical readings gives a zero spread and flags ordinary noise. A metric absent from this map (e.g. `noise_LAeq`) has no floor and is never spatially flagged on this path. |
| `frontend.default_zoom`, `frontend.default_lon`, `frontend.default_lat` | `internal/web/templates/index.gohtml`, `internal/api/locate.go` | The map's opening view — the national fallback used before geolocation resolves, and the body `/api/v1/locate` returns when it cannot place a client. Previously duplicated as three hardcoded literals in a template, a Go file and a JS island; there is now no JS-side fallback, so the attributes the server renders are the only source. |
