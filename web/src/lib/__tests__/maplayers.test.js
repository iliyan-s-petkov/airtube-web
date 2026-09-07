// @vitest-environment jsdom
//
// jsdom, because everything here is a real element: the kit classes, the
// disclosure's aria state, the checkbox a reader toggles. The map is a stub —
// installLayers only ever asks it for its style and writes visibility back, so
// it needs a renderer no more than the zoom stack does.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  mountLayers, installLayers, groupsIn, readState, writeState,
  LAYER_ORDER, STORAGE_KEY,
} from '../maplayers.js'

const frame = () => {
  const el = document.createElement('div')
  el.className = 'map map--hero'
  el.id = 'map'
  document.body.append(el)
  return el
}

// A style is a list of layers, and a layer's group is the only thing this
// module reads off one — so a fake style is exactly that list.
const layer = (id, group) => (group ? { id, metadata: { 'airbg:group': group } } : { id })

const fakeMap = (layers) => ({
  getStyle: () => ({ layers }),
  setLayoutProperty: vi.fn(),
})

// A storage stand-in, so no test depends on jsdom's localStorage surviving
// between files or on the order they run in.
const fakeStorage = (seed = null) => {
  let value = seed
  return {
    getItem: vi.fn(() => value),
    setItem: vi.fn((_, v) => { value = v }),
    get value() { return value },
  }
}

const labels = Object.fromEntries(LAYER_ORDER.map((g) => [g, `L:${g}`]))
const options = (ui) => [...ui.fieldset.querySelectorAll('.colmenu__opt')]
const keys = (ui) => options(ui).map((o) => o.querySelector('input').getAttribute('data-layer-key'))

beforeEach(() => { document.body.innerHTML = '' })

describe('mountLayers', () => {
  it('builds the kit disclosure, closed and hidden', () => {
    const el = frame()
    const ui = mountLayers(el, { label: 'Layers' })

    expect(ui.root.parentElement).toBe(el)
    expect(ui.root.className).toBe('colmenu map__layers')
    // Hidden until installLayers finds something to put in it: a menu over
    // layers that do not exist is the dead control this module exists to avoid.
    expect(ui.root.hidden).toBe(true)
    expect(ui.button.className).toBe('btn btn--icon colmenu__btn')
    expect(ui.button.type).toBe('button')
    expect(ui.button.getAttribute('aria-label')).toBe('Layers')
    expect(ui.button.getAttribute('title')).toBe('Layers')
    expect(ui.button.getAttribute('aria-expanded')).toBe('false')
    expect(ui.panel.hidden).toBe(true)
    expect(ui.button.querySelector('svg').getAttribute('aria-hidden')).toBe('true')
  })

  it('points aria-controls at the panel it actually owns', () => {
    const el = frame()
    const ui = mountLayers(el, { label: 'Layers' })
    // Derived from the frame's id, not a constant: an area page and a home page
    // mount the same island, and a fixed id would collide the day two maps
    // share a document.
    expect(ui.panel.id).toBe('map-layers-panel')
    expect(ui.button.getAttribute('aria-controls')).toBe(ui.panel.id)
  })

  it('opens and closes from the button, recording the state once', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    ui.button.click()
    expect(ui.button.getAttribute('aria-expanded')).toBe('true')
    expect(ui.panel.hidden).toBe(false)
    ui.button.click()
    expect(ui.button.getAttribute('aria-expanded')).toBe('false')
    expect(ui.panel.hidden).toBe(true)
  })

  it('closes on Escape and gives the button its focus back', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    ui.button.click()
    ui.panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(ui.panel.hidden).toBe(true)
    expect(document.activeElement).toBe(ui.button)
  })

  it('closes on a click outside without taking the focus with it', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    ui.button.click()
    document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    expect(ui.panel.hidden).toBe(true)
    // Closed, not grabbed: the reader was on their way somewhere else.
    expect(document.activeElement).not.toBe(ui.button)
  })
})

