# airbg.org Phase 3b — Configuration and Theme Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move every hardcoded tunable in the Go server and the frontend into one mandatory, fully-populated, commented `airbg.yaml`, with `AIRBG_*` environment overrides derived mechanically from the YAML key path, leaving runtime behaviour byte-for-byte unchanged.

**Architecture:** Two layers only — the YAML file, then environment overrides. No defaults live in Go code. `internal/config` grows a *raw* schema of pointer fields (so *absent* is distinguishable from *zero*, and zero is the dangerous value for nearly every knob here), decoded with `yaml.Decoder.KnownFields(true)`; a missing key is a startup error, not a silently-defaulted zero. The raw schema is then validated and flattened into a resolved nested `Config` of value types that the rest of the tree consumes. Constants are **deleted, not shadowed**, so the Go compiler is the completeness check for a ~40-key sweep. Browser-side values reach the page as server-rendered `data-*` attributes (CSP forbids inline script), and the CSS palette moves to a committed `internal/web/static/theme.css`.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3 v3.0.1` (the one new Go dependency, explicitly authorised for this module), PostgreSQL 18 + PostGIS + TimescaleDB, Svelte 5 / Vite 8 / Vitest 4 on the frontend. Go tests are stdlib `testing` only; JS tests are Vitest, pure logic only.

## Global Constraints

- Module path is exactly `airbg.org`. Go 1.26.
- Permitted third-party Go dependencies, now five: `github.com/jackc/pgx/v5 v5.10.0`, `github.com/pressly/goose/v3 v3.27.3`, `github.com/testcontainers/testcontainers-go v0.44.0`, `gopkg.in/yaml.v3 v3.0.1`. Adding any other module is out of scope for this phase.
- Go tests use stdlib `testing` ONLY. Hand-written `if got != want { t.Errorf("X = %v, want %v", got, want) }`, `t.Run` subtests, `t.Setenv`. No assertion library, no mocking framework.
- **The inert-mutation rule.** Twelve Phase 2 tests once passed while inert. Every load-bearing property a task claims to test must be proven by breaking the **production** code and quoting the real failure output. Mutating *test* code proves nothing.
- **Never revert a mutation with `git checkout`.** Copy aside first (`cp x.go /tmp/x.go.orig`), restore from the copy, then confirm with `git diff` that the tree is clean.
- `git log` in this repo needs `--no-show-signature` (commits are gitsign-signed).
- **CSP is `default-src 'self'; style-src 'self'; …` with no `'unsafe-inline'` and no `'unsafe-eval'`, ever.** Consequences that bind this plan: browser config arrives as server-rendered `data-*` attributes; JS uses `textContent`, never `innerHTML`; the theme is a stylesheet *file*, never an inline `<style>` block or a `style=` attribute.
- **Secrets are env-only.** `AIRBG_DATABASE_URL` and `AIRBG_BASEMAP_KEY` must never appear in `airbg.yaml`, and their *presence* in the file is a hard startup rejection, not a silent ignore.
- No secrets in the repo or the container image.
- `CLAUDE.md` must never be staged or committed, not even with `git add -f`. `.claude/` and `.superpowers/` are gitignored.
- No `Co-Authored-By: Claude` trailer and no "Generated with Claude Code" line in any commit or PR body.
- `www-root/` (the legacy PHP app) must never be modified. `docker-compose.yml` is local-development-only.
- **Behaviour must be unchanged.** This phase moves values; it does not retune them. Every value written into `airbg.yaml` must equal the constant it replaces. Inertness is the primary test target (see Task 18).
- Band tables (EAQI / WHO / EU colour bands) stay in code, in `internal/api/scales.go`. They are legislative tables, not operator tunables.
- `go test ./...` starts testcontainers and takes ~2–8 minutes (db 97s, store 104s, area 81s). Only `internal/server/e2e_test.go` carries `//go:build integration`.
- Comments that explain *why* a value is what it is must survive the move — into the YAML file as comments, or stay attached in code. Named in the spec as mandatory survivors: the pressure-range "do not 'tidy' this back toward sea level" comment, the `DefaultSeriesWindow` "must equal the window api.parsePeriod derives from" comment, and the `httpx.CSPValue` no-inline-script comment.

## Environment Override Rule

One mechanical rule, no exceptions and no lookup table: the environment variable for a YAML key is `AIRBG_` + the full key path, uppercased, with `.` replaced by `_`.

```
listen.addr                      → AIRBG_LISTEN_ADDR
listen.metrics_addr              → AIRBG_LISTEN_METRICS_ADDR
upstream.poll_interval           → AIRBG_UPSTREAM_POLL_INTERVAL
quality.ranges.pressure.min      → AIRBG_QUALITY_RANGES_PRESSURE_MIN
```

Two names are **not** derived from the file, because they are secrets that must never be in it:

```
AIRBG_DATABASE_URL     → database connection string
AIRBG_BASEMAP_KEY      → basemap style key substituted into basemap.style_url's {key}
```

Eight existing variables are renamed by this rule. The old names stop working; that is intentional and is documented in Task 18.

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

`AIRBG_LIVE_TEST` is a test-only switch, not configuration; it is untouched.

## File Structure

**Created:**

| path | responsibility |
| --- | --- |
| `airbg.yaml` | The committed, fully-populated, commented configuration file. Repo root. Ships in the image. |
| `internal/config/schema.go` | The raw pointer schema — one struct per YAML group, every field a pointer, every field tagged. Nothing else. |
| `internal/config/duration.go` | `Duration`, a `time.Duration` wrapper with `UnmarshalYAML`. yaml.v3 does not parse `"5m"` into a `time.Duration` on its own. |
| `internal/config/load.go` | File read, strict decode, secret rejection, reflection-based env overlay, missing-key collection. |
| `internal/config/resolve.go` | Raw pointer schema → resolved value-typed nested `Config`. |
| `internal/config/validate.go` | Range and cross-field validation. Every rejection carries the key path and the offending value. |
| `internal/config/schema_test.go` | Round-trip: the committed `airbg.yaml` decodes, resolves and validates with zero errors. |
| `internal/config/load_test.go` | Missing key, unknown key, secret-in-file, env overlay, duration parsing. |
| `internal/config/validate_test.go` | One subtest per validation rule, each mutation-proven. |
| `internal/web/static/theme.css` | Only the `:root` palette. No selectors, no layout. |
| `cmd/airbg/validate.go` | The `validate-config` subcommand. |

**Modified (the delete-not-shadow sweep):**

`internal/config/config.go`, `cmd/airbg/main.go`, `internal/quality/{score,spatial,ranges,history}.go`, `internal/store/{aggregate,store}.go`, `internal/ratelimit/{bucket,enumerate}.go`, `internal/db/{db,timeout}.go`, `internal/upstream/client.go`, `internal/backfill/backfill.go`, `internal/api/{router,series,sensors}.go`, `internal/server/server.go`, `internal/snapshot/snapshot.go`, `internal/web/static/app.css`, `internal/web/*.go` (the `data-*` render sites), `web/src/lib/{colour,tier}.js`, `web/src/islands/{map,chart}.js`, `Dockerfile`, `README.md`, `docs/configuration.md`.

## Task Ordering Rationale

Tasks 1–7 are purely **additive**: the whole new loader lands beside the existing `config.Load()` without touching it, so the repo stays green throughout and each piece — duration decoding, schema, file, strict read, env overlay, resolve, validate — is reviewable on its own. Task 8 is the cutover: the single commit where `main.go` switches loaders and the eight environment variables are renamed. Tasks 9–15 are the per-package delete-not-shadow sweep, one package per task, each one driven by the compile errors that deleting a constant produces. Tasks 16–18 are the frontend, the `validate-config` subcommand, and the documentation plus the inertness proof.

A note on what does **not** become configuration, so no implementer has to re-litigate it: validation *mechanics* (the host-name regexp, the 253-byte host length limit), the EAQI/WHO/EU band tables in `internal/api/scales.go`, and the `cachePublic`/`cachePrivate` strings in `internal/api/router.go` all stay in code. The last of these is a security control — which responses may be stored by a shared cache — not an operator tunable.

---

### Task 1: Duration decoding

**Files:**
- Create: `internal/config/duration.go`
- Test: `internal/config/duration_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Duration time.Duration` with `func (d *Duration) UnmarshalYAML(node *yaml.Node) error` and `func (d Duration) Std() time.Duration`. Every duration field in Task 2's schema is `*Duration`.

- [ ] **Step 1: Add the dependency**

```bash
go get gopkg.in/yaml.v3@v3.0.1
go mod tidy
```

Expected: `go.mod` gains `gopkg.in/yaml.v3 v3.0.1` in the `require` block. If `go mod tidy` proposes any *other* new module, stop — that violates a Global Constraint.

- [ ] **Step 2: Write the failing test**

Create `internal/config/duration_test.go`:

```go
package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDurationUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"minutes", `"5m"`, 5 * time.Minute},
		{"seconds", `"150s"`, 150 * time.Second},
		{"hours", `"2h"`, 2 * time.Hour},
		{"compound", `"10min"`, 10 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			if err := yaml.Unmarshal([]byte(tt.in), &d); err != nil {
				t.Fatalf("Unmarshal(%s) error = %v, want nil", tt.in, err)
			}
			if d.Std() != tt.want {
				t.Errorf("Unmarshal(%s) = %v, want %v", tt.in, d.Std(), tt.want)
			}
		})
	}
}

// A bare integer is the failure mode this type exists to prevent: yaml.v3 would
// decode 300000000000 into a plain time.Duration field without complaint, and an
// operator writing `poll_interval: 300` would silently get 300 nanoseconds.
func TestDurationRejectsBareNumber(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte(`300`), &d)
	if err == nil {
		t.Fatalf("Unmarshal(300) error = nil, want an error; got duration %v", d.Std())
	}
}

func TestDurationRejectsGarbage(t *testing.T) {
	var d Duration
	err := yaml.Unmarshal([]byte(`"5 fortnights"`), &d)
	if err == nil {
		t.Fatalf("Unmarshal(\"5 fortnights\") error = nil, want an error")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestDuration -v`
Expected: FAIL to build — `undefined: Duration`.

- [ ] **Step 4: Write the implementation**

Create `internal/config/duration.go`:

```go
package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration because yaml.v3 has no special case for it.
// time.Duration is an int64 alias, so a bare time.Duration field silently
// accepts a nanosecond count and rejects "5m" — meaning `poll_interval: 300`
// would decode to 300 nanoseconds and poll upstream in a hot loop. Every
// duration in airbg.yaml is written the way an operator would write it, and
// this type is what makes that legal and makes the alternative an error.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf(`must be a quoted duration string such as "5m" or "150s", not %s`, node.Value)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("%q is not a valid duration: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std converts back for the call sites, all of which want a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/config/ -run TestDuration -v`
Expected: PASS, all three functions, four subtests under `TestDurationUnmarshalYAML`.

- [ ] **Step 6: Mutation-prove the bare-number rejection**

The inert-mutation rule applies: `TestDurationRejectsBareNumber` is the load-bearing test here, so break the production code and quote the failure.

```bash
cp internal/config/duration.go /tmp/duration.go.orig
```

Change the `node.Decode(&s)` error branch in `duration.go` to fall through instead of returning — i.e. replace the `return fmt.Errorf(...)` line with `var n int64; if e := node.Decode(&n); e == nil { *d = Duration(n); return nil }; return err`.

Run: `go test ./internal/config/ -run TestDurationRejectsBareNumber -v`
Expected: FAIL, with a message of the form `Unmarshal(300) error = nil, want an error; got duration 300ns`. Paste the real output into the task report.

Restore and confirm:

```bash
cp /tmp/duration.go.orig internal/config/duration.go
git diff --stat internal/config/duration.go   # expected: no output
```

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/config/duration.go internal/config/duration_test.go
git commit -m "config: add a YAML-decodable Duration type"
```

---

### Task 2: The raw pointer schema

**Files:**
- Create: `internal/config/schema.go`
- Test: `internal/config/schema_test.go`

**Interfaces:**
- Consumes: `Duration` from Task 1.
- Produces: `type raw struct` and its twelve group structs, all unexported. Every leaf field is a pointer. Task 3's `airbg.yaml` must key exactly against these tags; Task 4 decodes into `raw`; Task 5's reflection walk derives environment names from these `yaml:` tags; Task 6 reads them.

- [ ] **Step 1: Write the schema**

Create `internal/config/schema.go`. Every leaf is a pointer so that *absent* is distinguishable from *zero* — there are no defaults in code, so an absent key must be an error rather than a zero value.

```go
package config

// raw is the on-disk shape of airbg.yaml. Every leaf field is a pointer: this
// package has no defaults, so "the operator omitted this key" must be
// distinguishable from "the operator wrote 0". Zero is the dangerous value for
// most of these — max_conns: 0 is an unlimited public listener, and
// coverage_threshold: 0 paints a single sensor as a whole oblast.
//
// The yaml tags are load-bearing twice over: they name the file's keys, and the
// environment overlay derives AIRBG_* names from the tag path (see envName).
type raw struct {
	Listen    *rawListen    `yaml:"listen"`
	Timeouts  *rawTimeouts  `yaml:"timeouts"`
	Database  *rawDatabase  `yaml:"database"`
	RateLimit *rawRateLimit `yaml:"ratelimit"`
	Cache     *rawCache     `yaml:"cache"`
	Upstream  *rawUpstream  `yaml:"upstream"`
	Store     *rawStore     `yaml:"store"`
	Series    *rawSeries    `yaml:"series"`
	Quality   *rawQuality   `yaml:"quality"`
	Backfill  *rawBackfill  `yaml:"backfill"`
	Frontend  *rawFrontend  `yaml:"frontend"`
	Basemap   *rawBasemap   `yaml:"basemap"`
}

type rawListen struct {
	Addr              *string   `yaml:"addr"`
	MetricsAddr       *string   `yaml:"metrics_addr"`
	BaseURL           *string   `yaml:"base_url"`
	MaxConns          *int32    `yaml:"max_conns"`
	TrustedProxyCIDRs *[]string `yaml:"trusted_proxy_cidrs"`
	CSP               *string   `yaml:"csp"`
	PermissionsPolicy *string   `yaml:"permissions_policy"`
}

type rawTimeouts struct {
	ReadHeader    *Duration `yaml:"read_header"`
	Read          *Duration `yaml:"read"`
	Write         *Duration `yaml:"write"`
	Idle          *Duration `yaml:"idle"`
	ShutdownGrace *Duration `yaml:"shutdown_grace"`
}

type rawDatabase struct {
	APIConns          *int32                `yaml:"api_conns"`
	CollectorConns    *int32                `yaml:"collector_conns"`
	MaxInflight       *int32                `yaml:"max_inflight"`
	StatementTimeouts *rawStatementTimeouts `yaml:"statement_timeouts"`
}

type rawStatementTimeouts struct {
	Default  *Duration `yaml:"default"`
	Assign   *Duration `yaml:"assign"`
	Operator *Duration `yaml:"operator"`
	Series   *Duration `yaml:"series"`
}

type rawRateLimit struct {
	API        *rawBucket    `yaml:"api"`
	Series     *rawBucket    `yaml:"series"`
	Enumerate  *rawEnumerate `yaml:"enumerate"`
	ShardCount *int          `yaml:"shard_count"`
}

type rawBucket struct {
	PerSecond     *float64  `yaml:"per_second"`
	Burst         *float64  `yaml:"burst"`
	TTL           *Duration `yaml:"ttl"`
	EvictInterval *Duration `yaml:"evict_interval"`
	RetryAfter    *Duration `yaml:"retry_after"`
}

type rawEnumerate struct {
	AreasPerWindow   *int      `yaml:"areas_per_window"`
	SensorsPerWindow *int      `yaml:"sensors_per_window"`
	Window           *Duration `yaml:"window"`
	RetryAfter       *Duration `yaml:"retry_after"`
}

type rawCache struct {
	DataMaxAge   *Duration `yaml:"data_max_age"`
	ScalesMaxAge *Duration `yaml:"scales_max_age"`
}

type rawUpstream struct {
	URL             *string   `yaml:"url"`
	RequestTimeout  *Duration `yaml:"request_timeout"`
	PollInterval    *Duration `yaml:"poll_interval"`
	MinPollInterval *Duration `yaml:"min_poll_interval"`
	MaxPayloadBytes *int64    `yaml:"max_payload_bytes"`
}

type rawStore struct {
	CoverageThreshold *int      `yaml:"coverage_threshold"`
	FreshnessWindow   *Duration `yaml:"freshness_window"`
}

type rawSeries struct {
	DefaultMetric *string      `yaml:"default_metric"`
	DefaultWindow *Duration    `yaml:"default_window"`
	Periods       []rawPeriod  `yaml:"periods"`
}

// rawPeriod is a list entry, not a map, so that ordering is stable in the file
// and a duplicate name is detectable. Each period carries its own cache
// lifetime: internal/api/series.go's seriesMaxAge is an explicit table, not a
// formula, because four values each need their own justification.
type rawPeriod struct {
	Name   *string   `yaml:"name"`
	Window *Duration `yaml:"window"`
	Hourly *bool     `yaml:"hourly"`
	MaxAge *Duration `yaml:"max_age"`
}

type rawQuality struct {
	MinNeighbours         *int       `yaml:"min_neighbours"`
	MADScale              *float64   `yaml:"mad_scale"`
	MADThreshold          *float64   `yaml:"mad_threshold"`
	NeighbourRadiusMetres *float64   `yaml:"neighbour_radius_metres"`
	EarthRadiusMetres     *float64   `yaml:"earth_radius_metres"`
	HistoryDepth          *int       `yaml:"history_depth"`
	Ranges                *rawRanges `yaml:"ranges"`
}

type rawRanges struct {
	P1          *rawRange `yaml:"P1"`
	P2          *rawRange `yaml:"P2"`
	Temperature *rawRange `yaml:"temperature"`
	Humidity    *rawRange `yaml:"humidity"`
	Pressure    *rawRange `yaml:"pressure"`
	NoiseLAeq   *rawRange `yaml:"noise_LAeq"`
	NoiseLAMax  *rawRange `yaml:"noise_LA_max"`
}

type rawRange struct {
	Min *float64 `yaml:"min"`
	Max *float64 `yaml:"max"`
}

type rawBackfill struct {
	HighRejectionFraction *float64 `yaml:"high_rejection_fraction"`
}

