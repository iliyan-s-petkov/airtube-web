import { describe, it, expect, vi } from 'vitest'
import {
  mapStyle, overlayLayers, addBasemapOverlay, registerProtocols, glyphsURL, installErrorHandler,
} from '../mapstyle.js'

// The style is now raster-only. The self-hosted vector archive is a BULGARIA
// extract whose opaque land/water fills painted a rectangle over the world
// raster beneath it — the Danube stopped at Silistra, the ground changed
// colour at the border, and the Black Sea went unnamed.
describe('mapStyle', () => {
  const cfg = { basemap: 'https://tiles.airbg.org/style.json', emptyBasemapColour: '#eef2f5' }

  it('draws the world raster and no vector layer at all', () => {
    const style = mapStyle(cfg)
    const raster = style.layers.filter((l) => l.type === 'raster')

    expect(raster).toHaveLength(1)
    expect(style.sources[raster[0].source].tiles[0]).toContain('tile.openstreetmap.org')
    expect(style.sources[raster[0].source].attribution).toContain('OpenStreetMap')
    for (const l of style.layers) expect(l.type).not.toBe('fill')
  })

  it('paints the backing colour from cfg.emptyBasemapColour, not another field', () => {
    const style = mapStyle({ ...cfg, noDataColour: '#9ca3af' })
    const bg = style.layers.find((l) => l.type === 'background')
    expect(bg.paint['background-color']).toBe('#eef2f5')
  })

  it('puts the raster over the backing colour, which would otherwise cover it', () => {
    const ids = mapStyle(cfg).layers.map((l) => l.type)
    expect(ids.indexOf('background')).toBeLessThan(ids.indexOf('raster'))
  })

  // A raster-only style has no glyphs of its own. Without one MapLibre draws
  // no symbol layer at all: no marker labels, no cell values, no wind arrows.
  it('keeps a glyphs endpoint so the symbol layers still have letters', () => {
    expect(mapStyle(cfg).glyphs).toBe('https://tiles.airbg.org/glyphs/{fontstack}/{range}.pbf')
  })

  it('omits glyphs rather than inventing one when no basemap is configured', () => {
    expect(mapStyle({ basemap: '', emptyBasemapColour: '#eef2f5' }).glyphs).toBeUndefined()
  })
})

// The vector archive is a BULGARIA extract. Its background and fill layers are
// opaque polygons clipped to the extract's rectangle, so over the world raster
// they painted a box across it — the Danube ending at Silistra, the ground
// changing colour at the border, the Black Sea unnamed. Every one of those is a
// FILLED layer; the traced ones draw only what they trace and let the raster
// through, which is how the POI categories come back without the box.
describe('overlayLayers', () => {
  const style = {
    sources: { basemap: { type: 'vector' }, unused: { type: 'vector' } },
    layers: [
      { id: 'bg', type: 'background', source: undefined },
      { id: 'water', type: 'fill', source: 'basemap' },
      { id: 'roads', type: 'line', source: 'basemap' },
      { id: 'poi-shop-name', type: 'symbol', source: 'basemap' },
      { id: 'poi-shop', type: 'circle', source: 'basemap' },
    ],
  }

  it('drops every filled layer and keeps every traced one', () => {
    expect(overlayLayers(style).layers.map((l) => l.id))
      .toEqual(['roads', 'poi-shop-name', 'poi-shop'])
  })

  it('keeps the sources the surviving layers need, and no others', () => {
    expect(Object.keys(overlayLayers(style).sources)).toEqual(['basemap'])
  })

  it('has nothing to say about a style it was handed nothing of', () => {
    expect(overlayLayers(undefined)).toEqual({ sources: {}, layers: [] })
  })
})

