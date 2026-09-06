// The colour ramp: one continuous scale, shared by the map and its key.
//
// The bands used to be painted as steps: a reading took its band's colour
// outright, so 24.9 and 25.1 µg/m³ came out as two different colours while 5.1
// and 24.9 came out identical. A step scale says "this reading is in this category", which is a legislative
// claim; a map of measurements should say "this reading is HERE", which is a
// continuous one.
//
// The model is the design kit's (ui_kits/app/map-render.js): stops interpolated
// BY VALUE, mixed in OKLab, on a piecewise axis. What differs is where the
// stops come from. The kit hardcodes a µg/m³ ramp measured off its reference
// image; this site paints seven metrics, and its band colours arrive from
// /api/v1/scales so that a legislative change stays a one-file server edit.
// So the stops are derived from the served band table instead — same model,
// same look, and no colour in code.
//
// The axis is piecewise because the bands are: each band takes an equal share
// of the bar however wide it is in value. That is what lets one key serve an
// ordinary day and a wildfire without the useful range collapsing into a
// sliver, and it is the axis the key has always drawn — the numbers sit on the
// band seams, evenly spaced, whatever values they carry.

// A band's colour is exact at the MIDDLE of its share of the bar, not at its
// edges: an edge is a boundary between two bands and belongs to neither, so a
// reading sitting exactly on one should read as the blend it is.
const ANCHOR = 0.5

/**
 * rampStops turns a band table into the ramp's colour stops: `{ colour, pos }`
 * with pos in 0..100, ascending, bottom of the scale first.
 *
 * The end stops repeat the first and last band's colour at 0 and 100, so the
 * bar does not fade out at its ends into half a band of nothing.
 *
 * Fewer than two bands is not a ramp — there is nothing to interpolate between
 * — and yields no stops rather than a one-colour gradient pretending to be one.
 */
export function rampStops(bands) {
  if (!bands || bands.length === 0) return []
  // One band is a scale — a flat one. It has nothing to blend towards, so it
  // paints its own colour from end to end rather than dropping to no-data.
  if (bands.length === 1) {
    return [{ colour: bands[0].colour, pos: 0 }, { colour: bands[0].colour, pos: 100 }]
  }
  const step = 100 / bands.length
  const stops = bands.map((band, i) => ({ colour: band.colour, pos: (i + ANCHOR) * step }))
  return [
    { colour: stops[0].colour, pos: 0 },
    ...stops,
    { colour: stops[stops.length - 1].colour, pos: 100 },
  ]
}

/**
 * rampSpans states, for each band, the value range the ramp draws it across:
 * `{ lower, upper }`, ascending, one per band.
 *
 * The first band has no stated lower bound and the last usually has no upper
 * one, and a ramp cannot blend across a range of unknown width. Each open end
 * is given a width, and everything beyond it clamps to the end of the bar. That
 * keeps the ramp continuous, which matters more than it sounds: holding an open
 * band flat at its own colour instead, the obvious alternative, puts a visible
 * step at the very first boundary, and a step is the one thing this scale is
 * not.
 *
 * The top band's width comes from the scale's stated `ceiling` when it has one.
 * Only when it does not does the band fall back to borrowing its NEIGHBOUR's
 * width — the nearest guess the table itself supports, and a bad one on a real
 * scale: PM2.5's neighbour is 25 wide, so an open band starting at 50 was drawn
 * to 75 and every winter reading above that came out the same colour.
 *
 * A one-band scale has no neighbour to borrow from. Its span is arbitrary and
 * says nothing, which is correct: with one colour, where a reading sits on the
 * bar cannot change what it is painted.
 */
export function rampSpans(bands) {
  if (!bands || bands.length === 0) return []
  if (bands.length === 1) return [{ lower: 0, upper: bands[0].upper ?? bands[0].ceiling ?? 1 }]
  const uppers = bands.map((b) => b.upper)
  const n = uppers.length
  // The top's borrowed width is the band below it, which needs a lower edge of
  // its own to be measured from — on a two-band scale there is none, so it
  // falls back to the one bound the table does state. A ceiling at or below
  // where the top band starts is not a width, so it is ignored rather than
  // inverting the band.
  const below = uppers[n - 2]
  const borrowed = below + (n >= 3 ? below - uppers[n - 3] : Math.abs(below) || 1)
  const ceiling = bands[n - 1].ceiling
  const top = uppers[n - 1] ?? (ceiling > below ? ceiling : borrowed)
  // The bottom's borrowed width is the second band's.
  const bottom = uppers[0] - ((uppers[1] ?? top) - uppers[0])

  return bands.map((band, i) => ({
    lower: i === 0 ? bottom : uppers[i - 1],
    upper: i === n - 1 ? top : uppers[i],
  }))
}

/**
 * rampPosition maps a reading onto the bar, 0..100, or null when it is not a
 * reading at all.
 *
 * Within a band the position is linear in value, and each band takes an equal
 * share of the bar however wide it is, so the top of band i and the bottom of
 * band i+1 are the same point: the ramp has no seams.
 */
export function rampPosition(value, bands) {
  if (value === null || value === undefined || Number.isNaN(value)) return null
  const spans = rampSpans(bands)
  if (spans.length === 0) return null
  const step = 100 / spans.length

  for (let i = 0; i < spans.length; i++) {
    const { lower, upper } = spans[i]
    if (i < spans.length - 1 && value > upper) continue
    const t = upper === lower ? 0 : (value - lower) / (upper - lower)
    return (i + Math.max(0, Math.min(1, t))) * step
  }
  return 100
}

