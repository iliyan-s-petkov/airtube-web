import { describe, it, expect } from 'vitest'
import { localNow, periodQuery, toInstant } from '../period.js'

describe('localNow', () => {
  // The value a datetime-local input accepts is the reader's WALL CLOCK with no
  // zone on it. toISOString would state UTC and move the "now" button by the
  // reader's own offset, which is the bug this function exists to avoid.
  it('states the wall clock, not UTC', () => {
    const d = new Date(2026, 8, 8, 7, 5)
    expect(localNow(d)).toBe('2026-09-08T07:05')
  })

  it('pads every field to the width the input requires', () => {
    expect(localNow(new Date(2026, 0, 2, 3, 4))).toBe('2026-01-02T03:04')
  })

  // Round trip: what the button writes has to be a value the query builder can
  // read back, or the chart asks for a window the API refuses.
  it('produces a value the range query accepts', () => {
    const from = localNow(new Date(2026, 8, 7, 12, 0))
    const to = localNow(new Date(2026, 8, 8, 12, 0))
    expect(toInstant(to)).not.toBeNull()
    expect(periodQuery('custom', from, to)).toContain('period=custom')
  })
})
