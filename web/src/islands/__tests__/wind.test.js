import { describe, it, expect } from 'vitest'
import {
  arrowBearing, arrowImage, arrowLayout, arrowPaint, windFeatures, windLabel,
  ARROW_IMAGE_ID, ARROW_PX, WIND_LAYER_ID,
} from '../wind.js'
import { toggleWind } from '../map.js'

describe('arrowBearing', () => {
  // The API reports where the wind comes FROM. A northerly (0°) blows
  // southward, so its arrow points south (180°). Getting this backwards
  // produces a map that looks entirely plausible and is entirely wrong.
  it('points the arrow where the air is going, not where it came from', () => {
    expect(arrowBearing(0)).toBe(180)
    expect(arrowBearing(90)).toBe(270)
    expect(arrowBearing(270)).toBe(90)
  })

  it('wraps past 360 rather than returning a bearing no renderer expects', () => {
    expect(arrowBearing(181)).toBe(1)
    expect(arrowBearing(359)).toBe(179)
  })
})

// The arrow is a raster this file draws, not a character.
//
// It was '→' rendered through the style's glyph source, and the layer switched
// on and drew NOTHING: the served font pack carries U+2100..U+2189 in the
// 8448-8703 range and stops there, so the whole Arrows block is absent and
// MapLibre silently omits a glyph it cannot find. A missing glyph reports
// itself nowhere the map can see — the layer is visible, the source has its
// features, the request for the range even answers 200. That is why the pixels
// are asserted here rather than the layer state.
describe('arrowImage', () => {
  const cfg = { labelColour: '#000000', markerStrokeColour: '#ffffff' }

  it('is a square RGBA raster of the declared size', () => {
    const img = arrowImage(cfg)
    expect(img.width).toBe(ARROW_PX)
    expect(img.height).toBe(ARROW_PX)
    expect(img.data).toHaveLength(ARROW_PX * ARROW_PX * 4)
  })

  const at = (img, x, y) => {
    const i = (y * img.width + x) * 4
    return [img.data[i], img.data[i + 1], img.data[i + 2], img.data[i + 3]]
  }

  it('draws in the label colour, haloed in the stroke colour', () => {
    const img = arrowImage(cfg)
    const mid = ARROW_PX >> 1
    // On the shaft: the arrow's own colour, fully opaque.
    const shaft = at(img, mid, mid)
    expect(shaft[3]).toBe(255)
    expect(shaft.slice(0, 3)).toEqual([0, 0, 0])
    // Just off it: the halo, which is what keeps a dark arrow legible over a
    // dark cell — the same pairing the marker labels use.
    const halo = at(img, mid, mid - Math.round(ARROW_PX * 0.09))
    expect(halo[3]).toBeGreaterThan(0)
    expect(halo[0]).toBeGreaterThan(200)
  })

  // The glyph pointed east and the layout subtracts 90 from the bearing to
  // match. A raster drawn pointing any other way would rotate every arrow on
  // the map by a constant, which looks like weather rather than like a bug.
  it('points east, which is what the layout rotation assumes', () => {
    const img = arrowImage(cfg)
    const mid = ARROW_PX >> 1
    const opaque = (x) => at(img, x, mid)[3] > 0
    // The head reaches further east than the tail reaches west.
    let east = 0; let west = 0
    for (let x = mid; x < ARROW_PX; x++) if (opaque(x)) east = x - mid
    for (let x = mid; x >= 0; x--) if (opaque(x)) west = mid - x
    expect(east).toBeGreaterThan(0)
    // The barbs are the widest part and they sit on the head, east of centre.
    const spread = (x) => {
      let n = 0
      for (let y = 0; y < ARROW_PX; y++) if (at(img, x, y)[3] > 0) n++
      return n
    }
    expect(spread(mid + Math.round(ARROW_PX * 0.2))).toBeGreaterThan(spread(mid - Math.round(ARROW_PX * 0.2)))
    expect(west).toBeGreaterThan(0)
  })

  it('leaves the corners transparent rather than drawing a filled tile', () => {
    const img = arrowImage(cfg)
    expect(at(img, 0, 0)[3]).toBe(0)
    expect(at(img, ARROW_PX - 1, ARROW_PX - 1)[3]).toBe(0)
  })
})

describe('arrowLayout', () => {
  it('draws the image this file registers, not a font glyph', () => {
    const layout = arrowLayout()
    expect(layout['icon-image']).toBe(ARROW_IMAGE_ID)
    expect(layout['text-field']).toBeUndefined()
    expect(layout['text-font']).toBeUndefined()
  })

  it('rotates with the map, by the bearing the feature carries', () => {
    const layout = arrowLayout()
    expect(layout['icon-rotate']).toEqual(['-', ['get', 'bearing'], 90])
    expect(layout['icon-rotation-alignment']).toBe('map')
  })

  // Overlap is deliberate: the model's grid is regular, so a placement rule
  // that dropped colliding arrows would thin the field in exactly the places
  // it is densest and make the wind look patchy.
  it('lets the arrows overlap, so the field stays regular', () => {
    expect(arrowLayout()['icon-allow-overlap']).toBe(true)
  })

  it('grows with the speed, between bounds', () => {
    const size = arrowLayout()['icon-size']
    expect(size[0]).toBe('interpolate')
    expect(size[2]).toEqual(['get', 'speed'])
    const stops = size.slice(3)
    expect(stops[1]).toBeGreaterThan(0)
    expect(stops[3]).toBeGreaterThan(stops[1])
  })
})

describe('arrowPaint', () => {
  it('does not fade the arrow to where the halo cannot save it', () => {
    expect(arrowPaint()['icon-opacity']).toBeGreaterThanOrEqual(0.9)
  })
})

