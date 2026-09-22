// MapLibre GL JS 6.x ships no default export — only named ones (Map,
// AttributionControl, ...) — so `import maplibregl from 'maplibre-gl'` builds
// under Vitest (which does not check the export list) but fails a real Rollup
// build with MISSING_EXPORT. Importing the one class actually used avoids the
// mismatch entirely.
import { Map as MapLibreMap } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { installZoom } from '../lib/mapcontrols.js'
import { getJSON } from '../lib/api.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { readWindow } from '../lib/mapwindow.js'
import { provideAreaSelect } from '../lib/mapareas.svelte.js'
import { BOUNDARY_FILL_LAYER_ID, boundsOf, findBoundary } from '../lib/boundaries.js'
import { LAYER_ID, HEX_LAYER_ID, HEX_POINT_LAYER_ID, HEX_SOURCE_ID } from '../lib/mapids.js'
import { MIN_ZOOM, readConfig } from '../lib/mapconfig.js'
import { POINT_TIER_MIN_ZOOM } from '../lib/hexes.js'
import { registerProtocols, mapStyle, installErrorHandler } from '../lib/mapstyle.js'
import { paintWind } from '../lib/mapwind.js'
import {
  hit, boundaryChoice, highlightBoundary, cellArea, BOUNDARY_FIT_PADDING,
} from '../lib/mapboundaries.js'
import {
  MOVE_DEBOUNCE_MS, refresh, refreshHexes, showArea, debounce,
} from '../lib/mapdata.js'
import { openDeepLinkedSensor, locateMe } from '../lib/placement.js'
import { mountChrome } from '../lib/chrome.js'
import { installMapLoad } from '../lib/mapload.js'


// Mean of a polygon ring's vertices, dropping the closing vertex GeoJSON
// repeats to match the first — averaging it in would double-weight one corner.
function ringCentroid(ring) {
  const [fx, fy] = ring[0]
  const last = ring[ring.length - 1]
  const pts = (last[0] === fx && last[1] === fy) ? ring.slice(0, -1) : ring
  const sum = pts.reduce(([sx, sy], [x, y]) => [sx + x, sy + y], [0, 0])
  return [sum[0] / pts.length, sum[1] / pts.length]
}

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

  // Compact attribution starts expanded on load; collapse it to the (i) so it
  // does not sit open over the map on first paint. No private API beyond the
  // class MapLibre itself toggles.
  map.on('load', () => {
    document.querySelector('.maplibregl-ctrl-attrib.maplibregl-compact-show')
      ?.classList.remove('maplibregl-compact-show')
  })

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
  // kept. Starts false only because nothing is drawn yet; the layer menu turns
  // it on at build time (no defaultOff, see mapload.js's boundaryView), unlike
  // the wind, because the outlines are part of the map a reader is shown
  // rather than an overlay they ask for.
  const boundaryState = { on: false, body: null, loading: false }

  chrome.locateButton.addEventListener('click', () => locateMe(map, state, cfg, chrome))

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

  // The four teardowns are assigned inside installMapLoad (lib/mapload.js) and
  // read by the returned `stop`. No call site in this app ever invokes `stop`
  // today — islands mount once at page load and are never explicitly unmounted
  // (there is no SPA router, see main.js's runIsland) — so this subscription is
  // intentionally page-lifetime. Exposed anyway, the same way $effect.root's
  // teardown would be, for test hygiene and in case that ever changes.
  // One object rather than four `let`s because the handler is async: by the
  // time it runs, mount() has returned and cannot receive them.
  const subs = {}
  installMapLoad({ map, state, cfg, chrome, vs, windState, boundaryState, onMoveEnd, subs })

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

  // The cells inherit that click wherever the markers have stepped aside.
  // A cell naming one station opens the panel; a bin of several has none to
  // name, so it selects the area its centre falls nearest instead.
  map.on('click', HEX_LAYER_ID, (e) => {
    const id = e.features?.[0]?.properties?.sensorId
    if (id !== undefined && id !== null) {
      vs.openSensor(Number(id))
      // The hash alone is not the panel: it reads the registry, which a map
      // with no area selected never fills.
      openDeepLinkedSensor(map, state, cfg, chrome, vs, getJSON, { move: false })
      return
    }
    // cellArea returns null once an area is already selected — that must not
    // block the zoom-in, only the area selection it would otherwise carry.
    const slug = cellArea(state, e.lngLat)
    if (slug) state.slug = slug
    // A bin naming several stations has no single one to open, so the click
    // zooms in toward the point tier instead of selecting an area outright.
    const geom = e.features?.[0]?.geometry
    const center = geom?.type === 'Polygon' ? ringCentroid(geom.coordinates[0]) : [e.lngLat.lng, e.lngLat.lat]
    const target = Math.min(map.getZoom() + 2, POINT_TIER_MIN_ZOOM)
    if (target > map.getZoom()) {
      // moveend (already wired above) repaints markers and hexes once the
      // camera settles — a refresh here would just be repainted over.
      map.easeTo({ center, zoom: target })
      return
    }
    if (slug) refresh(map, state, cfg, chrome)
  })

  // A click that opens neither a marker nor a named cell closes the open panel.
  // Registered after the layer handlers, which claim clicks that open something.
  map.on('click', (e) => {
    if (vs.sensorId == null) return
    const feats = hit(map, e.point, [LAYER_ID, HEX_LAYER_ID])
    if (feats.some((f) => f.properties?.id != null || f.properties?.sensorId != null)) return
    vs.closeSensor()
  })

  // The hover highlight. Tracked explicitly rather than left to mouseleave:
  // sliding from a cell straight onto a point circle fires no leave, and a
  // refresh can replace the source under a cell the pointer never left.
  let hoveredHexId = null
  const clearHexHover = () => {
    if (hoveredHexId === null) return
    map.setFeatureState({ source: HEX_SOURCE_ID, id: hoveredHexId }, { hover: false })
    hoveredHexId = null
  }
  const hoverHex = (e) => {
    const id = e.features?.[0]?.id
    if (id === undefined || id === hoveredHexId) return
    clearHexHover()
    hoveredHexId = id
    map.setFeatureState({ source: HEX_SOURCE_ID, id }, { hover: true })
  }
  // Both layers: a feature is a polygon or a point, never both.
  for (const id of [HEX_LAYER_ID, HEX_POINT_LAYER_ID]) {
    map.on('mousemove', id, hoverHex)
    map.on('mouseenter', id, () => { map.getCanvas().style.cursor = 'pointer' })
    map.on('mouseleave', id, () => {
      map.getCanvas().style.cursor = ''
      clearHexHover()
    })
  }
  // A refresh replaces the source wholesale, so a held id can land on an
  // unrelated new cell. Reuses the paint event the e2e specs already listen for.
  map.getContainer?.()?.addEventListener?.('airbg:paint', (e) => {
    if (e.detail?.source === HEX_SOURCE_ID) clearHexHover()
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
    stop: () => { subs.unsubscribe?.(); subs.unprovide?.(); subs.unfilter?.(); subs.unfilterSource?.(); unselect() },
  }
}
