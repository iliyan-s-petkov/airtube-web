// MapLibre GL JS 6.x ships no default export — only named ones (Map,
// AttributionControl, ...) — so `import maplibregl from 'maplibre-gl'` builds
// under Vitest (which does not check the export list) but fails a real Rollup
// build with MISSING_EXPORT. Importing the one class actually used avoids the
// mismatch entirely.
import { Map as MapLibreMap, addProtocol } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { Protocol } from 'pmtiles'
import { tierFor } from '../lib/tier.js'
import { LEGEND_CLASSES, legendRows, legendTitle, renderLegend } from '../lib/legend.js'
import { mountFullscreen, mountZoom, installZoom } from '../lib/mapcontrols.js'
import { mountLayers, installLayers, LAYER_ORDER } from '../lib/maplayers.js'
import { colourFor } from '../lib/colour.js'
import { getJSON, clearCache } from '../lib/api.js'
import { getFreshness } from '../lib/freshness.svelte.js'
import { parseMetricList, splitAttr, byMetric, hasScale } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { setSensors, setScales } from '../lib/sensors.svelte.js'
import { filterByStatus, getSensorStatus, onSensorStatusChange } from '../lib/sensorfilter.svelte.js'
import { applyLocate } from '../lib/locate.js'
import { nearestArea } from '../lib/nearest.js'
import { hexesURL, hexFeatures, resolutionForZoom, POINT_TIER_MIN_ZOOM } from '../lib/hexes.js'
import { WIND_SOURCE_ID, WIND_LAYER_ID, windFeatures, windLabel, arrowLayout, arrowPaint } from './wind.js'

// Debounce before any tier change fires a request. One pinch-zoom gesture emits
// a dozen moveend events; undebounced, that is a dozen requests and the whole
// burst.
const MOVE_DEBOUNCE_MS = 250

// The camera's own floor, and the reason the zoom stack can be honest about it.
// MapLibre keeps TWO minimums: the setting getMinZoom() reports (0 by default)
// and the floor Transform._constrain silently enforces so the world still
// covers the container — around 0.2 on a hero-height map. Left at the default,
// getZoom() bottoms out at the constrained floor while getMinZoom() keeps
// saying 0, so installZoom's `z <= getMinZoom()` never fires and the minus
// button stays live over a camera that has stopped moving. Setting it makes the
// reported floor the reachable one. It used to be 5 — "a map of one country,
// nothing below it to zoom out to" — which stopped a reader from putting
// Bulgaria in its neighbourhood. 2 shows the continent and then some, and is
// kept off 0 because MapLibre's own constrained floor sits near 0.2 on a
// hero-height map, which would put the reported floor back out of reach.
const MIN_ZOOM = 2

// The world underneath the vector archive.
//
// The archive we host is a Bulgaria extract: outside its bounding box it has
// no tiles at any zoom, so the map went beige the moment the viewport left the
// country. This raster layer draws the rest of the world under it, and the
// extract keeps painting its own detail on top wherever it has some.
//
// It is a THIRD-PARTY ORIGIN, unlike everything else this site loads, and the
// operator's decision: the alternative was rebuilding the archive from a
// Europe-wide extract. Two consequences to keep in view — every visitor's
// viewport is disclosed to that host, and the OSM Foundation's tile usage
// policy asks that busy sites not use tile.openstreetmap.org. Swapping in a
// keyed provider is this constant plus the CSP origin in airbg.yaml.
const RASTER_BASEMAP = {
  type: 'raster',
  tiles: ['https://tile.openstreetmap.org/{z}/{x}/{y}.png'],
  tileSize: 256,
  minzoom: 0,
  maxzoom: 19,
  attribution: '© OpenStreetMap contributors',
}
const RASTER_SOURCE_ID = 'airbg-raster'
const RASTER_LAYER_ID = 'airbg-raster-base'

const SOURCE_ID = 'airbg-data'
const LAYER_ID = 'airbg-markers'
const LABEL_LAYER_ID = 'airbg-marker-labels'

const HEX_SOURCE_ID = 'airbg-hexes'
const HEX_LAYER_ID = 'airbg-hex-fill'
const HEX_OUTLINE_LAYER_ID = 'airbg-hex-outline'
const HEX_POINT_LAYER_ID = 'airbg-hex-point'
const HEX_LABEL_LAYER_ID = 'airbg-hex-labels'