type rawFrontend struct {
	NoDataColour       *string `yaml:"no_data_colour"`
	MarkerStrokeColour *string `yaml:"marker_stroke_colour"`
	EmptyBasemapColour *string `yaml:"empty_basemap_colour"`
	ChartLineColour    *string `yaml:"chart_line_colour"`
	ZoomCity           *int    `yaml:"zoom_city"`
	ZoomSensor         *int    `yaml:"zoom_sensor"`
}

type rawBasemap struct {
	StyleURL *string `yaml:"style_url"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/config/ && go vet ./internal/config/`
Expected: no output. `gofmt -l internal/config/` must also print nothing.

- [ ] **Step 3: Commit**

```bash
git add internal/config/schema.go
git commit -m "config: add the raw YAML schema"
```

---

### Task 3: The committed airbg.yaml

**Files:**
- Create: `airbg.yaml`
- Test: `internal/config/schema_test.go`

**Interfaces:**
- Consumes: the `raw` schema from Task 2.
- Produces: `airbg.yaml` at the repo root, and `testdataPath`-free tests that read the real committed file (not a fixture copy — a fixture would let the shipped file rot).

Every value below equals the constant it replaces. Do not round, retune, or "tidy" anything: behaviour must be unchanged, and Task 18 proves it.

- [ ] **Step 1: Write the file**

Create `airbg.yaml` at the repo root:

```yaml
# airbg.org configuration.
#
# This file is mandatory and fully populated: this project keeps no defaults in
# code, so every key below must be present or the server refuses to start. A
# missing key is an error, never a zero value — for most of these, zero is the
# dangerous setting.
#
# Any key can be overridden by an environment variable named AIRBG_ + the key
# path, uppercased, with dots as underscores:
#   listen.metrics_addr         -> AIRBG_LISTEN_METRICS_ADDR
#   quality.ranges.pressure.min -> AIRBG_QUALITY_RANGES_PRESSURE_MIN
#
# Two settings are deliberately absent and env-only, because they are secrets
# and this file is committed:
#   AIRBG_DATABASE_URL  - the PostgreSQL connection string
#   AIRBG_BASEMAP_KEY   - substituted into basemap.style_url's {key} placeholder
# Writing either of them into this file is a startup error, not an ignore.

listen:
  # Loopback by default and on purpose. The origin must be unreachable except
  # through the CDN: CF-Connecting-IP is the only rate-limit bucket key, so a
  # scraper that reaches the origin directly bypasses every limiter in one hop.
  # Do not "fix" a container that answers nothing by binding 0.0.0.0.
  addr: "127.0.0.1:8080"
  # The private listener carrying /metrics. Must differ from addr: sharing them
  # publishes the counters that show an attacker whether their probing is being
  # rate limited.
  metrics_addr: "127.0.0.1:9090"
  # Absolute origin used to build canonical and alternate-language URLs.
  base_url: "http://localhost:8080"
  # Hard ceiling on concurrent connections to the public listener. 0 would mean
  # unlimited, which is why absent is an error rather than a default.
  max_conns: 4096
  # CIDRs whose X-Forwarded-For / CF-Connecting-IP headers are believed. Empty
  # means trust nothing and bucket on the direct peer address.
  trusted_proxy_cidrs: []
  # No 'unsafe-inline' and no 'unsafe-eval', ever: the islands ship as external
  # modules and the map styles as external JSON, so nothing needs them, and an
  # inline-script allowance is the single most common way a CSP stops
  # mitigating XSS. img-src allows data: for MapLibre's canvas sprites and
  # blob: for its worker-produced tiles; worker-src blob: is required by
  # MapLibre GL JS, which builds its workers from blobs. Validation rejects
  # either unsafe directive, so this key cannot be used to disable the policy.
  csp: >-
    default-src 'self'; script-src 'self'; style-src 'self';
    img-src 'self' data: blob:; font-src 'self'; connect-src 'self';
    worker-src 'self' blob:; object-src 'none'; base-uri 'none';
    form-action 'none'; frame-ancestors 'none'
  # Denies every browser capability the site does not use. The threat is the
  # frontend bundle itself: hundreds of transitive npm packages served
  # same-origin under a CSP that trusts 'self'. geolocation stays denied until
  # the "sensors near me" button exists (Phase 3c) — an allowance nobody uses
  # is an allowance nobody chose.
  permissions_policy: "geolocation=(), camera=(), microphone=(), payment=(), usb=()"

timeouts:
  read_header: "5s"
  read: "10s"
  write: "30s"
  idle: "60s"
  # How long in-flight requests get to finish after SIGTERM.
  shutdown_grace: "15s"

database:
  # Two pools: the API pool must not be starved by the collector's writes.
  api_conns: 8
  collector_conns: 4
  # Non-blocking admission semaphore in front of the database. Requests over
  # this limit are shed immediately rather than queued.
  max_inflight: 16
  statement_timeouts:
    # Applied to every connection as a RuntimeParam.
    default: "15s"
    # Area assignment walks the whole sensor table.
    assign: "60s"
    # Operator-triggered maintenance, run by hand, allowed to be slow.
    operator: "10min"
    # The tightest one, and the one that matters: /series is the most
    # expensive public query, so it is scoped well below the default.
    series: "5s"

ratelimit:
  api:
    per_second: 10
    burst: 60
    ttl: "30m"
    evict_interval: "5m"
    retry_after: "2s"
  # The series routes are the expensive ones and get their own, much tighter
  # bucket. Worst case is one series request per page load.
  series:
    per_second: 1
    burst: 10
    ttl: "30m"
    evict_interval: "5m"
    retry_after: "2s"
  # Breadth limiting: not about request rate but about how much of the dataset
  # one client can enumerate. Anti-extraction here is tiering plus rate
  # limiting, not authentication.
  enumerate:
    # Bulgaria has 28 oblasti and comparing them is the site's obvious use, so
    # this is the knob most likely to need raising in production.
    areas_per_window: 12
    sensors_per_window: 40
    window: "1h"
    retry_after: "900s"
  # Bucket shards. Reduces lock contention; purely a performance knob.
  shard_count: 32

cache:
  # Deliberately half the poll interval: a client that caches for longer than
  # one ingest cycle can show a reading that has already been superseded.
  # Validation enforces data_max_age <= upstream.poll_interval / 2, so this
  # coupling is checked rather than left as a comment for a human to maintain.
  data_max_age: "150s"
  # The band tables change when legislation changes, which is to say never.
  scales_max_age: "86400s"

upstream:
  url: "https://data.sensor.community/airrohr/v1/filter/country=BG"
  request_timeout: "30s"
  poll_interval: "5m"
  # Floor on poll_interval. Polling a volunteer-run public API faster than this
  # is abusive, so the floor is enforced rather than advisory.
  min_poll_interval: "30s"
  # 64 MiB. A response larger than this is a bug or an attack, not data.
  max_payload_bytes: 67108864

store:
  # Minimum sensors before an area gets a value at all. Below it the area is
  # painted "no data" — one sensor must never be presented as an oblast.
  coverage_threshold: 3
  # A reading older than this does not count toward an area's current value.
  freshness_window: "2h"

series:
  # Must match the first entry in periods below: the snapshot serves this
  # window without touching the database, and api.parsePeriod must derive the
  # same window from the default period name.
  default_metric: "P2"
  default_window: "24h"
  # The hourly flag is not a performance hint, it is a correctness
  # requirement. Raw readings are retained for 30 days, so a 1-year window
  # against the raw table silently returns the last 30 days under a "1 year"
  # label — a chart that is wrong without being empty.
  #
  # max_age is per period because each value needs its own justification: a
  # 24h chart of raw readings is a live view whose right edge moves every few
  # minutes, while a 1-year chart is hourly rollups where re-running the
  # heaviest query in the service repaints a single pixel.
  periods:
    - name: "24h"
      window: "24h"
      hourly: false
      max_age: "150s"
    - name: "7d"
      window: "168h"
      hourly: false
      max_age: "600s"
    - name: "30d"
      window: "720h"
      hourly: false
      max_age: "1800s"
    - name: "1y"
      window: "8760h"
      hourly: true
      max_age: "10800s"

quality:
  # Spatial outlier detection needs at least this many neighbours before its
  # verdict means anything.
  min_neighbours: 3
  # 1.4826 makes the median absolute deviation a consistent estimator of the
  # standard deviation for normally distributed data. This is a mathematical
  # constant, not a tuning parameter — changing it changes what the threshold
  # below means.
  mad_scale: 1.4826
  mad_threshold: 3.5
  neighbour_radius_metres: 15000.0
  # Mean Earth radius, used for the haversine distance. Also not a tunable.
  earth_radius_metres: 6371000.0
  # Readings kept per sensor for the rejection-rate history.
  history_depth: 12
  # Plausibility gates. A reading outside its range is rejected before it can
  # reach an average. These are instrument ranges, not air-quality thresholds.
  ranges:
    P1:
      min: 0.0
      max: 1000.0
    P2:
      min: 0.0
      max: 1000.0
    temperature:
      min: -40.0
      max: 60.0
    humidity:
      min: 0.0
      max: 100.0
    # Bulgaria's inhabited altitudes run past 1600 m, where station pressure is
    # genuinely near 650 hPa. Do not "tidy" this back toward sea level: doing
    # so silently discards every reading from the mountain sensors.
    pressure:
      min: 650.0
      max: 1100.0
    noise_LAeq:
      min: 25.0
      max: 120.0
    noise_LA_max:
      min: 25.0
      max: 120.0

backfill:
  # A backfill batch that rejects more than this fraction of its rows is
  # reported as suspect rather than silently accepted.
  high_rejection_fraction: 0.5

frontend:
  # These four are paint values handed to WebGL layers and a canvas, so no CSS
  # rule can ever reach them — which is why they live here and not in
  # theme.css. The boundary is mechanical: if CSS can style it, it belongs in
  # internal/web/static/theme.css; if JS passes it to a canvas or a GL layer,
  # it belongs here. Band colours are NOT here — they come from
  # /api/v1/scales, so a legislative change stays a one-file server edit.
  #
  # Neutral grey, deliberately not a band colour: an area below the coverage
  # threshold must not be paintable as clean air.
  no_data_colour: "#9ca3af"
  marker_stroke_colour: "#ffffff"
  empty_basemap_colour: "#eef2f5"
  chart_line_colour: "#2563eb"
  # Zoom thresholds for the country -> city -> sensor tiering. Below zoom_city
  # the map shows country-level aggregates; below zoom_sensor, city-level.
  zoom_city: 9
  zoom_sensor: 11

basemap:
  # {key} is substituted from AIRBG_BASEMAP_KEY at startup. Userinfo in this
  # URL is rejected: it would put a credential in a value the browser fetches.
  style_url: "https://tiles.example.org/styles/basic/style.json?key={key}"
```

- [ ] **Step 2: Write the round-trip test**

Create `internal/config/schema_test.go`. It reads the **real committed file**, not a fixture — a fixture copy is how a shipped config file rots.

```go
package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// repoConfigPath locates the committed airbg.yaml from inside the package
// directory. Tests run with the package directory as CWD, so the repo root is
// two levels up from internal/config.
func repoConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "airbg.yaml")
}

// The committed file must decode strictly against the schema: no unknown keys,
// every value the right type, every duration a parseable string. If this fails,
// the shipped configuration is broken for every operator.
//
// This test reads the real committed file rather than a fixture on purpose. A
// fixture copy is how a shipped config file rots.
func TestCommittedConfigDecodesStrictly(t *testing.T) {
	data, err := os.ReadFile(repoConfigPath(t))
	if err != nil {
		t.Fatalf("ReadFile(airbg.yaml) error = %v, want nil", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var r raw
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("Decode(airbg.yaml) error = %v, want nil", err)
	}
	// Spot-check one leaf per depth so a whole group silently decoding to nil
	// cannot pass. Full completeness is Task 4's missingKeys walk.
	if r.Listen == nil || r.Listen.Addr == nil {
		t.Fatal("listen.addr decoded to nil, want a value")
	}
	if got, want := *r.Listen.Addr, "127.0.0.1:8080"; got != want {
		t.Errorf("listen.addr = %q, want %q", got, want)
	}
	if r.Quality == nil || r.Quality.Ranges == nil || r.Quality.Ranges.Pressure == nil {
		t.Fatal("quality.ranges.pressure decoded to nil, want a value")
	}
	if got, want := *r.Quality.Ranges.Pressure.Min, 650.0; got != want {
		t.Errorf("quality.ranges.pressure.min = %v, want %v", got, want)
	}
	if len(r.Series.Periods) != 4 {
		t.Errorf("len(series.periods) = %d, want 4", len(r.Series.Periods))
	}
}
```

- [ ] **Step 3: Run the test to verify it passes**

Run: `go test ./internal/config/ -run TestCommittedConfigDecodesStrictly -v`
Expected: PASS.

- [ ] **Step 4: Mutation-prove strictness**

```bash
cp airbg.yaml /tmp/airbg.yaml.orig
```

Add a line `addr_typo: "x"` under `listen:` in `airbg.yaml`.

Run: `go test ./internal/config/ -run TestCommittedConfigDecodesStrictly -v`
Expected: FAIL, with `field addr_typo not found in type config.rawListen`. Paste the real output into the task report — this is what proves `KnownFields(true)` is actually on, and a typo'd key is what it would otherwise silently ignore.

Restore and confirm:

```bash
cp /tmp/airbg.yaml.orig airbg.yaml
git diff --stat airbg.yaml   # expected: no output
```

- [ ] **Step 5: Commit**

```bash
git add airbg.yaml internal/config/schema_test.go
git commit -m "config: add the committed airbg.yaml"
```

---

### Task 4: Strict file reading, secret rejection, missing-key collection

**Files:**
- Create: `internal/config/load.go`
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: `raw` (Task 2), `airbg.yaml` (Task 3).
- Produces:
  - `func decodeStrict(data []byte, r *raw) error`
  - `func rejectSecrets(data []byte) error`
  - `func missingKeys(v reflect.Value, prefix string) []string`
  - `func envName(path string) string`
  - `func readRaw(path string) (*raw, error)` — read, reject secrets, decode strictly, collect missing keys; returns one error listing every missing key rather than the first.
  - `const PathEnv = "AIRBG_CONFIG"` — where the file path comes from. There is no default path: an unset `AIRBG_CONFIG` is an error naming the variable, because a silently-guessed path is a default in disguise.

- [ ] **Step 1: Write the failing test**

Create `internal/config/load_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeTemp puts a config body in a temp file and returns its path.
func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "airbg.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	return path
}

// A key present in the schema but absent from the file must be an error naming
// that key. This is the whole point of the pointer schema: with no defaults in
// code, an absent key that decoded to zero would give an unlimited listener or a
// one-sensor oblast.
func TestReadRawReportsMissingKeys(t *testing.T) {
	path := writeTemp(t, "listen:\n  addr: \"127.0.0.1:8080\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want an error listing missing keys")
	}
	for _, want := range []string{"listen.metrics_addr", "quality.ranges.pressure.min", "series.periods"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention missing key %q", err, want)
		}
	}
}

// The error must list every missing key at once. An operator fixing a 40-key
// file one restart at a time is a loader bug, not an operator problem.
func TestReadRawListsAllMissingKeysAtOnce(t *testing.T) {
	path := writeTemp(t, "listen:\n  addr: \"127.0.0.1:8080\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want an error")
	}
	if n := strings.Count(err.Error(), "\n"); n < 20 {
		t.Errorf("error reports %d lines, want at least 20 missing keys listed together:\n%s", n+1, err)
	}
}

func TestReadRawRejectsUnknownKey(t *testing.T) {
	path := writeTemp(t, "listen:\n  addr_typo: \"x\"\n")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want an unknown-field error")
	}
	if !strings.Contains(err.Error(), "addr_typo") {
		t.Errorf("error %q does not name the unknown key addr_typo", err)
	}
}

// Secrets must be rejected on sight, not ignored. An ignored database_url in a
// committed file is a credential in git that appears to be in use.
func TestRejectSecrets(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"database_url at root", "database_url: \"postgres://u:p@h/db\"\n"},
		{"url under database", "database:\n  url: \"postgres://u:p@h/db\"\n"},
		{"dsn", "database:\n  dsn: \"postgres://u:p@h/db\"\n"},
		{"password", "database:\n  password: \"hunter2\"\n"},
		{"basemap key", "basemap:\n  key: \"abc123\"\n"},
		{"basemap_key at root", "basemap_key: \"abc123\"\n"},
		{"api_key", "basemap:\n  api_key: \"abc123\"\n"},
		{"token", "upstream:\n  token: \"abc123\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectSecrets([]byte(tt.body))
			if err == nil {
				t.Fatalf("rejectSecrets(%q) error = nil, want a rejection", tt.body)
			}
			if !strings.Contains(err.Error(), "environment") {
				t.Errorf("error %q should tell the operator to use an environment variable", err)
			}
		})
	}
}

// The committed file must not trip the secret scanner.
func TestRejectSecretsAllowsCommittedFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if err := rejectSecrets(data); err != nil {
		t.Errorf("rejectSecrets(airbg.yaml) error = %v, want nil", err)
	}
}

func TestEnvName(t *testing.T) {
	tests := []struct{ path, want string }{
		{"listen.addr", "AIRBG_LISTEN_ADDR"},
		{"listen.metrics_addr", "AIRBG_LISTEN_METRICS_ADDR"},
		{"upstream.poll_interval", "AIRBG_UPSTREAM_POLL_INTERVAL"},
		{"quality.ranges.pressure.min", "AIRBG_QUALITY_RANGES_PRESSURE_MIN"},
		{"quality.ranges.noise_LAeq.max", "AIRBG_QUALITY_RANGES_NOISE_LAEQ_MAX"},
	}
	for _, tt := range tests {
		if got := envName(tt.path); got != tt.want {
			t.Errorf("envName(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// The full committed file must produce no missing keys. This is the guard that
// catches a schema field added without a corresponding file key.
func TestCommittedConfigHasEveryKey(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw(airbg.yaml) error = %v, want nil", err)
	}
	if missing := missingKeys(reflect.ValueOf(r).Elem(), ""); len(missing) != 0 {
		t.Errorf("airbg.yaml is missing %d keys: %v", len(missing), missing)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run 'TestReadRaw|TestRejectSecrets|TestEnvName|TestCommittedConfigHasEveryKey' -v`
Expected: build failure — `undefined: readRaw`, `undefined: rejectSecrets`, `undefined: envName`, `undefined: missingKeys`.

- [ ] **Step 3: Write the implementation**

Create `internal/config/load.go`:

```go
package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// PathEnv names the environment variable holding the config file path. There is
// deliberately no default: guessing a path is a default in disguise, and this
// project keeps no defaults in code.
const PathEnv = "AIRBG_CONFIG"

// secretKeys are key names that must never appear in the committed file, at any
// depth. Rejecting them is not tidiness: an ignored credential in a committed
// file is a credential in git that looks like it is in use.
var secretKeys = map[string]string{
	"database_url": "AIRBG_DATABASE_URL",
	"dsn":          "AIRBG_DATABASE_URL",
	"password":     "AIRBG_DATABASE_URL",
	"basemap_key":  "AIRBG_BASEMAP_KEY",
	"key":          "AIRBG_BASEMAP_KEY",
	"api_key":      "AIRBG_BASEMAP_KEY",
	"secret":       "an environment variable",
	"token":        "an environment variable",
}

// "url" is legal under upstream and basemap but not under database, so it is
// checked by full path rather than by name.
var secretPaths = map[string]string{
	"database.url": "AIRBG_DATABASE_URL",
}

func envName(path string) string {
	return "AIRBG_" + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}

func decodeStrict(data []byte, r *raw) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	// Without this, a typo'd key is silently ignored and the value it was meant
	// to set stays absent — which, with no defaults, becomes a missing-key error
	// pointing at the wrong thing.
	dec.KnownFields(true)
	if err := dec.Decode(r); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

func rejectSecrets(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var found []string
	walkKeys(&root, "", func(path, key string) {
		if env, ok := secretPaths[path]; ok {
			found = append(found, fmt.Sprintf("%s (set %s in the environment instead)", path, env))
			return
		}
		if env, ok := secretKeys[strings.ToLower(key)]; ok {
			found = append(found, fmt.Sprintf("%s (set %s in the environment instead)", path, env))
		}
	})
	if len(found) > 0 {
		sort.Strings(found)
		return fmt.Errorf("config: secrets must never be written to the config file; remove:\n  %s",
			strings.Join(found, "\n  "))
	}
	return nil
}

// walkKeys visits every mapping key in the document with its dotted path.
func walkKeys(n *yaml.Node, prefix string, fn func(path, key string)) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			walkKeys(c, prefix, fn)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			fn(path, key)
			walkKeys(n.Content[i+1], path, fn)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walkKeys(c, prefix, fn)
		}
	}
}

// missingKeys returns the dotted paths of every unset field, depth first. It is
// the completeness check that replaces defaults: the schema is the list of
// things that must be configured, and anything absent is named here.
func missingKeys(v reflect.Value, prefix string) []string {
	var missing []string
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Ptr:
			if f.IsNil() {
				missing = append(missing, path)
				continue
			}
			if f.Elem().Kind() == reflect.Struct && f.Type().Elem() != reflect.TypeOf(Duration(0)) {
				missing = append(missing, missingKeys(f.Elem(), path)...)
			}
		case reflect.Slice:
			if f.Len() == 0 {
				missing = append(missing, path)
				continue
			}
			for j := 0; j < f.Len(); j++ {
				if f.Index(j).Kind() == reflect.Struct {
					missing = append(missing, missingKeys(f.Index(j), fmt.Sprintf("%s[%d]", path, j))...)
				}
			}
		}
	}
	return missing
}

func readRaw(path string) (*raw, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: cannot read %s: %w", path, err)
	}
	if err := rejectSecrets(data); err != nil {
		return nil, err
	}
	var r raw
	if err := decodeStrict(data, &r); err != nil {
		return nil, err
	}
	// Every missing key at once: an operator fixing a 40-key file one restart at
	// a time is a loader bug.
	if missing := missingKeys(reflect.ValueOf(&r).Elem(), ""); len(missing) > 0 {
		return nil, fmt.Errorf("config: %s is missing %d required keys:\n  %s",
			path, len(missing), strings.Join(missing, "\n  "))
	}
	return &r, nil
}
```

Note the `Duration(0)` guard in `missingKeys`: `*Duration` is a pointer to a named integer type, not a struct, so it is handled by the nil check alone. The guard is there because a future pointer-to-struct leaf would otherwise be walked as a group.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, including `TestCommittedConfigDecodesStrictly` from Task 3. If `TestCommittedConfigHasEveryKey` fails, `airbg.yaml` and the schema disagree — fix the file, not the test.

- [ ] **Step 5: Mutation-prove the missing-key error**

The load-bearing property is that an absent key is an error rather than a zero value.

```bash
cp internal/config/load.go /tmp/load.go.orig
```

In `readRaw`, delete the `missingKeys` block (the `if missing := ...` statement).

Run: `go test ./internal/config/ -run TestReadRawReportsMissingKeys -v`
Expected: FAIL with `readRaw error = nil, want an error listing missing keys`. Quote the real output.

Restore, then mutate a second time: change `dec.KnownFields(true)` to `dec.KnownFields(false)`.

Run: `go test ./internal/config/ -run TestReadRawRejectsUnknownKey -v`
Expected: FAIL — the error will now be the missing-key error, which does not name `addr_typo`, so the assertion `error %q does not name the unknown key addr_typo` fires. Quote the real output.

Restore and confirm:

```bash
cp /tmp/load.go.orig internal/config/load.go
git diff --stat internal/config/load.go   # expected: no output
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/load.go internal/config/load_test.go
git commit -m "config: read airbg.yaml strictly and name every missing key"
```

---

### Task 5: The environment overlay

**Files:**
- Modify: `internal/config/load.go`
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: `raw`, `envName`, `Duration`.
- Produces: `func applyEnv(v reflect.Value, prefix string) error` — walks the raw struct and overlays any `AIRBG_*` variable whose name matches a leaf's derived key path. Called by `readRaw` after decoding and **before** the missing-key check, so a key may be supplied by the environment alone.

The overlay is a reflection walk rather than a hand-maintained table on purpose: a 40-entry table is a 40-entry opportunity for the documented mechanical rule to be quietly false for one key. Deriving the name from the same `yaml` tag the file uses makes the rule true by construction.

Scalar leaves and `[]string` are overridable. `series.periods` — a list of structs — is not: there is no sane name for "the third entry's window", and the file is the right place to edit a table. This limit must be stated in `docs/configuration.md` (Task 18).

- [ ] **Step 1: Write the failing test**

Append to `internal/config/load_test.go`:

```go
// The mechanical rule must hold for every scalar kind, not just strings.
func TestApplyEnvOverridesEveryScalarKind(t *testing.T) {
	path := filepath.Join("..", "..", "airbg.yaml")

	t.Setenv("AIRBG_LISTEN_ADDR", "0.0.0.0:9999")
	t.Setenv("AIRBG_LISTEN_MAX_CONNS", "512")
	t.Setenv("AIRBG_UPSTREAM_POLL_INTERVAL", "11m")
	t.Setenv("AIRBG_QUALITY_MAD_SCALE", "2.5")
	t.Setenv("AIRBG_STORE_COVERAGE_THRESHOLD", "7")
	t.Setenv("AIRBG_UPSTREAM_MAX_PAYLOAD_BYTES", "1024")
	t.Setenv("AIRBG_LISTEN_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,192.168.0.0/16")

	r, err := readRaw(path)
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	if got, want := *r.Listen.Addr, "0.0.0.0:9999"; got != want {
		t.Errorf("listen.addr = %q, want %q", got, want)
	}
	if got, want := *r.Listen.MaxConns, int32(512); got != want {
		t.Errorf("listen.max_conns = %d, want %d", got, want)
	}
	if got, want := r.Upstream.PollInterval.Std(), 11*time.Minute; got != want {
		t.Errorf("upstream.poll_interval = %v, want %v", got, want)
	}
	if got, want := *r.Quality.MADScale, 2.5; got != want {
		t.Errorf("quality.mad_scale = %v, want %v", got, want)
	}
	if got, want := *r.Store.CoverageThreshold, 7; got != want {
		t.Errorf("store.coverage_threshold = %d, want %d", got, want)
	}
	if got, want := *r.Upstream.MaxPayloadBytes, int64(1024); got != want {
		t.Errorf("upstream.max_payload_bytes = %d, want %d", got, want)
	}
	if got, want := len(*r.Listen.TrustedProxyCIDRs), 2; got != want {
		t.Fatalf("len(listen.trusted_proxy_cidrs) = %d, want %d", got, want)
	}
	if got, want := (*r.Listen.TrustedProxyCIDRs)[1], "192.168.0.0/16"; got != want {
		t.Errorf("listen.trusted_proxy_cidrs[1] = %q, want %q", got, want)
	}
}

// A nested leaf three levels down must follow the same rule.
func TestApplyEnvOverridesNestedLeaf(t *testing.T) {
	t.Setenv("AIRBG_QUALITY_RANGES_PRESSURE_MIN", "700")
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	if got, want := *r.Quality.Ranges.Pressure.Min, 700.0; got != want {
		t.Errorf("quality.ranges.pressure.min = %v, want %v", got, want)
	}
}

// An unparseable override must be a startup error naming the variable, never a
// silently-ignored value that leaves the file's setting in place.
func TestApplyEnvRejectsGarbage(t *testing.T) {
	t.Setenv("AIRBG_LISTEN_MAX_CONNS", "many")
	_, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err == nil {
		t.Fatal("readRaw error = nil, want an error for AIRBG_LISTEN_MAX_CONNS=many")
	}
	if !strings.Contains(err.Error(), "AIRBG_LISTEN_MAX_CONNS") {
		t.Errorf("error %q does not name the offending variable", err)
	}
}

// The environment can supply a key the file omits entirely — the two layers are
// file then environment, and either may be the source of a value.
func TestApplyEnvSuppliesAbsentKey(t *testing.T) {
	body := "listen:\n  metrics_addr: \"127.0.0.1:9090\"\n"
	path := writeTemp(t, body)
	t.Setenv("AIRBG_LISTEN_ADDR", "127.0.0.1:8080")
	_, err := readRaw(path)
	if err == nil {
		t.Fatal("readRaw error = nil, want a missing-key error for the other keys")
	}
	if strings.Contains(err.Error(), "listen.addr") {
		t.Errorf("listen.addr was supplied by the environment but still reported missing:\n%s", err)
	}
}
```

Add `"time"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestApplyEnv -v`
Expected: FAIL — `listen.addr = "127.0.0.1:8080", want "0.0.0.0:9999"`, because no overlay exists yet.

- [ ] **Step 3: Write the implementation**

Add to `internal/config/load.go`:

```go
// applyEnv overlays AIRBG_* variables onto the decoded schema. The variable name
// is derived from the same yaml tag the file uses, so the documented rule
// ("AIRBG_ + key path, uppercased, dots to underscores") is true by
// construction rather than by a hand-maintained table that can drift.
//
// series.periods is not overridable: there is no sane environment-variable name
// for "the third list entry's window", and a table belongs in the file.
func applyEnv(v reflect.Value, prefix string) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		f := v.Field(i)
		if f.Kind() != reflect.Ptr {
			continue
		}
		elem := f.Type().Elem()
		if elem.Kind() == reflect.Struct && elem != reflect.TypeOf(Duration(0)) {
			// A group. Allocate it if absent so an environment-only override of a
			// leaf inside an omitted group still lands.
			if f.IsNil() {
				f.Set(reflect.New(elem))
			}
			if err := applyEnv(f.Elem(), path); err != nil {
				return err
			}
			continue
		}
		val, ok := os.LookupEnv(envName(path))
		if !ok {
			continue
		}
		if f.IsNil() {
			f.Set(reflect.New(elem))
		}
		if err := assignScalar(f.Elem(), val); err != nil {
			return fmt.Errorf("config: %s=%q: %w", envName(path), val, err)
		}
	}
	return nil
}

func assignScalar(dst reflect.Value, val string) error {
	if dst.Type() == reflect.TypeOf(Duration(0)) {
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("not a duration such as \"5m\": %w", err)
		}
		dst.SetInt(int64(d))
		return nil
	}
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(val)
	case reflect.Bool:
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("not a boolean: %w", err)
		}
		dst.SetBool(b)
	case reflect.Int, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(val, 10, dst.Type().Bits())
		if err != nil {
			return fmt.Errorf("not an integer: %w", err)
		}
		dst.SetInt(n)
	case reflect.Float64:
		x, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("not a number: %w", err)
		}
		dst.SetFloat(x)
	case reflect.Slice:
		if dst.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("cannot be set from the environment")
		}
		parts := strings.Split(val, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		dst.Set(reflect.ValueOf(out))
	default:
		return fmt.Errorf("cannot be set from the environment")
	}
	return nil
}
```

Add `"strconv"` and `"time"` to `load.go`'s imports.

Then wire it into `readRaw`, between the decode and the missing-key check:

```go
	if err := decodeStrict(data, &r); err != nil {
		return nil, err
	}
	// Environment second: the two layers are file then environment, and either
	// may be the sole source of a value.
	if err := applyEnv(reflect.ValueOf(&r).Elem(), ""); err != nil {
		return nil, err
	}
	if missing := missingKeys(reflect.ValueOf(&r).Elem(), ""); len(missing) > 0 {
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS.

Note the interaction the group-allocation handles: `applyEnv` allocates an absent group so a leaf override inside it can land, which means `missingKeys` then sees a non-nil group with nil leaves and reports the leaves individually — a better error than "the whole `quality` group is missing".

- [ ] **Step 5: Mutation-prove the garbage rejection**

```bash
cp internal/config/load.go /tmp/load.go.orig
```

In `assignScalar`'s `reflect.Int` case, replace the error return with `return nil` (ignore the parse failure).

Run: `go test ./internal/config/ -run TestApplyEnvRejectsGarbage -v`
Expected: FAIL with `readRaw error = nil, want an error for AIRBG_LISTEN_MAX_CONNS=many`. Quote the real output — this is the mutation that proves a typo'd override cannot silently leave the file's value in place.

Restore and confirm:

```bash
cp /tmp/load.go.orig internal/config/load.go
git diff --stat internal/config/load.go   # expected: no output
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/load.go internal/config/load_test.go
git commit -m "config: overlay AIRBG_* overrides derived from the yaml key path"
```

---

### Task 6: Resolve the raw schema into the typed Config

**Files:**
- Create: `internal/config/resolve.go`
- Test: `internal/config/resolve_test.go`

**Interfaces:**
- Consumes: `raw` and everything under it.
- Produces the type the rest of the tree consumes. Later tasks depend on these exact names:

```go
type Config struct {
	Listen    Listen
	Timeouts  Timeouts
	Database  Database
	RateLimit RateLimit
	Cache     Cache
	Upstream  Upstream
	Store     Store
	Series    Series
	Quality   Quality
	Backfill  Backfill
	Frontend  Frontend
	Basemap   Basemap
}
```

- `func resolve(r *raw) Config` — dereferences every pointer. Safe to dereference unconditionally because `readRaw` has already guaranteed no leaf is nil; that guarantee is the reason resolve is a separate, trivial function rather than 200 lines of nil checks scattered through the call sites.
- `Quality.Ranges` is a `map[string]Range` keyed by canonical metric name (`P1`, `P2`, `temperature`, `humidity`, `pressure`, `noise_LAeq`, `noise_LA_max`) — that is the shape `internal/quality` wants.
- `Series.Periods` is a `map[string]Period` plus `Series.PeriodNames []string` preserving file order.
- `Database.URL` and `Basemap.Key` come from the environment, never the file.
- `Basemap.StyleURL` has `{key}` already substituted.

- [ ] **Step 1: Write the resolved types and resolve()**

Create `internal/config/resolve.go`:

```go
package config

import "time"

// Config is the resolved configuration: value types, no pointers, no defaults.
// Every field is guaranteed set, because readRaw refuses to return a schema with
// a nil leaf. That guarantee is why resolve can dereference freely and why the
// consuming packages never see an Option or a nil check.
type Config struct {
	Listen    Listen
	Timeouts  Timeouts
	Database  Database
	RateLimit RateLimit
	Cache     Cache
	Upstream  Upstream
	Store     Store
	Series    Series
	Quality   Quality
	Backfill  Backfill
	Frontend  Frontend
	Basemap   Basemap
}

type Listen struct {
	Addr              string
	MetricsAddr       string
	BaseURL           string
	MaxConns          int32
	TrustedProxyCIDRs []string
	CSP               string
	PermissionsPolicy string
}

type Timeouts struct {
	ReadHeader    time.Duration
	Read          time.Duration
	Write         time.Duration
	Idle          time.Duration
	ShutdownGrace time.Duration
}

type Database struct {
	// URL is env-only (AIRBG_DATABASE_URL). It is a credential, and the config
	// file is committed.
	URL               string
	APIConns          int32
	CollectorConns    int32
	MaxInflight       int32
	StatementTimeouts StatementTimeouts
}

type StatementTimeouts struct {
	Default  time.Duration
	Assign   time.Duration
	Operator time.Duration
	Series   time.Duration
}

type RateLimit struct {
	API        Bucket
	Series     Bucket
	Enumerate  Enumerate
	ShardCount int
}

type Bucket struct {
	PerSecond     float64
	Burst         float64
	TTL           time.Duration
	EvictInterval time.Duration
	RetryAfter    time.Duration
}

type Enumerate struct {
	AreasPerWindow   int
	SensorsPerWindow int
	Window           time.Duration
	RetryAfter       time.Duration
}

type Cache struct {
	DataMaxAge   time.Duration
	ScalesMaxAge time.Duration
}

type Upstream struct {
	URL             string
	RequestTimeout  time.Duration
	PollInterval    time.Duration
	MinPollInterval time.Duration
	MaxPayloadBytes int64
}

type Store struct {
	CoverageThreshold int
	FreshnessWindow   time.Duration
}

type Series struct {
	DefaultMetric string
	DefaultWindow time.Duration
	Periods       map[string]Period
	// PeriodNames preserves file order, which is the order the UI offers them in.
	PeriodNames []string
}

type Period struct {
	Window time.Duration
	Hourly bool
	MaxAge time.Duration
}

type Quality struct {
	MinNeighbours         int
	MADScale              float64
	MADThreshold          float64
	NeighbourRadiusMetres float64
	EarthRadiusMetres     float64
	HistoryDepth          int
	// Ranges is keyed by canonical metric name.
	Ranges map[string]Range
}

type Range struct {
	Min float64
	Max float64
}

type Backfill struct {
	HighRejectionFraction float64
}

type Frontend struct {
	NoDataColour       string
	MarkerStrokeColour string
	EmptyBasemapColour string
	ChartLineColour    string
	ZoomCity           int
	ZoomSensor         int
}

type Basemap struct {
	// StyleURL already has {key} substituted.
	StyleURL string
	// Key is env-only (AIRBG_BASEMAP_KEY).
	Key string
}

func resolve(r *raw) Config {
	cfg := Config{
		Listen: Listen{
			Addr:              *r.Listen.Addr,
			MetricsAddr:       *r.Listen.MetricsAddr,
			BaseURL:           *r.Listen.BaseURL,
			MaxConns:          *r.Listen.MaxConns,
			TrustedProxyCIDRs: *r.Listen.TrustedProxyCIDRs,
			CSP:               *r.Listen.CSP,
			PermissionsPolicy: *r.Listen.PermissionsPolicy,
		},
		Timeouts: Timeouts{
			ReadHeader:    r.Timeouts.ReadHeader.Std(),
			Read:          r.Timeouts.Read.Std(),
			Write:         r.Timeouts.Write.Std(),
			Idle:          r.Timeouts.Idle.Std(),
			ShutdownGrace: r.Timeouts.ShutdownGrace.Std(),
		},
		Database: Database{
			APIConns:       *r.Database.APIConns,
			CollectorConns: *r.Database.CollectorConns,
			MaxInflight:    *r.Database.MaxInflight,
			StatementTimeouts: StatementTimeouts{
				Default:  r.Database.StatementTimeouts.Default.Std(),
				Assign:   r.Database.StatementTimeouts.Assign.Std(),
				Operator: r.Database.StatementTimeouts.Operator.Std(),
				Series:   r.Database.StatementTimeouts.Series.Std(),
			},
		},
		RateLimit: RateLimit{
			API:        resolveBucket(r.RateLimit.API),
			Series:     resolveBucket(r.RateLimit.Series),
			ShardCount: *r.RateLimit.ShardCount,
			Enumerate: Enumerate{
				AreasPerWindow:   *r.RateLimit.Enumerate.AreasPerWindow,
				SensorsPerWindow: *r.RateLimit.Enumerate.SensorsPerWindow,
				Window:           r.RateLimit.Enumerate.Window.Std(),
				RetryAfter:       r.RateLimit.Enumerate.RetryAfter.Std(),
			},
		},
		Cache: Cache{
			DataMaxAge:   r.Cache.DataMaxAge.Std(),
			ScalesMaxAge: r.Cache.ScalesMaxAge.Std(),
		},
		Upstream: Upstream{
			URL:             *r.Upstream.URL,
			RequestTimeout:  r.Upstream.RequestTimeout.Std(),
			PollInterval:    r.Upstream.PollInterval.Std(),
			MinPollInterval: r.Upstream.MinPollInterval.Std(),
			MaxPayloadBytes: *r.Upstream.MaxPayloadBytes,
		},
		Store: Store{
			CoverageThreshold: *r.Store.CoverageThreshold,
			FreshnessWindow:   r.Store.FreshnessWindow.Std(),
		},
		Series: Series{
			DefaultMetric: *r.Series.DefaultMetric,
			DefaultWindow: r.Series.DefaultWindow.Std(),
			Periods:       make(map[string]Period, len(r.Series.Periods)),
		},
		Quality: Quality{
			MinNeighbours:         *r.Quality.MinNeighbours,
			MADScale:              *r.Quality.MADScale,
			MADThreshold:          *r.Quality.MADThreshold,
			NeighbourRadiusMetres: *r.Quality.NeighbourRadiusMetres,
			EarthRadiusMetres:     *r.Quality.EarthRadiusMetres,
			HistoryDepth:          *r.Quality.HistoryDepth,
			Ranges: map[string]Range{
				"P1":           resolveRange(r.Quality.Ranges.P1),
				"P2":           resolveRange(r.Quality.Ranges.P2),
				"temperature":  resolveRange(r.Quality.Ranges.Temperature),
				"humidity":     resolveRange(r.Quality.Ranges.Humidity),
				"pressure":     resolveRange(r.Quality.Ranges.Pressure),
				"noise_LAeq":   resolveRange(r.Quality.Ranges.NoiseLAeq),
				"noise_LA_max": resolveRange(r.Quality.Ranges.NoiseLAMax),
			},
		},
		Backfill: Backfill{
			HighRejectionFraction: *r.Backfill.HighRejectionFraction,
		},
		Frontend: Frontend{
			NoDataColour:       *r.Frontend.NoDataColour,
			MarkerStrokeColour: *r.Frontend.MarkerStrokeColour,
			EmptyBasemapColour: *r.Frontend.EmptyBasemapColour,
			ChartLineColour:    *r.Frontend.ChartLineColour,
			ZoomCity:           *r.Frontend.ZoomCity,
			ZoomSensor:         *r.Frontend.ZoomSensor,
		},
		Basemap: Basemap{
			StyleURL: *r.Basemap.StyleURL,
		},
	}
	for _, p := range r.Series.Periods {
		cfg.Series.Periods[*p.Name] = Period{
			Window: p.Window.Std(),
			Hourly: *p.Hourly,
			MaxAge: p.MaxAge.Std(),
		}
		cfg.Series.PeriodNames = append(cfg.Series.PeriodNames, *p.Name)
	}
	return cfg
}

func resolveBucket(b *rawBucket) Bucket {
	return Bucket{
		PerSecond:     *b.PerSecond,
		Burst:         *b.Burst,
		TTL:           b.TTL.Std(),
		EvictInterval: b.EvictInterval.Std(),
		RetryAfter:    b.RetryAfter.Std(),
	}
}

func resolveRange(r *rawRange) Range {
	return Range{Min: *r.Min, Max: *r.Max}
}
```

- [ ] **Step 2: Write the test**

Create `internal/config/resolve_test.go`:

```go
package config

import (
	"path/filepath"
	"testing"
	"time"
)

// resolve must reproduce the committed file's values exactly. These assertions
// are the anchor for the behaviour-unchanged requirement: each value here equals
// the constant it replaces in the package named in the comment.
func TestResolveCommittedConfig(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)

	if got, want := cfg.Listen.MaxConns, int32(4096); got != want {
		t.Errorf("Listen.MaxConns = %d, want %d", got, want) // server.defaultMaxConns
	}
	if got, want := cfg.Timeouts.ReadHeader, 5*time.Second; got != want {
		t.Errorf("Timeouts.ReadHeader = %v, want %v", got, want) // server.readHeaderTimeout
	}
	if got, want := cfg.Database.StatementTimeouts.Series, 5*time.Second; got != want {
		t.Errorf("StatementTimeouts.Series = %v, want %v", got, want) // db.SeriesStatementTimeout
	}
	if got, want := cfg.RateLimit.Enumerate.AreasPerWindow, 12; got != want {
		t.Errorf("Enumerate.AreasPerWindow = %d, want %d", got, want) // ratelimit.DistinctAreaLimit
	}
	if got, want := cfg.Cache.DataMaxAge, 150*time.Second; got != want {
		t.Errorf("Cache.DataMaxAge = %v, want %v", got, want) // api.dataMaxAge
	}
	if got, want := cfg.Store.CoverageThreshold, 3; got != want {
		t.Errorf("Store.CoverageThreshold = %d, want %d", got, want) // store.CoverageThreshold
	}
	if got, want := cfg.Quality.MADScale, 1.4826; got != want {
		t.Errorf("Quality.MADScale = %v, want %v", got, want) // quality.madScale
	}
	if got, want := cfg.Quality.Ranges["pressure"].Min, 650.0; got != want {
		t.Errorf("Quality.Ranges[pressure].Min = %v, want %v", got, want) // quality.ranges
	}
	if got, want := cfg.Backfill.HighRejectionFraction, 0.5; got != want {
		t.Errorf("Backfill.HighRejectionFraction = %v, want %v", got, want)
	}
	if got, want := cfg.Frontend.NoDataColour, "#9ca3af"; got != want {
		t.Errorf("Frontend.NoDataColour = %q, want %q", got, want) // colour.js NO_DATA_COLOUR
	}
}

