// @vitest-environment jsdom
//
// jsdom, not the default node environment: the repaint test below drives
// mount() through a real container element and a real `location.hash` /
// `hashchange`, which the rest of this file's pure-logic tests do not need
// but do not mind either — jsdom is a superset, not a different behaviour,
// for code that touches no DOM.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mount } from '../map.js'
import { installTimelapse } from '../../lib/timelapse-island.js'
import {
  NOT_OFFICIAL, CARRIED_OPACITY, FRESH_OPACITY, SETTLING_OPACITY, applyMarkerZoomRange,
} from '../../lib/mappaint.js'
import { mountChrome } from '../../lib/chrome.js'
import { HEX_LABEL_LAYER_ID } from '../../lib/mapids.js'
import { PLAY_SPEED_KEY } from '../../lib/mapconfig.js'
import { DEEP_LINK_ZOOM } from '../../lib/placement.js'
import { ARROW_IMAGE_ID, WIND_LAYER_ID, WIND_SOURCE_ID } from '../wind.js'
import { GRID_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM } from '../../lib/hexes.js'
import { clearCache } from '../../lib/api.js'
import { resetViewStateForTests, getViewState } from '../../lib/viewstate.svelte.js'
import { findSensor, setSensors } from '../../lib/sensors.svelte.js'
import { setSensorStatus, resetSensorFilterForTests } from '../../lib/sensorfilter.svelte.js'
import { setSourceEnabled, resetSourceFilterForTests } from '../../lib/sourcefilter.svelte.js'
import * as mapboundaries from '../../lib/mapboundaries.js'

// mount() constructs a REAL MapLibreMap, which needs a working WebGL canvas —
// out of reach under jsdom (see the "no jsdom" rule respected everywhere else
// in this file). Mocked here, ONLY for the mountTestMap-based tests below, so
// mount() can be driven end to end (readConfig -> addLayer -> the metric
// subscription) without a real renderer. Every other describe block in this
// file drives map.js's exported functions directly with plain objects, never
// through mount(), so this mock never applies to them in practice — but
// vi.mock is file-scoped, so it is declared once, here.
// The layers the mounted style reports. Hoisted so the vi.mock factory — which
// runs before this module's own body — can close over it, and mutable so a
// test can mount a map whose style already has layers to sit under.
const fakeStyle = vi.hoisted(() => ({ layers: [] }))

