// @vitest-environment jsdom
//
// jsdom for 'the opening render' below, which drives initData/placeVisitor
// through fake objects that need no real DOM but tolerate one; every other
// describe block here is pure-logic and does not mind it either.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  debounce, hintController, loadScales, initData, refreshHexes, showArea, mapHint,
  setSourceViewAvailability, metricNote, cellTier, urlFor,
} from '../mapdata.js'
import { placeVisitor } from '../placement.js'
import { clearCache } from '../api.js'
import { resetViewStateForTests } from '../viewstate.svelte.js'
import { setSourceEnabled, resetSourceFilterForTests } from '../sourcefilter.svelte.js'
import { POINT_TIER_MIN_ZOOM } from '../hexes.js'
import { HEX_SOURCE_ID } from '../mapids.js'

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
      showLegend: () => {},
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
    t: { hint: 'Select an area', noSources: 'No networks are shown', unavailable: 'Map data is unavailable right now' },
  }

  // Zoom 7 is the index page's server-rendered default, where tierFor gives
  // 'country' and refresh serves it as-is — so refresh takes the showHint('')
  // path. That is the production scenario, not a contrived one.
  function fakeMap(zoom = 7) {
    const painted = []
    return {
      painted,
      getZoom: () => zoom,
      // Only the marker source is recorded: the hex layer draws over the same
      // map from its own source, and counting its setData here would make this
      // test about how many layers exist rather than about marker colour.
      getSource: (id) => (id === 'airbg-data' ? { setData: (data) => painted.push(data) } : undefined),
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

  beforeEach(() => { clearCache(); resetSourceFilterForTests() })

  it('still explains the grey map after refresh has run', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: false }))
    const rendered = []
    const chrome = { ...hintController((t) => rendered.push(t)), showLegend: () => {} }
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

  // The grid used to be painted after initData returned, one request later than
  // the markers: on a slow link that is a second draw of the same screen.
  it('paints the layer given alongside in the markers own pass', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: true }))
    const chrome = { ...hintController(() => {}), showLegend: () => {} }
    const map = fakeMap()
    const order = []
    let paintedWhenStarted = null
    const alongside = vi.fn(async () => {
      // Zero: sequenced after the markers, this would be one, and the reader
      // would see the grid arrive on a screen that already had dots on it.
      paintedWhenStarted = map.painted.length
      order.push('alongside started')
      await new Promise((resolve) => setTimeout(resolve, 5))
      order.push('alongside painted')
    })

    await initData(map, { slug: null, tier: null, scales: null }, cfg, chrome, null, alongside)
    order.push('initData resolved')

    // Started before the markers land, so the two arrive together; and awaited,
    // because initData resolving is what mount treats as the first screen being
    // complete.
    expect(paintedWhenStarted).toBe(0)
    expect(map.painted).toHaveLength(1)
    expect(order).toEqual(['alongside started', 'alongside painted', 'initData resolved'])
  })

  it('leaves the banner empty when everything loads', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: true }))
    const rendered = []
    const chrome = { ...hintController((t) => rendered.push(t)), showLegend: () => {} }
    const map = fakeMap()

    await initData(map, { slug: null, tier: null, scales: null }, cfg, chrome)

    expect(rendered.at(-1)).toBe('')
    expect(map.painted[0].features[0].properties.colour).toBe('#00ff00')
  })

  // The wiring, not the rule: refresh has to consult the source filter at all.
  // mapHint's own tests would pass with the call site still showing ''.
  it('explains the blank map when the reader has unticked both networks', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: true }))
    setSourceEnabled('sensor.community', false)
    setSourceEnabled('eea', false)
    const rendered = []
    const chrome = { ...hintController((t) => rendered.push(t)), showLegend: () => {} }
    const map = fakeMap()

    await initData(map, { slug: null, tier: null, scales: null }, cfg, chrome)

    expect(rendered.at(-1)).toBe(cfg.t.noSources)
  })

  // The bug: the country tier used to disable both boxes, so the toggle on the
  // opening map was inert. They stay live at every tier now.
  it('leaves the network checkboxes live at the country tier', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: true }))
    const fieldset = document.createElement('fieldset')
    const boxes = ['communitySensors', 'officialStations'].map((id) => {
      const label = document.createElement('label')
      const input = document.createElement('input')
      input.type = 'checkbox'
      input.dataset.layerKey = `view:${id}`
      label.append(input, document.createElement('span'))
      fieldset.appendChild(label)
      return input
    })
    const chrome = { ...hintController(() => {}), showLegend: () => {}, layersUI: { fieldset } }

    await initData(fakeMap(7), { slug: null, tier: null, scales: null }, cfg, chrome)

    expect(boxes.map((b) => b.disabled)).toEqual([false, false])
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
    const chrome = { ...hintController(() => {}), showLegend: () => {} }
    const map = fakeMap(7) // country under the old 9/11; city under 3/20

    await initData(map, { slug: null, tier: null, scales: null }, offThresholdCfg, chrome)

    const aggregateURL = requestedURLs.find((u) => u !== '/api/v1/scales')
    expect(aggregateURL).toBe('/api/v1/overview?tier=city')
    expect(aggregateURL).not.toBe('/api/v1/overview')
  })

  // The averaging window is state, not config: it has to reach the request, or
  // the selector changes the label above a map that keeps showing this minute's
  // reading. The live default must add nothing — that URL is what every cache,
  // client and server side, is already keyed on.
  it('carries the chosen window on the aggregate request, and adds nothing for live', async () => {
    const requestedURLs = []
    const record = vi.fn(async (url) => {
      requestedURLs.push(url)
      if (url === '/api/v1/scales') {
        return { ok: true, status: 200, headers: new Headers(), json: async () => [] }
      }
      return { ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }
    })
    vi.stubGlobal('fetch', record)
    const chrome = { ...hintController(() => {}), showLegend: () => {} }

    await initData(fakeMap(7), { slug: null, tier: null, scales: null, window: '' }, cfg, chrome)
    expect(requestedURLs.at(-1)).toBe('/api/v1/overview')

    clearCache()
    await initData(fakeMap(7), { slug: null, tier: null, scales: null, window: '7d' }, cfg, chrome)
    expect(requestedURLs.at(-1)).toBe('/api/v1/overview?window=7d')
  })
})

