import { describe, it, expect } from 'vitest'
import { DAY, tickMode, formatTick, tickValues } from '../timeaxis.js'

// Fixed locale and UTC-safe instants: these assertions are about which UNIT the
// axis prints, not about how a particular machine's timezone renders it, so
// every timestamp below is chosen to name the same day on either side of the
// dateline-free part of the world this site serves (Europe/Sofia).
const NOON = Date.parse('2026-09-07T12:00:00Z') / 1000

describe('tickMode', () => {
  it('prints clock times for a day and for two', () => {
    expect(tickMode(DAY)).toBe('hour')
    expect(tickMode(2 * DAY)).toBe('hour')
  })

  it('prints dates once past two days', () => {
    expect(tickMode(2 * DAY + 1)).toBe('day')
    expect(tickMode(7 * DAY)).toBe('day')
    expect(tickMode(60 * DAY)).toBe('day')
  })

  it('prints months beyond two of them', () => {
    expect(tickMode(60 * DAY + 1)).toBe('month')
    expect(tickMode(365 * DAY)).toBe('month')
  })

  // An empty or single-point series has no span. It must still pick a mode
  // rather than produce undefined labels.
  it('falls back to clock times for no span at all', () => {
    expect(tickMode(0)).toBe('hour')
    expect(tickMode(NaN)).toBe('hour')
  })
})

describe('formatTick', () => {
  it('gives an hour label a clock and no date', () => {
    const label = formatTick(NOON, 'hour', 'en-GB')
    expect(label).toMatch(/\d{2}:\d{2}/)
    expect(label).not.toMatch(/Sep/)
  })

  it('gives a day label a date and no clock', () => {
    const label = formatTick(NOON, 'day', 'en-GB')
    expect(label).toContain('Sep')
    expect(label).not.toMatch(/\d{2}:\d{2}/)
  })

  it('gives a month label the year, so a year of data is not twelve nameless months', () => {
    expect(formatTick(NOON, 'month', 'en-GB')).toContain('2026')
  })

  it('speaks the page language', () => {
    expect(formatTick(NOON, 'month', 'bg')).not.toBe(formatTick(NOON, 'month', 'en-GB'))
  })
})

describe('tickValues', () => {
  const xs = (n, step) => Array.from({ length: n }, (_, i) => NOON + i * step)

  it('labels a 24-hour plot with clock times', () => {
    const values = tickValues(xs(288, 300), 'en-GB')
    expect(values(null, [NOON])[0]).toMatch(/\d{2}:\d{2}/)
  })

  // The reported bug: a year asked for, eight days returned, hours printed.
  // The span is what the axis answers to, so eight days reads as dates.
  it('labels a week-wide plot with dates', () => {
    const values = tickValues(xs(8, DAY), 'en-GB')
    expect(values(null, [NOON])[0]).toContain('Sep')
  })

  it('labels a year-wide plot with months', () => {
    const values = tickValues(xs(365, DAY), 'en-GB')
    expect(values(null, [NOON])[0]).toContain('2026')
  })

  it('labels every split it is handed, in order', () => {
    const values = tickValues(xs(365, DAY), 'en-GB')
    const splits = [NOON, NOON + 40 * DAY, NOON + 200 * DAY]
    expect(values(null, splits)).toEqual(splits.map((s) => formatTick(s, 'month', 'en-GB')))
  })

  // uPlot spaces ticks by pixels: on a week-wide plot several land in one day,
  // and repeating the date makes one day look like four.
  it('blanks a label that repeats the one before it', () => {
    const values = tickValues(xs(8, DAY), 'en-GB')
    const noon = NOON
    const got = values(null, [noon, noon + 3600, noon + 7200, noon + DAY])
    expect(got[0]).toContain('Sep')
    expect(got[1]).toBe('')
    expect(got[2]).toBe('')
    expect(got[3]).not.toBe('')
  })

  it('survives a plot with no points', () => {
    expect(tickValues([], 'en-GB')(null, [NOON])[0]).toMatch(/\d{2}:\d{2}/)
  })
})
