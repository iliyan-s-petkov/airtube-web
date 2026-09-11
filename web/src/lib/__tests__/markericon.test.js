import { describe, it, expect } from 'vitest'
import { diamondImage, DIAMOND_PX, DIAMOND_RADIUS_PX } from '../markericon.js'

// MapLibre's SDF shader cuts the glyph at alpha 192 (its buffer, 0.75). Every
// assertion here is about where that cut falls, because that — not the raw
// numbers — is the shape a reader sees.
const EDGE = 192
const alphaAt = (img, x, y) => img.data[(y * img.width + x) * 4 + 3]

describe('diamondImage', () => {
  const img = diamondImage()

  it('is an RGBA buffer of the nominal size', () => {
    expect(img.width).toBe(DIAMOND_PX)
    expect(img.height).toBe(DIAMOND_PX)
    expect(img.data).toHaveLength(DIAMOND_PX * DIAMOND_PX * 4)
  })

  it('puts the shape edge at the SDF cut', () => {
    const c = DIAMOND_PX / 2
    // On the horizontal axis the boundary is one radius out from centre.
    expect(alphaAt(img, c, c)).toBeGreaterThan(EDGE)
    // A pixel either side of it, not the boundary pixel itself: samples are
    // taken at pixel centres, so the one straddling the edge is the edge.
    expect(alphaAt(img, c + DIAMOND_RADIUS_PX - 2, c)).toBeGreaterThan(EDGE)
    expect(alphaAt(img, c + DIAMOND_RADIUS_PX + 1, c)).toBeLessThan(EDGE)
  })

  // The one thing separating this from a circle: the corners are outside.
  it('cuts the corners a circle of the same radius would keep', () => {
    const c = DIAMOND_PX / 2
    const d = Math.round(DIAMOND_RADIUS_PX * 0.6)
    expect(alphaAt(img, c + d, c + d)).toBeLessThan(EDGE)
  })

  it('leaves the halo somewhere to draw', () => {
    expect(alphaAt(img, 0, 0)).toBeLessThan(EDGE)
    expect(alphaAt(img, DIAMOND_PX - 1, DIAMOND_PX / 2)).toBeLessThan(EDGE)
  })

  it('scales the glyph with the requested size rather than fattening it', () => {
    const big = diamondImage(DIAMOND_PX * 2)
    const c = DIAMOND_PX
    expect(alphaAt(big, c + 2 * DIAMOND_RADIUS_PX - 2, c)).toBeGreaterThan(EDGE)
    expect(alphaAt(big, c + 2 * DIAMOND_RADIUS_PX + 2, c)).toBeLessThan(EDGE)
  })
})