// The grid is the same readings under the same markers: a window that moved one
// and not the other would put two answers to one question on one map. Its dedup
// is by URL, which is also what lets a window change through.
describe('refreshHexes under a window', () => {
  const cfg = { metric: 'P2', noDataColour: '#9ca3af', unscaledColour: '#94a3b8' }
  const map = {
    getZoom: () => 12,
    getBounds: () => ({ getWest: () => 23, getSouth: () => 42, getEast: () => 24, getNorth: () => 43 }),
    getSource: () => ({ setData: () => {} }),
    setPaintProperty: () => {},
    getLayer: () => ({}),
  }
  const empty = async () => ({ type: 'FeatureCollection', features: [] })

  it('asks for the window it is showing', async () => {
    const state = { scales: null, hexUrl: null, window: '48h' }
    await refreshHexes(map, state, cfg, empty)
    expect(state.hexUrl).toContain('window=48h')
  })

  it('asks for no window at all when showing live', async () => {
    const state = { scales: null, hexUrl: null, window: '' }
    await refreshHexes(map, state, cfg, empty)
    expect(state.hexUrl).not.toContain('window=')
  })

  it('refetches when only the window changed', async () => {
    const asked = []
    const fetchJSON = async (url) => { asked.push(url); return { type: 'FeatureCollection', features: [] } }
    const state = { scales: null, hexUrl: null, window: '' }
    await refreshHexes(map, state, cfg, fetchJSON)
    await refreshHexes(map, state, cfg, fetchJSON)
    expect(asked, 'the same viewport and window must not refetch').toHaveLength(1)
    state.window = '24h'
    await refreshHexes(map, state, cfg, fetchJSON)
    expect(asked).toHaveLength(2)
  })
})