// All seven canonical metrics must have a range. A missing entry would mean a
// metric whose readings are never plausibility-checked.
func TestResolveHasEveryMetricRange(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)
	for _, m := range []string{"P1", "P2", "temperature", "humidity", "pressure", "noise_LAeq", "noise_LA_max"} {
		rng, ok := cfg.Quality.Ranges[m]
		if !ok {
			t.Errorf("Quality.Ranges is missing %q", m)
			continue
		}
		if rng.Max <= rng.Min {
			t.Errorf("Quality.Ranges[%q] = %+v, want Max > Min", m, rng)
		}
	}
}

// The four periods must resolve with their per-period cache lifetimes, and the
// 1-year period must be hourly: raw readings are retained for 30 days, so a
// 1-year window against the raw table returns 30 days under a "1 year" label.
func TestResolvePeriods(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)
	want := map[string]Period{
		"24h": {24 * time.Hour, false, 150 * time.Second},
		"7d":  {7 * 24 * time.Hour, false, 600 * time.Second},
		"30d": {30 * 24 * time.Hour, false, 1800 * time.Second},
		"1y":  {365 * 24 * time.Hour, true, 10800 * time.Second},
	}
	for name, w := range want {
		got, ok := cfg.Series.Periods[name]
		if !ok {
			t.Errorf("Series.Periods is missing %q", name)
			continue
		}
		if got != w {
			t.Errorf("Series.Periods[%q] = %+v, want %+v", name, got, w)
		}
	}
	if got, want := len(cfg.Series.PeriodNames), 4; got != want {
		t.Errorf("len(Series.PeriodNames) = %d, want %d", got, want)
	}
	if got, want := cfg.Series.PeriodNames[0], "24h"; got != want {
		t.Errorf("Series.PeriodNames[0] = %q, want %q (file order is UI order)", got, want)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/config/ -v`
Expected: PASS. Any failure here is `airbg.yaml` disagreeing with the constant it replaces — fix the file.

- [ ] **Step 4: Commit**

```bash
git add internal/config/resolve.go internal/config/resolve_test.go
git commit -m "config: resolve the raw schema into a typed Config"
```

---

### Task 7: Validation, Load and LoadFile

**Files:**
- Create: `internal/config/validate.go`
- Test: `internal/config/validate_test.go`
- Modify: `internal/config/load.go` — gains `Load` and `LoadFile`.

**Interfaces:**
- Produces:
  - `func (c Config) Validate() error` — accumulates **every** violation and returns them in one error. Fail-closed: any violation is a refusal to start.
  - `func LoadFile(path string) (Config, error)` — read → env overlay → resolve → env secrets → `{key}` substitution → validate.
  - `func Load() (Config, error)` — resolves the path from `AIRBG_CONFIG` and calls `LoadFile`. This is what `main.go` calls in Task 8.
  - `const DatabaseURLEnv = "AIRBG_DATABASE_URL"`, `const BasemapKeyEnv = "AIRBG_BASEMAP_KEY"`.

Validation is where "everything is configurable" stops being a hazard. A knob with no floor is a footgun; the ranges below are the floors, and every one of them refuses at startup rather than degrading at runtime.

- [ ] **Step 1: Write the validator**

Create `internal/config/validate.go`:

```go
package config

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// hostPattern and maxHostLength are validation mechanics, not tunables: they
// describe what a hostname is, which is not an operator decision.
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.-]+(:[0-9]+)?$`)

const maxHostLength = 253

var colourPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

var canonicalMetrics = map[string]bool{
	"P1": true, "P2": true, "temperature": true, "humidity": true,
	"pressure": true, "noise_LAeq": true, "noise_LA_max": true,
}

// problems accumulates every violation so an operator sees the whole list in one
// startup attempt rather than one per restart.
type problems []string

func (p *problems) addf(format string, args ...any) {
	*p = append(*p, fmt.Sprintf(format, args...))
}

func (p *problems) positive(path string, d time.Duration) {
	if d <= 0 {
		p.addf("%s = %v, must be greater than zero", path, d)
	}
}

func (p *problems) positiveInt(path string, n int) {
	if n <= 0 {
		p.addf("%s = %d, must be greater than zero", path, n)
	}
}

func (p *problems) positiveFloat(path string, x float64) {
	if x <= 0 {
		p.addf("%s = %v, must be greater than zero", path, x)
	}
}

func (c Config) Validate() error {
	var p problems

	c.validateListen(&p)
	c.validateTimeouts(&p)
	c.validateDatabase(&p)
	c.validateRateLimit(&p)
	c.validateUpstreamAndCache(&p)
	c.validateStoreAndSeries(&p)
	c.validateQuality(&p)
	c.validateFrontend(&p)

	if len(p) > 0 {
		return fmt.Errorf("config: %d problem(s):\n  %s", len(p), strings.Join(p, "\n  "))
	}
	return nil
}

func (c Config) validateListen(p *problems) {
	for path, addr := range map[string]string{
		"listen.addr":         c.Listen.Addr,
		"listen.metrics_addr": c.Listen.MetricsAddr,
	} {
		if addr == "" {
			p.addf("%s is empty", path)
			continue
		}
		if len(addr) > maxHostLength {
			p.addf("%s is %d bytes, must be at most %d", path, len(addr), maxHostLength)
		}
		if !hostPattern.MatchString(addr) {
			p.addf("%s = %q, must be host:port", path, addr)
		}
	}
	// Sharing the address means /metrics is reachable from the public chain,
	// which hands an attacker the counters that show whether their probing is
	// being rate limited.
	if c.Listen.Addr == c.Listen.MetricsAddr {
		p.addf("listen.addr and listen.metrics_addr are both %q; the private listener must be separate", c.Listen.Addr)
	}
	if u, err := url.Parse(c.Listen.BaseURL); err != nil {
		p.addf("listen.base_url = %q is not a URL: %v", c.Listen.BaseURL, err)
	} else if u.Scheme != "http" && u.Scheme != "https" {
		p.addf("listen.base_url = %q must use http or https", c.Listen.BaseURL)
	} else if u.Host == "" {
		p.addf("listen.base_url = %q must be absolute", c.Listen.BaseURL)
	} else if u.User != nil {
		p.addf("listen.base_url must not contain userinfo")
	}
	if c.Listen.MaxConns <= 0 {
		p.addf("listen.max_conns = %d, must be greater than zero; zero would be an unlimited public listener", c.Listen.MaxConns)
	}
	for _, cidr := range c.Listen.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			p.addf("listen.trusted_proxy_cidrs contains %q, which is not a CIDR: %v", cidr, err)
		}
	}
	// A CSP with either of these is decorative. Making the policy configurable
	// must not make it disableable.
	for _, bad := range []string{"unsafe-inline", "unsafe-eval"} {
		if strings.Contains(c.Listen.CSP, bad) {
			p.addf("listen.csp contains %q, which is never permitted", bad)
		}
	}
	if !strings.Contains(c.Listen.CSP, "default-src") {
		p.addf("listen.csp has no default-src directive")
	}
	if c.Listen.PermissionsPolicy == "" {
		p.addf("listen.permissions_policy is empty; write an explicit denial list instead")
	}
}

func (c Config) validateTimeouts(p *problems) {
	p.positive("timeouts.read_header", c.Timeouts.ReadHeader)
	p.positive("timeouts.read", c.Timeouts.Read)
	p.positive("timeouts.write", c.Timeouts.Write)
	p.positive("timeouts.idle", c.Timeouts.Idle)
	p.positive("timeouts.shutdown_grace", c.Timeouts.ShutdownGrace)
	if c.Timeouts.Read < c.Timeouts.ReadHeader {
		p.addf("timeouts.read (%v) is shorter than timeouts.read_header (%v)", c.Timeouts.Read, c.Timeouts.ReadHeader)
	}
}

func (c Config) validateDatabase(p *problems) {
	if c.Database.URL == "" {
		p.addf("%s is not set in the environment; it is required and must never be written to the config file", DatabaseURLEnv)
	}
	if c.Database.APIConns <= 0 {
		p.addf("database.api_conns = %d, must be greater than zero", c.Database.APIConns)
	}
	if c.Database.CollectorConns <= 0 {
		p.addf("database.collector_conns = %d, must be greater than zero", c.Database.CollectorConns)
	}
	if c.Database.MaxInflight <= 0 {
		p.addf("database.max_inflight = %d, must be greater than zero", c.Database.MaxInflight)
	}
	t := c.Database.StatementTimeouts
	p.positive("database.statement_timeouts.default", t.Default)
	p.positive("database.statement_timeouts.assign", t.Assign)
	p.positive("database.statement_timeouts.operator", t.Operator)
	p.positive("database.statement_timeouts.series", t.Series)
	// /series is the most expensive public query, so its budget must stay at or
	// below the default rather than above it.
	if t.Series > t.Default {
		p.addf("database.statement_timeouts.series (%v) exceeds .default (%v); the public series query must be the tighter budget", t.Series, t.Default)
	}
}

func (c Config) validateRateLimit(p *problems) {
	for path, b := range map[string]Bucket{
		"ratelimit.api":    c.RateLimit.API,
		"ratelimit.series": c.RateLimit.Series,
	} {
		p.positiveFloat(path+".per_second", b.PerSecond)
		p.positiveFloat(path+".burst", b.Burst)
		p.positive(path+".ttl", b.TTL)
		p.positive(path+".evict_interval", b.EvictInterval)
		p.positive(path+".retry_after", b.RetryAfter)
		if b.Burst < b.PerSecond {
			p.addf("%s.burst (%v) is below .per_second (%v); the bucket could never fill for one second of traffic", path, b.Burst, b.PerSecond)
		}
		if b.EvictInterval > b.TTL {
			p.addf("%s.evict_interval (%v) exceeds .ttl (%v); entries would outlive their bucket", path, b.EvictInterval, b.TTL)
		}
	}
	e := c.RateLimit.Enumerate
	p.positiveInt("ratelimit.enumerate.areas_per_window", e.AreasPerWindow)
	p.positiveInt("ratelimit.enumerate.sensors_per_window", e.SensorsPerWindow)
	p.positive("ratelimit.enumerate.window", e.Window)
	p.positive("ratelimit.enumerate.retry_after", e.RetryAfter)
	p.positiveInt("ratelimit.shard_count", c.RateLimit.ShardCount)
}

func (c Config) validateUpstreamAndCache(p *problems) {
	if u, err := url.Parse(c.Upstream.URL); err != nil {
		p.addf("upstream.url = %q is not a URL: %v", c.Upstream.URL, err)
	} else if u.Scheme != "http" && u.Scheme != "https" {
		p.addf("upstream.url = %q must use http or https", c.Upstream.URL)
	} else if u.Host == "" {
		p.addf("upstream.url = %q must be absolute", c.Upstream.URL)
	}
	p.positive("upstream.request_timeout", c.Upstream.RequestTimeout)
	p.positive("upstream.min_poll_interval", c.Upstream.MinPollInterval)
	// Polling a volunteer-run public API faster than the floor is abusive, so
	// the floor is enforced rather than advisory.
	if c.Upstream.PollInterval < c.Upstream.MinPollInterval {
		p.addf("upstream.poll_interval (%v) is below upstream.min_poll_interval (%v)", c.Upstream.PollInterval, c.Upstream.MinPollInterval)
	}
	if c.Upstream.MaxPayloadBytes <= 0 {
		p.addf("upstream.max_payload_bytes = %d, must be greater than zero", c.Upstream.MaxPayloadBytes)
	}
	p.positive("cache.data_max_age", c.Cache.DataMaxAge)
	p.positive("cache.scales_max_age", c.Cache.ScalesMaxAge)
	// A client caching for longer than one ingest cycle can show a reading that
	// has already been superseded. This was a code comment; here it is checked.
	if half := c.Upstream.PollInterval / 2; c.Cache.DataMaxAge > half {
		p.addf("cache.data_max_age (%v) exceeds half of upstream.poll_interval (%v)", c.Cache.DataMaxAge, half)
	}
}

func (c Config) validateStoreAndSeries(p *problems) {
	if c.Store.CoverageThreshold < 1 {
		p.addf("store.coverage_threshold = %d, must be at least 1; below that a single sensor would be painted as a whole area", c.Store.CoverageThreshold)
	}
	p.positive("store.freshness_window", c.Store.FreshnessWindow)

	if !canonicalMetrics[c.Series.DefaultMetric] {
		p.addf("series.default_metric = %q is not a canonical metric", c.Series.DefaultMetric)
	}
	p.positive("series.default_window", c.Series.DefaultWindow)
	if len(c.Series.Periods) == 0 {
		p.addf("series.periods is empty")
	}
	seen := map[string]bool{}
	for _, name := range c.Series.PeriodNames {
		if seen[name] {
			p.addf("series.periods has a duplicate entry named %q", name)
		}
		seen[name] = true
		pd := c.Series.Periods[name]
		if name == "" {
			p.addf("series.periods has an entry with an empty name")
		}
		p.positive(fmt.Sprintf("series.periods[%s].window", name), pd.Window)
		p.positive(fmt.Sprintf("series.periods[%s].max_age", name), pd.MaxAge)
	}
	// The snapshot serves the default window without touching the database, so
	// the default window must equal the window api.parsePeriod derives from one
	// of the configured periods. If it does not, the snapshot answers a question
	// no period asks.
	matched := false
	for _, pd := range c.Series.Periods {
		if pd.Window == c.Series.DefaultWindow {
			matched = true
			break
		}
	}
	if !matched && len(c.Series.Periods) > 0 {
		p.addf("series.default_window (%v) matches no entry in series.periods", c.Series.DefaultWindow)
	}
}

func (c Config) validateQuality(p *problems) {
	q := c.Quality
	if q.MinNeighbours < 1 {
		p.addf("quality.min_neighbours = %d, must be at least 1", q.MinNeighbours)
	}
	p.positiveFloat("quality.mad_scale", q.MADScale)
	p.positiveFloat("quality.mad_threshold", q.MADThreshold)
	p.positiveFloat("quality.neighbour_radius_metres", q.NeighbourRadiusMetres)
	p.positiveFloat("quality.earth_radius_metres", q.EarthRadiusMetres)
	if q.HistoryDepth < 1 {
		p.addf("quality.history_depth = %d, must be at least 1", q.HistoryDepth)
	}
	for metric := range canonicalMetrics {
		rng, ok := q.Ranges[metric]
		if !ok {
			p.addf("quality.ranges has no entry for %q; its readings would never be plausibility-checked", metric)
			continue
		}
		if rng.Max <= rng.Min {
			p.addf("quality.ranges.%s: max (%v) must exceed min (%v)", metric, rng.Max, rng.Min)
		}
	}
	f := c.Backfill.HighRejectionFraction
	if f <= 0 || f > 1 {
		p.addf("backfill.high_rejection_fraction = %v, must be in (0, 1]", f)
	}
}

func (c Config) validateFrontend(p *problems) {
	for path, colour := range map[string]string{
		"frontend.no_data_colour":       c.Frontend.NoDataColour,
		"frontend.marker_stroke_colour": c.Frontend.MarkerStrokeColour,
		"frontend.empty_basemap_colour": c.Frontend.EmptyBasemapColour,
		"frontend.chart_line_colour":    c.Frontend.ChartLineColour,
	} {
		if !colourPattern.MatchString(colour) {
			p.addf("%s = %q, must be a six-digit hex colour such as #9ca3af", path, colour)
		}
	}
	for path, zoom := range map[string]int{
		"frontend.zoom_city":   c.Frontend.ZoomCity,
		"frontend.zoom_sensor": c.Frontend.ZoomSensor,
	} {
		if zoom < 0 || zoom > 24 {
			p.addf("%s = %d, must be between 0 and 24", path, zoom)
		}
	}
	if c.Frontend.ZoomCity >= c.Frontend.ZoomSensor {
		p.addf("frontend.zoom_city (%d) must be below frontend.zoom_sensor (%d); the tiers are country, then city, then sensor", c.Frontend.ZoomCity, c.Frontend.ZoomSensor)
	}
	if c.Basemap.StyleURL == "" {
		p.addf("basemap.style_url is empty")
	} else if u, err := url.Parse(c.Basemap.StyleURL); err != nil {
		p.addf("basemap.style_url is not a URL: %v", err)
	} else {
		if u.Scheme != "http" && u.Scheme != "https" {
			p.addf("basemap.style_url must use http or https")
		}
		// Userinfo would put a credential in a URL the browser fetches, and the
		// CSP widens connect-src and img-src by this URL's host.
		if u.User != nil {
			p.addf("basemap.style_url must not contain userinfo")
		}
		if len(u.Host) > maxHostLength {
			p.addf("basemap.style_url host is %d bytes, must be at most %d", len(u.Host), maxHostLength)
		}
		if !hostPattern.MatchString(u.Host) {
			p.addf("basemap.style_url host = %q is not a valid hostname", u.Host)
		}
	}
}
```

