import { beforeEach, describe, expect, it } from 'vitest'
import {
  SOURCES, filterBySource, getSources, measuredBy, onSourceChange,
  resetSourceFilterForTests, setSourceEnabled, sourceOf,
} from '../sourcefilter.svelte.js'

beforeEach(() => resetSourceFilterForTests())

describe('the source filter', () => {
  it('starts with every source on', () => {
    expect([...getSources()].sort()).toEqual([...SOURCES].sort())
  })

  it('drops the features of a source that is off', () => {
    const features = [
      { properties: { source: 'sensor.community' } },
      { properties: { source: 'eea' } },
    ]
    setSourceEnabled('eea', false)
    const got = filterBySource(features, getSources())
    expect(got).toHaveLength(1)
    expect(got[0].properties.source).toBe('sensor.community')
  })

  // Snapshots written before the source column exists have no source.
  it('treats an absent source as sensor.community', () => {
    const features = [{ properties: {} }]
    expect(filterBySource(features, new Set(['sensor.community']))).toHaveLength(1)
    expect(filterBySource(features, new Set(['eea']))).toHaveLength(0)
  })

  it('notifies subscribers', () => {
    let seen = null
    onSourceChange((s) => { seen = s })
    setSourceEnabled('eea', false)
    expect(seen && seen.has('eea')).toBe(false)
  })
})

describe('what each network measures', () => {
  it('knows the gases are official-only', () => {
    for (const metric of ['SO2', 'O3', 'NO2', 'NOX', 'CO', 'C6H6']) {
      expect(measuredBy('eea', metric)).toBe(true)
      expect(measuredBy('sensor.community', metric)).toBe(false)
    }
  })

  it('knows the weather metrics are citizen-only', () => {
    for (const metric of ['temperature', 'humidity', 'pressure', 'noise_LAeq', 'noise_LA_max']) {
      expect(measuredBy('sensor.community', metric)).toBe(true)
      expect(measuredBy('eea', metric)).toBe(false)
    }
  })

  it('knows both networks measure particulates', () => {
    for (const metric of ['P1', 'P2']) {
      expect(measuredBy('sensor.community', metric)).toBe(true)
      expect(measuredBy('eea', metric)).toBe(true)
    }
  })

  it('assumes an unknown metric is measured by everyone', () => {
    expect(measuredBy('eea', 'brand_new')).toBe(true)
    expect(measuredBy('sensor.community', 'brand_new')).toBe(true)
  })
})

describe('sourceOf', () => {
  it('reads a hex entry directly', () => {
    expect(sourceOf({ source: 'eea', n: 1 })).toBe('eea')
  })

  it('reads a GeoJSON feature through properties', () => {
    expect(sourceOf({ properties: { source: 'eea' } })).toBe('eea')
  })

  it('treats an absent source as sensor.community on either shape', () => {
    expect(sourceOf({ n: 3 })).toBe('sensor.community')
    expect(sourceOf({ properties: {} })).toBe('sensor.community')
  })
})
