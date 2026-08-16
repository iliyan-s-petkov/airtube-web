// MapLibre GL JS 6.x ships no default export — only named ones (Map,
// AttributionControl, ...) — so `import maplibregl from 'maplibre-gl'` builds
// under Vitest (which does not check the export list) but fails a real Rollup
// build with MISSING_EXPORT. Importing the one class actually used avoids the
// mismatch entirely.
import { Map as MapLibreMap, addProtocol } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { Protocol } from 'pmtiles'
import { tierFor } from '../lib/tier.js'
import { colourFor } from '../lib/colour.js'
import { getJSON } from '../lib/api.js'
import { parseMetricList, hasScale } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { setSensors, setScales } from '../lib/sensors.svelte.js'
import { applyLocate } from '../lib/locate.js'
import { nearestArea } from '../lib/nearest.js'

// Debounce before any tier change fires a request. One pinch-zoom gesture emits
// a dozen moveend events; undebounced, that is a dozen requests and the whole
// burst.
const MOVE_DEBOUNCE_MS = 250

const SOURCE_ID = 'airbg-data'
const LAYER_ID = 'airbg-markers'

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
    attributionControl: { compact: true },
  })

  installErrorHandler(map)

  // On /area/{slug} the slug is fixed, one area, ever. On / it starts empty and
  // is only ever set by a deliberate click — never derived from the viewport,
  // which would be a client-side bbox query and is exactly what the API's
  // no-bbox rule forbids.
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
  const state = { slug: cfg.slug, tier: null, scales: null, areas: null }

  chrome.locateButton.addEventListener('click', () => locateMe(state, cfg, chrome))

  // unsubscribe is assigned inside the 'load' handler (see below) and read by
  // the returned `stop`. No call site in this app ever invokes `stop` today —
  // islands mount once at page load and are never explicitly unmounted (there
  // is no SPA router, see main.js's runIsland) — so this subscription is
  // intentionally page-lifetime. Exposed anyway, the same way $effect.root's
  // teardown would be, for test hygiene and in case that ever changes.
  let unsubscribe = null

  map.on('load', async () => {
    map.addSource(SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: LAYER_ID,
      type: 'circle',
      source: SOURCE_ID,
      paint: layerPaint(cfg),
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

  map.on('moveend', debounce(() => refresh(map, state, cfg, chrome), MOVE_DEBOUNCE_MS))

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

  return { map, chrome, stop: () => unsubscribe?.() }
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
  } else {
    // The raw payload, not areaFeatures' output: features drop `zoom`
    // entirely and fold lon/lat into GeoJSON geometry, but locateMe needs
    // exactly {slug, lon, lat, zoom} per area (see nearestArea's signature).
    state.areas = body?.areas ?? []
  }

  const features = effective === 'sensors'
    ? sensorFeatures(body, cfg.metric, state.scales, cfg.noDataColour)
    : areaFeatures(body, cfg.metric, state.scales, cfg.noDataColour)
  map.getSource(SOURCE_ID).setData({ type: 'FeatureCollection', features })
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
      const area = nearestArea([pos.coords.longitude, pos.coords.latitude], state.areas)
      if (!area) {
        chrome.showHint(cfg.t.locateOutside)
        return
      }
      navigate(areaPath(area.slug))
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

// areaPath builds the area URL under whatever language prefix the visitor is
// currently on (see internal/web/pages.go's Routes: "" and "/en" are the only
// two, each area route registered once per prefix) — a plain "/area/{slug}"
// would silently switch an /en/ visitor back to the default language on
// click.
export function areaPath(slug) {
  const p = window.location.pathname
  const prefix = p === '/en' || p.startsWith('/en/') ? '/en' : ''
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
    basemap: d.basemap || '',
    // Paint values and zoom thresholds: configuration, arriving as data-*
    // attributes, no fallback here — a hardcoded fallback that numerically
    // agrees with today's airbg.yaml is exactly the duplicated constant this
    // phase removes.
    noDataColour: d.noDataColour,
    unscaledColour: d.unscaledColour,
    markerStrokeColour: d.markerStrokeColour,
    emptyBasemapColour: d.emptyBasemapColour,
    zoomCity: Number(d.zoomCity),
    zoomSensor: Number(d.zoomSensor),
    // Strings come from the server, not from a JS catalogue: Go owns the
    // catalogue, and a second copy here would drift on the first edit.
    t: {
      legend: d.tLegend || '',
      hint: d.tHint || '',
      rateLimited: d.tRateLimited || '',
      unavailable: d.tUnavailable || '',
      unscaled: d.tUnscaled || '',
      locateButton: d.tLocateButton || '',
      locateDenied: d.tLocateDenied || '',
      locateFailed: d.tLocateFailed || '',
      locateOutside: d.tLocateOutside || '',
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
function mountChrome(el, cfg) {
  const legend = document.createElement('div')
  legend.className = 'map-legend'
  legend.textContent = cfg.t.legend
  el.appendChild(legend)

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

  // The precedence rule lives in hintController; this is only the wiring from
  // its decision to the banner. textContent, never innerHTML.
  const hintCtl = hintController((text) => {
    hint.textContent = text
    hint.hidden = !text
  })

  return {
    ...hintCtl,
    showNote(text) {
      note.textContent = text
      note.hidden = !text
    },
    locateButton,
  }
}
