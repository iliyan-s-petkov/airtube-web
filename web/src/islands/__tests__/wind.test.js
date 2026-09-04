import { describe, it, expect } from 'vitest'
import { arrowBearing, windFeatures, windLabel, WIND_LAYER_ID } from '../wind.js'
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
