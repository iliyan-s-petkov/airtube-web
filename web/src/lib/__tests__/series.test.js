import { describe, it, expect } from 'vitest'
import { toUplotData, mergeSeries } from '../series.js'

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

const t0 = '2026-08-11T00:00:00Z'
const t1 = '2026-08-11T01:00:00Z'
const t2 = '2026-08-11T02:00:00Z'
const sec = (s) => Date.parse(s) / 1000

describe('mergeSeries', () => {
  it('keeps one series unchanged', () => {
    expect(mergeSeries([{ t: [t0, t1], v: [1, 2] }]))
      .toEqual([[sec(t0), sec(t1)], [1, 2]])
  })

  // The whole point: two metrics from two devices, one of which missed the
  // middle hour. Aligning by position would put its 02:00 value at 01:00.
  it('aligns on timestamps, not on position, and holes become null', () => {
    const [xs, a, b] = mergeSeries([
      { t: [t0, t1, t2], v: [10, 11, 12] },
      { t: [t0, t2], v: [20, 22] },
    ])
    expect(xs).toEqual([sec(t0), sec(t1), sec(t2)])
    expect(a).toEqual([10, 11, 12])
    expect(b).toEqual([20, null, 22])
  })

  // Union, not intersection: a timestamp only the second series has must widen
  // the x axis rather than drop that reading.
  it('takes the union of the timestamps, sorted ascending', () => {
    const [xs, a, b] = mergeSeries([
      { t: [t2], v: [12] },
      { t: [t0], v: [20] },
    ])
    expect(xs).toEqual([sec(t0), sec(t2)])
    expect(a).toEqual([null, 12])
    expect(b).toEqual([20, null])
  })

  // A real value of 0 must survive as 0 — the missing marker is null, and a
  // truthiness test here would erase clean air and a freezing morning alike.
  it('keeps a zero reading as zero', () => {
    const [, a] = mergeSeries([{ t: [t0], v: [0] }])
    expect(a).toEqual([0])
  })

  it('returns an empty x axis when every series is empty', () => {
    expect(mergeSeries([{ t: [], v: [] }, undefined])).toEqual([[], [], []])
  })
})
