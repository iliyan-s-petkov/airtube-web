// The forecast wind overlay. Not measured data — see docs/wind-overlay.md.

export const WIND_SOURCE_ID = 'airbg-wind'
export const WIND_LAYER_ID = 'airbg-wind-arrows'

// arrowBearing converts the meteorological direction the API reports — the
// direction the wind comes FROM — into the direction the arrow points, which is
// where the air is going. Reversing this is the classic wind-map bug and is
// invisible without a second source to check against, so it lives in one named
// function with a test rather than inline in a paint expression.
export function arrowBearing(fromDeg) {
  return (fromDeg + 180) % 360
}

// windFeatures turns the payload into arrow points. A body that does not say
// forecast: true is refused: the server marks this layer, and a client that
// drew an unmarked one would be drawing something else's data as wind.
export function windFeatures(body) {
  if (!body || body.forecast !== true || !Array.isArray(body.vectors)) return []
  return body.vectors.map((v) => ({
    type: 'Feature',
    geometry: { type: 'Point', coordinates: [v.lon, v.lat] },
    properties: {
      bearing: arrowBearing(v.direction_deg),
      speed: v.speed_ms,
    },
  }))
}

// The served field is a fixed national lattice at the snapshot's own hex
// resolution. Zoom past a city and the viewport holds one vector, then none,
// and a layer the reader switched on empties itself — which from the outside
// looks exactly like the forecast having failed.
//
// So the arrows are resampled onto a lattice sized to the screen. NEAREST
// served vector, never an interpolation: an interpolated field would show
// speeds and directions no forecast reported, and the arrows would stop being
// the model's answer. Repetition is the honest artefact, and the disclosure
// already names the model's own grid as the thing to judge the detail by.
export const WIND_SERVED_SPACING_KM = 15
const WIND_ARROW_PX = 64
export const WIND_FIELD_MAX = 600

const EARTH_RADIUS_KM = 6371
const KM_PER_DEG_LAT = (Math.PI * EARTH_RADIUS_KM) / 180
const M_PER_PX_Z0 = ((2 * Math.PI * EARTH_RADIUS_KM * 1000) / 256) * Math.cos((42.7 * Math.PI) / 180)

// The spacing that puts an arrow every WIND_ARROW_PX on screen.
function windSpacingKm(zoom) {
  return (WIND_ARROW_PX * M_PER_PX_Z0) / 2 ** zoom / 1000
}

export function windField(body, { bounds, zoom }) {
  const served = windFeatures(body)
  const spacing = windSpacingKm(zoom)
  // Zoomed out far enough that the served lattice is already denser than the
  // screen: resampling would only throw arrows away.
  if (!served.length || spacing >= WIND_SERVED_SPACING_KM) return served

  const [west, south, east, north] = bounds
  const dLat = spacing / KM_PER_DEG_LAT
  const kmPerDegLon = KM_PER_DEG_LAT * Math.cos(((north + south) / 2 * Math.PI) / 180)
  const dLon = spacing / kmPerDegLon

  // Two ways to overrun: a wide viewport, and a high zoom. Both end in the same
  // place — a lattice of tens of thousands of points, each costing a scan of
  // the served field — so the step is widened until the count fits rather than
  // the lattice being truncated, which would fill a corner and leave the rest
  // blank.
  const rows = Math.floor((north - south) / dLat) + 1
  const cols = Math.floor((east - west) / dLon) + 1
  let stride = Math.max(1, Math.ceil(Math.sqrt((rows * cols) / WIND_FIELD_MAX)))
  while (Math.ceil(rows / stride) * Math.ceil(cols / stride) > WIND_FIELD_MAX) stride++

  // A point with nothing near it draws nothing: the model stops at the coast
  // and at the border, and borrowing a reading from across that gap would put
  // arrows over water the forecast never described.
  const reach = WIND_SERVED_SPACING_KM

  const out = []
  for (let r = 0; r * stride < rows; r++) {
    for (let c = 0; c * stride < cols; c++) {
      const lat = south + r * stride * dLat
      const lon = west + c * stride * dLon
      const near = nearest(served, lon, lat, kmPerDegLon)
      if (!near || near.km > reach) continue
      out.push({
        type: 'Feature',
        geometry: { type: 'Point', coordinates: [lon, lat] },
        properties: { ...near.feature.properties },
      })
    }
  }
  return out
}

function nearest(features, lon, lat, kmPerDegLon) {
  let best = null
  for (const f of features) {
    const [flon, flat] = f.geometry.coordinates
    const dx = (flon - lon) * kmPerDegLon
    const dy = (flat - lat) * KM_PER_DEG_LAT
    const km = Math.hypot(dx, dy)
    if (!best || km < best.km) best = { km, feature: f }
  }
  return best
}

