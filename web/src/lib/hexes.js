// The hex grid's client half: which resolution a zoom asks for, which viewport
// it asks about, and how a bin centre becomes a drawable hexagon.
//
// The projection constants below are a DELIBERATE DUPLICATE of
// internal/snapshot/hexes.go (hexRefLat, earthRadiusKM, hexSizeOf, hexCentre).
// They cannot be imported — one is Go on the server, the other JS in the
// browser — and they must agree, or the drawn cell sits off the ground its
// count came from. hexes.test.js pins the two together against values taken
// from the Go implementation; change one side and that test fails.
import { OFFICIAL_SOURCE, sourceOf } from './sourcefilter.svelte.js'

const EARTH_RADIUS_KM = 6371
const HEX_REF_LAT = 42.75

// Target on-screen width of one hex, in CSS pixels: the cell stays about this
// big at every zoom. See docs/map-rendering.md for why it is not larger.
export const TARGET_HEX_PX = 32

// Metres per pixel at zoom 0 at the reference latitude — the standard Web
// Mercator figure, 2*pi*R/256, narrowed by cos(lat).
const M_PER_PX_Z0 = (2 * Math.PI * EARTH_RADIUS_KM * 1000) / 256 * Math.cos(radians(HEX_REF_LAT))

// Below this zoom the viewport holds more than the country, so a bounding box
// describes nothing and only fragments the cache. Requests there carry no bbox
// and hit the one pre-encoded country-wide body the server built at ingest.
export const BBOX_MIN_ZOOM = 8

// The finest cell the server publishes, in km — the last entry of
// snapshot.HexTiersKM. Another deliberate duplicate, for the same reason as the
// projection constants above, and it earns its keep: past this size the server
// has no smaller bin to snap to, so asking for one only re-requests the 250 m
// grid under a different URL. That is where the point tier begins instead.
const FINEST_TIER_KM = 0.25

// The resolution that means "not a grid at all": one entry per sensor, each
// naming its device. Zero is the limit of the tier list — a cell small enough
// to hold one sensor IS that sensor.
export const POINT_RESOLUTION_KM = 0

// The first whole zoom at which hexesURL asks for devices rather than bins.
// Derived from the two constants that decide it, never restated as a literal:
// retarget TARGET_HEX_PX or publish a finer tier and this follows.
//
// It is the map's handover point. Below it a marker is the only thing that
// carries a reading; at and above it the cells are individually visible and
// carry it themselves, so the markers step aside rather than sit labelled and
// off-centre inside a labelled cell.
export const POINT_TIER_MIN_ZOOM = (() => {
  let z = 0
  while (resolutionForZoom(z) >= FINEST_TIER_KM) z++
  return z
})()

// The COARSEST cell the server publishes, in km — the first entry of
// snapshot.HexTiersKM, and a deliberate duplicate for the same reason
// FINEST_TIER_KM is one.
const COARSEST_TIER_KM = 100

// The smallest a cell may be drawn before it stops reading as a cell, in screen
// pixels. A hexagon under about this size is a speck: its colour is still there
// but its shape, its border and any number inside it are not.
//
// This is what decides where the grid stops, NOT whether the coarsest tier is
// as big as the zoom would ideally like. Those are different questions, and
// answering the second one is what previously turned the grid off at the
// national view: at zoom 7 the ideal cell is ~45 km wide, and a 15 km bin drawn
// there is a third of that — small, but 17 px and perfectly legible. Turning
// the grid off at the very zoom the country fits on screen is the defect that
// rule produced.
const MIN_HEX_PX = 8

// The first whole zoom at which the coarsest published bin is still big enough
// to read. Below it there is nothing coarser for the server to snap to, so the
// same bins would be drawn smaller and smaller until the grid is a field of
// specks; there the area markers carry the map alone.
export const GRID_MIN_ZOOM = (() => {
  const drawnPx = (z) => (TARGET_HEX_PX * COARSEST_TIER_KM) / resolutionForZoom(z)
  let z = 0
  while (drawnPx(z) < MIN_HEX_PX) z++
  return z
})()

// hexesURL picks a tier from Math.round(zoom); MapLibre applies a layer's
// minzoom/maxzoom to the TRUE fractional zoom. So a layer range written against
// a whole tier zoom is half a level out of step with the data in it, and in
// that half-level the map draws one tier's cells under another tier's markers.
// Half a level down is where the rounding actually flips.
const TIER_HANDOVER = 0.5

