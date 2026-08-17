// @vitest-environment jsdom
//
// jsdom, not the default node environment: the repaint test below drives
// mount() through a real container element and a real `location.hash` /
// `hashchange`, which the rest of this file's pure-logic tests do not need
// but do not mind either — jsdom is a superset, not a different behaviour,
// for code that touches no DOM.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { urlFor, bandsFor, areaFeatures, sensorFeatures, readConfig, debounce, loadScales, hintController, initData, layerPaint, markerPaint, metricNote, blankStyle, mapStyle, registerProtocols, installErrorHandler, mount, locateVisitor, locateMe, areaPath } from '../map.js'
import { clearCache } from '../../lib/api.js'
import { resetViewStateForTests, getViewState } from '../../lib/viewstate.svelte.js'
import { findSensor, setSensors } from '../../lib/sensors.svelte.js'

// mount() constructs a REAL MapLibreMap, which needs a working WebGL canvas —
// out of reach under jsdom (see the "no jsdom" rule respected everywhere else
// in this file). Mocked here, ONLY for the mountTestMap-based tests below, so
// mount() can be driven end to end (readConfig -> addLayer -> the metric
// subscription) without a real renderer. Every other describe block in this
// file drives map.js's exported functions directly with plain objects, never
// through mount(), so this mock never applies to them in practice — but
// vi.mock is file-scoped, so it is declared once, here.
vi.mock('maplibre-gl', () => {
  class FakeMap {
    constructor(options) {
      this.options = options
      this.handlers = {}
      this.setPaintProperty = vi.fn()
      this.addSource = vi.fn()
      this.addLayer = vi.fn()
      this.getZoom = vi.fn(() => 7)
      this.getSource = vi.fn(() => ({ setData: vi.fn() }))
      // Spied so the locateVisitor tests below can assert a "geoip" response
      // jumps the map, and that a "default"/rejected response does not.
      this.jumpTo = vi.fn()
    }
    // map.on('click', LAYER_ID, cb) carries the layer id as a second
    // argument; every other event map.js registers is map.on(event, cb).
    on(event, a, b) {
      this.handlers[event] = event === 'click' ? b : a
    }
  }
  return { Map: FakeMap, addProtocol: vi.fn() }
})

