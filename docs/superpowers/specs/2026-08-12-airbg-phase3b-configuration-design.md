# airbg.org Phase 3b — Configuration and Theme Design

**Date:** 2026-08-12
**Status:** approved design, ready for an implementation plan
**Preceded by:** Phase 3a (frontend), merged into `master` as `a007f05`
**Followed by:** Phase 3c (metric switcher, sensor panel, find-me control, Playwright browser tests)
**Then:** Phase 4 (deployment)

---

## 1. Goal

Remove every hardcoded operational, domain and presentational value from the Go and JavaScript
sources and move it into **one human-readable YAML file**, `airbg.yaml`, that ships with the project
pre-populated with today's values. After this phase, an operator can retune the site — rate limits,
timeouts, quality thresholds, zoom tiers, colours — by editing one commented file, and no value the
program uses is a value nobody wrote down.

**This phase must change no observable behaviour.** Every value in the committed file equals the
constant it replaces. That property is the phase's primary test target (§11).

## 2. Non-goals

- No retuning. The 12-areas-per-hour enumeration limit and the 1 rps series limit become
  *changeable* here; deciding what they should be is Phase 4's call against real traffic.
- No new features. The metric switcher, sensor detail panel and find-me control are Phase 3c.
- No Playwright or browser tests. Phase 3c.
- No live reload / SIGHUP. Values reach constructors at startup; changing them means a restart.
  Reload would require every consumer to hold a mutable reference — a large concurrency surface for
  a site that redeploys in seconds.
- No change to `www-root/` (the legacy PHP app).
- No change to the EAQI / WHO / EU colour band tables (§6.3).
- No new npm dependency. `web/package.json` is untouched by this phase.

## 3. Decisions taken by the owner during design

These override standing project constraints and are recorded as explicit decisions, not drift.

1. **"Nothing should be hardcoded, everything should be configurable."** Asked whether mathematical
   and standards-fixed constants (`madScale = 1.4826`, `earthRadiusMetres`, the limiter shard count,
   the 253-character DNS name limit) should be exempt, the owner chose **no exemptions**. All four
   become configurable. Mitigation, not a carve-out: every value is range-validated and fails closed
   at startup (§7), so a wrong value stops the process rather than silently changing behaviour.
2. **Configuration lives in a human-readable file, not in Go.** One global config file for the whole
   project, in preference to per-package Go defaults.
3. **Defaults live in the file, not in code.** There is no built-in default layer. The file is
   mandatory and pre-populated; a missing file or a missing key is a startup error.
4. **Format is YAML**, accepting `gopkg.in/yaml.v3 v3.0.1` as a new Go dependency. This is the first
   deliberate break of the standing "no new Go dependency, `go.mod` byte-identical" rule, which had
   held for three phases. The owner was shown that `encoding/json` is the only zero-dependency
   option and that JSON forbids comments, and chose comments. The module has no transitive
   dependencies.
5. **The EAQI / WHO / EU colour bands stay in code** (§6.3).
6. **The theme palette is a separate committed CSS file**, not a `theme:` block in `airbg.yaml`
   (§6.4).

## 4. Constraints inherited from earlier phases

- Module path is exactly `airbg.org`. Go 1.26. PostgreSQL 18 + PostGIS + TimescaleDB.
- Go tests use the standard library `testing` package only — no assertion library, no mocking
  framework. Hand-written `if got != want { t.Errorf("X = %v, want %v", got, want) }`, `t.Run`
  subtests, `t.Setenv`.
- Every load-bearing property must be **mutation-proven**: break the production code, observe and
  quote the real failure. A mutation of test code proves nothing. Twelve Phase 2 tests once passed
  while inert; that is the failure mode this rule exists to catch.
- `CLAUDE.md` is never staged or committed. No `Co-Authored-By` trailer and no "Generated with"
  line in any commit or PR.
- The CSP has no `'unsafe-inline'` and no `'unsafe-eval'`, and never will. `style-src` is `'self'`,
  so no server-rendered `<style>` block and no inline `style=` attribute is possible.
