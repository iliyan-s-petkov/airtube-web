import { describe, it, expect, vi, beforeEach } from 'vitest'
import { urlFor, bandsFor, areaFeatures, sensorFeatures, readConfig, debounce, loadScales, hintController, initData } from '../map.js'
import { clearCache } from '../../lib/api.js'

// The no-data colour is configuration now (arrives as a data-* attribute), not
// a module constant — restated here as a literal because these tests are about
// feature-mapping logic, not about the specific grey.
const NO_DATA_COLOUR = '#9ca3af'

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
    const [feature] = areaFeatures(body, 'P2', scales, NO_DATA_COLOUR)
    expect(feature.properties.colour).toBe('#111111')
    expect(feature.properties.value).toBe(5)
    expect(feature.geometry.coordinates).toEqual([23.3, 42.7])
  })

  it('renders an uncovered area in no-data grey with no value, even if it carries a stray value', () => {
    const body = { areas: [{ slug: 'vidin', lon: 22.9, lat: 44.0, covered: false, values: { P2: 999 }, sensor_count: 1 }] }
    const [feature] = areaFeatures(body, 'P2', scales, NO_DATA_COLOUR)
    expect(feature.properties.colour).toBe('#9ca3af')
    expect(feature.properties.value).toBeNull()
  })

  it('returns no features for a missing or empty areas array', () => {
    expect(areaFeatures({}, 'P2', scales, NO_DATA_COLOUR)).toEqual([])
    expect(areaFeatures(null, 'P2', scales, NO_DATA_COLOUR)).toEqual([])
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
    const features = sensorFeatures(body, 'P2', scales, NO_DATA_COLOUR)
    expect(features).toHaveLength(2)
    expect(features[0].properties).toMatchObject({ id: 1, value: 5, quality: 'ok' })
    expect(features[1].properties).toMatchObject({ id: 2, value: null, quality: 'stuck' })
  })

  it('colours a null metric value as no-data, not as zero', () => {
    const features = sensorFeatures(body, 'P2', scales, NO_DATA_COLOUR)
    expect(features[1].properties.colour).toBe('#9ca3af')
  })

  it('returns no features for a missing sensors object', () => {
    expect(sensorFeatures({}, 'P2', scales, NO_DATA_COLOUR)).toEqual([])
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

  // toEqual, not toMatchObject: cfg.t must contain exactly these keys. A field
  // read from an attribute no template renders any more (t.noData, dropped with
  // data-t-no-data) is the same "written but never read" asymmetry pointing the
  // other way, and it silently resolves to ''.
  it('carries every server-rendered translation string through to cfg.t, and no others', () => {
    const cfg = readConfig({
      dataset: {
        tLegend: 'Air quality', tHint: 'Select an area',
        tRateLimited: 'Retrying', tUnavailable: 'Unavailable',
        tNoData: 'Not enough data', // no longer rendered; must not reappear in cfg
      },
    })
    expect(cfg.t).toEqual({
      legend: 'Air quality', hint: 'Select an area',
      rateLimited: 'Retrying', unavailable: 'Unavailable',
    })
  })

  it('reads numeric attributes as numbers, not strings', () => {
    const cfg = readConfig({ dataset: { zoom: '12', lon: '25.1', lat: '42.2' } })
    expect(cfg.zoom).toBe(12)
    expect(cfg.lon).toBe(25.1)
    expect(cfg.lat).toBe(42.2)
  })

  // The paint values and zoom thresholds are configuration, not a JS default:
  // readConfig must read the exact server-rendered attribute, not a name that
  // happens to look similar.
  it('reads the frontend paint values and zoom thresholds from their data-* attributes', () => {
    const cfg = readConfig({
      dataset: {
        noDataColour: '#9ca3af',
        markerStrokeColour: '#ffffff',
        emptyBasemapColour: '#eef2f5',
        zoomCity: '9',
        zoomSensor: '11',
      },
    })
    expect(cfg.noDataColour).toBe('#9ca3af')
    expect(cfg.markerStrokeColour).toBe('#ffffff')
    expect(cfg.emptyBasemapColour).toBe('#eef2f5')
    expect(cfg.zoomCity).toBe(9)
    expect(cfg.zoomSensor).toBe(11)
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

// hintController is the precedence rule: an error outranks the routine tier
// hint permanently. `render` is the only side effect, so these drive the real
// rule with an array as the sink — no DOM, and no second implementation that
// could disagree with the one the page runs.
describe('hintController', () => {
  it('shows and clears the routine hint while no error is outstanding', () => {
    const rendered = []
    const c = hintController((t) => rendered.push(t))

    c.showHint('Select an area')
    c.showHint('')

    expect(rendered).toEqual(['Select an area', ''])
  })

  it('refuses to let a later showHint erase an error', () => {
    const rendered = []
    const c = hintController((t) => rendered.push(t))

    c.showError('Map data is unavailable right now')
    c.showHint('')
    c.showHint('Select an area')

    expect(rendered).toEqual(['Map data is unavailable right now'])
  })
})

// loadScales: without the band tables every marker is painted NO_DATA_COLOUR,
// so a failed /api/v1/scales produces a uniformly grey map. On an air-quality
// site that reads as "the whole country has insufficient data" — a confident
// wrong answer — rather than as "the colour scale did not load". The hint is
// what makes the two distinguishable.
describe('loadScales', () => {
  const cfg = { t: { unavailable: 'Map data is unavailable right now' } }

  function stubChrome() {
    const calls = []
    return {
      calls,
      showHint: (text) => calls.push(['hint', text]),
      showError: (text) => calls.push(['error', text]),
    }
  }

  it('explains an all-grey map when the scales request fails', async () => {
    const chrome = stubChrome()
    const scales = await loadScales(chrome, cfg, async () => { throw new Error('HTTP 500') })

    expect(scales).toBe(null)
    // showError, not showHint: the scales are never refetched, so the condition
    // is permanent for this page and the message must outrank the tier hint.
    expect(chrome.calls).toEqual([['error', cfg.t.unavailable]])
  })

  it('says nothing when the scales load, so the banner keeps its meaning', async () => {
    const chrome = stubChrome()
    const tables = [{ metric: 'P2', bands: [{ upper: 10, colour: '#000000' }] }]
    const scales = await loadScales(chrome, cfg, async () => tables)

    expect(scales).toBe(tables)
    expect(chrome.calls).toEqual([])
  })

  it('asks the scales endpoint and nothing else', async () => {
    const urls = []
    await loadScales(stubChrome(), cfg, async (url) => { urls.push(url); return [] })
    expect(urls).toEqual(['/api/v1/scales'])
  })
})

// initData is the ORDERING test, and it is the one that matters. The three
// loadScales cases above all passed while the fix was unreachable in
// production: initData runs refresh immediately afterwards, refresh calls
// showHint('') whenever the zoom's tier is served as-is, and clear-on-empty
// then wiped the explanation before the visitor ever saw it. Nothing that
// exercises either function alone can observe that.
//
// Driven through the REAL hintController with an array sink and a fake map
// object (getZoom/getSource only — refresh touches nothing else), over a
// stubbed global fetch. No jsdom, no MapLibre, no component render.
describe('initData ordering', () => {
  const cfg = {
    metric: 'P2',
    // The zoom thresholds tierFor needs — previously hardcoded 9/11 inside
    // tier.js, now configuration threaded through cfg, same as the server
    // would render them from airbg.yaml's frontend.zoom_city/zoom_sensor.
    zoomCity: 9,
    zoomSensor: 11,
    noDataColour: '#9ca3af',
    t: { hint: 'Select an area', unavailable: 'Map data is unavailable right now' },
  }

  // Zoom 7 is the index page's server-rendered default, where tierFor gives
  // 'country' and refresh serves it as-is — so refresh takes the showHint('')
  // path. That is the production scenario, not a contrived one.
  function fakeMap(zoom = 7) {
    const painted = []
    return {
      painted,
      getZoom: () => zoom,
      getSource: () => ({ setData: (data) => painted.push(data) }),
    }
  }

  function stubFetch({ scalesOk }) {
    return vi.fn(async (url) => {
      if (url === '/api/v1/scales') {
        if (!scalesOk) return { ok: false, status: 500, headers: new Headers() }
        return {
          ok: true, status: 200, headers: new Headers(),
          json: async () => [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }] }],
        }
      }
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ areas: [{ slug: 'sofia', lon: 23.3, lat: 42.7, covered: true, values: { P2: 5 }, sensor_count: 9 }] }),
      }
    })
  }

  beforeEach(() => { clearCache() })

  it('still explains the grey map after refresh has run', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: false }))
    const rendered = []
    const chrome = hintController((t) => rendered.push(t))
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null }

    await initData(map, state, cfg, chrome)

    // The FINAL displayed state, not "was called with it at some point":
    // refresh's showHint('') runs after the error and used to win.
    expect(rendered.at(-1)).toBe(cfg.t.unavailable)
    expect(state.scales).toBe(null)
    // And the aggregate fetch still succeeded, so this is the exact scenario
    // the fix exists for: real markers, no colour scale, uniformly grey.
    expect(map.painted).toHaveLength(1)
    expect(map.painted[0].features[0].properties.colour).toBe(NO_DATA_COLOUR)
  })

  it('leaves the banner empty when everything loads', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: true }))
    const rendered = []
    const chrome = hintController((t) => rendered.push(t))
    const map = fakeMap()

    await initData(map, { slug: null, tier: null, scales: null }, cfg, chrome)

    expect(rendered.at(-1)).toBe('')
    expect(map.painted[0].features[0].properties.colour).toBe('#00ff00')
  })
})
