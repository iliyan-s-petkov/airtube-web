import { addProtocol } from 'maplibre-gl'
import { Protocol } from 'pmtiles'
import { RASTER_SOURCE_ID, RASTER_LAYER_ID } from './mapids.js'

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
export const RASTER_BASEMAP = {
  type: 'raster',
  tiles: ['https://tile.openstreetmap.org/{z}/{x}/{y}.png'],
  tileSize: 256,
  minzoom: 0,
  maxzoom: 19,
  attribution: '© OpenStreetMap contributors',
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
