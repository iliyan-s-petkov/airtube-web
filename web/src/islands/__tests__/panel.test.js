// @vitest-environment jsdom
//
// The brief this task started from assumed normaliseSensor projects a
// GeoJSON feature ({ properties: { id, flag, P1, P2, ... } }). It does not:
// sensorFeatures (islands/map.js:242-262) puts only `id`, `colour`, `value`
// (the CURRENTLY SELECTED metric only) and `quality` on a feature's
// properties — no per-metric columns at all. The real projection is out of
// the columnar /api/v1/area/{slug}/sensors body (internal/snapshot/build.go),
// which is what setSensors/normaliseSensor below actually consume. See
// lib/sensors.svelte.js for the full rationale.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// panel.js imports Chart.svelte (for the embedded chart snippet), which
// imports uplot — and uplot touches layout APIs (matchMedia) at import time,
// which jsdom does not provide, even though none of the tests below ever
// render a chart. Same mock as components/__tests__/chart.component.test.js,
// for the same reason: this file is about normaliseSensor/the registry, not
// about uPlot construction.
vi.mock('uplot', () => ({
  default: vi.fn(function () { this.setSize = vi.fn() }),
}))

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { mount, normaliseSensor, flagTextFor } from '../panel.js'
import { setSensors, findSensor } from '../../lib/sensors.svelte.js'
import { getViewState, resetViewStateForTests } from '../../lib/viewstate.svelte.js'

// The values a rendered page would carry in each of the panel island's
// attributes, keyed by dataset key. Deliberately NOT read from the i18n
// catalogues: these tests assert that whatever the template puts in an
// attribute reaches the DOM, so the strings here are fixtures, not copy.
// An attribute with no entry here fails the test rather than defaulting —
// a new data-t-* on the panel island must be given a value here, which is
// what stops "the attribute is rendered" from silently drifting away from
// "the attribute is read".
const PANEL_ATTR_FIXTURES = {
  metrics: 'P1,P2',
  metricLabels: 'PM10,PM2.5',
  metric: 'P2',
  period: '24h',
  lineColour: '#2563eb',
  tTitle: 'Sensor',
  tClose: 'Close this panel',
  tNoValue: 'no reading',
  tFlagOutOfRange: 'This reading is out of the expected range.',
  tFlagStuck: 'This reading has not changed in a while.',
  tFlagSpatialOutlier: 'This reading disagrees with nearby sensors.',
  tChartTitle: 'Chart',
  tChartValue: 'Value',
  tChartTime: 'Time',
  tChartEmpty: 'empty',
  tChartUnavailable: 'unavailable',
}

// islandFrom returns the REAL server template's island container as a live DOM
// element, ready to mount. Go actions ({{.X}}, {{.T "k"}}, {{/* comments */}})
// are stripped before parsing — an action's own inner quotes (data-t-title="{{.T
// "panel.title"}}") would otherwise terminate the attribute early and hand the
// parser garbage — and every emptied attribute is then filled from the fixture
// table above.
//
// The point of going through the template rather than hand-building a div: a
// hand-built one proves nothing about whether the SERVER renders a mount point
// on that page. That was the defect this covers — the panel island existed and
// worked, and index.gohtml simply never rendered it.
function islandFrom(templateName, island) {
  // join(dirname(fileURLToPath(...))) rather than new URL(path, import.meta.url):
  // Vite rewrites the latter into an ASSET import at transform time and then
  // refuses the path for being outside the project root — it never reaches
  // readFileSync at all.
  const here = dirname(fileURLToPath(import.meta.url))
  const path = join(here, '..', '..', '..', '..', 'internal', 'web', 'templates', templateName)
  const src = readFileSync(path, 'utf8').replace(/\{\{[\s\S]*?\}\}/g, '')
  const doc = new DOMParser().parseFromString(src, 'text/html')
  return doc.querySelector(`[data-island="${island}"]`)
}

function fillFixtures(el) {
  for (const key of Object.keys({ ...el.dataset })) {
    if (key === 'island') continue
    if (!Object.hasOwn(PANEL_ATTR_FIXTURES, key)) {
      throw new Error(`no fixture for data-${key}; add one to PANEL_ATTR_FIXTURES`)
    }
    el.dataset[key] = PANEL_ATTR_FIXTURES[key]
  }
  return el
}