- All browser configuration arrives as server-rendered `data-*` attributes on island containers,
  written with `textContent`, never `innerHTML`.
- Immutable caching is only sound on content-hashed filenames.
- `www-root/` is never modified. `docker-compose.yml` is local development only.

## 5. Architecture

### 5.1 Two layers

```
airbg.yaml   →   AIRBG_* environment overrides
(the file)       (deploy-time retuning)
```

There is no defaults-in-code layer. The file is the source of truth; environment variables override
individual scalars for a deploy that must retune without editing the image.

### 5.2 The file is mandatory

- Path from `AIRBG_CONFIG_FILE`, defaulting to `airbg.yaml` beside the binary.
- Missing, unreadable, or incomplete file → the process **refuses to start**, with an error naming
  the offending key or path. Fail-closed, matching how `AIRBG_DATABASE_URL` already behaves.
- Committed at the repository root as `airbg.yaml`; `COPY`'d into the release image at
  `/etc/airbg/airbg.yaml`. Because the file is required, this Dockerfile change is load-bearing: an
  image without it cannot boot.

### 5.3 Absent must be distinguishable from zero

YAML decodes an absent key by leaving the field at its zero value. Zero is the *dangerous* value for
almost every knob in this schema: a zero rate limit refuses every request, a zero `mad_scale`
divides by zero, a zero timeout means no timeout. A zero-value default is still a default — just an
invisible one.

Therefore the schema decodes into a **raw struct of pointers**, and loading requires every pointer
to be non-nil before converting to the plain-valued struct the rest of the program sees:

```go
type rawQuality struct {
    MADScale     *float64 `yaml:"mad_scale"`
    MADThreshold *float64 `yaml:"mad_threshold"`
}
// nil → error: "config: quality.mad_scale is not set in airbg.yaml"
// set → range-validated, then copied into config.Quality
```

Decoding uses `yaml.Decoder` with `KnownFields(true)`. Both failure directions are then covered: a
misspelled key errors as unknown, and the correctly-spelled key it was meant to be errors as
missing. Neither can silently become zero.

### 5.4 Secrets never enter the file

`AIRBG_DATABASE_URL` and `AIRBG_BASEMAP_KEY` remain environment-only and keep their current names.
The file is committed and baked into the image; secrets are not. Loading **rejects** a file that
contains either key rather than ignoring it, so an operator who puts a password there gets an error
instead of a false sense of safety.

### 5.5 Schema shape in Go

`internal/config` holds the whole schema as grouped nested structs — `Config.Listen`,
`Config.Timeouts`, `Config.Database`, `Config.RateLimit`, `Config.Upstream`, `Config.Store`,
`Config.Series`, `Config.Quality`, `Config.Backfill`, `Config.Frontend`, `Config.Basemap`. Each
consumer receives **only its own group**: `store.New(pool, cfg.Store)`,
`quality.NewScorer(cfg.Quality)`. Packages cannot reach values that are not theirs, and no package
outside `internal/config` reads an environment variable.

## 6. The file

### 6.1 `airbg.yaml`, as committed

Every value below is the value the code uses today.

