import { describe, it, expect } from 'vitest'
import { readFlag, writeFlag, safeStorage, readChoice, writeChoice } from '../storage.js'

// A stand-in for the real thing, so a test can be a browser that has decided
// not to cooperate without needing that browser.
function store(initial = {}, { throwOnGet = false, throwOnSet = false } = {}) {
  const map = new Map(Object.entries(initial))
  return {
    map,
    getItem(k) { if (throwOnGet) throw new Error('blocked'); return map.has(k) ? map.get(k) : null },
    setItem(k, v) { if (throwOnSet) throw new Error('blocked'); map.set(k, v) },
  }
}

describe('readFlag', () => {
  it('reads the two spellings it writes', () => {
    const s = store({ a: 'true', b: 'false' })
    expect(readFlag('a', false, s)).toBe(true)
    expect(readFlag('b', true, s)).toBe(false)
  })

  // The whole reason the fallback is a parameter: auto-refresh defaults ON and
  // the layer menu defaults OFF, and an unreadable value must not quietly turn
  // either of them into `false`.
  it('falls back for an absent, a garbage, or an unreadable value', () => {
    expect(readFlag('missing', true, store())).toBe(true)
    expect(readFlag('x', true, store({ x: 'yes' }))).toBe(true)
    expect(readFlag('x', true, store({ x: '1' }))).toBe(true)
    expect(readFlag('x', true, store({}, { throwOnGet: true }))).toBe(true)
    expect(readFlag('x', false, store({}, { throwOnGet: true }))).toBe(false)
  })

  it('falls back when there is no storage at all', () => {
    expect(readFlag('x', true, null)).toBe(true)
  })
})

describe('writeFlag', () => {
  it('writes the spelling readFlag reads', () => {
    const s = store()
    writeFlag('x', true, s)
    expect(s.map.get('x')).toBe('true')
    writeFlag('x', false, s)
    expect(s.map.get('x')).toBe('false')
  })

  // A full quota or a blocked origin must not take the control down with it:
  // the preference is forgotten, the click still works.
  it('swallows a storage that refuses to be written to', () => {
    expect(() => writeFlag('x', true, store({}, { throwOnSet: true }))).not.toThrow()
    expect(() => writeFlag('x', true, null)).not.toThrow()
  })
})

describe('safeStorage', () => {
  // Private mode throws on the property ACCESS, not on getItem — which is why
  // the try wraps the access rather than the read.
  it('returns null when reaching for localStorage throws', () => {
    const had = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() { throw new Error('blocked') },
    })
    expect(safeStorage()).toBe(null)
    if (had) Object.defineProperty(globalThis, 'localStorage', had)
    else delete globalThis.localStorage
  })

  it('returns null when there is no localStorage', () => {
    const had = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
    Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: undefined })
    expect(safeStorage()).toBe(null)
    if (had) Object.defineProperty(globalThis, 'localStorage', had)
    else delete globalThis.localStorage
  })
})


// readFlag covers the one-boolean case, which is most preferences. The
// playback speed is one of a fixed few numbers instead, and the same rule
// applies: a stored value outside the set is treated as unset.
describe('readChoice/writeChoice', () => {
  const fake = () => {
    const kv = new Map()
    return {
      kv,
      getItem: (k) => (kv.has(k) ? kv.get(k) : null),
      setItem: (k, v) => kv.set(k, v),
    }
  }

  it('returns the fallback when nothing is stored', () => {
    expect(readChoice('k', [1, 0.5], 1, fake())).toBe(1)
  })

  it('reads back what was written', () => {
    const s = fake()
    writeChoice('k', 0.5, s)
    expect(readChoice('k', [1, 0.5], 1, s)).toBe(0.5)
  })

  // The defect this exists to prevent: localStorage stores strings, so a
  // careless read hands the caller "0.5" and every === against 0.5 fails.
  it('returns a member of the allowed set, not the stored string', () => {
    const s = fake()
    writeChoice('k', 0.25, s)
    const got = readChoice('k', [1, 0.5, 0.25], 1, s)
    expect(got).toBe(0.25)
    expect(typeof got).toBe('number')
  })

  it('treats a value outside the set as unset', () => {
    const s = fake()
    s.setItem('k', '3')
    expect(readChoice('k', [1, 0.5], 1, s)).toBe(1)
    s.setItem('k', 'nonsense')
    expect(readChoice('k', [1, 0.5], 1, s)).toBe(1)
  })

  // Private mode throws on the access itself; a preference that cannot be
  // stored must still leave a working control.
  it('survives storage that throws', () => {
    const boom = { getItem: () => { throw new Error('denied') }, setItem: () => { throw new Error('denied') } }
    expect(readChoice('k', [1], 1, boom)).toBe(1)
    expect(() => writeChoice('k', 1, boom)).not.toThrow()
  })

  it('survives no storage at all', () => {
    expect(readChoice('k', [1], 1, null)).toBe(1)
    expect(() => writeChoice('k', 1, null)).not.toThrow()
  })
})