vi.mock('maplibre-gl', () => {
  class FakeMap {
    constructor(options) {
      this.options = options
      this.handlers = {}
      this.setPaintProperty = vi.fn()
      this.setFilter = vi.fn()
      this.addSource = vi.fn()
      this.addLayer = vi.fn()
      // Backed by addLayer's own call log, not a separate list: a real map
      // knows about a layer once addLayer has been called for it, which is
      // exactly what map.js's beforeId guard (map.getLayer?.(id)) checks.
      this.getLayer = vi.fn((id) => (this.addLayer.mock.calls.some((c) => c[0]?.id === id) ? { id } : undefined))
      this.addImage = vi.fn()
      this.setLayoutProperty = vi.fn()
      this.setLayerZoomRange = vi.fn()
      this.getZoom = vi.fn(() => 7)
      // The zoom stack asks the camera for its own limits rather than
      // restating them (see installZoom), so a map that cannot answer is not
      // a map this island can mount.
      this.getMinZoom = vi.fn(() => 5)
      this.getMaxZoom = vi.fn(() => 18)
      this.zoomIn = vi.fn()
      this.zoomOut = vi.fn()
      this.flyTo = vi.fn()
      this.easeTo = vi.fn()
      // Empty by default: a test that cares who was hit sets its own return.
      this.queryRenderedFeatures = vi.fn(() => [])
      // One object per source id, every setData recorded in order.
      this.painted = []
      this.sources = {}
      this.getSource = vi.fn((id) => {
        this.sources[id] ??= { setData: vi.fn(() => this.painted.push(id)) }
        return this.sources[id]
      })
      this.container = document.createElement('div')
      this.getContainer = vi.fn(() => this.container)
      // The layers menu reads its options off the mounted style, so a map that
      // cannot report one is a map this island cannot mount. Empty here: the
      // basemap layers come from tools/basemap/style.json, which no test
      // fetches, and an empty style is the real state of a map served without
      // tiles — the one the menu has to survive.
      this.getStyle = vi.fn(() => ({ layers: fakeStyle.layers }))
      // Spied so the locateVisitor tests below can assert a "geoip" response
      // jumps the map, and that a "default"/rejected response does not.
      this.jumpTo = vi.fn()
      this.clickHandlers = {}
      this.bareClickHandlers = []
    }
    // map.on('click', LAYER_ID, cb) carries the layer id as a second
    // argument; a bare map.on('click', cb) (the boundary handler, and the
    // panel-closing handler added alongside it) does not.
    // Clicks are ALSO kept per layer: the map binds one click handler to the
    // markers and another to the cells, and a single `handlers.click` slot
    // would silently hand every test the last one registered. Bare handlers
    // go on their own list, in registration order, since MapLibre fires every
    // one of them on every click regardless of what else it hit.
    on(event, a, b) {
      if (event !== 'click') { this.handlers[event] = a; return }
      if (b === undefined) { this.bareClickHandlers.push(a); return }
      this.handlers.click ??= b
      this.clickHandlers[a] = b
    }

    off(event, handler) {
      if (this.handlers[event] === handler) delete this.handlers[event]
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
function mountTestMap({ metric, styleLayers = [], dataset = {} }) {
  fakeStyle.layers = styleLayers
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
  el.dataset.markerLabelColour = '#161616'
  el.dataset.emptyBasemapColour = '#eef2f5'
  el.dataset.zoomCity = '9'
  el.dataset.zoomSensor = '11'
  el.dataset.hexOpacity = '0.55'
  el.dataset.tWindToggle = 'Wind'
  el.dataset.tViewCellValues = 'Cell values'
  Object.assign(el.dataset, dataset)
  // In the document, in a wrapper of its own: mountChrome puts the tier caption
  // AFTER the map's element, which a detached node has nowhere to put.
  const wrapper = document.createElement('div')
  wrapper.appendChild(el)
  document.body.appendChild(wrapper)

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
  return { map, chrome, el }
}

// The no-data colour is configuration now (arrives as a data-* attribute), not
// a module constant — restated here as a literal because these tests are about
// feature-mapping logic, not about the specific grey.
const NO_DATA_COLOUR = '#9ca3af'


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
// The camera's floor has to be a floor the camera can actually stand on.
// MapLibre's default minZoom of 0 is not: Transform._constrain stops getZoom()
// somewhere above it so the world keeps covering the container, and the zoom
// stack — which compares getZoom() against getMinZoom() — then leaves the minus
// button live over a camera that has stopped moving. Shipped exactly that once.
describe('mount() gives the camera a reachable floor', () => {
  beforeEach(() => { resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  it('sets minZoom above the constrained floor', () => {
    const { map } = mountTestMap({ metric: 'P1' })
    // Low enough to put Bulgaria in its continent — the floor was 5, which
    // stopped the camera at the country's own edges.
    expect(map.options.minZoom).toBeLessThanOrEqual(3)
    // Above MapLibre's own default, which is the whole point: a default of 0
    // is a floor getZoom() never reaches.
    expect(map.options.minZoom).toBeGreaterThan(0)
    // And below the view the page opens at, or the opening view would already
    // be clamped.
    expect(map.options.minZoom).toBeLessThan(Number(map.options.zoom))
  })
})

// The hex layer's wiring, as opposed to its logic: refreshHexes is tested
// directly further down, but nothing there proves mount() ever calls it. These
// two assert the layer is actually driven — once on load, again when the
// viewport moves — which is the difference between a working grid and dead
// code.
describe('mount() drives the hex layer', () => {
  beforeEach(() => { resetViewStateForTests(); clearCache() })
  afterEach(() => { resetViewStateForTests() })

  function stubHexFetch() {
    return vi.fn(async (url) => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => (String(url).startsWith('/api/v1/hexes')
        ? { resolution_km: 15, hexes: [] }
        : { areas: [] }),
    }))
  }

  const hexCalls = (f) => f.mock.calls.filter((c) => String(c[0]).startsWith('/api/v1/hexes'))

  it('asks for the grid on first load', async () => {
    const fetchSpy = stubHexFetch()
    vi.stubGlobal('fetch', fetchSpy)

    mountTestMap({ metric: 'P2' })

    await vi.waitFor(() => expect(hexCalls(fetchSpy)).toHaveLength(1))
  })

  it('asks again, at the new resolution, once the viewport settles', async () => {
    const fetchSpy = stubHexFetch()
    vi.stubGlobal('fetch', fetchSpy)
    const { map } = mountTestMap({ metric: 'P2' })
    await vi.waitFor(() => expect(hexCalls(fetchSpy)).toHaveLength(1))

    // A real zoom change, not just another moveend: an identical view produces
    // an identical URL, which refreshHexes deliberately does not refetch.
    map.getZoom.mockReturnValue(13)
    map.handlers.moveend()

    await vi.waitFor(() => {
      const calls = hexCalls(fetchSpy)
      expect(calls).toHaveLength(2)
      expect(calls[1][0]).not.toBe(calls[0][0])
    }, { timeout: 2000 })
  })
})

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
  el.dataset.markerLabelColour = '#161616'
  el.dataset.emptyBasemapColour = '#eef2f5'
  el.dataset.zoomCity = '9'
  el.dataset.zoomSensor = '11'
  el.dataset.tNoSources = 'No networks are shown'

  const { map, chrome, stop } = mount(el)
  // FakeMap.getZoom is hardcoded to 7 (see the vi.mock at the top of this
  // file) — every other test in this file relies on that fixed value, so it
  // is overridden here rather than in the mock itself, to reach the sensors
  // tier without disturbing them.
  map.getZoom = () => 12
  map.handlers.load()
  return { map, chrome, stop, el }
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
    el.dataset.markerLabelColour = '#161616'
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


// The point tier needs a layer that paints points. The fill and line layers on
// the hex source only draw Polygons, so without this one the devices arrive and
// nothing appears — the exact failure the site had while the API was already
// serving them.
describe('mount() gives the point tier a layer to paint into', () => {
  beforeEach(() => { resetViewStateForTests(); clearCache() })
  afterEach(() => { resetViewStateForTests() })

  it('adds a circle layer on the hex source', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const layers = map.addLayer.mock.calls.map((c) => c[0])
    const point = layers.find((l) => l.source === 'airbg-hexes' && l.type === 'circle')

    expect(point, 'no circle layer on the hex source').toBeTruthy()
    // Coloured by the same property the cells use, so a device and a cell at
    // the same reading are the same colour.
    expect(point.paint['circle-color']).toEqual(['get', 'colour'])
  })

  // A circle layer draws a circle at EVERY position of the geometry it is
  // handed, and a polygon's positions are its corners: once the point tier
  // started drawing cells, this layer put a dot on all six vertices of every
  // hexagon on the map. It is the fallback for a device with no size to draw
  // at, so it must see points and nothing else.
  it('paints points only, never a cell\u2019s corners', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const point = map.addLayer.mock.calls.map((c) => c[0])
      .find((l) => l.source === 'airbg-hexes' && l.type === 'circle')

    expect(point.filter).toEqual(['==', ['geometry-type'], 'Point'])
  })
})

// The reading belongs INSIDE the cell that describes it. Printed only beside a
// marker, it sat at the device's own coordinate — so at the zooms where cells
// are individually visible the number appeared off-centre in one cell, on the
// edge of the next, and nowhere at all in the rest.
describe('mount() prints the reading inside the cell', () => {
  const hexLabel = () => {
    const { map } = mountTestMap({ metric: 'P2' })
    return map.addLayer.mock.calls.map((c) => c[0])
      .find((l) => l.source === 'airbg-hexes' && l.type === 'symbol')
  }

  it('labels the cells from the source they are drawn from', () => {
    const label = hexLabel()
    expect(label, 'no symbol layer on the hex source').toBeTruthy()
    // The FRACTIONAL handover: hexesURL picks the point tier from
    // Math.round(zoom), which flips at 14.5, and MapLibre applies minzoom to
    // the true zoom. A whole 15 here left half a level where the cells were
    // already one-sensor cells with no number printed in them.
    expect(label.minzoom).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
  })

  it('centres the number rather than offsetting it past a dot', () => {
    const label = hexLabel()
    expect(label.layout['text-anchor']).toBe('center')
    expect(label.layout['text-offset']).toBeUndefined()
  })

  it('prints nothing where there is no reading, and nothing on a bare point', () => {
    const label = hexLabel()
    expect(JSON.stringify(label.filter)).toContain('value')
    expect(JSON.stringify(label.filter)).toContain('Polygon')
  })
})

// The world raster is the style mount() opens with, not something inserted on
// 'load' afterwards: there is no longer a vector style for it to be positioned
// relative to, and inserting it late meant one frame of blank canvas.
describe('mount() opens on the world raster', () => {
  it('hands MapLibre a style carrying the raster, not a URL to fetch', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const style = map.options.style

    expect(typeof style, 'still fetching a style document').toBe('object')
    expect(style.layers.some((l) => l.type === 'raster')).toBe(true)
  })

  it('does not add a second raster once loaded', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    expect(map.addSource.mock.calls.filter((c) => c[1]?.type === 'raster')).toHaveLength(0)
    expect(map.addLayer.mock.calls.filter((c) => c[0]?.type === 'raster')).toHaveLength(0)
  })
})