/**
 * rampColour is the colour a reading is painted, mixed from the two stops that
 * bracket its position.
 *
 * noDataColour covers every reading the ramp cannot place — no value, and a
 * band table too short to be a scale. It is passed in rather than defined here
 * for the same reason the band colours are not defined here.
 */
export function rampColour(value, bands, noDataColour) {
  const pos = rampPosition(value, bands)
  if (pos === null) return noDataColour
  const stops = rampStops(bands)
  if (stops.length === 0) return noDataColour

  for (let i = 0; i < stops.length - 1; i++) {
    const lo = stops[i]
    const hi = stops[i + 1]
    if (pos > hi.pos) continue
    const t = hi.pos === lo.pos ? 0 : (pos - lo.pos) / (hi.pos - lo.pos)
    return mixOklab(lo.colour, hi.colour, t)
  }
  return stops[stops.length - 1].colour
}

/**
 * rampValueStops is the ramp expressed against VALUE rather than against the
 * bar: `{ value, colour }`, strictly ascending.
 *
 * It exists for MapLibre. The hexes carry a colour computed per feature, but
 * the markers are painted by a style expression evaluated in the renderer —
 * that is deliberate, so switching metric does not re-walk every feature — and
 * an expression cannot call rampColour. Interpolating these stops linearly
 * reproduces it: a stop at every band midpoint AND every boundary, which are
 * exactly the points where the ramp's slope changes, so each segment between
 * two stops is linear in value on both sides of the fence.
 */
export function rampValueStops(bands) {
  const spans = rampSpans(bands)
  if (spans.length === 0) return []

  const values = [spans[0].lower]
  for (const { lower, upper } of spans) {
    values.push((lower + upper) / 2, upper)
  }
  // Strictly ascending, or MapLibre rejects the whole expression: a zero-width
  // band in a served table would otherwise repeat a value.
  const rising = values.filter((v, i) => i === 0 || v > values[i - 1])
  return rising.map((value) => ({ value, colour: rampColour(value, bands, null) }))
}

/**
 * rampGradient is the same ramp as a CSS gradient, bottom to top — so the key
 * cannot show a colour the map does not paint.
 */
export function rampGradient(bands) {
  const stops = rampStops(bands)
  if (stops.length === 0) return ''
  const css = stops.map((s) => `${s.colour} ${s.pos.toFixed(3)}%`)
  return `linear-gradient(to top, ${css.join(', ')})`
}

/**
 * mixOklab blends two colours perceptually.
 *
 * Not sRGB: a straight channel average between two saturated hues dips through
 * a muddy, darker middle — teal to amber passes through olive — and on a scale
 * whose whole job is to be read by eye, that dip reads as a band that is not
 * there. OKLab is uniform enough that the midpoint looks like a midpoint.
 *
 * A colour it cannot parse comes back unchanged rather than as an exception:
 * band colours are server data, and a map that goes blank because one of them
 * arrived in a form this function did not expect is a worse failure than a map
 * with one hard edge in its ramp.
 */
export function mixOklab(a, b, t) {
  // The ends come back verbatim, not as an rgb() round-trip of themselves: a
  // reading at a band's anchor is that band's own served colour, and a caller
  // comparing the two should see them match.
  if (t <= 0) return a
  if (t >= 1) return b
  // A blend of one colour is that colour. Without this, a flat one-band scale
  // paints every feature an rgb() round-trip of the served hex — the same
  // colour, spelled differently, which no caller can compare against.
  if (a === b) return a
  const from = parseHex(a)
  const to = parseHex(b)
  if (!from || !to) return t < 0.5 ? a : b
  const A = rgbToOklab(from)
  const B = rgbToOklab(to)
  const [r, g, bl] = oklabToRGB([
    A[0] + (B[0] - A[0]) * t,
    A[1] + (B[1] - A[1]) * t,
    A[2] + (B[2] - A[2]) * t,
  ])
  return `rgb(${r}, ${g}, ${bl})`
}

// Hex because that is what the server sends, and rgb() because that is what
// this file's own output looks like — a ramp colour is mixed from two others
// often enough (rampValueStops re-reads them) that not accepting its own
// format would be a trap.
function parseHex(colour) {
  const text = (colour ?? '').trim()
  const hexMatch = /^#([0-9a-f]{6}|[0-9a-f]{3})$/i.exec(text)
  if (hexMatch) {
    const hex = hexMatch[1].length === 3 ? hexMatch[1].replace(/./g, (c) => c + c) : hexMatch[1]
    return [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16))
  }
  const rgbMatch = /^rgb\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$/i.exec(text)
  if (rgbMatch) return [1, 2, 3].map((i) => Number(rgbMatch[i]))
  return null
}

function srgbToLinear(c) {
  const v = c / 255
  return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
}

function linearToSRGB(v) {
  const c = v <= 0.0031308 ? v * 12.92 : 1.055 * v ** (1 / 2.4) - 0.055
  return Math.max(0, Math.min(255, Math.round(c * 255)))
}

// The sRGB <-> OKLab matrices, from Björn Ottosson's definition of the space.
function rgbToOklab([r, g, b]) {
  const R = srgbToLinear(r)
  const G = srgbToLinear(g)
  const B = srgbToLinear(b)
  const l = Math.cbrt(0.4122214708 * R + 0.5363325363 * G + 0.0514459929 * B)
  const m = Math.cbrt(0.2119034982 * R + 0.6806995451 * G + 0.1073969566 * B)
  const s = Math.cbrt(0.0883024619 * R + 0.2817188376 * G + 0.6299787005 * B)
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ]
}

function oklabToRGB([L, a, b]) {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  return [
    linearToSRGB(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    linearToSRGB(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    linearToSRGB(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
  ]
}