export function mount(el) {
  const cfg = readConfig(el)
  registerProtocols()
  const chrome = mountChrome(el, cfg)

  // Shared with the switcher island through the module-level singleton (see
  // getViewState's own doc comment) — the same store, so a metric picked
  // there is the metric this map follows.
  const vs = getViewState({ metrics: cfg.metrics, defaultMetric: cfg.metric })

  const map = new MapLibreMap({
    container: el,
    // An unset style URL is not fatal: the map renders data markers over a
    // plain background, so local development needs no tile artefacts.
    style: mapStyle(cfg),
    center: [cfg.lon, cfg.lat],
    zoom: cfg.zoom,
    minZoom: MIN_ZOOM,
    attributionControl: { compact: true },
  })

  installErrorHandler(map)

  // The zoom stack is built by mountChrome (before this map exists) and wired
  // here, to the camera it drives. `home` is the view the server rendered this
  // page at — the country fit on /, the area's own centre on /area/{slug} — so
  // reset needs no branch on which page it is standing in.
  installZoom(map, chrome.zoomButtons, { centre: [cfg.lon, cfg.lat], zoom: cfg.zoom })

  // On /area/{slug} the slug is fixed, one area, ever. On / it starts empty and
  // is only ever set by a deliberate click — never derived from the viewport.
  // Deriving it would turn the area endpoints into a rectangle query, which is
  // what they are built not to answer. The hex layer below does follow the
  // viewport; it is allowed to because it serves aggregates, not areas.
  //
  // areas: the raw {slug, lon, lat, zoom, ...} area payload (see refresh's own
  // comment on why it must be the raw body, not the lossy GeoJSON features
  // areaFeatures produces), retained here for locateMe below. null until the
  // first country/city-tier response lands — on an area page opened straight
  // at the sensor tier (fixed data-slug), that may never happen unless the
  // visitor zooms out, so locateMe's "outside coverage" branch can fire
  // before there is anything to compare against. Accepted: fetching the
  // overview solely to populate this would be the extra request the brief
  // rules out ("no new request").
  // hexUrl/hexBody are the hex layer's own dedup and cache: the grid follows
  // the viewport, so it changes on passes where tier and slug do not.
  // sensorBody is the last sensor-tier payload, held so a filter change can
  // redraw from it without a refresh cycle. Null on the area tiers, unlike the
  // sensors registry, which is deliberately left standing when a visitor zooms
  // out (see refresh) — this one drives what is PAINTED, and painting stale
  // sensors over area dots is the failure that distinction prevents.
  const state = {
    slug: cfg.slug, tier: null, scales: null, areas: null,
    hexUrl: null, hexBody: null, sensorBody: null,
  }

  // The wind overlay's own state, separate from `state` above: it is off by
  // default and never follows the viewport, the tier, or the metric — one
  // fetch for the whole country, cached for the page's life, because the
  // payload is a single forecast hour and does not change while the visitor
  // pans. See docs/wind-overlay.md.
  const windState = { on: false, body: null, loading: false }

  chrome.locateButton.addEventListener('click', () => locateMe(state, cfg, chrome))

  // unsubscribe is assigned inside the 'load' handler (see below) and read by
  // the returned `stop`. No call site in this app ever invokes `stop` today —
  // islands mount once at page load and are never explicitly unmounted (there
  // is no SPA router, see main.js's runIsland) — so this subscription is
  // intentionally page-lifetime. Exposed anyway, the same way $effect.root's
  // teardown would be, for test hygiene and in case that ever changes.
  let unsubscribe = null
  let unprovide = null
  let unfilter = null

  map.on('load', async () => {
    addRasterBasemap(map)

    // The hex grid goes in FIRST, so every later layer draws over it. It is the
    // background density field — where sensors are and roughly what they read —
    // and the area markers and sensor dots are the foreground a visitor clicks.
    // Added before the marker source for that ordering alone; MapLibre paints in
    // insertion order.
    map.addSource(HEX_SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: HEX_LAYER_ID,
      type: 'fill',
      source: HEX_SOURCE_ID,
      paint: { 'fill-color': ['get', 'colour'], 'fill-opacity': cfg.hexOpacity },
    })
    // A separate hairline outline rather than a fill-outline-color: MapLibre's
    // fill outline is always one pixel and cannot be faded, and at the address
    // tier a solid grid of them reads as a mesh rather than as cells.
    map.addLayer({
      id: HEX_OUTLINE_LAYER_ID,
      type: 'line',
      source: HEX_SOURCE_ID,
      paint: { 'line-color': ['get', 'colour'], 'line-width': 0.5, 'line-opacity': cfg.hexOpacity },
    })
    // The point tier's fallback, sharing the hex source. Past the finest
    // published cell the server sends devices rather than bins, and those are
    // normally drawn as cells like every other tier (see refreshHexes on why:
    // marks vanished under the sensor markers). A device only reaches this
    // layer when there is no size to draw a cell at — hexFeatures then keeps it
    // a Point rather than inventing a radius. The two coexist on one source
    // because the fill and line layers above ignore Point geometry and the
    // filter below holds this one to the same discipline in the other
    // direction — a circle layer draws a circle at every position it is given,
    // and a polygon's positions are its six corners, so without the filter
    // every cell on the map wore a ring of dots.
    //
    // Fully opaque, unlike the cells behind it: a cell is a summary and reads
    // as a wash, a bare device is a position and should not.
    map.addLayer({
      id: HEX_POINT_LAYER_ID,
      type: 'circle',
      source: HEX_SOURCE_ID,
      filter: ['==', ['geometry-type'], 'Point'],
      paint: {
        'circle-color': ['get', 'colour'],
        // Grows with zoom so a dense city does not read as one blob when a
        // reader zooms in to separate it — which is the reason to be at this
        // tier at all.
        'circle-radius': ['interpolate', ['linear'], ['zoom'], 15, 4, 18, 9],
        'circle-stroke-width': 1,
        // The same stroke the sensor dots use, not a new config key: this IS a
        // sensor dot — the difference is which endpoint delivered it, which is
        // not a distinction a reader should have to see.
        'circle-stroke-color': cfg.markerStrokeColour,
      },
    })

    // The reading, printed in the middle of the cell it belongs to. It takes
    // over from LABEL_LAYER_ID at exactly the zoom that layer stops at, so one
    // reading is never drawn twice and never absent.
    //
    // Polygon-only and value-only: a bare point has no interior to centre a
    // number in, and a cell with no reading for this metric keeps its no-data
    // colour and says nothing.
    map.addLayer({
      id: HEX_LABEL_LAYER_ID,
      type: 'symbol',
      source: HEX_SOURCE_ID,
      minzoom: POINT_TIER_MIN_ZOOM,
      filter: ['all',
        ['==', ['geometry-type'], 'Polygon'],
        ['has', 'value'],
        ['!=', ['get', 'value'], null],
      ],
      layout: hexLabelLayout(cfg),
      paint: labelPaint(cfg),
    })

    map.addSource(SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: LAYER_ID,
      type: 'circle',
      source: SOURCE_ID,
      // Both marker layers stop at the handover zoom. Above it the cells are
      // individually visible and carry the reading themselves; leaving the
      // markers on drew the same number twice, once at the device's own
      // coordinate — which is why a labelled dot appeared off-centre inside
      // one cell and on the edge of another. The cell covers the ground
      // around the sensor, and that is the claim the map makes here.
      maxzoom: POINT_TIER_MIN_ZOOM,
      paint: layerPaint(cfg),
    })

    // The reading, printed on the map. Colour alone carried three different
    // facts here — no reading, an unscaled metric, and a real band value — so
    // a reader without colour vision lost all three at once, and the legend
    // could only tell them what the colours WOULD have meant. The number is
    // the second channel D4 asks for, and it is the same value the panel and
    // the province list show.
    //
    // A separate symbol layer rather than a bigger circle: the circles are
    // 5-9px and cannot hold a number, and growing them to fit would crowd the
    // map at exactly the zooms where areas sit closest together.
    map.addLayer({
      id: LABEL_LAYER_ID,
      type: 'symbol',
      source: SOURCE_ID,
      maxzoom: POINT_TIER_MIN_ZOOM,
      filter: ['all', ['has', 'value'], ['!=', ['get', 'value'], null]],
      layout: labelLayout(cfg),
      paint: labelPaint(cfg),
    })

    // The wind layer is added empty and hidden at load, not on first toggle:
    // adding a source and a layer to a live map is the part that can fail, and
    // failing it here — before any visitor has asked for wind — keeps the
    // toggle itself down to setData plus a visibility flip.
    map.addSource(WIND_SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: WIND_LAYER_ID,
      type: 'symbol',
      source: WIND_SOURCE_ID,
      layout: { ...arrowLayout(), visibility: 'none' },
      paint: arrowPaint(cfg),
    })
    chrome.windButton.addEventListener('click', () => toggleWind(map, cfg, chrome, windState))

    // Here and not in mountChrome: the options are the style's own groups, and
    // map.getStyle() has no layers to report until the style has loaded. A menu
    // built any earlier is a menu of nothing, which is why it stays hidden
    // until this call finds something to put in it.
    installLayers(map, chrome.layersUI, {
      labels: cfg.t.layers,
      caption: cfg.t.layersCaption,
      views: chrome.layerViews,
    })

    // Registered synchronously, right here — after addLayer so setPaintProperty
    // always has a real layer to act on, but deliberately BEFORE awaiting
    // initData below, not after: islands/map.js is plain .js, not .svelte.js,
    // so $effect.root cannot be used here (runes only compile in
    // .svelte/.svelte.js — see the task brief's own note on this). vs.metric
    // is an ordinary getter backed by a rune defined in viewstate.svelte.js,
    // so reading it needs no rune; reacting to it changing does, which is why
    // onMetricChange (a plain callback list, see that file) exists instead of
    // a second, invented store API — it already had to exist for the reason
    // documented there (window 'hashchange' does not fire for our own
    // pushState/replaceState writes, so it cannot substitute either).
    //
    // A metric change arriving before state.scales has loaded is handled, not
    // ignored: markerPaint treats "no scales yet" the same as "no band
    // table" (hasScale(null, metric) is false), so it paints unscaledColour
    // rather than throwing or silently dropping the change — and the explicit
    // call below self-corrects it the moment initData's own scales arrive.
    unsubscribe = vs.onMetricChange((metric) => onMetricChange(map, state, cfg, chrome, metric))

    // The sensor filter repaints from the body already in hand rather than
    // going back through refresh(): the tier, the slug and the metric are all
    // untouched by a filter change, so a refresh would be a request (cached,
    // but still a full repaint cycle) for data that has not changed. Only which
    // of it is drawn has.
    unfilter = onSensorStatusChange(() => repaintSensors(map, state, cfg))

    // What "refresh" MEANS lives here, with the map that owns the data; the
    // toolbar button and the freshness line only ask for it (see
    // lib/freshness.svelte.js). clearCache first, or the button would be a
    // control that visibly does nothing: getJSON's cache lives for the page's
    // lifetime and would answer every one of these calls from memory. `force`
    // for the same reason on refresh()'s own tier:slug dedup. The hexes are
    // reloaded too — they are the density field under the same readings, and a
    // page where half the picture updated is worse than one where none did.
    unprovide = getFreshness().provide(async () => {
      clearCache()
      await refresh(map, state, cfg, chrome, true)
      await refreshHexes(map, state, cfg)
    })

    await initData(map, state, cfg, chrome)

    // Explicit first call for the metric the page opened on: the STORE
    // method registered above only notifies on a CHANGE, and this is
    // deliberately AFTER initData so the FIRST real paint has state.scales to
    // work with, rather than racing it.
    onMetricChange(map, state, cfg, chrome, vs.metric)

    // Home page only: an area page's map island carries a fixed data-slug
    // (cfg.slug is non-null there), so its opening view is already the
    // area's own centre and there is nothing for /api/v1/locate to improve.
    // Fired after the first paint above, not before it, so a slow or failed
    // lookup never delays the map the visitor already sees.
    if (!cfg.slug) await locateVisitor(map, state, cfg, chrome)
  })

  map.on('moveend', debounce(() => {
    refresh(map, state, cfg, chrome)
    refreshHexes(map, state, cfg)
  }, MOVE_DEBOUNCE_MS))

  // One layer, two kinds of feature (see sensorFeatures/areaFeatures): an
  // aggregate marker carries `slug` and clicking it is what selects an area
  // — the deliberate act the enumeration budget is denominated in. A sensor
  // marker carries `id` instead and clicking it opens the panel via the
  // shared viewstate; the map does not render the panel itself (see
  // islands/panel.js), only publishes the click as a destination.
  map.on('click', LAYER_ID, (e) => {
    const props = e.features?.[0]?.properties
    if (!props) return
    if (props.slug) {
      state.slug = props.slug
      refresh(map, state, cfg, chrome)
      return
    }
    // Number(): the click path already carries a number in production (see
    // sensorFeatures, which sets properties.id = ids[i] straight from the
    // JSON int64 column), but GeoJSON feature properties are not guaranteed
    // by the spec to preserve type across every producer, and
    // lib/sensors.svelte.js's own lookup applies the same coercion — so this
    // stays a defensive match to that contract rather than an assumption
    // about MapLibre's internals.
    if (props.id !== undefined) vs.openSensor(Number(props.id))
  })

  // The cells inherit that click above the handover zoom, where the markers
  // have stepped aside. Only a point-tier cell answers: the server sends
  // sensor_id there and nowhere else, so an aggregate cell — which stands for
  // a bin, not a device — has no panel to open and stays inert rather than
  // opening some arbitrary member of itself.
  map.on('click', HEX_LAYER_ID, (e) => {
    const id = e.features?.[0]?.properties?.sensorId
    if (id !== undefined && id !== null) vs.openSensor(Number(id))
  })

  return { map, chrome, stop: () => { unsubscribe?.(); unprovide?.(); unfilter?.() } }
}