// MapLibre resolves icon-image when the layer renders, and an unresolved one
// draws nothing and says nothing — the exact failure the missing '→' glyph
// already produced once. The ordering is what stops it happening again.
describe('mount() registers the wind arrow before the layer that draws it', () => {
  it('adds the arrow image, then the layer naming it', () => {
    const { map } = mountTestMap({ metric: 'P2' })

    const image = map.addImage.mock.calls.find((c) => c[0] === ARROW_IMAGE_ID)
    expect(image, 'no arrow image registered').toBeDefined()
    expect(image[1].data).toHaveLength(image[1].width * image[1].height * 4)

    const at = map.addLayer.mock.calls.findIndex((c) => c[0]?.id === WIND_LAYER_ID)
    expect(at, 'no wind layer added').toBeGreaterThanOrEqual(0)
    expect(map.addLayer.mock.calls[at][0].layout['icon-image']).toBe(ARROW_IMAGE_ID)
    expect(map.addImage.mock.invocationCallOrder[0])
      .toBeLessThan(map.addLayer.mock.invocationCallOrder[at])
  })
})

// The mute is only worth anything if the layer that draws the digits actually
// asks for it; wiring plain labelPaint here would carry the readings forward
// and draw every held one as if it had been measured.
describe('mount() fades the held readings on the hex label layer', () => {
  it('builds the hex label layer with the carried-aware paint', () => {
    const { map } = mountTestMap({ metric: 'P2' })

    const labels = map.addLayer.mock.calls.find((c) => c[0]?.id === HEX_LABEL_LAYER_ID)
    expect(labels, 'no hex label layer added').toBeDefined()
    expect(labels[0].paint['text-opacity']).toEqual([
      'case',
      ['==', ['get', 'carried'], true], CARRIED_OPACITY,
      ['==', ['get', 'fresh'], 0], FRESH_OPACITY,
      ['==', ['get', 'fresh'], 1], SETTLING_OPACITY,
      1,
    ])
  })
})

// The wind layer used to be appended last, drawing over the hex value labels
// and hiding the digits — icon-allow-overlap/icon-ignore-placement keep the
// arrows from yielding, so stacking order is the only thing that decides this.
describe('mount() draws the wind arrows beneath the hex labels', () => {
  it('adds the wind layer before the hex label layer', () => {
    const { map } = mountTestMap({ metric: 'P2' })

    const wind = map.addLayer.mock.calls.find((c) => c[0]?.id === WIND_LAYER_ID)
    expect(wind[1]).toBe(HEX_LABEL_LAYER_ID)
  })
})

