import { describe, it, expect } from 'vitest'
import { stationValues, median, areaStats } from '../areastats.js'

const body = (sensors) => ({ sensors })

const four = body({
  id: [1, 2, 3, 4],
  station: [1, 1, 3, 4],
  P2: [10, 99, 4, 6],
})

describe('stationValues', () => {
  // The second device at station 1 is the same address as the first. Counting
  // it would put one street into the set twice and drag the median with it.
  it('keeps one value per station, not one per device', () => {
    expect(stationValues(four, 'P2').map((r) => r.value)).toEqual([10, 4, 6])
  })

  it('skips a station with no usable reading', () => {
    const b = body({ id: [1, 2, 3], station: [1, 2, 3], P2: [10, null, Number.NaN] })
    expect(stationValues(b, 'P2').map((r) => r.station)).toEqual([1])
  })

  it('keeps a zero reading, which is a reading', () => {
    const b = body({ id: [1, 2], station: [1, 2], P2: [0, 5] })
    expect(stationValues(b, 'P2').map((r) => r.value)).toEqual([0, 5])
  })

  it('has nothing to say about a metric the area does not report', () => {
    expect(stationValues(four, 'noise_LAeq')).toEqual([])
    expect(stationValues(null, 'P2')).toEqual([])
  })
})

describe('median', () => {
  it('takes the middle of an odd count', () => {
    expect(median([5, 1, 3])).toBe(3)
  })

  // The server averages the two middles (internal/snapshot/hexes.go). A strip
  // that picked one of them would disagree with the page around it.
  it('averages the two middles of an even count', () => {
    expect(median([1, 3, 5, 9])).toBe(4)
  })
})

describe('areaStats', () => {
  it('reports the area around the open sensor', () => {
    expect(areaStats(four, 'P2', 3)).toEqual({
      high: 10, low: 4, median: 6, total: 3, value: 4, rank: 1,
    })
  })

  // Rank 1 is the cleanest air, so "1 of 3" reads as good news rather than as
  // an alarm — the opposite ordering would praise the dirtiest sensor.
  it('ranks from the cleanest reading up', () => {
    expect(areaStats(four, 'P2', 1).rank).toBe(3)
    expect(areaStats(four, 'P2', 4).rank).toBe(2)
  })

  // Device 2 stands at station 1 and a deep link may name either. Both are the
  // same dot on the map, so both must find the same place in the ranking.
  it('resolves any device at an address to that address', () => {
    expect(areaStats(four, 'P2', 2).rank).toBe(areaStats(four, 'P2', 1).rank)
  })

  it('takes the id as the string a URL carries', () => {
    expect(areaStats(four, 'P2', '3').rank).toBe(1)
  })

  // The other three figures are still true when the open sensor measures
  // something else; only its own place among them is not.
  it('drops the rank when the open sensor reports nothing for the metric', () => {
    const stats = areaStats(four, 'P2', 999)
    expect(stats.rank).toBe(null)
    expect(stats.value).toBe(null)
    expect(stats.high).toBe(10)
  })

  // A highest, a lowest and a median of one station are three names for one
  // number, which is not a comparison.
  it('says nothing at all where a single station reports', () => {
    expect(areaStats(body({ id: [1], station: [1], P2: [5] }), 'P2', 1)).toBe(null)
  })
})