describe('windFeatures', () => {
  const body = {
    forecast: true,
    model: 'ecmwf_ifs025',
    model_resolution_deg: 0.25,
    valid_at: '2026-09-05T14:00:00Z',
    vectors: [{ lon: 23.3, lat: 42.7, speed_ms: 3.5, direction_deg: 270 }],
  }

  it('carries the reversed bearing and the speed', () => {
    const [f] = windFeatures(body)
    expect(f.geometry.coordinates).toEqual([23.3, 42.7])
    expect(f.properties.bearing).toBe(90)
    expect(f.properties.speed).toBe(3.5)
  })

  // The server stamps forecast: true on this payload and on nothing else. A
  // client that drew whatever it was handed could render an unrelated body as
  // wind, which is the one mistake this layer must not make.
  it('refuses a body that does not declare itself a forecast', () => {
    expect(windFeatures({ ...body, forecast: undefined })).toEqual([])
    expect(windFeatures({ ...body, forecast: false })).toEqual([])
  })

  it('is empty for a missing or malformed body rather than throwing', () => {
    expect(windFeatures(null)).toEqual([])
    expect(windFeatures({ forecast: true })).toEqual([])
  })
})

describe('windLabel', () => {
  const t = { windAttribution: 'Forecast · {model} ({resolution}°) · valid {time}' }
  const body = {
    forecast: true,
    model: 'ecmwf_ifs025',
    model_resolution_deg: 0.25,
    valid_at: '2026-09-05T14:00:00Z',
    vectors: [],
  }

  // The model's grid is coarser than our hexes, so neighbouring arrows repeat.
  // Naming the resolution is what tells a reader that is the model's grid and
  // not a suspiciously uniform wind.
  it('names the model, its grid, and the forecast hour', () => {
    expect(windLabel(body, t)).toBe('Forecast · ecmwf_ifs025 (0.25°) · valid 2026-09-05 14:00 UTC')
  })

  it('is empty with no body, so nothing claims a forecast that is not there', () => {
    expect(windLabel(null, t)).toBe('')
  })

  // A visitor who presses the toggle gets arrows and a model name, neither of
  // which says what the arrows are for. The note does, and it leads.
  it('leads with the note that says what the arrows mean', () => {
    const withNote = { ...t, windNote: 'Arrows show where the wind blows.' }
    const label = windLabel(body, withNote)
    expect(label.startsWith('Arrows show where the wind blows. ')).toBe(true)
    expect(label).toContain('ecmwf_ifs025')
  })

  it('still names the model when no note is translated', () => {
    expect(windLabel(body, { ...t, windNote: '' })).toContain('ecmwf_ifs025')
  })
})

describe('toggleWind', () => {
  const body = {
    forecast: true,
    model: 'ecmwf_ifs025',
    model_resolution_deg: 0.25,
    valid_at: '2026-09-05T14:00:00Z',
    vectors: [{ lon: 23.3, lat: 42.7, speed_ms: 3.5, direction_deg: 270 }],
  }
  const cfg = { t: { windAttribution: '{model} {resolution} {time}' } }

  function fakes() {
    const source = { data: null, setData(d) { this.data = d } }
    const map = {
      layout: {},
      getSource: () => source,
      setLayoutProperty(_id, k, v) { this.layout[k] = v },
    }
    const chrome = { on: null, text: null, showWind(on, text) { this.on = on; this.text = text } }
    return { map, chrome, source }
  }

  it('shows the arrows and the disclosure in the same act', async () => {
    const { map, chrome, source } = fakes()
    const state = { on: false, body: null, loading: false }

    await toggleWind(map, cfg, chrome, state, async () => body)

    expect(map.layout.visibility).toBe('visible')
    expect(source.data.features).toHaveLength(1)
    // The disclosure is not optional chrome: it is the condition on which this
    // layer is allowed over a map of measurements at all.
    expect(chrome.on).toBe(true)
    expect(chrome.text).toBe('ecmwf_ifs025 0.25 2026-09-05 14:00 UTC')
  })

  // /api/v1/wind answers 503 whenever no forecast covers the current hour. The
  // failure must leave the layer OFF — a visible layer with no disclosure, or
  // a disclosure over no arrows, are both worse than nothing here.
  it('leaves the layer off when the forecast is unavailable', async () => {
    const { map, chrome } = fakes()
    const state = { on: false, body: null, loading: false }

    await toggleWind(map, cfg, chrome, state, async () => { throw new Error('503') })

    expect(state.on).toBe(false)
    expect(map.layout.visibility).toBeUndefined()
    expect(chrome.on).toBe(false)
  })

  it('hides both halves again, and does not refetch to do it', async () => {
    const { map, chrome } = fakes()
    const state = { on: true, body, loading: false }
    let fetches = 0

    await toggleWind(map, cfg, chrome, state, async () => { fetches++; return body })

    expect(map.layout.visibility).toBe('none')
    expect(chrome.on).toBe(false)
    expect(chrome.text).toBe('')
    expect(fetches).toBe(0)
  })

  it('serves a second switch-on from the cached body, not a second request', async () => {
    const { map, chrome } = fakes()
    const state = { on: false, body: null, loading: false }
    let fetches = 0
    const fetchJSON = async () => { fetches++; return body }

    await toggleWind(map, cfg, chrome, state, fetchJSON)
    await toggleWind(map, cfg, chrome, state, fetchJSON)
    await toggleWind(map, cfg, chrome, state, fetchJSON)

    expect(fetches).toBe(1)
    expect(map.layout.visibility).toBe('visible')
  })

  it('names the layer it toggles, so a renamed layer cannot silently no-op', () => {
    expect(WIND_LAYER_ID).toBe('airbg-wind-arrows')
  })
})
