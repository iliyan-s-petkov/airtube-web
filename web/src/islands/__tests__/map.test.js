// @vitest-environment jsdom
//
// jsdom, not the default node environment: the repaint test below drives
// mount() through a real container element and a real `location.hash` /
// `hashchange`, which the rest of this file's pure-logic tests do not need
// but do not mind either — jsdom is a superset, not a different behaviour,
// for code that touches no DOM.
// node:fs, not a fixture: the translation-key rule test below reads
// internal/web/templates/ off disk so the template stays the single source.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { DIAMOND_RADIUS_PX } from '../../lib/markericon.js'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { urlFor, bandsFor, markerMaxZoom, applyMarkerZoomRange, hexOutlinePaint, refreshHexes, installTimelapse, areaFeatures, sensorFeatures, readConfig, debounce, loadScales, hintController, mapHint, setSourceViewAvailability, initData, layerPaint, markerPaint, officialLayout, officialPaint, NOT_OFFICIAL, metricNote, mapStyle, glyphsURL, cellArea, cellTier, overlayLayers, addBasemapOverlay, registerProtocols, installErrorHandler, mount, mountChrome, HEX_LABEL_LAYER_ID, HEX_SOURCE_ID, hexLabelPaint, CARRIED_OPACITY, FRESH_OPACITY, SETTLING_OPACITY, PLAY_SPEED_KEY, LEGEND_FOLD_KEY, locateVisitor, placeVisitor, locateMe, showArea, openDeepLinkedSensor, prefetchPlacement, DEEP_LINK_ZOOM, layerLabelKey } from '../map.js'
import { ARROW_IMAGE_ID, WIND_LAYER_ID, WIND_SOURCE_ID } from '../wind.js'
import { GRID_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM, resolutionForZoom } from '../../lib/hexes.js'
import { clearCache } from '../../lib/api.js'
import { mountPlayer, FRAME_MS } from '../../lib/timelapse.js'
import { resetViewStateForTests, getViewState } from '../../lib/viewstate.svelte.js'
import { findSensor, setSensors } from '../../lib/sensors.svelte.js'
import { setSensorStatus, getSensorStatus, resetSensorFilterForTests } from '../../lib/sensorfilter.svelte.js'
import { setSourceEnabled, resetSourceFilterForTests } from '../../lib/sourcefilter.svelte.js'
import { getMapAreas, setMapAreas } from '../../lib/mapareas.svelte.js'
import { LAYER_ORDER } from '../../lib/maplayers.js'

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
    }
    // map.on('click', LAYER_ID, cb) carries the layer id as a second
    // argument; every other event map.js registers is map.on(event, cb).
    // Clicks are ALSO kept per layer: the map binds one click handler to the
    // markers and another to the cells, and a single `handlers.click` slot
    // would silently hand every test the last one registered.
    on(event, a, b) {
      if (event !== 'click') { this.handlers[event] = a; return }
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

  // The ramp and the key both take bands, never scales, so a ceiling that stops
  // here is a ceiling nothing draws to.
  it('carries the scale ceiling onto the top band, and onto no other', () => {
    const withCeiling = [
      { metric: 'P2', ceiling: 500, bands: [{ upper: 25, colour: 'y' }, { upper: null, colour: 'z' }] },
    ]
    expect(bandsFor(withCeiling, 'P2')).toEqual([
      { upper: 25, colour: 'y' },
      { upper: null, colour: 'z', ceiling: 500 },
    ])
  })

  it('leaves the bands untouched when the scale states no ceiling', () => {
    expect(bandsFor(scales, 'P1')).toEqual([{ upper: 50, colour: 'x' }])
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

  // The language prefix is server-supplied for any language, not just the two
  // that happen to be embedded — and '' is the default language's real value,
  // not a missing one.
  it('reads the language prefix the server rendered', () => {
    expect(readConfig({ dataset: { langPrefix: '/de' } }).langPrefix).toBe('/de')
    expect(readConfig({ dataset: {} }).langPrefix).toBe('')
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

  // The attribute names the server actually renders on the map island, read
  // off the template rather than kept as a list here: index.gohtml's open tag
  // with its {{template}} partials (mapLayerLabels) spliced in, which is the
  // attribute set the browser hands readConfig.
  function mapIslandAttributes() {
    // import.meta.dirname, not cwd: vitest is run from web/, but this rule is
    // about a file four levels up and must not move when the cwd does.
    const dir = join(import.meta.dirname, '../../../../internal/web/templates')
    const base = readFileSync(join(dir, 'base.gohtml'), 'utf8')
    const page = readFileSync(join(dir, 'index.gohtml'), 'utf8')
    const marker = page.indexOf('data-island="map"')
    expect(marker).toBeGreaterThan(-1)
    const start = page.lastIndexOf('<', marker)
    // Quote-aware: stop at the '>' that closes the open tag, never at one
    // inside a translated string.
    let end = start
    for (let quoted = false; end < page.length; end++) {
      if (page[end] === '"') quoted = !quoted
      else if (page[end] === '>' && !quoted) break
    }
    const tag = page.slice(start, end).replace(/\{\{template\s+"([^"]+)"[^}]*\}\}/g, (_, name) => {
      const define = base.match(new RegExp(`\\{\\{define "${name}"\\}\\}([\\s\\S]*?)\\{\\{end\\}\\}`))
      expect(define, `{{define "${name}"}} in base.gohtml`).not.toBeNull()
      return define[1]
    })
    // The leading (?<![\w-]) is what keeps this off `my-data-t-x`; requiring a
    // trailing [a-z0-9] before the '=' keeps it off a bare `data-t-`.
    return new Set([...tag.matchAll(/(?<![\w-])data-t-([a-z0-9-]*[a-z0-9])=/g)].map((m) => m[1]))
  }

  // The DOM's own attribute-to-dataset rule: a hyphen before an ASCII lowercase
  // letter uppercases it, a hyphen before a digit stays (data-t-window-24h is
  // tWindow-24h, which is why that one is positional instead).
  function datasetKey(attr) {
    const camel = attr.replace(/-([a-z])/g, (_, c) => c.toUpperCase())
    return 't' + camel[0].toUpperCase() + camel.slice(1)
  }

  // Which dataset properties readConfig actually touches, recorded rather than
  // restated: the alias (communitySensors) and the two nested shapes (tier,
  // layers) mean cfg.t's key names are not the attribute names, so the
  // comparison has to happen on the dataset side of readConfig, not after it.
  function datasetKeysReadConfigReads() {
    const seen = new Set()
    const dataset = new Proxy({}, {
      get(_, prop) {
        if (typeof prop === 'string') seen.add(prop)
        return ''
      },
    })
    readConfig({ dataset })
    return seen
  }

  const sorted = (set) => [...set].sort()

  // A rule, not a snapshot. The exact-object toEqual this replaced passed only
  // for one frozen list: it proved someone had edited two literals in step, not
  // that the template and the reader agree. Set equality both ways fails the
  // moment either side gains or loses a string.
  it('reads exactly the data-t-* attributes the template renders on the map island', () => {
    const rendered = new Set([...mapIslandAttributes()].map(datasetKey))
    // t followed by a non-lowercase char: the dataset spelling of data-t-*,
    // and never 'then'/'toString'/'title' that a Proxy also sees.
    const read = new Set(sorted(datasetKeysReadConfigReads()).filter((k) => /^t[^a-z]/.test(k)))
    expect(rendered.size).toBeGreaterThan(40)
    expect(sorted(rendered)).toEqual(sorted(read))
  })

  // The three places where one attribute is not one flat key, which the set
  // comparison above deliberately cannot see.
  it('nests and aliases the translations the lookups index by', () => {
    const cfg = readConfig({
      dataset: {
        tLayerBase: 'Terrain and parks', tLayerStreetNames: 'Street names',
        tTierCountry: 'Each dot is an oblast average',
        tViewCommunitySensors: 'Citizen sensors',
        tViewOfficialStations: 'Official stations',
      },
    })
    // One entry per LAYER_ORDER group, always: the menu looks a label up by the
    // group the STYLE reports, so an absent key is a group rendering under its
    // own slug the day the style starts carrying it.
    expect(Object.keys(cfg.t.layers)).toEqual(LAYER_ORDER)
    expect(cfg.t.layers.base).toBe('Terrain and parks')
    expect(cfg.t.layers['street-names']).toBe('Street names')
    expect(cfg.t.layers.water).toBe('')
    // Keyed by the names tierFor returns, so showLegend indexes rather than branches.
    expect(cfg.t.tier).toEqual({ country: 'Each dot is an oblast average', city: '', sensors: '' })
    // setSourceViewAvailability keys the checkbox label by view id, so the two
    // source labels are read a second time under a second name.
    expect(cfg.t.communitySensors).toBe('Citizen sensors')
    expect(cfg.t.officialStations).toBe('Official stations')
  })

  // One comma-separated attribute, positional against WINDOW_CHOICES, because
  // the per-window alternative would have to spell "24h" as a dataset key and
  // data-t-window-24h converts to tWindow-24h — not reachable with a dot.
  it('reads the window labels positionally from one attribute', () => {
    const cfg = readConfig({ dataset: { tWindows: 'Now,Last 24 hours,Last 48 hours,Last week' } })
    expect(cfg.windowLabels).toEqual(['Now', 'Last 24 hours', 'Last 48 hours', 'Last week'])
  })

  it('reads no window labels when the attribute is absent', () => {
    expect(readConfig({ dataset: {} }).windowLabels).toEqual([])
  })

  // The one place the style's group names and the template's attribute names
  // have to agree. They agree by rule, so the rule is what gets tested: a
  // second hand-kept list is exactly what this function exists to avoid.
  it('turns a style group into the dataset spelling of its label attribute', () => {
    expect(layerLabelKey('water')).toBe('tLayerWater')
    // The hyphenated ones are the whole point: data-t-layer-street-names and
    // data-t-layer-poi-education are where a hand-written mapping would slip.
    expect(layerLabelKey('street-names')).toBe('tLayerStreetNames')
    expect(layerLabelKey('poi-education')).toBe('tLayerPoiEducation')
  })

  // data-metrics is the same attribute (and same parseMetricList) the switcher
  // island reads — getViewState needs the full metric list to validate a
  // metric read from the hash before adopting it.
  it('reads the metric list from data-metrics with parseMetricList\'s own blank-input rule', () => {
    expect(readConfig({ dataset: { metrics: 'P1,P2,temperature' } }).metrics).toEqual(['P1', 'P2', 'temperature'])
    expect(readConfig({ dataset: {} }).metrics).toEqual([])
  })

  // Keyed by metric, not positional: the key looks its caption up by name.
  it('keys the metric names and units by metric', () => {
    const cfg = readConfig({
      dataset: {
        metrics: 'P1,P2,temperature',
        metricLabels: 'PM10,PM2.5,Temperature',
        metricUnits: 'µg/m³,µg/m³,°C',
      },
    })
    expect(cfg.metricLabels).toEqual({ P1: 'PM10', P2: 'PM2.5', temperature: 'Temperature' })
    expect(cfg.metricUnits.temperature).toBe('°C')
  })

  // A metric the server has no unit for is an empty slot, not a missing one:
  // the list stays positional, so every later unit keeps its own metric.
  it('keeps a metric with no unit aligned with the ones after it', () => {
    const cfg = readConfig({
      dataset: { metrics: 'P1,pressure,temperature', metricUnits: 'µg/m³,,°C' },
    })
    expect(cfg.metricUnits).toEqual({ P1: 'µg/m³', pressure: '', temperature: '°C' })
  })

  // Absent attributes: every metric gets '', never undefined, because the
  // caption prints what it is given and "undefined" is a word.
  it('gives every metric an empty string when the attributes are missing', () => {
    const cfg = readConfig({ dataset: { metrics: 'P1,P2' } })
    expect(cfg.metricLabels).toEqual({ P1: '', P2: '' })
    expect(cfg.metricUnits).toEqual({ P1: '', P2: '' })
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

// The animation is the hex layer with a past hour's numbers in it. These fix
// what that must NOT disturb: the live grid the map goes back to, and the
// nothing it costs a reader who never presses play.
describe('installTimelapse', () => {
  const cfg = { metric: 'P2', noDataColour: '#9ca3af', lang: 'en' }
  const BODY = {
    metric: 'P2', resolution_km: 15, cells: [[23, 42]],
    frames: [{ t: '2026-09-08T06:00:00Z', v: [10] }, { t: '2026-09-08T07:00:00Z', v: [20] }],
  }

  function harness(fetchJSON, t, storage) {
    const painted = []
    let zoom = 12
    const zoomHandlers = []
    const map = {
      getZoom: () => zoom,
      // Fires the same handlers a real MapLibreMap would, so a test can move
      // the map the way a reader's pinch or scroll does.
      setZoom: (z) => { zoom = z; for (const fn of zoomHandlers) fn() },
      getBounds: () => ({ getWest: () => 23, getSouth: () => 42, getEast: () => 24, getNorth: () => 43 }),
      getSource: () => ({ setData: (d) => painted.push(d) }),
      on: (evt, fn) => { if (evt === 'zoom') zoomHandlers.push(fn) },
      off: (evt, fn) => {
        if (evt !== 'zoom') return
        const i = zoomHandlers.indexOf(fn)
        if (i >= 0) zoomHandlers.splice(i, 1)
      },
    }
    const ui = mountPlayer(document.createElement('div'), { label: 'Time', playLabel: 'Play', pauseLabel: 'Pause', exitLabel: 'Now', speedLabel: 'Speed' })
    const state = { scales: null, hexUrl: null, window: '24h' }
    const ctl = installTimelapse(map, state, { ...cfg, t: { ...cfg.t, ...t } }, { player: ui, storage }, fetchJSON)
    return { painted, ui, state, ctl, map }
  }

  // The two shapes production actually serves that the guard exists for,
  // measured 2026-09-18: noise_LAeq has no history at all, and NO2 over 24h
  // lands in bursts, leaving most hours blank between them.
  const NO_HISTORY = {
    metric: 'noise_LAeq', resolution_km: 15, cells: [],
    frames: [{ t: '2026-09-08T06:00:00Z', v: [] }, { t: '2026-09-08T07:00:00Z', v: [] }],
  }
  const PATCHY = {
    metric: 'NO2', resolution_km: 15, cells: [[23, 42], [23.2, 42], [23.4, 42], [23.6, 42]],
    frames: [
      { t: '2026-09-08T06:00:00Z', v: [1, 2, 3, 4] },
      { t: '2026-09-08T07:00:00Z', v: [null, null, null, null] },
      { t: '2026-09-08T08:00:00Z', v: [1, 2, 3, 4] },
    ],
  }
  // Same shape as PATCHY but on the metric this harness draws, so the cells
  // actually reach the layer.
  const GAPPY = {
    metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42], [23.4, 42], [23.6, 42]],
    frames: [
      { t: '2026-09-08T06:00:00Z', v: [1, 2, 3, 4] },
      { t: '2026-09-08T07:00:00Z', v: [null, null, null, null] },
      { t: '2026-09-08T08:00:00Z', v: [1, 2, 3, 4] },
    ],
  }
  const T = { replayThin: 'Partial data for this hour', replayNoHistory: 'Not enough history yet' }

  // Speed changes the gap between frames and nothing else: the same frames, in
  // the same order, from the same body.
  describe('playback speed', () => {
    const storageFor = (raw) => {
      const kv = new Map()
      if (raw !== undefined) kv.set(PLAY_SPEED_KEY, raw)
      return { kv, getItem: (k) => (kv.has(k) ? kv.get(k) : null), setItem: (k, v) => kv.set(k, v) }
    }

    it('opens at full speed and cycles on each press', async () => {
      const { ui } = harness(async () => BODY, T)
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      expect(ui.speed.textContent).toBe('1\u00d7')
      ui.speed.click()
      expect(ui.speed.textContent).toBe('0.5\u00d7')
      ui.speed.click()
      expect(ui.speed.textContent).toBe('0.25\u00d7')
      ui.speed.click()
      expect(ui.speed.textContent).toBe('1\u00d7')
    })

    it('remembers the speed for the next visit', async () => {
      const store = storageFor()
      const { ui } = harness(async () => BODY, T, store)
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      ui.speed.click()
      expect(store.kv.get(PLAY_SPEED_KEY)).toBe('0.5')
    })

    it('opens at the remembered speed', async () => {
      const { ui } = harness(async () => BODY, T, storageFor('0.25'))
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      expect(ui.speed.textContent).toBe('0.25\u00d7')
    })

    // The whole point of the control. At half speed the map must still be on
    // the same frame after one full-speed interval has passed.
    it('holds each frame longer at a slower speed', async () => {
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T, storageFor('0.5'))
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS)
        expect(painted.length, 'advanced a frame at half speed').toBe(after)
        await vi.advanceTimersByTimeAsync(FRAME_MS)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })

    // Pressing the speed button mid-animation must take effect now, not at the
    // next press of play — the timer is already running at the old delay.
    it('applies a speed change to a running animation', async () => {
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        ui.speed.click()
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS)
        expect(painted.length, 'still on the old fast timer').toBe(after)
        // +16: the fake clock's rAF ticks land on its own 16ms grid, not on
        // this delay's boundary, so the tick that crosses it can be up to one
        // frame later than the exact millisecond.
        await vi.advanceTimersByTimeAsync(FRAME_MS + 16)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })

    // A reader who has paused and pressed the speed button is asking what the
    // next play will look like, not for the animation to start again.
    it('does not start the animation when paused', async () => {
      const { painted, ui } = harness(async () => BODY, T)
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      ui.button.click()
      await vi.waitFor(() => expect(ui.button.getAttribute('aria-pressed')).toBe('false'))
      const after = painted.length
      ui.speed.click()
      expect(ui.button.getAttribute('aria-pressed')).toBe('false')
      expect(painted.length).toBe(after)
    })
  })

  // Pressing play is user-initiated, so replay must still run under reduced
  // motion — only the pace changes, floored at the 0.25x delay.
  describe('reduced motion', () => {
    const mockReducedMotion = (matches) => {
      globalThis.matchMedia = vi.fn((q) => ({ media: q, matches }))
    }

    it('floors the delay at 0.25x even at full speed', async () => {
      vi.useFakeTimers()
      try {
        mockReducedMotion(true)
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        // Full speed's own delay (FRAME_MS) is nowhere near the 0.25x floor
        // (4 * FRAME_MS) — three of it must still produce nothing.
        await vi.advanceTimersByTimeAsync(FRAME_MS * 3)
        expect(painted.length, 'reduced motion must floor the delay').toBe(after)
        await vi.advanceTimersByTimeAsync(FRAME_MS * 2 + 16)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
        delete globalThis.matchMedia
      }
    })

    // matchMedia is read fresh on every run(), not cached at install, so a
    // reader flipping the OS setting mid-session takes effect on the very
    // next play without a reload.
    it('re-reads the preference on every run(), not once at install', async () => {
      vi.useFakeTimers()
      try {
        mockReducedMotion(false)
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))

        mockReducedMotion(true)
        ui.speed.click()
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS * 3)
        expect(painted.length, 'now floored, mid-session').toBe(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
        delete globalThis.matchMedia
      }
    })

    // jsdom, like some old browsers, has no matchMedia at all — that must
    // read as "no preference", not throw.
    it('treats a missing matchMedia as no preference', async () => {
      expect(typeof matchMedia).toBe('undefined')
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS + 16)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })
  })

  describe('the rAF clock', () => {
    // The point of the accumulator: a naive "paint on every rAF tick" bug
    // would paint far more than 3 times across a span this long, since the
    // fake clock's rAF fires roughly every 16ms.
    it('advances one frame per elapsed delay, not once per rAF tick', async () => {
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS * 3 + 16)
        expect(painted.length - after).toBe(3)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })

    it('pauses while the tab is hidden, and resumes without a catch-up burst', async () => {
      vi.useFakeTimers()
      const setHidden = (v) => Object.defineProperty(document, 'hidden', { configurable: true, value: v })
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))

        setHidden(true)
        document.dispatchEvent(new Event('visibilitychange'))
        const hiddenSince = painted.length
        // Ten frame-delays of background time: a clock that keeps ticking
        // while hidden would paint several frames across this span.
        await vi.advanceTimersByTimeAsync(FRAME_MS * 10)
        expect(painted.length, 'no paint while hidden').toBe(hiddenSince)

        setHidden(false)
        document.dispatchEvent(new Event('visibilitychange'))
        // Immediately on resume — the elapsed background time must not be
        // replayed as a burst of frames.
        await vi.advanceTimersByTimeAsync(16)
        expect(painted.length, 'no catch-up burst on resume').toBe(hiddenSince)

        await vi.advanceTimersByTimeAsync(FRAME_MS + 16)
        expect(painted.length).toBeGreaterThan(hiddenSince)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
        setHidden(false)
      }
    })

    it('is fully torn down on exit: no further paints, and the zoom listener is gone', async () => {
      vi.useFakeTimers()
      try {
        const asked = []
        const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))

        ui.exit.click()
        await vi.advanceTimersByTimeAsync(0)
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS * 5)
        expect(painted.length, 'no further paints after exit').toBe(after)

        const askedBefore = asked.length
        map.setZoom(2)
        await vi.advanceTimersByTimeAsync(0)
        expect(asked.length, 'zoom listener removed on exit').toBe(askedBefore)
      } finally {
        vi.useRealTimers()
      }
    })
  })

  // A single rAF tick can carry an arbitrary delta — a long GC pause, a bfcache
  // restore — and the fake clock cannot produce one, since its rAF fires on its
  // own 16ms grid. Driving the callback by hand is the only way to hand the
  // clock one enormous tick.
  function manualClock(start = 100000) {
    const realRAF = globalThis.requestAnimationFrame
    const realCAF = globalThis.cancelAnimationFrame
    const nowSpy = vi.spyOn(performance, 'now').mockReturnValue(start)
    let at = start
    let cb = null
    globalThis.requestAnimationFrame = (fn) => { cb = fn; return 1 }
    globalThis.cancelAnimationFrame = () => { cb = null }
    return {
      tick(delta) {
        at += delta
        const fn = cb
        cb = null
        fn?.(at)
      },
      restore() {
        globalThis.requestAnimationFrame = realRAF
        globalThis.cancelAnimationFrame = realCAF
        nowSpy.mockRestore()
      },
    }
  }

  describe('the catch-up clamp', () => {
    const LONG = {
      metric: 'P2', resolution_km: 15, cells: [[23, 42]],
      frames: Array.from({ length: 40 }, (_, i) => ({
        t: new Date(Date.UTC(2026, 8, 8, 6 + i)).toISOString(), v: [i + 1],
      })),
    }

    const playing = async () => {
      const h = harness(async () => LONG, T)
      h.ui.button.click()
      await vi.waitFor(() => expect(h.painted.length).toBe(1))
      return h
    }

    // The playhead, not the paint count: one tick advances as many frames as
    // elapsed but paints only the frame that composites, so counting setData
    // would no longer measure the clamp.
    const at = (ui) => Number(ui.slider.value)

    it('replays at most four frames after a long stall', async () => {
      const clock = manualClock()
      try {
        const { ui } = await playing()
        const after = at(ui)
        clock.tick(FRAME_MS * 30)
        expect(at(ui) - after, 'a 30-frame backlog must not replay whole').toBe(4)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })

    // The near miss: the clamp must not cost a normally-paced tick its frame,
    // and a tick landing exactly on the limit is still a tick the reader saw.
    it('leaves a normal tick and an exactly-four-frame tick alone', async () => {
      const clock = manualClock()
      try {
        const { ui } = await playing()
        let after = at(ui)
        clock.tick(FRAME_MS)
        expect(at(ui) - after, 'a normal tick advances one frame').toBe(1)
        after = at(ui)
        clock.tick(FRAME_MS * 4)
        expect(at(ui) - after, 'exactly at the limit still advances four').toBe(4)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })

    // Four synchronous paints in one animation frame are four hexFeatures
    // builds and four setData calls, of which only the last ever composites.
    it('paints once however many frames one tick swallows', async () => {
      const clock = manualClock()
      try {
        const { painted, ui } = await playing()
        const wasAt = at(ui)
        const wasPainted = painted.length
        clock.tick(FRAME_MS * 30)
        expect(at(ui) - wasAt, 'the playhead still advanced four').toBe(4)
        expect(painted.length - wasPainted, 'one setData for the one frame shown').toBe(1)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })
  })

  // A digit that appears where there was none pulls the eye to the arrival
  // rather than to the value. It must ramp up instead of popping.
  describe('late joiners', () => {
    // A always reports, B joins at frame 1, C goes silent at frame 1 and so is
    // held (carried) there before reporting again.
    const JOINERS = {
      metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42], [23.4, 42]],
      frames: [
        { t: '2026-09-08T06:00:00Z', v: [10, null, 30] },
        { t: '2026-09-08T07:00:00Z', v: [10, 20, null] },
        { t: '2026-09-08T08:00:00Z', v: [10, 20, 30] },
        { t: '2026-09-08T09:00:00Z', v: [10, 20, 30] },
      ],
    }
    const A = 10
    const B = 20
    const C = 30

    const cell = (frame, value) => frame.features.find((f) => f.properties.value === value)?.properties

    // Evaluates the layer's own 'case' expression: these tests are about what a
    // cell is DRAWN at, not only about what it is tagged with.
    const opacityOf = (expr, props) => {
      for (let i = 1; i < expr.length - 1; i += 2) {
        const [, [, key], want] = expr[i]
        if ((props[key] ?? null) === want) return expr[i + 1]
      }
      return expr[expr.length - 1]
    }

    // delay is the tick size: reduced motion floors the frame delay at 0.25x,
    // so a FRAME_MS tick would advance nothing there.
    const play = async (frames, delay = FRAME_MS) => {
      const clock = manualClock()
      const { painted, ui } = harness(async () => JOINERS, T)
      ui.button.click()
      await vi.waitFor(() => expect(painted.length).toBe(1))
      for (let i = 1; i < frames; i += 1) clock.tick(delay)
      // Snapshot before exit: leaving the player repaints the live grid, and
      // that paint is not one of the replay's frames.
      const replayed = painted.slice()
      ui.exit.click()
      clock.restore()
      return replayed
    }

    it('ramps a newly arrived cell up over two frames, then settles it', async () => {
      const painted = await play(4)
      expect(cell(painted[1], B).fresh, 'the frame B arrives on').toBe(0)
      expect(cell(painted[2], B).fresh, 'one frame later, no longer freshly arrived').toBe(1)
      expect(cell(painted[3], B).fresh, 'settled').toBeUndefined()
    })

    // A catch-up tick advances several frames and paints one. The arrival
    // tracking has to describe that painted frame: a cell whose first reading
    // fell in the swallowed span is new to the reader, who never saw the frames
    // it arrived on.
    it('fades a cell in that first appeared inside a skipped span', async () => {
      const LATE = {
        metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42]],
        frames: [
          { t: '2026-09-08T06:00:00Z', v: [10, null] },
          { t: '2026-09-08T07:00:00Z', v: [10, 20] },
          { t: '2026-09-08T08:00:00Z', v: [10, 20] },
          { t: '2026-09-08T09:00:00Z', v: [10, 20] },
        ],
      }
      const clock = manualClock()
      try {
        const { painted, ui } = harness(async () => LATE, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBe(1))
        // One tick worth three frames: frames 1 and 2 are never composited.
        clock.tick(FRAME_MS * 3)
        expect(Number(ui.slider.value), 'the playhead is on frame 3').toBe(3)
        expect(cell(painted.at(-1), B).fresh, 'new to the reader on the frame shown').toBe(0)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })

    it('never marks a cell that reported in both frames', async () => {
      const painted = await play(4)
      for (const frame of painted) expect(cell(frame, A).fresh).toBeUndefined()
    })

    // The opening frame is the start of the story, not an arrival: fading the
    // whole map in on every press of play is the pop this task is about, moved.
    it('treats the opening frame as settled, not as a mass arrival', async () => {
      const painted = await play(1)
      expect(cell(painted[0], A).fresh).toBeUndefined()
      expect(cell(painted[0], C).fresh).toBeUndefined()
    })

    it('keeps a carried cell muted and never treats it as fresh', async () => {
      const painted = await play(3)
      const held = cell(painted[1], C)
      expect(held.carried, 'C is held on frame 1').toBe(true)
      expect(held.fresh).toBeUndefined()
      expect(opacityOf(hexLabelPaint({})['text-opacity'], held)).toBe(CARRIED_OPACITY)
      expect(cell(painted[2], C).fresh, 'a held cell reporting again is not an arrival').toBeUndefined()
    })

    // Scrubbing back to before a cell's first reading and forward past its gap
    // is the one way a held cell can meet a previous frame that never drew it.
    // Without the guard it would be tagged as an arrival and drawn brighter
    // than the held reading it is.
    it('keeps a held cell held when the reader scrubs back past its first hour', async () => {
      const LATE = {
        metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42]],
        frames: [
          { t: '2026-09-08T06:00:00Z', v: [10, null] },
          { t: '2026-09-08T07:00:00Z', v: [10, 7] },
          { t: '2026-09-08T08:00:00Z', v: [10, null] },
        ],
      }
      const { painted, ui } = harness(async () => LATE, T)
      ui.button.click()
      await vi.waitFor(() => expect(painted.length).toBe(1))
      const scrub = (i) => {
        ui.slider.value = String(i)
        ui.slider.dispatchEvent(new Event('input'))
      }
      scrub(0)
      scrub(2)
      const held = cell(painted.at(-1), 7)
      expect(held.carried, 'the late cell is held on the last hour').toBe(true)
      expect(held.fresh).toBeUndefined()
      expect(opacityOf(hexLabelPaint({})['text-opacity'], held)).toBe(CARRIED_OPACITY)
    })

    // Belt and braces with the guard above: even handed a feature tagged both
    // ways, the expression must draw it as held rather than as an arrival.
    it('draws a cell tagged both ways as held', () => {
      expect(opacityOf(hexLabelPaint({})['text-opacity'], { carried: true, fresh: 0 })).toBe(CARRIED_OPACITY)
    })

    it('ramps through the opacities the layer draws', async () => {
      const painted = await play(3)
      const expr = hexLabelPaint({})['text-opacity']
      expect(opacityOf(expr, cell(painted[1], B))).toBe(FRESH_OPACITY)
      expect(opacityOf(expr, cell(painted[2], B))).toBe(SETTLING_OPACITY)
      expect(opacityOf(expr, cell(painted[1], A))).toBe(1)
    })

    // Reduced motion gets the end state at once — a slower ramp is still a
    // ramp, and app.css suppresses transitions outright under the same query.
    it('draws an arrival at full opacity under reduced motion', async () => {
      globalThis.matchMedia = vi.fn((q) => ({ media: q, matches: true }))
      try {
        const painted = await play(3, FRAME_MS * 4)
        for (const frame of painted) {
          for (const f of frame.features) expect(f.properties.fresh).toBeUndefined()
        }
        expect(opacityOf(hexLabelPaint({})['text-opacity'], cell(painted[1], B))).toBe(1)
      } finally {
        delete globalThis.matchMedia
      }
    })
  })

  // Twenty-eight blank frames under a running clock read as clean air, not as
  // missing data. Saying so is the whole point of the guard.
  it('refuses to animate a metric with no history, and says why', async () => {
    const { painted, ui } = harness(async () => NO_HISTORY, T)

    ui.button.click()
    await vi.waitFor(() => expect(ui.note.textContent).toBe('Not enough history yet'))
    expect(painted).toEqual([])
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
  })

  // Not skipped and not frozen: the gap is the story, so the frame draws and
  // the caption says the hour is thin rather than letting it read as clean air.
  it('captions a thin frame and clears the caption on a full one', async () => {
    vi.useFakeTimers()
    try {
      const { ui } = harness(async () => PATCHY, T)
      ui.button.click()
      await vi.waitFor(() => expect(ui.clock.textContent).not.toBe(''))
      expect(ui.note.textContent).toBe('')

      await vi.advanceTimersByTimeAsync(FRAME_MS)
      expect(ui.note.textContent).toBe('Partial data for this hour')

      await vi.advanceTimersByTimeAsync(FRAME_MS)
      expect(ui.note.textContent).toBe('')
      ui.exit.click()
    } finally {
      vi.useRealTimers()
    }
  })

  // The silent hour is drawn at the previous hour's readings rather than
  // dropping its digits, which is what made the replay look like numbers
  // blinking on and off at random.
  it('holds a silent cell at its last reading, marked as carried', async () => {
    vi.useFakeTimers()
    try {
      const { painted, ui } = harness(async () => GAPPY, T)
      ui.button.click()
      await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
      await vi.advanceTimersByTimeAsync(FRAME_MS)

      const drawn = painted.map((p) => p.features).filter((fs) => fs.length > 0)
      const gap = drawn.find((fs) => fs.every((f) => f.properties.carried === true))
      expect(gap, 'a frame drawn entirely from carried readings').toBeDefined()
      expect(gap.map((f) => f.properties.value).sort()).toEqual([1, 2, 3, 4])
      ui.exit.click()
    } finally {
      vi.useRealTimers()
    }
  })

  it('clears the caption on the way out', async () => {
    const { ui } = harness(async () => PATCHY, T)
    ui.button.click()
    await vi.waitFor(() => expect(ui.clock.textContent).not.toBe(''))
    ui.onscrub.length
    ui.slider.value = '1'
    ui.slider.dispatchEvent(new Event('input'))
    expect(ui.note.textContent).toBe('Partial data for this hour')

    ui.exit.click()
    await vi.waitFor(() => expect(ui.note.textContent).toBe(''))
  })

  // A visitor who never presses play must not pay for the history.
  it('fetches nothing until the button is pressed', async () => {
    const asked = []
    const { ui } = harness(async (url) => { asked.push(url); return BODY })
    expect(asked).toEqual([])

    ui.button.click()
    await vi.waitFor(() => expect(asked).toHaveLength(1))
    expect(asked[0]).toContain('/api/v1/timelapse')
    ui.button.click()
  })

  it('paints the frame it is on, and says which hour that is', async () => {
    const { painted, ui } = harness(async () => BODY)

    ui.button.click()
    await vi.waitFor(() => expect(painted).toHaveLength(1))
    expect(painted[0].features).toHaveLength(1)
    expect(painted[0].features[0].properties.value).toBe(10)
    expect(ui.clock.textContent).not.toBe('')
    ui.button.click()
  })

  // The frame on screen is a past hour's. Pressing stop must put the live grid
  // back, which only happens if the dedup key refreshHexes holds is cleared.
  it('goes back to the live grid when stopped', async () => {
    const LIVE = { resolution_km: 15, hexes: [{ lon: 23, lat: 42, values: { P2: 99 } }] }
    const fetchJSON = async (url) => (url.includes('timelapse') ? BODY : LIVE)
    const { ui, painted, state, map } = harness(fetchJSON)

    // The live grid FIRST, so refreshHexes is holding a dedup key by the time
    // the animation runs. Without that key being cleared on stop, the second
    // call sees the URL it already fetched and repaints nothing — leaving a
    // past hour on screen under a map that says it is showing now.
    await refreshHexes(map, state, cfg, fetchJSON)
    ui.button.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(10))

    ui.button.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(99))
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
  })

  // Dragging is a request to look at one hour; leaving the timer running would
  // move the map off it a third of a second later.
  it('stops playing when the reader scrubs', async () => {
    const { ui, painted } = harness(async () => BODY)

    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    ui.slider.value = '1'
    ui.slider.dispatchEvent(new Event('input'))

    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
    expect(painted.at(-1).features[0].properties.value).toBe(20)
  })

  // Scrubbing pauses without restoring, so a reader who drags the slider and
  // stops is left on a past hour. The exit is the only way back to now from
  // there: pressing play again would replay, not return.
  it('goes back to the live grid from a scrubbed frame', async () => {
    const LIVE = { resolution_km: 15, hexes: [{ lon: 23, lat: 42, values: { P2: 99 } }] }
    const fetchJSON = async (url) => (url.includes('timelapse') ? BODY : LIVE)
    const { ui, painted, state, map } = harness(fetchJSON)

    await refreshHexes(map, state, cfg, fetchJSON)
    ui.button.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(10))

    ui.slider.value = '1'
    ui.slider.dispatchEvent(new Event('input'))
    expect(painted.at(-1).features[0].properties.value).toBe(20)

    ui.exit.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(99))
  })

  // Leaving the animation collapses the control back to the play button: a
  // scrubber left on screen over a live map is a control with nothing behind it.
  // The held body survives, so coming back costs no second fetch.
  it('collapses the scrubber on exit, and reopens it without a refetch', async () => {
    const asked = []
    const LIVE = { resolution_km: 15, hexes: [] }
    const { ui } = harness(async (url) => {
      asked.push(url)
      return url.includes('timelapse') ? BODY : LIVE
    })

    ui.button.click()
    await vi.waitFor(() => expect(ui.slider.hidden).toBe(false))
    expect(ui.exit.hidden).toBe(false)

    ui.exit.click()
    await vi.waitFor(() => expect(ui.slider.hidden).toBe(true))
    expect(ui.exit.hidden).toBe(true)
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')

    ui.button.click()
    await vi.waitFor(() => expect(ui.slider.hidden).toBe(false))
    expect(asked.filter((u) => u.includes('timelapse'))).toHaveLength(1)
    ui.button.click()
  })

  // A different window is a different animation: keeping the old body would
  // replay the day while the map claimed to be showing the week.
  it('drops what it holds on a reset, and refetches after', async () => {
    const asked = []
    const { ui, ctl } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    await vi.waitFor(() => expect(ui.button.getAttribute('aria-pressed')).toBe('true'))
    await ctl.reset()
    expect(ui.slider.hidden).toBe(true)

    ui.button.click()
    await vi.waitFor(() => expect(asked.filter((u) => u.includes('timelapse'))).toHaveLength(2))
    ui.button.click()
  })

  // The live grid asks for the tier its zoom draws at; the replay must ask for
  // the same one, or the hexes it swaps in are a different size from the ones
  // still on screen a moment before.
  it('asks for the tier matching the current zoom', async () => {
    const asked = []
    const { ui } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    await vi.waitFor(() => expect(asked).toHaveLength(1))
    const url = new URL(asked[0], 'http://x')
    expect(Number(url.searchParams.get('resolution_km'))).toBeCloseTo(resolutionForZoom(12), 4)
    ui.button.click()
  })

  // A zoom-to-street flight fires many zoom events on the way; only the tier
  // it lands on is worth a request.
  it('follows the reader onto the tier for the new zoom', async () => {
    const asked = []
    const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    // Waits on the paint, not the fetch: the paint happens after the player
    // is marked open, and it is openness the zoom handler below needs.
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    const before = painted.length

    map.setZoom(2)
    await vi.waitFor(() => expect(asked).toHaveLength(2))
    const url = new URL(asked[1], 'http://x')
    expect(Number(url.searchParams.get('resolution_km'))).toBeCloseTo(resolutionForZoom(2), 4)
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(before))
    ui.button.click()
  })

  // Rounded the same way hexesURL rounds it: a fraction of a zoom level is not
  // a new tier, and refetching for it would turn a smooth zoom into a burst.
  it('does not refetch or repaint when zooming within the same tier', async () => {
    const asked = []
    const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    // On the paint, not the fetch: the fetch resolves before the player is
    // marked open, and a zoom while it is still closed proves nothing.
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    const before = painted.length

    map.setZoom(12.4)
    await Promise.resolve()
    await Promise.resolve()
    expect(asked).toHaveLength(1)
    // Still playing here, so this is what pins the same-tier dedup itself
    // rather than the paused guard: FRAME_MS is far longer than two microtasks.
    expect(painted).toHaveLength(before)
    ui.button.click()
  })

  // Nor repaint. A flyTo fires a zoom event per frame; feeding the source the
  // body it already holds sixty times a second is the jank the dedup prevents.
  it('does not repaint when zooming within the same tier', async () => {
    const { ui, painted, map } = harness(async () => BODY)

    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    // Pause restores the live grid, which is itself a paint — settle it first,
    // or the repaint being measured is that one.
    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(1))
    const before = painted.length

    map.setZoom(12.4)
    await Promise.resolve()
    await Promise.resolve()
    expect(painted).toHaveLength(before)
  })

  // A reader watching the animation should not be thrown back to the first
  // hour because the tier under them changed.
  // Fake timers, because the assertion is about a specific frame: with the real
  // interval the playhead would have stepped on before the zoom was measured.
  it('keeps the playhead where it was when the tier changes', async () => {
    vi.useFakeTimers()
    try {
      const { ui, painted, map } = harness(async () => BODY)

      ui.button.click()
      await vi.advanceTimersByTimeAsync(FRAME_MS)
      expect(ui.slider.value).toBe('1')
      const before = painted.length

      map.setZoom(2)
      await vi.advanceTimersByTimeAsync(0)
      expect(painted.length).toBeGreaterThan(before)
      expect(ui.slider.value).toBe('1')
      ui.button.click()
    } finally {
      vi.useRealTimers()
    }
  })

  // Pause puts the live grid back on screen. A zoom is not a press of play, so
  // it must not drag a replay frame back over it — the new tier is fetched and
  // held, and the next press of play or drag of the scrubber draws it.
  it('does not redraw a frame over the live grid while paused', async () => {
    const asked = []
    const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(1))
    const before = painted.length

    map.setZoom(2)
    await vi.waitFor(() => expect(asked.filter((u) => u.includes('timelapse'))).toHaveLength(2))
    await Promise.resolve()
    expect(painted).toHaveLength(before)
  })

  // A zoom event before the button has ever been pressed must not fetch —
  // that is the live grid's job, not the replay's.
  it('ignores a zoom before the player has ever been opened', async () => {
    const asked = []
    const { map } = harness(async (url) => { asked.push(url); return BODY })

    map.setZoom(2)
    await Promise.resolve()
    expect(asked).toEqual([])
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

// The style is now raster-only. The self-hosted vector archive is a BULGARIA
// extract whose opaque land/water fills painted a rectangle over the world
// raster beneath it — the Danube stopped at Silistra, the ground changed
// colour at the border, and the Black Sea went unnamed.
describe('mapStyle', () => {
  const cfg = { basemap: 'https://tiles.airbg.org/style.json', emptyBasemapColour: '#eef2f5' }

  it('draws the world raster and no vector layer at all', () => {
    const style = mapStyle(cfg)
    const raster = style.layers.filter((l) => l.type === 'raster')

    expect(raster).toHaveLength(1)
    expect(style.sources[raster[0].source].tiles[0]).toContain('tile.openstreetmap.org')
    expect(style.sources[raster[0].source].attribution).toContain('OpenStreetMap')
    for (const l of style.layers) expect(l.type).not.toBe('fill')
  })

  it('paints the backing colour from cfg.emptyBasemapColour, not another field', () => {
    const style = mapStyle({ ...cfg, noDataColour: '#9ca3af' })
    const bg = style.layers.find((l) => l.type === 'background')
    expect(bg.paint['background-color']).toBe('#eef2f5')
  })

  it('puts the raster over the backing colour, which would otherwise cover it', () => {
    const ids = mapStyle(cfg).layers.map((l) => l.type)
    expect(ids.indexOf('background')).toBeLessThan(ids.indexOf('raster'))
  })

  // A raster-only style has no glyphs of its own. Without one MapLibre draws
  // no symbol layer at all: no marker labels, no cell values, no wind arrows.
  it('keeps a glyphs endpoint so the symbol layers still have letters', () => {
    expect(mapStyle(cfg).glyphs).toBe('https://tiles.airbg.org/glyphs/{fontstack}/{range}.pbf')
  })

  it('omits glyphs rather than inventing one when no basemap is configured', () => {
    expect(mapStyle({ basemap: '', emptyBasemapColour: '#eef2f5' }).glyphs).toBeUndefined()
  })
})

// The vector archive is a BULGARIA extract. Its background and fill layers are
// opaque polygons clipped to the extract's rectangle, so over the world raster
// they painted a box across it — the Danube ending at Silistra, the ground
// changing colour at the border, the Black Sea unnamed. Every one of those is a
// FILLED layer; the traced ones draw only what they trace and let the raster
// through, which is how the POI categories come back without the box.
describe('overlayLayers', () => {
  const style = {
    sources: { basemap: { type: 'vector' }, unused: { type: 'vector' } },
    layers: [
      { id: 'bg', type: 'background', source: undefined },
      { id: 'water', type: 'fill', source: 'basemap' },
      { id: 'roads', type: 'line', source: 'basemap' },
      { id: 'poi-shop-name', type: 'symbol', source: 'basemap' },
      { id: 'poi-shop', type: 'circle', source: 'basemap' },
    ],
  }

  it('drops every filled layer and keeps every traced one', () => {
    expect(overlayLayers(style).layers.map((l) => l.id))
      .toEqual(['roads', 'poi-shop-name', 'poi-shop'])
  })

  it('keeps the sources the surviving layers need, and no others', () => {
    expect(Object.keys(overlayLayers(style).sources)).toEqual(['basemap'])
  })

  it('has nothing to say about a style it was handed nothing of', () => {
    expect(overlayLayers(undefined)).toEqual({ sources: {}, layers: [] })
  })
})

describe('addBasemapOverlay', () => {
  const fakeMap = () => {
    const calls = { sources: [], layers: [] }
    return {
      calls,
      getSource: () => undefined,
      getLayer: (id) => (id === 'airbg-hex-fill' ? { id } : undefined),
      addSource: (id, s) => calls.sources.push([id, s]),
      addLayer: (l, before) => calls.layers.push([l.id, before]),
    }
  }
  const style = {
    sources: { basemap: { type: 'vector' } },
    layers: [{ id: 'water', type: 'fill', source: 'basemap' }, { id: 'roads', type: 'line', source: 'basemap' }],
  }

  // Under the readings, never over: a POI label drawn on top of a value is the
  // ground obscuring the thing the page exists to show.
  it('slots the traced layers beneath the grid', async () => {
    const map = fakeMap()
    await addBasemapOverlay(map, 'https://tiles.airbg.org/style.json', 'airbg-hex-fill', async () => style)

    expect(map.calls.sources).toEqual([['basemap', { type: 'vector' }]])
    expect(map.calls.layers).toEqual([['roads', 'airbg-hex-fill']])
  })

  it('adds them on top when the grid is not there to sit under', async () => {
    const map = { ...fakeMap(), getLayer: () => undefined }
    map.calls = { sources: [], layers: [] }
    map.addSource = (id, s) => map.calls.sources.push([id, s])
    map.addLayer = (l, before) => map.calls.layers.push([l.id, before])
    await addBasemapOverlay(map, 'https://x/style.json', 'airbg-hex-fill', async () => style)
    expect(map.calls.layers).toEqual([['roads', undefined]])
  })

  // The ground is optional detail; the map is not. An unreachable archive must
  // leave the raster and every reading standing.
  it('leaves the raster alone when the style cannot be fetched', async () => {
    const map = fakeMap()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await expect(addBasemapOverlay(map, 'https://x/style.json', 'airbg-hex-fill', async () => {
      throw new Error('502')
    })).resolves.toBeUndefined()
    expect(map.calls.layers).toEqual([])
    warn.mockRestore()
  })

  it('does nothing at all when no basemap is configured', async () => {
    const map = fakeMap()
    await addBasemapOverlay(map, '', 'airbg-hex-fill', async () => style)
    expect(map.calls.sources).toEqual([])
  })
})

// The archive is referenced as pmtiles://, which MapLibre cannot read until the
// protocol is registered — and addProtocol is global module state, so a second
// registration for the same scheme silently replaces the first.
describe('registerProtocols', () => {
  it('registers pmtiles exactly once across repeated calls', () => {
    const seen = []
    const add = (scheme, fn) => seen.push([scheme, typeof fn])
    registerProtocols(add)
    registerProtocols(add)
    expect(seen).toEqual([['pmtiles', 'function']])
  })
})

describe('glyphsURL', () => {
  it('replaces the style file with the font endpoint, keeping the path', () => {
    expect(glyphsURL('https://tiles.airbg.org/maps/style.json'))
      .toBe('https://tiles.airbg.org/maps/glyphs/{fontstack}/{range}.pbf')
  })

  // new URL() percent-encodes the braces into %7Bfontstack%7D, which MapLibre
  // then requests literally and gets a 404 for. The template must survive.
  it('leaves the braces MapLibre substitutes into unencoded', () => {
    expect(glyphsURL('https://tiles.airbg.org/style.json')).toContain('{fontstack}/{range}')
    expect(glyphsURL('https://tiles.airbg.org/style.json')).not.toContain('%7B')
  })

  it('has nothing to derive from when no basemap is configured', () => {
    expect(glyphsURL('')).toBeNull()
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

  // The markers are on the same scale as the hexes. A `step` expression here
  // would leave the dots painted in categories over a blended choropleth —
  // two answers to the same question, on the same screen.
  it('blends the markers rather than snapping them to a band', () => {
    const bands = [{ upper: 10, colour: '#00ff00' }, { upper: null, colour: '#ff0000' }]
    const paint = markerPaint(bands, { noDataColour: '#999999', unscaledColour: '#94a3b8', scaled: true })
    const ramp = paint[3]
    expect(ramp[0]).toBe('interpolate')
    // A stop at every band midpoint AND every boundary — strictly more than
    // one per band, which is all a step expression would need.
    const values = ramp.slice(3).filter((_, i) => i % 2 === 0)
    expect(values.length).toBeGreaterThan(bands.length)
    expect(values).toEqual([...values].sort((a, b) => a - b))
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

// Real fullscreen renders the frame and nothing else. The key is anchored to
// the shell — deliberately, so a wide window does not float it over the page —
// which meant going full screen took the colour key off the map, on the one
// view where the map is all there is.
// "Hide the basemap" must take down the ground and leave the readings standing.
// It used to walk every layer carrying an airbg:group — a marker the vector
// style set and the raster-only style cannot: the toggle reported itself on and
// hid nothing.
// Mounted in mountChrome and not in mount(), which is what puts it on both maps
// that carry this island — the home page and an area page — from one call.
describe('the averaging selector', () => {
  it('is mounted on the frame with the server-rendered words', () => {
    const el = document.createElement('div')
    el.dataset.tWindowLabel = 'Averaging period'
    el.dataset.tWindows = 'Now,Last 24 hours,Last 48 hours,Last week'
    document.body.appendChild(el)

    const { windowMenu } = mountChrome(el, readConfig(el))
    expect(windowMenu, 'no window menu in the chrome').toBeTruthy()
    expect(windowMenu.root.parentElement).toBe(el)
    expect(windowMenu.button.getAttribute('aria-label')).toBe('Averaging period')
    const radios = [...windowMenu.panel.querySelectorAll('input[type="radio"]')]
    expect(radios.map((r) => r.value)).toEqual(['', '24h', '48h', '7d'])
    expect(radios.map((r) => r.nextElementSibling.textContent))
      .toEqual(['Now', 'Last 24 hours', 'Last 48 hours', 'Last week'])
  })
})

describe('the basemap toggle', () => {
  it('hides the ground, raster and vector detail alike, and no reading', () => {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const { layerViews } = mountChrome(el, readConfig(el))
    const basemap = layerViews.find((v) => v.id === 'basemap')
    expect(basemap, 'no basemap view in the layers menu').toBeTruthy()

    const set = []
    const map = {
      setLayoutProperty: (...a) => set.push(a),
      // The ground is the raster PLUS the vector detail over it. Every layer
      // carrying an airbg:group came from the vector style; the readings carry
      // none, and must be left standing.
      getStyle: () => ({ layers: [
        { id: 'poi-shop', metadata: { 'airbg:group': 'poi-shop' } },
        { id: 'airbg-hex-fill' },
      ] }),
    }
    basemap.apply(false, map)

    expect(set).toEqual([
      ['airbg-raster-base', 'visibility', 'none'],
      ['poi-shop', 'visibility', 'none'],
    ])
  })
})

// The disclosure is why an unmeasured forecast layer is allowed on a map of
// measurements, so it is never dismissible — but two sentences and a model name
// unrolled over the map is most of a phone screen. Folded, it is a line the
// reader can open.
describe('the wind disclosure', () => {
  // Scoped to its own frame: the disclosure is one of two <details> the chrome
  // builds, and a document-wide query would find whichever came first.
  const chrome = () => {
    const el = document.createElement('div')
    el.className = 'map'
    el.dataset.tWindAbout = 'About the wind layer'
    document.body.appendChild(el)
    return { el, ...mountChrome(el, readConfig(el)) }
  }

  it('arrives folded, with the full text inside it', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const note = c.el.querySelector('.map-wind-label')
    expect(note.tagName).toBe('DETAILS')
    expect(note.open).toBe(false)
    expect(note.hidden).toBe(false)
    expect(note.textContent).toContain('Wind forecast · valid now')
  })

  it('names itself on the summary, so a folded line still says what it is', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const summary = c.el.querySelector('.map-wind-label summary')
    expect(summary).toBeTruthy()
    expect(summary.textContent.trim()).not.toBe('')
  })

  it('goes away with the arrows, and comes back folded', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const note = c.el.querySelector('.map-wind-label')
    note.open = true
    c.showWind(false, '')
    expect(note.hidden).toBe(true)
    c.showWind(true, 'Wind forecast · valid now')
    expect(note.open).toBe(false)
  })
})

// A sensor that has stopped reporting still has a cell on the grid, drawn in
// the no-data colour. At country zoom that is most of what a reader sees on a
// bad day for the network, and it reads as "nothing here" rather than "nobody
// is measuring here". The status filter already governed the markers; the grid
// is the tier that actually covers the country, so it is governed too.
describe('the inactive-sensors toggle', () => {
  const hexCfg = { metric: 'P2', noDataColour: '#cccccc' }
  const scales = [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }, { upper: null, colour: '#ff0000' }] }]
  const mixed = {
    resolution_km: 1,
    hexes: [
      { lon: 23.32, lat: 42.65, n: 1, values: { P2: 5 } },
      { lon: 23.34, lat: 42.66, n: 0, values: {} },
    ],
  }

  function hexMap() {
    const painted = []
    return {
      painted,
      getZoom: () => 12,
      getBounds: () => ({ getWest: () => 23.3, getSouth: () => 42.6, getEast: () => 23.4, getNorth: () => 42.7 }),
      getSource: (id) => (id === 'airbg-hexes' ? { setData: (d) => painted.push(d) } : undefined),
    }
  }

  afterEach(() => resetSensorFilterForTests())

  it('leaves the silent cells off the grid by default', async () => {
    const map = hexMap()
    await refreshHexes(map, { scales, hexUrl: null, hexBody: null }, hexCfg, async () => mixed)

    const values = map.painted[0].features.map((f) => f.properties.value)
    expect(values).toEqual([5])
  })

  it('draws them once the reader asks for them', async () => {
    setSensorStatus('all')
    const map = hexMap()
    await refreshHexes(map, { scales, hexUrl: null, hexBody: null }, hexCfg, async () => mixed)

    const values = map.painted[0].features.map((f) => f.properties.value)
    expect(values).toHaveLength(2)
    expect(values).toContain(null)
  })

  it('offers the option unticked, and flips the shared status', () => {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const { layerViews } = mountChrome(el, readConfig(el))
    const view = layerViews.find((v) => v.id === 'inactiveSensors')
    expect(view, 'no inactive-sensors view in the layers menu').toBeTruthy()

    // Unticked on arrival: the default is the quieter map.
    expect(view.defaultOff).toBe(true)
    view.apply(true)
    expect(getSensorStatus()).toBe('all')
    view.apply(false)
    expect(getSensorStatus()).toBe('active')
  })
})

describe('mountChrome() keeps the key on the map in fullscreen', () => {
  const chromeFrame = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)
    return { shell, el }
  }

  it('moves the key into the frame and back out again', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')
    expect(legend, 'no key on the shell').toBeTruthy()

    el.querySelector('.map__full').click()
    expect(legend.parentElement, 'the key stayed outside the fullscreen frame').toBe(el)

    el.querySelector('.map__full').click()
    expect(legend.parentElement).toBe(shell)
  })

  it('leaves it a details, so it can still be folded away', () => {
    const { el } = chromeFrame()
    mountChrome(el, readConfig(el))
    el.querySelector('.map__full').click()

    const legend = el.querySelector('details.scale')
    expect(legend.tagName).toBe('DETAILS')
    expect(legend.open, 'the key came back folded shut').toBe(true)
  })
})

