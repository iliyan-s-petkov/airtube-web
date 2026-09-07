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