// The markers are what a visitor clicked to open a sensor, and above the
// handover zoom they are gone. The cells inherit the click: at the point tier
// each carries the sensor_id of the device it was built from, so the panel
// stays reachable at exactly the zooms the dots stopped covering.
describe('mount() opens a sensor from the cell that carries one', () => {
  it('binds a click to the cells and opens the sensor it names', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const onCell = map.clickHandlers['airbg-hex-fill']
    expect(onCell, 'no click handler on the cells').toBeTypeOf('function')

    onCell({ features: [{ properties: { sensorId: 4242, value: 7 } }] })
    expect(getViewState().sensorId).toBe(4242)
  })

  // The hash alone opened nothing: the panel reads the registry, which the home
  // page (no slug) never fills.
  it('loads a sensor the map does not hold yet, so the panel opens without a reload', async () => {
    clearCache()
    setSensors(null)
    const asked = []
    vi.stubGlobal('fetch', vi.fn(async (url) => {
      asked.push(String(url))
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ id: 4242, lon: 23.31, lat: 42.69, slug: 'sofia', areas: [] }),
      }
    }))
    const { map } = mountTestMap({ metric: 'P2' })
    // After the load pass, which flies to a #sensor= of its own. Waited on the
    // markers, not on /api/v1/locate: that request is started ahead of the
    // scales now (see prefetchPlacement), so it no longer marks the end of the
    // pass — and a click landing before the pass reads the viewstate would make
    // the load pass itself the deep link.
    await vi.waitFor(() => expect(asked.some((u) => u.includes('/api/v1/overview'))).toBe(true))
    await new Promise((r) => setTimeout(r, 0))
    map.jumpTo.mockClear()

    map.clickHandlers['airbg-hex-fill']({ features: [{ properties: { sensorId: 4242 } }] })

    await vi.waitFor(() => {
      expect(asked.some((u) => u.includes('/api/v1/sensor/4242/locate'))).toBe(true)
    })
    await new Promise((r) => setTimeout(r, 0))
    // In place: the reader is already looking at the cell they clicked.
    expect(map.jumpTo.mock.calls.every((c) => c[0]?.zoom !== DEEP_LINK_ZOOM)).toBe(true)
  })

  // The placement decides the opening camera, and until it answers the map is
  // showing a view it is about to leave. Asked before the scales, not after
  // them: the camera needs no band table, and awaiting one to ask for the other
  // is a round trip of national view on a slow link.
  it('asks where to open before it asks for the colour scales', async () => {
    clearCache()
    setSensors(null)
    const asked = []
    vi.stubGlobal('fetch', vi.fn(async (url) => {
      asked.push(String(url))
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ areas: [] }),
      }
    }))

    mountTestMap({ metric: 'P2' })
    await vi.waitFor(() => expect(asked.some((u) => u.endsWith('/api/v1/locate'))).toBe(true))

    expect(asked.findIndex((u) => u.endsWith('/api/v1/locate')))
      .toBeLessThan(asked.findIndex((u) => u.endsWith('/api/v1/scales')))
  })

  it('opens no panel for an aggregate cell, which names no device', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    map.clickHandlers['airbg-hex-fill']({
      features: [{ properties: { n: 9, value: 7 } }],
      lngLat: { lng: 23.32, lat: 42.7 },
    })
    expect(getViewState().sensorId ?? null).toBeNull()
  })
})

// Keyed off the marker tier, the caption told a reader zoomed onto one device
// that every cell was an area average.
describe('cellTier', () => {
  it('is what the caption under the key is written from', async () => {
    clearCache()
    setSensors(null)
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }),
    })))
    const { map, el } = mountTestMap({
      metric: 'P2',
      dataset: { tTierCountry: 'each cell averages', tTierCity: 'each cell averages', tTierSensors: 'each cell is one sensor' },
    })
    map.getZoom = vi.fn(() => POINT_TIER_MIN_ZOOM + 1)

    map.handlers.moveend()

    await vi.waitFor(() => {
      expect(el.parentNode?.querySelector('.legend__tier')?.textContent ?? el.querySelector('.legend__tier')?.textContent)
        .toBe('each cell is one sensor')
    }, { timeout: 2000 })
  })
})

// A click that opens nothing (no marker, no named cell) closes whatever panel
// is already open — otherwise the panel sits over the map with no way back to
// the ground behind it.
describe('mount() closes the panel on a click that opens nothing', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  // Fires every bare handler map.js registered, the same way a real MapLibre
  // click dispatch would — the boundary handler sits alongside the one under
  // test and must not interfere (boundaryState.on is false by default).
  const fireBareClick = (map, e) => { for (const h of map.bareClickHandlers) h(e) }

  it('closes the panel when the click lands on a multi-station cell', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const vs = getViewState()
    vs.openSensor(42)
    const spy = vi.spyOn(vs, 'closeSensor')
    map.queryRenderedFeatures = vi.fn(() => [{ properties: { n: 3 } }])

    fireBareClick(map, { point: [10, 10] })

    expect(spy).toHaveBeenCalledTimes(1)
  })

  it('closes the panel when the click lands on empty ground', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const vs = getViewState()
    vs.openSensor(42)
    const spy = vi.spyOn(vs, 'closeSensor')
    map.queryRenderedFeatures = vi.fn(() => [])

    fireBareClick(map, { point: [10, 10] })

    expect(spy).toHaveBeenCalledTimes(1)
  })

  it('does nothing on an empty click when nothing is open', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const vs = getViewState()
    const spy = vi.spyOn(vs, 'closeSensor')
    map.queryRenderedFeatures = vi.fn(() => [])

    fireBareClick(map, { point: [10, 10] })

    expect(spy).not.toHaveBeenCalled()
  })

  it('leaves the panel open when the click hits a feature naming a sensor', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const vs = getViewState()
    vs.openSensor(42)
    const spy = vi.spyOn(vs, 'closeSensor')
    map.queryRenderedFeatures = vi.fn(() => [{ properties: { sensorId: 42 } }])

    fireBareClick(map, { point: [10, 10] })

    expect(spy).not.toHaveBeenCalled()
  })
})