// toggleWind is the whole wind control: fetch once, then show or hide.
//
// Exported and given an injectable fetch for the same reason initData is —
// the "no jsdom" rule puts a real MapLibre instance out of reach, so the
// behaviour that matters (the disclosure appears with the arrows and never
// without them) is only testable through a fake map and a fake chrome.
//
// A failed fetch leaves the layer off rather than raising the map's error
// banner: /api/v1/wind answers 503 whenever no forecast covers the current
// hour, which is an ordinary state for an optional overlay, and an error
// banner is reserved for the data the page actually exists to show.
export async function toggleWind(map, cfg, chrome, state, fetchJSON = getJSON) {
  if (state.on) {
    state.on = false
    map.setLayoutProperty(WIND_LAYER_ID, 'visibility', 'none')
    chrome.showWind(false, '')
    return
  }
  // A second click while the first fetch is in flight is dropped, not queued:
  // getJSON already dedupes the request, but two resolutions would each flip
  // the layer and the later one could turn on a layer the visitor just asked
  // to turn off.
  if (state.loading) return
  if (!state.body) {
    state.loading = true
    try {
      state.body = await fetchJSON('/api/v1/wind')
    } catch {
      chrome.showWind(false, '')
      return
    } finally {
      state.loading = false
    }
  }
  map.getSource(WIND_SOURCE_ID).setData({ type: 'FeatureCollection', features: windFeatures(state.body) })
  map.setLayoutProperty(WIND_LAYER_ID, 'visibility', 'visible')
  state.on = true
  chrome.showWind(true, windLabel(state.body, cfg.t))
}

// onMetricChange is what runs on every metric switch (and once, explicitly,
// for the metric the page opened on): repaint the layer via setPaintProperty
// — cheap, synchronous, and needs neither a new map nor a network round trip
// — show or clear the unscaled-metric note, and catch up the ALREADY-loaded
// features' stale `value`/`colour` (computed for the PREVIOUS metric) by
// forcing refresh() to recompute them.
//
// That forced refresh is NOT a network request in practice: urlFor never
// takes a metric (the aggregate/sensor endpoints return every metric's values
// in one payload — see sensorFeatures/areaFeatures, which merely pick a
// column), so it is the exact same URL as before and getJSON's cache serves
// it. `force` exists only to bypass refresh()'s own tier:slug dedup key,
// which does not change when just the metric does and would otherwise make
// this a silent no-op.
function onMetricChange(map, state, cfg, chrome, metric) {
  cfg.metric = metric
  const scaled = hasScale(state.scales, metric)
  map.setPaintProperty(LAYER_ID, 'circle-color', markerPaint(bandsFor(state.scales, metric), {
    noDataColour: cfg.noDataColour,
    unscaledColour: cfg.unscaledColour,
    scaled,
  }))
  chrome.showNote(metricNote(state.scales, metric, cfg.t.unscaled))
  refresh(map, state, cfg, chrome, true)
  // Also the grid's FIRST paint: mount() invokes this callback once at load for
  // the metric the page opened on, so initData does not call refreshHexes as
  // well — that would only be a second request for the same URL. On every later
  // call the URL is unchanged, and refreshHexes recolours the body it holds
  // rather than refetching.
  refreshHexes(map, state, cfg)
}

// initData is the whole body of the MapLibre 'load' handler after the source and
// layer exist: load the colour scales, then paint.
//
// Exported as ONE unit, and tested as one, because the ORDER of these two steps
// is load-bearing and a per-function test cannot see it. Round 1 of this fix
// tested loadScales in isolation and passed while being unreachable in
// production: refresh calls showHint('') on the ordinary path, which used to
// erase the scales-failure explanation set moments earlier. The bug lived
// between the two functions, so the test has to span both.
export async function initData(map, state, cfg, chrome) {
  state.scales = await loadScales(chrome, cfg)
  // Published into the registry the moment it resolves (null included, on a
  // failed fetch) — see lib/sensors.svelte.js's own comment on why the panel
  // reads scales from there rather than calling loadScales a second time.
  setScales(state.scales)
  await refresh(map, state, cfg, chrome)
}

