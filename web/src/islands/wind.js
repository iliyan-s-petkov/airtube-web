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
  return t.windAttribution
    .replace('{model}', body.model)
    .replace('{resolution}', String(body.model_resolution_deg))
    .replace('{time}', formatTime(body.valid_at))
}

function defaultFormatTime(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toISOString().slice(0, 16).replace('T', ' ') + ' UTC'
}

// arrowLayout draws the arrow as a glyph, not an image: the marker labels
// already render text through the style's own glyph source, so a character
// needs no addImage and no SDF sprite to tint. Size rather than colour carries
// the speed — the map's colour channel already means PM concentration.
export function arrowLayout() {
  return {
    'text-field': ARROW_GLYPH,
    'text-font': ['Noto Sans Regular'],
    // The glyph itself points east, so a bearing of 90 needs no rotation. The
    // property stays a compass bearing because that is what every other
    // surface calls it; the -90 is the glyph's own offset, applied once here.
    'text-rotate': ['-', ['get', 'bearing'], 90],
    'text-rotation-alignment': 'map',
    'text-allow-overlap': true,
    'text-ignore-placement': true,
    // Clamped at both ends: a calm-wind arrow must still be visible, and a
    // 15 m/s gale must not draw a glyph the size of the hex it belongs to.
    'text-size': [
      'interpolate', ['linear'], ['get', 'speed'],
      0, 12,
      15, 26,
    ],
  }
}

// U+2192, in Noto Sans's coverage — unlike the dingbat arrows, which would
// render as tofu against the same glyph source the marker labels use.
const ARROW_GLYPH = '→'

export function arrowPaint(cfg) {
  return {
    'text-color': cfg.markerStrokeColour,
    // Semi-transparent so the PM layer underneath stays the primary reading.
    'text-opacity': 0.75,
  }
}