```yaml
listen:
  addr: "127.0.0.1:8080"
  metrics_addr: "127.0.0.1:9090"
  base_url: ""
  trusted_proxy_cidrs: []
  max_conns: 4096

timeouts:
  read_header: 5s
  read: 10s
  write: 30s
  idle: 60s
  shutdown_grace: 15s

database:
  api_conns: 8
  collector_conns: 4
  max_db_inflight: 16
  statement_timeouts:
    pool: 15s
    assign: 60s
    operator: 10m
    series: 5s

ratelimit:
  bucket_ttl: 30m
  evict_interval: 5m
  shards: 32
  api:
    per_second: 10
    burst: 60
  series:
    per_second: 1
    burst: 10
    retry_after: 2s
  enumerate:
    areas_per_window: 12
    window: 1h
    retry_after: 900s

upstream:
  url: "https://data.sensor.community/airrohr/v1/filter/country=BG"
  poll_interval: 5m
  min_poll_interval: 30s
  timeout: 30s
  max_payload_bytes: 67108864

store:
  coverage_threshold: 3
  freshness_window: 2h

series:
  default_metric: "P2"
  default_period: "24h"
  periods:
    - { name: "24h", window: 24h,   hourly: false }
    - { name: "7d",  window: 168h,  hourly: false }
    - { name: "30d", window: 720h,  hourly: false }
    - { name: "1y",  window: 8760h, hourly: true }

quality:
  min_neighbours: 3
  mad_scale: 1.4826
  mad_threshold: 3.5
  neighbour_radius_m: 15000
  earth_radius_m: 6371000
  history_depth: 12
  ranges:
    P1:           { min: 0,   max: 1000 }
    P2:           { min: 0,   max: 1000 }
    temperature:  { min: -40, max: 60 }
    humidity:     { min: 0,   max: 100 }
    pressure:     { min: 650, max: 1100 }
    noise_LAeq:   { min: 25,  max: 120 }
    noise_LA_max: { min: 25,  max: 120 }

backfill:
  high_rejection_fraction: 0.5

frontend:
  zoom_country_below: 9
  zoom_city_below: 11
  moveend_debounce_ms: 250
  no_data_colour: "#9ca3af"
  marker_stroke_colour: "#ffffff"
  empty_basemap_colour: "#eef2f5"
  chart_line_colour: "#2563eb"
  chart_period: "24h"

basemap:
  style_url: ""
  max_host_length: 253
```

### 6.2 Comments that must survive the move

The file carries a comment for every key. Three are load-bearing and must be moved verbatim from
the code, not paraphrased:

- **`quality.ranges.pressure.min: 650`.** `internal/quality/ranges.go` explains that the floor is
  650 hPa (~3600 m) and not the approved spec's 800 hPa (~2000 m), because Bulgaria is mountainous —
  Musala is 2925 m (~715 hPa) and inhabited sites in Rila and Pirin sit above 2000 m with sensors
  reporting from them. An 800 hPa floor silently discarded every such reading as `out_of_range`,
  indistinguishable from a broken sensor. The existing comment ends *"do not 'tidy' this back
  toward sea level"* — in a config file, that value looks like a typo awaiting correction, so the
  reason must travel with it.
- **`quality.mad_scale: 1.4826`.** This is 1/Φ⁻¹(0.75), the constant that makes a median absolute
  deviation comparable to a standard deviation. Changing it does not tune outlier detection; it
  breaks it, silently.
- **`ratelimit.series`.** The existing comment explains that the limit constrains the burst rather
  than the sustained rate, and sits two orders of magnitude below what a scraper needs. That is the
  anti-extraction rationale and it must be readable by whoever later considers raising it.

### 6.3 What stays in code, and why

The EAQI / WHO / EU colour band tables in `internal/api/scales.go` — six scales (three standards ×
P1 and P2), each with band labels in English and Bulgarian, upper bounds and hex colours.

These are published health standards with fixed definitions, not tunables. An operator who edits
them ships a map labelled "EAQI" displaying thresholds that are not EAQI — misinformation about air
quality carrying the authority of an official scale. The band labels are also translated strings,
which belong with the rest of the i18n content in `internal/i18n/`, not in an operations file. No
validation can distinguish a wrong-but-plausible threshold from a right one.

### 6.4 Theme

`app.css` is refactored so that **every colour is a `var(--…)` custom property**. It currently has
five properties in a `:root` block plus eight literals further down (`#ddd`, `#eee`, `#fff8f0`,
`#f2f2f2`, `rgba(255,255,255,.9)` and the `--fg`/`--muted`/`--bg`/`--accent`/`--warn` values
themselves). The palette moves to a new committed file, `internal/web/static/theme.css`, containing
only the `:root` block. `app.css` consumes the properties and defines no colour of its own.

Swapping the theme means replacing `theme.css`. Because it is embedded via `go:embed`, this requires
a rebuild — accepted.