// loadScales fetches the band tables once per page load. Cache-Control: public,
// so it costs nothing on a repeat visit.
//
// A null result is NOT silent. Without the band tables, bandsFor returns [] and
// colourFor paints every marker NO_DATA_COLOUR — a uniformly grey map, which on
// an air-quality site reads as "the whole country has insufficient data" rather
// than "we could not load the colour scale".
//
// Reported through showError, not showHint: the scales are fetched exactly once
// per page load and never retried, so an all-grey map is permanent for the
// lifetime of the page and its explanation has to be too. showHint's text is
// recomputed on every refresh and cleared when it does not apply — which is
// precisely what silently erased this message before.
//
// Given its dependencies as arguments so a test can drive both branches with a
// stub chrome — the call site is inside a MapLibre 'load' handler.
export async function loadScales(chrome, cfg, fetchJSON = getJSON) {
  const scales = await fetchJSON('/api/v1/scales').catch(() => null)
  if (scales === null) chrome.showError(cfg.t.unavailable)
  return scales
}

// refresh fetches the tier the current zoom permits and repaints.
//
// `force` bypasses the tier:slug dedup key below. Ordinary callers (moveend,
// a marker click) never need it: those genuinely change the tier or the slug.
// onMetricChange does — the tier and slug are untouched by a metric switch,
// so without `force` the dedup key would make repainting for the new metric a
// silent no-op.
async function refresh(map, state, cfg, chrome, force = false) {
  const tier = tierFor(map.getZoom(), cfg.zoomCity, cfg.zoomSensor)

  // The sensor tier needs a slug and must not invent one. With none selected,
  // fall back to the city aggregate and show the hint — a real friction cost,
  // accepted so that enumeration breadth is bounded by deliberate clicks rather
  // than by pan distance.
  const effective = tier === 'sensors' && !state.slug ? 'city' : tier
  chrome.showHint(effective !== tier ? cfg.t.hint : '')

  // EFFECTIVE, not tier: on an area page opened at the sensor zoom with no slug
  // adopted, the dots are city aggregates while the page prints a sensor count.
  // Naming the raw tier here would restate that contradiction instead of
  // resolving it. Placed before the dedup return below so the legend is correct
  // even on the passes that fetch nothing.
  chrome.showLegend({ bands: bandsFor(state.scales, cfg.metric), tier: effective, metric: cfg.metric })

  const url = urlFor(effective, state.slug)
  // Unchanged tier and slug: nothing to do. getJSON would serve from cache
  // anyway, but repainting the same features on every moveend is visible churn.
  const key = `${effective}:${state.slug ?? ''}`
  if (!force && key === state.tier) return

  let body
  try {
    body = await getJSON(url)
  } catch (err) {
    chrome.showHint(cfg.t.unavailable)
    console.error('map data:', err)
    return
  }
  state.tier = key

  // Published for the panel to read (see lib/sensors.svelte.js) whenever
  // this fetch actually carried sensor coordinates. Left untouched on a
  // city/country tier response: those responses have no sensor columns at
  // all (see areaPayload), and clearing the registry here would blank an
  // already-open panel the instant a visitor zooms out past the sensor
  // tier, rather than leaving its last-known content on screen.
  if (effective === 'sensors') {
    setSensors(body)
    state.sensorBody = body
  } else {
    state.sensorBody = null
    // The raw payload, not areaFeatures' output: features drop `zoom`
    // entirely and fold lon/lat into GeoJSON geometry, but locateMe needs
    // exactly {slug, lon, lat, zoom} per area (see nearestArea's signature).
    state.areas = body?.areas ?? []
  }

  const features = effective === 'sensors'
    ? filterByStatus(sensorFeatures(body, cfg.metric, state.scales, cfg.noDataColour), getSensorStatus())
    : areaFeatures(body, cfg.metric, state.scales, cfg.noDataColour)
  map.getSource(SOURCE_ID).setData({ type: 'FeatureCollection', features })
}

// repaintSensors redraws the sensor tier from the payload already in hand.
// Exported for its own test, and a no-op away from the sensor tier: the filter
// is a control over sensors, so a click on it while the map is showing province
// aggregates must not blank them.
export function repaintSensors(map, state, cfg) {
  if (!state.sensorBody) return
  const features = filterByStatus(
    sensorFeatures(state.sensorBody, cfg.metric, state.scales, cfg.noDataColour),
    getSensorStatus(),
  )
  map.getSource(SOURCE_ID).setData({ type: 'FeatureCollection', features })
}

// refreshHexes fetches the hex grid for the current zoom and viewport and
// repaints the background layer.
//
// This is the ONE layer that follows the viewport. It is allowed to, and the
// area tiers still are not, because the two answer different questions: an
// aggregate bin names no area and spends no enumeration budget, whereas
// /area/{slug}/sensors returns identified sensors and is bounded by deliberate
// clicks. See the §7.1 amendment in the Phase 1 design.
//
// Separate from refresh() rather than folded into it: the hex grid changes on
// every zoom step and most pans, and the area tier changes on neither, so
// sharing one dedup key would refetch the areas on every pinch.
//
// The response body is retained so a metric switch repaints from memory. Only
// the colours change — the bins, their counts and their geometry do not — so a
// refetch would return bytes the client already holds.
export async function refreshHexes(map, state, cfg, fetchJSON = getJSON) {
  const url = hexesURL(map.getZoom(), map.getBounds?.())
  if (url !== state.hexUrl) {
    let body
    try {
      body = await fetchJSON(url)
    } catch (err) {
      // Deliberately quiet, unlike refresh()'s own failure. The hex grid is a
      // background layer over a working map: the markers, the panel and the
      // legend are all unaffected, so a hint claiming the data is unavailable
      // would misdescribe the page the visitor is looking at. The last good
      // grid stays on screen.
      console.error('hex grid:', err)
      return
    }
    state.hexUrl = url
    state.hexBody = body
  }
  const bands = bandsFor(state.scales, cfg.metric)
  // The point tier is drawn at the size this zoom would have asked the grid for
  // — rounded the same way hexesURL rounds it, so the cell the reader sees is
  // the one the URL describes. That is what keeps the grid on screen past the
  // finest published cell instead of collapsing it into marks hidden under the
  // sensor markers.
  const features = hexFeatures(
    state.hexBody, cfg.metric, bands, cfg.noDataColour, colourFor,
    resolutionForZoom(Math.round(map.getZoom())),
  )
  map.getSource(HEX_SOURCE_ID)?.setData({ type: 'FeatureCollection', features })
}

// locateVisitor asks the server where the visitor is and, only for a genuine
// "geoip" placement (see applyLocate's own comment on why "default" must
// never move the map or adopt a slug), jumps the map straight there and
// adopts the slug so refresh()'s next call may use the per-area sensor tier.
//
// map.jumpTo, never map.easeTo: a multi-second flight away from the national
// view on first paint reads as a bug, not a feature, on a page the visitor
// has been looking at for less than a second.
//
// The fetch is wrapped so a rejected promise (network failure, an endpoint
// that does not exist in a given environment) lands in applyLocate's own
// "stay put" branch rather than throwing out of this async 'load' handler.
export async function locateVisitor(map, state, cfg, chrome, fetchJSON = getJSON) {
  const body = await fetchJSON('/api/v1/locate').catch(() => null)
  const located = applyLocate(body, { defaultView: { lon: cfg.lon, lat: cfg.lat, zoom: cfg.zoom } })
  if (!located.move) return
  map.jumpTo({ center: located.centre, zoom: located.zoom })
  state.slug = located.slug
  await refresh(map, state, cfg, chrome, true)
}

