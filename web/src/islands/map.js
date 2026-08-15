// MapLibre GL JS 6.x ships no default export — only named ones (Map,
// AttributionControl, ...) — so `import maplibregl from 'maplibre-gl'` builds
// under Vitest (which does not check the export list) but fails a real Rollup
// build with MISSING_EXPORT. Importing the one class actually used avoids the
// mismatch entirely.
import { Map as MapLibreMap } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { tierFor } from '../lib/tier.js'
import { colourFor } from '../lib/colour.js'
import { getJSON } from '../lib/api.js'

// Debounce before any tier change fires a request. One pinch-zoom gesture emits
// a dozen moveend events; undebounced, that is a dozen requests and the whole
// burst.
const MOVE_DEBOUNCE_MS = 250

const SOURCE_ID = 'airbg-data'
const LAYER_ID = 'airbg-markers'

export function mount(el) {
  const cfg = readConfig(el)
  const chrome = mountChrome(el, cfg)

  const map = new MapLibreMap({
    container: el,
    // An unset style URL is not fatal: the map renders data markers over a
    // plain background, so local development needs no vendor account.
    style: cfg.basemap ? cfg.basemap : blankStyle(cfg.emptyBasemapColour),
    center: [cfg.lon, cfg.lat],
    zoom: cfg.zoom,
    attributionControl: { compact: true },
  })

  // On /area/{slug} the slug is fixed, one area, ever. On / it starts empty and
  // is only ever set by a deliberate click — never derived from the viewport,
  // which would be a client-side bbox query and is exactly what the API's
  // no-bbox rule forbids.
  const state = { slug: cfg.slug, tier: null, scales: null }

  map.on('load', async () => {
    map.addSource(SOURCE_ID, { type: 'geojson', data: emptyCollection() })
    map.addLayer({
      id: LAYER_ID,
      type: 'circle',
      source: SOURCE_ID,
      paint: {
        // Colour is resolved per feature in JS and carried on the feature, so
        // the band table stays the server's business. A MapLibre `step`
        // expression built from the scales would work too, but it would put the
        // band thresholds into the style — a second place they could drift.
        'circle-color': ['get', 'colour'],
        'circle-radius': ['interpolate', ['linear'], ['zoom'], 5, 5, 12, 9],
        'circle-stroke-width': 1,
        'circle-stroke-color': cfg.markerStrokeColour,
      },
    })

    await initData(map, state, cfg, chrome)
  })

  map.on('moveend', debounce(() => refresh(map, state, cfg, chrome), MOVE_DEBOUNCE_MS))

  // Clicking an aggregate marker is what selects an area — the deliberate act
  // the enumeration budget is denominated in.
  map.on('click', LAYER_ID, (e) => {
    const slug = e.features?.[0]?.properties?.slug
    if (!slug) return
    state.slug = slug
    refresh(map, state, cfg, chrome)
  })
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
async function refresh(map, state, cfg, chrome) {
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
  if (key === state.tier) return

  let body
  try {
    body = await getJSON(url)
  } catch (err) {
    chrome.showHint(cfg.t.unavailable)
    console.error('map data:', err)
    return
  }
  state.tier = key

  const features = effective === 'sensors'
    ? sensorFeatures(body, cfg.metric, state.scales, cfg.noDataColour)
    : areaFeatures(body, cfg.metric, state.scales, cfg.noDataColour)
  map.getSource(SOURCE_ID).setData({ type: 'FeatureCollection', features })
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
    zoom: Number(d.zoom ?? 7),
    lon: Number(d.lon ?? 25.4858),
    lat: Number(d.lat ?? 42.7339),
    metric: d.metric || 'P2',
    basemap: d.basemap || '',
    // Paint values and zoom thresholds: configuration, arriving as data-*
    // attributes, no fallback here — a hardcoded fallback that numerically
    // agrees with today's airbg.yaml is exactly the duplicated constant this
    // phase removes.
    noDataColour: d.noDataColour,
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
    },
  }
}

function emptyCollection() {
  return { type: 'FeatureCollection', features: [] }
}

// blankStyle is a valid MapLibre style with no tile sources, used when no
// basemap is configured. Data markers still render, over a plain background
// painted the server-configured emptyBasemapColour.
function blankStyle(emptyBasemapColour) {
  return { version: 8, sources: {}, layers: [{ id: 'bg', type: 'background', paint: { 'background-color': emptyBasemapColour } }] }
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

  // The precedence rule lives in hintController; this is only the wiring from
  // its decision to the banner. textContent, never innerHTML.
  return hintController((text) => {
    hint.textContent = text
    hint.hidden = !text
  })
}
