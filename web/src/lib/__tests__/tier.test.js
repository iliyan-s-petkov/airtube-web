import { describe, it, expect } from 'vitest'
import { tierFor } from '../tier.js'

// The zoom thresholds match airbg.yaml's frontend.zoom_city (9) and
// frontend.zoom_sensor (11), restated as literals: these tests are about the
// boundary logic, not about the specific numbers, but tierFor no longer
// carries a default so a threshold has to come from somewhere.
const ZOOM_CITY = 9
const ZOOM_SENSOR = 11

// The boundaries are <, not <=. An off-by-one here silently changes which
// endpoint a whole zoom level hits — and at the sensor boundary that is the
// difference between one cached aggregate and a per-area request that spends
// enumeration budget. Each boundary is asserted explicitly rather than
// sampled.
describe('tierFor', () => {
  it('serves the country aggregate below zoom_city', () => {
    expect(tierFor(0, ZOOM_CITY, ZOOM_SENSOR)).toBe('country')
    expect(tierFor(8.99, ZOOM_CITY, ZOOM_SENSOR)).toBe('country')
  })
  it('serves the city aggregate from zoom_city up to but not including zoom_sensor', () => {
    expect(tierFor(9, ZOOM_CITY, ZOOM_SENSOR)).toBe('city')
    expect(tierFor(10.99, ZOOM_CITY, ZOOM_SENSOR)).toBe('city')
  })
  it('serves individual sensors from zoom_sensor up', () => {
    expect(tierFor(11, ZOOM_CITY, ZOOM_SENSOR)).toBe('sensors')
    expect(tierFor(18, ZOOM_CITY, ZOOM_SENSOR)).toBe('sensors')
  })
  // Pinned against different thresholds than every other case, so this cannot
  // pass by accident of ZOOM_CITY/ZOOM_SENSOR being hardcoded inside tier.js
  // itself — proof the parameters are real, not shadowed defaults.
  it('honours whatever thresholds the caller passes, not fixed ones', () => {
    expect(tierFor(5, 3, 20)).toBe('city')
    expect(tierFor(2, 3, 20)).toBe('country')
  })
})