describe('addBasemapOverlay', () => {
  const fakeMap = () => {
    const calls = { sources: [], layers: [] }
    return {
      calls,
      getSource: () => undefined,
      getLayer: (id) => (id === 'airbg-hex-fill' ? { id } : undefined),
      addSource: (id, s) => calls.sources.push([id, s]),
      addLayer: (l, before) => calls.layers.push([l.id, before]),
    }
  }
  const style = {
    sources: { basemap: { type: 'vector' } },
    layers: [{ id: 'water', type: 'fill', source: 'basemap' }, { id: 'roads', type: 'line', source: 'basemap' }],
  }

  // Under the readings, never over: a POI label drawn on top of a value is the
  // ground obscuring the thing the page exists to show.
  it('slots the traced layers beneath the grid', async () => {
    const map = fakeMap()
    await addBasemapOverlay(map, 'https://tiles.airbg.org/style.json', 'airbg-hex-fill', async () => style)

    expect(map.calls.sources).toEqual([['basemap', { type: 'vector' }]])
    expect(map.calls.layers).toEqual([['roads', 'airbg-hex-fill']])
  })

  it('adds them on top when the grid is not there to sit under', async () => {
    const map = { ...fakeMap(), getLayer: () => undefined }
    map.calls = { sources: [], layers: [] }
    map.addSource = (id, s) => map.calls.sources.push([id, s])
    map.addLayer = (l, before) => map.calls.layers.push([l.id, before])
    await addBasemapOverlay(map, 'https://x/style.json', 'airbg-hex-fill', async () => style)
    expect(map.calls.layers).toEqual([['roads', undefined]])
  })

  // The ground is optional detail; the map is not. An unreachable archive must
  // leave the raster and every reading standing.
  it('leaves the raster alone when the style cannot be fetched', async () => {
    const map = fakeMap()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    await expect(addBasemapOverlay(map, 'https://x/style.json', 'airbg-hex-fill', async () => {
      throw new Error('502')
    })).resolves.toBeUndefined()
    expect(map.calls.layers).toEqual([])
    warn.mockRestore()
  })

  it('does nothing at all when no basemap is configured', async () => {
    const map = fakeMap()
    await addBasemapOverlay(map, '', 'airbg-hex-fill', async () => style)
    expect(map.calls.sources).toEqual([])
  })
})

// The archive is referenced as pmtiles://, which MapLibre cannot read until the
// protocol is registered — and addProtocol is global module state, so a second
// registration for the same scheme silently replaces the first.
describe('registerProtocols', () => {
  it('registers pmtiles exactly once across repeated calls', () => {
    const seen = []
    const add = (scheme, fn) => seen.push([scheme, typeof fn])
    registerProtocols(add)
    registerProtocols(add)
    expect(seen).toEqual([['pmtiles', 'function']])
  })
})

describe('glyphsURL', () => {
  it('replaces the style file with the font endpoint, keeping the path', () => {
    expect(glyphsURL('https://tiles.airbg.org/maps/style.json'))
      .toBe('https://tiles.airbg.org/maps/glyphs/{fontstack}/{range}.pbf')
  })

  // new URL() percent-encodes the braces into %7Bfontstack%7D, which MapLibre
  // then requests literally and gets a 404 for. The template must survive.
  it('leaves the braces MapLibre substitutes into unencoded', () => {
    expect(glyphsURL('https://tiles.airbg.org/style.json')).toContain('{fontstack}/{range}')
    expect(glyphsURL('https://tiles.airbg.org/style.json')).not.toContain('%7B')
  })

  it('has nothing to derive from when no basemap is configured', () => {
    expect(glyphsURL('')).toBeNull()
  })
})

// installErrorHandler is what keeps a style-load failure (missing archive,
// unreachable tiles host, a CSP that blocks the fetch) from taking the sensor
// markers down with it: it must log and must never let the error propagate
// out of the 'error' callback. Driven with a fake map exposing only `.on` —
// installErrorHandler needs nothing heavier, unlike the 'load' handler which
// genuinely needs a real MapLibre instance for addSource/addLayer.
describe('installErrorHandler', () => {
  function fakeMap() {
    let handler
    return {
      on: (event, cb) => { if (event === 'error') handler = cb },
      trigger: (e) => handler(e),
    }
  }

  it('logs a warning when the style fails to load', () => {
    const warnings = []
    const map = fakeMap()
    installErrorHandler(map, (...args) => warnings.push(args))

    map.trigger({ error: { message: 'style fetch failed' } })

    expect(warnings).toHaveLength(1)
  })

  it('logs once, not once per error event', () => {
    const warnings = []
    const map = fakeMap()
    installErrorHandler(map, (...args) => warnings.push(args))

    map.trigger({ error: { message: 'first' } })
    map.trigger({ error: { message: 'second' } })

    expect(warnings).toHaveLength(1)
  })

  it('does not let the error propagate out of the callback', () => {
    const map = fakeMap()
    installErrorHandler(map, () => {})

    expect(() => map.trigger({ error: { message: 'boom' } })).not.toThrow()
  })
})
