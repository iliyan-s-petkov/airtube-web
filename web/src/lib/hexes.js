// The hex grid's client half: which resolution a zoom asks for, which viewport
// it asks about, and how a bin centre becomes a drawable hexagon.
//
// The projection constants below are a DELIBERATE DUPLICATE of
// internal/snapshot/hexes.go (hexRefLat, earthRadiusKM, hexSizeOf, hexCentre).
// They cannot be imported — one is Go on the server, the other JS in the
// browser — and they must agree, or the drawn cell sits off the ground its
// count came from. hexes.test.js pins the two together against values taken
// from the Go implementation; change one side and that test fails.
const EARTH_RADIUS_KM = 6371
const HEX_REF_LAT = 42.75

// Target on-screen width of one hex, in CSS pixels. The whole point of the
// tiered grid: the cell stays about this big at every zoom, so the map reads
// the same whether it shows a country or a street. Roughly what
// maps.sensor.community draws.
const TARGET_HEX_PX = 50

// Metres per pixel at zoom 0 at the reference latitude — the standard Web
// Mercator figure, 2*pi*R/256, narrowed by cos(lat).
const M_PER_PX_Z0 = (2 * Math.PI * EARTH_RADIUS_KM * 1000) / 256 * Math.cos(radians(HEX_REF_LAT))

// Below this zoom the viewport holds more than the country, so a bounding box
// describes nothing and only fragments the cache. Requests there carry no bbox
// and hit the one pre-encoded country-wide body the server built at ingest.
export const BBOX_MIN_ZOOM = 8

// The grid a requested bounding box is snapped out to, in degrees. Raw viewport
// edges would give every pixel of pan its own URL and no two visitors would ever
// share a cache entry. Snapped OUTWARD on all four sides, never inward, so the
// box still covers everything on screen.
const BBOX_QUANTUM_DEG = 0.05

/**
 * resolutionForZoom returns the cell size, in km, that draws at about
 * TARGET_HEX_PX at this zoom.
 *
 * Returned raw, not rounded onto a tier: the server owns the tier list
 * (snapshot.HexTiersKM) and snaps whatever it is given. Duplicating the list
 * here would let the two drift, and the client has no need to know it — the
 * response states the resolution it was actually served at, which is the number
 * the geometry is then built from.
 */
export function resolutionForZoom(zoom) {
  return (TARGET_HEX_PX * M_PER_PX_Z0) / 2 ** zoom / 1000
}

/**
 * hexesURL builds the request for a zoom and a viewport.
 *
 * bounds is MapLibre's LngLatBounds, or anything with the same four getters;
 * null or a zoom below BBOX_MIN_ZOOM yields the unclipped country-wide URL.
 */
export function hexesURL(zoom, bounds) {
  // Rounded to a whole zoom level first, for the same reason the bbox is snapped
  // to a grid: MapLibre reports a fractional zoom that changes on every frame of
  // a flyTo, and an unrounded resolution gives each of those frames its own URL.
  // Measured on the live site, one zoom-to-street flight spent three separate
  // requests on 44.85, 40.89 and 37.29 km — three URLs the server answers with
  // the identical 15 km body. A whole level is the finest distinction worth
  // making: it changes the cell by 2x, which is a tier, and everything between
  // is the same picture.
  const z = Math.round(zoom)
  const params = new URLSearchParams({ resolution_km: String(round(resolutionForZoom(z), 4)) })
  const bbox = bboxParam(z, bounds)
  if (bbox) params.set('bbox', bbox)
  return `/api/v1/hexes?${params}`
}

/**
 * bboxParam formats a viewport as the server's "w,s,e,n", snapped outward onto
 * BBOX_QUANTUM_DEG and clamped to the legal lon/lat range. Returns '' when no
 * box should be sent at all.
 */
export function bboxParam(zoom, bounds) {
  if (!bounds || zoom < BBOX_MIN_ZOOM) return ''
  const w = Math.max(-180, quantise(bounds.getWest(), Math.floor))
  const s = Math.max(-90, quantise(bounds.getSouth(), Math.floor))
  const e = Math.min(180, quantise(bounds.getEast(), Math.ceil))
  const n = Math.min(90, quantise(bounds.getNorth(), Math.ceil))
  // A box the server would reject — an antimeridian-crossing viewport gives
  // west > east — is dropped rather than sent. The server would discard it and
  // answer country-wide anyway; not sending it keeps that a cache hit.
  if (!(w < e && s < n)) return ''
  return [w, s, e, n].map((v) => round(v, 4)).join(',')
}

/**
 * hexPolygon returns one bin's hexagon as a GeoJSON ring: six corners plus the
 * repeated first, in the order MapLibre expects.
 *
 * Pointy-top, matching the server's axial layout — corners at 30° + 60°k, so a
 * vertex points north and the flat sides face east and west. The circumradius
 * is resKM/sqrt(3), which is what makes the horizontal centre-to-centre spacing
 * exactly resKM.
 */
export function hexPolygon(lon, lat, resKM) {
  const size = resKM / Math.sqrt(3)
  const ring = []
  for (let i = 0; i < 6; i++) {
    const angle = radians(60 * i + 30)
    // The projection is linear in both axes, so a corner offset in projected
    // kilometres converts to a degree offset from the centre directly — no need
    // to project the centre and unproject the corner.
    const dLon = degrees((size * Math.cos(angle)) / (EARTH_RADIUS_KM * Math.cos(radians(HEX_REF_LAT))))
    const dLat = degrees((size * Math.sin(angle)) / EARTH_RADIUS_KM)
    // Unrounded, unlike the centres the server sends: these coordinates go
    // straight into MapLibre as numbers and are never serialised, so rounding
    // would buy no bytes and would cost visible precision at the 250 m tier,
    // where a corner offset is only ~0.0015°.
    ring.push([lon + dLon, lat + dLat])
  }
  ring.push(ring[0])
  return ring
}

/**
 * hexFeatures turns a /api/v1/hexes body into coloured polygons.
 *
 * The cell size comes from the RESPONSE's resolution_km, never from the value
 * the client asked for: the server snaps the request onto a published tier, so
 * those two differ on most requests and drawing at the requested size would
 * paint cells that overlap or leave gaps between the bins they describe.
 *
 * A bin with no reading for the current metric keeps its cell — the count is
 * still true and the cell still marks where sensors are — but takes the no-data
 * colour, the same rule the area markers follow.
 */
export function hexFeatures(body, metric, bands, noDataColour, colourFor) {
  const resKM = Number(body?.resolution_km)
  if (!(resKM > 0)) return []
  return (body?.hexes ?? []).map((h) => {
    const value = h.values?.[metric] ?? null
    return {
      type: 'Feature',
      geometry: { type: 'Polygon', coordinates: [hexPolygon(h.lon, h.lat, resKM)] },
      properties: {
        colour: colourFor(value, bands, noDataColour),
        value,
        n: h.n,
      },
    }
  })
}

function quantise(deg, roundFn) {
  return roundFn(deg / BBOX_QUANTUM_DEG) * BBOX_QUANTUM_DEG
}

function radians(d) {
  return (d * Math.PI) / 180
}

function degrees(r) {
  return (r * 180) / Math.PI
}

function round(v, places) {
  const f = 10 ** places
  return Math.round(v * f) / f
}
