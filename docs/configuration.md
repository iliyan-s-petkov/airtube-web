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

Writing either of these — or any recognisable secret-shaped key such as
`password`, `dsn`, `api_key`, `secret`, or `token` — into `airbg.yaml` is a
**hard rejection at load time**, not a silently-ignored value. This is
enforced by `internal/config/load.go`'s `rejectSecrets`, which walks every key
in the parsed document before the schema is even decoded, so a credential
pasted into the committed file fails the very first load rather than being
merely unused.

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
