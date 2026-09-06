import { describe, it, expect } from 'vitest'
import { statusText, formatTime, AUTO_KEY, AUTO_INTERVAL_MS } from '../freshness.js'

const T = { updated: 'Данни от', loading: 'Обновяване…', failed: 'Обновяването не успя' }

// 2026-03-01 14:07 UTC. Asserted through formatTime rather than against a
// literal "14:07", because the runner's zone is not the reader's and this test
// is about which of the three sentences gets picked, not about the clock.
const AT = Date.UTC(2026, 2, 1, 14, 7)

describe('statusText', () => {
  it('says it is working before it says anything else', () => {
    // busy wins over both a previous time and a previous failure: the reader
    // needs to know a request is in flight, not what the last one did.
    expect(statusText({ busy: true, at: AT, failed: true }, T)).toBe(T.loading)
  })

  it('reports a failure rather than a stale time', () => {
    expect(statusText({ busy: false, at: AT, failed: true }, T)).toBe(T.failed)
  })

  it('states the time of the last success', () => {
    expect(statusText({ busy: false, at: AT, failed: false }, T)).toBe(`${T.updated} ${formatTime(AT)}`)
  })

  // Absence stated plainly (DESIGN.md §2.3): a page that has never loaded says
  // nothing at all rather than announcing that it has no time to show.
  it('says nothing when there has never been a reading', () => {
    expect(statusText({ busy: false, at: null, failed: false }, T)).toBe('')
    expect(statusText({ busy: false, at: undefined, failed: false }, T)).toBe('')
  })

  // 0 is a legitimate epoch instant, and `at == null` is what distinguishes
  // "never" from it. A truthiness check here would silence the line.
  it('treats epoch zero as a time, not as an absence', () => {
    expect(statusText({ busy: false, at: 0, failed: false }, T)).not.toBe('')
  })
})

describe('formatTime', () => {
  it('writes hours and minutes in the page language', () => {
    expect(formatTime(AT, 'bg')).toMatch(/^\d{2}:\d{2}$/)
    // en-US asks for a 12-hour clock, so the two spellings must differ — this
    // is what passing `lang` buys, and a hard-coded format would lose it.
    expect(formatTime(AT, 'en-US')).toMatch(/AM|PM/)
  })
})

describe('constants', () => {
  it('keys the preference under the site namespace', () => {
    expect(AUTO_KEY).toBe('airbg:auto-refresh')
  })

  // Five minutes. Faster asks the origin for numbers that have not changed;
  // much slower and the timestamp stops being reassurance.
  it('polls at five minutes', () => {
    expect(AUTO_INTERVAL_MS).toBe(300000)
  })
})
