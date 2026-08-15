import { describe, it, expect } from 'vitest'
import { colourFor } from '../colour.js'

// Bands as /api/v1/scales serves them: ascending, inclusive `upper`, and the
// top band's upper is null rather than a sentinel a caller could plot.
// This fixture mimics an API response and is NOT production code — it is
// permitted to contain hex values under the task-4 ruling; production files
// in web/src must never contain a band hex.
const BANDS = [
  { label: 'Good', upper: 5, colour: '#50f0e6' },
  { label: 'Fair', upper: 10, colour: '#50ccaa' },
  { label: 'Moderate', upper: 20, colour: '#f0e641' },
  { label: 'Extremely poor', upper: null, colour: '#7d2181' },
]

// The no-data colour is configuration now (arrives as a data-* attribute), not
// a module constant — restated here as a literal because these tests are
// about band-selection logic, not about the specific grey.
const NO_DATA_COLOUR = '#9ca3af'

describe('colourFor', () => {
  it('picks the band containing the value', () => {
    expect(colourFor(3, BANDS, NO_DATA_COLOUR)).toBe('#50f0e6')
    expect(colourFor(7, BANDS, NO_DATA_COLOUR)).toBe('#50ccaa')
  })

  // `upper` is INCLUSIVE. "Exactly 50 µg/m³" landing in the wrong band is the
  // kind of bug nobody notices and a regulator would.
  it('treats upper as inclusive', () => {
    expect(colourFor(5, BANDS, NO_DATA_COLOUR)).toBe('#50f0e6')
    expect(colourFor(10, BANDS, NO_DATA_COLOUR)).toBe('#50ccaa')
  })

  it('falls to the open top band above every finite upper', () => {
    expect(colourFor(500, BANDS, NO_DATA_COLOUR)).toBe('#7d2181')
  })

  // No data is not "good". Returning the first band would paint an area with no
  // readings the same colour as the cleanest air in the country.
  it('returns the no-data colour for null and undefined', () => {
    expect(colourFor(null, BANDS, NO_DATA_COLOUR)).toBe(NO_DATA_COLOUR)
    expect(colourFor(undefined, BANDS, NO_DATA_COLOUR)).toBe(NO_DATA_COLOUR)
  })

  it('returns the no-data colour when there are no bands', () => {
    expect(colourFor(10, [], NO_DATA_COLOUR)).toBe(NO_DATA_COLOUR)
    expect(colourFor(10, undefined, NO_DATA_COLOUR)).toBe(NO_DATA_COLOUR)
  })

  // NaN and negative readings are things real sensor data actually produces.
  it('returns the no-data colour for NaN', () => {
    expect(colourFor(NaN, BANDS, NO_DATA_COLOUR)).toBe(NO_DATA_COLOUR)
  })

  it('treats a negative reading and zero as falling in the lowest band', () => {
    expect(colourFor(-1, BANDS, NO_DATA_COLOUR)).toBe('#50f0e6')
    expect(colourFor(0, BANDS, NO_DATA_COLOUR)).toBe('#50f0e6')
  })

  // Pinned against a DIFFERENT literal than every other case in this file, so
  // this assertion cannot pass by accident of a hardcoded '#9ca3af' surviving
  // somewhere in colour.js — proof that noDataColour is a real parameter, not
  // a name that shadows an internal default.
  it('returns whatever no-data colour the caller passes, not a fixed one', () => {
    expect(colourFor(null, BANDS, '#ff00ff')).toBe('#ff00ff')
  })
})