beforeEach(() => setSensors(null))

describe('normaliseSensor', () => {
  it('projects one sensor out of the columnar body into the shape panelRows expects', () => {
    const body = { sensors: { id: [42], quality: ['ok'], P1: [30], P2: [12] } }
    expect(normaliseSensor(body, 42)).toEqual({ id: 42, flag: 'ok', values: { P1: 30, P2: 12 } })
  })

  // id/type/lon/lat/quality describe the sensor, not a measurement: leaking
  // any of them into values would put a row labelled "lon" or "quality" in
  // the panel. quality is renamed to `flag` instead of being dropped — see
  // the "renames quality to flag" test below.
  it('keeps metadata out of the values map', () => {
    const body = { sensors: { id: [1], type: ['SDS011'], lon: [23.3], lat: [42.7], quality: ['stale'], P1: [5] } }
    expect(normaliseSensor(body, 1).values).toEqual({ P1: 5 })
  })

  it('renames quality to flag', () => {
    const body = { sensors: { id: [1], quality: ['stuck'] } }
    expect(normaliseSensor(body, 1).flag).toBe('stuck')
  })

  // A metric column that EXISTS but is null at this sensor's index is a
  // reported-but-currently-missing reading and must survive as null. A
  // column the response never carries (here: no `temperature` key at all)
  // must not appear as a key of `values` — that is a different fact
  // (hardware does not measure it) that lib/sensorview.js's panelRows relies
  // on to omit rather than grey out a row.
  it('keeps a null reading distinct from a metric the response does not carry at all', () => {
    const body = { sensors: { id: [1], quality: ['ok'], P1: [30], P2: [null] } }
    const { values } = normaliseSensor(body, 1)
    expect(values).toEqual({ P1: 30, P2: null })
    expect(Object.hasOwn(values, 'temperature')).toBe(false)
  })

  it('returns null for an id the body does not carry', () => {
    const body = { sensors: { id: [42] } }
    expect(normaliseSensor(body, 999)).toBeNull()
  })
})

describe('the sensor registry', () => {
  // The panel reads what the map already loaded. Refetching would double
  // every page's request count against a per-IP enumeration limiter that
  // counts distinct sensor ids — the panel would burn the visitor's budget
  // twice.
  it('finds a sensor the map published', () => {
    setSensors({ sensors: { id: [42], quality: ['ok'], P2: [12] } })
    expect(findSensor(42).values.P2).toBe(12)
  })

  it('returns null for an id that is not on the map', () => {
    setSensors({ sensors: { id: [42] } })
    expect(findSensor(999)).toBeNull()
  })

  // Mutation 2 (adapted): the id column is JSON-typed by whatever produced
  // it, and vs.sensorId is always a number (lib/viewstate.js's
  // parseSensorId), so an id-column entry of a DIFFERENT type must still
  // resolve. A strict `===` (no Number() coercion on both sides) fails this
  // the moment the column holds strings, which the fixture below forces.
  it('finds a sensor even when the id column holds strings', () => {
    setSensors({ sensors: { id: ['42'], quality: ['ok'], P2: [12] } })
    expect(findSensor(42)).not.toBeNull()
    expect(findSensor(42).values.P2).toBe(12)
  })

  it('returns null before any data has loaded', () => {
    expect(findSensor(42)).toBeNull()
  })

  it('returns null for a null or undefined id without matching id 0', () => {
    setSensors({ sensors: { id: [0], quality: ['ok'], P2: [1] } })
    expect(findSensor(null)).toBeNull()
    expect(findSensor(undefined)).toBeNull()
  })
})

// 'ok' and 'no_neighbours' are quality values a sensor can legitimately have
// with nothing wrong: the catalogue (panel.flag.*, internal/i18n/*.json)
// deliberately has no entry for either. A visitor must see no warning for
// them — and, just as importantly, must never see the raw lookup miss
// leaking through as 'undefined' or the Go-side i18n miss-marker ('!key!').
describe('flagTextFor', () => {
  const catalogue = {
    out_of_range: 'This reading is out of the expected range.',
    stuck: 'This reading has not changed in a while.',
    spatial_outlier: 'This reading disagrees with nearby sensors.',
  }

  it('renders the matching warning for a real flag', () => {
    expect(flagTextFor('stuck', catalogue)).toBe(catalogue.stuck)
  })

  it('renders nothing for the two non-failure quality values', () => {
    expect(flagTextFor('ok', catalogue)).toBe('')
    expect(flagTextFor('no_neighbours', catalogue)).toBe('')
  })

  it('renders nothing, not undefined or a miss-marker, for an unrecognised or missing flag', () => {
    expect(flagTextFor('something_new', catalogue)).toBe('')
    expect(flagTextFor(undefined, catalogue)).toBe('')
    expect(flagTextFor('', catalogue)).toBe('')
  })
})

