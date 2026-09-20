import { describe, it, expect } from 'vitest'
import { parseHash, serialiseHash } from '../viewstate.js'
import { SOURCES, CITIZEN_SOURCE, OFFICIAL_SOURCE } from '../sourcefilter.svelte.js'

const opts = { metrics: ['P1', 'P2', 'temperature'], defaultMetric: 'P2' }
const allSources = new Set(SOURCES)

describe('parseHash', () => {
  it('reads both keys in either order', () => {
    expect(parseHash('#metric=P1&sensor=1234', opts))
      .toEqual({ metric: 'P1', sensorId: 1234, sources: allSources })
    expect(parseHash('#sensor=1234&metric=P1', opts))
      .toEqual({ metric: 'P1', sensorId: 1234, sources: allSources })
  })

  it('falls back to the server default for an unknown metric', () => {
    expect(parseHash('#metric=plutonium', opts).metric).toBe('P2')
  })

  // The two keys are independent: a bad sensor id must not take the metric with
  // it. This is the whole reason validation is per-key rather than a single
  // "is this hash valid" check.
  it('keeps a valid metric when the sensor id is junk', () => {
    expect(parseHash('#metric=temperature&sensor=abc', opts))
      .toEqual({ metric: 'temperature', sensorId: null, sources: allSources })
  })

  it('rejects zero, negative and fractional sensor ids', () => {
    for (const bad of ['0', '-5', '1.5']) {
      expect(parseHash(`#sensor=${bad}`, opts).sensorId).toBeNull()
    }
  })

  it('ignores unknown keys so a future #period= cannot break old links', () => {
    expect(parseHash('#period=7d&metric=P1', opts))
      .toEqual({ metric: 'P1', sensorId: null, sources: allSources })
  })

  it('treats an empty hash as the default state', () => {
    expect(parseHash('', opts)).toEqual({ metric: 'P2', sensorId: null, sources: allSources })
  })

  it('reads a single layers token', () => {
    expect(parseHash('#layers=official', opts).sources).toEqual(new Set([OFFICIAL_SOURCE]))
  })

  it('reads multiple layers tokens, "none", and falls back on junk', () => {
    expect(parseHash('#layers=community,official', opts).sources)
      .toEqual(new Set([CITIZEN_SOURCE, OFFICIAL_SOURCE]))
    expect(parseHash('#layers=none', opts).sources).toEqual(new Set())
    expect(parseHash('#layers=foo', opts).sources).toEqual(allSources)
  })

  it('keeps metric and layers independent', () => {
    expect(parseHash('#metric=P1&layers=official', opts))
      .toEqual({ metric: 'P1', sensorId: null, sources: new Set([OFFICIAL_SOURCE]) })
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
    expect(parseHash(serialiseHash(state, 'P2'), opts))
      .toEqual({ ...state, sources: allSources })
  })

  it('omits layers when all sources are on', () => {
    expect(serialiseHash({ sources: allSources }, 'P2')).toBe('')
  })

  it('writes layers for a subset, in SOURCES order', () => {
    expect(serialiseHash({ sources: new Set([OFFICIAL_SOURCE]) }, 'P2')).toBe('#layers=official')
  })

  it('writes "none" for an empty set', () => {
    expect(serialiseHash({ sources: new Set() }, 'P2')).toBe('#layers=none')
  })

  it('keeps metric working alongside layers', () => {
    expect(serialiseHash({ metric: 'P1', sources: new Set([OFFICIAL_SOURCE]) }, 'P2'))
      .toBe('#metric=P1&layers=official')
  })

  it('round-trips sources through parseHash for the default, subset and empty cases', () => {
    for (const sources of [allSources, new Set([OFFICIAL_SOURCE]), new Set()]) {
      expect(parseHash(serialiseHash({ sources }, 'P2'), opts).sources).toEqual(sources)
    }
  })
})
