import { describe, it, expect } from 'vitest'
import {
  bandsFor, hexOutlinePaint, markerMaxZoom, layerPaint, markerPaint, officialLayout, officialPaint,
  hexLabelPaint, CARRIED_OPACITY, FRESH_OPACITY, SETTLING_OPACITY,
} from '../mappaint.js'
import { DIAMOND_RADIUS_PX } from '../markericon.js'
import { GRID_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM_FRACTIONAL } from '../hexes.js'

// bandsFor: matching by `metric` field, not array position, so a reordered
// /api/v1/scales response cannot silently recolour the map with the wrong
// band table.
describe('bandsFor', () => {
  const scales = [
    { metric: 'P1', bands: [{ upper: 50, colour: 'x' }] },
    { metric: 'P2', bands: [{ upper: 25, colour: 'y' }] },
  ]

  it('finds the table for the requested metric regardless of array order', () => {
    expect(bandsFor(scales, 'P2')).toEqual([{ upper: 25, colour: 'y' }])
  })
  it('returns an empty table for an unknown metric rather than the first one found', () => {
    expect(bandsFor(scales, 'humidity')).toEqual([])
  })
  it('returns an empty table when scales is not an array (a failed fetch resolved to null)', () => {
    expect(bandsFor(null, 'P2')).toEqual([])
    expect(bandsFor(undefined, 'P2')).toEqual([])
  })

  // The ramp and the key both take bands, never scales, so a ceiling that stops
  // here is a ceiling nothing draws to.
  it('carries the scale ceiling onto the top band, and onto no other', () => {
    const withCeiling = [
      { metric: 'P2', ceiling: 500, bands: [{ upper: 25, colour: 'y' }, { upper: null, colour: 'z' }] },
    ]
    expect(bandsFor(withCeiling, 'P2')).toEqual([
      { upper: 25, colour: 'y' },
      { upper: null, colour: 'z', ceiling: 500 },
    ])
  })

  it('leaves the bands untouched when the scale states no ceiling', () => {
    expect(bandsFor(scales, 'P1')).toEqual([{ upper: 50, colour: 'x' }])
  })
})

// J1 (review round 2): the circle layer's stroke colour must come from
// cfg.markerStrokeColour, not from any other config field. This is built by
// layerPaint(cfg) (named markerPaint before task 7, renamed to make room for
// the metric-aware markerPaint(bands, opts) below — see that function's own
// comment for why one name could not serve both), extracted out of mount()'s
// map.on('load', ...) callback specifically so it can be tested without a
// real MapLibre map.
describe('layerPaint', () => {
  it('reads circle-stroke-color from cfg.markerStrokeColour', () => {
    const cfg = { markerStrokeColour: '#ffffff', noDataColour: '#9ca3af' }
    const paint = layerPaint(cfg)
    expect(paint['circle-stroke-color']).toBe('#ffffff')
  })
})

// Three different facts must not share one colour: "no reading" (grey),
// "this metric has no band table" (unscaledColour), and a real band value.
// Evaluates the one MapLibre expression shape markerPaint's !scaled branch
// produces — ['case', ['==', ['get', 'value'], null], whenNull, whenNotNull]
// — against a fake feature's properties. Used instead of a bare
// JSON.stringify/toContain check so the unscaled test below proves the
// actual no-reading/has-reading DISTINCTION (both branches individually),
// not just "some colour string is present somewhere in the JSON" — a check
// that a mutation swapping the two colours would still pass.
function evalUnscaledCase(expr, properties) {
  const [op, [cmp, [, key], compareTo], whenTrue, whenFalse] = expr
  if (op !== 'case' || cmp !== '==') throw new Error(`unexpected expression shape: ${JSON.stringify(expr)}`)
  return properties[key] === compareTo ? whenTrue : whenFalse
}

describe('markerPaint', () => {
  const scales = [{ metric: 'P2', bands: [{ upper: 5, colour: '#50f0e6' }] }]

  it('keeps "no reading" distinct from "has a reading" when the metric has no scale', () => {
    const paint = markerPaint([], { noDataColour: '#999999', unscaledColour: '#94a3b8', scaled: false })
    expect(evalUnscaledCase(paint, { value: null })).toBe('#999999')
    expect(evalUnscaledCase(paint, { value: 12 })).toBe('#94a3b8')
  })

  // The markers are on the same scale as the hexes. A `step` expression here
  // would leave the dots painted in categories over a blended choropleth —
  // two answers to the same question, on the same screen.
  it('blends the markers rather than snapping them to a band', () => {
    const bands = [{ upper: 10, colour: '#00ff00' }, { upper: null, colour: '#ff0000' }]
    const paint = markerPaint(bands, { noDataColour: '#999999', unscaledColour: '#94a3b8', scaled: true })
    const ramp = paint[3]
    expect(ramp[0]).toBe('interpolate')
    // A stop at every band midpoint AND every boundary — strictly more than
    // one per band, which is all a step expression would need.
    const values = ramp.slice(3).filter((_, i) => i % 2 === 0)
    expect(values.length).toBeGreaterThan(bands.length)
    expect(values).toEqual([...values].sort((a, b) => a - b))
  })

  it('still uses the bands when the metric is scaled', () => {
    const paint = markerPaint(scales[0].bands, { noDataColour: '#999999', unscaledColour: '#94a3b8', scaled: true })
    expect(JSON.stringify(paint)).toContain('#50f0e6')
    expect(JSON.stringify(paint)).not.toContain('#94a3b8')
  })
})

