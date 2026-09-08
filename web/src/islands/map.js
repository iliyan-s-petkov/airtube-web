// MapLibre GL JS 6.x ships no default export — only named ones (Map,
// AttributionControl, ...) — so `import maplibregl from 'maplibre-gl'` builds
// under Vitest (which does not check the export list) but fails a real Rollup
// build with MISSING_EXPORT. Importing the one class actually used avoids the
// mismatch entirely.
import { Map as MapLibreMap, addProtocol } from 'maplibre-gl'
import { Protocol } from 'pmtiles'
import 'maplibre-gl/dist/maplibre-gl.css'
import { tierFor } from '../lib/tier.js'
import { LEGEND_CLASSES, legendRows, legendTitle, renderLegend } from '../lib/legend.js'
import { createScaleDialog } from '../lib/scaledialog.js'
import { scaleFor } from '../lib/scaleinfo.js'
import { mountFullscreen, mountZoom, mountLocate, installZoom } from '../lib/mapcontrols.js'
import { mountLayers, installLayers, LAYER_ORDER } from '../lib/maplayers.js'
import { rampColour, rampValueStops } from '../lib/ramp.js'
import { getJSON, clearCache } from '../lib/api.js'
import { getFreshness } from '../lib/freshness.svelte.js'
import { parseMetricList, splitAttr, byMetric, hasScale } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { setSensors, setScales, findSensor, getSensors } from '../lib/sensors.svelte.js'
import { filterByStatus, getSensorStatus, setSensorStatus, onSensorStatusChange } from '../lib/sensorfilter.svelte.js'
import { applyLocate } from '../lib/locate.js'
import { readFlag, writeFlag } from '../lib/storage.js'
import { nearestArea, nearestSensor } from '../lib/nearest.js'
import {
  chooseWindow, mountWindow, readWindow, windowOptions, withWindow,
} from '../lib/mapwindow.js'
import { setMapAreas, provideAreaSelect } from '../lib/mapareas.svelte.js'
import { stationsOf, readingAt } from '../lib/stations.js'
import {
  hexesURL, hexFeatures, resolutionForZoom,
  GRID_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM,
} from '../lib/hexes.js'
import {
  WIND_SOURCE_ID, WIND_LAYER_ID, ARROW_IMAGE_ID, windFeatures, windField, windLabel, windIsStale,
  arrowImage, arrowLayout, arrowPaint,
} from './wind.js'
import {
  BOUNDARY_SOURCE_ID, BOUNDARY_FILL_LAYER_ID, BOUNDARY_LINE_LAYER_ID,
  BOUNDARY_SELECTED_LAYER_ID, BOUNDARY_LAYER_IDS,
  boundaryFillPaint, boundaryLinePaint, boundarySelectedPaint,
  selectedFilter, boundsOf, findBoundary,
} from '../lib/boundaries.js'

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
export const HEX_LABEL_LAYER_ID = 'airbg-hex-labels'

// Whether the colour key is unrolled. Its own key, not part of the layers
// menu's state: the menu decides whether the key exists, this decides whether
// it is folded, and conflating them would make turning the key back on undo a
// fold the reader never touched.
export const LEGEND_FOLD_KEY = 'airbg:legend-open'

