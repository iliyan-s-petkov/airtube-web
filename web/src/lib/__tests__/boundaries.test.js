import { describe, it, expect } from 'vitest'
import {
  boundsOf, findBoundary, selectedFilter,
  boundaryFillPaint, boundaryLinePaint, boundarySelectedPaint,
} from '../boundaries.js'

const polygon = (ring) => ({ type: 'Feature', properties: { slug: 'p' }, geometry: { type: 'Polygon', coordinates: [ring] } })

describe('boundsOf', () => {
  it('is the extent of a polygon ring', () => {
    expect(boundsOf(polygon([[23, 42], [24, 42], [24, 43], [23, 43], [23, 42]])))
      .toEqual([[23, 42], [24, 43]])
  })

  // A province with an exclave has its positions one level deeper. Fitting only
  // the first polygon would leave the rest of the province off screen.
  it('spans every polygon of a MultiPolygon', () => {
    const f = {
      geometry: {
        type: 'MultiPolygon',
        coordinates: [
          [[[23, 42], [24, 42], [24, 43], [23, 42]]],
          [[[26, 41], [27, 41], [27, 41.5], [26, 41]]],
        ],
      },
    }
    expect(boundsOf(f)).toEqual([[23, 41], [27, 43]])
  })

  // An empty geometry must not become a camera move to null island.
  it('answers null when there is no position to fit', () => {
    expect(boundsOf(null)).toBe(null)
    expect(boundsOf({})).toBe(null)
    expect(boundsOf({ geometry: { type: 'Polygon', coordinates: [] } })).toBe(null)
  })

  // Longitude and latitude are both signed, and a min seeded from the first
  // position rather than from Infinity would be right only by accident.
  it('keeps negative and zero positions', () => {
    expect(boundsOf(polygon([[-1, 0], [0, -2], [-1, 0]]))).toEqual([[-1, -2], [0, 0]])
  })
})

describe('findBoundary', () => {
  const body = { features: [polygon([[0, 0]]), { properties: { slug: 'q' }, geometry: null }] }

  it('finds the feature by slug', () => {
    expect(findBoundary(body, 'q').properties.slug).toBe('q')
  })

  it('answers null for a slug the collection does not carry, or for none', () => {
    expect(findBoundary(body, 'zzz')).toBe(null)
    expect(findBoundary(body, null)).toBe(null)
    expect(findBoundary(null, 'p')).toBe(null)
  })
})

describe('selectedFilter', () => {
  it('matches the selected slug', () => {
    expect(selectedFilter('sofiya-grad-oblast')).toEqual(['==', ['get', 'slug'], 'sofiya-grad-oblast'])
  })

  // No selection has to match nothing. A filter comparing against null matches
  // every feature whose property is missing, which on this source is none of
  // them — but '' is the value that says so without depending on that.
  it('matches nothing when nothing is selected', () => {
    expect(selectedFilter(null)).toEqual(['==', ['get', 'slug'], ''])
  })
})

describe('paint', () => {
  const cfg = { labelColour: 'rgb(1,2,3)' }

  // The hit target is invisible on purpose; a fill anyone can see would wash
  // out the readings under it.
  it('paints the hit fill at zero opacity', () => {
    expect(boundaryFillPaint(cfg)['fill-opacity']).toBe(0)
  })

  // The selection is weight, not colour: the same ink at full opacity, thicker.
  it('draws the selected outline heavier than the rest', () => {
    const line = boundaryLinePaint(cfg)
    const selected = boundarySelectedPaint(cfg)
    expect(selected['line-width']).toBeGreaterThan(line['line-width'])
    expect(selected['line-opacity']).toBeGreaterThan(line['line-opacity'])
    expect(selected['line-color']).toBe(cfg.labelColour)
    expect(line['line-color']).toBe(cfg.labelColour)
  })
})
