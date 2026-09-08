import { describe, it, expect } from 'vitest'
import { CUSTOM, toInstant, customRangeValid, periodQuery } from './period.js'

describe('toInstant', () => {
  it('reads a datetime-local value in the reader\'s zone and states the offset', () => {
    const local = '2026-08-14T12:30'
    expect(toInstant(local)).toBe(new Date(2026, 7, 14, 12, 30).toISOString())
  })

  it('is null for an empty or unparseable value', () => {
    expect(toInstant('')).toBeNull()
    expect(toInstant('not a time')).toBeNull()
  })
})

describe('customRangeValid', () => {
  it('needs both ends, with the start earlier', () => {
    expect(customRangeValid('2026-08-14T00:00', '2026-08-15T00:00')).toBe(true)
    expect(customRangeValid('2026-08-15T00:00', '2026-08-14T00:00')).toBe(false)
    expect(customRangeValid('2026-08-14T00:00', '2026-08-14T00:00')).toBe(false)
    expect(customRangeValid('2026-08-14T00:00', '')).toBe(false)
    expect(customRangeValid('', '2026-08-15T00:00')).toBe(false)
  })
})

describe('periodQuery', () => {
  it('sends a named period as its own name', () => {
    expect(periodQuery('7d', '', '')).toBe('period=7d')
  })

  it('escapes the period name rather than pasting it into the query', () => {
    expect(periodQuery('7d&metric=P1', '', '')).toBe('period=7d%26metric%3DP1')
  })

  it('sends both instants for a usable custom range', () => {
    const q = periodQuery(CUSTOM, '2026-08-14T00:00', '2026-08-15T00:00')
    expect(q).toContain(`period=${CUSTOM}`)
    expect(q).toContain(`from=${encodeURIComponent(new Date(2026, 7, 14).toISOString())}`)
    expect(q).toContain(`to=${encodeURIComponent(new Date(2026, 7, 15).toISOString())}`)
  })

  // null is the caller's cue to draw its "choose a range" message instead of
  // requesting a window the API would refuse.
  it('is null for a half filled in or backwards custom range', () => {
    expect(periodQuery(CUSTOM, '2026-08-14T00:00', '')).toBeNull()
    expect(periodQuery(CUSTOM, '2026-08-15T00:00', '2026-08-14T00:00')).toBeNull()
  })
})
