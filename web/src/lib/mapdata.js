import { tierFor } from './tier.js'
import { scaleFor } from './scaleinfo.js'
import { getJSON } from './api.js'
import { hasScale } from './metrics.js'
import { getSensorStatus, filterByStatus } from './sensorfilter.svelte.js'
import { filterBySource, getSources } from './sourcefilter.svelte.js'
import { setSensors, setScales } from './sensors.svelte.js'
import { setMapAreas } from './mapareas.svelte.js'
import { withWindow } from './mapwindow.js'
import {
  hexesURL, hexFeatures, resolutionForZoom, targetHexPx,
  POINT_TIER_MIN_ZOOM_FRACTIONAL,
} from './hexes.js'
import { rampColour } from './ramp.js'
import {
  SOURCE_ID, LAYER_ID, HEX_SOURCE_ID,
} from './mapids.js'
import { areaFeatures, sensorFeatures } from './mapfeatures.js'
import { applyMarkerZoomRange, bandsFor, markerPaint } from './mappaint.js'

// urlFor turns a tier into the endpoint that serves it. It lives here, beside
// refresh, so the data seam does not import from the placement seam that
// imports it back; placement calls it too, for the city overview.
export function urlFor(tier, slug) {
  if (tier === 'country') return '/api/v1/overview'
  if (tier === 'city') return '/api/v1/overview?tier=city'
  return `/api/v1/area/${encodeURIComponent(slug)}/sensors`
}

// Debounce before any tier change fires a request. One pinch-zoom gesture emits
// a dozen moveend events; undebounced, that is a dozen requests and the whole
// burst.
export const MOVE_DEBOUNCE_MS = 250