- [ ] **Step 2: Add Load and LoadFile**

Append to `internal/config/load.go`:

```go
const (
	// DatabaseURLEnv and BasemapKeyEnv are env-only by design: both are
	// credentials, and airbg.yaml is committed.
	DatabaseURLEnv = "AIRBG_DATABASE_URL"
	BasemapKeyEnv  = "AIRBG_BASEMAP_KEY"
)

// Load reads the configuration named by AIRBG_CONFIG. There is no fallback
// path: guessing one would be a default, and this project keeps none.
func Load() (Config, error) {
	path := os.Getenv(PathEnv)
	if path == "" {
		return Config{}, fmt.Errorf("config: %s is not set; it must name the airbg.yaml to load", PathEnv)
	}
	return LoadFile(path)
}

func LoadFile(path string) (Config, error) {
	r, err := readRaw(path)
	if err != nil {
		return Config{}, err
	}
	cfg := resolve(r)
	cfg.Database.URL = os.Getenv(DatabaseURLEnv)
	cfg.Basemap.Key = os.Getenv(BasemapKeyEnv)
	// The style URL is templated so the key never appears in the committed file.
	cfg.Basemap.StyleURL = strings.ReplaceAll(cfg.Basemap.StyleURL, "{key}", cfg.Basemap.Key)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
```