// locateMe is the PRECISE, user-initiated path to an area page — distinct
// from locateVisitor's coarse, server-side placement above. The coordinate
// itself never reaches the network: nearestArea resolves it against the
// already-loaded area list entirely in the browser, and only the resulting
// slug becomes a request, as an ordinary page navigation. See nearest.js's
// own comment for why: there is no server endpoint that accepts a point, by
// design, because one would be a bounding-box query in disguise.
//
// geolocation/navigate are injected (default: the real browser APIs) so a
// test can drive both the success and every error branch without a real
// location prompt or a real page navigation.
export function locateMe(state, cfg, chrome, { geolocation = navigator.geolocation, navigate = defaultNavigate } = {}) {
  if (!geolocation) {
    chrome.showHint(cfg.t.locateFailed)
    return
  }
  geolocation.getCurrentPosition(
    (pos) => {
      // nearestArea has no distance cutoff: any non-empty areas array always
      // yields SOME nearest match, however far away it actually is. So its
      // own null return only ever means "the area list itself is empty or
      // unknown" (state.areas hasn't loaded — see state.areas's own comment
      // above), never "you are genuinely outside coverage". Checked here,
      // BEFORE calling nearestArea, so that distinction reaches the honest
      // message: locateFailed ("we don't know"), not an "you're outside
      // coverage" claim this implementation cannot make true.
      //
      // Past this guard nearestArea cannot return null, so there is no
      // outside-coverage branch to write. Give it a real distance cutoff
      // before adding one back.
      if (!state.areas || state.areas.length === 0) {
        chrome.showHint(cfg.t.locateFailed)
        return
      }
      const area = nearestArea([pos.coords.longitude, pos.coords.latitude], state.areas)
      navigate(areaPath(cfg.langPrefix, area.slug))
    },
    (err) => {
      // PERMISSION_DENIED === 1 is the Geolocation API's own constant
      // (GeolocationPositionError.PERMISSION_DENIED); every other error
      // (POSITION_UNAVAILABLE, TIMEOUT, or none of the above) gets the
      // generic message.
      chrome.showHint(err?.code === 1 ? cfg.t.locateDenied : cfg.t.locateFailed)
    },
  )
}

function defaultNavigate(url) {
  window.location.href = url
}

// areaPath builds the area URL under the language prefix the server rendered
// this page at — a plain "/area/{slug}" would silently switch a non-default
// reader back to the site's default language on click.
//
// The prefix is SERVER-SUPPLIED (data-lang-prefix), not sniffed from
// window.location. The set of languages is data — an operator adds one by
// dropping a catalogue into i18n.dir — so no expression here can know which
// first path segment is a language and which is a page. Matching "/en" by hand
// would send every German reader back to Bulgarian the day de.json lands.
export function areaPath(prefix, slug) {
  return `${prefix}/area/${encodeURIComponent(slug)}`
}

export function urlFor(tier, slug) {
  if (tier === 'country') return '/api/v1/overview'
  if (tier === 'city') return '/api/v1/overview?tier=city'
  return `/api/v1/area/${encodeURIComponent(slug)}/sensors`
}

// areaFeatures maps the choropleth payload straight onto point features.
//
// covered === false renders in the neutral no-data grey with no value label.
// Fewer than three distinct sensors is not data, and drawing it in a band colour
// would imply a confidence the pipeline explicitly refuses.
export function areaFeatures(body, metric, scales, noDataColour) {
  const bands = bandsFor(scales, metric)
  return (body?.areas ?? []).map((a) => ({
    type: 'Feature',
    geometry: { type: 'Point', coordinates: [a.lon, a.lat] },
    properties: {
      slug: a.slug,
      colour: a.covered ? colourFor(a.values?.[metric], bands, noDataColour) : noDataColour,
      value: a.covered ? a.values?.[metric] ?? null : null,
      sensor_count: a.sensor_count,
    },
  }))
}

// sensorFeatures reads the COLUMNAR payload: parallel arrays, each metric a
// sibling key of the fixed columns. That shape was chosen precisely for this
// consumer, so it maps onto features with no reshaping.
//
// A null in a metric column means the sensor does not report that metric, which
// is distinct from reporting zero and must stay distinct.
export function sensorFeatures(body, metric, scales, noDataColour) {
  const bands = bandsFor(scales, metric)
  const s = body?.sensors ?? {}
  const ids = s.id ?? []
  const column = s[metric] ?? []
  const features = []
  for (let i = 0; i < ids.length; i++) {
    const value = column[i] ?? null
    features.push({
      type: 'Feature',
      geometry: { type: 'Point', coordinates: [s.lon[i], s.lat[i]] },
      properties: {
        id: ids[i],
        colour: colourFor(value, bands, noDataColour),
        value,
        quality: s.quality?.[i] ?? '',
      },
    })
  }
  return features
}

// bandsFor picks the scale table for one metric. The scales endpoint returns an
// array of tables; matching on `metric` rather than on array position means a
// reordered response cannot silently recolour the map.
export function bandsFor(scales, metric) {
  if (!Array.isArray(scales)) return []
  return scales.find((s) => s.metric === metric)?.bands ?? []
}

// 'street-names' -> 'tLayerStreetNames', the dataset spelling of
// data-t-layer-street-names. Exported for its own test: it is the one place the
// group keys and the template's attribute names have to agree, and they agree
// by rule rather than by two lists kept in step by hand.
export function layerLabelKey(group) {
  const camel = group.split('-').map((p) => p.charAt(0).toUpperCase() + p.slice(1)).join('')
  return `tLayer${camel}`
}

