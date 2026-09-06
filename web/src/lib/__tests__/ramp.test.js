import { describe, expect, it } from 'vitest'
import {
  mixOklab,
  rampColour,
  rampGradient,
  rampPosition,
  rampSpans,
  rampStops,
  rampValueStops,
} from '../ramp.js'

// A scale shaped like the served PM one: an open first band, three closed
// middle bands of unequal width, an open top.
const BANDS = [
  { upper: 20, colour: '#00796b' },
  { upper: 40, colour: '#8bc34a' },
  { upper: 50, colour: '#ffc107' },
  { upper: 100, colour: '#dd2c00' },
  { upper: null, colour: '#8c0084' },
]

// Either form the ramp emits: a mixed rgb(), or a served colour handed back
// verbatim at a stop.
function channels(css) {
  const rgb = /^rgb\((\d+), (\d+), (\d+)\)$/.exec(css)
  if (rgb) return [1, 2, 3].map((i) => Number(rgb[i]))
  const hex = /^#([0-9a-f]{6})$/i.exec(css)
  expect(hex, `not a colour this ramp emits: ${css}`).toBeTruthy()
  return [0, 2, 4].map((i) => parseInt(hex[1].slice(i, i + 2), 16))
}

describe('rampStops', () => {
  it('anchors each band colour at the middle of its share of the bar', () => {
    const stops = rampStops(BANDS)
    const step = 100 / BANDS.length
    expect(stops.slice(1, -1)).toEqual(
      BANDS.map((b, i) => ({ colour: b.colour, pos: (i + 0.5) * step })),
    )
  })

  it('carries the end colours out to the ends of the bar', () => {
    const stops = rampStops(BANDS)
    expect(stops[0]).toEqual({ colour: '#00796b', pos: 0 })
    expect(stops[stops.length - 1]).toEqual({ colour: '#8c0084', pos: 100 })
  })

  it('rises from bottom to top without going backwards', () => {
    const pos = rampStops(BANDS).map((s) => s.pos)
    expect(pos).toEqual([...pos].sort((a, b) => a - b))
  })

  it('draws a one-band scale flat in its own colour', () => {
    expect(rampStops([{ upper: null, colour: '#fff' }])).toEqual([
      { colour: '#fff', pos: 0 },
      { colour: '#fff', pos: 100 },
    ])
    expect(rampColour(42, [{ upper: null, colour: '#fff' }], '#bbb')).toBe('#fff')
  })

  it('is not a ramp with no bands at all', () => {
    expect(rampStops([])).toEqual([])
    expect(rampStops(null)).toEqual([])
  })
})

describe('rampPosition', () => {
  it('moves continuously across a band boundary', () => {
    const below = rampPosition(39.999, BANDS)
    const on = rampPosition(40, BANDS)
    const above = rampPosition(40.001, BANDS)
    expect(below).toBeLessThan(on)
    expect(on).toBeLessThan(above)
    expect(Math.abs(above - below)).toBeLessThan(0.01)
  })

  it('lands a boundary reading exactly on the seam between two bands', () => {
    // The top of band 1 (index 1) is the start of band 2's share.
    expect(rampPosition(40, BANDS)).toBeCloseTo(2 * (100 / 5), 9)
  })

  it('places a value linearly inside its own band', () => {
    // 45 is halfway through the 40..50 band, whose share runs 40%..60%.
    expect(rampPosition(45, BANDS)).toBeCloseTo(50, 9)
  })

  it('has no step at ANY boundary, including the first and the last', () => {
    for (const edge of [20, 40, 50, 100]) {
      const below = rampPosition(edge - 0.001, BANDS)
      const above = rampPosition(edge + 0.001, BANDS)
      expect(Math.abs(above - below)).toBeLessThan(0.01)
    }
  })

  it('clamps beyond the ends of the bar instead of running off it', () => {
    expect(rampPosition(-500, BANDS)).toBe(0)
    expect(rampPosition(9000, BANDS)).toBe(100)
  })

  it('gives the open ends the width of the band beside them', () => {
    // Band 0 is open below; band 1 is 20 wide, so band 0 is drawn 0..20.
    expect(rampSpans(BANDS)[0]).toEqual({ lower: 0, upper: 20 })
    // Band 4 is open above; band 3 is 50 wide, so band 4 is drawn 100..150.
    expect(rampSpans(BANDS)[4]).toEqual({ lower: 100, upper: 150 })
  })

  it('gives a two-band scale with an open top a width to draw', () => {
    const spans = rampSpans([{ upper: 20, colour: '#000' }, { upper: null, colour: '#fff' }])
    expect(spans).toEqual([{ lower: 0, upper: 20 }, { lower: 20, upper: 40 }])
    expect(Number.isNaN(spans[0].lower)).toBe(false)
  })

  it('has no position for a reading that is not one', () => {
    expect(rampPosition(null, BANDS)).toBeNull()
    expect(rampPosition(undefined, BANDS)).toBeNull()
    expect(rampPosition(NaN, BANDS)).toBeNull()
    expect(rampPosition(10, [])).toBeNull()
  })
})

