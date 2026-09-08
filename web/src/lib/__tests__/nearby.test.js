import { describe, it, expect } from 'vitest'
import { NEARBY_KEYS, nearbyOptions, nearbySources } from '../nearby.js'

const LABELS = { low: 'Наблизо · минимум', median: 'Наблизо · медиана', high: 'Наблизо · максимум' }

const args = (extra) => ({
  slug: 'ovcha-kupel',
  metric: 'P2',
  query: 'period=24h',
  keys: NEARBY_KEYS,
  colour: 'rgb(4, 5, 6)',
  scale: 'y',
  unit: 'µg/m³',
  labels: LABELS,
  ...extra,
})

describe('nearbySources', () => {
  // Three lines, one url. The endpoint returns all three columns in one banded
  // body, and a url per line would ask the server the same question three times
  // — on the heaviest route in the service.
  it('gives every chosen line the same banded url', () => {
    const sources = nearbySources(args())
    expect(sources).toHaveLength(3)
    expect(new Set(sources.map((s) => s.url)).size).toBe(1)
    expect(sources[0].url).toContain('/api/v1/area/ovcha-kupel/series')
    expect(sources[0].url).toContain('band=1')
    expect(sources[0].url).toContain('metric=P2')
    expect(sources[0].url).toContain('period=24h')
  })

  // The columns are what tell the three apart. Reading them all from "v" would
  // draw the median three times and call two of them the extremes.
  it('reads a different column per line', () => {
    expect(nearbySources(args()).map((s) => s.column)).toEqual(['lo', 'v', 'hi'])
  })

  it('draws only the lines that were ticked', () => {
    const sources = nearbySources(args({ keys: ['high'] }))
    expect(sources).toHaveLength(1)
    expect(sources[0].column).toBe('hi')
    expect(sources[0].label).toBe(LABELS.high)
  })

  // Ticking high and then low must not put the high line first: the legend
  // would reorder itself under the reader as they choose.
  it('keeps the drawing order regardless of the order they were ticked', () => {
    expect(nearbySources(args({ keys: ['high', 'low'] })).map((s) => s.column)).toEqual(['lo', 'hi'])
  })

  // Same colour, different dashes: three hues would read as three more metrics
  // rather than as one annotation on the sensor's own line.
  it('dashes the lines and shares one colour with the sensor axis', () => {
    const sources = nearbySources(args())
    expect(new Set(sources.map((s) => s.colour))).toEqual(new Set(['rgb(4, 5, 6)']))
    expect(new Set(sources.map((s) => String(s.dash))).size).toBe(3)
    for (const s of sources) {
      expect(s.scale).toBe('y')
      expect(s.unit).toBe('µg/m³')
    }
  })

  // Nothing ticked is the default, and an area the map never loaded has no slug
  // to ask about. Both must draw the sensor alone rather than a broken url.
  it('returns nothing without a selection, a slug, a metric or a window', () => {
    expect(nearbySources(args({ keys: [] }))).toEqual([])
    expect(nearbySources(args({ slug: null }))).toEqual([])
    expect(nearbySources(args({ metric: '' }))).toEqual([])
    expect(nearbySources(args({ query: '' }))).toEqual([])
  })

  // A slug reaches a URL path. It comes from the server today, but the encode
  // is what keeps that from being a rule held by discipline.
  it('encodes the slug and the metric', () => {
    const [s] = nearbySources(args({ slug: 'a/b', keys: ['median'] }))
    expect(s.url).toContain('/api/v1/area/a%2Fb/series')
  })
})

describe('nearbyOptions', () => {
  it('offers the three lines in drawing order, each with its label', () => {
    expect(nearbyOptions(LABELS)).toEqual([
      { key: 'low', label: LABELS.low },
      { key: 'median', label: LABELS.median },
      { key: 'high', label: LABELS.high },
    ])
  })

  // A missing catalogue key must leave a readable button rather than a blank
  // one: the key itself says more than nothing does.
  it('falls back to the key when a label is missing', () => {
    expect(nearbyOptions({}).map((o) => o.label)).toEqual(NEARBY_KEYS)
  })
})