The split is *palette* from *layout*: an operator changes colours, while margins, flex rules and
breakpoints stay in code. This also keeps the CSP intact — the theme cannot introduce arbitrary CSS
rules, so it cannot become a styling-based injection surface (for example a `background: url(...)`
that phones home).

`theme.css` is served through the existing `/static/` handler, whose cache policy is unchanged.
Since it is not content-hashed, it keeps the same revalidating policy as the other unhashed static
assets rather than an immutable one.

**Boundary between `theme.css` and the `frontend.*` colours:** `theme.css` holds what CSS can style —
text, backgrounds, borders, rules, accents. Four colours cannot live there because nothing applies a
CSS rule to them: they are paint values handed to MapLibre's WebGL layers and uPlot's canvas, read by
JavaScript from `data-*` attributes.

| Key | Today's value | Consumer |
|---|---|---|
| `no_data_colour` | `#9ca3af` | `colour.js` — the "no usable reading" grey, deliberately not a band colour so an area below the coverage threshold can never paint as clean air |
| `marker_stroke_colour` | `#ffffff` | `map.js` — `circle-stroke-color` on the sensor layer |
| `empty_basemap_colour` | `#eef2f5` | `map.js` — the background of the fallback style used when `basemap.style_url` is empty |
| `chart_line_colour` | `#2563eb` | `chart.js` — the uPlot series stroke |

The rule is mechanical, so a later edit knows which file to reach for: **if CSS can style it, it
belongs in `theme.css`; if JavaScript passes it to a canvas or a GL layer, it belongs in
`frontend.*`.** Both files carry a comment stating that rule and pointing at the other.

An operator changing the palette must therefore edit two files. That is a real cost, accepted because
the alternative — serving the whole palette through `data-*` attributes so it lives in one place —
would move page chrome out of CSS and into JavaScript, which is worse for both caching and the CSP.

### 6.5 Three couplings collapsed into single keys

Values that must always agree become one key, so they cannot be configured into disagreement:

1. **`series.default_period`** drives the snapshot's window. `internal/snapshot/snapshot.go:36`
   currently carries the comment *"DefaultSeriesWindow must equal the window api.parsePeriod derives
   from"* — two constants in different packages that must match. Two variables that must always be
   equal are one variable; the comment becomes a validated invariant.
2. **`database.max_db_inflight`** replaces three definitions of 16 — `admit.DefaultSize`,
   `api.defaultMaxDBInflight` and `server.defaultMaxDBInflight`.
3. **`ratelimit.bucket_ttl` and `ratelimit.evict_interval`** replace the pair duplicated verbatim in
   `internal/server/server.go` and `internal/api/series.go`.

## 7. Validation and failure behaviour

Every scalar is validated at load with an error that names the key. Not a generic "invalid config".

| Kind | Rule |
|---|---|
| Rates, bursts | `> 0` |
| Timeouts, windows, TTLs, retry-afters | `> 0` |
| `upstream.poll_interval` | `> 0` and `>= upstream.min_poll_interval` (preserves the existing 30s floor) |
| Counts — `shards`, `history_depth`, `min_neighbours`, `coverage_threshold`, `api_conns`, `collector_conns`, `max_db_inflight`, `max_conns` | `>= 1` |
| `quality.mad_scale`, `mad_threshold`, `neighbour_radius_m`, `earth_radius_m` | `> 0` and finite — rejects `0`, `NaN`, `-1` |
| `backfill.high_rejection_fraction` | `0 < f <= 1` |
| `frontend.zoom_country_below`, `zoom_city_below` | each `0..24`, and `zoom_country_below < zoom_city_below` — rejects an inverted tier order |
| `frontend.no_data_colour`, `marker_stroke_colour`, `empty_basemap_colour`, `chart_line_colour` | each matches `^#[0-9a-fA-F]{6}$` |
| `quality.ranges` | `min < max` per metric, and **exactly** the seven canonical metrics — none missing, none unknown |
| `series.periods` | non-empty, unique names, `window > 0` per entry; `series.default_period` must name one of them |
| `series.default_metric` | must be a canonical metric |
| `upstream.max_payload_bytes` | `> 0` |
| `upstream.url`, `basemap.style_url` | existing rules unchanged: absolute `https`, no userinfo, plain host or host:port, host no longer than `basemap.max_host_length` |
| `listen.trusted_proxy_cidrs` | existing rule unchanged: each entry parses as a CIDR |
| `database.statement_timeouts.*` | non-empty, and parses as a Postgres interval the same way the current constants do |