// Code-review finding (fix round 1): every test above exercises
// normaliseSensor/findSensor/flagTextFor as plain functions. None of them
// mounts panel.js's OWN mount() — so nothing above actually proves the
// getter-prop chain in mount() (`get rows() { const sensor =
// findSensor(vs.sensorId); ... }`) repaints an already-mounted SensorPanel
// when data that did not exist at mount time (setSensors called later)
// finally lands. A one-time destructure at mount would compile, read
// identically at the moment of construction, and pass every test above while
// silently freezing the panel — see the mutation this test is proven against
// in task-9-report.md.
//
// The case that matters is deep-link-before-data, not pan-brings-in-more:
// the hash already names a sensor when mount() runs, the registry has
// nothing for it yet (setSensors(null)), and the panel must render CLOSED
// (not throw, not show a blank dialog) until the map's own fetch calls
// setSensors — at which point THIS SAME mounted instance, with no remount,
// must show it.
describe('mount() end to end: deep link before data', () => {
  beforeEach(() => {
    resetViewStateForTests()
    setSensors(null)
  })
  afterEach(() => {
    resetViewStateForTests()
    setSensors(null)
  })

  it('renders closed at mount, then repaints with the sensor once the registry catches up', async () => {
    // A visitor following a shared #sensor=42 link, or reloading a page with
    // the panel already open: vs.sensorId is 42 before mount() ever runs.
    history.replaceState(null, '', '/#sensor=42')

    const el = document.createElement('div')
    el.dataset.metrics = 'P2'
    el.dataset.metricLabels = 'PM2.5'
    el.dataset.metric = 'P2'
    el.dataset.period = '24h'
    el.dataset.lineColour = '#2563eb'
    el.dataset.tTitle = 'Sensor'
    el.dataset.tClose = 'Close'
    el.dataset.tNoValue = 'no data'
    el.dataset.tFlagOutOfRange = 'out of range'
    el.dataset.tFlagStuck = 'stuck'
    el.dataset.tFlagSpatialOutlier = 'outlier'
    el.dataset.tChartTitle = 'Chart'
    el.dataset.tChartValue = 'Value'
    el.dataset.tChartEmpty = 'empty'
    el.dataset.tChartUnavailable = 'unavailable'

    mount(el)

    // No data yet: findSensor(42) is null, so SensorPanel's `open` prop is
    // false and {#if open} renders nothing — not a blank/empty dialog.
    expect(el.querySelector('[role="dialog"]')).toBeNull()

    // The map's own fetch lands AFTER mount — this is the moment a one-time
    // destructure at mount would have already missed.
    setSensors({ sensors: { id: [42], quality: ['ok'], P2: [12] } })

    await vi.waitFor(() => {
      expect(el.querySelector('[role="dialog"]')).not.toBeNull()
    })
    expect(el.textContent).toContain('42')
    expect(el.textContent).toContain('PM2.5')
    expect(el.textContent).toContain('12')
  })
})