- [ ] **Step 3: Write the validation tests**

Create `internal/config/validate_test.go`. `good()` builds a valid Config from the committed file so each subtest mutates exactly one field — that isolation is what makes each assertion prove its own rule.

```go
package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func good(t *testing.T) Config {
	t.Helper()
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile(airbg.yaml) error = %v, want nil", err)
	}
	return cfg
}

// The committed file plus a database URL must be valid. If this fails the
// shipped configuration cannot start.
func TestCommittedConfigValidates(t *testing.T) {
	_ = good(t)
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"metrics addr equal to public addr", func(c *Config) { c.Listen.MetricsAddr = c.Listen.Addr }, "must be separate"},
		{"zero max_conns", func(c *Config) { c.Listen.MaxConns = 0 }, "listen.max_conns"},
		{"unsafe-inline in csp", func(c *Config) { c.Listen.CSP += "; script-src 'unsafe-inline'" }, "unsafe-inline"},
		{"unsafe-eval in csp", func(c *Config) { c.Listen.CSP += "; script-src 'unsafe-eval'" }, "unsafe-eval"},
		{"bad cidr", func(c *Config) { c.Listen.TrustedProxyCIDRs = []string{"10.0.0.1"} }, "not a CIDR"},
		{"missing database url", func(c *Config) { c.Database.URL = "" }, DatabaseURLEnv},
		{"series timeout above default", func(c *Config) { c.Database.StatementTimeouts.Series = time.Minute }, "tighter budget"},
		{"burst below rate", func(c *Config) { c.RateLimit.Series.Burst = 0.5 }, "below .per_second"},
		{"evict beyond ttl", func(c *Config) { c.RateLimit.API.EvictInterval = 2 * time.Hour }, "outlive their bucket"},
		{"zero enumerate areas", func(c *Config) { c.RateLimit.Enumerate.AreasPerWindow = 0 }, "areas_per_window"},
		{"poll below floor", func(c *Config) { c.Upstream.PollInterval = 10 * time.Second }, "min_poll_interval"},
		{"cache above half poll", func(c *Config) { c.Cache.DataMaxAge = 4 * time.Minute }, "half of upstream.poll_interval"},
		{"zero coverage threshold", func(c *Config) { c.Store.CoverageThreshold = 0 }, "single sensor"},
		{"unknown default metric", func(c *Config) { c.Series.DefaultMetric = "PM9" }, "not a canonical metric"},
		{"default window matches no period", func(c *Config) { c.Series.DefaultWindow = 3 * time.Hour }, "matches no entry"},
		{"missing metric range", func(c *Config) { delete(c.Quality.Ranges, "pressure") }, "no entry for \"pressure\""},
		{"inverted range", func(c *Config) { c.Quality.Ranges["pressure"] = Range{Min: 1100, Max: 650} }, "must exceed min"},
		{"rejection fraction above one", func(c *Config) { c.Backfill.HighRejectionFraction = 1.5 }, "high_rejection_fraction"},
		{"bad colour", func(c *Config) { c.Frontend.NoDataColour = "grey" }, "hex colour"},
		{"zoom tiers inverted", func(c *Config) { c.Frontend.ZoomCity = 12 }, "must be below"},
		{"basemap userinfo", func(c *Config) { c.Basemap.StyleURL = "https://u:p@tiles.example.org/s.json" }, "userinfo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := good(t)
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want a rejection mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// Every violation must be reported in one pass. One-per-restart is a validator
// bug when the file has forty keys.
func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	cfg := good(t)
	cfg.Listen.MaxConns = 0
	cfg.Store.CoverageThreshold = 0
	cfg.Frontend.NoDataColour = "grey"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want three problems")
	}
	for _, want := range []string{"listen.max_conns", "store.coverage_threshold", "hex colour"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// A secret in the environment must never be required to be in the file, and the
// {key} placeholder must be substituted before the URL is validated or served.
func TestBasemapKeySubstitution(t *testing.T) {
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	t.Setenv(BasemapKeyEnv, "s3cr3t")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	if strings.Contains(cfg.Basemap.StyleURL, "{key}") {
		t.Errorf("Basemap.StyleURL still contains the {key} placeholder: %q", cfg.Basemap.StyleURL)
	}
	if !strings.Contains(cfg.Basemap.StyleURL, "s3cr3t") {
		t.Errorf("Basemap.StyleURL = %q, want the key substituted", cfg.Basemap.StyleURL)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config/ -v`
Expected: PASS, 21 subtests under `TestValidateRejects`.

- [ ] **Step 5: Mutation-prove three security-relevant rules**

These three are the ones that would matter if the tests were inert. Prove each by breaking `validate.go`, quoting the failure, and restoring from a copy.

```bash
cp internal/config/validate.go /tmp/validate.go.orig
```

1. Delete the `listen.addr == listen.metrics_addr` check.
   Run: `go test ./internal/config/ -run 'TestValidateRejects/metrics_addr_equal_to_public_addr' -v`
   Expected: FAIL — `Validate() error = nil, want a rejection mentioning "must be separate"`.
2. Restore, then change the CSP loop's `bad` slice to `[]string{}`.
   Run: `go test ./internal/config/ -run 'TestValidateRejects/unsafe' -v`
   Expected: FAIL on both `unsafe-inline` and `unsafe-eval` subtests.
3. Restore, then change `if half := c.Upstream.PollInterval / 2` to `/ 1`.
   Run: `go test ./internal/config/ -run 'TestValidateRejects/cache_above_half_poll' -v`
   Expected: FAIL — the 4-minute max-age no longer exceeds the 5-minute bound.

Quote all three real failures in the task report, then:

```bash
cp /tmp/validate.go.orig internal/config/validate.go
git diff --stat internal/config/validate.go   # expected: no output
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/validate.go internal/config/validate_test.go internal/config/load.go
git commit -m "config: validate every knob and fail closed at startup"
```

---

### Task 8: The cutover

**Files:**
- Delete: the old body of `internal/config/config.go` (the file itself goes away; `load.go`/`resolve.go`/`validate.go` replace it)
- Modify: `cmd/airbg/main.go`
- Modify/Delete: `internal/config/config_test.go` — tests of the deleted loader go with it; anything asserting a behaviour still required moves to `validate_test.go`
- Modify: `Dockerfile`, `docker-compose.yml`, `.github/workflows/*` if they set any renamed variable

**Interfaces:**
- Consumes: `config.Load() (Config, error)` from Task 7.
- Produces: a `main.go` that reads configuration exactly once and threads `cfg` into every constructor. No package outside `internal/config` reads an environment variable after this task.

This is the one task where the repo is briefly inconsistent, so it is one commit. After it, `go build ./...` is the completeness check for the whole sweep: every deleted constant is a compile error at its call site, and Tasks 9–15 clear them package by package. Expect this task to leave **other packages still compiling** — they still hold their own constants; the cutover only changes who supplies `main.go`'s values.

- [ ] **Step 1: Delete the old loader**

```bash
git rm internal/config/config.go
```

Read `internal/config/config_test.go` first. For each test, decide: does it assert a rule that `Validate()` now owns? If yes, port it to `validate_test.go` as a subtest of `TestValidateRejects`. If it asserts the *old* env-name behaviour or a default that no longer exists in code, delete it. Do not keep a test that passes against nothing.

```bash
git rm internal/config/config_test.go   # after porting anything still load-bearing
```

- [ ] **Step 2: Rewrite main.go's startup**

In `cmd/airbg/main.go`, replace the `config.Load()` call site and thread `cfg` outward. The exact edits, against the call sites confirmed in the current file:

```go
	cfg, err := config.Load()
	if err != nil {
		// Fail closed and print the whole list: a config error is an operator
		// error, and one problem per restart is a bad trade for them.
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
```

Then each threading change:

| line (current) | before | after |
| --- | --- | --- |
| `db.Open` | `db.Open(ctx, cfg.DatabaseURL)` | `db.Open(ctx, cfg.Database)` (Task 12) |
| `db.OpenPair` | `db.OpenPair(ctx, cfg.DatabaseURL, cfg.DBAPIConns, cfg.DBCollectorConns)` | `db.OpenPair(ctx, cfg.Database)` (Task 12) |
| `upstream.New` (both sites) | `upstream.New(cfg.UpstreamURL, 30*time.Second)` | `upstream.New(cfg.Upstream)` (Task 13) |
| `quality.NewHistory` (both sites) | `quality.NewHistory(12)` | `quality.NewHistory(cfg.Quality.HistoryDepth)` |
| `quality` scorer | free functions `quality.Score`/`SpatialCheck`/`InRange` | `quality.NewScorer(cfg.Quality)`, threaded to the ingest and backfill paths (Task 9) |
| `snapshot.NewHolder` | `snapshot.NewHolder()` | `snapshot.NewHolder(cfg.Series)` (Task 15) |
| `store.New` (three sites) | `store.New(pool)` | `store.New(pool, cfg.Store)` |
| `ing.Loop` | `ing.Loop(pollCtx, cfg.PollInterval)` | `ing.Loop(pollCtx, cfg.Upstream.PollInterval)` |
| `server.New` | see below | see below |

The constructor signature changes above are made in Tasks 9–15; in this task they will not compile yet. That is expected and is why this task's verification step is `go build ./cmd/airbg` **failing with a known list**, recorded in the report so the next tasks have a checklist.

`server.New`'s options block becomes:

```go
	srv, err := server.New(server.Options{
		Config:    cfg,
		Catalogue: catalogue,
		Snapshots: snapshots,
		Store:     st,
		Publisher: publisher,
		Logger:    logger,
	})
```

`server.Options` collapses its eleven scalar fields into the single `Config` field in Task 15. Passing the whole `Config` rather than fifteen scalars is the point: it is one value, already validated, and a new knob does not change any signature.

- [ ] **Step 3: Rename the environment variables everywhere outside Go**

```bash
grep -rn 'AIRBG_METRICS_ADDR\|AIRBG_BASE_URL\|AIRBG_POLL_INTERVAL\|AIRBG_DB_API_CONNS\|AIRBG_DB_COLLECTOR_CONNS\|AIRBG_MAX_CONNS\|AIRBG_MAX_DB_INFLIGHT\|AIRBG_TRUSTED_PROXY_CIDRS' --include='*' . | grep -v '^./docs/'
```

Apply the rename table from this plan's **Environment Override Rule** section to every hit: `Dockerfile`, `docker-compose.yml`, CI workflows, and any script. Then add `ENV AIRBG_CONFIG=/etc/airbg/airbg.yaml` and `COPY airbg.yaml /etc/airbg/airbg.yaml` to the `Dockerfile`'s final stage — the file is mandatory, so the image must carry one.

