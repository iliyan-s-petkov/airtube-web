// @vitest-environment jsdom
//
// jsdom, because every assertion here is on a real element: the class the kit
// styles, the aria-label an icon-only control needs, the disabled property.
// What jsdom does NOT have is the Fullscreen API, which is why mountFullscreen
// takes its document as an argument.
import { describe, it, expect, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mountFullscreen, mountZoom, installZoom } from '../mapcontrols.js'

const frame = () => {
  const el = document.createElement('div')
  el.className = 'map map--hero'
  document.body.append(el)
  return el
}

// A document stand-in, because jsdom implements neither requestFullscreen nor
// fullscreenElement — so without one there is no way to reach either branch.
const fakeDoc = () => ({
  fullscreenElement: null,
  exitFullscreen: vi.fn(function () { this.fullscreenElement = null }),
  body: document.createElement('body'),
  addEventListener: vi.fn(),
})

describe('mountFullscreen', () => {
  it('carries the kit classes and both glyphs, and starts unpressed', () => {
    const el = frame()
    const btn = mountFullscreen(el, { label: 'Full screen', exitLabel: 'Exit' }, fakeDoc())

    expect(btn.parentElement).toBe(el)
    expect(btn.className).toBe('map__full')
    expect(btn.type).toBe('button')
    expect(btn.getAttribute('aria-pressed')).toBe('false')
    // Both icons ship at once; the kit's CSS shows one per pressed state, so a
    // JS swap here would be a second record of the same fact.
    expect([...btn.querySelectorAll('svg')].map((s) => s.getAttribute('class'))).toEqual([
      'map__full-ico map__full-ico--in',
      'map__full-ico map__full-ico--out',
    ])
  })

  it('names the button after what the next click does, not after the state', () => {
    const el = frame()
    const doc = fakeDoc()
    const btn = mountFullscreen(el, { label: 'Full screen', exitLabel: 'Exit' }, doc)
    expect(btn.getAttribute('aria-label')).toBe('Full screen')
    expect(btn.getAttribute('title')).toBe('Full screen')

    el.requestFullscreen = () => { doc.fullscreenElement = el; return Promise.resolve() }
    btn.click()
    return Promise.resolve().then(() => {
      expect(btn.getAttribute('aria-pressed')).toBe('true')
      expect(btn.getAttribute('aria-label')).toBe('Exit')
    })
  })

  it('falls back to the pinned frame when the browser refuses fullscreen', async () => {
    const el = frame()
    const doc = fakeDoc()
    const btn = mountFullscreen(el, { label: 'Full screen', exitLabel: 'Exit' }, doc)

    el.requestFullscreen = () => Promise.reject(new Error('refused'))
    btn.click()
    await Promise.resolve()
    await Promise.resolve()

    // The whole point of the fallback: a refusal must still change something.
    expect(el.classList.contains('map--faux-full')).toBe(true)
    expect(doc.body.classList.contains('has-faux-full')).toBe(true)
    expect(btn.getAttribute('aria-pressed')).toBe('true')
  })

  it('falls back where requestFullscreen does not exist at all', () => {
    const el = frame()
    const doc = fakeDoc()
    const btn = mountFullscreen(el, { label: 'Full screen', exitLabel: 'Exit' }, doc)
    btn.click()
    expect(el.classList.contains('map--faux-full')).toBe(true)
  })

  it('leaves the fallback on a second click', () => {
    const el = frame()
    const doc = fakeDoc()
    const btn = mountFullscreen(el, { label: 'Full screen', exitLabel: 'Exit' }, doc)
    btn.click()
    btn.click()
    expect(el.classList.contains('map--faux-full')).toBe(false)
    expect(doc.body.classList.contains('has-faux-full')).toBe(false)
    expect(btn.getAttribute('aria-pressed')).toBe('false')
  })

  it('exits real fullscreen through the document, not by dropping a class', () => {
    const el = frame()
    const doc = fakeDoc()
    doc.fullscreenElement = el
    const btn = mountFullscreen(el, { label: 'Full screen', exitLabel: 'Exit' }, doc)
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    btn.click()
    expect(doc.exitFullscreen).toHaveBeenCalled()
  })
})

describe('mountZoom', () => {
  it('builds the kit stack: three named buttons, in the kit order', () => {
    const el = frame()
    const { el: bar, buttons } = mountZoom(el, { inLabel: 'In', outLabel: 'Out', resetLabel: 'Reset' })

    expect(bar.parentElement).toBe(el)
    expect(bar.className).toBe('map-zoom')
    expect([...bar.children].map((b) => b.getAttribute('data-act'))).toEqual(['in', 'out', 'reset'])
    expect([...bar.children].map((b) => b.getAttribute('aria-label'))).toEqual(['In', 'Out', 'Reset'])
    // Icon-only, so title carries the same name for a pointer reader.
    expect([...bar.children].map((b) => b.getAttribute('title'))).toEqual(['In', 'Out', 'Reset'])
    for (const b of Object.values(buttons)) {
      expect(b.className).toBe('map-zoom__btn')
      expect(b.type).toBe('button')
      expect(b.querySelector('svg').getAttribute('aria-hidden')).toBe('true')
    }
  })

  it('gives each button its own glyph', () => {
    const el = frame()
    const { buttons } = mountZoom(el, { inLabel: 'In', outLabel: 'Out', resetLabel: 'Reset' })
    const paths = (b) => [...b.querySelectorAll('path')].map((p) => p.getAttribute('d'))
    // Plus is minus with a stroke added, and reset is neither — the marks have
    // to differ, because three identical squares in a column is one control.
    expect(paths(buttons.in)).toHaveLength(2)
    expect(paths(buttons.out)).toHaveLength(1)
    expect(paths(buttons.reset)).toHaveLength(3)
    expect(new Set([...paths(buttons.in), ...paths(buttons.reset)]).size).toBe(4)
  })
})

