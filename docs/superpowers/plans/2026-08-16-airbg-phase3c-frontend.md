# Phase 3c Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every metric, every sensor's history, and the visitor's own area reachable from the map, and put all three test tiers into CI.

**Architecture:** Pure-JS logic modules (`web/src/lib/*.js`) hold every decision and carry the proofs; a Svelte 5 runes store (`lib/viewstate.svelte.js`) holds client state mirrored into the URL hash; thin `.svelte` components render; MapLibre stays imperative and subscribes to the store. Spec: `docs/superpowers/specs/2026-08-16-airbg-phase3c-frontend-design.md`.

**Tech Stack:** Go 1.25 (stdlib + pgx/goose/testcontainers), Svelte 5 (runes), Vite, Vitest, MapLibre GL, uPlot, Playwright (Chromium), PostgreSQL + PostGIS + TimescaleDB.

## Global Constraints

Every task's requirements implicitly include this section.

- **Exactly two new devDependencies across the whole plan: `jsdom` and `@playwright/test`.** Both pinned to an exact version (no `^`, no `~`), as `maplibre-gl`, `pmtiles` and `uplot` already are. No new runtime dependency. **No new Go dependency** — `go.mod`'s require block must not gain a line.
- **No defaults compiled into the binary.** A missing config key is a startup error. The one new key `frontend.unscaled_colour` must ship in `airbg.yaml`, be validated, appear in `validate-config`'s table, and be documented.
- **No user-visible string literal anywhere in `web/src/`.** Strings arrive from the server as `data-t-*` attributes and are passed to components as props. New copy goes in BOTH `internal/i18n/bg.json` and `internal/i18n/en.json`.
- **No colour, metric name, or threshold literal in `web/src/`.** `web/src/lib/literals.test.js` enforces the colour half and must be extended to cover `src/components/*.svelte`.
- **Logic in `web/src/lib/*.js`, markup in `web/src/components/*.svelte`.** A component may not contain a comparison, a fallback, or arithmetic that a pure module could hold.
- **Every new test must be mutation-proven.** After writing a test, delete or invert the line it covers, re-run, paste the failure into the report, restore. A test that passes with its subject removed is not a test.
- All SQL through pgx parameterised queries, test helpers included. No string-concatenated SQL.
- `www-root/` must not be modified. `CLAUDE.md` must never be staged. No `Co-Authored-By: Claude` trailer and no "Generated with Claude Code" line in any commit or PR.
- No endpoint may accept a bounding box or an unbounded list parameter. This plan adds **no API endpoint at all**.
- Rate-limit numbers in `airbg.yaml` are not changed by this plan.

## File Structure

**Created:**

| File | Responsibility |
|---|---|
| `web/src/lib/viewstate.js` | Pure: parse/serialise `#metric=&sensor=`, validate both keys |
| `web/src/lib/viewstate.svelte.js` | `$state` store, `hashchange` listener, push/replace policy |
| `web/src/lib/metrics.js` | Canonical metric list handling, `hasScale`, unit lookup |
| `web/src/lib/nearest.js` | Pure: nearest area centroid to a coordinate |
| `web/src/components/Chart.svelte` | uPlot wrapper (logic moved out of `islands/chart.js`) |
| `web/src/components/MetricSwitcher.svelte` | Metric buttons, writes the store |
| `web/src/components/SensorPanel.svelte` | Per-sensor values, quality flag, embedded chart |
| `web/src/islands/switcher.js` | Mounts `MetricSwitcher` |
| `web/src/islands/panel.js` | Mounts `SensorPanel` |
| `internal/e2e/e2e_test.go` | `//go:build e2e` — boots stack, runs Playwright |
| `web/playwright.config.js` | Playwright configuration |
| `web/e2e/*.spec.js` | Browser specs |

**Modified:** `web/src/islands/chart.js` (reduced to a mounter), `web/src/islands/map.js`, `web/src/main.js`, `web/src/lib/literals.test.js`, `web/package.json`, `internal/config/{schema,resolve,validate}.go`, `cmd/airbg/validate.go`, `airbg.yaml`, `.env.example`, `docs/configuration.md`, `internal/web/render.go`, `internal/web/templates/{index,area}.gohtml`, `internal/i18n/{bg,en}.json`, `.github/workflows/ci.yml`.

---

### Task 1: The `frontend.unscaled_colour` config key

The map must be able to say "this metric has no air-quality scale" in a colour that is not `no_data_colour`. Config, not a constant, because it is a paint value handed to a GL layer.

**Files:**
- Modify: `internal/config/schema.go`, `internal/config/resolve.go`, `internal/config/validate.go`, `cmd/airbg/validate.go`, `internal/web/render.go`, `internal/web/templates/index.gohtml`, `internal/web/templates/area.gohtml`, `airbg.yaml`, `.env.example`, `docs/configuration.md`
- Test: `internal/config/validate_test.go`, `cmd/airbg/validate_test.go`, `internal/web/render_test.go`

**Interfaces:**
- Produces: `config.Frontend.UnscaledColour string`; YAML key `frontend.unscaled_colour`; env override `AIRBG_FRONTEND_UNSCALED_COLOUR`; template field `.UnscaledColour`; DOM attribute `data-unscaled-colour` on every `data-island="map"` container.

- [ ] **Step 1: Write the failing config test**

In `internal/config/validate_test.go`, follow the file's existing table style:

```go
func TestValidateRejectsEmptyUnscaledColour(t *testing.T) {
	c := validConfig(t)
	c.Frontend.UnscaledColour = ""
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "frontend.unscaled_colour") {
		t.Fatalf("Validate() = %v, want an error naming frontend.unscaled_colour", err)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/config/ -run TestValidateRejectsEmptyUnscaledColour`
Expected: FAIL — `c.Frontend.UnscaledColour` undefined.

- [ ] **Step 3: Add the field through all four config layers**

`internal/config/schema.go`, beside `NoDataColour`:

```go
	UnscaledColour     *string `yaml:"unscaled_colour"`
```

`internal/config/resolve.go`, in the `Frontend` struct beside `NoDataColour`:

```go
	UnscaledColour     string
```

and in the assembled value:

```go
			UnscaledColour:     *r.Frontend.UnscaledColour,
```

`internal/config/validate.go`, in the same required-non-empty map that holds `"frontend.no_data_colour"`:

```go
		"frontend.unscaled_colour":      c.Frontend.UnscaledColour,
```

Find the env-override table that maps `frontend.no_data_colour` to `AIRBG_FRONTEND_NO_DATA_COLOUR` and add the parallel `unscaled_colour` entry the same way — the file's existing mechanism, not a new one.

- [ ] **Step 4: Ship the value**

`airbg.yaml`, in the `frontend:` block directly after `no_data_colour`:

```yaml
  # Shown when the selected metric has NO air-quality scale — temperature,
  # humidity, pressure and the two noise metrics, none of which /api/v1/scales
  # publishes bands for. Deliberately NOT no_data_colour: "we have no reading"
  # and "this metric has no band table" are different facts, and painting both
  # the same grey makes a working map indistinguishable from a broken one.
  # A muted blue-grey, distinct from both the no-data grey and every band colour.
  unscaled_colour: "#94a3b8"
```

Add the matching commented line to `.env.example` and a row to `docs/configuration.md` alongside the other `frontend.*` keys.

- [ ] **Step 5: Run the config test**

Run: `go test ./internal/config/`
Expected: PASS.

- [ ] **Step 6: Write the failing validate-config and render tests**

`cmd/airbg/validate_test.go` — extend the existing key loop in `TestValidateConfigShowsEmptyTilesKeys`'s sibling test for frontend keys if one exists; otherwise add:

```go
func TestValidateConfigShowsUnscaledColour(t *testing.T) {
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	t.Setenv(config.DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	var out, errOut bytes.Buffer
	if code := runValidateConfig(&out, &errOut); code != 0 {
		t.Fatalf("runValidateConfig = %d, want 0; stderr:\n%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "frontend.unscaled_colour") {
		t.Errorf("stdout does not mention frontend.unscaled_colour:\n%s", out.String())
	}
}
```

`internal/web/render_test.go` — the page must carry the value to the browser:

```go
func TestIndexRendersUnscaledColour(t *testing.T) {
	body := renderIndex(t) // the file's existing helper
	if !strings.Contains(body, `data-unscaled-colour="#94a3b8"`) {
		t.Errorf("index page does not render data-unscaled-colour:\n%s", body)
	}
}
```

- [ ] **Step 7: Run both and watch them fail**

Run: `go test ./cmd/airbg/ ./internal/web/`
Expected: FAIL — the row is absent from the table, the attribute absent from the page.

- [ ] **Step 8: Wire the value to the browser**

`cmd/airbg/validate.go`: add `frontend.unscaled_colour` to the printed table beside `frontend.no_data_colour`.

`internal/web/render.go`: add `UnscaledColour string` to `PageData` beside `NoDataColour`, and `UnscaledColour: rr.frontend.UnscaledColour,` in `newPageData`.

Both templates, on every `data-island="map"` div, directly after `data-no-data-colour`:

```gohtml
     data-unscaled-colour="{{.UnscaledColour}}"
```

- [ ] **Step 9: Run the suites**

Run: `go test ./... && gofmt -l . && go vet ./...`
Expected: PASS, no gofmt output.

- [ ] **Step 10: Mutation-prove**

Delete the `"frontend.unscaled_colour"` line from validate.go's required map, re-run `go test ./internal/config/` — record the failure text — restore. Then change the template attribute to `data-unscaled-colour=""`, re-run `go test ./internal/web/`, record, restore.

- [ ] **Step 11: Commit**

```bash
git add internal/config cmd/airbg internal/web airbg.yaml .env.example docs/configuration.md
git commit -m "config: add frontend.unscaled_colour and render it on the map island"
```

---

### Task 2: `lib/viewstate.js` — the pure hash contract

**Files:**
- Create: `web/src/lib/viewstate.js`, `web/src/lib/__tests__/viewstate.test.js`

**Interfaces:**
- Produces:
  - `parseHash(hash: string, {metrics: string[], defaultMetric: string}) -> {metric: string, sensorId: number|null}`
  - `serialiseHash({metric, sensorId}, defaultMetric: string) -> string` — returns `''` when the state is the default (no sensor, default metric), otherwise `'#metric=…&sensor=…'` omitting whichever key is at its default.

- [ ] **Step 1: Write the failing tests**

