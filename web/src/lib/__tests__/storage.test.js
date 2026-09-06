import { describe, it, expect } from 'vitest'
import { readFlag, writeFlag, safeStorage } from '../storage.js'

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