// MapLibre's own maxzoom default. setLayerZoomRange takes both ends, so a call
// that only means to move the floor still has to name a ceiling.
const MAX_ZOOM_CEILING = 24

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
    // Read from storage rather than defaulting to live: a reader who picked a
    // week's average is asking a question about this map, not about this visit,
    // and re-picking it on every page is the map disagreeing with its own
    // selector for one paint. An unknown stored name degrades to live.
    window: readWindow(),
  }

  // The wind overlay's own state, separate from `state` above: it is off by
  // default and never follows the viewport, the tier, or the metric — one
  // fetch for the whole country, cached for the page's life, because the
  // payload is a single forecast hour and does not change while the visitor
  // pans. See docs/wind-overlay.md.
  const windState = { on: false, body: null, loading: false }

  // The province outlines' own state, on the same one-fetch-per-page terms as
  // the wind: the borders do not move, so the collection is fetched once and
  // kept. On, unlike the wind, because the outlines are part of the map a
  // reader is shown rather than an overlay they ask for.
  const boundaryState = { on: false, body: null, loading: false }

  chrome.locateButton.addEventListener('click', () => locateMe(map, state, cfg, chrome))

  // unsubscribe is assigned inside the 'load' handler (see below) and read by
  // the returned `stop`. No call site in this app ever invokes `stop` today —
  // islands mount once at page load and are never explicitly unmounted (there
  // is no SPA router, see main.js's runIsland) — so this subscription is
  // intentionally page-lifetime. Exposed anyway, the same way $effect.root's
  // teardown would be, for test hygiene and in case that ever changes.
  let unsubscribe = null
  let unprovide = null
  let unfilter = null
  // The finder island is beside this one, not inside it: it names an area and
  // this map is what moves. Registered here, where the camera is.
  const unselect = provideAreaSelect((area) => showArea(map, state, cfg, chrome, area))

  // Declared before the 'load' handler that cancels it: a jumpTo taken during
  // the opening placement queues a moveend the handler has already answered.
  const onMoveEnd = debounce(() => {
    refresh(map, state, cfg, chrome)
    refreshHexes(map, state, cfg)
    // Only while the layer is on: the arrow lattice is sized to the viewport,
    // so a move that changes the zoom changes which arrows exist.
    if (windState.on) paintWind(map, windState)
  }, MOVE_DEBOUNCE_MS)

  map.on('load', async () => {
    // Not awaited: mount()'s metric subscription must be registered before this
    // handler's first await (see below), and the ground is detail the map does
    // not need in order to be a map. It slots itself under the grid when it
    // arrives, by id.
    addBasemapOverlay(map, cfg.basemap, HEX_LAYER_ID)

    // The hex grid goes in FIRST, so every later layer draws over it. It is the
    // background density field — where sensors are and roughly what they read —
    // and the area markers and sensor dots are the foreground a visitor clicks.
    // Added before the marker source for that ordering alone; MapLibre paints in
    // insertion order.
    map.addSource(HEX_SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    // GRID_MIN_ZOOM_FRACTIONAL on all three grid layers: below it the coarsest
    // bin the server publishes is drawn too small to read as a cell at all, and
    // the area markers carry the reading alone.
    map.addLayer({
      id: HEX_LAYER_ID,
      type: 'fill',
      source: HEX_SOURCE_ID,
      minzoom: GRID_MIN_ZOOM_FRACTIONAL,
      paint: { 'fill-color': ['get', 'colour'], 'fill-opacity': cfg.hexOpacity },
    })
    // A separate hairline outline rather than a fill-outline-color: MapLibre's
    // fill outline is always one pixel and cannot be faded, and at the address
    // tier a solid grid of them reads as a mesh rather than as cells.
    map.addLayer({
      id: HEX_OUTLINE_LAYER_ID,
      type: 'line',
      source: HEX_SOURCE_ID,
      minzoom: GRID_MIN_ZOOM_FRACTIONAL,
      paint: hexOutlinePaint(cfg),
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
      minzoom: GRID_MIN_ZOOM_FRACTIONAL,
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
      // FRACTIONAL, like every other handover here: hexesURL picks the point
      // tier at Math.round(zoom) >= 15, which is true from 14.5. Written as a
      // whole 15, this layer stayed off for half a level after the cells under
      // it had already become one-sensor cells — so a reader who zoomed all the
      // way in saw a hexagon with no number in it.
      minzoom: POINT_TIER_MIN_ZOOM_FRACTIONAL,
      filter: ['all',
        ['==', ['geometry-type'], 'Polygon'],
        ['has', 'value'],
        ['!=', ['get', 'value'], null],
      ],
      layout: hexLabelLayout(cfg),
      paint: labelPaint(cfg),
    })

    // Between the grid and the markers: the outlines frame the readings, so
    // they draw over the cells, and the dots a visitor clicks draw over them.
    // Added empty and hidden for the reason the wind layer is — the part that
    // can fail is adding a source and three layers to a live map, and failing
    // it here costs nothing a reader can see.
    map.addSource(BOUNDARY_SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: BOUNDARY_FILL_LAYER_ID,
      type: 'fill',
      source: BOUNDARY_SOURCE_ID,
      layout: { visibility: 'none' },
      paint: boundaryFillPaint(cfg),
    })
    map.addLayer({
      id: BOUNDARY_LINE_LAYER_ID,
      type: 'line',
      source: BOUNDARY_SOURCE_ID,
      layout: { visibility: 'none' },
      paint: boundaryLinePaint(cfg),
    })
    map.addLayer({
      id: BOUNDARY_SELECTED_LAYER_ID,
      type: 'line',
      source: BOUNDARY_SOURCE_ID,
      layout: { visibility: 'none' },
      filter: selectedFilter(state.slug),
      paint: boundarySelectedPaint(cfg),
    })

    map.addSource(SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: LAYER_ID,
      type: 'circle',
      source: SOURCE_ID,
      // Both marker layers stop at a handover zoom. Above it the cells are
      // individually visible and carry the reading themselves; leaving the
      // markers on drew the same number twice, once at the device's own
      // coordinate — which is why a labelled dot appeared off-centre inside
      // one cell and on the edge of another. The cell covers the ground
      // around the sensor, and that is the claim the map makes here.
      //
      // WHICH handover depends on what the markers currently are, so the real
      // value is set per tier in refresh() (see markerMaxZoom). This is the
      // starting one, for the tier the map opens on.
      maxzoom: markerMaxZoom('country'),
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
      maxzoom: markerMaxZoom('country'),
      filter: ['all', ['has', 'value'], ['!=', ['get', 'value'], null]],
      layout: labelLayout(cfg),
      paint: labelPaint(cfg),
    })

    // The wind layer is added empty and hidden at load, not on first toggle:
    // adding a source and a layer to a live map is the part that can fail, and
    // failing it here — before any visitor has asked for wind — keeps the
    // toggle itself down to setData plus a visibility flip.
    map.addSource(WIND_SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    // pixelRatio 2: the raster is drawn at twice its nominal size so it stays
    // sharp on a retina screen and when icon-size scales it past 1.
    map.addImage(ARROW_IMAGE_ID, arrowImage(cfg), { pixelRatio: 2 })
    map.addLayer({
      id: WIND_LAYER_ID,
      type: 'symbol',
      source: WIND_SOURCE_ID,
      layout: { ...arrowLayout(), visibility: 'none' },
      paint: arrowPaint(cfg),
    })

    // Wind is an overlay, so it belongs with the other overlays rather than in
    // a button of its own in the corner. Assembled here and not in mountChrome
    // because it is the only view that needs the map's source and the fetch
    // state, both of which live in this scope.
    //
    // defaultOff: every other option starts on because the map the reader was
    // shown is the map they keep. This one is not part of that map, and turning
    // it on costs a request.
    const windView = {
      id: 'wind',
      label: cfg.t.windToggle,
      // No needsMap: the arrows are this island's own source and layer, not the
      // basemap's, so they still draw on a map served without tiles.
      defaultOff: true,
      apply: (on) => setWind(map, cfg, chrome, windState, on),
    }

    // Here and not in mountChrome: the options are the style's own groups, and
    // map.getStyle() has no layers to report until the style has loaded. A menu
    // built any earlier is a menu of nothing, which is why it stays hidden
    // until this call finds something to put in it.
    // No defaultOff: the outlines are on unless the reader has switched them
    // off, because a province map with no provinces drawn on it is a claim the
    // page keeps making in words and never showing.
    const boundaryView = {
      id: 'boundaries',
      label: cfg.t.viewBoundaries,
      apply: (on) => setBoundaries(map, state, boundaryState, on),
    }

    installLayers(map, chrome.layersUI, {
      labels: cfg.t.layers,
      caption: cfg.t.layersCaption,
      views: [...chrome.layerViews, windView, boundaryView],
    })

    // Wired here rather than in mount(), for the reason the layers menu is: a
    // pick reloads every data layer, and there is nothing to reload until the
    // sources exist. The grid comes along because it is the same readings under
    // the same markers, and a map where half the picture averaged a week and
    // the other half did not would be two answers to one question.
    chrome.windowMenu.onpick(async (name) => {
      if (!chooseWindow(state, name)) return
      await refresh(map, state, cfg, chrome, true)
      await refreshHexes(map, state, cfg)
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
    unfilter = onSensorStatusChange(() => {
      repaintSensors(map, state, cfg)
      // The grid too, from the body already held: refreshHexes short-circuits
      // the fetch when the URL has not moved, so this is a repaint, not a call.
      refreshHexes(map, state, cfg)
    })

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
      await refreshWind(map, cfg, chrome, windState)
    })

    // The opening camera is settled BEFORE the first paint, inside initData's
    // `place` step — after the scales, because the colours come from them, and
    // before the refresh, because that refresh is meant to be the only one.
    // The map used to draw the national view, then the metric it was already
    // showing, then the visitor's city, then the moveend its own jump had
    // queued: four draws of one screen, seen as the map redrawing itself
    // outwards from the centre for a second or two after every reload.
    //
    // The cost is that the map holds off its readings until the placement
    // answers — capped at LOCATE_TIMEOUT_MS, past which the national view is
    // drawn and a late answer moves it the old way.
    let placed = false
    await initData(map, state, cfg, chrome, async () => {
      applyMetricColours(map, state, cfg, chrome, vs.metric)

      // A #sensor= in the URL is the most specific thing anyone can say about
      // where this map should open, so it is asked first and, when it answers,
      // the geoip placement below is skipped: a link to a sensor in Plovdiv
      // sent to a reader in Sofia must land on the sensor.
      placed = await openDeepLinkedSensor(map, state, cfg, chrome, vs, getJSON, { paint: false })

      // Home page only: an area page's map island carries a fixed data-slug
      // (cfg.slug is non-null there), so its opening view is already the area's
      // own centre and there is nothing for /api/v1/locate to improve.
      if (!placed && !cfg.slug) {
        placed = await placeVisitor(map, state, cfg, getJSON, { timeoutMs: LOCATE_TIMEOUT_MS })
      }
    })
    await refreshHexes(map, state, cfg)

    // Every jumpTo above queued a moveend of its own, and the paint it would
    // debounce into has just happened at that exact camera position.
    onMoveEnd.cancel()

    // The slow lookup only: the body is in getJSON's cache by now if it ever
    // arrived, so this costs a request only when the race above lost. Not
    // awaited — the map is already on screen and complete without it.
    if (!placed && !cfg.slug) locateVisitor(map, state, cfg, chrome)
  })

  map.on('moveend', onMoveEnd)

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

  // The cells inherit that click wherever the markers have stepped aside,
  // which is now everywhere the grid draws. A point-tier cell names its device
  // and opens the panel.
  //
  // An aggregate cell names none — it stands for a bin, not a device — but it
  // used to answer nothing at all, and with the markers hidden that left the
  // map with no way to drill into a province by clicking it. It resolves to the
  // area its centre falls nearest instead, by the same nearest-centroid rule
  // the locate button already navigates by. In place, like the marker click:
  // the bin is not the area, so it selects the area rather than claiming to be
  // one.
  map.on('click', HEX_LAYER_ID, (e) => {
    const id = e.features?.[0]?.properties?.sensorId
    if (id !== undefined && id !== null) {
      vs.openSensor(Number(id))
      // The hash alone is not the panel: it reads the registry, which a map
      // with no area selected never fills.
      openDeepLinkedSensor(map, state, cfg, chrome, vs, getJSON, { move: false })
      return
    }
    const slug = cellArea(state, e.lngLat)
    if (!slug) return
    state.slug = slug
    refresh(map, state, cfg, chrome)
  })

  // The province outlines answer a click the two handlers above did not.
  //
  // Registered on the map rather than on the outline layer, and asking first
  // whether anything else was hit: the hit fill covers the whole country at
  // every zoom, so a layer-bound handler would fire on top of the marker and
  // cell handlers and select a second area for one click. Those two are the
  // more specific claim — a dot is a station, a cell is a bin — and this is
  // what the ground between them means.
  map.on('click', (e) => {
    if (!boundaryState.on) return
    if (hit(map, e.point, [LAYER_ID, HEX_LAYER_ID]).length) return
    const slug = boundaryChoice(state, hit(map, e.point, [BOUNDARY_FILL_LAYER_ID])[0])
    if (!slug) return
    state.slug = slug
    highlightBoundary(map, slug)
    refresh(map, state, cfg, chrome)
    const bounds = boundsOf(findBoundary(boundaryState.body, slug))
    if (bounds) map.fitBounds(bounds, { padding: BOUNDARY_FIT_PADDING })
  })

  return { map, chrome, stop: () => { unsubscribe?.(); unprovide?.(); unfilter?.(); unselect() } }
}