// A cell naming several stations has no single one to open — the click zooms
// toward the point tier instead, so the next click there names one.
describe('mount() zooms into a multi-station cell', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests(); vi.restoreAllMocks() })

  const polygonFeature = () => ({
    properties: { n: 3, value: 5 },
    geometry: { type: 'Polygon', coordinates: [[[23, 42], [24, 42], [23.5, 43], [23, 42]]] },
  })

  it('eases in on the ring centroid, and does not refresh synchronously', () => {
    vi.spyOn(mapboundaries, 'cellArea').mockReturnValue('sofia')
    const { map } = mountTestMap({ metric: 'P2' })
    map.getZoom = vi.fn(() => 9)
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)

    map.clickHandlers['airbg-hex-fill']({
      features: [polygonFeature()],
      lngLat: { lng: 23.5, lat: 42.4 },
    })

    expect(map.easeTo).toHaveBeenCalledWith({
      center: [23.5, (42 + 42 + 43) / 3],
      zoom: Math.min(9 + 2, POINT_TIER_MIN_ZOOM),
    })
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('caps the target zoom at the point tier', () => {
    vi.spyOn(mapboundaries, 'cellArea').mockReturnValue('sofia')
    const { map } = mountTestMap({ metric: 'P2' })
    const zoom = POINT_TIER_MIN_ZOOM - 1
    map.getZoom = vi.fn(() => zoom)

    map.clickHandlers['airbg-hex-fill']({
      features: [polygonFeature()],
      lngLat: { lng: 23.5, lat: 42.4 },
    })

    expect(map.easeTo).toHaveBeenCalledWith(expect.objectContaining({ zoom: POINT_TIER_MIN_ZOOM }))
  })

  it('refreshes in place, with no easeTo, once already at the point tier', () => {
    vi.spyOn(mapboundaries, 'cellArea').mockReturnValue('sofia')
    const { map } = mountTestMap({ metric: 'P2' })
    map.getZoom = vi.fn(() => POINT_TIER_MIN_ZOOM)
    const fetchSpy = vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))
    vi.stubGlobal('fetch', fetchSpy)

    map.clickHandlers['airbg-hex-fill']({
      features: [polygonFeature()],
      lngLat: { lng: 23.5, lat: 42.4 },
    })

    expect(map.easeTo).not.toHaveBeenCalled()
    expect(fetchSpy).toHaveBeenCalled()
  })

  it('centres on the click point for a Point feature, not a ring', () => {
    vi.spyOn(mapboundaries, 'cellArea').mockReturnValue('sofia')
    const { map } = mountTestMap({ metric: 'P2' })
    map.getZoom = vi.fn(() => 9)

    map.clickHandlers['airbg-hex-fill']({
      features: [{ properties: { n: 3, value: 5 }, geometry: { type: 'Point', coordinates: [23.5, 42.4] } }],
      lngLat: { lng: 23.5, lat: 42.4 },
    })

    expect(map.easeTo).toHaveBeenCalledWith(expect.objectContaining({ center: [23.5, 42.4] }))
  })

  // cellArea returns null once an area is already selected (see its own
  // comment in mapboundaries.js) — the zoom-in must not depend on it resolving.
  it('zooms even when the map is already scoped to an area', () => {
    vi.spyOn(mapboundaries, 'cellArea').mockReturnValue(null)
    const { map } = mountTestMap({ metric: 'P2', dataset: { slug: 'sofia' } })
    map.getZoom = vi.fn(() => 9)
    // A resolving stub, not a bare one: this leaks past restoreAllMocks (see
    // the note on the other stubGlobal calls in this describe) and a bare
    // vi.fn() left standing broke unrelated fetches in later describes.
    const fetchSpy = vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))
    vi.stubGlobal('fetch', fetchSpy)

    map.clickHandlers['airbg-hex-fill']({
      features: [polygonFeature()],
      lngLat: { lng: 23.5, lat: 42.4 },
    })

    expect(map.easeTo).toHaveBeenCalledWith({
      center: [23.5, (42 + 42 + 43) / 3],
      zoom: 11,
    })
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  // Already at the point tier, and no area resolves: there is nothing to zoom
  // toward and nothing to select, so the click is a no-op.
  it('does nothing at the point tier when no area resolves', () => {
    vi.spyOn(mapboundaries, 'cellArea').mockReturnValue(null)
    const { map } = mountTestMap({ metric: 'P2' })
    map.getZoom = vi.fn(() => POINT_TIER_MIN_ZOOM)
    // A resolving stub — see the note on the previous test's fetchSpy.
    const fetchSpy = vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))
    vi.stubGlobal('fetch', fetchSpy)

    map.clickHandlers['airbg-hex-fill']({
      features: [polygonFeature()],
      lngLat: { lng: 23.5, lat: 42.4 },
    })

    expect(map.easeTo).not.toHaveBeenCalled()
    expect(fetchSpy).not.toHaveBeenCalled()
  })
})

