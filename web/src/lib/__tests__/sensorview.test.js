import { describe, it, expect } from 'vitest'
import { panelRows } from '../sensorview.js'

const options = [
  { metric: 'P2', label: 'PM2.5' },
  { metric: 'P1', label: 'PM10' },
  { metric: 'temperature', label: 'Temperature' },
]
const scales = [{ metric: 'P2', unit: 'µg/m³', bands: [] }, { metric: 'P1', unit: 'µg/m³', bands: [] }]

describe('panelRows', () => {
  it('lists every metric the sensor reports, in the switcher order', () => {
    const rows = panelRows({ values: { P1: 30, P2: 12 } }, options, scales)
    expect(rows.map((r) => r.metric)).toEqual(['P2', 'P1'])
  })

  // A metric the sensor does not measure is omitted; a metric it measures but
  // has no CURRENT value for is kept and marked missing. Collapsing the two
  // would tell a reader a working sensor does not measure PM10.
  it('keeps a reported metric with no current value and marks it missing', () => {
    const rows = panelRows({ values: { P1: null, P2: 12 } }, options, scales)
    expect(rows.find((r) => r.metric === 'P1')).toMatchObject({ missing: true, value: null })
  })

  it('carries the unit from the scales response', () => {
    expect(panelRows({ values: { P2: 12 } }, options, scales)[0].unit).toBe('µg/m³')
  })

  it('leaves the unit empty for a metric with no scale table', () => {
    const rows = panelRows({ values: { temperature: 21 } }, options, scales)
    expect(rows[0].unit).toBe('')
  })

  it('returns nothing for a sensor with no values at all', () => {
    expect(panelRows({ values: {} }, options, scales)).toEqual([])
  })
})
