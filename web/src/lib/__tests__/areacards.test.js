import { describe, it, expect } from 'vitest'
import { areaCards, gaugeFor, bandColour } from '../areacards.js'

const bands = [
  { upper: 10, colour: '#0a0' },
  { upper: 25, colour: '#fa0' },
  { upper: null, colour: '#a00' },
]

const pm = { metric: 'P2', unit: 'µg/m³', ceiling: 50, bands }

const t = {
  high: 'Highest · {metric}',
  low: 'Lowest · {metric}',
  median: 'Median · {metric}',
  thisSensor: 'This sensor',
  ofTotal: 'of {total}',
  aboveMedian: 'Above the area median',
  belowMedian: 'Below the area median',
  atMedian: 'At the area median',
  areaSensors: '{area} · {total} sensors',
  sensorsOnly: '{total} sensors in this area',
}

const stats = { high: 30, low: 4, median: 12, total: 9, value: 4, rank: 1 }

const cards = (over = {}, opts = {}) =>
  areaCards({ ...stats, ...over }, { metric: 'P2', metricLabel: 'PM2.5', scale: pm, area: 'Ovcha Kupel', lang: 'en', t, ...opts })

describe('bandColour', () => {
  it('puts a value exactly on a boundary in the lower band', () => {
    expect(bandColour(bands, 10)).toBe('#0a0')
  })

  it('gives the open-ended top band to anything above the last boundary', () => {
    expect(bandColour(bands, 900)).toBe('#a00')
  })
})

describe('gaugeFor', () => {
  it('measures the value against the scale drawn ceiling', () => {
    expect(gaugeFor(pm, 25)).toEqual({ percent: 50, colour: '#fa0' })
  })

  it('clamps a reading past the ceiling rather than overdrawing the arc', () => {
    expect(gaugeFor(pm, 500).percent).toBe(100)
  })

  it('falls back to the highest band where the scale draws no ceiling', () => {
    expect(gaugeFor({ ...pm, ceiling: 0, bands: [{ upper: 20, colour: '#0a0' }] }, 10).percent).toBe(50)
  })

  // 944 hPa is an ordinary day. An arc at 97% would invent an alarm out of an
  // axis, so only the µg/m³ metrics get one.
  it('draws no arc for a metric counted in something else', () => {
    expect(gaugeFor({ ...pm, unit: 'hPa' }, 20)).toBe(null)
    expect(gaugeFor(null, 20)).toBe(null)
  })
})

describe('areaCards', () => {
  it('states the area high, low and median, then this sensor place in it', () => {
    expect(cards().map((c) => [c.label, c.value, c.unit])).toEqual([
      ['Highest · PM2.5', '30', 'µg/m³'],
      ['Lowest · PM2.5', '4', 'µg/m³'],
      ['Median · PM2.5', '12', 'µg/m³'],
      ['This sensor', '1', 'of 9'],
    ])
  })

  it('names the area and its size on every card', () => {
    expect(cards()[0].tier).toBe('Ovcha Kupel · 9 sensors')
  })

  // A number with no set is not a comparison. Where the area has no name to
  // hand, the count still says what the figures are drawn from.
  it('falls back to the bare count when the area has no name', () => {
    expect(cards({}, { area: '' })[0].tier).toBe('9 sensors in this area')
  })

  it('says which side of the median this sensor sits on', () => {
    expect(cards({ value: 4, rank: 1 })[3].tier).toBe('Below the area median')
    expect(cards({ value: 30, rank: 9 })[3].tier).toBe('Above the area median')
    expect(cards({ value: 12, rank: 5 })[3].tier).toBe('At the area median')
  })

  // A rank of nothing is not a rank; the area's own three figures survive it.
  it('drops the rank card when this sensor reports nothing for the metric', () => {
    const out = cards({ value: null, rank: null })
    expect(out).toHaveLength(3)
  })

  it('writes the decimal the language writes', () => {
    expect(cards({ high: 30.25 })[0].value).toBe('30.3')
    expect(cards({ high: 30.25 }, { lang: 'bg' })[0].value).toBe('30,3')
  })

  it('gauges the three area figures and leaves the rank a plain count', () => {
    const out = cards()
    expect(out.slice(0, 3).every((c) => c.gauge)).toBe(true)
    expect(out[3].gauge).toBe(null)
  })

  // Nothing honest to say, so the caller leaves the national strip standing
  // rather than showing four blanks.
  it('returns nothing where there are no stats', () => {
    expect(areaCards(null, { metric: 'P2', metricLabel: 'PM2.5', scale: pm, area: 'x', lang: 'en', t })).toEqual([])
  })
})
