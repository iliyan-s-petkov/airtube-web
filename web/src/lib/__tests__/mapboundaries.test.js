import { describe, it, expect } from 'vitest'
import { cellArea } from '../mapboundaries.js'

  // With the grid on at every zoom the country is visible at, the area markers
  // are hidden — so a cell is the only thing left to click, and it has to be
  // the way into a province. It resolves to the nearest area centroid, the same
  // rule the locate button navigates by.
  describe('cellArea', () => {
    const areas = [
      { slug: 'sofia', lon: 23.32, lat: 42.7 },
      { slug: 'varna', lon: 27.91, lat: 43.2 },
    ]

    it('selects the area the click falls nearest', () => {
      expect(cellArea({ areas }, { lng: 27.8, lat: 43.1 })).toBe('varna')
      expect(cellArea({ areas }, { lng: 23.4, lat: 42.6 })).toBe('sofia')
    })

    it('selects nothing on a map already scoped to one area', () => {
      expect(cellArea({ areas, slug: 'sofia' }, { lng: 27.8, lat: 43.1 })).toBeNull()
    })

    it('selects nothing before the area list has loaded', () => {
      expect(cellArea({ areas: [] }, { lng: 27.8, lat: 43.1 })).toBeNull()
      expect(cellArea({}, { lng: 27.8, lat: 43.1 })).toBeNull()
    })

    it('selects nothing for a click with no position', () => {
      expect(cellArea({ areas }, undefined)).toBeNull()
    })
  })
