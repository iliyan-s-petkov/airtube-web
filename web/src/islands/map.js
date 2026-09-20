// MapLibre GL JS 6.x ships no default export — only named ones (Map,
// AttributionControl, ...) — so `import maplibregl from 'maplibre-gl'` builds
// under Vitest (which does not check the export list) but fails a real Rollup
// build with MISSING_EXPORT. Importing the one class actually used avoids the
// mismatch entirely.
import { Map as MapLibreMap } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { installZoom } from '../lib/mapcontrols.js'
import { installLayers, LAYER_ORDER } from '../lib/maplayers.js'
import { getJSON, clearCache } from '../lib/api.js'
import { getFreshness } from '../lib/freshness.svelte.js'
import { parseMetricList, splitAttr, byMetric } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { onSensorStatusChange } from '../lib/sensorfilter.svelte.js'
import {
  getSources, onSourceChange, setSourceEnabled,
  CITIZEN_SOURCE, OFFICIAL_SOURCE,
} from '../lib/sourcefilter.svelte.js'
import { diamondImage } from '../lib/markericon.js'
import { chooseWindow, readWindow } from '../lib/mapwindow.js'
import { provideAreaSelect } from '../lib/mapareas.svelte.js'
import { GRID_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM_FRACTIONAL } from '../lib/hexes.js'
import {
  WIND_SOURCE_ID, WIND_LAYER_ID, ARROW_IMAGE_ID, arrowImage, arrowLayout, arrowPaint,
} from './wind.js'
import {
  BOUNDARY_SOURCE_ID, BOUNDARY_FILL_LAYER_ID, BOUNDARY_LINE_LAYER_ID,
  BOUNDARY_SELECTED_LAYER_ID,
  boundaryFillPaint, boundaryLinePaint, boundarySelectedPaint,
  selectedFilter, boundsOf, findBoundary,
} from '../lib/boundaries.js'
import {
  SOURCE_ID, LAYER_ID, OFFICIAL_LAYER_ID, OFFICIAL_IMAGE_ID, LABEL_LAYER_ID,
  HEX_SOURCE_ID, HEX_LAYER_ID, HEX_OUTLINE_LAYER_ID, HEX_POINT_LAYER_ID, HEX_LABEL_LAYER_ID,
} from '../lib/mapids.js'
import {
  LEGEND_FOLD_KEY, PLAY_SPEED_KEY, MIN_ZOOM, readConfig,
} from '../lib/mapconfig.js'
import { emptyCollection } from '../lib/mapfeatures.js'
import {
  markerMaxZoom, hexOutlinePaint, bandsFor,
  hexLabelPaint, layerPaint, NOT_OFFICIAL, officialLayout, officialPaint, labelLayout,
  hexLabelLayout, labelPaint, MARKER_PIXEL_RATIO,
} from '../lib/mappaint.js'
import { registerProtocols, mapStyle, installErrorHandler, addBasemapOverlay } from '../lib/mapstyle.js'
import { setWind, refreshWind, paintWind } from '../lib/mapwind.js'
import {
  hit, boundaryChoice, highlightBoundary, setBoundaries, cellArea, BOUNDARY_FIT_PADDING,
} from '../lib/mapboundaries.js'
import {
  MOVE_DEBOUNCE_MS, refresh, refreshHexes, onMetricChange, applyMetricColours, initData,
  showArea, mapHint, repaintSensors, setSourceViewAvailability, setCellValues, debounce,
  hintController,
} from '../lib/mapdata.js'
import {
  locateVisitor, openDeepLinkedSensor, placeVisitor, prefetchPlacement, LOCATE_TIMEOUT_MS,
  locateMe,
} from '../lib/placement.js'
import { installTimelapse } from '../lib/timelapse-island.js'
import { mountChrome } from '../lib/chrome.js'


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
    hexUrl: null, hexBody: null, hexAbort: null, sensorBody: null,
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
  let unfilterSource = null
  // The finder island is beside this one, not inside it: it names an area and
  // this map is what moves. Registered here, where the camera is.
  const unselect = provideAreaSelect((area) => showArea(map, state, cfg, chrome, area))

  // Declared before the 'load' handler that cancels it: a jumpTo taken during
  // the opening placement queues a moveend the handler has already answered.
  // Markers and grid answer at different latencies; painting each on arrival is
  // the map visibly redrawing itself twice per zoom. Load both, paint both.
  const onMoveEnd = debounce(async () => {
    const paints = await Promise.all([
      refresh(map, state, cfg, chrome, false, { defer: true }),
      refreshHexes(map, state, cfg, getJSON, { defer: true }),
    ])
    for (const paint of paints) paint?.()
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
      paint: hexLabelPaint(cfg),
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
      filter: NOT_OFFICIAL,
      paint: layerPaint(cfg),
    })

    // The official stations, as diamonds. A second layer rather than a second
    // paint expression because a circle layer draws circles: the shape is the
    // one thing about a marker MapLibre will not take from a property, and the
    // shape is what tells a reader which network they are looking at without a
    // click. Same source, same colour ramp, same outline — only the outline
    // changes shape, so the reading still reads the same way.
    map.addImage(OFFICIAL_IMAGE_ID, diamondImage(), { sdf: true, pixelRatio: MARKER_PIXEL_RATIO })
    map.addLayer({
      id: OFFICIAL_LAYER_ID,
      type: 'symbol',
      source: SOURCE_ID,
      maxzoom: markerMaxZoom('country'),
      filter: ['==', ['get', 'source'], OFFICIAL_SOURCE],
      layout: officialLayout(),
      paint: officialPaint(cfg),
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
    // Under the hex labels, or icon-allow-overlap paints the arrows straight
    // over the digits — see arrowLayout for why the arrows cannot yield instead.
    map.addLayer({
      id: WIND_LAYER_ID,
      type: 'symbol',
      source: WIND_SOURCE_ID,
      layout: { ...arrowLayout(), visibility: 'none' },
      paint: arrowPaint(cfg),
    }, map.getLayer?.(HEX_LABEL_LAYER_ID) ? HEX_LABEL_LAYER_ID : undefined)

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

    // One toggle per network, both on by default (no defaultOff).
    // setSourceViewAvailability labels each with its station count for the
    // selected metric.
    const sourceViews = [
      {
        id: 'communitySensors',
        label: cfg.t.viewCommunitySensors,
        // The shape the map draws this network in, shown beside its name. The
        // shapes are the only thing telling the two apart on the map itself,
        // and a key that named them in words would still leave a reader
        // guessing which of two shapes the words meant.
        mark: 'circle',
        apply: (on) => { setSourceEnabled(CITIZEN_SOURCE, on); return on },
      },
      {
        id: 'officialStations',
        label: cfg.t.viewOfficialStations,
        mark: 'diamond',
        apply: (on) => { setSourceEnabled(OFFICIAL_SOURCE, on); return on },
      },
    ]

    installLayers(map, chrome.layersUI, {
      labels: cfg.t.layers,
      caption: cfg.t.layersCaption,
      views: [...chrome.layerViews, ...sourceViews, windView, boundaryView],
    })

    setSourceViewAvailability(chrome, cfg.metric, cfg.t, state.coverage)

    // Wired here rather than in mount(), for the reason the layers menu is: a
    // pick reloads every data layer, and there is nothing to reload until the
    // sources exist. The grid comes along because it is the same readings under
    // the same markers, and a map where half the picture averaged a week and
    // the other half did not would be two answers to one question.
    // On state so a metric switch, which holds no chrome of its own, can reset it.
    state.timelapse = installTimelapse(map, state, cfg, chrome)

    chrome.windowMenu.onpick(async (name) => {
      if (!chooseWindow(state, name)) return
      await state.timelapse?.reset()
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

    unfilterSource = onSourceChange(() => {
      repaintSensors(map, state, cfg)
      // The grid too: refreshHexes short-circuits the fetch when the URL has not
      // moved, so this is a repaint, not a call.
      refreshHexes(map, state, cfg)
      // Unticking both networks empties the map, and the repaints alone would
      // leave that unexplained. Recomputed here rather than in repaintSensors
      // because the hint is chrome, not paint.
      chrome.showHint(mapHint(cfg.t, { fellBack: state.fellBack, sources: getSources() }))
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
    // Started beside the scales, not after them. The camera needs neither band
    // table, but initData awaits the scales before it calls `place`, so the
    // placement request used to queue behind them — and on a slow link the
    // national view sat on screen for that whole extra round trip before
    // jumping. getJSON dedups by URL, so `place` below adopts this very
    // promise instead of asking again.
    prefetchPlacement(vs, cfg)

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
    }, () => refreshHexes(map, state, cfg, getJSON, { defer: true }))

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

  return {
    map,
    chrome,
    stop: () => { unsubscribe?.(); unprovide?.(); unfilter?.(); unfilterSource?.(); unselect() },
  }
}

// Re-exports: task 6.1 moved this code to lib/, but map.js stays the Vite
// chunk facade and every name it exported before must still be importable
// from here. sensorFeatures is the one exception — its only external
// importer now reads it straight from lib/mapfeatures.js.
export { HEX_SOURCE_ID, HEX_LABEL_LAYER_ID } from '../lib/mapids.js'
export { LEGEND_FOLD_KEY, PLAY_SPEED_KEY, readConfig, layerLabelKey } from '../lib/mapconfig.js'
export { areaFeatures } from '../lib/mapfeatures.js'
export {
  CARRIED_OPACITY, FRESH_OPACITY, SETTLING_OPACITY, markerMaxZoom, hexOutlinePaint, bandsFor,
  hexLabelPaint, layerPaint, NOT_OFFICIAL, officialLayout, officialPaint, labelLayout,
  hexLabelLayout, labelPaint, markerPaint,
} from '../lib/mappaint.js'
export {
  setWind, refreshWind, paintWind,
} from '../lib/mapwind.js'
export {
  boundaryChoice, highlightBoundary, setBoundaries, cellArea,
} from '../lib/mapboundaries.js'
export {
  paintSource, initData, loadScales, cellTier, showArea,
  applyMarkerZoomRange, mapHint, repaintSensors, setSourceViewAvailability, refreshHexes,
  metricNote, hintController, debounce, setCellValues, urlFor,
} from '../lib/mapdata.js'
export {
  LOCATE_TIMEOUT_MS, placeVisitor, prefetchPlacement, DEEP_LINK_ZOOM, openDeepLinkedSensor,
  locateMe, showNearestSensor, locateVisitor,
} from '../lib/placement.js'
export {
  glyphsURL, overlayLayers, registerProtocols, mapStyle, installErrorHandler, addBasemapOverlay,
} from '../lib/mapstyle.js'
export { installTimelapse } from '../lib/timelapse-island.js'
export { mountChrome } from '../lib/chrome.js'