**Why the `ranges` completeness rule matters more than it looks.** `quality.InRange` looks up
`metricRanges[metric]`; a Go map miss returns the zero `valueRange{0, 0}`, so a *missing* metric does
not fail open or closed — it rejects every reading for that one metric, silently. Requiring exactly
the canonical seven turns a partial config into a startup error rather than one quietly dark metric.

**Failure order,** all before the listener opens, none reachable at runtime:

1. File missing or unreadable → exit, naming the path.
2. Unknown key → exit, naming the key.
3. Missing key → exit, naming the key.
4. Secret key present in the file → exit, refusing it by name.
5. Range violation → exit, naming key, value and rule.

## 8. Environment overrides

One mechanical rule: the key path, uppercased, joined with `_`, prefixed `AIRBG_`.

```
ratelimit.series.per_second  →  AIRBG_RATELIMIT_SERIES_PER_SECOND
quality.mad_scale            →  AIRBG_QUALITY_MAD_SCALE
timeouts.read                →  AIRBG_TIMEOUTS_READ
listen.max_conns             →  AIRBG_LISTEN_MAX_CONNS
```

**This renames eight existing variables:**

| Today | After |
|---|---|
| `AIRBG_METRICS_ADDR` | `AIRBG_LISTEN_METRICS_ADDR` |
| `AIRBG_BASE_URL` | `AIRBG_LISTEN_BASE_URL` |
| `AIRBG_TRUSTED_PROXY_CIDRS` | `AIRBG_LISTEN_TRUSTED_PROXY_CIDRS` |
| `AIRBG_MAX_CONNS` | `AIRBG_LISTEN_MAX_CONNS` |
| `AIRBG_POLL_INTERVAL` | `AIRBG_UPSTREAM_POLL_INTERVAL` |
| `AIRBG_DB_API_CONNS` | `AIRBG_DATABASE_API_CONNS` |
| `AIRBG_DB_COLLECTOR_CONNS` | `AIRBG_DATABASE_COLLECTOR_CONNS` |
| `AIRBG_MAX_DB_INFLIGHT` | `AIRBG_DATABASE_MAX_DB_INFLIGHT` |

`AIRBG_LISTEN_ADDR`, `AIRBG_UPSTREAM_URL` and `AIRBG_BASEMAP_STYLE_URL` already match the derived
name and do not change.

The rename is taken now because nothing is deployed yet — this is the last moment it is free, and a
mechanical rule lets an operator derive any variable name from the file without a lookup table. No
alias layer is added; an alias table would be a second source of truth.

The two secrets keep their exact current names: `AIRBG_DATABASE_URL`, `AIRBG_BASEMAP_KEY`.

Two variables are **not** configuration and stay env-only, outside the schema: `AIRBG_LIVE_TEST`,
which opts a test into hitting the real upstream, and `AIRBG_CONFIG_FILE`, which names the file and
therefore cannot live in it. Neither may appear in `airbg.yaml`; `AIRBG_LIVE_TEST` also stays in
`clearEnv`'s cleared set.

**Overrides apply to scalars only.** `quality.ranges` and `series.periods` are file-only: there is
no sane environment encoding for a nested table, and a half-overridable table is worse than a fixed
one. `listen.trusted_proxy_cidrs` keeps its existing comma-separated string form.

## 9. Wiring

Constants are **deleted**, not shadowed. Each package receives only its group.

