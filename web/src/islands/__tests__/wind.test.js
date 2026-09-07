import { describe, it, expect } from 'vitest'
import {
  arrowBearing, arrowImage, arrowLayout, arrowPaint, windFeatures, windField, windLabel, windIsStale,
  ARROW_IMAGE_ID, ARROW_PX, WIND_LAYER_ID, WIND_FIELD_MAX,
} from '../wind.js'
import { setWind, refreshWind } from '../map.js'

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

// The served field is a fixed national lattice at HexResolutionKM. Zoom past a
// city and the viewport holds one vector, then none, and a layer the reader
// switched on empties itself — which is indistinguishable from the forecast
// having failed. The arrows are resampled onto a viewport-sized lattice so the
// field stays a field at every zoom.
//
// This adds no information and is not allowed to imply any: every arrow is the
// nearest served vector, repeated, and the disclosure already names the model's
// own grid as the thing the reader should judge the detail by.
describe('windField', () => {
  const body = {
    forecast: true,
    vectors: [
      { lon: 23.0, lat: 42.6, speed_ms: 3, direction_deg: 0 },
      { lon: 23.4, lat: 42.6, speed_ms: 9, direction_deg: 90 },
    ],
  }
  // A tight viewport around the first vector, at a zoom where the served field
  // would put one arrow on the screen.
  const view = { bounds: [22.9, 42.55, 23.1, 42.65], zoom: 13 }

  it('draws many arrows where the served field would draw one', () => {
    const served = windFeatures(body).filter((f) => {
      const [lon, lat] = f.geometry.coordinates
      return lon >= 22.9 && lon <= 23.1 && lat >= 42.55 && lat <= 42.65
    })
    expect(served).toHaveLength(1)
    expect(windField(body, view).length).toBeGreaterThan(served.length * 5)
  })

  it('repeats the nearest served vector rather than inventing a value', () => {
    const f = windField(body, view)
    // Every point in this viewport is nearest the 3 m/s vector, so every arrow
    // carries its speed and its bearing. An interpolated field would show
    // values between 3 and 9 that no forecast ever reported.
    expect([...new Set(f.map((x) => x.properties.speed))]).toEqual([3])
    expect([...new Set(f.map((x) => x.properties.bearing))]).toEqual([180])
  })

  it('stays inside the viewport it was given', () => {
    for (const f of windField(body, view)) {
      const [lon, lat] = f.geometry.coordinates
      expect(lon).toBeGreaterThanOrEqual(22.9)
      expect(lon).toBeLessThanOrEqual(23.1)
      expect(lat).toBeGreaterThanOrEqual(42.55)
      expect(lat).toBeLessThanOrEqual(42.65)
    }
  })

  // Zoomed out, the served lattice is already denser than the screen: resampling
  // there would draw arrows on top of each other and cost a scan per point for
  // nothing.
  it('serves the whole field untouched when zoomed out', () => {
    const wide = { bounds: [22, 41, 28, 44], zoom: 7 }
    expect(windField(body, wide)).toEqual(windFeatures(body))
  })

  // The model does not cover the sea, and the served field stops at the
  // country. A lattice point with no vector near it must draw nothing rather
  // than borrow a reading from a hundred kilometres away.
  it('draws nothing where no served vector is near', () => {
    const offshore = { bounds: [28.5, 43.0, 28.7, 43.1], zoom: 13 }
    expect(windField(body, offshore)).toEqual([])
  })

  it('is empty for a body that does not declare itself a forecast', () => {
    expect(windField({ ...body, forecast: false }, view)).toEqual([])
    expect(windField(null, view)).toEqual([])
  })

  // A lattice sized purely by zoom is unbounded: a wide viewport at a high zoom
  // is tens of thousands of points, each costing a scan of the served field.
  it('never builds more arrows than it can draw usefully', () => {
    // Blanketed with vectors on purpose: a sparse field is capped by the
    // distance cutoff instead, which would let an uncapped lattice pass.
    const dense = { forecast: true, vectors: [] }
    for (let lon = 22; lon <= 28; lon += 0.1) {
      for (let lat = 41; lat <= 44; lat += 0.1) {
        dense.vectors.push({ lon, lat, speed_ms: 4, direction_deg: 45 })
      }
    }
    const huge = { bounds: [22, 41, 28, 44], zoom: 15 }
    expect(windField(dense, huge).length).toBeLessThanOrEqual(WIND_FIELD_MAX)
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

describe('setWind', () => {
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

  const bounds = (w, s, e, n) => ({
    getWest: () => w, getSouth: () => s, getEast: () => e, getNorth: () => n,
  })

  // A map that cannot report its viewport still gets arrows: the fallback is the
  // served field, which is what the layer drew before it was resampled at all.
  it('falls back to the served field when the map reports no bounds', async () => {
    const { map, chrome, source } = fakes()
    const state = { on: false, body: null, loading: false }

    await setWind(map, cfg, chrome, state, true, async () => body)

    expect(source.data.features).toEqual(windFeatures(body))
  })

  it('shows the arrows and the disclosure in the same act', async () => {
    const { map, chrome, source } = fakes()
    const state = { on: false, body: null, loading: false }

    await setWind(map, cfg, chrome, state, true, async () => body)

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

    const reached = await setWind(map, cfg, chrome, state, true, async () => { throw new Error('503') })

    expect(state.on).toBe(false)
    expect(map.layout.visibility).toBeUndefined()
    expect(chrome.on).toBe(false)
    // Reported back, not just left off: the checkbox in the layers menu takes
    // this answer, and a ticked box over a map with no arrows would be the menu
    // claiming a layer that is not there.
    expect(reached).toBe(false)
  })

  // The bug this fixes: at street zoom the served lattice puts one arrow in the
  // viewport, then none, and a layer the reader deliberately switched on goes
  // blank. setWind has to hand the source the resampled field, not the served
  // one — otherwise the fix exists in windField and never reaches the map.
  it('draws the field the viewport asked for, not the one the server sent', async () => {
    const { map, chrome, source } = fakes()
    map.getZoom = () => 13
    map.getBounds = () => bounds(23.2, 42.65, 23.4, 42.75)
    const state = { on: false, body: null, loading: false }

    await setWind(map, cfg, chrome, state, true, async () => body)

    expect(source.data.features.length).toBeGreaterThan(body.vectors.length)
  })

  it('hides both halves again, and does not refetch to do it', async () => {
    const { map, chrome } = fakes()
    const state = { on: true, body, loading: false }
    let fetches = 0

    const reached = await setWind(map, cfg, chrome, state, false, async () => { fetches++; return body })

    expect(map.layout.visibility).toBe('none')
    expect(chrome.on).toBe(false)
    expect(chrome.text).toBe('')
    expect(fetches).toBe(0)
    expect(reached).toBe(false)
  })

  // A checkbox says what it wants, not "the other thing from last time". Asking
  // for a state already held must be a no-op, where a toggle would flip it.
  it('is idempotent: asking for on twice leaves it on', async () => {
    const { map, chrome } = fakes()
    const state = { on: false, body: null, loading: false }
    let fetches = 0
    const fetchJSON = async () => { fetches++; return body }

    await setWind(map, cfg, chrome, state, true, fetchJSON)
    await setWind(map, cfg, chrome, state, true, fetchJSON)
    await setWind(map, cfg, chrome, state, true, fetchJSON)

    expect(fetches).toBe(1)
    expect(map.layout.visibility).toBe('visible')
    expect(state.on).toBe(true)
  })

  it('is idempotent the other way: asking for off twice leaves it off', async () => {
    const { map, chrome } = fakes()
    const state = { on: false, body: null, loading: false }
    const fetchJSON = async () => body

    await setWind(map, cfg, chrome, state, true, fetchJSON)
    await setWind(map, cfg, chrome, state, false, fetchJSON)
    await setWind(map, cfg, chrome, state, false, fetchJSON)

    expect(map.layout.visibility).toBe('none')
    expect(state.on).toBe(false)
    expect(chrome.on).toBe(false)
  })

  it('names the layer it toggles, so a renamed layer cannot silently no-op', () => {
    expect(WIND_LAYER_ID).toBe('airbg-wind-arrows')
  })
})

describe('windIsStale', () => {
  const body = { valid_at: '2026-09-05T14:00:00Z' }

  // The forecast row is the one for the hour containing now, so within that
  // hour a refetch returns the same national grid byte for byte.
  it('is fresh anywhere inside the hour it is valid for', () => {
    expect(windIsStale(body, new Date('2026-09-05T14:00:00Z'))).toBe(false)
    expect(windIsStale(body, new Date('2026-09-05T14:59:59Z'))).toBe(false)
  })

  it('is stale one second into the next hour', () => {
    expect(windIsStale(body, new Date('2026-09-05T15:00:00Z'))).toBe(true)
  })

  // A tab left open overnight: the same body, many hours old, still stale.
  it('stays stale however far past the hour it gets', () => {
    expect(windIsStale(body, new Date('2026-09-06T09:00:00Z'))).toBe(true)
  })

  // Nothing held is not staleness: dropping a null body would make every
  // refresh refetch a layer the reader never switched on.
  it('holds nothing against a body that is not there', () => {
    expect(windIsStale(null, new Date('2026-09-06T09:00:00Z'))).toBe(false)
    expect(windIsStale({}, new Date('2026-09-06T09:00:00Z'))).toBe(false)
    expect(windIsStale({ valid_at: 'not a time' }, new Date())).toBe(false)
  })
})

describe('refreshWind', () => {
  const body = (validAt) => ({
    forecast: true,
    model: 'ecmwf_ifs025',
    model_resolution_deg: 0.25,
    valid_at: validAt,
    vectors: [{ lon: 23.3, lat: 42.7, speed_ms: 3.5, direction_deg: 270 }],
  })
  const cfg = { t: { windAttribution: '{model} {resolution} {time}' } }

  function fakes() {
    const source = { data: null, setData(d) { this.data = d } }
    const map = {
      layout: {},
      getSource: () => source,
      setLayoutProperty(_id, k, v) { this.layout[k] = v },
    }
    const chrome = { on: null, text: null, showWind(on, text) { this.on = on; this.text = text } }
    return { map, chrome }
  }

  const HELD = '2026-09-05T14:00:00Z'
  const SAME_HOUR = new Date('2026-09-05T14:40:00Z')
  const NEXT_HOUR = new Date('2026-09-05T15:00:00Z')

  it('does not refetch inside the hour the held forecast is valid for', async () => {
    const { map, chrome } = fakes()
    const state = { on: true, body: body(HELD), loading: false }
    let fetches = 0

    const did = await refreshWind(map, cfg, chrome, state, SAME_HOUR, async () => { fetches++; return body(HELD) })

    expect(did).toBe(false)
    expect(fetches).toBe(0)
    expect(state.body.valid_at).toBe(HELD)
  })

  // The bug: a tab open past the hour boundary showed the previous hour's wind
  // for as long as it stayed open, and the refresh button did not touch it.
  it('refetches and repaints once the hour has turned', async () => {
    const { map, chrome } = fakes()
    const state = { on: true, body: body(HELD), loading: false }
    let fetches = 0

    const did = await refreshWind(map, cfg, chrome, state, NEXT_HOUR, async () => {
      fetches++
      return body('2026-09-05T15:00:00Z')
    })

    expect(did).toBe(true)
    expect(fetches).toBe(1)
    expect(state.body.valid_at).toBe('2026-09-05T15:00:00Z')
    expect(map.layout.visibility).toBe('visible')
    // The disclosure names the hour on the map, so it has to move with it.
    expect(chrome.text).toContain('2026-09-05 15:00 UTC')
  })

  // Layer off: the stale body still goes, but nothing is fetched for a layer
  // nobody is looking at. The next toggle pays for the current hour.
  it('drops the stale body without fetching when the layer is off', async () => {
    const { map, chrome } = fakes()
    const state = { on: false, body: body(HELD), loading: false }
    let fetches = 0

    const did = await refreshWind(map, cfg, chrome, state, NEXT_HOUR, async () => { fetches++; return body(HELD) })

    expect(did).toBe(true)
    expect(fetches).toBe(0)
    expect(state.body).toBe(null)
    expect(map.layout.visibility).toBeUndefined()
  })

  it('does nothing at all when no forecast is held', async () => {
    const { map, chrome } = fakes()
    const state = { on: false, body: null, loading: false }
    let fetches = 0

    const did = await refreshWind(map, cfg, chrome, state, NEXT_HOUR, async () => { fetches++; return body(HELD) })

    expect(did).toBe(false)
    expect(fetches).toBe(0)
  })
})
