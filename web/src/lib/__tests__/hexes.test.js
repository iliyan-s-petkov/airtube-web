import { describe, it, expect } from 'vitest'
import {
  resolutionForZoom,
  hexesURL,
  bboxParam,
  hexPolygon,
  hexFeatures,
  BBOX_MIN_ZOOM,
} from '../hexes.js'
import { colourFor } from '../colour.js'

// Bin centres taken from the Go implementation, which is the only authority on
// where a bin actually sits. Regenerate by printing hexCentre(axial{q,r}, res)
// from a test inside internal/snapshot. If these stop matching, the browser is
// drawing cells somewhere other than where the counts came from.
const GOLD = {
  15: { origin: [0, 0], east: [0.183704352, 0], northeast: [0.091852176, 0.116825304] },
  1: { origin: [0, 0], east: [0.012246957, 0], northeast: [0.006123478, 0.007788354] },
  0.25: { origin: [0, 0], east: [0.003061739, 0], northeast: [0.001530870, 0.001947088] },
}

const radians = (d) => (d * Math.PI) / 180
const lons = (ring) => ring.map((p) => p[0])
const lats = (ring) => ring.map((p) => p[1])

describe('hexPolygon', () => {
  it('is a closed six-corner ring', () => {
    const ring = hexPolygon(23.3, 42.7, 1)
    expect(ring).toHaveLength(7)
    expect(ring[0]).toEqual(ring[6])
    expect(new Set(ring.slice(0, 6).map(String)).size).toBe(6)
  })

  it('is pointy-top: a vertex due north, flat sides east and west', () => {
    const ring = hexPolygon(0, 0, 15).slice(0, 6)
    // Exactly one corner sits on the centre's own meridian, at the top.
    const onMeridian = ring.filter((p) => Math.abs(p[0]) < 1e-9)
    expect(onMeridian).toHaveLength(2)
    expect(Math.max(...onMeridian.map((p) => p[1]))).toBeGreaterThan(0)
    // Two corners share the extreme longitude on each side — that pair is the
    // flat east/west side. A flat-top hex would have a single corner there.
    const maxLon = Math.max(...lons(ring))
    expect(ring.filter((p) => Math.abs(p[0] - maxLon) < 1e-9)).toHaveLength(2)
  })

  // The tiling test, and the reason the Go golden values are here: the server
  // spaced these two bins one resolution apart, so the cells the browser draws
  // around them must meet exactly. A mismatched Earth radius or reference
  // latitude on either side shows up here as a gap or an overlap.
  it.each([15, 1, 0.25])('tiles against its neighbours at %s km', (res) => {
    const g = GOLD[res]
    const origin = hexPolygon(g.origin[0], g.origin[1], res).slice(0, 6)
    const east = hexPolygon(g.east[0], g.east[1], res).slice(0, 6)
    // Shared vertical edge: the origin's east side is the neighbour's west side.
    const tolerance = res * 1e-6
    expect(Math.max(...lons(origin))).toBeCloseTo(Math.min(...lons(east)), 9)
    expect(Math.abs(Math.max(...lons(origin)) - Math.min(...lons(east)))).toBeLessThan(tolerance)

    // The north-east neighbour sits a half-step over and a three-quarter step
    // up; its lowest corner meets the origin's upper-right corner.
    const ne = hexPolygon(g.northeast[0], g.northeast[1], res).slice(0, 6)
    expect(Math.min(...lats(ne))).toBeCloseTo(Math.max(...lats(origin)) - heightOf(origin) / 4, 9)
  })

  it('scales linearly with the resolution', () => {
    const wide = widthOf(hexPolygon(23.3, 42.7, 1).slice(0, 6))
    const narrow = widthOf(hexPolygon(23.3, 42.7, 0.25).slice(0, 6))
    expect(wide / narrow).toBeCloseTo(4, 6)
  })
})

