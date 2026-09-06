import { describe, it, expect } from 'vitest'
import { countSensors, sensorCountLine } from '../sensorcount.js'

const body = (P2) => ({ sensors: { id: [1, 2, 3], P2 } })

const texts = { shown: 'Showing', of: 'of', sensors: 'sensors', silent: 'with no recent readings' }

describe('countSensors', () => {
  it('splits the sensors into reporting and silent for the chosen metric', () => {
    expect(countSensors(body([3.2, null, 7]), 'P2')).toEqual({ total: 3, active: 2, silent: 1 })
  })

  // The same three sensors count differently per metric — that is the whole
  // reason the count takes a metric rather than reading a stored total.
  it('counts against the metric it is asked about, not against any other', () => {
    const b = { sensors: { id: [1, 2], P1: [1, 2], P2: [null, null] } }
    expect(countSensors(b, 'P1').active).toBe(2)
    expect(countSensors(b, 'P2').active).toBe(0)
  })

  it('counts a zero reading as reporting', () => {
    expect(countSensors(body([0, 0, 0]), 'P2')).toEqual({ total: 3, active: 3, silent: 0 })
  })

  // An area where nothing reports this metric has no column at all. Reporting
  // "0 of 3, 0 silent" would be a lie about three sensors the map is drawing.
  it('calls every sensor silent when the metric column is missing entirely', () => {
    expect(countSensors(body(undefined), 'P2')).toEqual({ total: 3, active: 0, silent: 3 })
  })

  // The bar mounts before the map's fetch lands; a throw here would take the
  // island down for the two seconds before the data arrives.
  it('reports zeroes rather than throwing before any data has arrived', () => {
    expect(countSensors(null, 'P2')).toEqual({ total: 0, active: 0, silent: 0 })
  })
})

describe('sensorCountLine', () => {
  const counts = { total: 10, active: 6, silent: 4 }

  it('shows the total when nothing is filtered', () => {
    expect(sensorCountLine(texts, counts, 'all')).toBe(
      'Showing 10 of 10 sensors — 4 with no recent readings',
    )
  })

  it('follows the filter for the shown count', () => {
    expect(sensorCountLine(texts, counts, 'active')).toContain('Showing 6 of 10')
    expect(sensorCountLine(texts, counts, 'inactive')).toContain('Showing 4 of 10')
  })

  // Coverage does not stop being true because the reader hid the silent ones.
  it('keeps the silent tail on every status', () => {
    for (const s of ['all', 'active', 'inactive']) {
      expect(sensorCountLine(texts, counts, s)).toContain('4 with no recent readings')
    }
  })
})