// windLabel is the persistent attribution, shown whenever the layer is on.
// Never behind a control: the point of sourcing wind from a met model rather
// than deriving it from our own sensors was to avoid presenting inference as
// measurement, and a disclosure a user has to open does not do that.
export function windLabel(body, t, formatTime = defaultFormatTime) {
  if (!body) return ''
  const attribution = t.windAttribution
    .replace('{model}', body.model)
    .replace('{resolution}', String(body.model_resolution_deg))
    .replace('{time}', formatTime(body.valid_at))
  // The note leads: a visitor who has just turned the layer on needs to know
  // what the arrows mean before they need to know which model drew them.
  return t.windNote ? `${t.windNote} ${attribution}` : attribution
}

// windIsStale asks whether the held forecast is still the current hour's.
//
// The model publishes hourly (wind.poll_interval, airbg.yaml) and the snapshot
// serves the row for the hour containing now, so a forecast only ever changes
// on an hour boundary. Anything else the refresh button reloads changes every
// five minutes; this one would return a byte-identical national grid.
//
// No body and no valid_at are both "not stale": there is nothing held to drop,
// and the next toggle fetches.
export function windIsStale(body, now = new Date()) {
  const validAt = new Date(body?.valid_at ?? NaN).getTime()
  if (Number.isNaN(validAt)) return false
  const hour = (ms) => Math.floor(ms / 3600000)
  return hour(validAt) !== hour(now.getTime())
}

function defaultFormatTime(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toISOString().slice(0, 16).replace('T', ' ') + ' UTC'
}

export const ARROW_IMAGE_ID = 'airbg-wind-arrow'

// Drawn at 48px and registered with pixelRatio 2, so it occupies 24 CSS px at
// icon-size 1 — the size the glyph used to be at its midpoint.
export const ARROW_PX = 48

// The arrow was '→' rendered through the style's glyph source, which meant the
// layer switched on and drew nothing: the served font pack answers the
// 8448-8703 range with U+2100..U+2189 and no Arrows block at all, and MapLibre
// drops a glyph it cannot find without saying so. Rasterising it here puts the
// arrow beyond the reach of whatever the tile server's font pack happens to
// cover, and needs no canvas and no new dependency to do it.
//
// Unit square, pointing east, so icon-rotate can stay a plain compass bearing:
const SHAFT = [[0.15, 0.5], [0.8, 0.5]]
const HEAD = [[[0.55, 0.28], [0.85, 0.5]], [[0.85, 0.5], [0.55, 0.72]]]
const STROKE_HALF = 0.055
const HALO_HALF = 0.105

export function arrowImage(cfg, size = ARROW_PX) {
  const ink = rgb(cfg.labelColour)
  const halo = rgb(cfg.markerStrokeColour)
  const segments = [SHAFT, ...HEAD]
  const data = new Uint8Array(size * size * 4)
  const aa = 1 / size

  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const px = (x + 0.5) / size
      const py = (y + 0.5) / size
      let d = Infinity
      for (const [a, b] of segments) d = Math.min(d, distToSegment(px, py, a, b))

      const inkCov = coverage(d, STROKE_HALF, aa)
      const haloCov = coverage(d, HALO_HALF, aa) * (1 - inkCov)
      const alpha = inkCov + haloCov
      const i = (y * size + x) * 4
      if (alpha <= 0) continue
      // Non-premultiplied, which is what addImage expects: the weighted colour
      // is divided back out by the coverage it was mixed at.
      for (let c = 0; c < 3; c++) {
        data[i + c] = Math.round((ink[c] * inkCov + halo[c] * haloCov) / alpha)
      }
      data[i + 3] = Math.round(alpha * 255)
    }
  }
  return { width: size, height: size, data }
}

function coverage(d, half, aa) {
  return Math.max(0, Math.min(1, (half - d) / aa + 0.5))
}

function distToSegment(px, py, [ax, ay], [bx, by]) {
  const dx = bx - ax
  const dy = by - ay
  const t = Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)))
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy))
}

function rgb(hex) {
  const h = hex.replace('#', '')
  const full = h.length === 3 ? h.split('').map((c) => c + c).join('') : h
  return [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16))
}

// Size rather than colour carries the speed — the map's colour channel already
// means PM concentration.
export function arrowLayout() {
  return {
    'icon-image': ARROW_IMAGE_ID,
    // The drawn arrow points east, so a bearing of 90 needs no rotation. The
    // property stays a compass bearing because that is what every other
    // surface calls it; the -90 is the image's own offset, applied once here.
    'icon-rotate': ['-', ['get', 'bearing'], 90],
    'icon-rotation-alignment': 'map',
    'icon-allow-overlap': true,
    'icon-ignore-placement': true,
    // Clamped at both ends: a calm-wind arrow must still be visible, and a
    // 15 m/s gale must not draw an arrow the size of the hex it belongs to.
    // The stops are the old 12px and 26px glyph sizes over the 24px image.
    'icon-size': [
      'interpolate', ['linear'], ['get', 'speed'],
      0, 0.5,
      15, 1.0833,
    ],
  }
}

// The colour pairing moved into the raster: the label colour drawn, the marker
// stroke colour as a halo around it. The halo, not transparency, is what keeps
// the arrows off the readings — a haloed arrow stays legible over a dark hex
// and still reads as an overlay, where a faded one disappears over both.
export function arrowPaint() {
  return { 'icon-opacity': 0.9 }
}