- [ ] **Step 4: Record the expected compile errors**

Run: `go build ./... 2>&1 | tee /tmp/phase3b-cutover-errors.txt`
Expected: FAIL. Copy the error list into the task report verbatim. Every line is a call site Tasks 9–15 must clear; anything in that list not covered by Tasks 9–15 is a gap in this plan and must be raised, not silently fixed.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "config: cut over to airbg.yaml and rename the AIRBG_* variables"
```

Do not stage `CLAUDE.md`. `git add -A` respects `.gitignore`, which already excludes it — verify with `git status --short` before committing.

---

### Task 9: Sweep internal/quality

**Files:**
- Modify: `internal/quality/score.go`, `internal/quality/spatial.go`, `internal/quality/ranges.go`, `internal/quality/history.go`
- Test: the package's existing `*_test.go` files

**Interfaces:**
- Consumes: `config.Quality`, `config.Range`.
- Produces:
  - `type Scorer struct { cfg config.Quality }`
  - `func NewScorer(cfg config.Quality) *Scorer`
  - `func (s *Scorer) Score(...)`, `func (s *Scorer) SpatialCheck(...)`, `func (s *Scorer) InRange(metric string, value float64) bool` — the three current free functions become methods, keeping their existing parameter lists otherwise unchanged.
  - `func NewHistory(depth int) *History` — unchanged signature; the caller now passes `cfg.Quality.HistoryDepth`.

- [ ] **Step 1: Delete the constants**

Delete, do not shadow — the compiler is the completeness check:

- `spatial.go`: `minNeighbours`, `madScale`, `madThreshold`
- `score.go`: `NeighbourRadiusMetres`, `earthRadiusMetres`
- `ranges.go`: the whole `ranges` table

The pressure comment must move to `airbg.yaml` (it is already there from Task 3). Verify it reads the same intent before deleting it here — a deleted rationale is the failure mode this step risks.

- [ ] **Step 2: Add the Scorer**

```go
// Scorer holds the quality thresholds. They were package constants; they are
// now configuration, so the checks that use them need an owner rather than
// reading a global.
type Scorer struct {
	cfg config.Quality
}

func NewScorer(cfg config.Quality) *Scorer { return &Scorer{cfg: cfg} }

// InRange rejects a reading outside its instrument's plausibility range. A
// metric with no configured range is rejected rather than accepted: an unknown
// metric that flows through unchecked is how bad data reaches an average.
func (s *Scorer) InRange(metric string, value float64) bool {
	r, ok := s.cfg.Ranges[metric]
	if !ok {
		return false
	}
	// NaN fails both comparisons, which is the intended answer.
	return value >= r.Min && value <= r.Max
}
```

Convert `Score` and `SpatialCheck` to methods and replace every constant reference with `s.cfg.<Field>`.

- [ ] **Step 3: Fix the tests**

Every test that called `quality.Score(...)` now needs a `Scorer`. Add one helper to the package's test files:

```go
// testScorer builds a Scorer with the same values airbg.yaml ships, so the
// existing assertions keep asserting the same thresholds.
func testScorer() *Scorer {
	return NewScorer(config.Quality{
		MinNeighbours:         3,
		MADScale:              1.4826,
		MADThreshold:          3.5,
		NeighbourRadiusMetres: 15000.0,
		EarthRadiusMetres:     6371000.0,
		HistoryDepth:          12,
		Ranges: map[string]config.Range{
			"P1":           {Min: 0, Max: 1000},
			"P2":           {Min: 0, Max: 1000},
			"temperature":  {Min: -40, Max: 60},
			"humidity":     {Min: 0, Max: 100},
			"pressure":     {Min: 650, Max: 1100},
			"noise_LAeq":   {Min: 25, Max: 120},
			"noise_LA_max": {Min: 25, Max: 120},
		},
	})
}
```

Do **not** change any expected value in any existing test. If an assertion has to change to pass, the sweep changed behaviour and must be corrected instead.

- [ ] **Step 4: Add the unknown-metric test**

`InRange` gained a branch the free function did not have — an unconfigured metric — so it needs its own assertion:

```go
func TestInRangeRejectsUnknownMetric(t *testing.T) {
	s := testScorer()
	if s.InRange("PM9", 5) {
		t.Error("InRange(\"PM9\", 5) = true, want false: an unconfigured metric must not pass unchecked")
	}
}
```

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/quality/ -v`
Expected: PASS, with the same set of test names as before plus `TestInRangeRejectsUnknownMetric`.

- [ ] **Step 6: Mutation-prove the pressure range survived**

```bash
cp airbg.yaml /tmp/airbg.yaml.orig
```

Set `quality.ranges.pressure.min` to `950.0` in `airbg.yaml`.

Run: `go test ./internal/config/ -run TestResolveCommittedConfig -v`
Expected: FAIL — `Quality.Ranges[pressure].Min = 950, want 650`. This is the test that stops someone "tidying" the range back toward sea level and silently discarding the mountain sensors. Quote the real output.

```bash
cp /tmp/airbg.yaml.orig airbg.yaml
git diff --stat airbg.yaml   # expected: no output
```

- [ ] **Step 7: Commit**

```bash
git add internal/quality/
git commit -m "quality: take thresholds and ranges from configuration"
```

---

### Task 10: Sweep internal/store

**Files:**
- Modify: `internal/store/store.go`, `internal/store/aggregate.go`
- Test: `internal/store/*_test.go` (testcontainers; slow — budget ~104s)

**Interfaces:**
- Consumes: `config.Store`.
- Produces: `func New(pool *pgxpool.Pool, cfg config.Store) *Store`. `Store` gains a `cfg config.Store` field. `CoverageThreshold` and `freshnessWindow` stop being package-level.

- [ ] **Step 1: Delete the constants and thread the config**

Delete `CoverageThreshold` and `freshnessWindow` from `aggregate.go`. Then:

```go
type Store struct {
	pool *pgxpool.Pool
	cfg  config.Store
}

func New(pool *pgxpool.Pool, cfg config.Store) *Store {
	return &Store{pool: pool, cfg: cfg}
}
```

Replace every use with `s.cfg.CoverageThreshold` / `s.cfg.FreshnessWindow`. Note `CoverageThreshold` was **exported** — `go build ./...` will surface any external reader; if `internal/api` reads it to build a response, that reader takes the value from its own `config.Store` rather than reaching into `store`.

- [ ] **Step 2: Fix the test constructors**

Add to the package's test helpers:

```go
// testStoreConfig mirrors airbg.yaml so existing coverage assertions keep
// asserting the same threshold.
func testStoreConfig() config.Store {
	return config.Store{CoverageThreshold: 3, FreshnessWindow: 2 * time.Hour}
}
```

and change every `store.New(pool)` in tests to `store.New(pool, testStoreConfig())`. Change no expected value.

- [ ] **Step 3: Run the package tests**

Run: `go test ./internal/store/ -v`
Expected: PASS. This starts a Postgres container; allow ~2 minutes.

- [ ] **Step 4: Mutation-prove the coverage threshold is still load-bearing**

```bash
cp airbg.yaml /tmp/airbg.yaml.orig
```

Set `store.coverage_threshold` to `1`.

Run: `go test ./internal/config/ -run TestResolveCommittedConfig -v`
Expected: FAIL — `Store.CoverageThreshold = 1, want 3`. Quote it: this is the value that stops one sensor being painted as an oblast.

```bash
cp /tmp/airbg.yaml.orig airbg.yaml && git diff --stat airbg.yaml
```

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "store: take the coverage threshold and freshness window from configuration"
```

---

### Task 11: Sweep internal/ratelimit

**Files:**
- Modify: `internal/ratelimit/bucket.go`, `internal/ratelimit/enumerate.go`
- Test: `internal/ratelimit/*_test.go`

**Interfaces:**
- Consumes: `config.Bucket`, `config.Enumerate`.
- Produces:
  - `func New(cfg config.Bucket) *Limiter` — replaces `New(rate Rate, ttl time.Duration)`. The `Rate` type stays: it is the algorithm's input, and `config.Bucket` carries the shard count and evict interval alongside it.
  - `func NewBreadth(cfg config.Enumerate) *Breadth` — replaces the three positional parameters.
  - `shardCount` becomes `cfg.ShardCount`, stored on the `Limiter`.
  - Deleted: `DistinctAreaLimit`, `DistinctSensorLimit`, `EnumerationWindow`, `shardCount`.

- [ ] **Step 1: Delete the constants and change the constructors**

```go
func New(cfg config.Bucket) *Limiter {
	// Shards reduce lock contention only; the count is a performance knob, not a
	// policy one, which is why it lives beside the rate rather than in its own
	// group.
	shards := make([]*shard, cfg.ShardCount)
	for i := range shards {
		shards[i] = newShard()
	}
	return &Limiter{
		rate:   Rate{PerSecond: cfg.PerSecond, Burst: cfg.Burst},
		ttl:    cfg.TTL,
		shards: shards,
	}
}

func NewBreadth(cfg config.Enumerate) *Breadth {
	return &Breadth{
		areaLimit:   cfg.AreasPerWindow,
		sensorLimit: cfg.SensorsPerWindow,
		window:      cfg.Window,
	}
}
```

Keep the existing internal field names and the existing algorithm untouched. If `Rate`'s fields are typed `int` rather than `float64`, convert at this boundary rather than changing `config.Bucket` — `per_second: 0.5` is a legitimate thing for an operator to want and the config type should not lose it.

- [ ] **Step 2: Fix the test constructors**

```go
func testAPIBucket() config.Bucket {
	return config.Bucket{PerSecond: 10, Burst: 60, TTL: 30 * time.Minute, EvictInterval: 5 * time.Minute, RetryAfter: 2 * time.Second}
}

func testEnumerate() config.Enumerate {
	return config.Enumerate{AreasPerWindow: 12, SensorsPerWindow: 40, Window: time.Hour, RetryAfter: 900 * time.Second}
}
```

`config.Bucket` has no `ShardCount` field: the shard count is `RateLimit.ShardCount`, one level up, shared by both buckets. It is passed as a second argument rather than copied into `config.Bucket` during resolve — duplicating one value into two structs is exactly the quiet divergence this phase exists to remove. Hence the signature:

```go
func New(cfg config.Bucket, shardCount int) *Limiter
```

and `Step 1`'s `make([]*shard, cfg.ShardCount)` becomes `make([]*shard, shardCount)`.

- [ ] **Step 3: Run the package tests**

Run: `go test ./internal/ratelimit/ -v`
Expected: PASS, no expected value changed.

- [ ] **Step 4: Mutation-prove the enumeration limits**

```bash
cp airbg.yaml /tmp/airbg.yaml.orig
```

Set `ratelimit.enumerate.areas_per_window` to `0`.

Run: `go test ./internal/config/ -run TestValidateRejects -v`
Expected: FAIL is **not** what happens here — validation *rejects* it, so the correct expectation is that `TestCommittedConfigValidates` fails with `ratelimit.enumerate.areas_per_window = 0, must be greater than zero`. Run that test instead and quote the output. Zero would mean every area request is denied.

```bash
cp /tmp/airbg.yaml.orig airbg.yaml && git diff --stat airbg.yaml
```

- [ ] **Step 5: Commit**

```bash
git add internal/ratelimit/
git commit -m "ratelimit: take rates, windows and shard count from configuration"
```

---

### Task 12: Sweep internal/db

**Files:**
- Modify: `internal/db/db.go`, `internal/db/timeout.go`
- Test: `internal/db/*_test.go` (testcontainers; ~97s)

**Interfaces:**
- Consumes: `config.Database`, `config.StatementTimeouts`.
- Produces:
  - `func Open(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error)`
  - `func OpenPair(ctx context.Context, cfg config.Database) (api, collector *pgxpool.Pool, err error)` — the conn counts come from `cfg`, so the positional `apiConns, collectorConns` parameters go away.
  - Deleted: the `"15000"` literal in `Open`, and `AssignStatementTimeout`, `OperatorStatementTimeout`, `SeriesStatementTimeout` from `timeout.go`. Their consumers take a `config.StatementTimeouts` instead.

- [ ] **Step 1: Replace the RuntimeParam literal**

The current line is `cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"` — a bare integer, which PostgreSQL reads as milliseconds. Configuration carries a `time.Duration`, so convert explicitly rather than formatting a duration and hoping:

```go
	// PostgreSQL reads a bare statement_timeout as milliseconds. Formatting the
	// duration instead ("15s") also works, but an explicit millisecond
	// conversion is the one that cannot be broken by a unit-suffix change.
	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] =
		strconv.FormatInt(dbCfg.StatementTimeouts.Default.Milliseconds(), 10)
```

Rename the local `cfg` (the pgx pool config) if it now collides with the incoming `config.Database` — the current code names the pool config `cfg`.

- [ ] **Step 2: Convert the scoped timeouts**

The three exported string constants become methods or plain formatting at the call site. Keep them strings, because they are interpolated into `SET LOCAL statement_timeout`:

```go
// StatementTimeoutValue renders a duration for SET LOCAL statement_timeout.
// Milliseconds, explicitly: "10min" is valid PostgreSQL but only by accident of
// its parser, and a duration whose String() changes would change the statement.
func StatementTimeoutValue(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}
```

Every site that used `SeriesStatementTimeout` now uses `db.StatementTimeoutValue(cfg.Database.StatementTimeouts.Series)`.

- [ ] **Step 3: Run the package tests**

Run: `go test ./internal/db/ -v`
Expected: PASS. Container startup ~97s.

- [ ] **Step 4: Verify the timeout is actually applied**

The scoped-timeout tests are the ones most at risk of being inert, because a timeout that is never applied looks identical to one that is never hit.

```bash
cp airbg.yaml /tmp/airbg.yaml.orig
```

Set `database.statement_timeouts.series` to `"1ms"`.

Run: `go test ./internal/db/ -run Timeout -v`
Expected: the series-scoped test FAILS with a PostgreSQL `canceling statement due to statement timeout` error. That failure is the proof the value reaches the session. Quote it. If the test passes with a 1 ms budget, the timeout is not being applied and that is a real bug to report, not to work around.

```bash
cp /tmp/airbg.yaml.orig airbg.yaml && git diff --stat airbg.yaml
```

- [ ] **Step 5: Commit**

```bash
git add internal/db/
git commit -m "db: take pool sizes and statement timeouts from configuration"
```

---

### Task 13: Sweep internal/upstream and internal/backfill

**Files:**
- Modify: `internal/upstream/client.go`, `internal/backfill/backfill.go`
- Test: `internal/upstream/*_test.go`, `internal/backfill/*_test.go`

**Interfaces:**
- Consumes: `config.Upstream`, `config.Backfill`.
- Produces:
  - `func New(cfg config.Upstream) *Client` — replaces `New(baseURL string, timeout time.Duration)`. `maxPayloadBytes` moves onto the client from `cfg.MaxPayloadBytes`.
  - `func NewReport(cfg config.Backfill) *Report` (or the equivalent existing constructor) taking `HighRejectionFraction`; delete the package constant.

- [ ] **Step 1: Change the upstream constructor**

```go
func New(cfg config.Upstream) *Client {
	return &Client{
		baseURL:     cfg.URL,
		maxPayload:  cfg.MaxPayloadBytes,
		http:        &http.Client{Timeout: cfg.RequestTimeout},
	}
}
```

Delete `maxPayloadBytes`. Every `io.LimitReader(resp.Body, maxPayloadBytes)` becomes `io.LimitReader(resp.Body, c.maxPayload)`.

- [ ] **Step 2: Change the backfill threshold**

Delete `HighRejectionFraction` and take it from `config.Backfill`. If the current code reads the constant from a free function, that function grows a `cfg config.Backfill` parameter rather than a package variable — a package variable would be a default in code by another name.

- [ ] **Step 3: Fix the tests**

```go
func testUpstreamConfig(url string) config.Upstream {
	return config.Upstream{
		URL:             url,
		RequestTimeout:  30 * time.Second,
		PollInterval:    5 * time.Minute,
		MinPollInterval: 30 * time.Second,
		MaxPayloadBytes: 64 << 20,
	}
}
```

The oversized-payload test must keep passing with its own small limit — it should construct a config with a deliberately tiny `MaxPayloadBytes` rather than relying on the production value.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/upstream/ ./internal/backfill/ -v`
Expected: PASS.

- [ ] **Step 5: Mutation-prove the payload cap**

```bash
cp internal/upstream/client.go /tmp/client.go.orig
```

Change the `io.LimitReader(resp.Body, c.maxPayload)` call to read `resp.Body` directly.

Run: `go test ./internal/upstream/ -run Payload -v`
Expected: FAIL — the oversized-payload test no longer sees a truncated read. Quote the output; a payload cap that is configurable but unenforced is worse than a constant.

```bash
cp /tmp/client.go.orig internal/upstream/client.go && git diff --stat internal/upstream/client.go
```

- [ ] **Step 6: Commit**

```bash
git add internal/upstream/ internal/backfill/
git commit -m "upstream,backfill: take timeouts, payload cap and thresholds from configuration"
```

---

### Task 14: Sweep internal/api

**Files:**
- Modify: `internal/api/router.go`, `internal/api/series.go`, `internal/api/sensors.go`
- Test: `internal/api/*_test.go`

**Interfaces:**
- Consumes: `config.Config`.
- Produces:
  - `api.Deps` gains `Config config.Config` and drops `BaseURL string` (it is `Config.Listen.BaseURL`). `Snapshots`, `Breadth`, `Store`, `SeriesLimiter`, `Admission` stay as they are — they are collaborators, not values.
  - Deleted: `dataMaxAge`, `scalesMaxAge`, the `periods` map, the `seriesMaxAge` map, `seriesRate`, `seriesBucketTTL`, `seriesEvictInterval`, and both `Retry-After` literals (`"2"` in `series.go`, `"900"` twice in `sensors.go`).
  - **Kept in code:** `cachePublic` and `cachePrivate`. Which responses a shared cache may store is a security control, not an operator tunable — a `public` on an enumerable, per-sensor response is a cache-poisoning and scraping amplifier.
  - `func NewSeriesLimiter(cfg config.Config) *ratelimit.Limiter` — takes the config instead of reading package constants.
  - `func parsePeriod(cfg config.Series, v string) (time.Duration, bool, bool)` and `func maxAgeFor(cfg config.Series, period string) int` — the maps become lookups into `cfg.Series.Periods`.
  - `func ParsePeriodForTesting(cfg config.Series, v string) (time.Duration, bool, bool)` — the existing exported test hook keeps existing, with the config parameter added. It is what asserts the raw/hourly cut-over directly.

- [ ] **Step 1: Replace the period tables**

```go
// parsePeriod resolves a caller's ?period= against the configured table. The
// hourly flag is not a performance hint but a correctness requirement: raw
// readings are retained for 30 days, so a 1-year window against `reading`
// silently returns the last 30 days under a "1 year" label — a chart that is
// wrong without being empty.
func parsePeriod(cfg config.Series, v string) (time.Duration, bool, bool) {
	p, ok := cfg.Periods[v]
	return p.Window, p.Hourly, ok
}

// maxAgeFor is per period because each value needs its own justification: a 24h
// chart of raw readings is a live view whose right edge moves every few
// minutes, while a 1-year chart is hourly rollups where refetching repaints one
// pixel of 8,760 and re-runs the heaviest query in the service.
//
// An unrecognised period cannot reach here (parsePeriod rejects it), but a zero
// max-age would mean no caching at all, so fall back to the shared lifetime.
func maxAgeFor(cfg config.Config, period string) int {
	if p, ok := cfg.Series.Periods[period]; ok && p.MaxAge > 0 {
		return int(p.MaxAge.Seconds())
	}
	return int(cfg.Cache.DataMaxAge.Seconds())
}
```

Note the signature asymmetry is deliberate: `parsePeriod` needs only `config.Series`, `maxAgeFor` needs the `Cache` fallback too. Do not widen `parsePeriod` to the whole `Config` to make them match — the narrower parameter documents what the function reads.

- [ ] **Step 2: Replace the Retry-After literals**

```go
	// Retry-After in seconds, from the bucket that rejected the request.
	w.Header().Set("Retry-After", strconv.Itoa(int(d.Config.RateLimit.Series.RetryAfter.Seconds())))
```

and in `sensors.go`, both sites:

```go
	w.Header().Set("Retry-After", strconv.Itoa(int(d.Config.RateLimit.Enumerate.RetryAfter.Seconds())))
```

The 900 in `sensors.go` is the enumeration window's penalty, not the token bucket's — keep them mapped to the group they came from. Getting these two crossed would make a breadth rejection tell the client to retry in 2 seconds.

- [ ] **Step 3: Replace the cache-control lifetimes**

`dataMaxAge` → `int(d.Config.Cache.DataMaxAge.Seconds())`, `scalesMaxAge` → `int(d.Config.Cache.ScalesMaxAge.Seconds())`. `cachePublic`/`cachePrivate` are untouched.

- [ ] **Step 4: Fix the tests**

Add one helper to the package's test files:

```go
// testConfig is the committed configuration, loaded once, so API tests assert
// against the values the service actually ships with rather than a second copy
// that can drift.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := config.LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	return cfg
}
```

This is the one place in the sweep where tests read the real file rather than a hand-built struct: the API's cache headers and Retry-After values are exactly what an operator would notice changing, so the assertions should be pinned to the shipped file.

Set `Config: testConfig(t)` in every `api.Deps` literal, and drop `BaseURL:`.

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/api/ -v`
Expected: PASS with no expected header value changed. If a `max-age` assertion has to move, the sweep changed behaviour.

- [ ] **Step 6: Mutation-prove the Retry-After mapping**

```bash
cp internal/api/sensors.go /tmp/sensors.go.orig
```

Change one of the two `sensors.go` sites to `d.Config.RateLimit.Series.RetryAfter`.

Run: `go test ./internal/api/ -run 'Retry|Breadth|Enumerat' -v`
Expected: FAIL — a breadth rejection now advertises `Retry-After: 2` instead of `900`. Quote the output. If nothing fails, the enumeration `Retry-After` is asserted by no test and one must be added before this task can complete.

```bash
cp /tmp/sensors.go.orig internal/api/sensors.go && git diff --stat internal/api/sensors.go
```

- [ ] **Step 7: Commit**

```bash
git add internal/api/
git commit -m "api: take cache lifetimes, periods and retry hints from configuration"
```

---

### Task 15: Sweep internal/server and internal/snapshot

**Files:**
- Modify: `internal/server/server.go`, `internal/snapshot/snapshot.go`
- Test: `internal/server/*_test.go` (including `e2e_test.go`, build tag `integration`), `internal/snapshot/*_test.go`

**Interfaces:**
- Consumes: `config.Config`.
- Produces:
  - `server.Options` collapses to: `Config config.Config`, `Catalogue *i18n.Catalogue`, `Snapshots *snapshot.Holder`, `Store api.DataSource`, `Publisher *Publisher`, `Logger *slog.Logger`. Deleted fields: `ListenAddr`, `MetricsAddr`, `TrustedProxyCIDRs`, `BaseURL`, `BasemapStyleURL`, `CSP`, `MaxDBInflight`, `MaxConns`.
  - `func NewHolder(cfg config.Series) *Holder` — replaces `NewHolder()`.
  - Deleted: `readHeaderTimeout`, `readTimeout`, `writeTimeout`, `idleTimeout`, `shutdownGrace`, `evictInterval`, `apiRate`, `bucketTTL`, `defaultMaxConns` from `server.go`; `DefaultSeriesMetric`, `DefaultSeriesWindow` from `snapshot.go`.

- [ ] **Step 1: Collapse Options and rewire the constructor**

```go
// Options carries the collaborators plus the whole validated configuration.
// One Config field rather than fifteen scalars: adding a knob then changes no
// signature, and there is no second place for a value to be forgotten.
type Options struct {
	Config    config.Config
	Catalogue *i18n.Catalogue
	Snapshots *snapshot.Holder
	Store     api.DataSource
	Publisher *Publisher
	Logger    *slog.Logger
}
```

and inside `New`:

```go
	limiter := ratelimit.New(opts.Config.RateLimit.API, opts.Config.RateLimit.ShardCount)
	seriesLimiter := api.NewSeriesLimiter(opts.Config)
	breadth := ratelimit.NewBreadth(opts.Config.RateLimit.Enumerate)

	httpSrv := &http.Server{
		Addr:              opts.Config.Listen.Addr,
		ReadHeaderTimeout: opts.Config.Timeouts.ReadHeader,
		ReadTimeout:       opts.Config.Timeouts.Read,
		WriteTimeout:      opts.Config.Timeouts.Write,
		IdleTimeout:       opts.Config.Timeouts.Idle,
	}
```

The eviction goroutine's ticker takes `opts.Config.RateLimit.API.EvictInterval`; shutdown takes `opts.Config.Timeouts.ShutdownGrace`; the connection cap takes `opts.Config.Listen.MaxConns`; the admission semaphore takes `opts.Config.Database.MaxInflight`; the security headers take `opts.Config.Listen.CSP` and `opts.Config.Listen.PermissionsPolicy`.

`internal/httpx` keeps `CSPValue` and `PermissionsPolicyValue` **only if** something still reads them. If nothing does after this task, delete both — a constant nobody reads is a second source of truth waiting to diverge from the file. Their explanatory comments must move to `airbg.yaml` first; Task 3 already carries them, so verify the intent survived and then delete.

- [ ] **Step 2: Snapshot's default series**

```go
// NewHolder takes the series configuration because the snapshot serves the
// default window without touching the database. That window must equal the one
// api.parsePeriod derives from the configured periods — config.Validate
// enforces it, which is why this constructor can simply trust it.
func NewHolder(cfg config.Series) *Holder {
	return &Holder{
		metric: cfg.DefaultMetric,
		window: cfg.DefaultWindow,
	}
}
```

- [ ] **Step 3: Fix the tests**

Every `server.New(server.Options{...})` and `snapshot.NewHolder()` call site changes. Use the same `testConfig(t)` helper shape as Task 14 (each package needs its own copy — a shared test helper package is a bigger change than this phase should make).

`internal/server/e2e_test.go` carries `//go:build integration` and must be updated too — it is easy to miss because the default test run does not compile it.

- [ ] **Step 4: Run both suites**

```bash
go test ./internal/server/ ./internal/snapshot/ -v
go test -tags=integration ./internal/server/ -v
```

Expected: PASS both. The second is the one that catches an unupdated `e2e_test.go`.

- [ ] **Step 5: Verify the whole tree builds**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output from any of the three. This is the moment the cutover's compile-error list from Task 8 should be empty — compare against `/tmp/phase3b-cutover-errors.txt` and report any line still outstanding.

- [ ] **Step 6: Commit**

```bash
git add internal/server/ internal/snapshot/ internal/httpx/
git commit -m "server,snapshot: take the whole configuration through Options"
```

---

### Task 16: Frontend — data-\* attributes and theme.css

**Files:**
- Create: `internal/web/static/theme.css`
- Modify: `internal/web/static/app.css`, `internal/web/templates/base.gohtml`, `internal/web/templates/area.gohtml`, `internal/web/templates/index.gohtml`, `internal/web/render.go`, `internal/web/pages.go`
- Modify: `web/src/lib/colour.js`, `web/src/lib/tier.js`, `web/src/islands/map.js`, `web/src/islands/chart.js`
- Test: `internal/web/render_test.go`, `web/src/lib/*.test.js`, plus a new literal-scan test

**Interfaces:**
- Consumes: `config.Frontend`, `config.Series`.
- Produces: the page-data struct grows the frontend fields; every island reads its paint colours and zoom thresholds from `dataset`.

Two colour homes, one mechanical boundary: **if CSS can style it, it belongs in `theme.css`; if JS hands it to a canvas or a GL layer, it belongs in `config.Frontend` and arrives as a `data-*` attribute.** The cost is real — an operator retheming the site edits two files — and it is accepted, because three of the four JS colours are paint values no CSS rule can ever reach.

- [ ] **Step 1: Split the palette into theme.css**

Create `internal/web/static/theme.css` holding only the custom properties, including the literals currently inlined in `app.css`:

```css
/* The site palette, and nothing else. No selectors, no layout: this file exists
   so a retheme is one file, and so the CSP never needs a style-src allowance
   for an inline <style> block or a style= attribute. */