describe('unscaled metrics', () => {
  const scales = [{ metric: 'P2', bands: [{ upper: 5, colour: '#50f0e6' }] }]

  it('explains an unscaled metric and says nothing for a scaled one', () => {
    expect(metricNote(scales, 'temperature', 'no scale')).toBe('no scale')
    expect(metricNote(scales, 'P2', 'no scale')).toBe('')
  })
})

// A cold load used to draw the country, then draw it again for the metric it
// was already showing, then jump to the visitor's city and draw a third time,
// then draw a fourth on the moveend that jump fired — visible as the map
// redrawing itself from the centre outwards for a second or two after every
// refresh. The camera is settled BEFORE the first paint now, and these are the
// seams that hold that order.
describe('the opening render', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  const cfg = {
    lon: 25.4858, lat: 42.7339, zoom: 7,
    zoomCity: 9, zoomSensor: 11, metric: 'P2', noDataColour: '#9ca3af',
    t: { hint: 'h', unavailable: 'u' },
  }

  function paintingMap(zoom = 7) {
    const painted = []
    return {
      painted,
      jumpTo: vi.fn(),
      getZoom: () => zoom,
      getSource: (id) => (id === 'airbg-data' ? { setData: (d) => painted.push(d) } : undefined),
    }
  }

  const chrome = () => ({ showHint: vi.fn(), showError: vi.fn(), showNote: vi.fn(), showLegend: vi.fn() })

  const areaFetch = () => vi.fn(async () => ({
    ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }),
  }))

  const GEOIP = { source: 'geoip', slug: 'sofia', lon: 23.32, lat: 42.7, zoom: 11 }

  // The whole point of the seam: it moves the camera and adopts the slug, and
  // draws nothing. A paint here would be the paint the first refresh is about
  // to do anyway, at a camera position that is one line older.
  it('places the visitor without painting', async () => {
    vi.stubGlobal('fetch', areaFetch())
    const map = paintingMap()
    const state = { slug: null, tier: null, scales: null }

    expect(await placeVisitor(map, state, cfg, vi.fn().mockResolvedValue(GEOIP))).toBe(true)
    expect(map.jumpTo).toHaveBeenCalledWith({ center: [23.32, 42.7], zoom: 11 })
    expect(state.slug).toBe('sofia')
    expect(map.painted).toHaveLength(0)
  })

  // A slow lookup must not hold the map back — an empty frame while a geoip
  // call hangs is worse than the national view the server already rendered for.
  it('gives up on a lookup that outruns the timeout, leaving the camera alone', async () => {
    vi.stubGlobal('fetch', areaFetch())
    const map = paintingMap()
    const state = { slug: null, tier: null, scales: null }
    const slow = vi.fn(() => new Promise((resolve) => setTimeout(() => resolve(GEOIP), 50)))

    expect(await placeVisitor(map, state, cfg, slow, { timeoutMs: 5 })).toBe(false)
    expect(map.jumpTo).not.toHaveBeenCalled()
    expect(state.slug).toBeNull()
  })

  // The camera step runs between the scales and the first refresh, so that
  // refresh is the first and only paint — and it is the tier the settled camera
  // asks for, not the one the default view would have.
  it('paints once, after the camera has been placed', async () => {
    vi.stubGlobal('fetch', areaFetch())
    const map = paintingMap()
    const state = { slug: null, tier: null, scales: null }
    const order = []
    map.jumpTo = vi.fn(() => order.push('jump'))
    const painting = { getSource: map.getSource }
    map.getSource = (id) => {
      const src = painting.getSource(id)
      return src && { setData: (d) => { order.push('paint'); src.setData(d) } }
    }

    await initData(map, state, cfg, chrome(), () => placeVisitor(map, state, cfg, vi.fn().mockResolvedValue(GEOIP)))

    expect(order).toEqual(['jump', 'paint'])
  })

  // The moveend the placement's own jumpTo queues would repaint everything a
  // quarter-second after the map settled — the last of the redraws, and the one
  // that arrives late enough to look like a glitch rather than a load.
  it('cancels a pending debounced call', async () => {
    vi.useFakeTimers()
    try {
      const fn = vi.fn()
      const debounced = debounce(fn, 250)
      debounced()
      debounced.cancel()
      vi.advanceTimersByTime(1000)
      expect(fn).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })
})