function widthOf(ring) {
  return Math.max(...lons(ring)) - Math.min(...lons(ring))
}

function heightOf(ring) {
  return Math.max(...lats(ring)) - Math.min(...lats(ring))
}

describe('resolutionForZoom', () => {
  it('shrinks the cell as the map zooms in', () => {
    for (let z = 6; z < 16; z++) {
      expect(resolutionForZoom(z + 1)).toBeLessThan(resolutionForZoom(z))
      // Exactly halving per zoom level, which is what keeps the drawn cell the
      // same size on screen at every zoom.
      expect(resolutionForZoom(z) / resolutionForZoom(z + 1)).toBeCloseTo(2, 9)
    }
  })

  // The promise the constant exists to keep: a cell draws at roughly a fixed
  // number of screen pixels, at every zoom. Asserted against the published Web
  // Mercator ground resolution rather than against the module's own arithmetic,
  // so a dropped cos(lat) — which inflates every request by 1/cos(42.75) = 1.36x
  // and lands whole zoom bands on the wrong tier — shows up here.
  it('draws about a constant number of pixels wide at every zoom', () => {
    // 156543.03392 m/px at zoom 0 on the equator, narrowed by cos(lat). The
    // 2% window absorbs the radius difference between this WGS84 figure and the
    // module's own 6371 km sphere, and nothing larger.
    const mPerPx = (z) => (156543.03392 * Math.cos(radians(42.75))) / 2 ** z
    for (let z = 6; z <= 16; z++) {
      const px = (resolutionForZoom(z) * 1000) / mPerPx(z)
      expect(px).toBeGreaterThan(49)
      expect(px).toBeLessThan(51)
    }
  })

  // The whole feature in one assertion: country zoom asks for the coarse grid,
  // street zoom asks for something the address tier can serve. The exact tier is
  // the server's to pick — these are the bounds that decide which it picks.
  it('spans the published tier range across the map\'s zooms', () => {
    expect(resolutionForZoom(7)).toBeGreaterThan(15)
    expect(resolutionForZoom(16)).toBeLessThan(0.25)
  })
})

describe('bboxParam', () => {
  const bounds = (w, s, e, n) => ({
    getWest: () => w, getSouth: () => s, getEast: () => e, getNorth: () => n,
  })

  it('is omitted below BBOX_MIN_ZOOM, where the viewport holds the country', () => {
    expect(bboxParam(BBOX_MIN_ZOOM - 1, bounds(23, 42, 24, 43))).toBe('')
    expect(bboxParam(BBOX_MIN_ZOOM, bounds(23, 42, 24, 43))).not.toBe('')
  })

  it('is omitted entirely when there are no bounds', () => {
    expect(bboxParam(12, null)).toBe('')
    expect(bboxParam(12, undefined)).toBe('')
  })

  // Snapped OUTWARD, never inward: a box tighter than the viewport leaves a
  // visible strip of unpainted map along whichever edge got clipped.
  it('never snaps the box inside the viewport', () => {
    const [w, s, e, n] = bboxParam(12, bounds(23.31, 42.61, 23.44, 42.72)).split(',').map(Number)
    expect(w).toBeLessThanOrEqual(23.31)
    expect(s).toBeLessThanOrEqual(42.61)
    expect(e).toBeGreaterThanOrEqual(23.44)
    expect(n).toBeGreaterThanOrEqual(42.72)
  })

  // Two visitors panning slightly differently over the same street must produce
  // the same URL, or nothing downstream can cache a hex response.
  it('collapses nearby viewports onto one URL', () => {
    const a = bboxParam(13, bounds(23.311, 42.611, 23.339, 42.639))
    const b = bboxParam(13, bounds(23.314, 42.613, 23.341, 42.641))
    expect(a).toBe(b)
  })

  it('clamps to the legal coordinate range', () => {
    const [w, s, e, n] = bboxParam(9, bounds(-179.99, -89.99, 179.99, 89.99)).split(',').map(Number)
    expect(w).toBeGreaterThanOrEqual(-180)
    expect(s).toBeGreaterThanOrEqual(-90)
    expect(e).toBeLessThanOrEqual(180)
    expect(n).toBeLessThanOrEqual(90)
  })

  // An antimeridian-crossing viewport gives west > east, which the server
  // rejects. Dropping it keeps the request on the cached country-wide answer
  // instead of spending a round trip to be told the same thing.
  it('drops a box the server would reject', () => {
    expect(bboxParam(12, bounds(179, 42, -179, 43))).toBe('')
    expect(bboxParam(12, bounds(23, 42, 23, 43))).toBe('')
  })
})

