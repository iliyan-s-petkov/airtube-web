import { describe, it, expect, vi } from 'vitest'
import { urlFor, bandsFor, areaFeatures, sensorFeatures, readConfig, debounce } from '../map.js'

// urlFor is the anti-enumeration seam: it is the ONLY place a tier turns into a
// request URL, and it must never accept a bounding box or build one from a
// slug the caller did not explicitly select.
describe('urlFor', () => {
  it('asks for the country aggregate with no per-entity key', () => {
    expect(urlFor('country', null)).toBe('/api/v1/overview')
  })
  it('asks for the city aggregate via the tier query parameter, not a path segment', () => {
    expect(urlFor('city', null)).toBe('/api/v1/overview?tier=city')
  })
  it('asks for one area\'s sensors by the slug the caller passed in, percent-encoded', () => {
    expect(urlFor('sensors', 'sofia')).toBe('/api/v1/area/sofia/sensors')
  })
  it('percent-encodes a slug containing characters that would otherwise change the path', () => {
    expect(urlFor('sensors', 'a/b?c')).toBe('/api/v1/area/a%2Fb%3Fc/sensors')
  })
})

// bandsFor: matching by `metric` field, not array position, so a reordered
// /api/v1/scales response cannot silently recolour the map with the wrong
// band table.
describe('bandsFor', () => {
  const scales = [
    { metric: 'P1', bands: [{ upper: 50, colour: 'x' }] },
    { metric: 'P2', bands: [{ upper: 25, colour: 'y' }] },
  ]

  it('finds the table for the requested metric regardless of array order', () => {
    expect(bandsFor(scales, 'P2')).toEqual([{ upper: 25, colour: 'y' }])
  })
  it('returns an empty table for an unknown metric rather than the first one found', () => {
    expect(bandsFor(scales, 'humidity')).toEqual([])
  })
  it('returns an empty table when scales is not an array (a failed fetch resolved to null)', () => {
    expect(bandsFor(null, 'P2')).toEqual([])
    expect(bandsFor(undefined, 'P2')).toEqual([])
  })
})

// areaFeatures: the choropleth payload maps straight onto features, and
// `covered === false` must render as no-data grey with no numeric value —
// never a band colour, which would claim a confidence the pipeline refuses to
// assert below the 3-sensor threshold.
describe('areaFeatures', () => {
  const scales = [{ metric: 'P2', bands: [{ upper: 10, colour: '#111111' }, { upper: null, colour: '#222222' }] }]

  it('colours a covered area from its value through the metric\'s band table', () => {
    const body = { areas: [{ slug: 'sofia', lon: 23.3, lat: 42.7, covered: true, values: { P2: 5 }, sensor_count: 12 }] }
    const [feature] = areaFeatures(body, 'P2', scales)
    expect(feature.properties.colour).toBe('#111111')
    expect(feature.properties.value).toBe(5)
    expect(feature.geometry.coordinates).toEqual([23.3, 42.7])
  })

  it('renders an uncovered area in no-data grey with no value, even if it carries a stray value', () => {
    const body = { areas: [{ slug: 'vidin', lon: 22.9, lat: 44.0, covered: false, values: { P2: 999 }, sensor_count: 1 }] }
    const [feature] = areaFeatures(body, 'P2', scales)
    expect(feature.properties.colour).toBe('#9ca3af')
    expect(feature.properties.value).toBeNull()
  })

  it('returns no features for a missing or empty areas array', () => {
    expect(areaFeatures({}, 'P2', scales)).toEqual([])
    expect(areaFeatures(null, 'P2', scales)).toEqual([])
  })
})

// sensorFeatures reads the columnar payload — parallel arrays keyed by column
// name, the metric a sibling of the fixed columns — and must keep "sensor does
// not report this metric" (null) distinct from "reported zero".
describe('sensorFeatures', () => {
  const scales = [{ metric: 'P2', bands: [{ upper: 10, colour: '#111111' }, { upper: null, colour: '#222222' }] }]
  const body = {
    sensors: {
      id: [1, 2],
      lon: [23.1, 23.2],
      lat: [42.1, 42.2],
      quality: ['ok', 'stuck'],
      P2: [5, null],
    },
  }

  it('maps each column entry onto one feature, by index', () => {
    const features = sensorFeatures(body, 'P2', scales)
    expect(features).toHaveLength(2)
    expect(features[0].properties).toMatchObject({ id: 1, value: 5, quality: 'ok' })
    expect(features[1].properties).toMatchObject({ id: 2, value: null, quality: 'stuck' })
  })

  it('colours a null metric value as no-data, not as zero', () => {
    const features = sensorFeatures(body, 'P2', scales)
    expect(features[1].properties.colour).toBe('#9ca3af')
  })

  it('returns no features for a missing sensors object', () => {
    expect(sensorFeatures({}, 'P2', scales)).toEqual([])
  })
})

// readConfig reads the server-rendered data-* attributes. Passed a plain
// {dataset} object rather than a real DOM element: readConfig only ever
// touches el.dataset, so this is exactly as pure as any other object-in,
// object-out function here, and stays inside the "no jsdom" rule.
describe('readConfig', () => {
  it('applies the documented defaults when an attribute is absent', () => {
    const cfg = readConfig({ dataset: {} })
    expect(cfg).toMatchObject({ slug: null, zoom: 7, lon: 25.4858, lat: 42.7339, metric: 'P2', basemap: '' })
  })

  it('treats a blank data-basemap as "no basemap configured", not as a broken URL', () => {
    const cfg = readConfig({ dataset: { basemap: '' } })
    expect(cfg.basemap).toBe('')
  })

  it('carries every server-rendered translation string through to cfg.t', () => {
    const cfg = readConfig({
      dataset: {
        tLegend: 'Air quality', tNoData: 'Not enough data', tHint: 'Select an area',
        tRateLimited: 'Retrying', tUnavailable: 'Unavailable',
      },
    })
    expect(cfg.t).toEqual({
      legend: 'Air quality', noData: 'Not enough data', hint: 'Select an area',
      rateLimited: 'Retrying', unavailable: 'Unavailable',
    })
  })

  it('reads numeric attributes as numbers, not strings', () => {
    const cfg = readConfig({ dataset: { zoom: '12', lon: '25.1', lat: '42.2' } })
    expect(cfg.zoom).toBe(12)
    expect(cfg.lon).toBe(25.1)
    expect(cfg.lat).toBe(42.2)
  })
})

// debounce: the 250ms gate between a moveend event and the request it may
// fire. One pinch-zoom gesture emits a dozen moveend events; without this, that
// is a dozen requests and the whole burst.
describe('debounce', () => {
  it('calls the wrapped function once, after the delay, for a burst of calls', () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const debounced = debounce(fn, 250)

    debounced()
    debounced()
    debounced()
    expect(fn).not.toHaveBeenCalled()

    vi.advanceTimersByTime(249)
    expect(fn).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(fn).toHaveBeenCalledTimes(1)

    vi.useRealTimers()
  })

  it('passes the latest call\'s arguments through', () => {
    vi.useFakeTimers()
    const fn = vi.fn()
    const debounced = debounce(fn, 250)

    debounced('first')
    debounced('second')
    vi.advanceTimersByTime(250)

    expect(fn).toHaveBeenCalledWith('second')
    vi.useRealTimers()
  })
})