describe('markerMaxZoom', () => {
  it('gives the sensor dots the whole grid range, since no cell replaces them', () => {
    // The sensors tier IS the point tier's own data: dot and cell carry the
    // same one device, so the dots run to the changeover rather than stopping
    // at the aggregate one.
    expect(markerMaxZoom('sensors')).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
    expect(markerMaxZoom('country')).toBe(GRID_MIN_ZOOM_FRACTIONAL)
    expect(markerMaxZoom('municipality')).toBe(GRID_MIN_ZOOM_FRACTIONAL)
  })
})

describe('hexOutlinePaint', () => {
  it('draws the cell borders in an ink that shows against a pale street map', () => {
    // The outline used to be ['get', 'colour'] — the fill's own value colour —
    // so every border vanished into the cell it bounded and the grid read as a
    // smear. It is now the marker stroke, the same edge the dots carry.
    const paint = hexOutlinePaint({ labelColour: '#111827', markerStrokeColour: '#ffffff' })
    expect(paint['line-color']).toBe('#111827')
    // Not the marker stroke either: white lifts a dot off a dark map and
    // vanishes into a pale one, which over the OSM raster left the cells with
    // no visible edge at all.
    expect(paint['line-color']).not.toBe('#ffffff')
    expect(paint['line-width']).toBeGreaterThanOrEqual(1)
    expect(paint['line-opacity']).toBeGreaterThanOrEqual(0.6)
  })
})

describe('officialLayout / officialPaint', () => {
  it('draws the diamond at the radius the circles use and in the same colours', () => {
    const cfg = { markerStrokeColour: '#ffffff' }
    const size = officialLayout()['icon-size']
    const radius = layerPaint(cfg)['circle-radius']

    // Both are [interpolate, linear, [zoom], z1, v1, z2, v2]. The zoom stops
    // must match exactly; the values differ only by the glyph's own unit.
    expect(size.slice(0, 4)).toEqual(radius.slice(0, 4))
    expect(size[4] * (DIAMOND_RADIUS_PX / 2)).toBeCloseTo(radius[4])
    expect(size[6] * (DIAMOND_RADIUS_PX / 2)).toBeCloseTo(radius[6])

    expect(officialPaint(cfg)['icon-color']).toEqual(['get', 'colour'])
    expect(officialPaint(cfg)['icon-halo-color']).toBe('#ffffff')
  })
})

describe('hexLabelPaint', () => {
  const cfg = { labelColour: '#222', markerStrokeColour: '#fff' }

  it('fades a carried reading and leaves a measured one alone', () => {
    expect(hexLabelPaint(cfg)['text-opacity']).toEqual([
      'case',
      ['==', ['get', 'carried'], true], CARRIED_OPACITY,
      ['==', ['get', 'fresh'], 0], FRESH_OPACITY,
      ['==', ['get', 'fresh'], 1], SETTLING_OPACITY,
      1,
    ])
  })

  it('is faded enough to tell apart from a measured reading', () => {
    expect(CARRIED_OPACITY).toBeLessThan(1)
    expect(CARRIED_OPACITY).toBeGreaterThan(0)
  })

  // A cell fading in is not a cell holding an old number. Keeping the whole
  // ramp above the held mute is what stops the two from ever looking alike.
  it('never draws an arriving cell as faint as a held one', () => {
    expect(FRESH_OPACITY).toBeGreaterThan(CARRIED_OPACITY)
    expect(SETTLING_OPACITY).toBeGreaterThan(FRESH_OPACITY)
    expect(SETTLING_OPACITY).toBeLessThan(1)
  })

  it('keeps the colour and halo the measured labels use', () => {
    expect(hexLabelPaint(cfg)['text-color']).toBe('#222')
    expect(hexLabelPaint(cfg)['text-halo-color']).toBe('#fff')
  })
})