// refreshHexes is the ONE layer that follows the viewport. These tests fix the
// three things that makes it different from refresh(): it dedupes on the URL,
// it repaints from the retained body when only the metric changed, and a failed
// fetch leaves the rest of the map alone rather than surfacing chrome.
describe('refreshHexes', () => {
  const hexCfg = { metric: 'P2', noDataColour: '#cccccc' }
  const scales = [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }, { upper: null, colour: '#ff0000' }] }]

  function hexMap(zoom = 12) {
    const painted = []
    return {
      painted,
      getZoom: () => zoom,
      getBounds: () => ({ getWest: () => 23.3, getSouth: () => 42.6, getEast: () => 23.4, getNorth: () => 42.7 }),
      getSource: (id) => (id === 'airbg-hexes' ? { setData: (d) => painted.push(d) } : undefined),
    }
  }

  const body = { resolution_km: 1, hexes: [{ lon: 23.32, lat: 42.65, n: 4, values: { P2: 5 } }] }

  it('fetches once for a view and repaints from memory on the next pass', async () => {
    const map = hexMap()
    const state = { scales, hexUrl: null, hexBody: null }
    const fetchJSON = vi.fn(async () => body)

    await refreshHexes(map, state, hexCfg, fetchJSON)
    await refreshHexes(map, state, hexCfg, fetchJSON)

    // One request, two paints: the second pass is what a metric switch does.
    expect(fetchJSON).toHaveBeenCalledTimes(1)
    expect(map.painted).toHaveLength(2)
    expect(map.painted[1].features[0].properties.colour).toBe('#00ff00')
  })

  // The bug this fixes: past the finest published cell the grid used to be
  // drawn as bare marks, which land under the labelled sensor markers already on
  // the map — so one zoom step took a street full of hexagons to an apparently
  // empty one. The cells stay, sized from the zoom.
  it('draws the point tier as cells, sized from the zoom', async () => {
    const points = { resolution_km: 0, hexes: [{ lon: 23.36, lat: 42.66, sensor_id: 7, n: 1, values: { P2: 5 } }] }
    const near = hexMap(16)
    const far = hexMap(18)
    const fetchJSON = vi.fn(async () => points)

    await refreshHexes(near, { scales, hexUrl: null, hexBody: null }, hexCfg, fetchJSON)
    await refreshHexes(far, { scales, hexUrl: null, hexBody: null }, hexCfg, fetchJSON)

    const span = (map) => {
      const xs = map.painted[0].features[0].geometry.coordinates[0].map((c) => c[0])
      return Math.max(...xs) - Math.min(...xs)
    }
    expect(near.painted[0].features[0].geometry.type).toBe('Polygon')
    // Deeper zoom, smaller cell on the ground — the same size on screen.
    expect(span(far)).toBeLessThan(span(near))
  })

  it('refetches when the viewport moves to a different URL', async () => {
    const state = { scales, hexUrl: null, hexBody: null }
    const fetchJSON = vi.fn(async () => body)

    await refreshHexes(hexMap(12), state, hexCfg, fetchJSON)
    await refreshHexes(hexMap(15), state, hexCfg, fetchJSON)

    expect(fetchJSON).toHaveBeenCalledTimes(2)
    expect(fetchJSON.mock.calls[0][0]).not.toBe(fetchJSON.mock.calls[1][0])
  })

  it('recolours the same bins when only the metric changed', async () => {
    const map = hexMap()
    const state = { scales, hexUrl: null, hexBody: null }
    const twoMetrics = { resolution_km: 1, hexes: [{ lon: 23.32, lat: 42.65, n: 4, values: { P2: 5, P1: 40 } }] }
    const fetchJSON = vi.fn(async () => twoMetrics)

    await refreshHexes(map, state, hexCfg, fetchJSON)
    await refreshHexes(map, state, { ...hexCfg, metric: 'P1' }, fetchJSON)

    expect(fetchJSON).toHaveBeenCalledTimes(1)
    // P1 has no band table here, so it takes the no-data colour — the same rule
    // the markers follow for an unscaled metric.
    expect(map.painted[1].features[0].properties.colour).toBe('#cccccc')
    expect(map.painted[1].features[0].properties.value).toBe(40)
  })

  // A pan superseded by another pan is answering a viewport the reader has
  // already left. It cancels itself rather than finishing and being discarded.
  it('aborts the previous pan when a new one starts', async () => {
    const state = { scales, hexUrl: null, hexBody: null }
    const fetchJSON = vi.fn(async () => body)

    await refreshHexes(hexMap(12), state, hexCfg, fetchJSON)
    const first = fetchJSON.mock.calls[0][1].signal
    expect(first.aborted).toBe(false)

    await refreshHexes(hexMap(15), state, hexCfg, fetchJSON)

    expect(first.aborted).toBe(true)
    // The near miss: the pan in flight is not aborted by its own start.
    expect(fetchJSON.mock.calls[1][1].signal.aborted).toBe(false)
  })

  // An abort is the caller's own doing, not a failure: no console noise, and
  // no stale-grid hint. The last good grid simply stays on screen.
  it('stays quiet and paints nothing when its fetch is aborted', async () => {
    const map = hexMap()
    const state = { scales, hexUrl: null, hexBody: null }
    const fetchJSON = vi.fn(async () => { throw new DOMException('aborted', 'AbortError') })
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})

    await refreshHexes(map, state, hexCfg, fetchJSON)

    expect(map.painted).toHaveLength(0)
    expect(err).not.toHaveBeenCalled()
    expect(state.hexUrl).toBe(null)
    err.mockRestore()
  })

  it('leaves the map untouched and caches nothing when the fetch fails', async () => {
    const map = hexMap()
    const state = { scales, hexUrl: null, hexBody: null }
    const fetchJSON = vi.fn(async () => { throw new Error('502') })
    const err = vi.spyOn(console, 'error').mockImplementation(() => {})

    await refreshHexes(map, state, hexCfg, fetchJSON)

    expect(map.painted).toHaveLength(0)
    // Not cached: the next pass over the same view must try again.
    expect(state.hexUrl).toBe(null)
    await refreshHexes(map, state, hexCfg, fetchJSON)
    expect(fetchJSON).toHaveBeenCalledTimes(2)
    err.mockRestore()
  })

  describe('the network toggles and the grid', () => {
    beforeEach(() => { resetSourceFilterForTests() })
    afterEach(() => { resetSourceFilterForTests() })

    const cfg = { metric: 'P2', noDataColour: '#cccccc' }

    function fakeMap(zoom = 12) {
      const sources = {}
      return {
        getZoom: () => zoom,
        getBounds: () => ({ getWest: () => 23.3, getSouth: () => 42.6, getEast: () => 23.4, getNorth: () => 42.7 }),
        getSource: (id) => (sources[id] ??= { setData: vi.fn() }),
      }
    }

    const hexBody = {
      generated_at: '2026-09-10T09:00:00Z',
      resolution_km: 15,
      coverage: { eea: { P2: 4 }, 'sensor.community': { P2: 1180 } },
      hexes: [
        {
          lon: 23.32, lat: 42.69, n: 4, values: { P2: 25 },
          by_source: {
            'sensor.community': { n: 3, values: { P2: 20 } },
            eea: { n: 1, values: { P2: 100 } },
          },
        },
        { lon: 25.0, lat: 43.5, n: 1, source: 'eea', values: { P2: 40 } },
      ],
    }

    it('draws one network its own numbers without fetching again', async () => {
      const map = fakeMap()
      const state = { scales: null, hexUrl: null, hexBody: null }
      const fetchJSON = vi.fn(async () => hexBody)

      await refreshHexes(map, state, cfg, fetchJSON)
      expect(fetchJSON).toHaveBeenCalledTimes(1)

      setSourceEnabled('sensor.community', false)
      await refreshHexes(map, state, cfg, fetchJSON)

      // Still one call: the URL has not moved, so this was a repaint.
      expect(fetchJSON).toHaveBeenCalledTimes(1)
      const drawn = map.getSource(HEX_SOURCE_ID).setData.mock.calls.at(-1)[0]
      expect(drawn.features.map((f) => f.properties.value).sort((a, b) => a - b)).toEqual([40, 100])
    })

    it('holds the coverage block from the body it drew', async () => {
      const map = fakeMap()
      const state = { scales: null, hexUrl: null, hexBody: null }

      await refreshHexes(map, state, cfg, async () => hexBody)

      expect(state.coverage).toEqual({ eea: { P2: 4 }, 'sensor.community': { P2: 1180 } })
    })

    it('empties the grid when every network is off', async () => {
      const map = fakeMap()
      const state = { scales: null, hexUrl: null, hexBody: null }

      await refreshHexes(map, state, cfg, async () => hexBody)
      setSourceEnabled('sensor.community', false)
      setSourceEnabled('eea', false)
      await refreshHexes(map, state, cfg, async () => hexBody)

      const drawn = map.getSource(HEX_SOURCE_ID).setData.mock.calls.at(-1)[0]
      expect(drawn.features).toEqual([])
    })
  })
})