| Package | Change |
|---|---|
| `internal/quality` | `Score`, `SpatialCheck`, `InRange` become methods on a `Scorer` built by `NewScorer(cfg.Quality)`. Deletes `minNeighbours`, `madScale`, `madThreshold`, `NeighbourRadiusMetres`, `earthRadiusMetres`, `metricRanges`. `NewHistory` takes `history_depth` from config at its one call site |
| `internal/store` | `New(pool, cfg.Store)`. Deletes `CoverageThreshold` and `freshnessWindow`; `internal/api/overview.go` reads the config value for the `coverage_threshold` field it publishes |
| `internal/ratelimit` | `NewLimiter` takes the shard count. `EnumerationWindow` and the 12-area cap become constructor parameters |
| `internal/api` | `Deps` gains the rate, period and retry-after values. Deletes `seriesRate`, `seriesBucketTTL`, `seriesEvictInterval`, `defaultMaxDBInflight`, the period map, and the literal `Retry-After` strings in `sensors.go` and `series.go` |
| `internal/server` | `Options` gains the timeouts and the bucket TTL pair. Deletes `apiRate`, the five timeout constants, `bucketTTL`, `evictInterval`, `defaultMaxConns` |
| `internal/snapshot` | `DefaultSeriesWindow` and `DefaultSeriesMetric` become fields derived from `series.default_period` and `series.default_metric` |
| `internal/upstream` | `New(url, timeout, maxPayloadBytes)`. Deletes `maxPayloadBytes` |
| `internal/backfill` | Takes `high_rejection_fraction`. Deletes `HighRejectionFraction` |
| `internal/db` | Takes the four statement timeouts. Deletes `AssignStatementTimeout`, `OperatorStatementTimeout`, `SeriesStatementTimeout` and the literal `"15000"` pool parameter |
| `internal/config` | Becomes the YAML loader: raw pointer schema, validation, env overlay, `MinPollInterval` and `maxHostLength` become configured values |
| `internal/web` | Renders `frontend.*` into the existing `data-*` attributes on the island containers |
| `web/src/lib/tier.js` | Reads the zoom boundaries from configuration instead of the literals `9` and `11`; `moveend` debounce reads its interval the same way. No new client mechanism — these ride the existing `data-*` path |
| `web/src/lib/colour.js` | `NO_DATA_COLOUR = '#9ca3af'` becomes a parameter supplied from `frontend.no_data_colour`. `colourFor` gains it as an argument rather than reading a module-level constant, so the module stays pure and testable. Its header comment ("the only literal colours permitted in `web/src`") must be updated, not left asserting a rule the file no longer follows |
| `web/src/islands/map.js` | `circle-stroke-color: '#ffffff'` (line 54) reads `frontend.marker_stroke_colour`; the fallback style's `background-color: '#eef2f5'` (line 230) reads `frontend.empty_basemap_colour` |
| `web/src/islands/chart.js` | the series `stroke: '#2563eb'` (line 59) reads `frontend.chart_line_colour` |
| `cmd/airbg` | Loads the file once, wires each group into its constructor, adds the `validate-config` subcommand |

**Why deletion rather than shadowing.** If `madScale` survived as a fallback constant, a wiring
mistake would silently use the old value and every test would still pass — the configuration would
be decorative. With the constant gone, a package that forgets to thread its settings **does not
compile**. The compiler becomes the completeness check for a 40-key sweep.

## 10. New subcommand

`airbg validate-config` loads and validates the file, prints the resolved values, and exits 0 or 1.
It lets a deploy check the file without starting a server, and shows what the environment overrides
actually resolved to. It shares exactly one code path with `serve` — the same `config.Load` — so it
cannot report a different verdict than a real boot.

## 11. Testing

Go standard library only. The load-bearing property is **inertness**: this phase must change nothing.

1. **Value pinning.** Load the committed `airbg.yaml` and assert every field equals the value its
   deleted constant had, with the expected numbers written literally in the test. Mutation-proven by
   changing a value in the file and quoting the resulting failure.
2. **The committed file is complete.** A test fails if any schema key is absent from `airbg.yaml`,
   so adding a key without adding it to the file breaks CI rather than the release image.