// Padding in pixels around a province fitted into the frame. Enough that the
// outline the reader just selected is not flush against the edge of the map,
// where the highlight it was given would be half a line wide.
const BOUNDARY_FIT_PADDING = 24

// queryRenderedFeatures over whichever of the named layers the map actually
// carries. MapLibre throws on a layer id it does not know, and every caller
// here runs on a map whose layers were added in an async 'load' handler that
// may not have reached them yet.
function hit(map, point, layers) {
  const present = layers.filter((id) => map.getLayer?.(id))
  if (!present.length) return []
  return map.queryRenderedFeatures(point, { layers: present }) ?? []
}

// boundaryChoice is the whole decision behind a click on open ground: the
// province under the pointer, or nothing.
//
// Nothing on a map already scoped to one area — /area/{slug} is one province,
// ever, and there is nothing to drill into — which is the same rule cellArea
// applies to the cells, for the same reason. Separated from the handler because
// the handler needs a real MapLibre instance the "no jsdom" rule puts out of
// reach.
export function boundaryChoice(state, feature) {
  if (state.slug) return null
  return feature?.properties?.slug || null
}

// highlightBoundary is the selection, expressed as a filter on the heavy
// outline layer. One write, no geometry: the selected province is already in
// the source.
export function highlightBoundary(map, slug) {
  if (!map.getLayer?.(BOUNDARY_SELECTED_LAYER_ID)) return
  map.setFilter(BOUNDARY_SELECTED_LAYER_ID, selectedFilter(slug))
}

// setBoundaries is the outline control: fetch once, then show or hide.
//
// The same shape as setWind, and for the same reasons — it returns the state
// actually reached so a failed fetch corrects the checkbox rather than leaving
// it ticked over a map with no outlines on it, and it takes the state asked for
// rather than flipping the one it finds.
//
// A failed fetch leaves the outlines off and raises no banner: the readings are
// what the page is for, and they are all still there.
export async function setBoundaries(map, state, bstate, on, fetchJSON = getJSON) {
  if (!on) {
    bstate.on = false
    setBoundaryVisibility(map, 'none')
    return false
  }
  if (bstate.loading) return bstate.on
  if (!bstate.body) {
    bstate.loading = true
    try {
      bstate.body = await fetchJSON('/api/v1/boundaries')
    } catch {
      setBoundaryVisibility(map, 'none')
      return false
    } finally {
      bstate.loading = false
    }
  }
  map.getSource?.(BOUNDARY_SOURCE_ID)?.setData(bstate.body)
  // On /area/{slug} the province is already chosen, so the outlines arrive with
  // that one already picked out.
  highlightBoundary(map, state.slug)
  setBoundaryVisibility(map, 'visible')
  bstate.on = true
  return true
}

function setBoundaryVisibility(map, visibility) {
  for (const id of BOUNDARY_LAYER_IDS) {
    if (map.getLayer?.(id)) map.setLayoutProperty(id, 'visibility', visibility)
  }
}

// cellArea decides which area an aggregate cell click selects: the one whose
// centroid the click falls nearest.
//
// Returns null rather than a slug in the three cases where selecting anything
// would be wrong — a click MapLibre reported no position for, a map already
// scoped to one area (there is nothing to drill into), and a map that has not
// yet loaded the area list. Separated from the handler because that is the
// whole decision, and the handler around it needs a real MapLibre instance the
// "no jsdom" rule puts out of reach.
export function cellArea(state, lngLat) {
  if (!lngLat || state.slug) return null
  return nearestArea([lngLat.lng, lngLat.lat], state.areas)?.slug ?? null
}