export function readConfig(el) {
  const d = el.dataset
  return {
    slug: d.slug || null,
    // No fallbacks: the opening view is configuration
    // (frontend.default_zoom/default_lon/default_lat, or the area's own
    // centre), and the server renders all three on every map island. A
    // hardcoded 7/25.4858/42.7339 here would numerically agree with today's
    // airbg.yaml while masking a server that stopped rendering them.
    zoom: Number(d.zoom),
    lon: Number(d.lon),
    lat: Number(d.lat),
    // No fallback: series.default_metric is configuration. A hardcoded 'P2'
    // here would silently mask a missing data-metric attribute AND would be
    // the exact duplicated constant this phase removes — the server always
    // renders data-metric now (see internal/web/render.go), so a missing
    // attribute must surface as undefined, not a quiet default.
    metric: d.metric,
    // The full metric list (upstream.CanonicalMetrics, server-rendered) that
    // getViewState needs to validate a metric before adopting it — same
    // attribute, same parseMetricList, as the switcher island reads. No
    // fallback beyond what parseMetricList itself already gives a blank/
    // missing attribute ([]): a second default list here would be the
    // duplicated-constant problem series.default_metric's comment above is
    // about, one metric list instead of one metric.
    metrics: parseMetricList(d.metrics),
    // What the key calls the metric it is a key to, and what that metric is
    // measured in. Zipped into lookups here rather than kept as two positional
    // arrays, because the legend asks by metric name and never by index — and
    // an index that has to be looked up first is the off-by-one zipLabels
    // exists to prevent. Both fall back per-metric inside legendTitle.
    metricLabels: byMetric(parseMetricList(d.metrics), splitAttr(d.metricLabels)),
    metricUnits: byMetric(parseMetricList(d.metrics), splitAttr(d.metricUnits)),
    basemap: d.basemap || '',
    // The language prefix for in-app links: "" for the default language,
    // "/de" otherwise. Server-rendered because the language set is data (see
    // areaPath). Empty is a legitimate value, so the fallback is only for a
    // missing attribute on the default-language page.
    langPrefix: d.langPrefix || '',
    // Which of the band table's two shipped label languages to show. Read off
    // <html lang>, which base.gohtml already renders, rather than derived from
    // langPrefix — the default language has an empty prefix.
    // ownerDocument ?? document because readConfig is duck-typed on `dataset`
    // and its tests pass a plain object rather than a mounted element.
    lang: (el.ownerDocument ?? document).documentElement.lang,
    // Paint values and zoom thresholds: configuration, arriving as data-*
    // attributes, no fallback here — a hardcoded fallback that numerically
    // agrees with today's airbg.yaml is exactly the duplicated constant this
    // phase removes.
    noDataColour: d.noDataColour,
    unscaledColour: d.unscaledColour,
    markerStrokeColour: d.markerStrokeColour,
    // A WebGL paint value is configuration, not CSS: no rule can reach a
    // canvas layer, which is why every colour this island paints with arrives
    // as a data-* attribute. An earlier version read --fg through
    // getComputedStyle with a hex fallback; literals.test.js caught the
    // fallback, and it was right to — the fallback was the tell that the value
    // was coming from the wrong place.
    labelColour: d.markerLabelColour,
    emptyBasemapColour: d.emptyBasemapColour,
    // How solidly the hex grid paints. A paint value like the colours above,
    // and server-rendered for the same reason: no CSS rule reaches a WebGL
    // layer, and a fallback here that agreed with today's airbg.yaml would hide
    // a server that stopped rendering the attribute.
    hexOpacity: Number(d.hexOpacity),
    zoomCity: Number(d.zoomCity),
    zoomSensor: Number(d.zoomSensor),
    // Strings come from the server, not from a JS catalogue: Go owns the
    // catalogue, and a second copy here would drift on the first edit.
    t: {
      legend: d.tLegend || '',
      // The name of the fold, not of the key: the summary is icon-only, and an
      // icon-only control still has to be announced as something.
      legendToggle: d.tLegendToggle || '',
      legendNoData: d.tLegendNoData || '',
      // Keyed by the tier names tierFor returns, so the lookup in showLegend is
      // a direct index rather than a branch that could drift from tier.js.
      tier: {
        country: d.tTierCountry || '',
        city: d.tTierCity || '',
        sensors: d.tTierSensors || '',
      },
      // Two names for one button: what it will do next, not what state it is
      // in — aria-pressed already reports the state.
      fullscreen: d.tFullscreen || '',
      fullscreenExit: d.tFullscreenExit || '',
      zoomIn: d.tZoomIn || '',
      zoomOut: d.tZoomOut || '',
      zoomReset: d.tZoomReset || '',
      layersButton: d.tLayersButton || '',
      layersCaption: d.tLayersCaption || '',
      viewLegend: d.tViewLegend || '',
      viewBasemap: d.tViewBasemap || '',
      // One label per style group, keyed by the group's own name so the menu
      // can look up whatever the style turns out to carry. Derived from
      // LAYER_ORDER rather than written out, because the attribute name is a
      // mechanical transform of the key — data-t-layer-street-names becomes
      // d.tLayerStreetNames — and writing both would be two spellings of one
      // fact. A group with no string falls back to its key at render time.
      layers: Object.fromEntries(LAYER_ORDER.map((g) => [g, d[layerLabelKey(g)] || ''])),
      hint: d.tHint || '',
      rateLimited: d.tRateLimited || '',
      unavailable: d.tUnavailable || '',
      unscaled: d.tUnscaled || '',
      locateButton: d.tLocateButton || '',
      locateDenied: d.tLocateDenied || '',
      locateFailed: d.tLocateFailed || '',
      windToggle: d.tWindToggle || '',
      windAttribution: d.tWindAttribution || '',
    },
  }
}

function emptyCollection() {
  return { type: 'FeatureCollection', features: [] }
}

// blankStyle is a valid MapLibre style with no tile sources, used when no
// basemap is configured. Data markers still render, over a plain background
// painted the server-configured emptyBasemapColour.
//
// Exported (not module-private) so a test can prove it reads
// cfg.emptyBasemapColour and not some other config field, without going
// through mount()'s real MapLibreMap construction, which the "no jsdom" rule
// puts out of reach.
export function blankStyle(emptyBasemapColour) {
  return { version: 8, sources: {}, layers: [{ id: 'bg', type: 'background', paint: { 'background-color': emptyBasemapColour } }] }
}

// mapStyle picks the style mount() hands to MapLibre: the configured basemap
// URL, or a flat colour when none is set. Pulled out of the constructor call
// itself (not just blankStyle's body) because the mutation the review caught
// was in the ARGUMENT — blankStyle(cfg.noDataColour) instead of
// blankStyle(cfg.emptyBasemapColour) — which blankStyle's own tests cannot
// see since blankStyle only ever sees whatever value its caller already
// picked.
// addRasterBasemap slides the world raster in beneath the style's own drawn
// layers.
//
// Beneath the DRAWN layers, not beneath everything: a background layer paints
// the whole canvas, so a raster inserted under it would be covered wherever
// the vector style reaches — which is the entire viewport. The first layer
// that is not a background is therefore the insertion point, and a style with
// no such layer (an empty style, which is what a map served without tiles
// mounts) gets the raster on top of nothing, which is where it belongs.
export function addRasterBasemap(map) {
  const layers = map.getStyle?.()?.layers ?? []
  const beforeId = layers.find((l) => l.type !== 'background')?.id
  map.addSource(RASTER_SOURCE_ID, RASTER_BASEMAP)
  map.addLayer({ id: RASTER_LAYER_ID, type: 'raster', source: RASTER_SOURCE_ID }, beforeId)
}

export function mapStyle(cfg) {
  return cfg.basemap ? cfg.basemap : blankStyle(cfg.emptyBasemapColour)
}

// registerProtocols teaches MapLibre to read pmtiles:// URLs, which is how
// style.json references the single 300 MB archive: the protocol turns each tile
// read into an HTTP range request, so a visitor transfers only the ranges their
// viewport needs.
//
// Idempotent, and takes `add` as a parameter, because MapLibre's addProtocol is
// global module state: registering twice would silently replace the first
// handler, and a test cannot observe a global it cannot inject into.
let protocolsRegistered = false
export function registerProtocols(add = addProtocol) {
  if (protocolsRegistered) return
  protocolsRegistered = true
  add('pmtiles', new Protocol().tile)
}

// installErrorHandler wires the 'error' event so a style-load failure cannot
// take the sensor markers down with it: tiles unavailable degrades to a blank
// background, it never fails the page. Logged once rather than per failed
// tile, because a missing archive produces one error per range request.
//
// Takes `map` (needs only `.on`, not a real MapLibre instance) and `warn` as
// parameters, same idiom as registerProtocols's injected `add`, so a test can
// drive it with a fake and assert the log-once behaviour without a real map.
export function installErrorHandler(map, warn = console.warn) {
  let errorLogged = false
  map.on('error', (e) => {
    if (errorLogged) return
    errorLogged = true
    warn('basemap unavailable, rendering markers only', e?.error?.message ?? e)
  })
}