describe('hexesURL', () => {
  const bounds = { getWest: () => 23.31, getSouth: () => 42.61, getEast: () => 23.44, getNorth: () => 42.72 }

  it('carries the resolution at every zoom and the box only when it means something', () => {
    const low = new URL(hexesURL(7, bounds), 'https://airbg.org')
    expect(low.searchParams.get('resolution_km')).toBeTruthy()
    expect(low.searchParams.has('bbox')).toBe(false)

    const high = new URL(hexesURL(13, bounds), 'https://airbg.org')
    expect(Number(high.searchParams.get('resolution_km'))).toBeLessThan(1)
    expect(high.searchParams.get('bbox')).toBe(bboxParam(13, bounds))
  })

  it('is stable for an unchanged view, so the cache can serve it', () => {
    expect(hexesURL(13, bounds)).toBe(hexesURL(13, bounds))
  })
})

describe('hexFeatures', () => {
  const bands = [{ upper: 20, colour: '#0a0' }, { upper: 40, colour: '#fa0' }, { upper: null, colour: '#a00' }]
  const body = {
    resolution_km: 1,
    hexes: [
      { lon: 23.32, lat: 42.7, n: 3, values: { P1: 12 } },
      { lon: 23.34, lat: 42.7, n: 1, values: { P1: 55 } },
      { lon: 23.36, lat: 42.7, n: 2, values: {} },
    ],
  }

  it('makes one closed polygon per bin, carrying its count and value', () => {
    const f = hexFeatures(body, 'P1', bands, '#ccc', colourFor)
    expect(f).toHaveLength(3)
    expect(f[0].geometry.type).toBe('Polygon')
    expect(f[0].geometry.coordinates[0]).toHaveLength(7)
    expect(f[0].properties).toMatchObject({ n: 3, value: 12 })
    expect(f[0].properties.colour).not.toBe(f[1].properties.colour)
  })

  // A bin with no reading for this metric is not a bin with no sensors. The
  // count is still true, so the cell stays and only its colour says "no value".
  it('keeps a bin that has no value for the current metric', () => {
    const f = hexFeatures(body, 'P1', bands, '#ccc', colourFor)
    expect(f[2].properties).toMatchObject({ n: 2, value: null, colour: '#ccc' })
  })

  // The size the server SERVED, not the size the client asked for. The server
  // snaps onto its own tier list, so those two differ on most requests.
  it('draws at the resolution the response reports', () => {
    const coarse = hexFeatures({ ...body, resolution_km: 5 }, 'P1', bands, '#ccc', colourFor)
    const fine = hexFeatures({ ...body, resolution_km: 0.5 }, 'P1', bands, '#ccc', colourFor)
    expect(widthOf(coarse[0].geometry.coordinates[0].slice(0, 6)))
      .toBeCloseTo(10 * widthOf(fine[0].geometry.coordinates[0].slice(0, 6)), 6)
  })

  it('draws nothing rather than guessing when the response has no resolution', () => {
    for (const b of [null, undefined, {}, { hexes: [] }, { resolution_km: 0, hexes: body.hexes }]) {
      expect(hexFeatures(b, 'P1', bands, '#ccc', colourFor)).toEqual([])
    }
  })
})