// Every data-layer repaint goes through here. The event nothing in the app
// listens to is how e2e/redraw.spec.js counts the draws a reader sees.
export function paintSource(map, sourceId, features) {
  map.getSource(sourceId)?.setData({ type: 'FeatureCollection', features })
  map.getContainer?.()?.dispatchEvent?.(new CustomEvent('airbg:paint', { detail: { source: sourceId } }))
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
export function onMetricChange(map, state, cfg, chrome, metric) {
  applyMetricColours(map, state, cfg, chrome, metric)
  // One metric's numbers, not a column per metric: it cannot be recoloured.
  state.timelapse?.reset()
  refresh(map, state, cfg, chrome, true)
  // On every call the URL is unchanged, so refreshHexes recolours the body it
  // holds rather than refetching.
  refreshHexes(map, state, cfg)
  setSourceViewAvailability(chrome, metric, cfg.t, state.coverage)
}

// The colour half of a metric change: which band table the markers are painted
// from, and the note about a metric that has none. Split out because the map's
// FIRST paint needs the colours without the two refreshes around them — at load
// the data is about to be fetched anyway, and calling the whole of
// onMetricChange for a metric nobody had changed yet was one of the redraws
// that made a reload flicker.
export function applyMetricColours(map, state, cfg, chrome, metric) {
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
// `alongside`, when given, is painted in the same pass as the markers rather
// than after them: two layers of one screen arriving a request apart is the
// second draw a reader sees.
export async function initData(map, state, cfg, chrome, place = null, alongside = null) {
  state.scales = await loadScales(chrome, cfg)
  // Published into the registry the moment it resolves (null included, on a
  // failed fetch) — see lib/sensors.svelte.js's own comment on why the panel
  // reads scales from there rather than calling loadScales a second time.
  setScales(state.scales)
  if (place) await place()
  const paints = await Promise.all([
    refresh(map, state, cfg, chrome, false, { defer: true }),
    alongside ? alongside() : null,
  ])
  for (const paint of paints) if (typeof paint === 'function') paint()
  setSourceViewAvailability(chrome, cfg.metric, cfg.t, state.coverage)
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
// `defer` returns the paint instead of performing it, so a caller loading two
// layers at once can hold both until both are ready — see onMoveEnd.
export async function refresh(map, state, cfg, chrome, force = false, { defer = false } = {}) {
  const tier = tierFor(map.getZoom(), cfg.zoomCity, cfg.zoomSensor)

  // The sensor tier needs a slug and must not invent one. With none selected,
  // fall back to the city aggregate and show the hint — a real friction cost,
  // accepted so that enumeration breadth is bounded by deliberate clicks rather
  // than by pan distance.
  const effective = tier === 'sensors' && !state.slug ? 'city' : tier
  // Held for the source-toggle handler, which recomputes the hint without a
  // refresh and cannot work the fallback out for itself.
  state.fellBack = effective !== tier
  setSourceViewAvailability(chrome, cfg.metric, cfg.t, state.coverage)
  chrome.showHint(mapHint(cfg.t, { fellBack: state.fellBack, sources: getSources() }))

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
    ? filterBySource(
      filterByStatus(sensorFeatures(body, cfg.metric, state.scales, cfg.noDataColour), getSensorStatus()),
      getSources(),
    )
    : areaFeatures(body, cfg.metric, state.scales, cfg.noDataColour)
  const paint = () => paintSource(map, SOURCE_ID, features)
  if (defer) return paint
  paint()
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

// mapHint picks the one routine hint that applies now. Both networks unticked
// outranks the select-an-area hint: it empties the map completely, and with no
// message the reader is looking at a blank canvas with nothing to explain it.
// Returns '' when neither applies, because showHint's clear-on-empty is what
// makes a hint disappear once it stops applying (see hintController in
// chrome.js).
export function mapHint(t, { fellBack, sources }) {
  if (sources && sources.size === 0) return t.noSources
  return fellBack ? t.hint : ''
}

// repaintSensors redraws the sensor tier from the payload already in hand.
// Exported for its own test, and a no-op away from the sensor tier: the filter
// is a control over sensors, so a click on it while the map is showing province
// aggregates must not blank them.
export function repaintSensors(map, state, cfg) {
  if (!state.sensorBody) return
  const features = filterBySource(
    filterByStatus(
      sensorFeatures(state.sensorBody, cfg.metric, state.scales, cfg.noDataColour),
      getSensorStatus(),
    ),
    getSources(),
  )
  paintSource(map, SOURCE_ID, features)
}

// setSourceViewAvailability notes a network that does not measure the selected
// metric at all, so an empty layer says why rather than looking broken.
//
// Never disables, and never counts. A per-metric station count used to be
// appended here; it wrapped the option onto three lines and pushed the menu out
// of shape, and the network total is already reported below the map.
export function setSourceViewAvailability(chrome, metric, t, coverage) {
  for (const [id, source] of [['communitySensors', 'sensor.community'], ['officialStations', 'eea']]) {
    const input = chrome.layersUI?.fieldset?.querySelector(`[data-layer-key="view:${id}"]`)
    if (!input) continue
    // Not the first span: the shape glyph is one too, and it sits ahead of the
    // name. Writing the label into it printed the count rotated 45 degrees.
    const span = input.parentElement?.querySelector('span:not(.colmenu__mark)')
    if (!span) continue
    // No coverage yet — the first paint runs before the grid has answered. The
    // bare label is the honest thing to show; a "0 with data" would be a claim
    // about the network rather than about what we have loaded. An empty object
    // is the same fact as a missing key, whatever the server sent.
    const per = coverage?.[source]
    if (!per || Object.keys(per).length === 0) {
      span.textContent = t[id]
      continue
    }
    const n = per[metric] ?? 0
    // Composed from catalogue parts: i18n.Catalogue.T takes no parameters, so a
    // sentence built from two strings is assembled here.
    span.textContent = n > 0 ? t[id] : `${t[id]} — ${t.notMeasured}`
  }
}

// The map's own width, read fresh on every call: a rotation changes it long
// after module load. Unknown (a test double, a detached map) reads as desktop.
function inlineSize(map) {
  return map.getContainer?.()?.clientWidth || Infinity
}

// watchHexTier calls back when a resize crosses the phone breakpoint, which is
// the only resize that changes which tier refreshHexes asks for. resize fires
// on every frame of an orientation change; the rest are repaints, not requests.
export function watchHexTier(map, onChange) {
  let tier = targetHexPx(inlineSize(map))
  const onResize = () => {
    const next = targetHexPx(inlineSize(map))
    if (next === tier) return
    tier = next
    onChange()
  }
  map.on('resize', onResize)
  return () => map.off?.('resize', onResize)
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
export async function refreshHexes(map, state, cfg, fetchJSON = getJSON, { defer = false } = {}) {
  // The window rides on the URL, so it is also what makes the dedup below let a
  // window change through: the same viewport under a different window is a
  // different URL, and therefore a fetch rather than a repaint.
  const url = withWindow(hexesURL(map.getZoom(), map.getBounds?.(), inlineSize(map)), state.window)
  if (url !== state.hexUrl) {
    // A pan superseded by another pan is answering a viewport the reader has
    // already left: cancel it rather than let it finish and be discarded.
    state.hexAbort?.abort()
    const controller = new AbortController()
    state.hexAbort = controller
    let body
    try {
      body = await fetchJSON(url, { signal: controller.signal })
    } catch (err) {
      // A pan we cancelled ourselves is not a failure to report.
      if (err?.name === 'AbortError') return
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
  // Held for the layer menu, which says how many stations of each network have
  // data for the selected metric. Read off whichever body was drawn last, so a
  // window or viewport change updates it without a second request.
  state.coverage = state.hexBody?.coverage ?? null
  const bands = bandsFor(state.scales, cfg.metric)
  // The point tier is drawn at the size this zoom would have asked the grid for
  // — rounded the same way hexesURL rounds it, so the cell the reader sees is
  // the one the URL describes. That is what keeps the grid on screen past the
  // finest published cell instead of collapsing it into marks hidden under the
  // sensor markers.
  const features = hexFeatures(
    state.hexBody, cfg.metric, bands, cfg.noDataColour, rampColour,
    resolutionForZoom(Math.round(map.getZoom()), inlineSize(map)), getSources(),
  )
  // The same filter the markers answer to. The grid is the tier that covers the
  // country, so leaving it out made "hide inactive sensors" a control with no
  // visible effect anywhere a reader was likely to be looking.
  const paint = () => paintSource(map, HEX_SOURCE_ID, filterByStatus(features, getSensorStatus()))
  if (defer) return paint
  paint()
}

// The note is the only thing telling a reader why every dot on an unscaled
// metric's map is the same colour. Returned rather than rendered here so the
// caller (onMetricChange) owns the DOM, through chrome.showNote.
export function metricNote(scales, metric, text) {
  return hasScale(scales, metric) ? '' : text
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