describe('installZoom', () => {
  const fakeMap = (zoom = 7, min = 5, max = 18) => {
    const listeners = {}
    return {
      zoom,
      zoomIn: vi.fn(function () { this.zoom++; listeners.zoom?.() }),
      zoomOut: vi.fn(function () { this.zoom--; listeners.zoom?.() }),
      flyTo: vi.fn(),
      getZoom() { return this.zoom },
      getMinZoom: () => min,
      getMaxZoom: () => max,
      on: (name, fn) => { listeners[name] = fn },
      fire: (name) => listeners[name]?.(),
    }
  }

  const wire = (map) => {
    const { buttons } = mountZoom(frame(), { inLabel: 'In', outLabel: 'Out', resetLabel: 'Reset' })
    installZoom(map, buttons, { centre: [25.4858, 42.7339], zoom: 7 })
    return buttons
  }

  it('drives the camera rather than holding its own zoom', () => {
    const map = fakeMap()
    const buttons = wire(map)
    buttons.in.click()
    buttons.out.click()
    expect(map.zoomIn).toHaveBeenCalledOnce()
    expect(map.zoomOut).toHaveBeenCalledOnce()
  })

  it('returns the camera to the view the page opened at', () => {
    const map = fakeMap()
    wire(map).reset.click()
    // `center`, MapLibre's spelling, out of `centre`, ours. The two sit one
    // property apart and a map handed the wrong key flies to null island.
    expect(map.flyTo).toHaveBeenCalledWith({ center: [25.4858, 42.7339], zoom: 7 })
  })

  it('disables a button at the limit it reports, not at a limit written here', () => {
    const buttons = wire(fakeMap(5, 5, 18))
    expect(buttons.out.disabled).toBe(true)
    expect(buttons.in.disabled).toBe(false)

    const top = wire(fakeMap(18, 5, 18))
    expect(top.in.disabled).toBe(true)
    expect(top.out.disabled).toBe(false)
  })

  it('repaints the limits as the camera moves', () => {
    const map = fakeMap(6, 5, 18)
    const buttons = wire(map)
    expect(buttons.out.disabled).toBe(false)
    buttons.out.click()
    expect(buttons.out.disabled).toBe(true)
  })

  it('never disables reset: a panned camera still has somewhere to go back to', () => {
    expect(wire(fakeMap(7)).reset.disabled).toBe(false)
    expect(wire(fakeMap(5, 5, 18)).reset.disabled).toBe(false)
    expect(wire(fakeMap(18, 5, 18)).reset.disabled).toBe(false)
  })
})

// Same guard the key carries: every kit class this module writes has to exist
// in the kit's own stylesheet. A misspelling here is silent — the control still
// mounts, unstyled, in the top-left corner of the map.
it('writes only classes the kit defines', () => {
  // join(dirname(fileURLToPath(...))), not new URL(..., import.meta.url) — Vite
  // turns the latter into an asset import and then rejects the path for being
  // outside the project root. Same idiom as legend.test.js's own kit guard.
  const here = dirname(fileURLToPath(import.meta.url))
  const css = readFileSync(join(here, '..', '..', '..', '..', 'design-kit', 'components.css'), 'utf8')
  const el = frame()
  mountFullscreen(el, { label: 'a', exitLabel: 'b' }, fakeDoc())
  mountZoom(el, { inLabel: 'a', outLabel: 'b', resetLabel: 'c' })

  const used = new Set()
  for (const node of el.querySelectorAll('*')) {
    const cls = node.getAttribute('class') ?? ''
    for (const c of cls.split(/\s+/).filter(Boolean)) if (c.includes('__') || c.includes('--')) used.add(c)
  }
  expect([...used].sort()).toEqual([
    'map__full', 'map__full-ico', 'map__full-ico--in', 'map__full-ico--out',
    'map-zoom__btn', 'map-zoom__ico',
  ].sort())

  // map-zoom__ico carries no rule in the kit either — it is the kit's own
  // unstyled hook on the glyph, kept for parity rather than invented here, and
  // named as the exception so it cannot quietly grow company.
  for (const c of used) {
    if (c === 'map-zoom__ico') continue
    expect(css, `${c} missing from components.css`).toContain(`.${c}`)
  }
})