// The fold and the layers-menu option are two different controls: the menu says
// whether there is a key at all, the triangle says whether it is unrolled. A
// fold that forgets is the one that reads as broken — the reader folds the key
// away, reloads, and it is back over the map.
describe('mountChrome() remembers whether the key is folded', () => {
  const chromeFrame = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)
    return { shell, el }
  }

  // This jsdom has no localStorage of its own, so the seam is stubbed rather
  // than cleared — which also proves the code reaches for the real one.
  let store
  beforeEach(() => {
    store = new Map()
    vi.stubGlobal('localStorage', {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    })
  })
  // Left stubbed on purpose: vi.unstubAllGlobals would also drop the fetch stub
  // the later suites install, and an empty store reads exactly like the absent
  // localStorage this jsdom otherwise has.

  it('opens the key on a first visit', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    expect(shell.querySelector('details.scale').open).toBe(true)
  })

  it('records the fold when the reader closes it', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')

    legend.open = false
    legend.dispatchEvent(new Event('toggle'))

    expect(store.get(LEGEND_FOLD_KEY)).toBe('false')
  })

  it('opens folded on the next visit, and records the reopening', () => {
    store.set(LEGEND_FOLD_KEY, 'false')
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')
    expect(legend.open, 'the key ignored the remembered fold').toBe(false)

    legend.open = true
    legend.dispatchEvent(new Event('toggle'))
    expect(store.get(LEGEND_FOLD_KEY)).toBe('true')
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

  // With the grid on at every zoom the country is visible at, the area markers
  // are hidden — so a cell is the only thing left to click, and it has to be
  // the way into a province. It resolves to the nearest area centroid, the same
  // rule the locate button navigates by.
  describe('cellArea', () => {
    const areas = [
      { slug: 'sofia', lon: 23.32, lat: 42.7 },
      { slug: 'varna', lon: 27.91, lat: 43.2 },
    ]

    it('selects the area the click falls nearest', () => {
      expect(cellArea({ areas }, { lng: 27.8, lat: 43.1 })).toBe('varna')
      expect(cellArea({ areas }, { lng: 23.4, lat: 42.6 })).toBe('sofia')
    })

    it('selects nothing on a map already scoped to one area', () => {
      expect(cellArea({ areas, slug: 'sofia' }, { lng: 27.8, lat: 43.1 })).toBeNull()
    })

    it('selects nothing before the area list has loaded', () => {
      expect(cellArea({ areas: [] }, { lng: 27.8, lat: 43.1 })).toBeNull()
      expect(cellArea({}, { lng: 27.8, lat: 43.1 })).toBeNull()
    })

    it('selects nothing for a click with no position', () => {
      expect(cellArea({ areas }, undefined)).toBeNull()
    })
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

  it('gives the sensor dots the whole grid range, since no cell replaces them', () => {
    // The sensors tier IS the point tier's own data: dot and cell carry the
    // same one device, so the dots run to the changeover rather than stopping
    // at the aggregate one.
    expect(markerMaxZoom('sensors')).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
    expect(markerMaxZoom('country')).toBe(GRID_MIN_ZOOM_FRACTIONAL)
    expect(markerMaxZoom('municipality')).toBe(GRID_MIN_ZOOM_FRACTIONAL)
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

  it('draws the cell borders in an ink that shows against a pale street map', () => {
    // The outline used to be ['get', 'colour'] — the fill's own value colour —
    // so every border vanished into the cell it bounded and the grid read as a
    // smear. It is now the marker stroke, the same edge the dots carry.
    const paint = hexOutlinePaint({ labelColour: '#111827', markerStrokeColour: '#ffffff' })
    expect(paint['line-color']).toBe('#111827')
    // Not the marker stroke either: white lifts a dot off a dark map and
    // vanishes into a pale one, which over the OSM raster left the cells with
    // no visible edge at all.
    expect(paint['line-color']).not.toBe('#ffffff')
    expect(paint['line-width']).toBeGreaterThanOrEqual(1)
    expect(paint['line-opacity']).toBeGreaterThanOrEqual(0.6)
  })
})

// Where the key and the tier line LAND is load-bearing, not decoration, and
// both defects it guards were found in a browser rather than here.
//
// The key is absolutely positioned against .map-shell. Put it inside #map and
// the kit's phone rule — which turns it static so it sits UNDER the map below
// 672px — leaves it under the map but still inside it, over the canvas corner.
// Put the tier paragraph inside the shell and the shell grows taller than the
// map, so the key's inset-block-end:16px is measured from a bottom edge 16px
// below the map's own: measured live at -16px before this.
// The cfg every mountChrome test hands in. metricLabels and metricUnits are
// keyed by metric because that is what readConfig produces (see byMetric): the
// key's caption is looked up by name, never by position.
const chromeCfg = (over = {}) => ({
  t: { tier: {} },
  noDataColour: '#999',
  lang: 'bg',
  metric: 'P2',
  metricLabels: { P1: 'ФПЧ10', P2: 'ФПЧ2.5' },
  metricUnits: { P1: 'µg/m³', P2: 'µg/m³' },
  ...over,
})

// Wind is an overlay like the rest of what the map draws, so its control sits
// with them. A button of its own in the corner said it was a different kind of
// thing, and left the corner carrying two stacked buttons and a disclosure.
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

describe('mountChrome anchors the key to the shell and the tier line outside it', () => {
  const chrome = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map map--hero'
    shell.appendChild(el)
    const host = document.createElement('div')
    host.append(shell)
    mountChrome(el, chromeCfg())
    return { shell, el, host }
  }

  it('puts the key in the shell, not in the map', () => {
    const { shell, el } = chrome()
    expect(el.querySelector('.scale--onmap')).toBeNull()
    expect(shell.querySelector(':scope > .scale--onmap')).not.toBeNull()
  })

  it('puts the tier line after the shell, so the shell stays the map box', () => {
    const { shell, host } = chrome()
    expect(shell.querySelector('.map-tier')).toBeNull()
    expect(host.lastElementChild.className).toContain('map-tier')
  })

  // The banners stay children of the map: they are messages about the map and
  // they do not have the phone rule the key has.
  it('leaves the hint and note inside the map', () => {
    const { el } = chrome()
    expect(el.querySelector('.map-hint')).not.toBeNull()
    expect(el.querySelector('.map-note')).not.toBeNull()
  })

  // Without a shell the key still has to render. It anchors to the map instead,
  // which loses the phone layout but shows a key rather than throwing.
  it('falls back to the map when no shell wraps it', () => {
    const el = document.createElement('div')
    document.createElement('div').appendChild(el)
    mountChrome(el, chromeCfg())
    expect(el.querySelector('.scale--onmap')).not.toBeNull()
  })
})

// The key's caption is the metric and its unit, not a fixed phrase. "Качество
// на въздуха" is simply false when the map is painting temperature, and it is
// the same words for every metric — so it says nothing about which one is on
// screen. The unit comes from the server-rendered catalogue rather than
// /api/v1/scales, which the key must caption without waiting on.
describe('the key names the metric it is a key to', () => {
  const captionOf = (cfg) => {
    const el = document.createElement('div')
    document.createElement('div').appendChild(el)
    return { el, chrome: mountChrome(el, cfg) }
  }

  it('composes the name and the unit on the first paint', () => {
    const { el } = captionOf(chromeCfg())
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ2.5, µg/m³')
  })

  // cfg.metric is already the new metric by the time onMetricChange calls
  // refresh, and refresh passes it through — so the caption follows the
  // switcher without the key subscribing to anything.
  it('follows the metric switch', () => {
    const { el, chrome } = captionOf(chromeCfg())
    chrome.showLegend({ bands: [], tier: null, metric: 'P1' })
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ10, µg/m³')
  })

  // A metric the catalogue has no unit for still gets its name.
  it('drops to the name alone when the metric has no unit', () => {
    const cfg = chromeCfg({ metricUnits: { P1: '', P2: '' } })
    const { el } = captionOf(cfg)
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ2.5')
  })

  // Only a metric with no name at all falls back, because a caption reading
  // just "µg/m³" would name nothing.
  it('falls back to the generic title when the metric has no name', () => {
    const cfg = chromeCfg({ metricLabels: {}, t: { tier: {}, legend: 'Качество на въздуха' } })
    const { el } = captionOf(cfg)
    expect(el.querySelector('.scale__label').textContent).toBe('Качество на въздуха')
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

// The map is a canvas, so the banner is the only part of its running commentary
// a screen reader can reach. Without aria-live the text changes silently.
describe('mountChrome() announces the hint banner', () => {
  it('marks it as a polite live region', () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)

    mountChrome(el, readConfig(el))
    expect(el.querySelector('.map-hint')?.getAttribute('aria-live')).toBe('polite')
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

  it('draws the diamond at the radius the circles use and in the same colours', () => {
    const cfg = { markerStrokeColour: '#ffffff' }
    const size = officialLayout()['icon-size']
    const radius = layerPaint(cfg)['circle-radius']

    // Both are [interpolate, linear, [zoom], z1, v1, z2, v2]. The zoom stops
    // must match exactly; the values differ only by the glyph's own unit.
    expect(size.slice(0, 4)).toEqual(radius.slice(0, 4))
    expect(size[4] * (DIAMOND_RADIUS_PX / 2)).toBeCloseTo(radius[4])
    expect(size[6] * (DIAMOND_RADIUS_PX / 2)).toBeCloseTo(radius[6])

    expect(officialPaint(cfg)['icon-color']).toEqual(['get', 'colour'])
    expect(officialPaint(cfg)['icon-halo-color']).toBe('#ffffff')
  })
})

describe('hexLabelPaint', () => {
  const cfg = { labelColour: '#222', markerStrokeColour: '#fff' }

  it('fades a carried reading and leaves a measured one alone', () => {
    expect(hexLabelPaint(cfg)['text-opacity']).toEqual([
      'case',
      ['==', ['get', 'carried'], true], CARRIED_OPACITY,
      ['==', ['get', 'fresh'], 0], FRESH_OPACITY,
      ['==', ['get', 'fresh'], 1], SETTLING_OPACITY,
      1,
    ])
  })

  it('is faded enough to tell apart from a measured reading', () => {
    expect(CARRIED_OPACITY).toBeLessThan(1)
    expect(CARRIED_OPACITY).toBeGreaterThan(0)
  })

  // A cell fading in is not a cell holding an old number. Keeping the whole
  // ramp above the held mute is what stops the two from ever looking alike.
  it('never draws an arriving cell as faint as a held one', () => {
    expect(FRESH_OPACITY).toBeGreaterThan(CARRIED_OPACITY)
    expect(SETTLING_OPACITY).toBeGreaterThan(FRESH_OPACITY)
    expect(SETTLING_OPACITY).toBeLessThan(1)
  })

  it('keeps the colour and halo the measured labels use', () => {
    expect(hexLabelPaint(cfg)['text-color']).toBe('#222')
    expect(hexLabelPaint(cfg)['text-halo-color']).toBe('#fff')
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