:root {
  --fg: #1a1a1a;
  --muted: #666;
  --bg: #fff;
  --accent: #0b6;
  --warn: #a40;
  --border: #ddd;
  --border-faint: #eee;
  --notice-bg: #fff8f0;
  --map-placeholder-bg: #f2f2f2;
  --overlay-bg: rgba(255, 255, 255, .9);
}
```

Then in `app.css`: delete the `:root` line and replace all eight remaining literals with the variables — `#ddd` → `var(--border)`, `#eee` → `var(--border-faint)`, `#fff8f0` → `var(--notice-bg)`, `#f2f2f2` → `var(--map-placeholder-bg)`, `rgba(255, 255, 255, .9)` → `var(--overlay-bg)`.

Add the stylesheet to `base.gohtml` **before** `app.css`, since `app.css` consumes its variables:

```html
  <link rel="stylesheet" href="/static/theme.css?{{.AssetVersion}}">
```

Match the existing `app.css` link's cache-busting parameter exactly — whatever `base.gohtml` currently uses.

- [ ] **Step 2: Add a literal-colour scan to app.css's tests**

Append to `internal/web/assets_test.go`:

```go
// app.css must hold no literal colours: they belong in theme.css, which is the
// one file a retheme touches. A hex literal here is a colour that silently
// escapes the palette.
func TestAppCSSHasNoLiteralColours(t *testing.T) {
	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	for _, bad := range []string{"#", "rgb(", "rgba("} {
		if strings.Contains(string(data), bad) {
			t.Errorf("app.css contains %q; colours belong in theme.css as custom properties", bad)
		}
	}
}
```

`staticFS` is whatever the package already names its embedded filesystem — check `assets.go` and use that identifier. Note this test also forbids `#` in CSS comments and ID selectors: `app.css` currently has both (`#map`, `#chart`, and a `/* … */` comment). Scope the check to colour syntax instead — match `#` followed by three or six hex digits:

```go
var cssColour = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba?\(`)

func TestAppCSSHasNoLiteralColours(t *testing.T) {
	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if found := cssColour.FindAllString(string(data), -1); len(found) != 0 {
		t.Errorf("app.css contains literal colours %v; they belong in theme.css as custom properties", found)
	}
}
```

- [ ] **Step 3: Render the frontend config as data-\* attributes**

In `internal/web/render.go`, the page-data struct gains:

```go
	// Frontend paint values and zoom thresholds. They reach the browser as
	// data-* attributes because the CSP has no 'unsafe-inline' — there is no
	// inline <script> to put a config object in, and there never will be.
	NoDataColour       string
	MarkerStrokeColour string
	EmptyBasemapColour string
	ChartLineColour    string
	ZoomCity           int
	ZoomSensor         int
	DefaultMetric      string
	DefaultPeriod      string
```

`DefaultMetric` comes from `cfg.Series.DefaultMetric` and `DefaultPeriod` from `cfg.Series.PeriodNames[0]` — the templates currently hardcode `data-metric="P2"` and `data-period="24h"` in three places.

In `area.gohtml`, the chart island:

```html
  <div id="chart" data-island="chart"
       data-slug="{{.Area.Slug}}"
       data-metric="{{.DefaultMetric}}"
       data-period="{{.DefaultPeriod}}"
       data-line-colour="{{.ChartLineColour}}"
```

and the map islands in both `area.gohtml` and `index.gohtml`:

```html
     data-metric="{{.DefaultMetric}}"
     data-basemap="{{.BasemapStyleURL}}"
     data-no-data-colour="{{.NoDataColour}}"
     data-marker-stroke-colour="{{.MarkerStrokeColour}}"
     data-empty-basemap-colour="{{.EmptyBasemapColour}}"
     data-zoom-city="{{.ZoomCity}}"
     data-zoom-sensor="{{.ZoomSensor}}"
```

Go's `html/template` escapes these in attribute context, which is what makes a server-rendered colour safe to interpolate. Do not build any of these strings with `template.HTML` or `template.JS`.

- [ ] **Step 4: Consume them in the islands**

`web/src/lib/colour.js` — the module-level constant becomes a parameter. Its header comment currently asserts that it and chrome colours are "the only literal colours permitted in web/src", which stops being true; rewrite it rather than leaving a false claim:

```js
// colourFor picks the first band whose inclusive upper bound is at or above the
// value. bands come verbatim from /api/v1/scales, ascending, with the top band's
// upper === null meaning "open ended".
//
// noDataColour is passed in, not defined here: it arrives from the server as a
// data-* attribute, because this project keeps no defaults in code. Band colours
// still come only from /api/v1/scales, so a legislative change stays a one-file
// server edit rather than a frontend release.
export function colourFor(value, bands, noDataColour) {
  if (value === null || value === undefined || Number.isNaN(value)) return noDataColour
  if (!bands || bands.length === 0) return noDataColour
  for (const band of bands) {
    // upper is INCLUSIVE: a value exactly on a boundary belongs to the lower
    // band. `upper == null` catches both null and undefined and is the open top.
    if (band.upper == null || value <= band.upper) return band.colour
  }
  // Reachable only if the scale has no open top band, which would be a server
  // bug. The no-data colour rather than the last band's: better to show
  // "unknown" than to assert a band the scale does not actually claim.
  return noDataColour
}
```

Delete the `NO_DATA_COLOUR` export. Every caller now passes the colour in. `web/src/lib/tier.js`:

```js
// Which data source a zoom level may read. This is the anti-scraping design
// expressed client-side: the map picks a TIER, never a viewport query, because
// no endpoint accepts a bounding box.
//
// Boundaries are `<`, not `<=`, and each one is pinned by its own assertion. At
// the sensor boundary an off-by-one is the difference between one cached country
// aggregate and a per-area request that spends enumeration budget. The
// thresholds are configuration and arrive as data-* attributes.
export function tierFor(zoom, zoomCity, zoomSensor) {
  if (zoom < zoomCity) return 'country'
  if (zoom < zoomSensor) return 'city'
  return 'sensors'
}
```

`map.js` reads `el.dataset.noDataColour`, `el.dataset.markerStrokeColour`, `el.dataset.emptyBasemapColour`, and `Number(el.dataset.zoomCity)` / `Number(el.dataset.zoomSensor)`, and threads them into `colourFor` and `tierFor`. `chart.js` reads `el.dataset.lineColour` for the uPlot stroke.

Delete the `|| 'P2'` and `|| '24h'` fallbacks in `chart.js` — a fallback in code is a default in code, and the server now always renders both attributes. A missing attribute should surface as a visible failure, not a quiet substitution.

- [ ] **Step 5: Update the Vitest suites and add the literal scan**

Every `colourFor(v, bands)` call in `web/src/lib/colour.test.js` gains a third argument; every `tierFor(z)` gains two. Change no expected result. Then add `web/src/lib/literals.test.js`:

```js
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect } from 'vitest'

// Paint values handed to WebGL layers and the chart canvas are configuration:
// no CSS rule can reach them, so theme.css cannot hold them and they arrive as
// data-* attributes. A hex literal anywhere in web/src is one that escaped.
const roots = ['web/src/lib', 'web/src/islands']

