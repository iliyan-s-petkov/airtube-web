// @vitest-environment jsdom
//
// jsdom: mountChrome builds real DOM, which every test here drives directly.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { mountChrome } from '../chrome.js'
import { refreshHexes } from '../mapdata.js'
import { readConfig, LEGEND_FOLD_KEY } from '../mapconfig.js'
import { setSensorStatus, getSensorStatus, resetSensorFilterForTests } from '../sensorfilter.svelte.js'

// The cfg every mountChrome test hands in. metricLabels and metricUnits are
// keyed by metric because that is what readConfig produces (see byMetric): the
// key's caption is looked up by name, never by position.
const chromeCfg = (over = {}) => ({
  t: { tier: {} },
  noDataColour: '#999',
  lang: 'bg',
  metric: 'P2',
  metricLabels: { P1: 'ФПЧ10', P2: 'ФПЧ2.5' },
  metricUnits: { P1: 'µg/m³', P2: 'µg/m³' },
  ...over,
})

// Mounted in mountChrome and not in mount(), which is what puts it on both maps
// that carry this island — the home page and an area page — from one call.
describe('the averaging selector', () => {
  it('is mounted on the frame with the server-rendered words', () => {
    const el = document.createElement('div')
    el.dataset.tWindowLabel = 'Averaging period'
    el.dataset.tWindows = 'Now,Last 24 hours,Last 48 hours,Last week'
    document.body.appendChild(el)

    const { windowMenu } = mountChrome(el, readConfig(el))
    expect(windowMenu, 'no window menu in the chrome').toBeTruthy()
    expect(windowMenu.root.parentElement).toBe(el)
    expect(windowMenu.button.getAttribute('aria-label')).toBe('Averaging period')
    const radios = [...windowMenu.panel.querySelectorAll('input[type="radio"]')]
    expect(radios.map((r) => r.value)).toEqual(['', '24h', '48h', '7d'])
    expect(radios.map((r) => r.nextElementSibling.textContent))
      .toEqual(['Now', 'Last 24 hours', 'Last 48 hours', 'Last week'])
  })
})

// "Hide the basemap" must take down the ground and leave the readings standing.
// It used to walk every layer carrying an airbg:group — a marker the vector
// style set and the raster-only style cannot: the toggle reported itself on and
// hid nothing.
describe('the basemap toggle', () => {
  it('hides the ground, raster and vector detail alike, and no reading', () => {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const { layerViews } = mountChrome(el, readConfig(el))
    const basemap = layerViews.find((v) => v.id === 'basemap')
    expect(basemap, 'no basemap view in the layers menu').toBeTruthy()

    const set = []
    const map = {
      setLayoutProperty: (...a) => set.push(a),
      // The ground is the raster PLUS the vector detail over it. Every layer
      // carrying an airbg:group came from the vector style; the readings carry
      // none, and must be left standing.
      getStyle: () => ({ layers: [
        { id: 'poi-shop', metadata: { 'airbg:group': 'poi-shop' } },
        { id: 'airbg-hex-fill' },
      ] }),
    }
    basemap.apply(false, map)

    expect(set).toEqual([
      ['airbg-raster-base', 'visibility', 'none'],
      ['poi-shop', 'visibility', 'none'],
    ])
  })
})

// The disclosure is why an unmeasured forecast layer is allowed on a map of
// measurements, so it is never dismissible — but two sentences and a model name
// unrolled over the map is most of a phone screen. Folded, it is a line the
// reader can open.
describe('the wind disclosure', () => {
  // Scoped to its own frame: the disclosure is one of two <details> the chrome
  // builds, and a document-wide query would find whichever came first.
  const chrome = () => {
    const el = document.createElement('div')
    el.className = 'map'
    el.dataset.tWindAbout = 'About the wind layer'
    document.body.appendChild(el)
    return { el, ...mountChrome(el, readConfig(el)) }
  }

  it('arrives folded, with the full text inside it', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const note = c.el.querySelector('.map-wind-label')
    expect(note.tagName).toBe('DETAILS')
    expect(note.open).toBe(false)
    expect(note.hidden).toBe(false)
    expect(note.textContent).toContain('Wind forecast · valid now')
  })

  it('names itself on the summary, so a folded line still says what it is', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const summary = c.el.querySelector('.map-wind-label summary')
    expect(summary).toBeTruthy()
    expect(summary.textContent.trim()).not.toBe('')
  })

  it('goes away with the arrows, and comes back folded', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const note = c.el.querySelector('.map-wind-label')
    note.open = true
    c.showWind(false, '')
    expect(note.hidden).toBe(true)
    c.showWind(true, 'Wind forecast · valid now')
    expect(note.open).toBe(false)
  })
})