// The minimum harness this task needs: mount a map island against a fresh
// container and fresh viewstate singleton, fire the 'load' handler mount()
// registers (which is where the metric-follow subscription is wired — see
// map.js), and hand the test the fake map plus the chrome object mount()
// returns. No harness by this name or shape existed before this task; the
// brief assumed one without it being written, so this is built fresh, kept to
// exactly what the two tests below need.
function mountTestMap({ metric }) {
  resetViewStateForTests()
  history.replaceState(null, '', `/#metric=${metric}`)

  const el = document.createElement('div')
  el.dataset.metric = metric
  el.dataset.metrics = 'P1,P2,temperature'
  el.dataset.zoom = '7'
  el.dataset.lon = '25.4858'
  el.dataset.lat = '42.7339'
  el.dataset.noDataColour = '#9ca3af'
  el.dataset.unscaledColour = '#94a3b8'
  el.dataset.markerStrokeColour = '#ffffff'
  el.dataset.emptyBasemapColour = '#eef2f5'
  el.dataset.zoomCity = '9'
  el.dataset.zoomSensor = '11'

  const { map, chrome } = mount(el)
  // Fired, not awaited: mount()'s 'load' handler registers the metric
  // subscription SYNCHRONOUSLY, before its first `await` (see map.js's own
  // comment on why) — so by the time this call returns to mountTestMap, the
  // subscription already exists, even though the handler's own data-loading
  // tail (initData) is still pending in the microtask queue. A real
  // MapLibreMap fires 'load' itself, asynchronously, once its style is
  // ready; this harness fires it eagerly instead, since the fake map here
  // has no style to wait for.
  map.handlers.load()
  return { map, chrome }
}

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
  it('reads the opening view from the server-rendered attributes', () => {
    const cfg = readConfig({ dataset: { zoom: '7', lon: '25.4858', lat: '42.7339' } })
    expect(cfg).toMatchObject({ slug: null, zoom: 7, lon: 25.4858, lat: 42.7339, basemap: '' })
  })

  // The opening view is configuration too (frontend.default_zoom/_lon/_lat, or
  // the area's own centre). A JS-side 7/25.4858/42.7339 would agree with
  // today's airbg.yaml by coincidence while hiding a server that stopped
  // rendering the attributes — the same rule data-metric follows below.
  it('has no JS-side default for the opening view', () => {
    const cfg = readConfig({ dataset: {} })
    expect(cfg.zoom).toBeNaN()
    expect(cfg.lon).toBeNaN()
    expect(cfg.lat).toBeNaN()
  })

  // series.default_metric is configuration, not a JS default: a missing
  // data-metric attribute must surface as undefined, never as a silent 'P2'.
  // A hardcoded fallback here would (a) mask a server bug that stops
  // rendering the attribute and (b) be exactly the duplicated constant this
  // phase exists to delete — 'P2' would keep working today by coincidence
  // even if airbg.yaml's series.default_metric changed to something else.
  it('reads metric from data-metric with no JS-side default', () => {
    expect(readConfig({ dataset: {} }).metric).toBeUndefined()
    expect(readConfig({ dataset: { metric: 'P1' } }).metric).toBe('P1')
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
        tUnscaled: 'No air-quality scale for this metric',
        tLocateButton: 'Find me', tLocateDenied: 'Location access was denied.',
        tLocateFailed: 'We could not determine your location.',
        // Neither is rendered any more; toEqual below is what keeps them from
        // reappearing in cfg.t. tLocateOutside went with the unreachable
        // outside-coverage branch (nearestArea has no distance cutoff).
        tLocateOutside: 'You appear to be outside the mapped area.',
        tNoData: 'Not enough data',
      },
    })
    expect(cfg.t).toEqual({
      legend: 'Air quality', hint: 'Select an area',
      rateLimited: 'Retrying', unavailable: 'Unavailable',
      unscaled: 'No air-quality scale for this metric',
      locateButton: 'Find me', locateDenied: 'Location access was denied.',
      locateFailed: 'We could not determine your location.',
    })
  })

  // data-metrics is the same attribute (and same parseMetricList) the switcher
  // island reads — getViewState needs the full metric list to validate a
  // metric read from the hash before adopting it.
  it('reads the metric list from data-metrics with parseMetricList\'s own blank-input rule', () => {
    expect(readConfig({ dataset: { metrics: 'P1,P2,temperature' } }).metrics).toEqual(['P1', 'P2', 'temperature'])
    expect(readConfig({ dataset: {} }).metrics).toEqual([])
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
        unscaledColour: '#94a3b8',
        markerStrokeColour: '#ffffff',
        emptyBasemapColour: '#eef2f5',
        zoomCity: '9',
        zoomSensor: '11',
      },
    })
    expect(cfg.noDataColour).toBe('#9ca3af')
    expect(cfg.unscaledColour).toBe('#94a3b8')
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

  // J3 (review round 2): refresh must call tierFor with cfg.zoomCity and
  // cfg.zoomSensor, not the old hardcoded 9/11 that used to live in tier.js.
  // The fixture above cannot catch a `tierFor(zoom, 9, 11)` mutation because
  // it happens to use zoomCity: 9, zoomSensor: 11 too — a hardcoded call and
  // a config-reading call produce IDENTICAL behaviour at those thresholds.
  // This test uses DIFFERENT thresholds (3 and 20, borrowed from tier.test.js's
  // own "honours whatever thresholds the caller passes" case) so the two
  // implementations diverge: at zoom 7, cfg.zoomCity=3/zoomSensor=20 selects
  // the CITY tier, while a hardcoded 9/11 would still select COUNTRY. The
  // aggregate request URL is the observable difference.
  it('threads cfg.zoomCity and cfg.zoomSensor into the tier decision, not fixed thresholds', async () => {
    const requestedURLs = []
    vi.stubGlobal('fetch', vi.fn(async (url) => {
      requestedURLs.push(url)
      if (url === '/api/v1/scales') {
        return {
          ok: true, status: 200, headers: new Headers(),
          json: async () => [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }] }],
        }
      }
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ areas: [] }),
      }
    }))
    const offThresholdCfg = { ...cfg, zoomCity: 3, zoomSensor: 20 }
    const chrome = hintController(() => {})
    const map = fakeMap(7) // country under the old 9/11; city under 3/20

    await initData(map, { slug: null, tier: null, scales: null }, offThresholdCfg, chrome)

    const aggregateURL = requestedURLs.find((u) => u !== '/api/v1/scales')
    expect(aggregateURL).toBe('/api/v1/overview?tier=city')
    expect(aggregateURL).not.toBe('/api/v1/overview')
  })
})

