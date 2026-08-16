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
import { describe, it, expect, vi, beforeEach } from 'vitest'

// panel.js imports Chart.svelte (for the embedded chart snippet), which
// imports uplot — and uplot touches layout APIs (matchMedia) at import time,
// which jsdom does not provide, even though none of the tests below ever
// render a chart. Same mock as components/__tests__/chart.component.test.js,
// for the same reason: this file is about normaliseSensor/the registry, not
// about uPlot construction.
vi.mock('uplot', () => ({
  default: vi.fn(function () { this.setSize = vi.fn() }),
}))

import { normaliseSensor, flagTextFor } from '../panel.js'
import { setSensors, findSensor } from '../../lib/sensors.svelte.js'

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