// The seam that keeps this module from holding a second copy of the style.
describe('groupsIn', () => {
  it('returns the style\'s groups in the reading order, not the style\'s', () => {
    const found = groupsIn([layer('a', 'poi-shop'), layer('b', 'water'), layer('c', 'base')])
    expect(found).toEqual(['base', 'water', 'poi-shop'])
  })

  it('skips a group the style does not carry, and one this file does not know', () => {
    expect(groupsIn([layer('a', 'water'), layer('b', 'weather')])).toEqual(['water'])
    expect(groupsIn([layer('a')])).toEqual([])
    expect(groupsIn([])).toEqual([])
  })
})

describe('readState / writeState', () => {
  it('reads back what it wrote', () => {
    const s = fakeStorage()
    writeState({ water: false }, s)
    expect(readState(s)).toEqual({ water: false })
  })

  it('treats anything that is not a plain object as no state at all', () => {
    // A stale or hand-edited value must never become a source of options.
    expect(readState(fakeStorage('not json'))).toEqual({})
    expect(readState(fakeStorage('[1,2]'))).toEqual({})
    expect(readState(fakeStorage('null'))).toEqual({})
    expect(readState(null)).toEqual({})
  })

  it('survives a storage that refuses to answer', () => {
    // Safari private browsing, a blocked third-party context: the menu still
    // works this visit, it just does not remember the next one.
    const blocked = {
      getItem: () => { throw new Error('denied') },
      setItem: () => { throw new Error('denied') },
    }
    expect(readState(blocked)).toEqual({})
    expect(() => writeState({ water: false }, blocked)).not.toThrow()
  })
})