describe('no literal colours in web/src', () => {
  for (const root of roots) {
    for (const name of readdirSync(root).filter((f) => f.endsWith('.js') && !f.endsWith('.test.js'))) {
      it(`${root}/${name}`, () => {
        const src = readFileSync(join(root, name), 'utf8')
        // Strip comments first: the rationale comments legitimately name colours.
        const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
        expect(code.match(/#[0-9a-fA-F]{3,8}\b/g)).toBeNull()
      })
    }
  }
})
```

Adjust `roots` to be relative to whatever Vitest's `root` is configured as in `vite.config.js` — verify by running the test and reading the resolution error, not by guessing.

- [ ] **Step 6: Run both suites**

```bash
npm test --prefix web
go test ./internal/web/ -v
```

Expected: PASS both. The Go render tests must assert the new attributes are present with the configured values — if `render_test.go` has no assertion naming `data-no-data-colour`, add one, because otherwise the whole rendering path is unverified.

- [ ] **Step 7: Mutation-prove the boundary and the wiring**

```bash
cp web/src/lib/tier.js /tmp/tier.js.orig
```

Change `if (zoom < zoomCity)` to `if (zoom <= zoomCity)`.

Run: `npm test --prefix web`
Expected: FAIL on the tier boundary assertion. Quote it — this is the off-by-one that spends enumeration budget.

```bash
cp /tmp/tier.js.orig web/src/lib/tier.js && git diff --stat web/src/lib/tier.js
```

Then the render path:

```bash
cp airbg.yaml /tmp/airbg.yaml.orig
```

Set `frontend.no_data_colour` to `"#ff00ff"`.

Run: `go test ./internal/web/ -run Render -v`
Expected: FAIL, because the rendered attribute no longer matches the asserted colour. If it passes, the attribute reaches no assertion and one must be added.

```bash
cp /tmp/airbg.yaml.orig airbg.yaml && git diff --stat airbg.yaml
```

- [ ] **Step 8: Commit**

```bash
git add internal/web/ web/src/
git commit -m "web: serve the palette from theme.css and paint values as data attributes"
```

---

### Task 17: The validate-config subcommand

**Files:**
- Create: `cmd/airbg/validate.go`
- Modify: `cmd/airbg/main.go`
- Test: `cmd/airbg/validate_test.go`

**Interfaces:**
- Consumes: `config.Load`.
- Produces: `airbg validate-config` — loads the configuration exactly as the server would, prints either the resolved values or the full problem list, and exits 0 or 1. This is what makes a config error findable before a deploy rather than during one.

- [ ] **Step 1: Write the subcommand**

Create `cmd/airbg/validate.go`:

```go
package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"airbg.org/internal/config"
)

// runValidateConfig loads the configuration the same way the server does and
// reports it. Exit code, not just output: this is meant to be a deploy gate.
func runValidateConfig(stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "listen.addr\t"+cfg.Listen.Addr)
	fmt.Fprintln(w, "listen.metrics_addr\t"+cfg.Listen.MetricsAddr)
	fmt.Fprintln(w, "listen.base_url\t"+cfg.Listen.BaseURL)
	fmt.Fprintf(w, "listen.max_conns\t%d\n", cfg.Listen.MaxConns)
	fmt.Fprintf(w, "database.api_conns\t%d\n", cfg.Database.APIConns)
	fmt.Fprintf(w, "database.collector_conns\t%d\n", cfg.Database.CollectorConns)
	fmt.Fprintf(w, "database.max_inflight\t%d\n", cfg.Database.MaxInflight)
	fmt.Fprintf(w, "upstream.poll_interval\t%v\n", cfg.Upstream.PollInterval)
	fmt.Fprintf(w, "cache.data_max_age\t%v\n", cfg.Cache.DataMaxAge)
	fmt.Fprintf(w, "ratelimit.api\t%v/s burst %v\n", cfg.RateLimit.API.PerSecond, cfg.RateLimit.API.Burst)
	fmt.Fprintf(w, "ratelimit.series\t%v/s burst %v\n", cfg.RateLimit.Series.PerSecond, cfg.RateLimit.Series.Burst)
	fmt.Fprintf(w, "ratelimit.enumerate\t%d areas, %d sensors per %v\n",
		cfg.RateLimit.Enumerate.AreasPerWindow, cfg.RateLimit.Enumerate.SensorsPerWindow, cfg.RateLimit.Enumerate.Window)
	fmt.Fprintf(w, "store.coverage_threshold\t%d\n", cfg.Store.CoverageThreshold)
	// Secrets are reported as present/absent, never printed. A validate command
	// that echoes a connection string is a credential in every CI log that runs
	// it.
	fmt.Fprintf(w, "%s\t%s\n", config.DatabaseURLEnv, presence(cfg.Database.URL))
	fmt.Fprintf(w, "%s\t%s\n", config.BasemapKeyEnv, presence(cfg.Basemap.Key))
	w.Flush()
	fmt.Fprintln(stdout, "configuration is valid")
	return 0
}

func presence(v string) string {
	if v == "" {
		return "(not set)"
	}
	return "(set)"
}
```

Wire it in `main.go` before any database or listener setup:

```go
	if len(os.Args) > 1 && os.Args[1] == "validate-config" {
		os.Exit(runValidateConfig(os.Stdout, os.Stderr))
	}
```

- [ ] **Step 2: Write the test**

Create `cmd/airbg/validate_test.go`:

```go
package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"airbg.org/internal/config"
)

func TestValidateConfigAcceptsCommittedFile(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 0 {
		t.Fatalf("runValidateConfig = %d, want 0; stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "configuration is valid") {
		t.Errorf("stdout does not confirm validity:\n%s", out.String())
	}
}

// The command must never print a credential: it runs in CI logs.
func TestValidateConfigNeverPrintsSecrets(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:hunter2@localhost:5432/airbg")
	t.Setenv(config.BasemapKeyEnv, "s3cr3tk3y")
	var out, errOut bytes.Buffer
	runValidateConfig(&out, &errOut)
	combined := out.String() + errOut.String()
	for _, secret := range []string{"hunter2", "s3cr3tk3y"} {
		if strings.Contains(combined, secret) {
			t.Errorf("output contains the secret %q:\n%s", secret, combined)
		}
	}
}

func TestValidateConfigRejectsMissingFile(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join(t.TempDir(), "absent.yaml"))
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 1 {
		t.Fatalf("runValidateConfig = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "cannot read") {
		t.Errorf("stderr = %q, want it to name the unreadable file", errOut.String())
	}
}

func TestValidateConfigRejectsUnsetPath(t *testing.T) {
	t.Setenv(config.PathEnv, "")
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 1 {
		t.Fatalf("runValidateConfig = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), config.PathEnv) {
		t.Errorf("stderr = %q, want it to name %s", errOut.String(), config.PathEnv)
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./cmd/airbg/ -v`
Expected: PASS.

- [ ] **Step 4: Mutation-prove the secret redaction**

```bash
cp cmd/airbg/validate.go /tmp/validate-cmd.go.orig
```

Change the `DatabaseURLEnv` line to print `cfg.Database.URL` instead of `presence(cfg.Database.URL)`.

Run: `go test ./cmd/airbg/ -run TestValidateConfigNeverPrintsSecrets -v`
Expected: FAIL — `output contains the secret "hunter2"`. Quote it.

```bash
cp /tmp/validate-cmd.go.orig cmd/airbg/validate.go && git diff --stat cmd/airbg/validate.go
```

- [ ] **Step 5: Commit**

```bash
git add cmd/airbg/
git commit -m "cmd: add airbg validate-config"
```

---

### Task 18: Documentation, image, and the inertness proof

**Files:**
- Create: `docs/configuration.md`
- Modify: `README.md`, `Dockerfile`, `docker-compose.yml`
- Test: `internal/config/inert_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: the operator-facing documentation, an image that carries a config file, and the test that makes "behaviour unchanged" a checked claim rather than a promise.

- [ ] **Step 1: Write the inertness test**

This is the primary test target for the whole phase. Every value in `airbg.yaml` must equal the constant it replaced; this test is the single place that record lives, so a future retune is a deliberate edit to a test with a reason attached rather than a silent drift.

Create `internal/config/inert_test.go`:

```go
package config

import (
	"path/filepath"
	"testing"
	"time"
)

// TestShippedValuesMatchPhase2Behaviour pins every value that Phase 3b moved out
// of code. The want column is the constant as it existed before the sweep,
// named in the comment. A failure here means the configuration sweep changed
// behaviour — which it is not allowed to do.
//
// Retuning any of these later is legitimate. Changing this test without saying
// why in the commit message is not.
func TestShippedValuesMatchPhase2Behaviour(t *testing.T) {
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}

	t.Run("durations", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			got  time.Duration
			want time.Duration
		}{
			{"timeouts.read_header", cfg.Timeouts.ReadHeader, 5 * time.Second},
			{"timeouts.read", cfg.Timeouts.Read, 10 * time.Second},
			{"timeouts.write", cfg.Timeouts.Write, 30 * time.Second},
			{"timeouts.idle", cfg.Timeouts.Idle, 60 * time.Second},
			{"timeouts.shutdown_grace", cfg.Timeouts.ShutdownGrace, 15 * time.Second},
			{"database.statement_timeouts.default", cfg.Database.StatementTimeouts.Default, 15 * time.Second},
			{"database.statement_timeouts.assign", cfg.Database.StatementTimeouts.Assign, 60 * time.Second},
			{"database.statement_timeouts.operator", cfg.Database.StatementTimeouts.Operator, 10 * time.Minute},
			{"database.statement_timeouts.series", cfg.Database.StatementTimeouts.Series, 5 * time.Second},
			{"ratelimit.api.ttl", cfg.RateLimit.API.TTL, 30 * time.Minute},
			{"ratelimit.api.evict_interval", cfg.RateLimit.API.EvictInterval, 5 * time.Minute},
			{"ratelimit.series.ttl", cfg.RateLimit.Series.TTL, 30 * time.Minute},
			{"ratelimit.series.evict_interval", cfg.RateLimit.Series.EvictInterval, 5 * time.Minute},
			{"ratelimit.series.retry_after", cfg.RateLimit.Series.RetryAfter, 2 * time.Second},
			{"ratelimit.enumerate.window", cfg.RateLimit.Enumerate.Window, time.Hour},
			{"ratelimit.enumerate.retry_after", cfg.RateLimit.Enumerate.RetryAfter, 900 * time.Second},
			{"cache.data_max_age", cfg.Cache.DataMaxAge, 150 * time.Second},
			{"cache.scales_max_age", cfg.Cache.ScalesMaxAge, 86400 * time.Second},
			{"upstream.request_timeout", cfg.Upstream.RequestTimeout, 30 * time.Second},
			{"upstream.poll_interval", cfg.Upstream.PollInterval, 5 * time.Minute},
			{"upstream.min_poll_interval", cfg.Upstream.MinPollInterval, 30 * time.Second},
			{"store.freshness_window", cfg.Store.FreshnessWindow, 2 * time.Hour},
			{"series.default_window", cfg.Series.DefaultWindow, 24 * time.Hour},
		} {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		}
	})

	t.Run("numbers", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			got  float64
			want float64
		}{
			{"listen.max_conns", float64(cfg.Listen.MaxConns), 4096},
			{"database.api_conns", float64(cfg.Database.APIConns), 8},
			{"database.collector_conns", float64(cfg.Database.CollectorConns), 4},
			{"database.max_inflight", float64(cfg.Database.MaxInflight), 16},
			{"ratelimit.api.per_second", cfg.RateLimit.API.PerSecond, 10},
			{"ratelimit.api.burst", cfg.RateLimit.API.Burst, 60},
			{"ratelimit.series.per_second", cfg.RateLimit.Series.PerSecond, 1},
			{"ratelimit.series.burst", cfg.RateLimit.Series.Burst, 10},
			{"ratelimit.enumerate.areas_per_window", float64(cfg.RateLimit.Enumerate.AreasPerWindow), 12},
			{"ratelimit.enumerate.sensors_per_window", float64(cfg.RateLimit.Enumerate.SensorsPerWindow), 40},
			{"ratelimit.shard_count", float64(cfg.RateLimit.ShardCount), 32},
			{"upstream.max_payload_bytes", float64(cfg.Upstream.MaxPayloadBytes), 64 << 20},
			{"store.coverage_threshold", float64(cfg.Store.CoverageThreshold), 3},
			{"quality.min_neighbours", float64(cfg.Quality.MinNeighbours), 3},
			{"quality.mad_scale", cfg.Quality.MADScale, 1.4826},
			{"quality.mad_threshold", cfg.Quality.MADThreshold, 3.5},
			{"quality.neighbour_radius_metres", cfg.Quality.NeighbourRadiusMetres, 15000},
			{"quality.earth_radius_metres", cfg.Quality.EarthRadiusMetres, 6371000},
			{"quality.history_depth", float64(cfg.Quality.HistoryDepth), 12},
			{"backfill.high_rejection_fraction", cfg.Backfill.HighRejectionFraction, 0.5},
			{"frontend.zoom_city", float64(cfg.Frontend.ZoomCity), 9},
			{"frontend.zoom_sensor", float64(cfg.Frontend.ZoomSensor), 11},
		} {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		}
	})

	t.Run("ranges", func(t *testing.T) {
		want := map[string]Range{
			"P1":           {0, 1000},
			"P2":           {0, 1000},
			"temperature":  {-40, 60},
			"humidity":     {0, 100},
			"pressure":     {650, 1100},
			"noise_LAeq":   {25, 120},
			"noise_LA_max": {25, 120},
		}
		for metric, w := range want {
			if got := cfg.Quality.Ranges[metric]; got != w {
				t.Errorf("quality.ranges.%s = %+v, want %+v", metric, got, w)
			}
		}
	})

	t.Run("strings", func(t *testing.T) {
		for _, tt := range []struct{ name, got, want string }{
			{"listen.addr", cfg.Listen.Addr, "127.0.0.1:8080"},
			{"listen.metrics_addr", cfg.Listen.MetricsAddr, "127.0.0.1:9090"},
			{"listen.base_url", cfg.Listen.BaseURL, "http://localhost:8080"},
			{"upstream.url", cfg.Upstream.URL, "https://data.sensor.community/airrohr/v1/filter/country=BG"},
			{"series.default_metric", cfg.Series.DefaultMetric, "P2"},
			{"frontend.no_data_colour", cfg.Frontend.NoDataColour, "#9ca3af"},
			{"frontend.marker_stroke_colour", cfg.Frontend.MarkerStrokeColour, "#ffffff"},
			{"frontend.empty_basemap_colour", cfg.Frontend.EmptyBasemapColour, "#eef2f5"},
			{"frontend.chart_line_colour", cfg.Frontend.ChartLineColour, "#2563eb"},
		} {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		}
	})

	t.Run("csp", func(t *testing.T) {
		// The exact Phase 1 §9.7 policy, reassembled. The YAML folded scalar must
		// produce this byte for byte, or the shipped policy is not the reviewed one.
		want := "default-src 'self'; script-src 'self'; style-src 'self'; " +
			"img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; " +
			"worker-src 'self' blob:; object-src 'none'; base-uri 'none'; " +
			"form-action 'none'; frame-ancestors 'none'"
		if cfg.Listen.CSP != want {
			t.Errorf("listen.csp =\n  %q\nwant\n  %q", cfg.Listen.CSP, want)
		}
	})
}
```

The `csp` subtest is the one most likely to fail first: YAML's `>-` folded scalar joins lines with single spaces, so the file's line breaks must fall exactly where a space belongs. If it fails, fix the **file**, not the expected string.

- [ ] **Step 2: Run it**

Run: `go test ./internal/config/ -run TestShippedValues -v`
Expected: PASS, five subtests. Any failure names a value the sweep changed — correct the YAML.

- [ ] **Step 3: Write docs/configuration.md**

Create `docs/configuration.md` covering, in this order:

1. **The two layers** — file then environment, no defaults in code. A missing key is a startup error.
2. **Where the file comes from** — `AIRBG_CONFIG` is required; there is no fallback path.
3. **The override rule** — `AIRBG_` + key path, uppercased, dots to underscores, with the four worked examples from this plan.
4. **The two env-only secrets** — `AIRBG_DATABASE_URL`, `AIRBG_BASEMAP_KEY`, and the fact that writing either into the file is a hard rejection.
5. **What cannot be overridden from the environment** — `series.periods`, because there is no sane name for a list entry's field. Edit the file.
6. **The renamed variables** — the full table from this plan's Environment Override Rule section, stated as a breaking change with the old names no longer read.
7. **`airbg validate-config`** — what it prints, that it never prints secrets, and that exit code 1 means do not deploy.
8. **The two colour homes** — `internal/web/static/theme.css` for anything CSS can style, `frontend.*` for the four paint values handed to WebGL and the chart canvas, with the mechanical boundary rule and the note that band colours come from `/api/v1/scales` and are in neither place.
9. **The checked couplings** — `listen.addr` ≠ `listen.metrics_addr`, `cache.data_max_age` ≤ `upstream.poll_interval / 2`, `upstream.poll_interval` ≥ `min_poll_interval`, `series.default_window` must match a configured period, `statement_timeouts.series` ≤ `.default`, `zoom_city` < `zoom_sensor`, and the CSP's refusal to contain `unsafe-inline` or `unsafe-eval`. Each with one line on why.

Then update `README.md`: the run instructions need `AIRBG_CONFIG`, the environment table needs the new names, and it should link to `docs/configuration.md` rather than duplicating it.

- [ ] **Step 4: Update the image**

In the `Dockerfile`'s final stage:

```dockerfile
COPY --from=build /src/airbg.yaml /etc/airbg/airbg.yaml
ENV AIRBG_CONFIG=/etc/airbg/airbg.yaml
```

The file is mandatory, so an image without one cannot start. A deployment overrides it by mounting its own file at that path or by pointing `AIRBG_CONFIG` elsewhere — both are documented in step 3.

Confirm no secret enters the image:

```bash
docker build -t airbg:phase3b .
docker run --rm airbg:phase3b validate-config
```

Expected: exit 1 with `AIRBG_DATABASE_URL is not set in the environment` — which is the correct result for an image that carries no credential. Quote it.

- [ ] **Step 5: Run everything**

```bash
gofmt -l .
go vet ./...
go test ./...
go test -tags=integration ./internal/server/
npm test --prefix web
```

Expected: `gofmt` and `vet` silent; all Go packages `ok`; integration suite `ok`; Vitest all green. Budget 2–8 minutes for the Go suite (testcontainers).

Then confirm no package outside `internal/config` reads the environment:

```bash
grep -rn 'os.Getenv\|os.LookupEnv' --include='*.go' . | grep -v '^./internal/config/' | grep -v '_test.go'
```

Expected: no output. A hit is a value that escaped the sweep — report it rather than leaving it.

- [ ] **Step 6: Commit**

```bash
git add docs/configuration.md README.md Dockerfile docker-compose.yml internal/config/inert_test.go
git commit -m "docs: document airbg.yaml and pin the shipped values"
```

---

## Notes for the reviewer

- The sweep's completeness check is `go build ./...` plus the `os.Getenv` grep in Task 18 Step 5, not a checklist. Deleting a constant rather than shadowing it is what makes that true; a task that leaves a constant in place "for compatibility" has broken the mechanism.
- Every mutation step exists because twelve Phase 2 tests once passed while inert. A task report that says "mutation confirmed" without quoting the real failure output has not done the step.
- Behaviour-unchanged is asserted in exactly one place, `internal/config/inert_test.go`. If a task needs to change a value there to pass, the answer is to fix the code, not the test.