`web/src/lib/__tests__/viewstate.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { parseHash, serialiseHash } from '../viewstate.js'

const opts = { metrics: ['P1', 'P2', 'temperature'], defaultMetric: 'P2' }

describe('parseHash', () => {
  it('reads both keys in either order', () => {
    expect(parseHash('#metric=P1&sensor=1234', opts)).toEqual({ metric: 'P1', sensorId: 1234 })
    expect(parseHash('#sensor=1234&metric=P1', opts)).toEqual({ metric: 'P1', sensorId: 1234 })
  })

  it('falls back to the server default for an unknown metric', () => {
    expect(parseHash('#metric=plutonium', opts).metric).toBe('P2')
  })

  // The two keys are independent: a bad sensor id must not take the metric with
  // it. This is the whole reason validation is per-key rather than a single
  // "is this hash valid" check.
  it('keeps a valid metric when the sensor id is junk', () => {
    expect(parseHash('#metric=temperature&sensor=abc', opts))
      .toEqual({ metric: 'temperature', sensorId: null })
  })

  it('rejects zero, negative and fractional sensor ids', () => {
    for (const bad of ['0', '-5', '1.5']) {
      expect(parseHash(`#sensor=${bad}`, opts).sensorId).toBeNull()
    }
  })

  it('ignores unknown keys so a future #period= cannot break old links', () => {
    expect(parseHash('#period=7d&metric=P1', opts)).toEqual({ metric: 'P1', sensorId: null })
  })

  it('treats an empty hash as the default state', () => {
    expect(parseHash('', opts)).toEqual({ metric: 'P2', sensorId: null })
  })
})

