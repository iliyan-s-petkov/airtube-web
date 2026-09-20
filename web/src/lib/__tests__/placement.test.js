import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  locateVisitor, openDeepLinkedSensor, prefetchPlacement, locateMe, DEEP_LINK_ZOOM,
} from '../placement.js'
import { clearCache } from '../api.js'
import { resetViewStateForTests } from '../viewstate.svelte.js'
import { setSensors } from '../sensors.svelte.js'
import { getMapAreas, setMapAreas } from '../mapareas.svelte.js'
import { POINT_TIER_MIN_ZOOM } from '../hexes.js'

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
    return { showHint: vi.fn(), showError: vi.fn(), showNote: vi.fn(), showLegend: vi.fn() }
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

// A URL like /en/#sensor=11338 carries the sensor id in a fragment the server
// never sees, so a page opened on it drew the whole country and left the reader
// to find the sensor themselves. openDeepLinkedSensor is what turns that URL
// into the view it promises.
describe('openDeepLinkedSensor', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests(); setSensors(null); setMapAreas(null) })
  afterEach(() => { resetViewStateForTests(); setSensors(null); setMapAreas(null) })

  function fakeMap() {
    return {
      jumpTo: vi.fn(),
      getZoom: vi.fn(() => 7),
      getSource: vi.fn(() => ({ setData: vi.fn() })),
    }
  }

  const chrome = () => ({ showHint: vi.fn(), showError: vi.fn(), showNote: vi.fn(), showLegend: vi.fn() })
  const cfg = {
    lon: 25.4858, lat: 42.7339, zoom: 7,
    zoomCity: 9, zoomSensor: 11, metric: 'P2', noDataColour: '#9ca3af',
    t: { hint: 'h', unavailable: 'u' },
  }
  // The forced refresh openDeepLinkedSensor ends with goes through the real
  // getJSON; stubbed so it resolves quietly instead of logging a failure.
  const stubFetch = () => vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))

  function viewState(sensorId) {
    return { get sensorId() { return sensorId } }
  }

  it('flies to the sensor at the sensor tier and adopts its area', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null }
    const fetchJSON = vi.fn().mockResolvedValue({ id: 11338, lon: 23.31, lat: 42.69, slug: 'sofia' })

    const moved = await openDeepLinkedSensor(map, state, cfg, chrome(), viewState(11338), fetchJSON)

    expect(fetchJSON).toHaveBeenCalledWith('/api/v1/sensor/11338/locate')
    // Past the point tier, not at cfg.zoomSensor: the link promises one
    // sensor, and below the point tier the map draws bins holding several.
    expect(map.jumpTo).toHaveBeenCalledWith({ center: [23.31, 42.69], zoom: DEEP_LINK_ZOOM })
    expect(DEEP_LINK_ZOOM).toBeGreaterThan(POINT_TIER_MIN_ZOOM)
    expect(state.slug).toBe('sofia')
    expect(moved).toBe(true)
  })

  // Nothing to resolve: no deep link, or the sensor is already in the payload
  // the map holds, in which case the panel opens without a request.
  it('asks for nothing when there is no deep link, or the sensor is already loaded', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const fetchJSON = vi.fn()

    expect(await openDeepLinkedSensor(fakeMap(), { slug: null }, cfg, chrome(), viewState(null), fetchJSON)).toBe(false)

    setSensors({ sensors: { id: [11338], lon: [23.31], lat: [42.69], quality: ['ok'], type: ['SDS011'], P2: [12] } })
    expect(await openDeepLinkedSensor(fakeMap(), { slug: null }, cfg, chrome(), viewState(11338), fetchJSON)).toBe(false)

    expect(fetchJSON).not.toHaveBeenCalled()
  })

  // The prefetch exists to overlap the placement with the scales, so what it
  // asks for has to be the URL the placement itself will ask for — a divergence
  // here is not an error anywhere, just a second request and the old delay.
  describe('prefetchPlacement', () => {
    it('asks for the deep-linked sensor, and otherwise for the visitor', () => {
      const fetchJSON = vi.fn().mockResolvedValue(null)

      prefetchPlacement(viewState(11338), cfg, fetchJSON)
      expect(fetchJSON).toHaveBeenCalledWith('/api/v1/sensor/11338/locate')

      fetchJSON.mockClear()
      prefetchPlacement(viewState(null), cfg, fetchJSON)
      expect(fetchJSON).toHaveBeenCalledWith('/api/v1/locate')
    })

    it('asks for nothing on an area page, which opens at its own centre', () => {
      const fetchJSON = vi.fn().mockResolvedValue(null)

      prefetchPlacement(viewState(null), { ...cfg, slug: 'sofia' }, fetchJSON)

      expect(fetchJSON).not.toHaveBeenCalled()
    })

    // mount's own order: a sensor the map already holds needs no lookup, and
    // the deep link then places nothing, so the visitor lookup is what runs.
    it('asks for the visitor when the map already holds the sensor', () => {
      const fetchJSON = vi.fn().mockResolvedValue(null)
      setSensors({ sensors: { id: [11338], lon: [23.31], lat: [42.69], quality: ['ok'], type: ['SDS011'], P2: [12] } })

      prefetchPlacement(viewState(11338), cfg, fetchJSON)

      expect(fetchJSON).toHaveBeenCalledWith('/api/v1/locate')
    })

    // An unawaited rejection is an unhandled rejection whatever the placement
    // later does with its own copy of the promise.
    it('swallows a failed lookup', async () => {
      prefetchPlacement(viewState(11338), cfg, vi.fn().mockRejectedValue(new Error('429')))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  })

  // A refused or failed lookup — the enumeration limiter answers 429 — must
  // leave the map exactly where the server put it, not throw out of mount().
  it('stays put when the lookup fails or answers nothing usable', async () => {
    vi.stubGlobal('fetch', stubFetch())
    for (const body of [Promise.reject(new Error('429')), Promise.resolve(null), Promise.resolve({ id: 11338, slug: 'sofia' })]) {
      const map = fakeMap()
      const state = { slug: null, tier: null, scales: null }
      const moved = await openDeepLinkedSensor(map, state, cfg, chrome(), viewState(11338), vi.fn(() => body))
      expect(moved).toBe(false)
      expect(map.jumpTo).not.toHaveBeenCalled()
      expect(state.slug).toBeNull()
    }
  })

  // A cell click is already looking at the sensor, so resolving it must not
  // move the map out from under the reader — only load it.
  it('adopts the area without moving the map when asked not to', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null }
    const fetchJSON = vi.fn().mockResolvedValue({ id: 11338, lon: 23.31, lat: 42.69, slug: 'sofia' })

    const moved = await openDeepLinkedSensor(map, state, cfg, chrome(), viewState(11338), fetchJSON, { move: false })

    expect(map.jumpTo).not.toHaveBeenCalled()
    expect(state.slug).toBe('sofia')
    expect(moved).toBe(true)
  })

  // /locate answers with the finest area holding the sensor, which the loaded
  // tier need not list. Taking it would rank the sensor against a set a click
  // on the same sensor never uses, and one the loaded tier cannot name.
  // The nearest area by centre is not the area a sensor stands in. Taking it
  // would rank the sensor among neighbours it has none of — the strip loses its
  // "this sensor, Nth of M" card, because the sensor is not in the M.
  it('keeps the area the locate call names over a nearer centre', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const state = {
      slug: null, tier: null, scales: null,
      // krasna-polyana's centre is the nearer one; ovcha-kupel is where the
      // sensor stands. This is sensor 11338's real arrangement in Sofia.
      areas: [{ slug: 'krasna-polyana', lon: 23.25, lat: 42.69 }, { slug: 'ovcha-kupel', lon: 23.2, lat: 42.65 }],
    }
    const fetchJSON = vi.fn().mockResolvedValue({ id: 11338, lon: 23.252, lat: 42.684, slug: 'ovcha-kupel' })

    await openDeepLinkedSensor(fakeMap(), state, cfg, chrome(), viewState(11338), fetchJSON, { move: false })

    expect(state.slug).toBe('ovcha-kupel')
    // The list is already loaded; nothing to fetch beyond the locate call.
    expect(fetchJSON).toHaveBeenCalledTimes(1)
  })

  // A reload resolves the deep link before the first refresh, so the list has
  // to be fetched here or the adopted slug has no name and the strip can only
  // count the sensors around this one.
  it('loads the area list when the map has none yet', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const areas = [{ slug: 'ovcha-kupel', lon: 23.24, lat: 42.67, name_bg: 'Овча купел' }]
    const state = { slug: null, tier: null, scales: null, areas: [] }
    const fetchJSON = vi.fn(async (url) => (
      url === '/api/v1/overview?tier=city'
        ? { areas }
        : { id: 11338, lon: 23.252, lat: 42.684, slug: 'ovcha-kupel' }
    ))

    // paint: false, as the reload path calls it — the caller paints once after
    // the camera settles, and that pass would overwrite the list published here.
    await openDeepLinkedSensor(fakeMap(), state, cfg, chrome(), viewState(11338), fetchJSON, { paint: false })

    expect(fetchJSON).toHaveBeenCalledWith('/api/v1/overview?tier=city')
    expect(state.slug).toBe('ovcha-kupel')
    expect(getMapAreas()).toEqual(areas)
  })

  // A sensor no area page owns still has a position, and the nearest area is
  // the only set left to compare it against.
  it('falls back to the nearest area when the sensor is in none', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const state = { slug: null, tier: null, scales: null, areas: [{ slug: 'vidin', lon: 22.87, lat: 43.99 }] }
    const fetchJSON = vi.fn().mockResolvedValue({ id: 7, lon: 22.9, lat: 44, slug: '' })

    await openDeepLinkedSensor(fakeMap(), state, cfg, chrome(), viewState(7), fetchJSON, { move: false })

    expect(state.slug).toBe('vidin')
  })

  // A sensor the snapshot knows but no area page owns: the position is still
  // worth flying to, and adopting a slug no endpoint serves would be worse.
  it('flies to a sensor with no area without adopting an empty slug', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null }
    const fetchJSON = vi.fn().mockResolvedValue({ id: 7, lon: 25, lat: 43, slug: '' })

    expect(await openDeepLinkedSensor(map, state, cfg, chrome(), viewState(7), fetchJSON)).toBe(true)
    expect(map.jumpTo).toHaveBeenCalledWith({ center: [25, 43], zoom: DEEP_LINK_ZOOM })
    expect(state.slug).toBeNull()
  })
})