// J1 (review round 2): the circle layer's stroke colour must come from
// cfg.markerStrokeColour, not from any other config field. This is built by
// layerPaint(cfg) (named markerPaint before task 7, renamed to make room for
// the metric-aware markerPaint(bands, opts) below — see that function's own
// comment for why one name could not serve both), extracted out of mount()'s
// map.on('load', ...) callback specifically so it can be tested without a
// real MapLibre map.
describe('layerPaint', () => {
  it('reads circle-stroke-color from cfg.markerStrokeColour', () => {
    const cfg = { markerStrokeColour: '#ffffff', noDataColour: '#9ca3af' }
    const paint = layerPaint(cfg)
    expect(paint['circle-stroke-color']).toBe('#ffffff')
  })
})

describe('blankStyle', () => {
  it('paints the background layer with the colour passed in', () => {
    const style = blankStyle('#eef2f5')
    expect(style.layers[0].paint['background-color']).toBe('#eef2f5')
  })
})

// J2 (review round 2): mount() must call blankStyle(cfg.emptyBasemapColour),
// not blankStyle(cfg.noDataColour) or any other config field. blankStyle's
// own test above cannot see this — it only ever gets the value its caller
// already picked. mapStyle is the call site itself, extracted so this
// argument-selection bug is directly testable without a real MapLibre map.
describe('mapStyle', () => {
  it('uses the configured basemap URL when one is set', () => {
    const cfg = { basemap: 'https://tiles.example/style.json', emptyBasemapColour: '#eef2f5' }
    expect(mapStyle(cfg)).toBe('https://tiles.example/style.json')
  })

  it('falls back to a flat background painted with cfg.emptyBasemapColour, not a different field', () => {
    const cfg = { basemap: '', emptyBasemapColour: '#eef2f5', noDataColour: '#9ca3af' }
    const style = mapStyle(cfg)
    expect(style.layers[0].paint['background-color']).toBe('#eef2f5')
  })
})

// The pmtiles:// protocol must be registered before any style referencing it
// loads, and exactly once — MapLibre's addProtocol is global, and a second
// registration for the same scheme replaces the first silently.
describe('registerProtocols', () => {
  it('registers pmtiles exactly once across repeated calls', () => {
    const seen = []
    const add = (scheme, fn) => seen.push([scheme, typeof fn])
    registerProtocols(add)
    registerProtocols(add)
    expect(seen).toEqual([['pmtiles', 'function']])
  })
})

// installErrorHandler is what keeps a style-load failure (missing archive,
// unreachable tiles host, a CSP that blocks the fetch) from taking the sensor
// markers down with it: it must log and must never let the error propagate
// out of the 'error' callback. Driven with a fake map exposing only `.on` —
// installErrorHandler needs nothing heavier, unlike the 'load' handler which
// genuinely needs a real MapLibre instance for addSource/addLayer.
describe('installErrorHandler', () => {
  function fakeMap() {
    let handler
    return {
      on: (event, cb) => { if (event === 'error') handler = cb },
      trigger: (e) => handler(e),
    }
  }

  it('logs a warning when the style fails to load', () => {
    const warnings = []
    const map = fakeMap()
    installErrorHandler(map, (...args) => warnings.push(args))

    map.trigger({ error: { message: 'style fetch failed' } })

    expect(warnings).toHaveLength(1)
  })

  it('logs once, not once per error event', () => {
    const warnings = []
    const map = fakeMap()
    installErrorHandler(map, (...args) => warnings.push(args))

    map.trigger({ error: { message: 'first' } })
    map.trigger({ error: { message: 'second' } })

    expect(warnings).toHaveLength(1)
  })

  it('does not let the error propagate out of the callback', () => {
    const map = fakeMap()
    installErrorHandler(map, () => {})

    expect(() => map.trigger({ error: { message: 'boom' } })).not.toThrow()
  })
})