// The finder names an area; this is what the map does about it. Same page, no
// navigation — and the area's own centre and zoom, not a guess.
describe('showArea', () => {
  beforeEach(() => { clearCache() })
  afterEach(() => { clearCache() })

  const cfg = {
    lon: 25.4858, lat: 42.7339, zoom: 7, zoomCity: 9, zoomSensor: 11,
    metric: 'P2', noDataColour: '#9ca3af',
    t: { hint: 'h', unavailable: 'u' },
  }

  function fakeMap() {
    let zoom = 7
    return {
      flyTo: vi.fn(({ zoom: z }) => { zoom = z }),
      getZoom: () => zoom,
      getBounds: () => ({ getWest: () => 23.2, getSouth: () => 42.6, getEast: () => 23.4, getNorth: () => 42.8 }),
      getSource: vi.fn(() => ({ setData: vi.fn() })),
    }
  }

  const chrome = () => ({ showHint: vi.fn(), showError: vi.fn(), showNote: vi.fn(), showLegend: vi.fn() })

  it('flies to the area and selects it', async () => {
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null, areas: [], hexUrl: null, hexBody: null, sensorBody: null }
    globalThis.fetch = vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))

    expect(await showArea(map, state, cfg, chrome(), { slug: 'varna', lon: 27.9, lat: 43.2, zoom: 11 })).toBe(true)
    expect(map.flyTo).toHaveBeenCalledWith({ center: [27.9, 43.2], zoom: 11 })
    expect(state.slug).toBe('varna')
  })

  it('does nothing without an area', async () => {
    const map = fakeMap()
    const state = { slug: 'sofia', tier: null, scales: null, areas: [], hexUrl: null, hexBody: null, sensorBody: null }
    expect(await showArea(map, state, cfg, chrome(), null)).toBe(false)
    expect(await showArea(map, state, cfg, chrome(), { lon: 1, lat: 2, zoom: 9 })).toBe(false)
    expect(map.flyTo).not.toHaveBeenCalled()
    expect(state.slug).toBe('sofia')
  })
})