// The two ways of showing one reading must never be on screen at once: the dot
// is the device's position, the cell is the ground around it. Drawn together
// they put a labelled dot off-centre inside a labelled cell. The dots stop
// exactly where the cells take the number over.
describe('mount() hands the reading from the dots to the cells', () => {
  it('stops the aggregate markers where the cells start, not where the dots do', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const markers = map.addLayer.mock.calls.map((c) => c[0])
      .filter((l) => l.source === 'airbg-data')

    // Circles, official diamonds, labels — every layer the sensor source
    // feeds, so a fourth one added later has to answer this question too.
    expect(markers).toHaveLength(3)
    // Mounted on the country tier: those markers are province/municipality
    // circles, and the cells cover the same ground from GRID_MIN_ZOOM up.
    // Held at the point tier they were drawn OVER six zoom levels of hexes —
    // the dots-on-hexes the map showed.
    for (const l of markers) expect(l.maxzoom).toBe(GRID_MIN_ZOOM_FRACTIONAL)
  })

  it('moves the handover when the tier changes under a mounted map', () => {
    const ranges = []
    const map = {
      getLayer: (id) => ({ id }),
      setLayerZoomRange: (id, min, max) => ranges.push([id, min, max]),
    }

    applyMarkerZoomRange(map, 'sensors')
    expect(ranges).toEqual([
      ['airbg-markers', 0, POINT_TIER_MIN_ZOOM_FRACTIONAL],
      // The official diamonds hand over with the circles they stand beside.
      // Left out, they would have outlived the network they belong to.
      ['airbg-markers-official', 0, POINT_TIER_MIN_ZOOM_FRACTIONAL],
      ['airbg-marker-labels', 0, POINT_TIER_MIN_ZOOM_FRACTIONAL],
    ])
  })

  it('skips layers the style does not carry', () => {
    const ranges = []
    const map = {
      getLayer: () => undefined,
      setLayerZoomRange: (...a) => ranges.push(a),
    }
    applyMarkerZoomRange(map, 'country')
    expect(ranges).toEqual([])
  })

  it('does not draw the grid below the zoom its coarsest tier can fill', () => {
    // The server's coarsest cell is 15 km. Below GRID_MIN_ZOOM one of them is
    // under a pixel wide, which is what turned the whole grid into a field of
    // dots when zoomed out.
    const { map } = mountTestMap({ metric: 'P2' })
    const grid = map.addLayer.mock.calls.map((c) => c[0])
      .filter((l) => l.source === 'airbg-hexes' && l.type !== 'symbol')

    expect(grid.length).toBeGreaterThan(0)
    for (const l of grid) expect(l.minzoom).toBe(GRID_MIN_ZOOM_FRACTIONAL)
  })
})

// The cfg the mountChrome tests that stayed hand in; the rest of them live
// in ../../lib/__tests__/chrome.test.js.
const chromeCfg = (over = {}) => ({
  t: { tier: {} },
  noDataColour: '#999',
  lang: 'bg',
  metric: 'P2',
  metricLabels: { P1: 'ФПЧ10', P2: 'ФПЧ2.5' },
  metricUnits: { P1: 'µg/m³', P2: 'µg/m³' },
  ...over,
})

