// MapLibre GL JS 6.x ships no default export — only named ones (Map,
// AttributionControl, ...) — so `import maplibregl from 'maplibre-gl'` builds
// under Vitest (which does not check the export list) but fails a real Rollup
// build with MISSING_EXPORT. Importing the one class actually used avoids the
// mismatch entirely.
import { Map as MapLibreMap } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import { tierFor } from '../lib/tier.js'
import { colourFor, NO_DATA_COLOUR } from '../lib/colour.js'
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
    style: cfg.basemap ? cfg.basemap : blankStyle(),
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
        'circle-stroke-color': '#ffffff',
      },
    })

    // Fetched once per page load and Cache-Control: public, so it costs nothing
    // on a repeat visit.
    state.scales = await getJSON('/api/v1/scales').catch(() => null)

    await refresh(map, state, cfg, chrome)
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

// refresh fetches the tier the current zoom permits and repaints.
async function refresh(map, state, cfg, chrome) {
  const tier = tierFor(map.getZoom())

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
    ? sensorFeatures(body, cfg.metric, state.scales)
    : areaFeatures(body, cfg.metric, state.scales)
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
export function areaFeatures(body, metric, scales) {
  const bands = bandsFor(scales, metric)
  return (body?.areas ?? []).map((a) => ({
    type: 'Feature',
    geometry: { type: 'Point', coordinates: [a.lon, a.lat] },
    properties: {
      slug: a.slug,
      colour: a.covered ? colourFor(a.values?.[metric], bands) : NO_DATA_COLOUR,
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
export function sensorFeatures(body, metric, scales) {
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
        colour: colourFor(value, bands),
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
    // Strings come from the server, not from a JS catalogue: Go owns the
    // catalogue, and a second copy here would drift on the first edit.
    t: {
      legend: d.tLegend || '',
      noData: d.tNoData || '',
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
// basemap is configured. Data markers still render, over a plain background.
function blankStyle() {
  return { version: 8, sources: {}, layers: [{ id: 'bg', type: 'background', paint: { 'background-color': '#eef2f5' } }] }
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

  return {
    showHint(text) {
      hint.textContent = text
      hint.hidden = !text
    },
  }
}