// locateMe zooms THIS map to the sensor nearest the fix; it no longer
// navigates to the area page, and never sends the coordinate anywhere.
describe('locateMe', () => {
  beforeEach(() => { clearCache(); setSensors(null) })
  afterEach(() => { clearCache(); setSensors(null) })

  const cfg = {
    lon: 25.4858, lat: 42.7339, zoom: 7,
    zoomCity: 9, zoomSensor: 11, metric: 'P2', noDataColour: '#9ca3af',
    t: {
      locateDenied: 'Location access was denied.',
      locateFailed: 'We could not determine your location.',
      hint: 'h', unavailable: 'u',
    },
  }

  function fakeChrome() {
    return { showHint: vi.fn(), showError: vi.fn(), showNote: vi.fn(), showLegend: vi.fn() }
  }

  // getZoom follows the last jumpTo, so refresh() sees the tier it lands in.
  function fakeMap() {
    let zoom = 7
    return {
      jumpTo: vi.fn(({ zoom: z }) => { zoom = z }),
      getZoom: () => zoom,
      getBounds: () => ({ getWest: () => 23.2, getSouth: () => 42.6, getEast: () => 23.4, getNorth: () => 42.8 }),
      getSource: vi.fn(() => ({ setData: vi.fn() })),
    }
  }

  const areas = [{ slug: 'sofia', lon: 23.32, lat: 42.7, zoom: 11 }]
  // Two sensors either side of the fix, so "nearest" is a real choice.
  const sensorBody = {
    generated_at: '2026-09-07T00:00:00Z',
    sensors: { id: [11338, 22], lon: [23.30, 23.39], lat: [42.70, 42.70], quality: ['ok', 'ok'], type: ['SDS011', 'SDS011'], P2: [5, 6] },
  }

  function stubFetch(sensors = sensorBody) {
    return vi.fn(async (url) => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => (String(url).includes('/sensors') ? sensors : { areas: [] }),
    }))
  }

  const fixAt = (lon, lat) => ({ getCurrentPosition: (onSuccess) => onSuccess({ coords: { longitude: lon, latitude: lat } }) })

  it('zooms this map to the nearest sensor and never leaves the page', async () => {
    vi.stubGlobal('fetch', stubFetch())
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null, areas, hexUrl: null, hexBody: null }

    await locateMe(map, state, cfg, fakeChrome(), { geolocation: fixAt(23.31, 42.70) })

    // The sensor list is per area, so the area has to be adopted first.
    expect(state.slug).toBe('sofia')
    // First the fix, then the sensor closest to it: 11338 at 23.30, not 22.
    expect(map.jumpTo).toHaveBeenNthCalledWith(1, { center: [23.31, 42.70], zoom: DEEP_LINK_ZOOM })
    expect(map.jumpTo).toHaveBeenNthCalledWith(2, { center: [23.30, 42.70], zoom: DEEP_LINK_ZOOM })
    expect(DEEP_LINK_ZOOM).toBeGreaterThan(POINT_TIER_MIN_ZOOM)
  })

  // Only the resolved slug may become a request: a coordinate in a URL would
  // be the point query the API is built not to answer.
  it('sends no coordinate to the server', async () => {
    const fetchSpy = stubFetch()
    vi.stubGlobal('fetch', fetchSpy)
    const state = { slug: null, tier: null, scales: null, areas, hexUrl: null, hexBody: null }

    await locateMe(fakeMap(), state, cfg, fakeChrome(), { geolocation: fixAt(23.31, 42.70) })

    for (const [url] of fetchSpy.mock.calls) {
      expect(String(url)).not.toContain('42.70')
      expect(String(url)).not.toContain('23.31')
    }
  })

  // No sensor to centre on: the fix itself is still the best view.
  it('stays over the fix when the area has no sensor to centre on', async () => {
    vi.stubGlobal('fetch', stubFetch({ sensors: { id: [], lon: [], lat: [] } }))
    const map = fakeMap()
    const state = { slug: null, tier: null, scales: null, areas, hexUrl: null, hexBody: null }

    await locateMe(map, state, cfg, fakeChrome(), { geolocation: fixAt(23.31, 42.70) })

    expect(map.jumpTo).toHaveBeenCalledTimes(1)
    expect(map.jumpTo).toHaveBeenCalledWith({ center: [23.31, 42.70], zoom: DEEP_LINK_ZOOM })
  })

  // null (never loaded) and [] (zero areas) both mean "we don't know", not
  // "outside coverage" — nearestArea has no distance cutoff.
  it('shows the "could not determine" message when the area list is unknown', async () => {
    vi.stubGlobal('fetch', stubFetch())

    for (const unknownAreas of [null, []]) {
      const map = fakeMap()
      const chrome = fakeChrome()

      await locateMe(map, { areas: unknownAreas }, cfg, chrome, { geolocation: fixAt(23.3, 42.7) })

      expect(map.jumpTo).not.toHaveBeenCalled()
      expect(chrome.showHint).toHaveBeenCalledTimes(1)
      expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateFailed)
    }
  })

  // PERMISSION_DENIED === 1 per the Geolocation API spec.
  it('shows the denied message for a PERMISSION_DENIED error', async () => {
    const chrome = fakeChrome()
    const geolocation = { getCurrentPosition: (_s, onError) => onError({ code: 1 }) }

    await locateMe(fakeMap(), { areas }, cfg, chrome, { geolocation })

    expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateDenied)
  })

  it('shows the generic failure message for any other geolocation error', async () => {
    const chrome = fakeChrome()
    const geolocation = { getCurrentPosition: (_s, onError) => onError({ code: 2 }) }

    await locateMe(fakeMap(), { areas }, cfg, chrome, { geolocation })

    expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateFailed)
  })

  it('shows the generic failure message when the browser has no geolocation API at all', async () => {
    const chrome = fakeChrome()

    await locateMe(fakeMap(), { areas }, cfg, chrome, { geolocation: null })

    expect(chrome.showHint).toHaveBeenCalledWith(cfg.t.locateFailed)
  })
})