describe('serialiseHash', () => {
  it('is empty for the default state, so shared URLs stay clean', () => {
    expect(serialiseHash({ metric: 'P2', sensorId: null }, 'P2')).toBe('')
  })

  it('omits the metric when it is the default', () => {
    expect(serialiseHash({ metric: 'P2', sensorId: 1234 }, 'P2')).toBe('#sensor=1234')
  })

  it('omits the sensor when none is open', () => {
    expect(serialiseHash({ metric: 'P1', sensorId: null }, 'P2')).toBe('#metric=P1')
  })

  it('round-trips through parseHash', () => {
    const state = { metric: 'temperature', sensorId: 77 }
    expect(parseHash(serialiseHash(state, 'P2'), opts)).toEqual(state)
  })
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `cd web && npx vitest run src/lib/__tests__/viewstate.test.js`
Expected: FAIL — cannot resolve `../viewstate.js`.

- [ ] **Step 3: Implement**

`web/src/lib/viewstate.js`:

```js
// The URL hash is the single source of truth for client state. Pure on purpose:
// no DOM, no framework, no globals — the fallback rules below are the ones most
// likely to be got wrong, and they are provable here with plain values.
//
// The hash, not the query string: it never reaches the server, so no page's
// cache key or canonical URL changes when a visitor switches metric.

// parseHash reads the two keys INDEPENDENTLY. A junk sensor id must not
// invalidate a good metric, and vice versa — a single "valid hash" check would
// throw away both on one typo.
//
// metrics and defaultMetric are arguments, never module constants: the metric
// list is the server's (upstream.CanonicalMetrics via data-metrics) and the
// default is series.default_metric. A copy here would be a second home for a
// value airbg.yaml already owns.
export function parseHash(hash, { metrics, defaultMetric }) {
  const params = new URLSearchParams(String(hash || '').replace(/^#/, ''))
  const metric = params.get('metric')
  return {
    metric: metrics.includes(metric) ? metric : defaultMetric,
    sensorId: parseSensorId(params.get('sensor')),
  }
}

// A sensor id is a positive integer. Rejecting '0', '-5' and '1.5' explicitly:
// Number('') is 0 and Number('1.5') is 1.5, so a bare Number() cast would let
// all three through and produce a request for a sensor that cannot exist.
function parseSensorId(raw) {
  if (raw === null || raw === '') return null
  const n = Number(raw)
  if (!Number.isInteger(n) || n <= 0) return null
  return n
}

// serialiseHash writes the WHOLE hash, so the two keys can never disagree with
// what is rendered. Defaults are omitted rather than written out: '#metric=P2'
// on every shared link is noise, and an empty return lets the caller strip the
// '#' from the address bar entirely.
export function serialiseHash({ metric, sensorId }, defaultMetric) {
  const params = new URLSearchParams()
  if (metric && metric !== defaultMetric) params.set('metric', metric)
  if (sensorId !== null && sensorId !== undefined) params.set('sensor', String(sensorId))
  const query = params.toString()
  return query ? `#${query}` : ''
}
```

- [ ] **Step 4: Run and watch it pass**

Run: `cd web && npx vitest run src/lib/__tests__/viewstate.test.js`
Expected: PASS, 10 tests.

- [ ] **Step 5: Mutation-prove**

Three mutations, each re-run and restored, failures pasted into the report:
1. `metrics.includes(metric) ? metric : defaultMetric` → `metric ?? defaultMetric` (must fail the unknown-metric test).
2. `if (!Number.isInteger(n) || n <= 0)` → `if (Number.isNaN(n))` (must fail the zero/negative/fractional test).
3. `if (metric && metric !== defaultMetric)` → `if (metric)` (must fail the clean-URL tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/viewstate.js web/src/lib/__tests__/viewstate.test.js
git commit -m "web: add the pure hash state contract"
```

---

### Task 3: `lib/metrics.js` — which metrics are scaled

**Files:**
- Create: `web/src/lib/metrics.js`, `web/src/lib/__tests__/metrics.test.js`

**Interfaces:**
- Consumes: the `/api/v1/scales` response shape already used by `islands/map.js`'s `bandsFor` — an array of `{name, metric, unit, bands}`.
- Produces:
  - `parseMetricList(raw: string) -> string[]` — splits the server's `data-metrics` attribute
  - `hasScale(scales, metric) -> boolean`
  - `unitFor(scales, metric) -> string` — `''` when unknown

- [ ] **Step 1: Write the failing tests**

`web/src/lib/__tests__/metrics.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { parseMetricList, hasScale, unitFor } from '../metrics.js'

// Shaped exactly like /api/v1/scales: two tables for P2, one for P1, none for
// anything else. The duplicate P2 entry is not padding — it is what the real
// endpoint returns (eaqi and eu_limit), and hasScale must not care.
const scales = [
  { name: 'eaqi', metric: 'P2', unit: 'µg/m³', bands: [{ upper: 5, colour: '#50f0e6' }] },
  { name: 'eaqi', metric: 'P1', unit: 'µg/m³', bands: [{ upper: 20, colour: '#50f0e6' }] },
  { name: 'eu_limit', metric: 'P2', unit: 'µg/m³', bands: [{ upper: 25, colour: '#50ccaa' }] },
]

describe('parseMetricList', () => {
  it('splits the server attribute', () => {
    expect(parseMetricList('P1,P2,temperature')).toEqual(['P1', 'P2', 'temperature'])
  })

  it('tolerates spacing and empty entries', () => {
    expect(parseMetricList(' P1 , ,P2 ')).toEqual(['P1', 'P2'])
  })

  it('returns an empty list for a missing attribute', () => {
    expect(parseMetricList(undefined)).toEqual([])
  })
})

describe('hasScale', () => {
  it('is true only for metrics the server publishes bands for', () => {
    expect(hasScale(scales, 'P1')).toBe(true)
    expect(hasScale(scales, 'P2')).toBe(true)
    expect(hasScale(scales, 'temperature')).toBe(false)
  })

  // A metric with an entry but an EMPTY band list is not scaled: colourFor
  // would paint every marker the no-data colour, which is the uniformly-grey
  // map this whole distinction exists to prevent.
  it('is false for an entry with no bands', () => {
    expect(hasScale([{ metric: 'pressure', bands: [] }], 'pressure')).toBe(false)
  })

  it('is false when the scales failed to load', () => {
    expect(hasScale(null, 'P2')).toBe(false)
  })
})

describe('unitFor', () => {
  it('reads the unit from the first matching table', () => {
    expect(unitFor(scales, 'P1')).toBe('µg/m³')
  })

  it('is empty for a metric with no table', () => {
    expect(unitFor(scales, 'humidity')).toBe('')
  })
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `cd web && npx vitest run src/lib/__tests__/metrics.test.js`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

`web/src/lib/metrics.js`:

```js
// Which metrics exist, and which of them the map can colour.
//
// Neither question is answered by a list in this file. The metric list is the
// server's (upstream.CanonicalMetrics, rendered as data-metrics) and "is it
// scaled" is derived from /api/v1/scales — so publishing pressure bands
// server-side would colour pressure with no frontend release. A hardcoded
// ['P1','P2'] here would be a second home for a fact the server already owns.

export function parseMetricList(raw) {
  return String(raw || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

// A metric is scaled if and only if the server publishes a NON-EMPTY band table
// for it. The emptiness check is load-bearing: colourFor with [] bands returns
// the no-data colour for every value, so an empty table renders exactly like a
// metric with no table at all — and must therefore be treated as one.
export function hasScale(scales, metric) {
  if (!Array.isArray(scales)) return false
  return scales.some((s) => s.metric === metric && Array.isArray(s.bands) && s.bands.length > 0)
}

export function unitFor(scales, metric) {
  if (!Array.isArray(scales)) return ''
  return scales.find((s) => s.metric === metric)?.unit ?? ''
}
```

- [ ] **Step 4: Run and watch it pass**

Run: `cd web && npx vitest run src/lib/__tests__/metrics.test.js`
Expected: PASS, 8 tests.

- [ ] **Step 5: Mutation-prove**

1. Drop `&& s.bands.length > 0` from `hasScale` (must fail the empty-band-list test).
2. Drop `.filter(Boolean)` from `parseMetricList` (must fail the spacing test).

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/metrics.js web/src/lib/__tests__/metrics.test.js
git commit -m "web: derive the scaled-metric set from /api/v1/scales"
```

---

### Task 4: `lib/viewstate.svelte.js` — the reactive store

**Files:**
- Create: `web/src/lib/viewstate.svelte.js`, `web/src/lib/__tests__/viewstate.store.test.js`
- Modify: `web/package.json` (add `jsdom`)

**Interfaces:**
- Consumes: `parseHash`, `serialiseHash` from Task 2.
- Produces: `createViewState({metrics, defaultMetric, win = window}) -> { get metric(), get sensorId(), setMetric(m), openSensor(id), closeSensor(), destroy() }`
  - `setMetric` uses `replaceState`; `openSensor`/`closeSensor` use `pushState`.
  - Reads the current hash on construction and on every `hashchange`.

- [ ] **Step 1: Add jsdom**

```bash
cd web && npm install --save-exact --save-dev jsdom
```

Confirm `package.json` records an exact version (no `^`).

- [ ] **Step 2: Write the failing tests**

`web/src/lib/__tests__/viewstate.store.test.js`:

```js
// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { createViewState } from '../viewstate.svelte.js'

const opts = { metrics: ['P1', 'P2', 'temperature'], defaultMetric: 'P2' }

beforeEach(() => {
  history.replaceState(null, '', '/area/sofia')
})

describe('createViewState', () => {
  it('adopts the hash present at construction', () => {
    history.replaceState(null, '', '/area/sofia#metric=P1&sensor=42')
    const vs = createViewState(opts)
    expect(vs.metric).toBe('P1')
    expect(vs.sensorId).toBe(42)
    vs.destroy()
  })

  // A metric switch is a view setting, not a destination: five toggles must not
  // put five entries in the back stack for the visitor to press Back through.
  it('switches metric without growing the history stack', () => {
    const vs = createViewState(opts)
    const before = history.length
    vs.setMetric('P1')
    vs.setMetric('temperature')
    expect(history.length).toBe(before)
    expect(location.hash).toBe('#metric=temperature')
    vs.destroy()
  })

  // Opening a sensor IS a destination: Back must close the panel.
  it('pushes a history entry when a sensor opens', () => {
    const vs = createViewState(opts)
    const before = history.length
    vs.openSensor(42)
    expect(history.length).toBe(before + 1)
    expect(location.hash).toBe('#sensor=42')
    vs.destroy()
  })

  it('clears the hash entirely when returning to the default state', () => {
    const vs = createViewState(opts)
    vs.setMetric('P1')
    vs.setMetric('P2')
    expect(location.hash).toBe('')
    vs.destroy()
  })

  it('follows an external hash change', () => {
    const vs = createViewState(opts)
    history.replaceState(null, '', '#metric=temperature')
    dispatchEvent(new HashChangeEvent('hashchange'))
    expect(vs.metric).toBe('temperature')
    vs.destroy()
  })

  it('stops listening after destroy', () => {
    const vs = createViewState(opts)
    vs.destroy()
    history.replaceState(null, '', '#metric=temperature')
    dispatchEvent(new HashChangeEvent('hashchange'))
    expect(vs.metric).toBe('P2')
  })
})
```

- [ ] **Step 3: Run and watch it fail**

Run: `cd web && npx vitest run src/lib/__tests__/viewstate.store.test.js`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement**

`web/src/lib/viewstate.svelte.js`:

```js
// The reactive mirror of the URL hash. Every decision lives in viewstate.js;
// this file holds $state, the listener, and the one policy that needs the
// History API: which writes are destinations and which are settings.
import { parseHash, serialiseHash } from './viewstate.js'

export function createViewState({ metrics, defaultMetric, win = globalThis }) {
  const initial = parseHash(win.location.hash, { metrics, defaultMetric })
  let metric = $state(initial.metric)
  let sensorId = $state(initial.sensorId)

  // Programmatic writes change the hash, which fires hashchange, which would
  // re-parse and re-assign what we just set. Harmless for values, but it also
  // fires while the caller is mid-update. The flag makes our own echo a no-op.
  let writing = false

  const onHashChange = () => {
    if (writing) return
    const next = parseHash(win.location.hash, { metrics, defaultMetric })
    metric = next.metric
    sensorId = next.sensorId
  }
  win.addEventListener('hashchange', onHashChange)

  // write() always serialises the WHOLE state, so the two keys cannot disagree
  // with what is rendered. An empty serialisation is written as the bare path,
  // not as '#' — otherwise the address bar keeps a dangling hash forever.
  function write(push) {
    const hash = serialiseHash({ metric, sensorId }, defaultMetric)
    const url = win.location.pathname + win.location.search + hash
    writing = true
    if (push) win.history.pushState(null, '', url)
    else win.history.replaceState(null, '', url)
    writing = false
  }

  return {
    get metric() { return metric },
    get sensorId() { return sensorId },
    // A view setting: replaceState, so the back stack stays navigational.
    setMetric(next) {
      if (!metrics.includes(next) || next === metric) return
      metric = next
      write(false)
    },
    // A destination: pushState, so Back closes the panel and Back again leaves.
    openSensor(id) {
      if (id === sensorId) return
      sensorId = id
      write(true)
    },
    closeSensor() {
      if (sensorId === null) return
      sensorId = null
      write(true)
    },
    destroy() { win.removeEventListener('hashchange', onHashChange) },
  }
}
```

- [ ] **Step 5: Run and watch it pass**

Run: `cd web && npx vitest run src/lib/__tests__/viewstate.store.test.js`
Expected: PASS, 6 tests. If `$state` is not compiled, confirm `vite.config.js` still lists `svelte()` in `plugins` — Vitest loads that config, and vite-plugin-svelte is what compiles `.svelte.js`.

- [ ] **Step 6: Mutation-prove**

1. `write(false)` → `write(true)` in `setMetric` (must fail the history-length test).
2. `write(true)` → `write(false)` in `openSensor` (must fail the push test).
3. Delete the `if (writing) return` guard and confirm the suite still passes — **if it does, the guard is untested**: add a test that calls `setMetric` and asserts the listener did not re-enter, rather than deleting the guard. Record which happened.
4. Delete the `removeEventListener` line (must fail the destroy test).

- [ ] **Step 7: Commit**

```bash
git add web/package.json web/package-lock.json web/src/lib/viewstate.svelte.js web/src/lib/__tests__/viewstate.store.test.js
git commit -m "web: add the reactive view-state store"
```

---

### Task 5: `Chart.svelte` — the first component

Moves uPlot out of the island and into a component the panel can embed. Behaviour must not change: same URL, same three failure messages.

**Files:**
- Create: `web/src/components/Chart.svelte`, `web/src/components/__tests__/chart.component.test.js`
- Modify: `web/src/islands/chart.js`, `web/src/lib/literals.test.js`
- Delete: `web/src/islands/__tests__/chart.test.js` only if every assertion in it has an equivalent in the new component test — otherwise keep both.

**Interfaces:**
- Produces: `Chart.svelte` with props `{ url, lineColour, title, valueLabel, empty, unavailable }`. It fetches `url` via `getJSON`, renders with uPlot, and renders `empty`/`unavailable` text instead when appropriate.
- Consumes: `toUplotData` from `lib/series.js`, `getJSON` from `lib/api.js`.

- [ ] **Step 1: Write the failing component test**

`web/src/components/__tests__/chart.component.test.js`:

```js
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import Chart from '../Chart.svelte'

// uPlot needs layout the jsdom environment does not provide, so it is stubbed:
// this test is about which BRANCH runs and what text the reader ends up with,
// which is exactly the part uPlot cannot tell us.
vi.mock('uplot', () => ({ default: vi.fn(function () { this.setSize = vi.fn() }) }))

const props = {
  url: '/api/v1/area/sofia/series?metric=P2&period=24h',
  lineColour: '#2563eb',
  title: 'PM2.5',
  valueLabel: 'µg/m³',
  empty: 'No readings in this window.',
  unavailable: 'Data is unavailable right now.',
}

let component
afterEach(() => { if (component) unmount(component); vi.restoreAllMocks() })

function render(extra) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(Chart, { target, props: { ...props, ...extra } })
  return target
}

describe('Chart.svelte', () => {
  it('says the data is unavailable when the fetch fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('boom'))
    const target = render({ url: '/api/v1/area/fail/series' })
    await vi.waitFor(() => expect(target.textContent).toContain(props.unavailable))
  })

  // An empty frame with no words on an air-quality page reads as "nothing to
  // report", i.e. as clean air. It must say why instead.
  it('says the window is empty rather than drawing an empty frame', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ t: [], v: [] }), { status: 200 }),
    )
    const target = render({ url: '/api/v1/area/empty/series' })
    await vi.waitFor(() => expect(target.textContent).toContain(props.empty))
  })
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `cd web && npx vitest run src/components/__tests__/chart.component.test.js`
Expected: FAIL — `../Chart.svelte` not found.

- [ ] **Step 3: Implement the component**

`web/src/components/Chart.svelte` — move the body of today's `islands/chart.js` in, unchanged in behaviour:

```svelte
<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { toUplotData } from '../lib/series.js'
  import { getJSON } from '../lib/api.js'

  let { url, lineColour, title, valueLabel, empty, unavailable } = $props()

  // Three states, one variable: the reader must always be told which one they
  // are in. 'loading' renders nothing rather than a spinner — the panel around
  // this component is already on screen with the current values.
  let status = $state('loading')
  let host

  $effect(() => {
    let chart
    let observer
    let cancelled = false

    ;(async () => {
      let body
      try {
        body = await getJSON(url)
      } catch (err) {
        if (!cancelled) status = 'unavailable'
        console.error('chart data:', err)
        return
      }
      if (cancelled) return

      const data = toUplotData(body)
      if (data[0].length === 0) { status = 'empty'; return }
      status = 'ok'

      chart = new uPlot({
        title,
        width: host.clientWidth || 600,
        height: 240,
        // Epoch SECONDS — see lib/series.js. uPlot's x scale is time by
        // default, so milliseconds would plot every point in 1970 silently.
        series: [{}, { label: valueLabel, stroke: lineColour, width: 2 }],
        scales: { x: { time: true } },
      }, data, host)

      // The container is fluid; a chart left at its first-paint width is
      // visibly wrong after a phone rotates. setSize does not re-fetch.
      observer = new ResizeObserver(() => {
        const width = host.clientWidth
        if (width > 0) chart.setSize({ width, height: 240 })
      })
      observer.observe(host)
    })()

    return () => { cancelled = true; observer?.disconnect(); chart?.destroy?.() }
  })
</script>

<div bind:this={host} class="chart-host"></div>
{#if status === 'unavailable'}<p class="chart-message">{unavailable}</p>{/if}
{#if status === 'empty'}<p class="chart-message">{empty}</p>{/if}
```

- [ ] **Step 4: Reduce the island to a mounter**

`web/src/islands/chart.js` becomes:

```js
// The chart island is now only a mount point: every decision lives in
// Chart.svelte, and the URL is built here because the dataset is the island's
// business, not the component's.
import { mount as mountComponent } from 'svelte'
import Chart from '../components/Chart.svelte'

export function mount(el) {
  const d = el.dataset
  if (!d.slug) return // nothing to draw; leave the server-rendered aggregate
  // No fallbacks: the server always renders data-metric and data-period, so a
  // missing one must surface as a visible failure, not a quiet substitution.
  const url = `/api/v1/area/${encodeURIComponent(d.slug)}/series` +
    `?metric=${encodeURIComponent(d.metric)}&period=${encodeURIComponent(d.period)}`
  mountComponent(Chart, {
    target: el,
    props: {
      url,
      lineColour: d.lineColour,
      title: d.tTitle || '',
      valueLabel: d.tValue || '',
      empty: d.tEmpty || '',
      unavailable: d.tUnavailable || '',
    },
  })
}
```

- [ ] **Step 5: Extend the literals guard to components**

`web/src/lib/literals.test.js` — add `src/components` to `roots` and accept `.svelte` alongside `.js`:

```js
const roots = ['src/lib', 'src/islands', 'src/components']
```

and change the filename filter to `(f) => (f.endsWith('.js') || f.endsWith('.svelte')) && !f.endsWith('.test.js')`.

- [ ] **Step 6: Run the full web suite**

Run: `cd web && npm test`
Expected: PASS. The pre-existing island chart test may now fail if it asserted on `islands/chart.js` internals that moved — port each of its assertions into the component test rather than deleting them.

- [ ] **Step 7: Verify the real build still works**

Run: `cd web && npm run build`
Expected: build succeeds and emits a chart chunk. This is the first `.svelte` file in the repo — if the Svelte plugin were misconfigured, this is where it shows.

- [ ] **Step 8: Mutation-prove**

1. Change `if (data[0].length === 0)` to `if (false)` (must fail the empty test).
2. Change the catch branch to `status = 'ok'` (must fail the unavailable test).
3. Add a hex colour literal to `Chart.svelte` and confirm `literals.test.js` fails — this proves Step 5's extension is live, not decorative. Restore.

- [ ] **Step 9: Commit**

```bash
git add web/src/components web/src/islands/chart.js web/src/lib/literals.test.js web/src/islands/__tests__
git commit -m "web: move the chart into a Svelte component"
```

---

### Task 6: The metric switcher

**Files:**
- Create: `web/src/components/MetricSwitcher.svelte`, `web/src/islands/switcher.js`, `web/src/components/__tests__/switcher.component.test.js`
- Modify: `web/src/lib/metrics.js` (+ its test), `web/src/main.js`, `internal/web/render.go`, `internal/web/templates/index.gohtml`, `internal/web/templates/area.gohtml`, `internal/i18n/bg.json`, `internal/i18n/en.json`
- Test: `internal/web/render_test.go`, `internal/i18n/catalogue_test.go` (whichever test asserts key parity between the two files — it will fail until both get the new keys)

**Interfaces:**
- Consumes: `createViewState` (Task 4), `parseMetricList` (Task 3).
- Produces:
  - `metrics.js` gains `zipLabels(metrics: string[], labels: string[]) -> [{metric, label}]`
  - `MetricSwitcher.svelte` props `{ options: [{metric,label}], selected: string, onselect: (metric) => void, legend: string }`
  - `PageData.Metrics []string` and `PageData.MetricLabels []string`
  - DOM: `data-island="switcher"` with `data-metrics`, `data-metric-labels`, `data-metric`, `data-t-legend`

- [ ] **Step 1: Add the label copy to both catalogues**

`internal/i18n/bg.json` and `internal/i18n/en.json` gain one key per canonical metric plus the group label. BG values:

```json
  "metric.P1": "ФПЧ10",
  "metric.P2": "ФПЧ2.5",
  "metric.temperature": "Температура",
  "metric.humidity": "Влажност",
  "metric.pressure": "Атмосферно налягане",
  "metric.noise_LAeq": "Шум (средно)",
  "metric.noise_LA_max": "Шум (макс.)",
  "metric.legend": "Показател",
  "metric.unscaled": "Този показател няма скала за качество на въздуха — точките показват само къде има измервания."
```

EN values:

```json
  "metric.P1": "PM10",
  "metric.P2": "PM2.5",
  "metric.temperature": "Temperature",
  "metric.humidity": "Humidity",
  "metric.pressure": "Pressure",
  "metric.noise_LAeq": "Noise (average)",
  "metric.noise_LA_max": "Noise (max)",
  "metric.legend": "Metric",
  "metric.unscaled": "This metric has no air-quality scale — dots show where readings exist, not how good they are."
```

- [ ] **Step 2: Write the failing server test**

`internal/web/render_test.go`:

```go
func TestPagesRenderTheMetricSwitcher(t *testing.T) {
	body := renderIndex(t)
	// The list is upstream.CanonicalMetrics in ITS order, not a template
	// literal: a second list here is one that silently stops matching.
	if !strings.Contains(body, `data-metrics="P1,P2,temperature,humidity,pressure,noise_LAeq,noise_LA_max"`) {
		t.Errorf("metric list not rendered:\n%s", body)
	}
	if !strings.Contains(body, `data-island="switcher"`) {
		t.Errorf("switcher island container missing:\n%s", body)
	}
	// Labels are positional: the Nth label belongs to the Nth metric. A
	// length mismatch would mislabel every metric after the gap.
	if strings.Count(body, ",") == 0 {
		t.Fatal("no label list rendered")
	}
}
```

Add an assertion in the same file that `len(PageData.Metrics) == len(PageData.MetricLabels)` by rendering and splitting both attributes.

- [ ] **Step 3: Run and watch it fail**

Run: `go test ./internal/web/ -run TestPagesRenderTheMetricSwitcher`
Expected: FAIL — no such attributes.

- [ ] **Step 4: Implement the server side**

`internal/web/render.go` — add to `PageData`:

```go
	// Metrics and MetricLabels are POSITIONAL pairs: MetricLabels[i] names
	// Metrics[i]. Two parallel attributes rather than a JSON blob because the
	// CSP has no 'unsafe-inline' and data-* attributes are the only channel.
	Metrics      []string
	MetricLabels []string
```

Add `func (p PageData) MetricsAttr() string { return strings.Join(p.Metrics, ",") }` and the same for labels, then in `newPageData`:

```go
	metrics := upstream.CanonicalMetrics()
	labels := make([]string, len(metrics))
	for i, m := range metrics {
		labels[i] = rr.cat.T(lang, "metric."+m)
	}
```

assigning both into the returned `PageData`. Confirm `upstream.CanonicalMetrics()` returns a stable order; if it iterates a map, sort it there and note why.

Both templates, above the map island:

```gohtml
<div data-island="switcher"
     data-metrics="{{.MetricsAttr}}"
     data-metric-labels="{{.MetricLabelsAttr}}"
     data-metric="{{.DefaultMetric}}"
     data-t-legend="{{.T "metric.legend"}}"></div>
```

- [ ] **Step 5: Run the Go suites**

Run: `go test ./internal/web/ ./internal/i18n/`
Expected: PASS. If a catalogue-parity test fails, a key is missing from one of the two JSON files.

- [ ] **Step 6: Write the failing component test**

`web/src/components/__tests__/switcher.component.test.js`:

```js
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import MetricSwitcher from '../MetricSwitcher.svelte'

const options = [
  { metric: 'P2', label: 'PM2.5' },
  { metric: 'P1', label: 'PM10' },
  { metric: 'temperature', label: 'Temperature' },
]

let component
afterEach(() => { if (component) unmount(component) })

function render(props) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(MetricSwitcher, { target, props: { options, legend: 'Metric', ...props } })
  return target
}

describe('MetricSwitcher.svelte', () => {
  it('renders one control per metric, labelled by the server', () => {
    const target = render({ selected: 'P2', onselect: () => {} })
    const buttons = target.querySelectorAll('button')
    expect(buttons.length).toBe(3)
    expect([...buttons].map((b) => b.textContent.trim())).toEqual(['PM2.5', 'PM10', 'Temperature'])
  })

  // aria-pressed, not a class: a screen-reader user must be able to tell which
  // metric the map is showing, and a colour change alone does not say it.
  it('marks the selected metric for assistive technology', () => {
    const target = render({ selected: 'P1', onselect: () => {} })
    const pressed = [...target.querySelectorAll('button')].filter((b) => b.getAttribute('aria-pressed') === 'true')
    expect(pressed.map((b) => b.textContent.trim())).toEqual(['PM10'])
  })

  it('reports the chosen metric by its canonical name, not its label', () => {
    const onselect = vi.fn()
    const target = render({ selected: 'P2', onselect })
    target.querySelectorAll('button')[2].click()
    expect(onselect).toHaveBeenCalledWith('temperature')
  })
})
```

- [ ] **Step 7: Run and watch it fail**

Run: `cd web && npx vitest run src/components/__tests__/switcher.component.test.js`
Expected: FAIL — component not found.

- [ ] **Step 8: Implement the component, the island, and `zipLabels`**

`web/src/lib/metrics.js` gains:

```js
// Positional pairing of the two server attributes. Extra labels are dropped and
// missing ones fall back to the metric's own name: a mislabelled control is
// worse than an unlabelled one, and silently shifting labels by one is exactly
// what an unchecked zip does when the two lists disagree.
export function zipLabels(metrics, labels) {
  return metrics.map((metric, i) => ({ metric, label: labels[i] || metric }))
}
```

with a test in the existing metrics test file covering the mismatched-length case.

`web/src/components/MetricSwitcher.svelte`:

```svelte
<script>
  let { options, selected, onselect, legend } = $props()
</script>

<fieldset class="metric-switcher">
  <legend>{legend}</legend>
  {#each options as option (option.metric)}
    <button
      type="button"
      aria-pressed={option.metric === selected}
      onclick={() => onselect(option.metric)}
    >{option.label}</button>
  {/each}
</fieldset>
```

`web/src/islands/switcher.js`:

```js
// The switcher owns no state: it renders what the store says and calls back
// into it. The store, not this island, decides that a metric switch is a
// replaceState — see lib/viewstate.svelte.js.
import { mount as mountComponent } from 'svelte'
import MetricSwitcher from '../components/MetricSwitcher.svelte'
import { parseMetricList, zipLabels } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'

export function mount(el) {
  const d = el.dataset
  const metrics = parseMetricList(d.metrics)
  const vs = getViewState({ metrics, defaultMetric: d.metric })
  mountComponent(MetricSwitcher, {
    target: el,
    props: {
      options: zipLabels(metrics, parseMetricList(d.metricLabels)),
      legend: d.tLegend,
      get selected() { return vs.metric },
      onselect: (m) => vs.setMetric(m),
    },
  })
}
```

This needs a single shared store instance across islands. Add to `lib/viewstate.svelte.js`:

```js
// One store per page, shared by every island. Two independent stores would each
// write the hash and clobber the other's key — the switcher would close the
// panel on every metric change.
let shared = null
export function getViewState(opts) {
  if (shared === null) shared = createViewState(opts)
  return shared
}
```

Register the island in `web/src/main.js` beside the existing entries.

- [ ] **Step 9: Run the web suite and build**

Run: `cd web && npm test && npm run build`
Expected: PASS, build succeeds.

- [ ] **Step 10: Mutation-prove**

1. `aria-pressed={option.metric === selected}` → `aria-pressed={false}` (must fail the a11y test).
2. `onselect(option.metric)` → `onselect(option.label)` (must fail the canonical-name test).
3. Remove `labels[i] || metric` fallback → `labels[i]` and confirm the `zipLabels` mismatch test fails.

- [ ] **Step 11: Commit**

```bash
git add web/src internal/web internal/i18n
git commit -m "web,api: add the metric switcher and the metric list it renders from"
```

---

### Task 7: The map follows the metric

The switcher currently changes nothing on the map. This task subscribes `islands/map.js` to the store, repaints on metric change, and paints unscaled metrics in `unscaled_colour` with the legend note.

**Files:**
- Modify: `web/src/islands/map.js`, `web/src/islands/__tests__/map.test.js`, both templates (`data-t-unscaled` on the map island)

**Interfaces:**
- Consumes: `getViewState`, `hasScale`, `bandsFor`.
- Produces: `markerPaint(bands, {noDataColour, unscaledColour, scaled})` — the existing `markerPaint` gains the unscaled branch; `metricNote(scales, metric, text) -> string` (empty when the metric is scaled).

- [ ] **Step 1: Write the failing tests**

In `web/src/islands/__tests__/map.test.js`, beside the existing `markerPaint` tests:

```js
import { metricNote } from '../map.js'

describe('unscaled metrics', () => {
  const scales = [{ metric: 'P2', bands: [{ upper: 5, colour: '#50f0e6' }] }]

  // Three different facts must not share one colour: "no reading" (grey),
  // "this metric has no band table" (unscaledColour), and a real band value.
  it('paints every marker the unscaled colour when the metric has no scale', () => {
    const paint = markerPaint([], { noDataColour: '#999999', unscaledColour: '#94a3b8', scaled: false })
    expect(JSON.stringify(paint)).toContain('#94a3b8')
    expect(JSON.stringify(paint)).not.toContain('#999999')
  })

  it('still uses the bands when the metric is scaled', () => {
    const paint = markerPaint(scales[0].bands, { noDataColour: '#999999', unscaledColour: '#94a3b8', scaled: true })
    expect(JSON.stringify(paint)).toContain('#50f0e6')
    expect(JSON.stringify(paint)).not.toContain('#94a3b8')
  })

  it('explains an unscaled metric and says nothing for a scaled one', () => {
    expect(metricNote(scales, 'temperature', 'no scale')).toBe('no scale')
    expect(metricNote(scales, 'P2', 'no scale')).toBe('')
  })
})
```

Add one behavioural test that a metric change triggers a repaint, using the map test file's existing fake-map harness:

```js
it('repaints when the store metric changes', async () => {
  const { map, chrome } = mountTestMap({ metric: 'P2' })
  location.hash = '#metric=P1'
  dispatchEvent(new HashChangeEvent('hashchange'))
  await vi.waitFor(() => expect(map.setPaintProperty).toHaveBeenCalled())
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `cd web && npx vitest run src/islands/__tests__/map.test.js`
Expected: FAIL — `metricNote` is not exported and `markerPaint` takes no `scaled`.

- [ ] **Step 3: Implement**

In `web/src/islands/map.js`:

```js
// An unscaled metric gets ONE colour for every marker with a reading. The
// alternative — falling through to noDataColour — makes a working temperature
// map indistinguishable from a broken PM map, which is the failure this whole
// branch exists to remove.
export function markerPaint(bands, { noDataColour, unscaledColour, scaled }) {
  if (!scaled) return ['case', ['has', 'value'], unscaledColour, noDataColour]
  // …existing band expression unchanged…
}

// The note is the only thing telling a reader why every dot is the same colour.
// Returned rather than rendered here so the caller owns the DOM.
export function metricNote(scales, metric, text) {
  return hasScale(scales, metric) ? '' : text
}
```

In `mount(el)`, after the store exists:

```js
  const vs = getViewState({ metrics: parseMetricList(cfg.metrics), defaultMetric: cfg.metric })
  // $effect.root because this island is not a component: without a root, the
  // effect has no owner and never runs.
  const stop = $effect.root(() => {
    $effect(() => {
      const metric = vs.metric
      repaint(metric)
      chrome.showNote(metricNote(scales, metric, cfg.t.unscaled))
    })
  })
```

`repaint(metric)` re-derives `bandsFor(scales, metric)` and calls `map.setPaintProperty` for the marker layers; it must NOT re-create the map or refetch tiles. It must also re-request data for the new metric through the existing `refresh()` path if the endpoint is metric-scoped — check `initData`/`urlFor` and reuse whichever already carries the metric rather than adding a parameter.

Both templates gain `data-t-unscaled="{{.T "metric.unscaled"}}"` and `data-metrics="{{.MetricsAttr}}"` on the map island; `readConfig` reads both with no fallback, matching the file's existing rule.

- [ ] **Step 4: Run and watch it pass**

Run: `cd web && npm test`
Expected: PASS.

- [ ] **Step 5: Mutation-prove**

1. `if (!scaled)` → `if (false)` (must fail the unscaled paint test).
2. `hasScale(...) ? '' : text` → `''` (must fail the note test).
3. Delete the `$effect` body's `repaint(metric)` call (must fail the repaint test).

- [ ] **Step 6: Commit**

```bash
git add web/src internal/web/templates
git commit -m "web: repaint the map when the metric changes, and mark unscaled metrics"
```

---

### Task 8: `SensorPanel.svelte`

**Files:**
- Create: `web/src/lib/sensorview.js`, `web/src/lib/__tests__/sensorview.test.js`, `web/src/components/SensorPanel.svelte`, `web/src/components/__tests__/panel.component.test.js`
- Modify: `internal/i18n/bg.json`, `internal/i18n/en.json`

**Interfaces:**
- Produces:
  - `panelRows(sensor, options, scales) -> [{metric, label, value, unit, missing: boolean}]`
  - `SensorPanel.svelte` props `{ sensor, rows, title, flagText, closeLabel, onclose, chart }` where `chart` is a Svelte snippet or `null`.

- [ ] **Step 0: Confirm the sensor shape**

Read `sensorFeatures` in `web/src/islands/map.js` and the handler behind `/api/v1/area/{slug}/sensors` in `internal/api/`. Write down the exact property names a sensor feature carries (id, per-metric values, quality flag) and use those names verbatim below — do not rename them in the frontend.

- [ ] **Step 1: Add the panel copy to both catalogues**

Keys, with BG values then EN:

```json
  "panel.title": "Сензор {{id}}" / "Sensor {{id}}"
  "panel.close": "Затвори" / "Close"
  "panel.no_value": "няма измерване" / "no reading"
  "panel.flag.ok": "" / ""
  "panel.flag.suspect": "Измерванията изглеждат съмнителни." / "These readings look suspect."
  "panel.flag.stale": "Последното измерване е остаряло." / "The latest reading is stale."
```

Add one key per value in `internal/quality/flag.go` — read the file and cover every flag it defines, not only the three above. If the catalogue is not templated, render the id in the component instead of the string and drop the `{{id}}` form.

- [ ] **Step 2: Write the failing pure test**

`web/src/lib/__tests__/sensorview.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { panelRows } from '../sensorview.js'

const options = [
  { metric: 'P2', label: 'PM2.5' },
  { metric: 'P1', label: 'PM10' },
  { metric: 'temperature', label: 'Temperature' },
]
const scales = [{ metric: 'P2', unit: 'µg/m³', bands: [] }, { metric: 'P1', unit: 'µg/m³', bands: [] }]

describe('panelRows', () => {
  it('lists every metric the sensor reports, in the switcher order', () => {
    const rows = panelRows({ values: { P1: 30, P2: 12 } }, options, scales)
    expect(rows.map((r) => r.metric)).toEqual(['P2', 'P1'])
  })

  // A metric the sensor does not measure is omitted; a metric it measures but
  // has no CURRENT value for is kept and marked missing. Collapsing the two
  // would tell a reader a working sensor does not measure PM10.
  it('keeps a reported metric with no current value and marks it missing', () => {
    const rows = panelRows({ values: { P1: null, P2: 12 } }, options, scales)
    expect(rows.find((r) => r.metric === 'P1')).toMatchObject({ missing: true, value: null })
  })

  it('carries the unit from the scales response', () => {
    expect(panelRows({ values: { P2: 12 } }, options, scales)[0].unit).toBe('µg/m³')
  })

  it('leaves the unit empty for a metric with no scale table', () => {
    const rows = panelRows({ values: { temperature: 21 } }, options, scales)
    expect(rows[0].unit).toBe('')
  })

  it('returns nothing for a sensor with no values at all', () => {
    expect(panelRows({ values: {} }, options, scales)).toEqual([])
  })
})
```

- [ ] **Step 3: Run and watch it fail**

Run: `cd web && npx vitest run src/lib/__tests__/sensorview.test.js`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement `sensorview.js`**

```js
import { unitFor } from './metrics.js'

// Rows follow the SWITCHER's order, not the object key order: JS object key
// order is insertion order, which here is the server's JSON order — stable
// today, and not something the panel's layout should depend on.
//
// A metric absent from sensor.values is not reported by this sensor and is
// omitted. A metric present with a null value IS reported and is kept, marked
// missing — "no reading right now" and "does not measure this" are different
// facts about the hardware.
export function panelRows(sensor, options, scales) {
  const values = sensor?.values ?? {}
  return options
    .filter(({ metric }) => Object.hasOwn(values, metric))
    .map(({ metric, label }) => ({
      metric,
      label,
      value: values[metric] ?? null,
      unit: unitFor(scales, metric),
      missing: values[metric] === null || values[metric] === undefined,
    }))
}
```

- [ ] **Step 5: Write the failing component test**

`web/src/components/__tests__/panel.component.test.js`:

```js
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import SensorPanel from '../SensorPanel.svelte'

const rows = [
  { metric: 'P2', label: 'PM2.5', value: 12.4, unit: 'µg/m³', missing: false },
  { metric: 'P1', label: 'PM10', value: null, unit: 'µg/m³', missing: true },
]

let component
afterEach(() => { if (component) unmount(component) })

function render(props) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(SensorPanel, {
    target,
    props: { rows, title: 'Sensor 42', flagText: '', closeLabel: 'Close', noValue: 'no reading', onclose: () => {}, ...props },
  })
  return target
}

describe('SensorPanel.svelte', () => {
  it('shows each row with its value and unit', () => {
    const target = render()
    expect(target.textContent).toContain('PM2.5')
    expect(target.textContent).toContain('12.4')
    expect(target.textContent).toContain('µg/m³')
  })

  // A blank cell reads as zero on an air-quality page. It must say so in words.
  it('spells out a missing value instead of leaving a blank', () => {
    const target = render()
    expect(target.textContent).toContain('no reading')
  })

  it('shows the quality warning only when there is one', () => {
    expect(render({ flagText: 'These readings look suspect.' }).textContent).toContain('suspect')
    expect(render({ flagText: '' }).querySelector('.panel-flag')).toBeNull()
  })

  it('closes on the close control and on Escape', () => {
    const onclose = vi.fn()
    const target = render({ onclose })
    target.querySelector('[data-close]').click()
    expect(onclose).toHaveBeenCalledTimes(1)
    target.querySelector('[role="dialog"]').dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
    )
    expect(onclose).toHaveBeenCalledTimes(2)
  })
})
```

- [ ] **Step 6: Run and watch it fail**

Run: `cd web && npx vitest run src/components/__tests__/panel.component.test.js`
Expected: FAIL — component not found.

- [ ] **Step 7: Implement the component**

`web/src/components/SensorPanel.svelte`:

```svelte
<script>
  let { rows, title, flagText, closeLabel, noValue, onclose, chart = null } = $props()
</script>

<!-- role=dialog + aria-label, and Escape closes: the panel covers the map on a
     phone, so a keyboard user who cannot reach the close control is trapped. -->
<section
  class="sensor-panel"
  role="dialog"
  aria-label={title}
  tabindex="-1"
  onkeydown={(e) => { if (e.key === 'Escape') onclose() }}
>
  <header>
    <h2>{title}</h2>
    <button type="button" data-close onclick={onclose}>{closeLabel}</button>
  </header>

  {#if flagText}<p class="panel-flag">{flagText}</p>{/if}

  <dl>
    {#each rows as row (row.metric)}
      <dt>{row.label}</dt>
      <dd>{#if row.missing}{noValue}{:else}{row.value} {row.unit}{/if}</dd>
    {/each}
  </dl>

  {#if chart}{@render chart()}{/if}
</section>
```

- [ ] **Step 8: Run and build**

Run: `cd web && npm test && npm run build`
Expected: PASS.

- [ ] **Step 9: Mutation-prove**

1. Drop the `.filter(Object.hasOwn…)` line (must fail the "lists every metric the sensor reports" test).
2. `missing: false` hardcoded (must fail the missing-value tests).
3. Remove the `onkeydown` handler (must fail the Escape test).

- [ ] **Step 10: Commit**

```bash
git add web/src internal/i18n
git commit -m "web,api: add the sensor panel component and its copy"
```

---

### Task 9: Wiring the panel — click, deep link, chart

**Files:**
- Create: `web/src/islands/panel.js`
- Modify: `web/src/islands/map.js`, `web/src/main.js`, `internal/web/templates/area.gohtml`, `web/src/islands/__tests__/map.test.js`
- Test: `web/src/islands/__tests__/panel.test.js`

**Interfaces:**
- Consumes: `getViewState`, `panelRows`, `SensorPanel`, `Chart`.
- Produces: `normaliseSensor(feature) -> {id, values, flag}` exported from `islands/panel.js`; the panel island reads sensors from the map island's last-loaded feature set via a module-level registry in `lib/sensors.svelte.js` — **not** by refetching.

- [ ] **Step 1: Write the failing tests**

`web/src/islands/__tests__/panel.test.js`:

```js
// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { normaliseSensor } from '../panel.js'
import { setSensors, findSensor } from '../../lib/sensors.svelte.js'

beforeEach(() => setSensors([]))

describe('normaliseSensor', () => {
  it('flattens a GeoJSON feature into the shape panelRows expects', () => {
    const f = { properties: { id: 42, flag: 'ok', P1: 30, P2: 12 } }
    expect(normaliseSensor(f)).toEqual({ id: 42, flag: 'ok', values: { P1: 30, P2: 12 } })
  })

  // id and flag are metadata, not measurements: leaking them into values would
  // put a row labelled "flag" in the panel.
  it('keeps metadata out of the values map', () => {
    expect(normaliseSensor({ properties: { id: 1, flag: 'stale' } }).values).toEqual({})
  })
})

describe('the sensor registry', () => {
  // The panel reads what the map already loaded. Refetching would double every
  // page's request count against a per-IP enumeration limiter that counts
  // distinct sensor ids — the panel would burn the visitor's budget twice.
  it('finds a sensor the map published', () => {
    setSensors([{ properties: { id: 42, flag: 'ok', P2: 12 } }])
    expect(findSensor(42).values.P2).toBe(12)
  })

  it('returns null for an id that is not on the map', () => {
    setSensors([{ properties: { id: 42 } }])
    expect(findSensor(999)).toBeNull()
  })
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `cd web && npx vitest run src/islands/__tests__/panel.test.js`
Expected: FAIL — modules not found.

- [ ] **Step 3: Implement the registry and the island**

`web/src/lib/sensors.svelte.js`:

```js
// What the map has loaded, published for the panel to read. A $state array so
// the panel re-renders when a pan brings new sensors in — and so a deep-linked
// #sensor= that arrives before the data does resolves as soon as it lands.
let sensors = $state([])
export function setSensors(next) { sensors = next }
export function allSensors() { return sensors }
export function findSensor(id) {
  return sensors.find((f) => Number(f.properties?.id) === Number(id)) ?? null
}
```

`web/src/islands/panel.js`:

```js
import { mount as mountComponent } from 'svelte'
import SensorPanel from '../components/SensorPanel.svelte'
import Chart from '../components/Chart.svelte'
import { panelRows } from '../lib/sensorview.js'
import { parseMetricList, zipLabels } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { findSensor } from '../lib/sensors.svelte.js'
import { loadScales } from './map.js'

// id and flag are metadata; every OTHER property is a metric reading. Derived by
// exclusion rather than by an allow-list so a metric added server-side shows up
// in the panel with no frontend change.
const META = ['id', 'flag']

export function normaliseSensor(feature) {
  const props = feature?.properties ?? {}
  const values = {}
  for (const [k, v] of Object.entries(props)) {
    if (!META.includes(k)) values[k] = v
  }
  return { id: props.id, flag: props.flag, values }
}
```

The island's `mount(el)` builds the options list from `data-metrics`/`data-metric-labels`, reads `vs.sensorId`, resolves it through `findSensor`, and mounts `SensorPanel` with a `chart` snippet that renders `Chart` against
`/api/v1/sensor/{id}/series?metric=&period=` — the endpoint that has had zero callers since Phase 2. Panel content renders immediately from the snapshot; the chart mounts alongside and fills in when its fetch returns.

In `islands/map.js`, call `setSensors(features)` wherever sensor features are loaded, and on a marker click call `vs.openSensor(id)` — the map must not render the panel itself.

`area.gohtml` gains the `data-island="panel"` container with `data-metrics`, `data-metric-labels`, `data-period`, `data-line-colour`, and the panel/chart `data-t-*` strings. **`index.gohtml` does not** — `#sensor=` is honoured only on an area page, because no `/api/v1/sensor/{id}` metadata endpoint exists to resolve a sensor the map has not loaded.

- [ ] **Step 4: Run and watch it pass**

Run: `cd web && npm test`
Expected: PASS.

- [ ] **Step 5: Mutation-prove**

1. `if (!META.includes(k))` → `values[k] = v` unconditionally (must fail the metadata test).
2. `Number(f.properties?.id) === Number(id)` → `f.properties?.id === id` and pass a string id in the test (must fail — proves the coercion is load-bearing, since the hash yields a number and GeoJSON may carry a string).
3. Delete the `setSensors(features)` call in map.js (must fail the registry-backed panel test).

- [ ] **Step 6: Commit**

```bash
git add web/src internal/web/templates
git commit -m "web: open the sensor panel from the map and from #sensor="
```

---

### Task 10: Auto-locate on the home page

**Files:**
- Create: `web/src/lib/locate.js`, `web/src/lib/__tests__/locate.test.js`
- Modify: `web/src/islands/map.js`, `internal/web/templates/index.gohtml`

**Interfaces:**
- Produces: `applyLocate(body, {defaultView}) -> {move: boolean, centre: [lon,lat], zoom: number, slug: string|null}`.

- [ ] **Step 1: Write the failing tests**

`web/src/lib/__tests__/locate.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { applyLocate } from '../locate.js'

const defaultView = { lon: 25.4858, lat: 42.7339, zoom: 7 }

describe('applyLocate', () => {
  // source: "default" means the server could NOT place the visitor and handed
  // back the national view the page already opened on. Moving the map then is a
  // visible jump to where it already is — and it would adopt a slug the visitor
  // never chose, unlocking the sensor tier for an area picked at random.
  it('does not move for source: "default"', () => {
    const out = applyLocate({ source: 'default', slug: 'bg', lon: 25.4, lat: 42.7, zoom: 7 }, { defaultView })
    expect(out.move).toBe(false)
    expect(out.slug).toBeNull()
  })

  it('moves and adopts the slug for source: "geoip"', () => {
    const out = applyLocate({ source: 'geoip', slug: 'sofia', lon: 23.32, lat: 42.7, zoom: 11 }, { defaultView })
    expect(out).toEqual({ move: true, centre: [23.32, 42.7], zoom: 11, slug: 'sofia' })
  })

  // The endpoint degrades to the national view under load (a full admission
  // pool) and on a failed lookup. A rejected fetch must land in the same
  // "stay put" branch rather than throwing out of the map's init.
  it('does not move when the lookup failed', () => {
    expect(applyLocate(null, { defaultView }).move).toBe(false)
  })

  it('does not move for an unknown source value', () => {
    expect(applyLocate({ source: 'guess', slug: 'x', lon: 1, lat: 2, zoom: 9 }, { defaultView }).move).toBe(false)
  })
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `cd web && npx vitest run src/lib/__tests__/locate.test.js`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement**

```js
// The response's `source` field is the whole decision. "default" is the
// server saying "I could not place you, here is the country" — it is NOT a
// location, and treating it as one both jars the view and adopts a slug the
// visitor never asked for, which is what unlocks the per-area sensor tier.
export function applyLocate(body, { defaultView }) {
  const stay = { move: false, centre: [defaultView.lon, defaultView.lat], zoom: defaultView.zoom, slug: null }
  if (!body || body.source !== 'geoip') return stay
  return { move: true, centre: [body.lon, body.lat], zoom: body.zoom, slug: body.slug ?? null }
}
```

In `islands/map.js`'s `mount`, on the home page only (the map island carries no `data-slug` there), fetch `/api/v1/locate` once after the map loads, pass the body through `applyLocate`, and on `move` call `map.jumpTo` — not `easeTo`: a long flight from the national view on first paint reads as a bug — then adopt the slug so `refresh()` may use the sensor tier. Wrap the fetch so a rejection yields `applyLocate(null, …)`.

- [ ] **Step 4: Run and watch it pass**

Run: `cd web && npm test`
Expected: PASS.

- [ ] **Step 5: Mutation-prove**

1. `body.source !== 'geoip'` → `!body.source` (must fail the "default" and unknown-source tests).
2. `slug: null` in `stay` → `slug: body?.slug ?? null` (must fail the "default" test's slug assertion).

- [ ] **Step 6: Commit**

```bash
git add web/src internal/web/templates
git commit -m "web: open the home map on the visitor's area when the server can place them"
```

---

### Task 11: The find-me button

**Files:**
- Create: `web/src/lib/nearest.js`, `web/src/lib/__tests__/nearest.test.js`
- Modify: `web/src/islands/map.js`, both templates, `internal/i18n/bg.json`, `internal/i18n/en.json`

**Interfaces:**
- Produces: `nearestArea([lon,lat], areas) -> {slug, lon, lat, zoom} | null`.

- [ ] **Step 1: Add the copy**

BG / EN:

```json
  "locate.button": "Намери ме" / "Find me"
  "locate.denied": "Няма достъп до местоположението." / "Location access was denied."
  "locate.failed": "Не успяхме да определим местоположението." / "We could not determine your location."
  "locate.outside": "Изглежда сте извън покритието на картата." / "You appear to be outside the mapped area."
```

- [ ] **Step 2: Write the failing tests**

`web/src/lib/__tests__/nearest.test.js`:

```js
import { describe, it, expect } from 'vitest'
import { nearestArea } from '../nearest.js'

const areas = [
  { slug: 'sofia', lon: 23.3219, lat: 42.6977, zoom: 11 },
  { slug: 'plovdiv', lon: 24.7453, lat: 42.1354, zoom: 11 },
  { slug: 'varna', lon: 27.9147, lat: 43.2141, zoom: 11 },
]

describe('nearestArea', () => {
  it('picks the closest centroid', () => {
    expect(nearestArea([23.4, 42.7], areas).slug).toBe('sofia')
    expect(nearestArea([27.8, 43.2], areas).slug).toBe('varna')
  })

  // Degrees of longitude shrink with latitude. At 43°N one degree of longitude
  // is ~0.73 of one degree of latitude, so a plain sqrt(dx²+dy²) on raw degrees
  // overstates east-west distance by a third and can pick the wrong city.
  it('accounts for longitude convergence', () => {
    const pair = [
      { slug: 'east', lon: 24.0, lat: 43.0, zoom: 11 },
      { slug: 'north', lon: 23.0, lat: 43.9, zoom: 11 },
    ]
    // 1.0° east at 43°N is ~81 km; 0.9° north is ~100 km. 'east' is closer.
    expect(nearestArea([23.0, 43.0], pair).slug).toBe('east')
  })

  it('returns null when there are no areas', () => {
    expect(nearestArea([23, 42], [])).toBeNull()
  })
})
```

- [ ] **Step 3: Run and watch it fail**

Run: `cd web && npx vitest run src/lib/__tests__/nearest.test.js`
Expected: FAIL — module not found.

- [ ] **Step 4: Implement**

```js
// Nearest area, computed IN THE BROWSER. The precise-GPS path must never send
// a coordinate to the server: /api/v1/locate exists for coarse placement and
// takes no body, and there is no endpoint that accepts a point — by design,
// since one would be a bounding-box query wearing a hat.
//
// Equirectangular approximation, not haversine: over Bulgaria's ~500 km extent
// the error is far below the distance between two oblast centroids, and it is
// one line instead of five.
export function nearestArea([lon, lat], areas) {
  if (!areas || areas.length === 0) return null
  const k = Math.cos((lat * Math.PI) / 180) // longitude degrees shrink with latitude
  let best = null
  let bestD = Infinity
  for (const a of areas) {
    const dx = (a.lon - lon) * k
    const dy = a.lat - lat
    const d = dx * dx + dy * dy // squared: monotonic, so no sqrt needed
    if (d < bestD) { bestD = d; best = a }
  }
  return best
}
```

In `islands/map.js`, render the button into the map chrome (the existing hint controller's container), wire it to `navigator.geolocation.getCurrentPosition`, and on success navigate to the nearest area's page. On `PERMISSION_DENIED` show `locate.denied`; on any other error `locate.failed`; when `nearestArea` returns null show `locate.outside`. The areas list comes from the already-loaded area features — no new request.

- [ ] **Step 5: Run and watch it pass**

Run: `cd web && npm test`
Expected: PASS.

- [ ] **Step 6: Mutation-prove**

1. Delete the `k` factor (`const dx = a.lon - lon`) — must fail the convergence test.
2. `if (d < bestD)` → `if (d <= bestD)` — confirm whether any test catches it; if none does, that is acceptable (the tie-break is not specified), and say so in the report rather than inventing a test for it.

- [ ] **Step 7: Commit**

```bash
git add web/src internal/web/templates internal/i18n
git commit -m "web: add the find-me button, resolving the area in the browser"
```

---

### Task 12: The Playwright harness

The stack under test is the real binary against real Postgres. `internal/testsupport`'s helpers take `*testing.T`, so the Go side must be a **test**, not a command: a `//go:build e2e` test boots the stack and execs Playwright. Playwright's own `webServer` option is not used.

**Files:**
- Create: `internal/e2e/e2e_test.go`, `web/playwright.config.js`, `web/e2e/smoke.spec.js`
- Modify: `web/package.json`, `.gitignore`

**Interfaces:**
- Produces: `go test -tags e2e ./internal/e2e/` — boots Postgres, migrates, seeds, starts the server, sets `AIRBG_E2E_BASE_URL`, runs `npx playwright test`, fails the Go test on a non-zero exit.

- [ ] **Step 1: Install Playwright**

```bash
cd web && npm install --save-exact --save-dev @playwright/test && npx playwright install --with-deps chromium
```

Add `web/test-results/`, `web/playwright-report/` and `web/blob-report/` to `.gitignore`.

- [ ] **Step 2: Write the Playwright config**

`web/playwright.config.js`:

```js
import { defineConfig, devices } from '@playwright/test'

// No webServer block: the Go test at internal/e2e/e2e_test.go owns the stack's
// lifetime, because internal/testsupport's Postgres helpers take a *testing.T
// and cannot be called from a plain command. The base URL arrives from it.
export default defineConfig({
  testDir: './e2e',
  // Serial, and no retries: a flaky E2E that passes on retry is a bug report
  // nobody reads. One worker also keeps the per-IP rate limiters — which see
  // every browser as the same client — out of the results.
  workers: 1,
  retries: 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.AIRBG_E2E_BASE_URL,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
```

Add to `web/package.json` scripts: `"e2e": "playwright test"`.

- [ ] **Step 3: Write the smoke spec**

`web/e2e/smoke.spec.js`:

```js
import { test, expect } from '@playwright/test'

test('the area page renders server-side with JavaScript disabled', async ({ browser }) => {
  // The whole point of server-rendered pages: this must pass with no bundle.
  const context = await browser.newContext({ javaScriptEnabled: false })
  const page = await context.newPage()
  await page.goto('/area/sofia')
  await expect(page.locator('h1')).toContainText('Sofia')
  await context.close()
})

test('the metric switcher is mounted and reflects the default metric', async ({ page }) => {
  await page.goto('/area/sofia')
  const pressed = page.locator('.metric-switcher button[aria-pressed="true"]')
  await expect(pressed).toHaveCount(1)
})
```

- [ ] **Step 4: Write the Go driver**

`internal/e2e/e2e_test.go`:

```go
//go:build e2e

// Package e2e boots the real stack and drives it with Playwright.
//
// A Go TEST rather than a Go command, and therefore Playwright is launched by
// Go rather than the other way round: testsupport.NewPostgres takes a *testing.T
// (container lifetime is tied to t.Cleanup), which a plain main() cannot supply.
// Inverting it would mean a second, divergent copy of the container setup.
package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	// … project imports: db, store, server, config, testsupport
)

func TestBrowser(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(pool)
	seedFixtures(t, st) // the fixtures every spec relies on; see Step 5

	// The same airbg.yaml the binary ships with — an E2E against a bespoke
	// config proves the config nobody runs.
	t.Setenv(config.PathEnv, filepath.Join("..", "..", "airbg.yaml"))
	baseURL := startServer(t, st) // listens on :0, returns http://127.0.0.1:PORT

	cmd := exec.Command("npx", "playwright", "test")
	cmd.Dir = filepath.Join("..", "..", "web")
	cmd.Env = append(os.Environ(), "AIRBG_E2E_BASE_URL="+baseURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("playwright: %v", err)
	}
}
```

`startServer` must bind port 0 and return the real address — a fixed port makes the suite fail on a developer machine that already runs the app. Reuse `internal/server`'s existing construction path rather than assembling handlers by hand; if the current API cannot start on an arbitrary port, add the smallest seam that allows it and say so in the report.

- [ ] **Step 5: Seed the fixtures**

`seedFixtures` inserts, via **parameterised** pgx queries only (copy the pattern from `internal/server/e2e_test.go`'s `seedArea`): the `sofia` area with an English and Bulgarian name, at least two sensors inside it with current P1 and P2 values, one sensor with a null P1 and a non-null P2, one sensor carrying a non-`ok` quality flag, and enough series points for a 24h chart to be non-empty. Every spec in Task 13 asserts against these, so any spec needing new data adds it here.

- [ ] **Step 6: Run it**

Run: `go test -tags e2e ./internal/e2e/ -v`
Expected: PASS, with Playwright's output inline. First run pulls containers and may take minutes.

- [ ] **Step 7: Mutation-prove**

Change the seeded area name to something else and confirm the JS-disabled spec fails with a diff naming the expected text — this proves the harness is really driving the seeded stack and not a stale dev server on the same port. Restore.

- [ ] **Step 8: Commit**

```bash
git add internal/e2e web/playwright.config.js web/e2e web/package.json web/package-lock.json .gitignore
git commit -m "test: drive the real stack with Playwright from a build-tagged Go test"
```

---

### Task 13: The E2E specs for this phase

Only behaviours no other tier can prove: real history, real geolocation permissions, real bundle loading.

**Files:**
- Create: `web/e2e/metric.spec.js`, `web/e2e/panel.spec.js`, `web/e2e/locate.spec.js`
- Modify: `internal/e2e/e2e_test.go` (fixtures only, if a spec needs data)

**Interfaces:**
- Consumes: the fixtures from Task 12 Step 5 and the Task 12 config.

- [ ] **Step 1: Write the metric spec**

`web/e2e/metric.spec.js`:

```js
import { test, expect } from '@playwright/test'

test('switching metric rewrites the hash without growing the back stack', async ({ page }) => {
  await page.goto('/area/sofia')
  await page.getByRole('button', { name: 'PM10' }).click()
  await expect(page).toHaveURL(/#metric=P1/)
  // Back must leave the page, not undo the metric — replaceState is the whole
  // reason this assertion exists and the only way to prove it in a real browser.
  await page.goBack()
  await expect(page).not.toHaveURL(/\/area\/sofia/)
})

test('a deep-linked metric is selected on load', async ({ page }) => {
  await page.goto('/area/sofia#metric=temperature')
  await expect(page.getByRole('button', { name: 'Temperature' })).toHaveAttribute('aria-pressed', 'true')
})

test('an unknown metric in the hash falls back to the default', async ({ page }) => {
  await page.goto('/area/sofia#metric=plutonium')
  await expect(page.locator('.metric-switcher button[aria-pressed="true"]')).toHaveCount(1)
})
```

- [ ] **Step 2: Write the panel spec**

`web/e2e/panel.spec.js`:

```js
import { test, expect } from '@playwright/test'

test('a deep-linked sensor opens the panel with a chart', async ({ page }) => {
  await page.goto('/area/sofia#sensor=1001') // seeded in internal/e2e
  const panel = page.getByRole('dialog')
  await expect(panel).toBeVisible()
  // The panel's values come from the snapshot the map already holds, so they
  // must be on screen BEFORE the series request settles.
  await expect(panel).toContainText('PM2.5')
  await expect(panel.locator('.chart-host canvas')).toBeVisible()
})

test('Back closes the panel and leaves the page loaded', async ({ page }) => {
  await page.goto('/area/sofia')
  await page.goto('/area/sofia#sensor=1001')
  await page.goBack()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page).toHaveURL(/\/area\/sofia$/)
})

test('Escape closes the panel', async ({ page }) => {
  await page.goto('/area/sofia#sensor=1001')
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('a sensor id that is not on this map leaves the page usable', async ({ page }) => {
  await page.goto('/area/sofia#sensor=999999')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.locator('.metric-switcher')).toBeVisible()
})
```

- [ ] **Step 3: Write the locate spec**

`web/e2e/locate.spec.js`:

```js
import { test, expect } from '@playwright/test'

// Real permission states are the reason this is an E2E and not a unit test:
// jsdom has no geolocation permission model at all.
test('find-me navigates to the nearest area when permission is granted', async ({ browser }) => {
  const context = await browser.newContext({
    permissions: ['geolocation'],
    geolocation: { longitude: 23.33, latitude: 42.70 },
  })
  const page = await context.newPage()
  await page.goto('/')
  await page.getByRole('button', { name: 'Find me' }).click()
  await expect(page).toHaveURL(/\/area\/sofia/)
  await context.close()
})

test('a denied permission explains itself and leaves the map usable', async ({ browser }) => {
  const context = await browser.newContext({ permissions: [] })
  const page = await context.newPage()
  await page.goto('/')
  await page.getByRole('button', { name: 'Find me' }).click()
  await expect(page.getByText('Location access was denied.')).toBeVisible()
  await context.close()
})
```

The English strings above require the specs to run against `/en/…` or the EN catalogue; if the default language is Bulgarian, navigate to `/en/` and `/en/area/sofia` in every spec and use the EN copy, or use `data-testid` — pick one and apply it to all three files consistently.

- [ ] **Step 4: Run the suite**

Run: `go test -tags e2e ./internal/e2e/ -v`
Expected: PASS, all specs.

- [ ] **Step 5: Mutation-prove**

Two mutations, each re-run and restored: change `setMetric`'s `write(false)` to `write(true)` (the back-stack spec must fail) and delete the `#sensor=` restore in `islands/panel.js` (the deep-link spec must fail). These are the two behaviours only a real browser can prove.

- [ ] **Step 6: Commit**

```bash
git add web/e2e internal/e2e
git commit -m "test: cover the metric switcher, the panel and find-me in a real browser"
```

---

### Task 14: CI

**Files:**
- Modify: `.github/workflows/ci.yml`, `docs/configuration.md`, `README.md` (test-running section, if one exists)

**Interfaces:**
- Produces: three added CI jobs — `web` (Vitest + build), `integration` (`go test -tags integration`), `e2e` (`go test -tags e2e`) — plus `go vet -tags integration ./...` folded into the existing job.

- [ ] **Step 1: Add the web job**

In `.github/workflows/ci.yml`, beside the existing Go job:

```yaml
  web:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: web
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: npm
          cache-dependency-path: web/package-lock.json
      # npm ci, not npm install: it fails on a lockfile that does not match
      # package.json instead of quietly resolving a different tree than the
      # one that was reviewed.
      - run: npm ci
      - run: npm test
      - run: npm run build
```

- [ ] **Step 2: Close the vet blind spot**

`go vet ./...` does not compile files behind a build tag, so `internal/server/e2e_test.go` and the new `internal/e2e` package are invisible to it. Add to the existing Go job:

```yaml
      - run: go vet -tags integration ./...
      - run: go vet -tags e2e ./...
```

- [ ] **Step 3: Add the integration and e2e jobs**

```yaml
  integration:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod, cache: true }
      # testcontainers starts Postgres itself; no services: block, so the
      # container image and extensions match what runs locally.
      - run: go test -tags integration ./... -race

  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod, cache: true }
      - uses: actions/setup-node@v4
        with:
          node-version: '22'
          cache: npm
          cache-dependency-path: web/package-lock.json
      - run: npm ci
        working-directory: web
      # The Go test serves the EMBEDDED bundle, so the build must run first or
      # every island is missing and every spec fails for the wrong reason.
      - run: npm run build
        working-directory: web
      - run: npx playwright install --with-deps chromium
        working-directory: web
      - run: go test -tags e2e ./internal/e2e/
      - uses: actions/upload-artifact@v4
        if: failure()
        with:
          name: playwright-report
          path: web/playwright-report/
```

Confirm the Node version against `web/package.json`'s `engines` and the Dockerfile's build stage; use whichever those already name rather than introducing a third.

- [ ] **Step 4: Verify locally**

Run: `go build ./... && go vet ./... && go vet -tags integration ./... && go vet -tags e2e ./... && go test ./... -race && go test -tags integration ./... && (cd web && npm test && npm run build) && go test -tags e2e ./internal/e2e/`
Expected: all green. Record the counts (Go packages `ok`, Vitest total, Playwright total) in the task report.

- [ ] **Step 5: Document how to run each tier**

Add a short section to `docs/configuration.md` (or the README's test section, whichever the repo already uses) listing the four commands: unit Go, integration Go, Vitest, E2E — and note that E2E requires `npm run build` first and a Docker daemon.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/ci.yml docs README.md
git commit -m "ci: run the web, integration and browser suites"
```

---

## Plan Self-Review

Checked against `docs/superpowers/specs/2026-08-16-airbg-phase3c-frontend-design.md`:

- **Spec coverage.** Metric switcher → Tasks 3, 6, 7. Sensor panel and `#sensor=` → Tasks 8, 9. Opening view and find-me → Tasks 10, 11. State model → Tasks 2, 4. `frontend.unscaled_colour` → Task 1, consumed in Task 7. Chart converted to a component → Task 5. Testing and CI → Tasks 12, 13, 14. `literals.test.js` extended to components → Task 5 Step 5. Every module in the spec's layout is created by some task.
- **Deviation from the spec, deliberate.** The spec described the E2E stack as "a build-tagged Go command that Playwright's `webServer` runs". That is not buildable: `internal/testsupport.NewPostgres` and `NewPostgresURL` both require a `*testing.T`, so container lifetime cannot be owned by a `main()`. Task 12 inverts it — a `//go:build e2e` Go **test** owns the stack and execs `npx playwright test`, passing the base URL in `AIRBG_E2E_BASE_URL`, with no `webServer` block. Everything else about the tier is unchanged. **The spec must be updated to match before execution begins.**
- **Two open items the implementer resolves, not the plan.** Task 8 Step 0 requires confirming the real sensor-feature property names before writing `panelRows` — the plan's `{id, flag, values}` shape is the target, and the source names must be read from `sensorFeatures` and the sensors handler rather than assumed. Task 13 Step 3 requires picking one language strategy (EN routes vs `data-testid`) and applying it to all three spec files.
- **Dependency budget.** `jsdom` (Task 4 Step 1) and `@playwright/test` (Task 12 Step 1). No others. No Go dependency anywhere.
- **Naming consistency.** `getViewState` is defined in Task 6 Step 8 and used in Tasks 6, 7, 9. `parseMetricList`/`zipLabels` defined in Tasks 3 and 6, used in 6, 9. `hasScale`/`unitFor` defined in Task 3, used in 7 and 8. `setSensors`/`findSensor` defined in Task 9, used in 9. `markerPaint`'s signature changes once, in Task 7, and its old call sites are in the same file.

