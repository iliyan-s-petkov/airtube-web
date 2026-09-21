// Pure gauge model for Gauge.svelte; no DOM.
import { rampColour, rampSpans } from './ramp.js'
import { hasScale } from './metrics.js'

// Display ranges for metrics the server publishes no bands for.
export const FIXED_RANGES = {
  humidity: { min: 0, max: 100 },
  temperature: { min: -20, max: 45 },
  pressure: { min: 950, max: 1050 },
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value))
}

// gaugeModel(row, scales) -> { fraction, colour, stops, range }
export function gaugeModel(row, scales) {
  const { metric, value, missing } = row
  const scaled = hasScale(scales, metric)

  if (scaled) {
    const scale = scales.find((s) => s.metric === metric)
    const bands = scale.bands
    const ceiling = scale.ceiling > 0 ? scale.ceiling : rampSpans(bands)[bands.length - 1]?.upper
    const range = { min: 0, max: ceiling }
    const stops = bandStops(bands, ceiling)
    if (missing) return { fraction: null, colour: null, stops, range }
    return {
      fraction: clamp((value - range.min) / (range.max - range.min), 0, 1),
      colour: rampColour(value, bands),
      stops,
      range,
    }
  }

  const range = FIXED_RANGES[metric] ?? null
  if (!range) return { fraction: null, colour: null, stops: [], range: null }
  if (missing) return { fraction: null, colour: null, stops: [], range }
  return {
    fraction: clamp((value - range.min) / (range.max - range.min), 0, 1),
    colour: null,
    stops: [],
    range,
  }
}

// One stop per band at its upper value; top band runs to the gauge ceiling.
function bandStops(bands, ceiling) {
  const spans = rampSpans(bands)
  return bands.map((band, i) => {
    const upper = i === bands.length - 1 ? ceiling : spans[i].upper
    return { fraction: clamp(upper / ceiling, 0, 1), colour: band.colour }
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

// arcPath draws the semicircle segment from one fraction to another.
export function arcPath(fromFraction, toFraction, r = 40, cx = 50, cy = 50) {
  const from = pointAt(fromFraction, r, cx, cy)
  const to = pointAt(toFraction, r, cx, cy)
  const largeArc = Math.abs(toFraction - fromFraction) > 0.5 ? 1 : 0
  return `M ${from.x} ${from.y} A ${r} ${r} 0 ${largeArc} 1 ${to.x} ${to.y}`
}
