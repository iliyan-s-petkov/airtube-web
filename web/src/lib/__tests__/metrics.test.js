import { describe, it, expect } from 'vitest'
import { parseMetricList, hasScale, unitFor } from '../metrics.js'

// Shaped exactly like /api/v1/scales: two tables for P2, one for P1, none for
// anything else. The duplicate P2 entry is not padding — it is what the real
// endpoint returns (eaqi and eu_limit), and hasScale must not care.
const scales = [
  { name: 'eaqi', metric: 'P2', unit: 'µg/m³', bands: [{ upper: 5, colour: '#50f0e6' }] },
  { name: 'eaqi', metric: 'P1', unit: 'µg/m³', bands: [{ upper: 20, colour: '#50f0e6' }] },
  { name: 'eu_limit', metric: 'P2', unit: 'µg/m³', bands: [{ upper: 25, colour: '#50ccaa' }] },
]

describe('parseMetricList', () => {
  it('splits the server attribute', () => {
    expect(parseMetricList('P1,P2,temperature')).toEqual(['P1', 'P2', 'temperature'])
  })

  it('tolerates spacing and empty entries', () => {
    expect(parseMetricList(' P1 , ,P2 ')).toEqual(['P1', 'P2'])
  })

  it('returns an empty list for a missing attribute', () => {
    expect(parseMetricList(undefined)).toEqual([])
  })
})

describe('hasScale', () => {
  it('is true only for metrics the server publishes bands for', () => {
    expect(hasScale(scales, 'P1')).toBe(true)
    expect(hasScale(scales, 'P2')).toBe(true)
    expect(hasScale(scales, 'temperature')).toBe(false)
  })

  // A metric with an entry but an EMPTY band list is not scaled: colourFor
  // would paint every marker the no-data colour, which is the uniformly-grey
  // map this whole distinction exists to prevent.
  it('is false for an entry with no bands', () => {
    expect(hasScale([{ metric: 'pressure', bands: [] }], 'pressure')).toBe(false)
  })

  it('is false when the scales failed to load', () => {
    expect(hasScale(null, 'P2')).toBe(false)
  })
})

describe('unitFor', () => {
  it('reads the unit from the first matching table', () => {
    expect(unitFor(scales, 'P1')).toBe('µg/m³')
  })

  it('is empty for a metric with no table', () => {
    expect(unitFor(scales, 'humidity')).toBe('')
  })
})