// Below the point tier a cell is an average of many sensors, and the number in
// it is the one thing the colour cannot say precisely. It stays off by default
// — a country of printed numbers is unreadable and the ramp is the primary
// reading — but a reader comparing two neighbourhoods should not have to zoom
// to sensor level, one cell at a time, to get the figures.
describe('the cell-values toggle', () => {
  const range = (map) =>
    map.setLayerZoomRange.mock.calls.filter((c) => c[0] === HEX_LABEL_LAYER_ID).at(-1)

  it('offers cell values as a layers option, off until asked for', () => {
    const { el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:cellValues"]')

    expect(box, 'no cell-values option in the layers menu').not.toBe(null)
    expect(box.checked).toBe(false)
    expect(box.closest('.colmenu__opt').textContent).toBe('Cell values')
  })

  it('leaves the labels at the point tier while it is off', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    expect(range(map)[1]).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
  })

  // The same zoom the cells themselves start at: a number that appeared at some
  // zoom of its own would print over ground that has no cell drawn under it.
  it('prints a number in every drawn cell once it is ticked', () => {
    const { map, el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:cellValues"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))

    expect(range(map)[1]).toBe(GRID_MIN_ZOOM_FRACTIONAL)
  })

  it('gives the point tier back when it is unticked', () => {
    const { map, el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:cellValues"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    box.checked = false
    box.dispatchEvent(new Event('change'))

    expect(range(map)[1]).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
  })
})

// Wind is an overlay like the rest of what the map draws, so its control sits
// with them. A button of its own in the corner said it was a different kind of
// thing, and left the corner carrying two stacked buttons and a disclosure.
describe('the wind toggle lives in the layers menu, not in the corner', () => {
  it('mounts no wind button of its own', () => {
    const el = document.createElement('div')
    mountChrome(el, chromeCfg({ t: { tier: {}, windToggle: 'Вятър' } }))
    expect(el.querySelector('.map-wind')).toBe(null)
  })

  it('offers wind as a layers option, off until it is asked for', () => {
    const { el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:wind"]')

    expect(box, 'no wind option in the layers menu').not.toBe(null)
    // Every other option starts on. This one is not part of the map the reader
    // was shown, and turning it on costs a request.
    expect(box.checked).toBe(false)
    expect(box.closest('.colmenu__opt').textContent).toBe('Wind')
  })

  it('shows the arrows when the option is ticked', async () => {
    const { map, el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:wind"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    await vi.waitFor(() => {
      expect(map.setLayoutProperty).toHaveBeenCalledWith(WIND_LAYER_ID, 'visibility', 'visible')
    })
  })

  // The arrow lattice is sized to the viewport, so the arrows a zoomed-in map
  // needs do not exist until the move is over. Without this the layer keeps the
  // field it was switched on with and empties out as the reader zooms in.
  it('redraws the field after a move, while the layer is on', async () => {
    const source = { setData: vi.fn() }
    const { map, el } = mountTestMap({ metric: 'P2' })
    // Only the wind source is spied: refresh and refreshHexes set data on their
    // own sources on the same moveend, and a shared spy could not tell them apart.
    map.getSource = vi.fn((id) => (id === WIND_SOURCE_ID ? source : { setData: vi.fn() }))
    const box = el.querySelector('[data-layer-key="view:wind"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled())

    source.setData.mockClear()
    map.getZoom.mockReturnValue(14)
    map.handlers.moveend()

    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled(), { timeout: 2000 })
  })

  // Switched on and then off again, the forecast body is still cached — so the
  // "have I got data" test is not enough to keep a hidden layer from being
  // repainted on every move.
  it('does not redraw the field after a move while the layer is off', async () => {
    const source = { setData: vi.fn() }
    const { map, el } = mountTestMap({ metric: 'P2' })
    // Only the wind source is spied: refresh and refreshHexes set data on their
    // own sources on the same moveend, and a shared spy could not tell them apart.
    map.getSource = vi.fn((id) => (id === WIND_SOURCE_ID ? source : { setData: vi.fn() }))
    const box = el.querySelector('[data-layer-key="view:wind"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled())
    box.checked = false
    box.dispatchEvent(new Event('change'))
    source.setData.mockClear()

    map.getZoom.mockReturnValue(14)
    map.handlers.moveend()

    await new Promise((r) => setTimeout(r, 600))
    expect(source.setData).not.toHaveBeenCalled()
  })
})

// One zoom used to draw twice: the markers when they landed, the grid a request
// later. Both are held now until both have answered.
describe('a move paints its layers in one pass', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  it('holds the marker paint until the grid has answered too', async () => {
    let gate = null
    vi.stubGlobal('fetch', vi.fn(async (url) => {
      if (String(url).includes('/api/v1/hexes') && gate) await gate
      const json = String(url).includes('/api/v1/scales')
        ? [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }] }]
        : { areas: [{ slug: 'sofia', lon: 23.3, lat: 42.7, covered: true, values: { P2: 5 }, sensor_count: 9 }], hexes: [] }
      return { ok: true, status: 200, headers: new Headers(), json: async () => json }
    }))

    const { map } = mountTestMap({ metric: 'P2' })
    await vi.waitFor(() => expect(map.painted).toContain('airbg-data'))

    let release
    gate = new Promise((r) => { release = r })
    map.painted.length = 0
    map.getZoom.mockReturnValue(10)
    map.handlers.moveend()

    // Long enough for the markers to have fetched, parsed and — before this
    // change — painted, while the grid is still out.
    await new Promise((r) => setTimeout(r, 600))
    expect(map.painted).toEqual([])

    release()
    await vi.waitFor(() => {
      expect(map.painted).toContain('airbg-data')
      expect(map.painted).toContain('airbg-hexes')
    })
  })
})

