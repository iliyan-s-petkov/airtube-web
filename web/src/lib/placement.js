import { getJSON } from './api.js'
import { applyLocate } from './locate.js'
import { findSensor, getSensors, setSensors } from './sensors.svelte.js'
import { nearestArea, nearestSensor } from './nearest.js'
import { setMapAreas } from './mapareas.svelte.js'
import { POINT_TIER_MIN_ZOOM } from './hexes.js'
import { refresh, refreshHexes, urlFor } from './mapdata.js'

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

// prefetchPlacement starts the request the opening camera will wait on, one
// step before anything awaits it. It asks the same URL the placement itself
// asks, and getJSON hands the same in-flight promise to both, so this costs no
// second request and its only effect is when the answer arrives.
//
// The rejection is swallowed here as well as at the call site: an unawaited
// promise that rejects is an unhandled rejection, whatever the later caller
// does with its own copy.
export function prefetchPlacement(vs, cfg, fetchJSON = getJSON) {
  const id = vs.sensorId
  if (id !== null && id !== undefined && !findSensor(id)) {
    fetchJSON(`/api/v1/sensor/${id}/locate`).catch(() => null)
    return
  }
  // Area pages open at their own centre and never ask (see mount).
  if (!cfg.slug) fetchJSON('/api/v1/locate').catch(() => null)
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

  // /locate names the area that CONTAINS the sensor; nearestArea only names
  // the one whose centre is closest, which for a sensor near a boundary is an
  // area it does not stand in — and the readout strip would then rank it
  // against neighbours it has none of. Kept as the fallback for a sensor no
  // area holds.
  const slug = body.slug || nearestArea([body.lon, body.lat], state.areas ?? [])?.slug

  // The city list, because that is the tier those slugs belong to. On a reload
  // this runs before the first refresh, so nothing has loaded one yet, and the
  // strip has no way to turn the adopted slug into a place name — it falls back
  // to counting sensors without saying where.
  if (slug && (!state.areas || state.areas.length === 0)) {
    const overview = await fetchJSON(urlFor('city')).catch(() => null)
    if (overview?.areas?.length) {
      state.areas = overview.areas
      setMapAreas(state.areas)
    }
  }
  // Only a real slug: a sensor outside every area still deserves the flight,
  // and adopting '' would make refresh() ask for an area page that cannot exist.
  if (slug) state.slug = slug
  // paint: false on the opening path only, where the caller paints once after
  // the camera has settled. Everywhere else this IS the paint.
  if (paint) {
    await refresh(map, state, cfg, chrome, true)
    // The cells too, and not left to the moveend jumpTo will fire: that pass is
    // debounced, and the sensor the link named is drawn by this layer.
    await refreshHexes(map, state, cfg)
    // An aggregate-tier refresh paints cells, not sensors, so the registry the
    // panel reads stays empty. Fill it without painting.
    if (slug && !findSensor(id)) {
      const sensorsBody = await fetchJSON(urlFor('sensors', slug)).catch(() => null)
      if (sensorsBody) setSensors(sensorsBody, slug)
    }
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