// layerPaint is the circle layer's INITIAL paint object, set once at
// map.addLayer time. Pulled out of mount()'s map.on('load', ...) callback,
// which is unreachable from a test (it needs a real MapLibre map), so the
// paint values it reads from cfg can be proven directly.
//
// Named layerPaint, not markerPaint (its name before this task): 'circle-
// color' here is only ever a placeholder — the source is empty at addLayer
// time (see emptyCollection), and by the time features exist, onMetricChange
// has already replaced 'circle-color' via setPaintProperty with the real,
// metric-aware expression from markerPaint below. Two functions named
// markerPaint, one returning a full paint object and one returning a single
// paint VALUE, would have been the same kind of silent ambiguity this file's
// other comments warn about elsewhere.
export function layerPaint(cfg) {
  return {
    'circle-color': ['get', 'colour'],
    'circle-radius': ['interpolate', ['linear'], ['zoom'], 5, 5, 12, 9],
    'circle-stroke-width': 1,
    'circle-stroke-color': cfg.markerStrokeColour,
  }
}

// labelLayout and labelPaint are the symbol layer that prints each area's
// reading beside its dot — the non-colour channel for the air-quality scale.
//
// text-allow-overlap stays FALSE, which is the whole crowding strategy: rather
// than inventing a zoom threshold to guess when labels start colliding,
// MapLibre drops the ones that would overlap and keeps the rest. The map thins
// itself out as areas converge, and no number is ever drawn on top of another.
//
// The fontstack is the one the basemap style already ships (the tiles are an
// OpenMapTiles build whose own label layers use Noto Sans Regular), so this
// adds no font asset and no new origin.
export function labelLayout(cfg) {
  return {
    // One decimal, matching the province list and the sensor panel: three
    // surfaces showing the same reading to different precision would be three
    // surfaces disagreeing. number-format localises the separator, so this
    // reads 12,4 in Bulgarian and 12.4 in English without a second formatter.
    'text-field': [
      'number-format',
      ['get', 'value'],
      { locale: cfg.lang || 'bg', 'min-fraction-digits': 1, 'max-fraction-digits': 1 },
    ],
    'text-font': ['Noto Sans Regular'],
    'text-size': 11,
    // Beside the dot, not on it: the circle is 5-9px, so a centred number
    // would sit on its own stroke and lose contrast against every band colour.
    'text-offset': [0.9, 0],
    'text-anchor': 'left',
    'text-allow-overlap': false,
    'text-ignore-placement': false,
    'text-optional': true,
  }
}

// hexLabelLayout is the same number, printed in the middle of a cell instead
// of beside a dot. It reuses labelLayout's formatting — one decimal, the
// basemap's own fontstack, overlap thinning — and differs only in placement:
// there is no dot to clear, so the number is centred on the cell it describes.
export function hexLabelLayout(cfg) {
  const { 'text-offset': _offset, 'text-anchor': _anchor, ...shared } = labelLayout(cfg)
  return { ...shared, 'text-anchor': 'center' }
}

export function labelPaint(cfg) {
  return {
    'text-color': cfg.labelColour,
    // The halo is what makes the number legible on every band from the palest
    // teal to the darkest purple, and over the basemap's own streets, without
    // re-tinting the served colour underneath it.
    'text-halo-color': cfg.markerStrokeColour,
    'text-halo-width': 1.4,
  }
}

// markerPaint is the circle layer's 'circle-color' paint VALUE for one
// metric — recomputed on every metric switch and applied via
// map.setPaintProperty, never at layer-creation time (see layerPaint above).
//
// Three different facts must not share one colour: "no reading" (grey,
// noDataColour), "this metric has no band table" (unscaledColour), and a
// real band value. An unscaled metric still distinguishes the first two —
// only the third collapses, because there is no per-value meaning left to
// draw once there are no bands. A flat unscaledColour return for the whole
// !scaled branch was tried and rejected: it paints "no reading" and "has a
// reading" identically, so on a metric most sensors don't report (e.g.
// temperature), the map reads as full coverage when it is not — the same
// class of defect this file's colourFor/noDataColour split exists to
// prevent for scaled metrics. ['has', 'value'] (the shape this task's brief
// originally suggested) is ALSO wrong here, for a reason worth stating
// loudly: areaFeatures and sensorFeatures always set the `value` key, even
// when its content is null (`value: a.covered ? ... : null` /
// `column[i] ?? null`), so `has` is true unconditionally and that branch
// would be dead code, always taking the "has a reading" side.
export function markerPaint(bands, { noDataColour, unscaledColour, scaled }) {
  if (!scaled) return ['case', ['==', ['get', 'value'], null], noDataColour, unscaledColour]

  // Mirrors colourFor's own rule (bands ascending, upper INCLUSIVE, upper ==
  // null is the open top band) but as a MapLibre `step` expression instead of
  // a JS loop, because this runs in the paint property, not against a feature
  // array — deliberately duplicated rather than shared with colourFor: the
  // whole point of computing colour here, instead of recomputing every
  // feature's `colour` property through colourFor again, is that switching
  // metric must not re-walk every feature. See onMetricChange's comment.
  const steps = []
  for (let i = 0; i < bands.length - 1; i++) steps.push(bands[i].upper, bands[i + 1].colour)
  return [
    'case',
    ['==', ['get', 'value'], null], noDataColour,
    ['step', ['get', 'value'], bands[0]?.colour ?? noDataColour, ...steps],
  ]
}

// The note is the only thing telling a reader why every dot on an unscaled
// metric's map is the same colour. Returned rather than rendered here so the
// caller (onMetricChange) owns the DOM, through chrome.showNote.
export function metricNote(scales, metric, text) {
  return hasScale(scales, metric) ? '' : text
}

// hintController owns the ONE rule about the hint banner: an error outranks the
// routine hint, permanently.
//
// showHint is called on every refresh with the text that applies right now, and
// with '' when none does — that clear-on-empty is what makes the tier hint
// disappear when it stops applying. It is also what silently erased the
// scales-failure explanation, because refresh runs immediately after the scales
// load and calls showHint('') whenever the zoom's tier is served as-is (the
// common case: zoom 7 on / and zoom ~10 on an area page). ANYONE ADDING A
// showHint CALL SHOULD KNOW IT CAN ERASE A REAL ERROR MESSAGE — use showError
// for anything the visitor must keep seeing.
//
// Pure and separate from the DOM on purpose: `render` is the only side effect,
// so the precedence rule itself can be driven by a test with an array as the
// sink instead of a browser, and the rule the test exercises is the same code
// the page runs.
export function hintController(render) {
  let stickyError = ''
  return {
    showHint(text) {
      // Deliberately not "only ignore the empty string": once the map is known
      // to be uncoloured, the tier hint is the lesser message too.
      if (stickyError) return
      render(text)
    },
    showError(text) {
      stickyError = text
      render(text)
    },
  }
}

export function debounce(fn, ms) {
  let timer
  return (...args) => {
    clearTimeout(timer)
    timer = setTimeout(() => fn(...args), ms)
  }
}