// Unticking both networks empties the marker source and the map goes blank with
// nothing said about it. mapHint is the rule that answers for that; it is pure,
// so the precedence it encodes can be driven directly.
describe('mapHint', () => {
  const t = { hint: 'Select an area', noSources: 'No networks are shown' }

  it('says nothing when both networks are shown and the tier is served as asked', () => {
    expect(mapHint(t, { fellBack: false, sources: new Set(['sensor.community', 'eea']) })).toBe('')
  })

  it('explains a blank map when no network is shown', () => {
    expect(mapHint(t, { fellBack: false, sources: new Set() })).toBe(t.noSources)
  })

  it('outranks the fallback hint, which describes markers that are not drawn', () => {
    expect(mapHint(t, { fellBack: true, sources: new Set() })).toBe(t.noSources)
  })

  it('keeps the fallback hint while a network is still shown', () => {
    expect(mapHint(t, { fellBack: true, sources: new Set(['eea']) })).toBe(t.hint)
  })
})

describe('setSourceViewAvailability', () => {
  const t = {
    communitySensors: 'Citizen sensors',
    officialStations: 'Official stations',
    notMeasured: 'does not measure this',
  }
  const coverage = {
    'sensor.community': { P1: 1180, P2: 1180 },
    eea: { P1: 27, P2: 4, O3: 20 },
  }

  function menu() {
    const fieldset = document.createElement('fieldset')
    const boxes = {}
    for (const id of ['communitySensors', 'officialStations']) {
      const label = document.createElement('label')
      const input = document.createElement('input')
      input.type = 'checkbox'
      input.dataset.layerKey = `view:${id}`
      // The shape glyph maplayers.addOption puts between the box and the name.
      // It is a <span> too, and it comes first — a helper that leaves it out
      // cannot catch a writer that picks the wrong one.
      const glyph = document.createElement('span')
      glyph.className = 'colmenu__mark colmenu__mark--diamond'
      glyph.setAttribute('aria-hidden', 'true')
      const span = document.createElement('span')
      label.append(input, glyph, span)
      fieldset.appendChild(label)
      boxes[id] = { input, span, glyph }
    }
    return { chrome: { layersUI: { fieldset } }, boxes }
  }

  // The count used to be appended here. It wrapped the option onto three lines
  // and pushed the menu out of shape, and the number it reported is already in
  // the network figure below the map.
  it('leaves the name alone when the network measures the metric', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'P2', t, coverage)

    expect(boxes.communitySensors.span.textContent).toBe('Citizen sensors')
    expect(boxes.officialStations.span.textContent).toBe('Official stations')
    expect(boxes.communitySensors.input.disabled).toBe(false)
    expect(boxes.officialStations.input.disabled).toBe(false)
  })

  it('names the metric a network does not measure, and still lets it be switched off', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'O3', t, coverage)

    expect(boxes.communitySensors.span.textContent).toBe('Citizen sensors — does not measure this')
    expect(boxes.communitySensors.input.disabled).toBe(false)
    expect(boxes.officialStations.span.textContent).toBe('Official stations')
  })

  it('falls back to the bare label before any coverage has arrived', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'P2', t, null)

    expect(boxes.officialStations.span.textContent).toBe('Official stations')
    expect(boxes.officialStations.input.disabled).toBe(false)
  })

  it('drops the not-measured note when the metric changes to one it does measure', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'O3', t, coverage)
    setSourceViewAvailability(chrome, 'P1', t, coverage)

    expect(boxes.communitySensors.span.textContent).toBe('Citizen sensors')
  })

  it('never writes on the shape glyph', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'O3', t, coverage)

    expect(boxes.communitySensors.glyph.textContent).toBe('')
    expect(boxes.officialStations.glyph.textContent).toBe('')
  })
})

// Keyed off the marker tier, the caption told a reader zoomed onto one device
// that every cell was an area average.
describe('cellTier', () => {
  it('says one device per cell only where the grid draws one', () => {
    expect(cellTier(POINT_TIER_MIN_ZOOM, 'city')).toBe('sensors')
    expect(cellTier(POINT_TIER_MIN_ZOOM + 3, 'country')).toBe('sensors')
  })

  it('never claims a bin is a device, whatever the markers are', () => {
    expect(cellTier(POINT_TIER_MIN_ZOOM - 1, 'sensors')).not.toBe('sensors')
    expect(cellTier(7, 'country')).toBe('country')
    expect(cellTier(10, 'city')).toBe('city')
  })
})
