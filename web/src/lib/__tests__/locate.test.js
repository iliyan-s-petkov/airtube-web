import { describe, it, expect } from 'vitest'
import { applyLocate } from '../locate.js'

const defaultView = { lon: 25.4858, lat: 42.7339, zoom: 7 }

describe('applyLocate', () => {
  // source: "default" means the server could NOT place the visitor and handed
  // back the national view the page already opened on. Moving the map then is a
  // visible jump to where it already is — and it would adopt a slug the visitor
  // never chose, unlocking the sensor tier for an area picked at random.
  it('does not move for source: "default"', () => {
    const out = applyLocate({ source: 'default', slug: 'bg', lon: 25.4, lat: 42.7, zoom: 7 }, { defaultView })
    expect(out.move).toBe(false)
    expect(out.slug).toBeNull()
  })

  it('moves and adopts the slug for source: "geoip"', () => {
    const out = applyLocate({ source: 'geoip', slug: 'sofia', lon: 23.32, lat: 42.7, zoom: 11 }, { defaultView })
    expect(out).toEqual({ move: true, centre: [23.32, 42.7], zoom: 11, slug: 'sofia' })
  })

  // The endpoint degrades to the national view under load (a full admission
  // pool) and on a failed lookup. A rejected fetch must land in the same
  // "stay put" branch rather than throwing out of the map's init.
  it('does not move when the lookup failed', () => {
    expect(applyLocate(null, { defaultView }).move).toBe(false)
  })

  it('does not move for an unknown source value', () => {
    expect(applyLocate({ source: 'guess', slug: 'x', lon: 1, lat: 2, zoom: 9 }, { defaultView }).move).toBe(false)
  })
})