3. **Rejection tests, one per rule in §7**, each asserting the error names the key: zero rate, zero
   burst, zero `mad_scale`, `NaN` `mad_scale`, inverted zoom tiers, a range with `min >= max`, a
   missing metric in `ranges`, an unknown metric in `ranges`, `default_period` naming an absent
   period, `default_metric` not canonical, a non-hex `no_data_colour`, `poll_interval` below
   `min_poll_interval`, `high_rejection_fraction` of `0` and of `1.5`, a secret key present in the
   file, an unknown key, and a missing key.
4. **Environment overrides** via `t.Setenv`, including a test asserting that table keys are *not*
   overridable.
5. **`clearEnv` regrows** to the full variable set, preserving the existing guard that a test does
   not inherit the developer's environment.
6. **Behavioural inertness at the edges.** The existing `internal/api`, `internal/server`,
   `internal/httpx`, `internal/web`, `internal/quality` and `internal/store` tests keep passing.
   Where a test must change because a signature changed, the change is mechanical (threading a
   settings value) and never an assertion change. **Any test whose assertion has to change is a
   signal that behaviour moved, and must be reported rather than edited.**
7. **Theme.** A Go test asserts `app.css` contains no colour literal — no `#rrggbb`, no `#rgb`, no
   `rgb(`/`rgba(`/`hsl(` — so a future edit cannot reintroduce a hardcoded colour outside
   `theme.css`. A second asserts every custom property `app.css` references is defined in
   `theme.css`, catching a rename that would silently fall back to an unstyled default. A Vitest
   test applies the same literal scan to `web/src/`, excluding `__tests__`, so the four `frontend.*`
   colours cannot creep back into the islands. Both scans must be mutation-proven by reintroducing a
   literal and quoting the failure — a regex scan that matches nothing is exactly the inert test this
   project has been bitten by.
8. **Container.** The release image boots with the file at `/etc/airbg/airbg.yaml`, and fails closed
   with the named error when the file is absent.

## 12. Documentation changes

- **`airbg.yaml`** is the documented configuration surface, self-documenting through comments.
- **`.env.example`** shrinks to the two secrets plus a short note that any file key can be
  overridden by the derived `AIRBG_*` name, with two or three worked examples.
- **`README.md`**'s two environment tables are restructured: one short table of secrets and
  deploy-time overrides, and a pointer to `airbg.yaml` for everything else. The old per-variable
  table is not duplicated in both places — that duplication is exactly what drifts.
- Every new key lands in `airbg.yaml`, the README pointer section, and `clearEnv` **in the same
  commit as the code that reads it**.

## 13. Risks

1. **A large mechanical diff across ~12 packages.** Mitigated by deletion-not-shadowing: the
   compiler finds every site. The real risk is an *assertion* quietly changing during the sweep,
   which §11.6 makes a reportable event.
2. **The file becomes required, so a deploy can fail to start.** Deliberate: fail-closed is the
   better failure for a project whose rate limits are its security posture, and the completeness
   test (§11.2) moves the failure from deploy time to CI time. Phase 4 must verify the file is
   present in the image and readable by `nonroot`.
3. **A first new Go dependency.** `gopkg.in/yaml.v3 v3.0.1`, no transitive dependencies. It parses a
   local operator-supplied file, not network input, so its attack surface is the config path rather
   than a request path. `go.sum` gains it; future dependency review starts from a set of four rather
   than three.
4. **The environment-variable rename is a breaking change** for any deployment that exists. None
   does — nothing is pushed and nothing is deployed — which is precisely why it happens now.
5. **`maxHostLength` becoming configurable lets an operator raise it above 253**, violating the DNS
   name limit for a host that will then be concatenated into a CSP header. The existing host-shape
   validation (no `;`, `"`, `'` or space) still runs and is what actually protects the header, so the
   consequence is a hostname that cannot resolve rather than a malformed policy. Recorded as an
   accepted consequence of decision 3.1.
6. **`quality` gaining a `Scorer` type touches the hottest path in the collector.** The change is a
   receiver, not an algorithm; §11.6's inertness requirement covers it, and the existing quality
   tests are the check.
