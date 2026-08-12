import { describe, it, expect } from 'vitest'
import { toUplotData } from '../series.js'

describe('toUplotData', () => {
  // uPlot wants x in epoch SECONDS. Handing it milliseconds is the classic way
  // a chart silently renders every point in 1970 with no error anywhere.
  //
  // Expected value verified with:
  //   node -e "console.log(Date.parse('2026-08-11T00:00:00Z')/1000)"
  // which prints 1786406400 (the brief's own text quoted 1786492800, which is
  // actually 2026-08-12T00:00:00Z — a stale/incorrect literal; this test uses
  // the value computed directly from Date.parse).
  it('converts RFC3339 timestamps to epoch seconds', () => {
    const [xs, ys] = toUplotData({ t: ['2026-08-11T00:00:00Z'], v: [12.5] })
    expect(xs).toEqual([1786406400])
    expect(ys).toEqual([12.5])
    // Guards the units directly: a millisecond value is three orders of
    // magnitude larger than any plausible epoch-second value.
    expect(xs[0]).toBeLessThan(1e11)
  })

  // uPlot given [[], []] must render an empty frame. Given null it throws, and
  // the whole chart island disappears with a console error.
  it('returns two empty arrays for an empty series', () => {
    expect(toUplotData({ t: [], v: [] })).toEqual([[], []])
    expect(toUplotData(undefined)).toEqual([[], []])
    expect(toUplotData({})).toEqual([[], []])
  })

  it('drops trailing values with no matching timestamp', () => {
    const [xs, ys] = toUplotData({ t: ['2026-08-11T00:00:00Z'], v: [1, 2, 3] })
    expect(xs).toHaveLength(1)
    expect(ys).toHaveLength(1)
  })
})