// mountChrome builds the legend and the hint banner as plain DOM, appended
// beside the MapLibre canvas inside the same container. Plain DOM rather than
// Svelte: two static-ish text nodes and a class toggle need no reactivity
// system, and pulling in Svelte for this would be a dependency with nothing to
// show for it.
//
// Classes only, never `el.style` — the CSP's style-src has no 'unsafe-inline',
// so an inline style written from JS is silently dropped by the browser, not
// merely a lint complaint.
// Exported for the placement test, not for other callers: mount() is the only
// one. It touches no MapLibre object, so where the key and the tier line land
// in the DOM is checkable without a WebGL context — and that placement is
// load-bearing (see the shell comments below), not decoration.
export function mountChrome(el, cfg) {
  // The key and the tier line go on the SHELL, not on #map, and they are the
  // only two things here that do. The kit turns .scale--onmap static below
  // 672px so the key sits under the map on a phone — and inside .map, "under
  // the map" is still inside the map, over the corner of the canvas. The shell
  // exists to be the positioning context for exactly this. Falling back to `el`
  // keeps a map mounted without a shell rendering something rather than
  // throwing, at the cost of the phone layout.
  const shell = el.closest('.map-shell') ?? el

  // <details>: it owns the open state, the keyboard and the accessible name, so
  // nothing else has to record whether the key is folded. Open by default — a
  // key the reader has to find and unfold does not explain the colours they are
  // already looking at.
  const legend = document.createElement('details')
  legend.className = LEGEND_CLASSES
  legend.open = true
  shell.appendChild(legend)

  // What a dot aggregates at this zoom. Under the map as prose, not inside the
  // key: the key is an overlay with no panel behind it (the kit's §5.2d — a box
  // there would hide the map it explains), and a sentence of that length haloed
  // over a choropleth is not readable. It is also not part of the ramp.
  // AFTER the shell, not inside it. The shell is the box the key is anchored
  // to — inset-block-end:16px is measured from the shell's bottom — so anything
  // else placed in it makes the shell taller than the map and pushes the key
  // down past the map's own edge. Measured live: 16px below it.
  const tierLine = document.createElement('p')
  tierLine.className = 'legend__tier map-tier'
  shell.after(tierLine)

  // Full screen and zoom go on the FRAME, not the shell: they are furniture on
  // the canvas and belong over it at every width, which is the opposite of the
  // key's rule directly above. Fullscreen wires itself — it drives the element,
  // not the camera — while the zoom stack is returned unwired, because the
  // MapLibre map is constructed after this function returns.
  mountFullscreen(el, { label: cfg.t.fullscreen, exitLabel: cfg.t.fullscreenExit })
  const zoom = mountZoom(el, {
    inLabel: cfg.t.zoomIn,
    outLabel: cfg.t.zoomOut,
    resetLabel: cfg.t.zoomReset,
  })

  // The layers menu takes the frame's top-left corner, which is why the hint
  // and the note below are offset past it in app.css rather than sharing it.
  // Built here, filled later: its options are read off the mounted style, which
  // does not exist until MapLibre has loaded one.
  const layers = mountLayers(el, { label: cfg.t.layersButton })

  // Two toggles about the SCREEN rather than about the basemap, listed above
  // the categories rather than smuggled in beside "Shops" as if they were one
  // more kind of place.
  //
  // The basemap one hides only what carries an airbg:group — the kit's own
  // version walks every layer in the style, which on this map would take the
  // readings down with the ground. "Hide the basemap" has to leave the
  // measurements standing, or it is not the control it says it is.
  const layerViews = [
    { id: 'legend', label: cfg.t.viewLegend, apply: (on) => { legend.hidden = !on } },
    {
      id: 'basemap',
      label: cfg.t.viewBasemap,
      needsMap: true,
      apply: (on, map) => {
        for (const l of map.getStyle()?.layers ?? []) {
          if (l.metadata?.['airbg:group']) {
            map.setLayoutProperty(l.id, 'visibility', on ? 'visible' : 'none')
          }
        }
      },
    },
  ]

  const hint = document.createElement('div')
  hint.className = 'map-hint'
  hint.hidden = true
  el.appendChild(hint)

  // The unscaled-metric explanation. A separate element from the hint/error
  // banner above, on purpose: hintController's whole reason to exist is the
  // precedence rule between a routine hint and a sticky error, and a note
  // about the CURRENT metric having no band table is neither of those — it is
  // not routine (it does not come and go with the viewport) and it is not an
  // error (nothing failed). Conflating it with hint/error would either let a
  // real error hide the note or let the note block a real error from showing.
  const note = document.createElement('div')
  note.className = 'map-note'
  note.hidden = true
  el.appendChild(note)

  // The find-me button: precise, user-initiated geolocation (see locateMe in
  // this file). textContent, never innerHTML — same CSP constraint as
  // everything else in this container. The click handler itself is wired by
  // mount(), which is where `state` (the loaded area list locateMe reads)
  // and the real `map` first exist; mountChrome only owns the DOM.
  const locateButton = document.createElement('button')
  locateButton.type = 'button'
  locateButton.className = 'map-locate'
  locateButton.textContent = cfg.t.locateButton
  el.appendChild(locateButton)

  // The wind overlay's toggle and its disclosure. aria-pressed, not a checkbox,
  // because this shows and hides a map layer rather than submitting anything —
  // and the pressed state is the only thing announcing that the layer is on,
  // since a screen reader cannot see the arrows. The label is a sibling, not
  // the button's own text: it must stay visible while the layer is, and a
  // control's label disappears the moment focus moves on.
  const windButton = document.createElement('button')
  windButton.type = 'button'
  windButton.className = 'map-wind'
  windButton.setAttribute('aria-pressed', 'false')
  windButton.textContent = cfg.t.windToggle
  el.appendChild(windButton)

  const windNote = document.createElement('div')
  windNote.className = 'map-wind-label'
  windNote.hidden = true
  el.appendChild(windNote)

  // The precedence rule lives in hintController; this is only the wiring from
  // its decision to the banner. textContent, never innerHTML.
  const hintCtl = hintController((text) => {
    hint.textContent = text
    hint.hidden = !text
  })

  const showLegend = ({ bands, tier, metric }) => {
    renderLegend(legend, {
      // Repainted with the bands, which is the only way it stays right: the
      // bands change with the metric, and so does the name of what they band.
      title: legendTitle({
        label: cfg.metricLabels[metric],
        unit: cfg.metricUnits[metric],
        fallback: cfg.t.legend,
      }),
      toggleLabel: cfg.t.legendToggle,
      ...legendRows(bands, {
        noDataColour: cfg.noDataColour,
        noDataLabel: cfg.t.legendNoData,
        lang: cfg.lang,
      }),
    })
    const text = cfg.t.tier[tier] ?? ''
    tierLine.textContent = text
    tierLine.hidden = !text
  }

  // Drawn once at mount, before any scales have loaded, so the key is never an
  // empty overlay: with no bands that is the title and the no-data row, both of
  // which are true at that moment.
  showLegend({ bands: [], tier: null, metric: cfg.metric })

  return {
    ...hintCtl,
    showNote(text) {
      note.textContent = text
      note.hidden = !text
    },
    showLegend,
    zoomButtons: zoom.buttons,
    layersUI: layers,
    layerViews,
    locateButton,
    windButton,
    // Both halves move together: the disclosure is shown exactly when the
    // arrows are, so no caller can turn one on without the other.
    showWind(on, text) {
      windButton.setAttribute('aria-pressed', String(on))
      windNote.textContent = on ? text : ''
      windNote.hidden = !on || !text
    },
  }
}
