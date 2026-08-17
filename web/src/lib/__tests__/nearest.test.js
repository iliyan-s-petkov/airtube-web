import { describe, it, expect } from 'vitest'
import { nearestArea } from '../nearest.js'

const areas = [
  { slug: 'sofia', lon: 23.3219, lat: 42.6977, zoom: 11 },
  { slug: 'plovdiv', lon: 24.7453, lat: 42.1354, zoom: 11 },
  { slug: 'varna', lon: 27.9147, lat: 43.2141, zoom: 11 },
]

describe('nearestArea', () => {
  it('picks the closest centroid', () => {
    expect(nearestArea([23.4, 42.7], areas).slug).toBe('sofia')
    expect(nearestArea([27.8, 43.2], areas).slug).toBe('varna')
  })

  // Degrees of longitude shrink with latitude. At 43°N one degree of longitude
  // is ~0.73 of one degree of latitude, so a plain sqrt(dx²+dy²) on raw degrees
  // overstates east-west distance by a third and can pick the wrong city.
  it('accounts for longitude convergence', () => {
    const pair = [
      { slug: 'east', lon: 24.0, lat: 43.0, zoom: 11 },
      { slug: 'north', lon: 23.0, lat: 43.9, zoom: 11 },
    ]
    // 1.0° east at 43°N is ~81 km; 0.9° north is ~100 km. 'east' is closer.
    expect(nearestArea([23.0, 43.0], pair).slug).toBe('east')
  })

  it('returns null when there are no areas', () => {
    expect(nearestArea([23, 42], [])).toBeNull()
  })
})