describe('installLayers', () => {
  const style = [
    layer('background', 'base'),
    layer('water', 'water'),
    layer('water-name', 'water'),
    layer('building', 'buildings'),
    layer('airbg-markers'),
  ]

  it('offers one option per group the style carries, in the reading order', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    const map = fakeMap(style)
    installLayers(map, ui, { labels, caption: 'Show on the map', storage: fakeStorage() })

    expect(keys(ui)).toEqual(['base', 'water', 'buildings'])
    expect(options(ui).map((o) => o.querySelector('span').textContent))
      .toEqual(['L:base', 'L:water', 'L:buildings'])
    expect(ui.caption.textContent).toBe('Show on the map')
    expect(ui.root.hidden).toBe(false)
  })

  it('shows a group with no label under its own key rather than hiding it', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    installLayers(fakeMap(style), ui, { labels: { base: 'L:base' }, caption: 'c', storage: fakeStorage() })
    // A visible gap, not a silent omission: the layer is still reachable.
    expect(options(ui).map((o) => o.querySelector('span').textContent))
      .toEqual(['L:base', 'water', 'buildings'])
  })

  it('stays hidden when the style has nothing to switch', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    installLayers(fakeMap([layer('airbg-markers')]), ui, { labels, caption: 'c', storage: fakeStorage() })
    expect(options(ui)).toHaveLength(0)
    expect(ui.root.hidden).toBe(true)
  })

  it('starts every category on, and switches only its own layers off', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    const map = fakeMap(style)
    installLayers(map, ui, { labels, caption: 'c', storage: fakeStorage() })
    expect(options(ui).every((o) => o.querySelector('input').checked)).toBe(true)

    map.setLayoutProperty.mockClear()
    const water = ui.fieldset.querySelector('[data-layer-key="water"]')
    water.checked = false
    water.dispatchEvent(new Event('change'))

    expect(map.setLayoutProperty.mock.calls).toEqual([
      ['water', 'visibility', 'none'],
      ['water-name', 'visibility', 'none'],
    ])
  })

  it('remembers a switched-off category and applies it on the next mount', () => {
    const store = fakeStorage()
    const first = mountLayers(frame(), { label: 'Layers' })
    installLayers(fakeMap(style), first, { labels, caption: 'c', storage: store })
    const water = first.fieldset.querySelector('[data-layer-key="water"]')
    water.checked = false
    water.dispatchEvent(new Event('change'))
    expect(JSON.parse(store.value)).toEqual({ water: false })

    document.body.innerHTML = ''
    const map = fakeMap(style)
    const again = mountLayers(frame(), { label: 'Layers' })
    installLayers(map, again, { labels, caption: 'c', storage: store })

    // Both halves, or the menu lies: the box says off AND the map draws it off.
    expect(again.fieldset.querySelector('[data-layer-key="water"]').checked).toBe(false)
    expect(map.setLayoutProperty).toHaveBeenCalledWith('water', 'visibility', 'none')
    expect(map.setLayoutProperty).not.toHaveBeenCalledWith('background', 'visibility', 'none')
  })

  it('lists the view toggles above the categories', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    const views = [{ id: 'legend', label: 'Scale', apply: vi.fn() }]
    installLayers(fakeMap(style), ui, { labels, caption: 'c', views, storage: fakeStorage() })

    expect(keys(ui)).toEqual(['view:legend', 'base', 'water', 'buildings'])
    expect(options(ui)[0].className).toBe('colmenu__opt colmenu__opt--view')
    expect(views[0].apply).toHaveBeenCalledWith(true, expect.anything())
  })

  it('applies a view AFTER the categories, so a remembered one is not undone', () => {
    // "Hide the basemap" and "show water" write visibility on the same layers.
    // Applied in list order, the category boxes would restore what the view
    // toggle had just switched off — one click of state, silently lost.
    const order = []
    const ui = mountLayers(frame(), { label: 'Layers' })
    const map = {
      getStyle: () => ({ layers: style }),
      setLayoutProperty: vi.fn(() => order.push('group')),
    }
    const views = [{ id: 'basemap', label: 'Basemap', apply: () => order.push('view') }]
    installLayers(map, ui, { labels, caption: 'c', views, storage: fakeStorage() })

    expect(order.at(-1)).toBe('view')
    expect(order.filter((o) => o === 'group').length).toBeGreaterThan(0)
  })

  it('does not offer a view that needs a camera when the style has no layers', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    const views = [
      { id: 'legend', label: 'Scale', apply: vi.fn() },
      { id: 'basemap', label: 'Basemap', needsMap: true, apply: vi.fn() },
    ]
    installLayers(fakeMap([]), ui, { labels, caption: 'c', views, storage: fakeStorage() })

    // An inert checkbox is worse than an absent one; the key toggle still works
    // without tiles, so the control is still offered.
    expect(keys(ui)).toEqual(['view:legend'])
    expect(ui.root.hidden).toBe(false)
    expect(views[1].apply).not.toHaveBeenCalled()
  })

  // Everything else here is on until the reader switches it off, because the
  // map they were shown is the map they keep. A forecast overlay is the other
  // case: it is not part of the map they were shown, and turning it on costs a
  // request, so it has to ask rather than assume.
  it('starts a defaultOff view off, and applies it off', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    const views = [{ id: 'wind', label: 'Wind', defaultOff: true, apply: vi.fn() }]
    installLayers(fakeMap(style), ui, { labels, caption: 'c', views, storage: fakeStorage() })

    expect(ui.fieldset.querySelector('[data-layer-key="view:wind"]').checked).toBe(false)
    expect(views[0].apply).toHaveBeenCalledWith(false, expect.anything())
  })

  it('remembers a defaultOff view the reader switched ON', () => {
    const store = fakeStorage()
    const first = mountLayers(frame(), { label: 'Layers' })
    const views = () => [{ id: 'wind', label: 'Wind', defaultOff: true, apply: vi.fn() }]
    installLayers(fakeMap(style), first, { labels, caption: 'c', views: views(), storage: store })
    const box = first.fieldset.querySelector('[data-layer-key="view:wind"]')
    box.checked = true
    box.dispatchEvent(new Event('change'))

    document.body.innerHTML = ''
    const again = mountLayers(frame(), { label: 'Layers' })
    installLayers(fakeMap(style), again, { labels, caption: 'c', views: views(), storage: store })
    expect(again.fieldset.querySelector('[data-layer-key="view:wind"]').checked).toBe(true)
  })

  // The wind forecast is fetched, and /api/v1/wind answers 503 whenever no
  // forecast covers the current hour. A box that stayed ticked over a map with
  // no arrows on it would be the menu reporting a layer that is not there —
  // the same silent lie the missing arrow glyph told.
  it('follows what apply actually achieved, not what was asked', async () => {
    const store = fakeStorage()
    const ui = mountLayers(frame(), { label: 'Layers' })
    const views = [{ id: 'wind', label: 'Wind', defaultOff: true, apply: async () => false }]
    installLayers(fakeMap(style), ui, { labels, caption: 'c', views, storage: store })

    const box = ui.fieldset.querySelector('[data-layer-key="view:wind"]')
    box.checked = true
    box.dispatchEvent(new Event('change'))
    await Promise.resolve()
    await Promise.resolve()

    expect(box.checked).toBe(false)
    expect(JSON.parse(store.value)['view:wind']).toBe(false)
  })

  // Most applies report nothing, because most of them cannot fail. Reading a
  // missing answer as "off" would silently untick every one of those boxes.
  it('leaves the box alone when apply reports nothing', async () => {
    const store = fakeStorage()
    const ui = mountLayers(frame(), { label: 'Layers' })
    const views = [{ id: 'wind', label: 'Wind', defaultOff: true, apply: () => undefined }]
    installLayers(fakeMap(style), ui, { labels, caption: 'c', views, storage: store })

    const box = ui.fieldset.querySelector('[data-layer-key="view:wind"]')
    box.checked = true
    box.dispatchEvent(new Event('change'))
    await Promise.resolve()
    await Promise.resolve()

    expect(box.checked).toBe(true)
    expect(JSON.parse(store.value)['view:wind']).toBe(true)
  })

  it('rebuilds rather than doubling up when called twice', () => {
    const ui = mountLayers(frame(), { label: 'Layers' })
    const map = fakeMap(style)
    installLayers(map, ui, { labels, caption: 'c', storage: fakeStorage() })
    installLayers(map, ui, { labels, caption: 'c', storage: fakeStorage() })
    expect(keys(ui)).toEqual(['base', 'water', 'buildings'])
  })
})