// Whole-branch review finding: the home page reaches the SENSOR tier — an
// area-marker click (islands/map.js's click handler) and locateVisitor's geoip
// slug adoption both set state.slug, and refresh() only downgrades to the area
// tier when the slug is null — so a sensor dot on / is clickable and
// vs.openSensor() pushes #sensor=<id> onto the history stack. index.gohtml
// rendered no panel island, so that click opened nothing and merely spent a
// Back press undoing its own hash. The fix is the mount point; these tests are
// what pin it there.
describe('the home page mounts the panel island', () => {
  beforeEach(() => {
    resetViewStateForTests()
    setSensors(null)
    history.replaceState(null, '', '/')
  })
  afterEach(() => {
    resetViewStateForTests()
    setSensors(null)
    history.replaceState(null, '', '/')
  })

  // Attribute-set parity, not a hand-listed expectation: the panel is entirely
  // data-driven, so anything area.gohtml passes it and index.gohtml does not is
  // a behaviour the home page silently loses (a missing data-t-* renders as an
  // empty string, which is invisible rather than broken).
  it('gives it the same attributes area.gohtml does', () => {
    const home = islandFrom('index.gohtml', 'panel')
    const area = islandFrom('area.gohtml', 'panel')
    expect(area).not.toBeNull()
    expect(home).not.toBeNull()
    const names = (el) => el.getAttributeNames().sort()
    expect(names(home)).toEqual(names(area))
  })

  it('opens a sensor clicked on the home page instead of only changing the hash', async () => {
    const el = fillFixtures(islandFrom('index.gohtml', 'panel'))
    document.body.append(el)
    mount(el)

    // What a marker click does, and all it does: islands/map.js:141 calls
    // exactly this on the shared viewstate singleton.
    getViewState({ metrics: ['P1', 'P2'], defaultMetric: 'P2' }).openSensor(42)
    setSensors({ sensors: { id: [42], quality: ['ok'], P2: [12] } })

    await vi.waitFor(() => {
      expect(el.querySelector('[role="dialog"]')).not.toBeNull()
    })
    expect(el.textContent).toContain('PM2.5')
    expect(el.textContent).toContain('12')

    el.remove()
  })
})

// Whole-branch review finding: mutating panel.js's flagText getter, closeLabel
// or noValue to '' left all 186 tests green. flagTextFor was tested only as a
// bare function, and SensorPanel.svelte's own close test supplied its own
// vi.fn() rather than the real wiring — so the entire quality-flag sentence
// path could have been deleted without a single failure. These three tests go
// through mount() and assert the strings on screen.
describe('mount() puts the panel copy on screen', () => {
  // The hash is set BEFORE mount, i.e. the deep-link order: the store reads it
  // at construction, so a test that mounts first would be exercising a
  // different path than the one these three tests are about.
  function mountPanel(sensorId) {
    history.replaceState(null, '', `/#sensor=${sensorId}`)
    const el = fillFixtures(islandFrom('area.gohtml', 'panel'))
    document.body.append(el)
    mount(el)
    return el
  }

  beforeEach(() => {
    resetViewStateForTests()
    setSensors(null)
  })
  afterEach(() => {
    resetViewStateForTests()
    setSensors(null)
    history.replaceState(null, '', '/')
  })

  // Sensor 104 mirrors internal/e2e/e2e_test.go's seeded fixture: an ordinary
  // sensor whose P2 reading carries the 'stuck' quality flag.
  it('renders the flag sentence for a non-ok quality flag', async () => {
    const el = mountPanel(104)
    setSensors({ sensors: { id: [104], quality: ['stuck'], P1: [12], P2: [300] } })

    await vi.waitFor(() => {
      expect(el.querySelector('.panel-flag')).not.toBeNull()
    })
    expect(el.querySelector('.panel-flag').textContent).toBe(PANEL_ATTR_FIXTURES.tFlagStuck)
    el.remove()
  })

  it('renders the close control with its label', async () => {
    const el = mountPanel(104)
    setSensors({ sensors: { id: [104], quality: ['stuck'], P2: [300] } })

    await vi.waitFor(() => {
      expect(el.querySelector('button[data-close]')).not.toBeNull()
    })
    expect(el.querySelector('button[data-close]').textContent).toBe(PANEL_ATTR_FIXTURES.tClose)
    el.remove()
  })

  // Sensor 103 mirrors the other seeded fixture: the P1 COLUMN exists (other
  // sensors report it) but this sensor's entry in it is null — a reported
  // metric with no current reading, which is the case noValue renders. A
  // metric the response does not carry at all is omitted instead, and would
  // make this test pass for the wrong reason, so the P1 label is asserted
  // present alongside the placeholder.
  it('renders the no-value placeholder for a reported metric with no reading', async () => {
    const el = mountPanel(103)
    setSensors({ sensors: { id: [103], quality: ['ok'], P1: [null], P2: [22] } })

    await vi.waitFor(() => {
      expect(el.querySelector('[role="dialog"]')).not.toBeNull()
    })
    expect(el.textContent).toContain('PM10')
    expect(el.querySelector('dl').textContent).toContain(PANEL_ATTR_FIXTURES.tNoValue)
    el.remove()
  })
})