describe('mapStyle with a self-hosted basemap', () => {
  it('passes the style URL through untouched', () => {
    const url = 'https://tiles.airbg.org/style.json'
    expect(mapStyle({ basemap: url, emptyBasemapColour: '#eef2f5' })).toBe(url)
  })

  it('falls back to a flat colour when no basemap is configured', () => {
    expect(mapStyle({ basemap: '', emptyBasemapColour: '#eef2f5' }))
      .toEqual(blankStyle('#eef2f5'))
  })
})

// Three different facts must not share one colour: "no reading" (grey),
// "this metric has no band table" (unscaledColour), and a real band value.
// Evaluates the one MapLibre expression shape markerPaint's !scaled branch
// produces — ['case', ['==', ['get', 'value'], null], whenNull, whenNotNull]
// — against a fake feature's properties. Used instead of a bare
// JSON.stringify/toContain check so the unscaled test below proves the
// actual no-reading/has-reading DISTINCTION (both branches individually),
// not just "some colour string is present somewhere in the JSON" — a check
// that a mutation swapping the two colours would still pass.
function evalUnscaledCase(expr, properties) {
  const [op, [cmp, [, key], compareTo], whenTrue, whenFalse] = expr
  if (op !== 'case' || cmp !== '==') throw new Error(`unexpected expression shape: ${JSON.stringify(expr)}`)
  return properties[key] === compareTo ? whenTrue : whenFalse
}

describe('unscaled metrics', () => {
  const scales = [{ metric: 'P2', bands: [{ upper: 5, colour: '#50f0e6' }] }]

  it('keeps "no reading" distinct from "has a reading" when the metric has no scale', () => {
    const paint = markerPaint([], { noDataColour: '#999999', unscaledColour: '#94a3b8', scaled: false })
    expect(evalUnscaledCase(paint, { value: null })).toBe('#999999')
    expect(evalUnscaledCase(paint, { value: 12 })).toBe('#94a3b8')
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

// mount() end to end, through the fake MapLibreMap declared at the top of
// this file: does changing vs.metric (via a hashchange, the same seam a
// Back/Forward navigation or an external link uses) reach the layer's paint
// property, without a new map and without re-registering the layer.
//
// getViewState is a page-lifetime singleton with no reset seam of its own
// (see viewstate.svelte.js) — nothing exercised it until this task, so
// without resetViewStateForTests() in beforeEach, whichever it() runs FIRST
// in this file (or in another file sharing this Vitest worker) would decide
// every later test's starting metric/hash. resetViewStateForTests() is a
// test-only export added specifically for this hazard.
describe('mount() follows the store metric', () => {
  beforeEach(() => { resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  it('repaints when the store metric changes', async () => {
    const { map } = mountTestMap({ metric: 'P2' })
    // mount() always paints once for the metric the page opened on (see the
    // comment above the explicit call in map.js), so a bare
    // "toHaveBeenCalled()" after the hashchange would pass even if the
    // store subscription itself were gutted into a no-op — that initial
    // call alone satisfies it. Wait for that first paint and record its
    // count, so the assertion below can only pass if the hashchange
    // triggers a REPAINT ON TOP OF it, which is what this test is for.
    await vi.waitFor(() => expect(map.setPaintProperty).toHaveBeenCalled())
    const callsBeforeChange = map.setPaintProperty.mock.calls.length

    location.hash = '#metric=P1'
    dispatchEvent(new HashChangeEvent('hashchange'))

    await vi.waitFor(() => {
      expect(map.setPaintProperty.mock.calls.length).toBeGreaterThan(callsBeforeChange)
    })
  })
})

// Task 9: the sensor tier's response body must reach the panel, not just the
// map layer, and a click on a sensor marker must open it — through the same
// viewstate singleton the switcher already shares, never by the map
// rendering a panel of its own (see islands/panel.js).
function mountSensorTierMap({ metric = 'P2' } = {}) {
  resetViewStateForTests()
  history.replaceState(null, '', '/')

  const el = document.createElement('div')
  // A fixed slug, above zoomSensor: on an area page tierFor picks 'sensors'
  // without needing a click first (see map.js's own comment on state.slug).
  el.dataset.slug = 'sofia'
  el.dataset.metric = metric
  el.dataset.metrics = 'P1,P2'
  el.dataset.zoom = '12'
  el.dataset.lon = '23.3'
  el.dataset.lat = '42.7'
  el.dataset.noDataColour = '#9ca3af'
  el.dataset.unscaledColour = '#94a3b8'
  el.dataset.markerStrokeColour = '#ffffff'
  el.dataset.emptyBasemapColour = '#eef2f5'
  el.dataset.zoomCity = '9'
  el.dataset.zoomSensor = '11'

  const { map, chrome } = mount(el)
  // FakeMap.getZoom is hardcoded to 7 (see the vi.mock at the top of this
  // file) — every other test in this file relies on that fixed value, so it
  // is overridden here rather than in the mock itself, to reach the sensors
  // tier without disturbing them.
  map.getZoom = () => 12
  map.handlers.load()
  return { map, chrome }
}

function stubSensorTierFetch() {
  return vi.fn(async (url) => {
    if (url === '/api/v1/scales') {
      return { ok: true, status: 200, headers: new Headers(), json: async () => [] }
    }
    return {
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ sensors: { id: [42], lon: [23.3], lat: [42.7], quality: ['ok'], P2: [12] } }),
    }
  })
}

describe('sensor tier: registry + marker click', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests(); setSensors(null) })
  afterEach(() => { resetViewStateForTests(); setSensors(null) })

  // Mutation 3 from the task brief, adapted to this design: deleting the
  // `if (effective === 'sensors') setSensors(body)` call in map.js's
  // refresh() must fail this test — findSensor would stay null forever,
  // and the panel would never resolve a marker's own click, let alone a
  // deep link that arrived before the fetch did.
  it('publishes the sensor-tier response into the registry', async () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    mountSensorTierMap()

    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    expect(findSensor(42).values.P2).toBe(12)
  })

  it('opens the panel (viewstate.sensorId) when a sensor marker is clicked', async () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())

    map.handlers.click({ features: [{ properties: { id: 42 } }] })

    expect(getViewState({ metrics: ['P2'], defaultMetric: 'P2' }).sensorId).toBe(42)
  })

  // The two branches (slug vs id) are mutually exclusive: an aggregate
  // marker click must not also open the panel.
  it('still selects an area, and does not open the panel, when an aggregate marker is clicked', async () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(map.setPaintProperty).toHaveBeenCalled())

    map.handlers.click({ features: [{ properties: { slug: 'plovdiv' } }] })

    expect(getViewState({ metrics: ['P2'], defaultMetric: 'P2' }).sensorId).toBeNull()
  })

  it('ignores a click with no recognisable feature properties', () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    const { map } = mountSensorTierMap()

    expect(() => map.handlers.click({ features: [{ properties: {} }] })).not.toThrow()
    expect(() => map.handlers.click({ features: [] })).not.toThrow()
  })
})