// A sensor that has stopped reporting still has a cell on the grid, drawn in
// the no-data colour. At country zoom that is most of what a reader sees on a
// bad day for the network, and it reads as "nothing here" rather than "nobody
// is measuring here". The status filter already governed the markers; the grid
// is the tier that actually covers the country, so it is governed too.
describe('the inactive-sensors toggle', () => {
  const hexCfg = { metric: 'P2', noDataColour: '#cccccc' }
  const scales = [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }, { upper: null, colour: '#ff0000' }] }]
  const mixed = {
    resolution_km: 1,
    hexes: [
      { lon: 23.32, lat: 42.65, n: 1, values: { P2: 5 } },
      { lon: 23.34, lat: 42.66, n: 0, values: {} },
    ],
  }

  function hexMap() {
    const painted = []
    return {
      painted,
      getZoom: () => 12,
      getBounds: () => ({ getWest: () => 23.3, getSouth: () => 42.6, getEast: () => 23.4, getNorth: () => 42.7 }),
      getSource: (id) => (id === 'airbg-hexes' ? { setData: (d) => painted.push(d) } : undefined),
    }
  }

  afterEach(() => resetSensorFilterForTests())

  it('leaves the silent cells off the grid by default', async () => {
    const map = hexMap()
    await refreshHexes(map, { scales, hexUrl: null, hexBody: null }, hexCfg, async () => mixed)

    const values = map.painted[0].features.map((f) => f.properties.value)
    expect(values).toEqual([5])
  })

  it('draws them once the reader asks for them', async () => {
    setSensorStatus('all')
    const map = hexMap()
    await refreshHexes(map, { scales, hexUrl: null, hexBody: null }, hexCfg, async () => mixed)

    const values = map.painted[0].features.map((f) => f.properties.value)
    expect(values).toHaveLength(2)
    expect(values).toContain(null)
  })

  it('offers the option unticked, and flips the shared status', () => {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const { layerViews } = mountChrome(el, readConfig(el))
    const view = layerViews.find((v) => v.id === 'inactiveSensors')
    expect(view, 'no inactive-sensors view in the layers menu').toBeTruthy()

    // Unticked on arrival: the default is the quieter map.
    expect(view.defaultOff).toBe(true)
    view.apply(true)
    expect(getSensorStatus()).toBe('all')
    view.apply(false)
    expect(getSensorStatus()).toBe('active')
  })
})

// Real fullscreen renders the frame and nothing else. The key is anchored to
// the shell — deliberately, so a wide window does not float it over the page —
// which meant going full screen took the colour key off the map, on the one
// view where the map is all there is.
describe('mountChrome() keeps the key on the map in fullscreen', () => {
  const chromeFrame = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)
    return { shell, el }
  }

  it('moves the key into the frame and back out again', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')
    expect(legend, 'no key on the shell').toBeTruthy()

    el.querySelector('.map__full').click()
    expect(legend.parentElement, 'the key stayed outside the fullscreen frame').toBe(el)

    el.querySelector('.map__full').click()
    expect(legend.parentElement).toBe(shell)
  })

  it('leaves it a details, so it can still be folded away', () => {
    const { el } = chromeFrame()
    mountChrome(el, readConfig(el))
    el.querySelector('.map__full').click()

    const legend = el.querySelector('details.scale')
    expect(legend.tagName).toBe('DETAILS')
    expect(legend.open, 'the key came back folded shut').toBe(true)
  })
})

// The fold and the layers-menu option are two different controls: the menu says
// whether there is a key at all, the triangle says whether it is unrolled. A
// fold that forgets is the one that reads as broken — the reader folds the key
// away, reloads, and it is back over the map.
describe('mountChrome() remembers whether the key is folded', () => {
  const chromeFrame = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)
    return { shell, el }
  }

  // This jsdom has no localStorage of its own, so the seam is stubbed rather
  // than cleared — which also proves the code reaches for the real one.
  let store
  beforeEach(() => {
    store = new Map()
    vi.stubGlobal('localStorage', {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    })
  })
  // Left stubbed on purpose: vi.unstubAllGlobals would also drop the fetch stub
  // the later suites install, and an empty store reads exactly like the absent
  // localStorage this jsdom otherwise has.

  it('opens the key on a first visit', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    expect(shell.querySelector('details.scale').open).toBe(true)
  })

  it('records the fold when the reader closes it', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')

    legend.open = false
    legend.dispatchEvent(new Event('toggle'))

    expect(store.get(LEGEND_FOLD_KEY)).toBe('false')
  })

  it('opens folded on the next visit, and records the reopening', () => {
    store.set(LEGEND_FOLD_KEY, 'false')
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')
    expect(legend.open, 'the key ignored the remembered fold').toBe(false)

    legend.open = true
    legend.dispatchEvent(new Event('toggle'))
    expect(store.get(LEGEND_FOLD_KEY)).toBe('true')
  })
})