// setWind is the whole wind control: fetch once, then show or hide.
//
// It takes the state asked for rather than flipping the one it finds, because
// the control is now a checkbox in the layers menu: a toggle would drift out of
// step with the box the moment a fetch failed. It RETURNS the state actually
// reached, which is what lets the box correct itself.
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
export async function setWind(map, cfg, chrome, state, on, fetchJSON = getJSON) {
  if (!on) {
    state.on = false
    map.setLayoutProperty(WIND_LAYER_ID, 'visibility', 'none')
    chrome.showWind(false, '')
    return false
  }
  // A second request while the first fetch is in flight is dropped, not queued:
  // getJSON already dedupes the request, but two resolutions would each flip
  // the layer and the later one could turn on a layer the visitor just asked
  // to turn off.
  if (state.loading) return state.on
  if (!state.body) {
    state.loading = true
    try {
      state.body = await fetchJSON('/api/v1/wind')
    } catch {
      chrome.showWind(false, '')
      return false
    } finally {
      state.loading = false
    }
  }
  paintWind(map, state)
  map.setLayoutProperty(WIND_LAYER_ID, 'visibility', 'visible')
  state.on = true
  chrome.showWind(true, windLabel(state.body, cfg.t))
  return true
}

// refreshWind is the wind layer's share of a refresh: nothing, until the hour
// the held forecast is valid for has passed.
//
// Everything else the button reloads moves on the five-minute ingest cycle. The
// forecast does not (see windIsStale), so this drops the body only on an hour
// boundary — and refetches there and then only if the layer is on. With it off,
// the cleared body is enough: the next toggle fetches the current hour.
export async function refreshWind(map, cfg, chrome, state, now = new Date(), fetchJSON = getJSON) {
  if (!windIsStale(state.body, now)) return false
  state.body = null
  if (state.on) await setWind(map, cfg, chrome, state, true, fetchJSON)
  return true
}

// setCellValues moves the cell-label layer's floor, and nothing else.
//
// The number is normally reserved for the point tier, where a cell is one
// sensor: below that a cell is an average of several, and a country covered in
// printed figures reads as noise over the ramp that is the primary reading.
// But a reader comparing two neighbourhoods should not have to zoom to sensor
// level one cell at a time to get the figures, so the floor is theirs to lower.
//
// Down to the CELLS' own floor, not to zero: a number below that would print
// over ground with no cell drawn under it. The label layer's own collision
// thinning does the rest — where the cells are too small to hold a number, it
// simply drops the ones that will not fit.
export function setCellValues(map, on) {
  map.setLayerZoomRange(
    HEX_LABEL_LAYER_ID,
    on ? GRID_MIN_ZOOM_FRACTIONAL : POINT_TIER_MIN_ZOOM_FRACTIONAL,
    MAX_ZOOM_CEILING,
  )
}

// paintWind redraws the arrows for the viewport the map is currently showing.
//
// The served field is one national lattice at the snapshot's hex resolution, so
// drawing it as-is means the arrows thin out as the reader zooms in and are
// gone entirely over a single neighbourhood — a layer that empties itself looks
// exactly like a forecast that failed. windField resamples the same vectors
// onto a screen-sized lattice instead; the values are still the model's, only
// repeated, and the disclosure already names the grid they came from.
export function paintWind(map, state) {
  if (!state.body) return
  const source = map.getSource(WIND_SOURCE_ID)
  if (!source) return
  const b = map.getBounds?.()
  const features = b
    ? windField(state.body, {
      bounds: [b.getWest(), b.getSouth(), b.getEast(), b.getNorth()],
      zoom: map.getZoom(),
    })
    : windFeatures(state.body)
  source.setData({ type: 'FeatureCollection', features })
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
  applyMetricColours(map, state, cfg, chrome, metric)
  refresh(map, state, cfg, chrome, true)
  // On every call the URL is unchanged, so refreshHexes recolours the body it
  // holds rather than refetching.
  refreshHexes(map, state, cfg)
}

// The colour half of a metric change: which band table the markers are painted
// from, and the note about a metric that has none. Split out because the map's
// FIRST paint needs the colours without the two refreshes around them — at load
// the data is about to be fetched anyway, and calling the whole of
// onMetricChange for a metric nobody had changed yet was one of the redraws
// that made a reload flicker.
function applyMetricColours(map, state, cfg, chrome, metric) {
  cfg.metric = metric
  map.setPaintProperty(LAYER_ID, 'circle-color', markerPaint(bandsFor(state.scales, metric), {
    noDataColour: cfg.noDataColour,
    unscaledColour: cfg.unscaledColour,
    scaled: hasScale(state.scales, metric),
  }))
  chrome.showNote(metricNote(state.scales, metric, cfg.t.unscaled))
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
// `place`, when given, runs between the scales and the first paint: it is
// where the opening camera is decided. Before it existed the map painted the
// server's default view, then the visitor's city, then whatever the moveend
// from that jump asked for — three draws of the same first screen.
export async function initData(map, state, cfg, chrome, place = null) {
  state.scales = await loadScales(chrome, cfg)
  // Published into the registry the moment it resolves (null included, on a
  // failed fetch) — see lib/sensors.svelte.js's own comment on why the panel
  // reads scales from there rather than calling loadScales a second time.
  setScales(state.scales)
  if (place) await place()
  await refresh(map, state, cfg, chrome)
}

// loadScales fetches the band tables once per page load. Cache-Control: public,
// so it costs nothing on a repeat visit.
//
// A null result is NOT silent. Without the band tables, bandsFor returns [] and
// rampColour paints every marker NO_DATA_COLOUR — a uniformly grey map, which on
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

// The caption says what one CELL is, so the grid's resolution decides it, not
// the marker tier.
export function cellTier(zoom, markerTier) {
  if (zoom >= POINT_TIER_MIN_ZOOM_FRACTIONAL) return 'sensors'
  return markerTier === 'sensors' ? 'city' : markerTier
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
  chrome.showLegend({
    bands: bandsFor(state.scales, cfg.metric),
    tier: cellTier(map.getZoom(), effective),
    metric: cfg.metric,
    scale: scaleFor(state.scales, cfg.metric),
  })

  // Before the dedup return, like the legend: the handover depends on what the
  // markers are, and a pass that fetches nothing can still be the pass where
  // that changed (an area click adopts a slug without moving the map).
  applyMarkerZoomRange(map, effective)

  const url = withWindow(urlFor(effective, state.slug), state.window)
  // Unchanged tier, slug and window: nothing to do. getJSON would serve from
  // cache anyway, but repainting the same features on every moveend is visible
  // churn. The window is part of the key even though every pick forces a
  // refresh, because a key that omits it would be a key two different answers
  // share.
  const key = `${effective}:${state.slug ?? ''}:${state.window}`
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
    setSensors(body, state.slug ?? null)
    state.sensorBody = body
  } else {
    state.sensorBody = null
    // The raw payload, not areaFeatures' output: features drop `zoom`
    // entirely and fold lon/lat into GeoJSON geometry, but locateMe needs
    // exactly {slug, lon, lat, zoom} per area (see nearestArea's signature).
    state.areas = body?.areas ?? []
    setMapAreas(state.areas)
  }

  const features = effective === 'sensors'
    ? filterByStatus(sensorFeatures(body, cfg.metric, state.scales, cfg.noDataColour), getSensorStatus())
    : areaFeatures(body, cfg.metric, state.scales, cfg.noDataColour)
  map.getSource(SOURCE_ID).setData({ type: 'FeatureCollection', features })
}