// Task 10, fix round 1: mountTestMap's harness sets no data-slug, so
// locateVisitor DOES run during the mount()-based tests above via the
// 'load' handler — but nothing there ever mocked fetchJSON, so only the
// applyLocate(null, …) "stay" branch (a rejected real fetch under jsdom) was
// ever exercised. The "geoip" branch — the one that calls map.jumpTo and
// adopts a slug, which is what unlocks the per-area sensor tier for a
// visitor the server actually placed — had never run under test. Driven
// directly here through the exported function and its fetchJSON seam,
// rather than through mount(), so the injected response body is the only
// thing standing between "stay" and "move".
describe('locateVisitor', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  function fakeMap() {
    return {
      jumpTo: vi.fn(),
      getZoom: vi.fn(() => 7),
      getSource: vi.fn(() => ({ setData: vi.fn() })),
    }
  }

  function fakeChrome() {
    return { showHint: vi.fn(), showError: vi.fn(), showNote: vi.fn() }
  }

  const cfg = {
    lon: 25.4858, lat: 42.7339, zoom: 7,
    zoomCity: 9, zoomSensor: 11, metric: 'P2', noDataColour: '#9ca3af',
    t: { hint: 'h', unavailable: 'u' },
  }

  // On a "move", locateVisitor's own refresh() call still goes through the
  // real getJSON (only the /api/v1/locate lookup itself is injected), so the
  // global fetch is stubbed to satisfy that forced repaint quietly rather
  // than logging a real network failure.
  function stubOverviewFetch() {
    return vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))
  }

  // The load-bearing pair: adopting body.slug straight off a "default"
  // response — or moving at all — is exactly the spoofing-defence bypass
  // described in internal/api/locate_test.go:81. The server answers
  // "default" both when it cannot place the visitor and when the Cloudflare
  // geo headers came from an untrusted peer; the frontend must never read
  // that as a placement.
  it('does not jump and leaves state.slug unset for source: "default"', async () => {
    vi.stubGlobal('fetch', stubOverviewFetch())
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null }
    const chrome = fakeChrome()
    const fetchJSON = vi.fn().mockResolvedValue({ source: 'default', slug: 'bg', lon: 25.4, lat: 42.7, zoom: 7 })

    await locateVisitor(map, state, cfg, chrome, fetchJSON)

    expect(map.jumpTo).not.toHaveBeenCalled()
    expect(state.slug).toBeNull()
  })

  it('jumps to and adopts the slug for source: "geoip"', async () => {
    vi.stubGlobal('fetch', stubOverviewFetch())
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null }
    const chrome = fakeChrome()
    const fetchJSON = vi.fn().mockResolvedValue({ source: 'geoip', slug: 'sofia', lon: 23.32, lat: 42.7, zoom: 11 })

    await locateVisitor(map, state, cfg, chrome, fetchJSON)

    expect(map.jumpTo).toHaveBeenCalledWith({ center: [23.32, 42.7], zoom: 11 })
    expect(state.slug).toBe('sofia')
  })

  // The wrapped-fetch requirement from the brief: a rejected lookup must land
  // in the same "stay put" branch rather than throwing out of the map's init.
  it('does not jump when the lookup rejects', async () => {
    vi.stubGlobal('fetch', stubOverviewFetch())
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null }
    const chrome = fakeChrome()
    const fetchJSON = vi.fn().mockRejectedValue(new Error('network'))

    await expect(locateVisitor(map, state, cfg, chrome, fetchJSON)).resolves.toBeUndefined()
    expect(map.jumpTo).not.toHaveBeenCalled()
    expect(state.slug).toBeNull()
  })
})