// Where the key and the tier line LAND is load-bearing, not decoration, and
// both defects it guards were found in a browser rather than here.
//
// The key is absolutely positioned against .map-shell. Put it inside #map and
// the kit's phone rule — which turns it static so it sits UNDER the map below
// 672px — leaves it under the map but still inside it, over the canvas corner.
// Put the tier paragraph inside the shell and the shell grows taller than the
// map, so the key's inset-block-end:16px is measured from a bottom edge 16px
// below the map's own: measured live at -16px before this.
describe('mountChrome anchors the key to the shell and the tier line outside it', () => {
  const chrome = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map map--hero'
    shell.appendChild(el)
    const host = document.createElement('div')
    host.append(shell)
    mountChrome(el, chromeCfg())
    return { shell, el, host }
  }

  it('puts the key in the shell, not in the map', () => {
    const { shell, el } = chrome()
    expect(el.querySelector('.scale--onmap')).toBeNull()
    expect(shell.querySelector(':scope > .scale--onmap')).not.toBeNull()
  })

  it('puts the tier line after the shell, so the shell stays the map box', () => {
    const { shell, host } = chrome()
    expect(shell.querySelector('.map-tier')).toBeNull()
    expect(host.lastElementChild.className).toContain('map-tier')
  })

  // The banners stay children of the map: they are messages about the map and
  // they do not have the phone rule the key has.
  it('leaves the hint and note inside the map', () => {
    const { el } = chrome()
    expect(el.querySelector('.map-hint')).not.toBeNull()
    expect(el.querySelector('.map-note')).not.toBeNull()
  })

  // Without a shell the key still has to render. It anchors to the map instead,
  // which loses the phone layout but shows a key rather than throwing.
  it('falls back to the map when no shell wraps it', () => {
    const el = document.createElement('div')
    document.createElement('div').appendChild(el)
    mountChrome(el, chromeCfg())
    expect(el.querySelector('.scale--onmap')).not.toBeNull()
  })
})

// The key's caption is the metric and its unit, not a fixed phrase. "Качество
// на въздуха" is simply false when the map is painting temperature, and it is
// the same words for every metric — so it says nothing about which one is on
// screen. The unit comes from the server-rendered catalogue rather than
// /api/v1/scales, which the key must caption without waiting on.
describe('the key names the metric it is a key to', () => {
  const captionOf = (cfg) => {
    const el = document.createElement('div')
    document.createElement('div').appendChild(el)
    return { el, chrome: mountChrome(el, cfg) }
  }

  it('composes the name and the unit on the first paint', () => {
    const { el } = captionOf(chromeCfg())
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ2.5, µg/m³')
  })

  // cfg.metric is already the new metric by the time onMetricChange calls
  // refresh, and refresh passes it through — so the caption follows the
  // switcher without the key subscribing to anything.
  it('follows the metric switch', () => {
    const { el, chrome } = captionOf(chromeCfg())
    chrome.showLegend({ bands: [], tier: null, metric: 'P1' })
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ10, µg/m³')
  })

  // A metric the catalogue has no unit for still gets its name.
  it('drops to the name alone when the metric has no unit', () => {
    const cfg = chromeCfg({ metricUnits: { P1: '', P2: '' } })
    const { el } = captionOf(cfg)
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ2.5')
  })

  // Only a metric with no name at all falls back, because a caption reading
  // just "µg/m³" would name nothing.
  it('falls back to the generic title when the metric has no name', () => {
    const cfg = chromeCfg({ metricLabels: {}, t: { tier: {}, legend: 'Качество на въздуха' } })
    const { el } = captionOf(cfg)
    expect(el.querySelector('.scale__label').textContent).toBe('Качество на въздуха')
  })
})

// The map is a canvas, so the banner is the only part of its running commentary
// a screen reader can reach. Without aria-live the text changes silently.
describe('mountChrome() announces the hint banner', () => {
  it('marks it as a polite live region', () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)

    mountChrome(el, readConfig(el))
    expect(el.querySelector('.map-hint')?.getAttribute('aria-live')).toBe('polite')
  })
})
