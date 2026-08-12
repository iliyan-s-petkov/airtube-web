import { describe, it, expect } from 'vitest'
import { tierFor } from '../tier.js'

// The boundaries are <, not <=. An off-by-one here silently changes which
// endpoint a whole zoom level hits — and at z=11 that is the difference between
// one cached aggregate and a per-area request that spends enumeration budget.
// Each boundary is asserted explicitly rather than sampled.
describe('tierFor', () => {
  it('serves the country aggregate below zoom 9', () => {
    expect(tierFor(0)).toBe('country')
    expect(tierFor(8.99)).toBe('country')
  })
  it('serves the city aggregate from 9 up to but not including 11', () => {
    expect(tierFor(9)).toBe('city')
    expect(tierFor(10.99)).toBe('city')
  })
  it('serves individual sensors from 11 up', () => {
    expect(tierFor(11)).toBe('sensors')
    expect(tierFor(18)).toBe('sensors')
  })
})
