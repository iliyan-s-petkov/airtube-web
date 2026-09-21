// Pure gauge model for Gauge.svelte; no DOM.
import { rampColour } from './ramp.js'
import { hasScale } from './metrics.js'

// Arc floors for metrics whose first band is open below; everything else starts at 0.
export const FLOORS = { temperature: -20, pressure: 930 }

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value))
}

// gaugeRange(metric, bands, ceiling) -> { min, max }, the arc's own scale.
// The top open band (upper: null) is given one band-width past the last
// stated boundary, clamped to the server's ceiling.
export function gaugeRange(metric, bands, ceiling) {
  const min = FLOORS[metric] ?? 0
  const u = bands.map((b) => b.upper).filter(Number.isFinite)
  let max
  if (u.length >= 2) {
    max = u[u.length - 1] + (u[u.length - 1] - u[u.length - 2])
  } else if (u.length === 1) {
    max = 2 * u[0]
  } else {
    max = ceiling
  }
  if (Number.isFinite(ceiling) && ceiling > min) max = Math.min(max, ceiling)
  return { min, max }
}

// gaugeModel(row, scales) -> { fraction, colour, stops, range }
export function gaugeModel(row, scales) {
  const { metric, value, missing } = row
  const scaled = hasScale(scales, metric)
  if (!scaled) return { fraction: null, colour: null, stops: [], range: null }

  const scale = scales.find((s) => s.metric === metric)
  const bands = scale.bands
  const range = gaugeRange(metric, bands, scale.ceiling)
  const stops = bandStops(bands, range)
  if (missing) return { fraction: null, colour: null, stops, range }
  return {
    fraction: clamp((value - range.min) / (range.max - range.min), 0, 1),
    colour: rampColour(value, bands),
    stops,
    range,
  }
}

// One stop per band at its upper value; the open top band runs to the arc's max.
function bandStops(bands, range) {
  const { min, max } = range
  return bands.map((band) => {
    const upper = band.upper === null ? max : band.upper
    return { fraction: clamp((upper - min) / (max - min), 0, 1), colour: band.colour }
  })
}

function round(n) {
  return Math.round(n * 1000) / 1000
}

// fraction 0 = left end, 0.5 = top, 1 = right end.
function pointAt(fraction, r, cx, cy) {
  const angle = Math.PI * (1 - fraction)
  return { x: round(cx + r * Math.cos(angle)), y: round(cy - r * Math.sin(angle)) }
}

// needlePoint(fraction, r, cx, cy) -> point on the arc's centreline; used for the needle tip/base.
export function needlePoint(fraction, r = 40, cx = 50, cy = 50) {
  return pointAt(fraction, r, cx, cy)
}

// arcPath draws the semicircle segment from one fraction to another.
export function arcPath(fromFraction, toFraction, r = 40, cx = 50, cy = 50) {
  const from = pointAt(fromFraction, r, cx, cy)
  const to = pointAt(toFraction, r, cx, cy)
  // Segments of a semicircle never exceed 180°, so the large-arc flag is always 0.
  return `M ${from.x} ${from.y} A ${r} ${r} 0 0 1 ${to.x} ${to.y}`
}