// The sensor status filter (lib/sensorfilter.svelte.js) decides which sensors
// the map draws. Driven through mount() rather than through repaintSensors
// alone, because the wiring — the subscription, and the payload the map keeps
// in hand so a filter change needs no second fetch — is the part that can
// silently rot.
describe('the sensor status filter', () => {
  // Two sensors, one of them silent on P2: the whole point of the filter is
  // that these two are not the same number.
  function stubMixedSensorFetch() {
    return vi.fn(async (url) => {
      if (url === '/api/v1/scales') {
        return { ok: true, status: 200, headers: new Headers(), json: async () => [] }
      }
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({
          sensors: {
            id: [42, 43], lon: [23.3, 23.4], lat: [42.7, 42.8],
            quality: ['ok', 'ok'], P2: [12, null],
          },
        }),
      }
    })
  }

  // A stable source stub: FakeMap.getSource hands back a fresh mock per call,
  // so without this every setData lands on an object the test cannot see.
  // A stable source stub, keyed by id: FakeMap.getSource hands back a fresh
  // mock per call, so without this every setData lands on an object the test
  // cannot see — and one shared stub would mix the sensor layer's paints with
  // the wind layer's, whose last call is an empty collection.
  function withStableSource(map) {
    const sources = new Map()
    map.getSource = vi.fn((id) => {
      if (!sources.has(id)) sources.set(id, { setData: vi.fn() })
      return sources.get(id)
    })
    return {
      get setData() { return sources.get('airbg-data')?.setData ?? { mock: { calls: [] } } },
    }
  }

  const drawn = (source) => source.setData.mock.calls.at(-1)[0].features

  beforeEach(() => { clearCache(); resetViewStateForTests(); setSensors(null); resetSensorFilterForTests(); resetSourceFilterForTests() })
  afterEach(() => { resetViewStateForTests(); setSensors(null); resetSensorFilterForTests(); resetSourceFilterForTests() })

  // The store opens on the kit's default, "with data", so the FIRST paint is
  // already filtered — the silent sensor never reaches the map until asked for.
  it('opens on the sensors with data, before the reader touches anything', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    const source = withStableSource(map)

    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled())
    expect(drawn(source).map((f) => f.properties.id)).toEqual([42])
  })

  // The repaint must come from the payload already in hand. A filter change
  // touches neither the tier, the slug nor the metric, so a refetch would be a
  // request for data the island is holding.
  it('repaints from the payload it already has, with no second request', async () => {
    const fetchSpy = stubMixedSensorFetch()
    vi.stubGlobal('fetch', fetchSpy)
    mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())

    const before = fetchSpy.mock.calls.length
    setSensorStatus('all')
    expect(fetchSpy.mock.calls.length).toBe(before)
  })

  it('brings the silent sensors back when the reader asks for all of them', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    const source = withStableSource(map)

    setSensorStatus('all')

    expect(drawn(source).map((f) => f.properties.id)).toEqual([42, 43])
  })

  it('keeps only the silent ones on the other side of the filter', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    const source = withStableSource(map)

    setSensorStatus('inactive')

    expect(drawn(source).map((f) => f.properties.id)).toEqual([43])
  })

  // The filter is a control over sensors. Zoomed out to province aggregates
  // there are no sensors on screen, so a click on it must leave the aggregates
  // alone rather than repaint the last sensor payload over them.
  it('does not repaint once the map has left the sensor tier', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())

    // Aggregate tier: a lower zoom, and an area marker click to drive the
    // refresh synchronously rather than through the debounced moveend.
    map.getZoom = () => 7
    const after = vi.fn(() => ({ setData: vi.fn() }))
    map.getSource = after
    map.handlers.click({ features: [{ properties: { slug: 'plovdiv' } }] })
    // The aggregate paint, not the sensor one: waiting on the spy installed
    // above is what guarantees the tier change has actually landed.
    await vi.waitFor(() => expect(after).toHaveBeenCalled())

    map.getSource = vi.fn(() => ({ setData: vi.fn() }))
    setSensorStatus('active')

    expect(map.getSource).not.toHaveBeenCalled()
  })

  // The banner, not the paint: unticking both networks empties the marker
  // source, and the source-change handler is the only thing that recomputes
  // the hint without a refresh behind it.
  it('explains the blank map as soon as both networks are unticked', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { el } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    const banner = el.querySelector('.map-hint')

    setSourceEnabled('sensor.community', false)
    setSourceEnabled('eea', false)

    expect(banner.hidden).toBe(false)
    expect(banner.textContent).not.toBe('')
  })

  // A subscription that outlives the island repaints a destroyed map on the
  // next status change.
  it('stops listening once the island is stopped', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map, stop } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    withStableSource(map)

    stop()
    setSensorStatus('active')

    // getSource, not setData: a stopped island never reaches the source at
    // all, so there is no spy on the sensor layer to interrogate.
    expect(map.getSource).not.toHaveBeenCalled()
  })
})


// The official stations draw as diamonds, the citizen ones as circles, and no
// marker is allowed to fall between the two layers or land in both.
describe('the official marker layer', () => {
  it('splits the source in two along the network, leaving areas with the dots', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const [circles, diamonds] = map.addLayer.mock.calls.map((c) => c[0])
      .filter((l) => l.source === 'airbg-data' && l.id !== 'airbg-marker-labels')

    expect(circles.type).toBe('circle')
    expect(diamonds.type).toBe('symbol')
    expect(circles.filter).toEqual(NOT_OFFICIAL)
    expect(diamonds.filter).toEqual(['==', ['get', 'source'], 'eea'])
  })

  it('registers the diamond as an SDF, which is what lets the ramp colour it', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const [id, image, opts] = map.addImage.mock.calls.find((c) => c[0] === 'airbg-diamond')

    expect(id).toBe('airbg-diamond')
    expect(image.width).toBe(image.height)
    expect(opts.sdf).toBe(true)
  })

})

// mountChrome returns a storage handle so player and legend prefs can thread
// through injected storage in tests and use the same handle in production.
describe('mountChrome storage handle', () => {
  it('threads the injected storage handle through to the player', async () => {
    const store = new Map()
    store.set(PLAY_SPEED_KEY, '0.5') // Set initial speed
    const fakeStorage = {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, v),
    }

    const { map } = mountTestMap({ metric: 'P2' })

    // The whole point of the task: chrome, built by mountChrome, is the object
    // installTimelapse is handed. Hand-building `{player, storage}` here would
    // pass whether or not mountChrome returns a storage handle at all.
    const el = document.createElement('div')
    document.body.appendChild(el)
    const chrome = mountChrome(el, chromeCfg({ storage: fakeStorage }))
    const ui = chrome.player

    installTimelapse(map, {}, { metric: 'P2', lang: 'en', t: {} }, chrome, async () => ({
      metric: 'P2', resolution_km: 15, cells: [[23, 42]],
      frames: [{ t: '2026-09-08T06:00:00Z', v: [10] }],
    }))

    ui.button.click()
    await vi.waitFor(() => expect(ui.speed.textContent).not.toBe(''))

    // Verify the initial speed was read from the fake storage
    expect(ui.speed.textContent).toBe('0.5×')

    // Change the speed
    ui.speed.click()
    expect(ui.speed.textContent).toBe('0.25×')

    // Verify it was written to the fake storage
    expect(store.get(PLAY_SPEED_KEY)).toBe('0.25')

    ui.button.click() // close player
  })
})