// The two zooms where the map hands over: grid on, and markers off. Both are
// used on BOTH sides of their handover, so the layer that appears and the layer
// that disappears cannot be written half a level apart.
export const GRID_MIN_ZOOM_FRACTIONAL = GRID_MIN_ZOOM - TIER_HANDOVER
export const POINT_TIER_MIN_ZOOM_FRACTIONAL = POINT_TIER_MIN_ZOOM - TIER_HANDOVER

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
  const res = resolutionForZoom(z)
  const bbox = bboxParam(z, bounds)

  // Past the finest published cell, the grid stops and individual sensors
  // begin. Conditional on HAVING a box, not merely on the zoom: the server
  // refuses resolution_km=0 without one — deliberately, so an unbounded request
  // cannot become a national device registry — and a request we know will 400
  // is not worth sending. Without a box we ask for the finest grid instead,
  // which is still a map.
  if (res < FINEST_TIER_KM && bbox) {
    return `/api/v1/hexes?${new URLSearchParams({
      resolution_km: String(POINT_RESOLUTION_KM), bbox,
    })}`
  }

  const params = new URLSearchParams({ resolution_km: String(round(res, 4)) })
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
 * diamondPolygon is the point tier's official-station cell: a square on its
 * corner, inscribed in the hexagon the same station would have drawn.
 *
 * The two networks are two different claims — one is a reference instrument the
 * state operates, the other is a box on somebody's balcony — and on a map where
 * every cell is the same shape a reader has no way to tell which they are
 * reading. It matches the diamond the sensor markers draw for the same network,
 * so the shape means the same thing at every zoom.
 *
 * Inscribed rather than circumscribed: the half-diagonal is the hexagon's own
 * inradius, so the diamond fits inside the lattice cell and can never reach a
 * neighbour's ground.
 */