// showArea is what the finder's pick does: fly to the area and select it, on
// the page the reader is already on.
//
// The area payload carries its own centre and zoom, so nothing here decides how
// close is close enough. refresh is forced because the slug changed while the
// tier may not have; the hex grid is left to the moveend the flight ends with,
// which is the only pass that knows the viewport it landed on.
export async function showArea(map, state, cfg, chrome, area) {
  if (!area || area.slug === undefined) return false
  state.slug = area.slug
  map.flyTo({ center: [area.lon, area.lat], zoom: area.zoom })
  await refresh(map, state, cfg, chrome, true)
  return true
}

// applyMarkerZoomRange moves both marker layers onto the handover the current
// tier calls for. Exported for its own test; guarded because refresh() runs on
// every moveend and a style reload can leave a layer briefly absent.
export function applyMarkerZoomRange(map, tier) {
  const max = markerMaxZoom(tier)
  for (const id of [LAYER_ID, LABEL_LAYER_ID]) {
    if (map.getLayer?.(id)) map.setLayerZoomRange(id, 0, max)
  }
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
  // The window rides on the URL, so it is also what makes the dedup below let a
  // window change through: the same viewport under a different window is a
  // different URL, and therefore a fetch rather than a repaint.
  const url = withWindow(hexesURL(map.getZoom(), map.getBounds?.()), state.window)
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
    state.hexBody, cfg.metric, bands, cfg.noDataColour, rampColour,
    resolutionForZoom(Math.round(map.getZoom())),
  )
  // The same filter the markers answer to. The grid is the tier that covers the
  // country, so leaving it out made "hide inactive sensors" a control with no
  // visible effect anywhere a reader was likely to be looking.
  map.getSource(HEX_SOURCE_ID)?.setData({
    type: 'FeatureCollection',
    features: filterByStatus(features, getSensorStatus()),
  })
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
  if (!await placeVisitor(map, state, cfg, fetchJSON)) return
  await refresh(map, state, cfg, chrome, true)
}

// How long the opening camera will wait for /api/v1/locate.
//
// The lookup runs BEFORE the first data paint (see mount), so every millisecond
// here is a millisecond of map with no readings on it. A geoip lookup that has
// not answered in this long is not worth an emptier page than the one the
// server already rendered for: past it the map draws the national view, and the
// answer — when it lands — moves it in the old way, one extra draw on a slow
// connection only.
export const LOCATE_TIMEOUT_MS = 400

// placeVisitor is locateVisitor's camera half: it decides where the map opens
// and adopts the slug that unlocks the per-area sensor tier, and paints
// nothing. Separate because the paint is the caller's to schedule — the whole
// reason the placement moved ahead of the first refresh is so there is only one
// paint, at the position the map is going to stay at.
//
// Returns whether it moved, so the caller knows whether a national-view paint
// still needs correcting later.
export async function placeVisitor(map, state, cfg, fetchJSON = getJSON, { timeoutMs = null } = {}) {
  const lookup = fetchJSON('/api/v1/locate').catch(() => null)
  const body = timeoutMs === null ? await lookup : await Promise.race([lookup, sleep(timeoutMs)])
  const located = applyLocate(body ?? null, { defaultView: { lon: cfg.lon, lat: cfg.lat, zoom: cfg.zoom } })
  if (!located.move) return false
  map.jumpTo({ center: located.centre, zoom: located.zoom })
  state.slug = located.slug
  return true
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(() => resolve(null), ms))
}

// The zoom a #sensor= link opens at.
//
// Not cfg.zoomSensor: that is only the zoom at which the map may ask for
// per-area sensor DATA, and at it the cells are still bins holding several
// devices. POINT_TIER_MIN_ZOOM is where a cell becomes one device — the floor,
// not a readable view: on a wide screen it still shows half a city, and the
// sensor the link named is one cell among hundreds. Two levels in is a
// neighbourhood, which is the scale at which "this sensor, here" reads.
export const DEEP_LINK_ZOOM = POINT_TIER_MIN_ZOOM + 2

// openDeepLinkedSensor resolves a #sensor=<id> the page was opened on into the
// view that link promises: the map at the sensor tier, over the sensor, with
// its area adopted so refresh() loads the sensors the panel then reads.
//
// The fragment never reaches the server, so this is the only moment the id can
// be acted on, and /api/v1/sensor/{id}/locate exists for exactly this question.
// Skipped entirely when the map already holds the sensor — that is the
// marker-click path, where the panel opens with no request at all.
//
// Returns whether it moved the map, so mount() can leave the visitor where the
// deep link put them rather than overriding it with a geoip placement. Any
// failure — a refusal by the enumeration limiter, a sensor the snapshot does
// not know — returns false and leaves the map exactly where it was.
// `move: false`: the cell-click path is already looking at the sensor.
export async function openDeepLinkedSensor(map, state, cfg, chrome, vs, fetchJSON = getJSON, { move = true, paint = true } = {}) {
  const id = vs.sensorId
  if (id === null || id === undefined || findSensor(id)) return false

  const body = await fetchJSON(`/api/v1/sensor/${id}/locate`).catch(() => null)
  if (typeof body?.lon !== 'number' || typeof body?.lat !== 'number') return false

  if (move) map.jumpTo({ center: [body.lon, body.lat], zoom: DEEP_LINK_ZOOM })
  // Only a real slug: a sensor outside every area still deserves the flight,
  // and adopting '' would make refresh() ask for an area page that cannot exist.
  if (body.slug) state.slug = body.slug
  // paint: false on the opening path only, where the caller paints once after
  // the camera has settled. Everywhere else this IS the paint.
  if (paint) {
    await refresh(map, state, cfg, chrome, true)
    // The cells too, and not left to the moveend jumpTo will fire: that pass is
    // debounced, and the sensor the link named is drawn by this layer.
    await refreshHexes(map, state, cfg)
  }
  return true
}