// Same guard the key and the zoom stack carry: a misspelt kit class is silent —
// the control still mounts, unstyled, wherever the document happens to put it.
it('writes only classes the kit defines', () => {
  const here = dirname(fileURLToPath(import.meta.url))
  const css = readFileSync(join(here, '..', '..', '..', '..', 'design-kit', 'components.css'), 'utf8')
  const el = frame()
  const ui = mountLayers(el, { label: 'Layers' })
  installLayers(fakeMap([layer('water', 'water')]), ui, {
    labels, caption: 'c', storage: fakeStorage(),
    views: [{ id: 'legend', label: 'Scale', apply: () => {} }],
  })

  const used = new Set()
  for (const node of el.querySelectorAll('*')) {
    for (const c of (node.getAttribute('class') ?? '').split(/\s+/).filter(Boolean)) {
      if (c.includes('__') || c.includes('--')) used.add(c)
    }
  }
  expect([...used].sort()).toEqual([
    'btn--icon', 'colmenu__btn', 'colmenu__opt', 'colmenu__opt--view', 'colmenu__panel', 'map__layers',
  ])

  // btn--icon and colmenu__opt--view carry no rule in the kit either: both are
  // the kit's own hooks, written by its map-layers.js and kept here for parity
  // rather than invented, and named as the exceptions so they cannot quietly
  // grow company.
  for (const c of used) {
    if (c === 'btn--icon' || c === 'colmenu__opt--view') continue
    expect(css, `${c} missing from components.css`).toContain(`.${c}`)
  }
})

it('keeps STORAGE_KEY namespaced like every other key this site writes', () => {
  expect(STORAGE_KEY).toBe('airbg:map-layers')
})