describe('rampColour', () => {
  it('blends between two bands rather than stepping', () => {
    // Two readings a hair apart across the 40 boundary must be near-identical,
    // which a band-snapping scale can never be.
    const [r1, g1, b1] = channels(rampColour(39.9, BANDS, '#000'))
    const [r2, g2, b2] = channels(rampColour(40.1, BANDS, '#000'))
    expect(Math.abs(r1 - r2) + Math.abs(g1 - g2) + Math.abs(b1 - b2)).toBeLessThan(6)
  })

  it('separates two readings the old band scale painted identically', () => {
    // Both sit inside the 40..50 band and used to come out the same amber.
    const low = channels(rampColour(41, BANDS, '#000'))
    const high = channels(rampColour(49, BANDS, '#000'))
    expect(low).not.toEqual(high)
  })

  it('gives a band its own colour at its anchor', () => {
    // 45 is the middle of the 40..50 band, which is that band's anchor.
    expect(channels(rampColour(45, BANDS, '#000'))).toEqual([255, 193, 7])
  })

  it('paints the ends in the end bands own colours', () => {
    expect(channels(rampColour(-40, BANDS, '#000'))).toEqual([0, 121, 107])
    expect(channels(rampColour(900, BANDS, '#000'))).toEqual([140, 0, 132])
  })

  it('rises monotonically up the scale', () => {
    const positions = [0, 25, 45, 60, 90, 300].map((v) => rampPosition(v, BANDS))
    expect(positions).toEqual([...positions].sort((a, b) => a - b))
  })

  it('hands a reading it cannot place to the no-data colour', () => {
    expect(rampColour(null, BANDS, '#bbb')).toBe('#bbb')
    expect(rampColour(NaN, BANDS, '#bbb')).toBe('#bbb')
    expect(rampColour(10, [], '#bbb')).toBe('#bbb')
  })
})

describe('rampValueStops', () => {
  it('rises strictly, so MapLibre will accept the expression', () => {
    const values = rampValueStops(BANDS).map((s) => s.value)
    expect(values.length).toBeGreaterThan(2)
    for (let i = 1; i < values.length; i++) expect(values[i]).toBeGreaterThan(values[i - 1])
  })

  it('reproduces rampColour when interpolated between its stops', () => {
    const stops = rampValueStops(BANDS)
    // Midway between two stops, a linear blend of their colours must match the
    // colour the hexes get for that same value — otherwise a marker and the
    // cell under it disagree.
    for (let i = 1; i < stops.length; i++) {
      const mid = (stops[i - 1].value + stops[i].value) / 2
      const expected = channels(rampColour(mid, BANDS, '#000'))
      const blended = channels(mixOklab(stops[i - 1].colour, stops[i].colour, 0.5))
      for (let c = 0; c < 3; c++) expect(Math.abs(blended[c] - expected[c])).toBeLessThanOrEqual(2)
    }
  })

  it('has nothing to interpolate without a scale', () => {
    expect(rampValueStops([])).toEqual([])
  })
})

describe('rampGradient', () => {
  it('blends, with one position per stop and no hard pairs', () => {
    const css = rampGradient(BANDS)
    expect(css.startsWith('linear-gradient(to top, ')).toBe(true)
    for (const stop of css.slice('linear-gradient(to top, '.length, -1).split(', ')) {
      expect(stop).toMatch(/^#[0-9a-f]{6} \d+\.\d{3}%$/)
    }
  })

  it('draws the same colours the map paints', () => {
    const css = rampGradient(BANDS)
    for (const band of BANDS) expect(css).toContain(band.colour)
  })

  it('spans the whole bar', () => {
    const css = rampGradient(BANDS)
    expect(css).toContain('0.000%')
    expect(css).toContain('100.000%')
  })

  it('is nothing at all when there is no scale to draw', () => {
    expect(rampGradient([])).toBe('')
  })
})

describe('mixOklab', () => {
  it('returns each end unchanged at the ends', () => {
    expect(channels(mixOklab('#00796b', '#dd2c00', 0))).toEqual([0, 121, 107])
    expect(channels(mixOklab('#00796b', '#dd2c00', 1))).toEqual([221, 44, 0])
  })

  it('does not dip through a dark middle the way an sRGB average does', () => {
    // The straight channel average of teal and amber; OKLab's midpoint must be
    // brighter than that, which is the entire reason for the colour space.
    const mid = channels(mixOklab('#00796b', '#ffc107', 0.5))
    const naive = [(0 + 255) / 2, (121 + 193) / 2, (107 + 7) / 2]
    expect(mid[0] + mid[1] + mid[2]).toBeGreaterThan(naive[0] + naive[1] + naive[2])
  })

  it('expands three-digit hex rather than failing to parse it', () => {
    // Halfway between black and white. Parsed as six digits it is a mid grey;
    // unexpanded it parses as nothing and falls through to an endpoint.
    const [r, g, b] = channels(mixOklab('#000', '#fff', 0.5))
    expect(r).toBe(g)
    expect(g).toBe(b)
    expect(r).toBeGreaterThan(80)
    expect(r).toBeLessThan(200)
  })

  it('returns a blend of one colour as that colour, not as a round-trip', () => {
    expect(mixOklab('#3c9f00', '#3c9f00', 0.5)).toBe('#3c9f00')
  })

  it('passes a colour it cannot parse straight through', () => {
    expect(mixOklab('rebeccapurple', '#fff', 0.2)).toBe('rebeccapurple')
    expect(mixOklab('rebeccapurple', '#fff', 0.8)).toBe('#fff')
  })
})