export function diamondPolygon(lon, lat, resKM) {
  const r = resKM / 2
  const dLon = degrees(r / (EARTH_RADIUS_KM * Math.cos(radians(HEX_REF_LAT))))
  const dLat = degrees(r / EARTH_RADIUS_KM)
  return [
    [lon, lat + dLat],
    [lon + dLon, lat],
    [lon, lat - dLat],
    [lon - dLon, lat],
    [lon, lat + dLat],
  ]
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
 *
 * `pointResKM` is the size to draw the POINT tier at, and it exists because the
 * grid must not vanish under the reader. Drawn as bare marks, the devices sat
 * under the labelled sensor markers already on the map, so the zoom step past
 * the finest published cell turned a street full of hexagons into an apparently
 * empty one. Sized from the zoom (resolutionForZoom), the cell keeps its
 * on-screen size and shrinks on the ground exactly as the tiers above it do.
 *
 * It applies to the point tier ONLY. An aggregate cell is the server's bin and
 * is drawn at the size the server binned it to, or the cells stop tiling the
 * ground their counts describe.
 */
export function hexFeatures(body, metric, bands, noDataColour, colourOf, pointResKM = 0, enabled = null) {
  // Read as a number rather than coerced with Number(): now that zero is a
  // meaningful tier rather than nonsense, Number(null) and Number('') would
  // both land on it, and a malformed response would be drawn as a street full
  // of devices instead of as nothing.
  const resKM = typeof body?.resolution_km === 'number' ? body.resolution_km : NaN
  // Negative and NaN are still nothing to draw; zero is the point tier.
  if (!(resKM >= 0)) return []
  const points = resKM === POINT_RESOLUTION_KM
  // The size a feature is actually drawn at: the server's bin on every
  // aggregate tier, and the caller's zoom-derived size on the point tier.
  const drawKM = points ? pointResKM : resKM

  // Which numbers a cell reports under the current network toggles. `null`
  // means no filter is in play (the timelapse, whose frames carry no source) and
  // takes the blended values the payload leads with. A cell with nothing left to
  // report is dropped rather than drawn grey: it holds no reading from any
  // enabled network, which is not the same fact as a silent sensor.
  const pick = (h) => {
    if (enabled === null) return { values: h.values, n: h.n }
    if (enabled.size === 0) return null
    if (enabled.size > 1 && h.by_source) return { values: h.values, n: h.n }
    if (h.by_source) {
      for (const src of enabled) {
        const part = h.by_source[src]
        if (part) return { values: part.values, n: part.n }
      }
      return null
    }
    return enabled.has(sourceOf(h)) ? { values: h.values, n: h.n } : null
  }

  // No-data cells first, and the served order kept within each group.
  //
  // A station is two sensor ids at ONE pair of coordinates — the dust sensor
  // and its climate twin — so on a PM metric the twin is a full no-data cell
  // sitting exactly on top of a real reading, and on the point tier the two are
  // drawn at the same size. Nothing else decides which wins: MapLibre paints a
  // fill layer in feature order, so whichever the server listed last covered
  // the other, and a street of readings came out speckled grey. The label layer
  // filters value != null and so kept showing the reading underneath, which is
  // what made it look like the ramp had failed rather than like two cells.
  //
  // A stable partition rather than a full sort: two co-located sensors that BOTH
  // report cannot be separated by anything here, and reordering them between
  // refreshes would just move the coin toss around.
  // Filtered BEFORE the point tier's merge, never after. A network is a property
  // of the device, and snapToLattice returns a new cell that no longer has one —
  // so filtering the merged cells read every one of them as the default network
  // and emptied the official layer at the one zoom where a station is finally a
  // thing of its own. Filtering first also makes the merge honest: a cell holding
  // one station of each network averages only the ones the reader left on.
  const picked = []
  for (const h of body?.hexes ?? []) {
    const p = pick(h)
    if (p) picked.push({ ...h, values: p.values, n: p.n })
  }
  const hexes = points && drawKM > 0 ? snapToLattice(picked, drawKM) : picked
  const ordered = [
    ...hexes.filter((h) => (h.values?.[metric] ?? null) === null),
    ...hexes.filter((h) => (h.values?.[metric] ?? null) !== null),
  ]

  return ordered.map((h) => {
    const value = h.values?.[metric] ?? null
    return {
      type: 'Feature',
      // A device with no size to draw at stays a Point: it is a position we
      // were given outright, and inventing a cell radius for it would be
      // inventing the one thing a cell claims. The two geometries share a
      // source — MapLibre draws each layer only over the geometry type it
      // paints, so the fill and outline skip points and the circle layer skips
      // cells.
      geometry: drawKM > 0
        ? { type: 'Polygon', coordinates: [ringFor(points, h, drawKM)] }
        : { type: 'Point', coordinates: [h.lon, h.lat] },
      properties: {
        colour: colourOf(value, bands, noDataColour),
        value,
        n: h.n,
        // The network, carried so the layers can shape and outline a cell by it.
        // Only the point tier has one to carry: an aggregate cell is a bin, and
        // a bin under both networks belongs to neither.
        source: points ? sourceOf(h) : undefined,
        // Undefined on every aggregate tier, so a popup can tell a device from
        // a bin without also having to know which resolution it asked for.
        sensorId: h.sensor_id,
      },
    }
  })
}

// ringFor picks a cell's outline: a diamond for an official station on the
// point tier, the lattice hexagon for everything else. An aggregate bin is
// always a hexagon — it tiles the ground its count came from.
function ringFor(points, h, drawKM) {
  if (points && sourceOf(h) === OFFICIAL_SOURCE) return diamondPolygon(h.lon, h.lat, drawKM)
  return hexPolygon(h.lon, h.lat, drawKM)
}

// snapToLattice moves each device onto the hex lattice of the size the point
// tier is being DRAWN at, and merges the devices that land on the same cell.
//
// The point tier is the one tier whose cell size does not come from the data:
// the zoom picks it (resolutionForZoom), and sensors are wherever people put
// them. Two devices closer together than that size were two hexagons centred on
// two positions, so they overlapped — the one shape the grid makes nowhere
// else, and one that reads as a broken renderer rather than as two sensors on
// one street. On the lattice, cells either coincide or tile.
//
// Merging is what the aggregate tiers do a resolution higher, done here for the
// same reason: a cell stands for the ground under it, so it says what every
// device standing there says. It also subsumes the climate twin — a station's
// two sensor ids share one pair of coordinates and so one cell, and the twin's
// missing PM reading stops being a grey cell laid over its sibling's.
function snapToLattice(hexes, drawKM) {
  const cells = new Map()
  for (const h of hexes) {
    const at = latticeCell(h.lon, h.lat, drawKM)
    // Keyed by network as well as by cell, for the same reason the server groups
    // its stations that way: a cell drawn as one network's shape while holding
    // the other's reading is the one merge the toggles could not survive.
    const source = sourceOf(h)
    const key = `${at.key}|${source}`
    let cell = cells.get(key)
    if (!cell) {
      cell = { lon: at.lon, lat: at.lat, n: 0, sensor_id: h.sensor_id, source, sums: new Map() }
      cells.set(key, cell)
    }
    cell.n += h.n ?? 0
    for (const [metric, value] of Object.entries(h.values ?? {})) {
      if (typeof value !== 'number') continue
      const sum = cell.sums.get(metric) ?? { total: 0, count: 0 }
      sum.total += value
      sum.count++
      cell.sums.set(metric, sum)
    }
  }
  return [...cells.values()].map((c) => ({
    lon: c.lon,
    lat: c.lat,
    n: c.n,
    sensor_id: c.sensor_id,
    source: c.source,
    values: Object.fromEntries([...c.sums].map(([m, s]) => [m, s.total / s.count])),
  }))
}

// latticeCell rounds a position to the centre of the hexagon containing it, in
// the same pointy-top layout hexPolygon draws and by the same projection, so a
// snapped centre is a centre hexPolygon can tile from. The axial rounding is
// the standard one: round all three cube coordinates and repair whichever moved
// furthest, which is what keeps q + r + s at zero.
function latticeCell(lon, lat, resKM) {
  const size = resKM / Math.sqrt(3)
  const lonKM = EARTH_RADIUS_KM * Math.cos(radians(HEX_REF_LAT))
  const x = radians(lon) * lonKM
  const y = radians(lat) * EARTH_RADIUS_KM

  const qf = ((Math.sqrt(3) / 3) * x - y / 3) / size
  const rf = ((2 / 3) * y) / size
  const sf = -qf - rf
  let q = Math.round(qf)
  let r = Math.round(rf)
  const s = Math.round(sf)
  const dq = Math.abs(q - qf)
  const dr = Math.abs(r - rf)
  const ds = Math.abs(s - sf)
  if (dq > dr && dq > ds) q = -r - s
  else if (dr > ds) r = -q - s

  return {
    key: `${q},${r}`,
    lon: degrees((size * Math.sqrt(3) * (q + r / 2)) / lonKM),
    lat: degrees((size * 1.5 * r) / EARTH_RADIUS_KM),
  }
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