// areaPath: the client-side navigation target locateMe hands to `navigate`.
// Mirrors internal/web/pages.go's Routes, which registers "/area/{slug}"
// under both "" and "/en" — a plain "/area/{slug}" from an /en/ visitor would
// silently drop them back to the default language on click.
describe('areaPath', () => {
  afterEach(() => { history.replaceState(null, '', '/') })

  it('builds an unprefixed path from the default-language root', () => {
    history.replaceState(null, '', '/')
    expect(areaPath('sofia')).toBe('/area/sofia')
  })

  it('keeps the /en prefix for an English visitor', () => {
    history.replaceState(null, '', '/en/')
    expect(areaPath('sofia')).toBe('/en/area/sofia')

    history.replaceState(null, '', '/en/area/plovdiv')
    expect(areaPath('sofia')).toBe('/en/area/sofia')
  })

  it('percent-encodes the slug', () => {
    history.replaceState(null, '', '/')
    expect(areaPath('a/b')).toBe('/area/a%2Fb')
  })
})

// locateMe: the PRECISE, user-initiated path. Never touches the network with
// a coordinate — nearestArea resolves it against state.areas entirely in the
// browser (see nearest.js), and only the resulting slug is handed to
// `navigate`.
describe('locateMe', () => {
  const cfg = {
    t: {
      locateDenied: 'Location access was denied.',
      locateFailed: 'We could not determine your location.',
    },
  }

  function fakeChrome() {
    return { showHint: vi.fn(), showError: vi.fn(), showNote: vi.fn() }
  }

  const areas = [{ slug: 'sofia', lon: 23.32, lat: 42.7, zoom: 11 }]

  it('navigates to the nearest area on a successful fix', () => {
    const state = { areas }
    const chrome = fakeChrome()
    const navigate = vi.fn()
    const geolocation = { getCurrentPosition: (onSuccess) => onSuccess({ coords: { longitude: 23.3, latitude: 42.7 } }) }

    locateMe(state, cfg, chrome, { geolocation, navigate })

    expect(navigate).toHaveBeenCalledWith('/area/sofia')
    expect(chrome.showHint).not.toHaveBeenCalled()
  })

  // nearestArea has no distance cutoff: any non-empty areas array always
  // yields SOME nearest match, however far away. So its own null return can
  // only mean "the area list is empty or has not loaded yet" — never
  // "you're genuinely outside coverage" — and that unknown case must get
  // locateFailed, the honest message. There is deliberately no
  // outside-coverage string to show instead; give nearestArea a real cutoff
  // before adding one back. Covers both null (never loaded, e.g. an area page
  // still at the sensor tier — see state.areas's own comment in map.js) and
  // [] (a country/city response with zero areas).
  it('shows the "could not determine" message when the area list is unknown', () => {
    const navigate = vi.fn()
    const geolocation = { getCurrentPosition: (onSuccess) => onSuccess({ coords: { longitude: 23.3, latitude: 42.7 } }) }

    for (const unknownAreas of [null, []]) {
      const state = { areas: unknownAreas }
      const chrome = fakeChrome()

      locateMe(state, cfg, chrome, { geolocation, navigate })

      expect(navigate).not.toHaveBeenCalled()
      expect(chrome.showHint).toHaveBeenCalledTimes(1)
      expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateFailed)
    }
  })

  // PERMISSION_DENIED === 1 per the Geolocation API spec.
  it('shows the denied message for a PERMISSION_DENIED error', () => {
    const state = { areas }
    const chrome = fakeChrome()
    const geolocation = { getCurrentPosition: (_s, onError) => onError({ code: 1 }) }

    locateMe(state, cfg, chrome, { geolocation, navigate: vi.fn() })

    expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateDenied)
  })

  it('shows the generic failure message for any other geolocation error', () => {
    const state = { areas }
    const chrome = fakeChrome()
    const geolocation = { getCurrentPosition: (_s, onError) => onError({ code: 2 }) }

    locateMe(state, cfg, chrome, { geolocation, navigate: vi.fn() })

    expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateFailed)
  })

  it('shows the generic failure message when the browser has no geolocation API at all', () => {
    const state = { areas }
    const chrome = fakeChrome()

    locateMe(state, cfg, chrome, { geolocation: null, navigate: vi.fn() })

    expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateFailed)
  })
})