// locateMe: the precise, user-initiated fix. Stays on this page — it zooms the
// map the visitor is looking at, instead of navigating to the area page. The
// coordinate never reaches the network (see nearest.js).
export function locateMe(map, state, cfg, chrome, { geolocation = navigator.geolocation } = {}) {
  if (!geolocation) {
    chrome.showHint(cfg.t.locateFailed)
    return Promise.resolve(false)
  }
  return new Promise((resolve) => {
    geolocation.getCurrentPosition(
      (pos) => resolve(showNearestSensor(map, state, cfg, chrome, [pos.coords.longitude, pos.coords.latitude])),
      (err) => {
        // PERMISSION_DENIED === 1 per the Geolocation API.
        chrome.showHint(err?.code === 1 ? cfg.t.locateDenied : cfg.t.locateFailed)
        resolve(false)
      },
    )
  })
}

// Two jumps, not one: sensor positions are only known once the area holding the
// fix has been loaded, so the map goes to the fix first and re-centres on the
// nearest sensor after. Hexes refreshed explicitly — the moveend pass is
// debounced, and at this zoom the cells are the sensors.
export async function showNearestSensor(map, state, cfg, chrome, point) {
  // A null from nearestArea means the area list has not loaded, never
  // "outside coverage" — it has no distance cutoff. Hence locateFailed.
  if (!state.areas || state.areas.length === 0) {
    chrome.showHint(cfg.t.locateFailed)
    return false
  }
  map.jumpTo({ center: point, zoom: DEEP_LINK_ZOOM })
  state.slug = nearestArea(point, state.areas).slug
  await refresh(map, state, cfg, chrome, true)

  const sensor = nearestSensor(point, getSensors())
  if (sensor) map.jumpTo({ center: [sensor.lon, sensor.lat], zoom: DEEP_LINK_ZOOM })
  await refreshHexes(map, state, cfg)
  return true
}

export function urlFor(tier, slug) {
  if (tier === 'country') return '/api/v1/overview'
  if (tier === 'city') return '/api/v1/overview?tier=city'
  return `/api/v1/area/${encodeURIComponent(slug)}/sensors`
}

