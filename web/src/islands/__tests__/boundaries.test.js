import { describe, it, expect } from 'vitest'
import { setBoundaries, boundaryChoice, highlightBoundary } from '../map.js'
import {
  BOUNDARY_SOURCE_ID, BOUNDARY_SELECTED_LAYER_ID, BOUNDARY_LAYER_IDS, selectedFilter,
} from '../../lib/boundaries.js'

const body = {
  type: 'FeatureCollection',
  features: [{
    type: 'Feature',
    properties: { slug: 'vidin-oblast' },
    geometry: { type: 'Polygon', coordinates: [[[22.3, 43.5], [23.1, 43.5], [23.1, 44.2], [22.3, 43.5]]] },
  }],
}

function fakes() {
  const source = { data: null, setData(d) { this.data = d } }
  const map = {
    visibility: {},
    filters: {},
    getSource: () => source,
    getLayer: (id) => BOUNDARY_LAYER_IDS.includes(id),
    setLayoutProperty(id, k, v) { this.visibility[id] = { [k]: v } },
    setFilter(id, f) { this.filters[id] = f },
  }
  return { map, source }
}

describe('setBoundaries', () => {
  it('shows every outline layer once the collection is in hand', async () => {
    const { map, source } = fakes()
    const state = { slug: null }
    const bstate = { on: false, body: null, loading: false }

    const reached = await setBoundaries(map, state, bstate, true, async () => body)

    expect(reached).toBe(true)
    expect(bstate.on).toBe(true)
    expect(source.data).toBe(body)
    for (const id of BOUNDARY_LAYER_IDS) {
      expect(map.visibility[id]).toEqual({ visibility: 'visible' })
    }
  })

  // The hit fill has to go down with the outlines. Left visible, it would keep
  // answering clicks over a map that is no longer drawing any borders.
  it('hides every layer when switched off, without fetching', async () => {
    const { map } = fakes()
    let fetches = 0
    const bstate = { on: true, body: null, loading: false }

    const reached = await setBoundaries(map, { slug: null }, bstate, false, async () => { fetches++; return body })

    expect(reached).toBe(false)
    expect(fetches).toBe(0)
    expect(bstate.on).toBe(false)
    for (const id of BOUNDARY_LAYER_IDS) {
      expect(map.visibility[id]).toEqual({ visibility: 'none' })
    }
  })

  // A ticked box over a map with no outlines on it is the menu reporting a
  // layer that is not there.
  it('reports the state it reached when the fetch fails', async () => {
    const { map } = fakes()
    const bstate = { on: false, body: null, loading: false }

    const reached = await setBoundaries(map, { slug: null }, bstate, true, async () => { throw new Error('503') })

    expect(reached).toBe(false)
    expect(bstate.on).toBe(false)
    expect(map.visibility[BOUNDARY_SELECTED_LAYER_ID]).toEqual({ visibility: 'none' })
  })

  // The borders do not move. Fetching them again on every toggle would be a
  // request per click.
  it('fetches once and reuses the collection', async () => {
    const { map } = fakes()
    let fetches = 0
    const state = { slug: null }
    const bstate = { on: false, body: null, loading: false }
    const fetchJSON = async () => { fetches++; return body }

    await setBoundaries(map, state, bstate, true, fetchJSON)
    await setBoundaries(map, state, bstate, false, fetchJSON)
    await setBoundaries(map, state, bstate, true, fetchJSON)

    expect(fetches).toBe(1)
  })

  // On /area/{slug} the province is already chosen: the outlines have to arrive
  // with that one picked out, or the reader is shown 28 identical borders on a
  // page about one of them.
  it('arrives with the page s own province already highlighted', async () => {
    const { map } = fakes()
    const bstate = { on: false, body: null, loading: false }

    await setBoundaries(map, { slug: 'vidin-oblast' }, bstate, true, async () => body)

    expect(map.filters[BOUNDARY_SELECTED_LAYER_ID]).toEqual(selectedFilter('vidin-oblast'))
  })

  // The source is the collection itself, not a rebuild of it: the server's
  // properties are what the filter and the click handler read.
  it('hands the source the served collection', async () => {
    const { map, source } = fakes()
    await setBoundaries(map, { slug: null }, { on: false, body: null, loading: false }, true, async () => body)
    expect(source.data.features[0].properties.slug).toBe('vidin-oblast')
    expect(map.getSource(BOUNDARY_SOURCE_ID).data).toBe(body)
  })

  // Two toggles while the first fetch is still in flight: the second resolution
  // would turn a layer back on that the reader had just switched off.
  it('drops a request made while the first fetch is in flight', async () => {
    const { map } = fakes()
    let fetches = 0
    const state = { slug: null }
    const bstate = { on: false, body: null, loading: false }
    let release
    const fetchJSON = () => { fetches++; return new Promise((r) => { release = () => r(body) }) }

    const first = setBoundaries(map, state, bstate, true, fetchJSON)
    const second = setBoundaries(map, state, bstate, true, fetchJSON)
    release()
    await Promise.all([first, second])

    expect(fetches).toBe(1)
    expect(await second).toBe(false)
  })
})

describe('boundaryChoice', () => {
  const feature = { properties: { slug: 'vidin-oblast' } }

  it('is the province under the pointer', () => {
    expect(boundaryChoice({ slug: null }, feature)).toBe('vidin-oblast')
  })

  // A map already scoped to one area has nothing to drill into, which is the
  // rule cellArea applies to the cells.
  it('selects nothing on a map already scoped to an area', () => {
    expect(boundaryChoice({ slug: 'sofiya-grad-oblast' }, feature)).toBe(null)
  })

  it('selects nothing when the click hit no province', () => {
    expect(boundaryChoice({ slug: null }, undefined)).toBe(null)
    expect(boundaryChoice({ slug: null }, { properties: {} })).toBe(null)
  })
})

describe('highlightBoundary', () => {
  it('writes the filter for the selected province', () => {
    const { map } = fakes()
    highlightBoundary(map, 'vidin-oblast')
    expect(map.filters[BOUNDARY_SELECTED_LAYER_ID]).toEqual(selectedFilter('vidin-oblast'))
  })

  // Called from setBoundaries, which can run before the load handler has added
  // the layers on a slow style.
  it('does nothing on a map that has no such layer yet', () => {
    const map = { getLayer: () => false, setFilter: () => { throw new Error('no layer') } }
    expect(() => highlightBoundary(map, 'vidin-oblast')).not.toThrow()
  })
})