// This phase's recurring defect (Tasks 9 and 10, both Important review
// findings) is code that is PRESENT but INERT: a function is written,
// unit-tested in isolation, and never actually wired to the DOM event that is
// supposed to trigger it. locateMe's own tests above call it directly; this
// test instead mounts a real map island and dispatches a real click on
// chrome.locateButton, so a mutation that drops the addEventListener call in
// mount() (or points it at the wrong element) fails here even though every
// locateMe test above still passes.
describe('mount() wires the locate button to a real click', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { clearCache(); resetViewStateForTests() })

  function stubOkFetch() {
    return vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))
  }

  it('reaches locateMe, surfaced through the real hint banner', async () => {
    vi.stubGlobal('fetch', stubOkFetch())
    history.replaceState(null, '', '/#metric=P2')

    const el = document.createElement('div')
    el.dataset.metric = 'P2'
    el.dataset.metrics = 'P1,P2'
    el.dataset.zoom = '7'
    el.dataset.lon = '25.4858'
    el.dataset.lat = '42.7339'
    el.dataset.noDataColour = '#9ca3af'
    el.dataset.unscaledColour = '#94a3b8'
    el.dataset.markerStrokeColour = '#ffffff'
    el.dataset.emptyBasemapColour = '#eef2f5'
    el.dataset.zoomCity = '9'
    el.dataset.zoomSensor = '11'
    // Distinct from every other cfg.t string in this test file, so a false
    // pass from some OTHER hint text (e.g. the tier hint) is not possible.
    el.dataset.tLocateDenied = 'LOCATE DENIED MARKER'

    const { map, chrome } = mount(el)
    map.handlers.load()
    await vi.waitFor(() => expect(map.setPaintProperty).toHaveBeenCalled())

    const originalDescriptor = Object.getOwnPropertyDescriptor(navigator, 'geolocation')
    Object.defineProperty(navigator, 'geolocation', {
      configurable: true,
      value: { getCurrentPosition: (_onSuccess, onError) => onError({ code: 1 }) },
    })

    try {
      chrome.locateButton.click()
    } finally {
      if (originalDescriptor) Object.defineProperty(navigator, 'geolocation', originalDescriptor)
      else delete navigator.geolocation
    }

    const hint = el.querySelector('.map-hint')
    expect(hint.textContent).toBe('LOCATE DENIED MARKER')
    expect(hint.hidden).toBe(false)
  })
})