// areaFeatures maps the choropleth payload straight onto point features.
//
// covered === false renders in the neutral no-data grey with no value label.
// Fewer than three distinct STATIONS is not data — three boxes at one address
// are one place — and drawing it in a band colour
// would imply a confidence the pipeline explicitly refuses.
export function areaFeatures(body, metric, scales, noDataColour) {
  const bands = bandsFor(scales, metric)
  return (body?.areas ?? []).map((a) => ({
    type: 'Feature',
    geometry: { type: 'Point', coordinates: [a.lon, a.lat] },
    properties: {
      slug: a.slug,
      colour: a.covered ? rampColour(a.values?.[metric], bands, noDataColour) : noDataColour,
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
  const features = []
  // One dot per STATION, not per device: the two boxes at one address carry
  // the same coordinate, so a dot each drew one exactly on top of the other
  // and left the underneath one unclickable. See lib/stations.js.
  for (const { station, indices } of stationsOf(body)) {
    // The reading is the first member that HAS one for this metric — the
    // climate box has no P2 and must not paint the address grey when the
    // particulate box beside it is reporting.
    const { value } = readingAt(body, indices, metric)
    const i = indices[0]
    features.push({
      type: 'Feature',
      geometry: { type: 'Point', coordinates: [s.lon[i], s.lat[i]] },
      properties: {
        id: station,
        colour: rampColour(value, bands, noDataColour),
        value,
        quality: s.quality?.[i] ?? '',
      },
    })
  }
  return features
}

// markerMaxZoom: the zoom at which the dots hand over, for the tier the dots
// currently ARE.
//
// The two tiers hand over to different things. Province and city markers are
// aggregates, and so is the hex grid — one reading per bin instead of one per
// province, but the same kind of claim about the same ground. Drawing both left
// the map with 15 km cells and labelled aggregate dots on top of them, and with
// dots in places (Перник, Банкя) where the grid had no bin at all: two answers
// to one question, disagreeing. So an aggregate marker steps aside the moment
// the grid appears.
//
// A sensor marker does not: it is a device at its own coordinate, which the
// grid does not draw until the point tier, and it is the thing a reader clicks
// to open a panel. It runs to the point-tier handover as it always did.
export function markerMaxZoom(tier) {
  return tier === 'sensors' ? POINT_TIER_MIN_ZOOM_FRACTIONAL : GRID_MIN_ZOOM_FRACTIONAL
}

// hexOutlinePaint: the border between one cell and the next.
//
// Drawn in the label ink — a dark colour — and NOT in either of the two that
// have already been tried and failed. The cell's own `colour` outlines a fill
// in the fill's own colour, which is by definition invisible. The marker stroke
// is white, which is what lifts a dot off a dark map but disappears completely
// into a pale one: over the OSM raster it left the cells edgeless, floating.
//
// A cell has to win against a busy street map, because the reading is what the
// page is for. Full width and most of the way opaque; the streets stay legible
// around the cell and through its fill.
export function hexOutlinePaint(cfg) {
  return {
    'line-color': cfg.labelColour,
    'line-width': 1.2,
    'line-opacity': 0.7,
  }
}

// bandsFor picks the scale table for one metric. The scales endpoint returns an
// array of tables; matching on `metric` rather than on array position means a
// reordered response cannot silently recolour the map.
//
// The scale's ceiling rides on its top band. The ceiling belongs to the scale,
// not to any one band, but the only band it can change is the open one at the
// top — and every consumer downstream (the ramp, the key) is handed bands, not
// scales. Carrying it here rather than widening four signatures keeps the
// ceiling one hop from the band whose width it sets.
export function bandsFor(scales, metric) {
  if (!Array.isArray(scales)) return []
  const scale = scales.find((s) => s.metric === metric)
  const bands = scale?.bands ?? []
  if (bands.length === 0 || scale?.ceiling == null) return bands
  return bands.map((band, i) =>
    i === bands.length - 1 ? { ...band, ceiling: scale.ceiling } : band,
  )
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
    // Server-rendered: the language set is data, so no expression here could
    // tell a language segment from a page segment. "" is the default language.
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
    // One positional list, in WINDOW_CHOICES order — the same idiom as
    // data-metric-labels, and for the same reason a per-window attribute cannot
    // work: data-t-window-24h arrives in the dataset as tWindow-24h.
    windowLabels: splitAttr(d.tWindows),
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
      // The (i) beside the key and what its dialog says: the name of the
      // button, the name of the outbound link, and the standing indicative-data
      // disclaimer, which belongs anywhere the bands are explained.
      legendAbout: d.tLegendAbout || '',
      legendSource: d.tLegendSource || '',
      disclaimer: d.tDisclaimer || '',
      close: d.tClose || '',
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
      windowLabel: d.tWindowLabel || '',
      layersButton: d.tLayersButton || '',
      layersCaption: d.tLayersCaption || '',
      viewLegend: d.tViewLegend || '',
      viewBasemap: d.tViewBasemap || '',
      viewCellValues: d.tViewCellValues || '',
      viewInactiveSensors: d.tViewInactiveSensors || '',
      // Its own string, not map.layer.boundaries: that one names the basemap's
      // administrative lines, which are a different set of lines from a
      // different source and switch independently.
      viewBoundaries: d.tViewBoundaries || '',
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
      windAbout: d.tWindAbout || '',
      windNote: d.tWindNote || '',
      windAttribution: d.tWindAttribution || '',
    },
  }
}

function emptyCollection() {
  return { type: 'FeatureCollection', features: [] }
}

// glyphsURL derives the font endpoint from the configured basemap URL. A
// raster-only style still needs one: glyphs are where MapLibre gets the letter
// shapes for EVERY symbol layer, so a style without them draws no marker
// labels, no cell values and no wind arrows — the map keeps working and simply
// stops saying anything.
//
// String surgery, not `new URL()`: the endpoint is a template, and new URL
// percent-encodes the braces in {fontstack}/{range} into %7B…%7D, which
// MapLibre then requests literally and gets a 404 for.
export function glyphsURL(basemap) {
  if (!basemap) return null
  return basemap.replace(/[^/]*$/, '') + 'glyphs/{fontstack}/{range}.pbf'
}

// overlayLayers splits the self-hosted vector style into the part that may be
// drawn over the world raster and the part that may not.
//
// The archive is a BULGARIA extract. Its `background` and `fill` layers are
// opaque polygons clipped to the extract's rectangle, so laid over the raster
// they painted a box across it: inside the box the Danube ended at Silistra,
// the ground changed colour at the border, and the Black Sea carried no label
// because its own is outside the extract. Every one of those defects is a
// FILLED layer. The lines, symbols and circles — roads, boundaries, street
// names, place names, the POI categories the layers menu is built from — cover
// only what they trace, so outside the extract they simply draw nothing and
// the raster shows through.
//
// So: keep the traced layers, drop the filled ones, and let OpenStreetMap's
// own raster be the ground everywhere.
export function overlayLayers(style) {
  const layers = (style?.layers ?? []).filter((l) => l.type !== 'background' && l.type !== 'fill')
  const used = new Set(layers.map((l) => l.source))
  const sources = Object.fromEntries(
    Object.entries(style?.sources ?? {}).filter(([id]) => used.has(id)),
  )
  return { sources, layers }
}

// addBasemapOverlay fetches the vector style and lays its traced layers over
// the raster, beneath everything the map itself draws.
//
// Beneath, via beforeId: the readings are the point of the page and a POI label
// must never be drawn on top of a value. A failure is logged and leaves the
// raster standing alone — the ground is optional detail, the map is not.
export async function addBasemapOverlay(map, basemap, beforeId, fetchStyle = fetchJSON) {
  if (!basemap) return
  let style
  try {
    style = await fetchStyle(basemap)
  } catch (e) {
    console.warn('basemap detail unavailable, showing the raster alone', e)
    return
  }
  const { sources, layers } = overlayLayers(style)
  for (const [id, source] of Object.entries(sources)) {
    if (!map.getSource?.(id)) map.addSource(id, source)
  }
  const under = map.getLayer?.(beforeId) ? beforeId : undefined
  for (const l of layers) map.addLayer(l, under)
}

// registerProtocols teaches MapLibre to read pmtiles:// URLs, which is how the
// vector style references the single archive: the protocol turns each tile read
// into an HTTP range request, so a visitor transfers only the ranges their
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

async function fetchJSON(url) {
  const r = await fetch(url)
  if (!r.ok) throw new Error(`${url}: ${r.status}`)
  return r.json()
}

// mapStyle is the style the map BOOTS with: the world raster and nothing else.
// The vector detail arrives afterwards through addBasemapOverlay, which needs
// the map loaded before it can position its layers under the readings.
export function mapStyle(cfg) {
  const glyphs = glyphsURL(cfg.basemap)
  return {
    version: 8,
    ...(glyphs ? { glyphs } : {}),
    sources: { [RASTER_SOURCE_ID]: RASTER_BASEMAP },
    layers: [
      { id: 'bg', type: 'background', paint: { 'background-color': cfg.emptyBasemapColour } },
      { id: RASTER_LAYER_ID, type: 'raster', source: RASTER_SOURCE_ID },
    ],
  }
}

// installErrorHandler wires the 'error' event so a style-load failure cannot
// take the sensor markers down with it: tiles unavailable degrades to a blank
// background, it never fails the page. Logged once rather than per failed
// tile, because a missing archive produces one error per range request.
//
// Takes `map` (needs only `.on`, not a real MapLibre instance) and `warn` as
// parameters, same idiom as installErrorHandler's own injection, so a test can
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
// class of defect this file's rampColour/noDataColour split exists to
// prevent for scaled metrics. ['has', 'value'] (the shape this task's brief
// originally suggested) is ALSO wrong here, for a reason worth stating
// loudly: areaFeatures and sensorFeatures always set the `value` key, even
// when its content is null (`value: a.covered ? ... : null` /
// `column[i] ?? null`), so `has` is true unconditionally and that branch
// would be dead code, always taking the "has a reading" side.
export function markerPaint(bands, { noDataColour, unscaledColour, scaled }) {
  if (!scaled) return ['case', ['==', ['get', 'value'], null], noDataColour, unscaledColour]

  // `interpolate`, not `step`: the markers are on the same scale as the hexes
  // and must not be the one thing on the map still painted in categories.
  //
  // The stops come from rampValueStops, which puts one at every point where the
  // ramp's slope changes, so a linear blend between them is the same colour
  // rampColour computes for that value — a dot and the cell under it agree.
  // Computed here rather than per feature for the reason onMetricChange gives:
  // switching metric must not re-walk every feature.
  const stops = rampValueStops(bands)
  if (stops.length < 2) return ['case', ['==', ['get', 'value'], null], noDataColour, unscaledColour]
  return [
    'case',
    ['==', ['get', 'value'], null], noDataColour,
    ['interpolate', ['linear'], ['get', 'value'], ...stops.flatMap((s) => [s.value, s.colour])],
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
  const debounced = (...args) => {
    clearTimeout(timer)
    timer = setTimeout(() => fn(...args), ms)
  }
  // For a move the caller made itself and has already answered: the opening
  // jumpTo queues a moveend like any other, and letting it through would
  // repaint the whole map a quarter-second after it settled.
  debounced.cancel = () => clearTimeout(timer)
  return debounced
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
  // nothing else in the DOM has to record whether the key is folded. Open by
  // default — a key the reader has to find and unfold does not explain the
  // colours they are already looking at.
  //
  // The fold is remembered, like every other map preference. It is a different
  // control from the layers menu's "Legend": the menu says whether there is a
  // key at all, the triangle says whether it is unrolled, and a reader who
  // folds the key on a small screen wants it folded on the next page too.
  const legend = document.createElement('details')
  legend.className = LEGEND_CLASSES
  legend.open = readFlag(LEGEND_FOLD_KEY, true)
  legend.addEventListener('toggle', () => writeFlag(LEGEND_FOLD_KEY, legend.open))
  shell.appendChild(legend)

  // The key says which colour is worse; it cannot say what 25 µg/m³ IS, whose
  // rule that is, or where to read it. That belongs behind an (i), not on the
  // map: it is a paragraph, and the map is the page.
  //
  // On the frame rather than the shell, because a modal in fullscreen must be
  // inside the fullscreen element or the browser renders it nowhere.
  const scaleDialog = createScaleDialog(el.ownerDocument, {
    closeLabel: cfg.t.close,
    sourceLabel: cfg.t.legendSource,
    disclaimer: cfg.t.disclaimer,
    lang: cfg.lang,
  })
  el.appendChild(scaleDialog.el)

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
  // The key rides along. It is anchored to the shell (see above), and in real
  // fullscreen the frame IS the viewport — so a reader who went full screen
  // lost the colour key on the one view where the map is all there is. Moved
  // rather than duplicated: it stays one <details>, so its folded state, its
  // contents and the layers menu's "show the key" toggle all keep pointing at
  // the same element on both sides of the trip.
  mountFullscreen(el, {
    label: cfg.t.fullscreen,
    exitLabel: cfg.t.fullscreenExit,
    onChange: (full) => { (full ? el : shell).appendChild(legend) },
  })
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

  // The averaging window. Built with the chrome and wired by mount(), which
  // owns what a pick costs — see lib/mapwindow.js on why it is a menu in the
  // bottom-left cluster rather than a select across the top of the map.
  const windowMenu = mountWindow(el, {
    label: cfg.t.windowLabel,
    options: windowOptions(cfg.windowLabels),
    value: readWindow(),
  })

  // Two toggles about the SCREEN rather than about the basemap, listed above
  // the categories rather than smuggled in beside "Shops" as if they were one
  // more kind of place.
  //
  // The basemap one hides the ground — the raster and the vector detail drawn
  // over it — and only that. The kit's own version walks every layer in the
  // style, which on this map would take the readings down with it. "Hide the
  // basemap" has to leave the measurements standing, or it is not the control
  // it says it is.
  const layerViews = [
    { id: 'legend', label: cfg.t.viewLegend, apply: (on) => { legend.hidden = !on } },
    {
      id: 'cellValues',
      label: cfg.t.viewCellValues,
      // No needsMap: the cells are this island's own layer and are drawn on a
      // map served without tiles like any other.
      defaultOff: true,
      apply: (on, map) => setCellValues(map, on),
    },
    {
      id: 'inactiveSensors',
      label: cfg.t.viewInactiveSensors,
      // Off by default: a sensor that stopped reporting has no reading to show,
      // and a grid full of no-data cells reads as an empty country rather than
      // a quiet one. The subscription in mount() repaints both tiers.
      defaultOff: true,
      apply: (on) => setSensorStatus(on ? 'all' : 'active'),
    },
    {
      id: 'basemap',
      label: cfg.t.viewBasemap,
      needsMap: true,
      apply: (on, map) => {
        const v = on ? 'visible' : 'none'
        map.setLayoutProperty(RASTER_LAYER_ID, 'visibility', v)
        // The raster AND the vector detail over it: hiding one and leaving the
        // other would strand road lines and POI pins over a blank canvas.
        for (const l of map.getStyle()?.layers ?? []) {
          if (l.metadata?.['airbg:group']) map.setLayoutProperty(l.id, 'visibility', v)
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
  // this file). The click handler is wired by mount(), which is where `state`
  // (the loaded area list locateMe reads) and the real `map` first exist;
  // mountChrome only owns the DOM.
  const locateButton = mountLocate(el, { label: cfg.t.locateButton })

  // The wind overlay's disclosure. Its control is a checkbox in the layers
  // menu, with the other overlays — it was a button of its own in the corner,
  // which said the layer was a different kind of thing from the rest of what
  // the map draws when it is not. The checkbox's own checked state is what
  // announces the layer is on, since a screen reader cannot see the arrows.
  //
  // The disclosure stays a sibling of the map rather than anything inside that
  // menu: it must remain visible while the layer is, and the menu closes.
  //
  // Folded, and folded again every time the layer comes back: unrolled it is
  // two sentences and a model name over the map, which on a phone is most of
  // the screen the arrows are drawn on. The summary keeps it a line that says
  // what it is, so nothing is hidden — only rolled up.
  const windNote = document.createElement('details')
  windNote.className = 'map-wind-label'
  windNote.hidden = true
  const windSummary = document.createElement('summary')
  windSummary.className = 'map-wind-label__toggle'
  windSummary.textContent = cfg.t.windAbout || cfg.t.windToggle
  const windText = document.createElement('div')
  windText.className = 'map-wind-label__text'
  windNote.append(windSummary, windText)
  el.appendChild(windNote)

  // The precedence rule lives in hintController; this is only the wiring from
  // its decision to the banner. textContent, never innerHTML.
  const hintCtl = hintController((text) => {
    hint.textContent = text
    hint.hidden = !text
  })

  const showLegend = ({ bands, tier, metric, scale }) => {
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
      // No scale, no (i): before the tables load, and for a metric none of them
      // claim, the dialog would open on nothing.
      info: scale ? { label: cfg.t.legendAbout, onOpen: () => scaleDialog.show(scale) } : null,
    })
    const text = cfg.t.tier[tier] ?? ''
    tierLine.textContent = text
    tierLine.hidden = !text
  }

  // Drawn once at mount, before any scales have loaded, so the key is never an
  // empty overlay: with no bands that is the title and the no-data row, both of
  // which are true at that moment.
  showLegend({ bands: [], tier: null, metric: cfg.metric, scale: null })

  return {
    ...hintCtl,
    showNote(text) {
      note.textContent = text
      note.hidden = !text
    },
    showLegend,
    zoomButtons: zoom.buttons,
    windowMenu,
    layersUI: layers,
    layerViews,
    locateButton,
    // Both halves move together: the disclosure is shown exactly when the
    // arrows are, so no caller can turn one on without the other.
    showWind(on, text) {
      windText.textContent = on ? text : ''
      windNote.hidden = !on || !text
      if (!on) windNote.open = false
    },
  }
}
