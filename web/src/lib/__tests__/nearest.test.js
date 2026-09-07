import { describe, it, expect } from 'vitest'
import { nearestArea, nearestSensor } from '../nearest.js'

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

const body = {
  sensors: {
    id: [11338, 22, 7],
    lon: [23.30, 23.40, 27.90],
    lat: [42.70, 42.70, 43.20],
  },
}

describe('nearestSensor', () => {
  it('picks the closest sensor in the loaded body', () => {
    expect(nearestSensor([23.31, 42.70], body)).toEqual({ id: 11338, lon: 23.30, lat: 42.70 })
    expect(nearestSensor([27.85, 43.21], body)).toEqual({ id: 7, lon: 27.90, lat: 43.20 })
  })

  // Same convergence as above; two sensors in one city are close enough for
  // the unscaled comparison to pick the wrong one.
  it('accounts for longitude convergence', () => {
    const pair = { sensors: { id: [1, 2], lon: [24.0, 23.0], lat: [43.0, 43.9] } }
    expect(nearestSensor([23.0, 43.0], pair).id).toBe(1)
  })

  // A null position would centre the map on 0,0, in the Atlantic.
  it('skips a sensor with no position', () => {
    const partial = { sensors: { id: [1, 2], lon: [null, 23.4], lat: [42.7, 42.7] } }
    expect(nearestSensor([23.3, 42.7], partial).id).toBe(2)
  })

  it('returns null when there is no body or no sensor to pick', () => {
    expect(nearestSensor([23, 42], null)).toBeNull()
    expect(nearestSensor([23, 42], { sensors: { id: [], lon: [], lat: [] } })).toBeNull()
    expect(nearestSensor([23, 42], { sensors: { id: [1], lon: [null], lat: [null] } })).toBeNull()
  })
})
