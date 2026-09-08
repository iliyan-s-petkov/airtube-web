// @vitest-environment jsdom
//
// jsdom for the two halves that touch the platform: a real <select> and the
// real localStorage chooseWindow writes through. Everything else here is pure.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  WINDOW_STORAGE_KEY, LIVE_WINDOW, WINDOWS, WINDOW_CHOICES,
  knownWindow, readWindow, writeWindow, withWindow, windowOptions,
  chooseWindow, mountWindow,
} from '../mapwindow.js'

// A storage that throws on both sides, which is what a browser in private mode
// with a full quota gives you — the selector still has to work this visit.
const hostile = () => ({
  getItem() { throw new Error('denied') },
  setItem() { throw new Error('denied') },
})

const stub = (value) => {
  const store = value === undefined ? {} : { [WINDOW_STORAGE_KEY]: value }
  return {
    getItem: (k) => (k in store ? store[k] : null),
    setItem: (k, v) => { store[k] = v },
    read: () => store[WINDOW_STORAGE_KEY],
  }
}

// chooseWindow writes through safeStorage(), which reads globalThis.localStorage
// — jsdom does not hand one out here, so the global is the stub for those tests
// rather than a real store this file would then have to clean between cases.
let global

beforeEach(() => {
  global = stub()
  vi.stubGlobal('localStorage', global)
  document.body.innerHTML = ''
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('knownWindow', () => {
  it('accepts live and every published window', () => {
    for (const name of WINDOW_CHOICES) expect(knownWindow(name)).toBe(true)
  })

  it('rejects anything else', () => {
    for (const name of ['1h', '24H', 'week', null, undefined, '7']) {
      expect(knownWindow(name)).toBe(false)
    }
  })

  it('publishes live first and the windows coarsest last', () => {
    expect(WINDOW_CHOICES).toEqual([LIVE_WINDOW, ...WINDOWS])
    expect(WINDOWS).toEqual(['24h', '48h', '7d'])
  })
})

describe('readWindow', () => {
  it('returns a stored window', () => {
    expect(readWindow(stub('48h'))).toBe('48h')
  })

  it('falls back to live when nothing is stored', () => {
    expect(readWindow(stub())).toBe(LIVE_WINDOW)
  })

  // A name we no longer publish must not reach the API, or the map answers
  // 400 on first paint. Degrading to live is the whole point.
  it('falls back to live for a name we do not publish', () => {
    expect(readWindow(stub('90d'))).toBe(LIVE_WINDOW)
  })

  it('falls back to live when storage throws', () => {
    expect(readWindow(hostile())).toBe(LIVE_WINDOW)
  })
})

describe('writeWindow', () => {
  it('stores under the shared key', () => {
    const s = stub()
    writeWindow('7d', s)
    expect(s.read()).toBe('7d')
  })

  it('survives storage that throws', () => {
    expect(() => writeWindow('7d', hostile())).not.toThrow()
  })
})

describe('withWindow', () => {
  it('adds nothing for live, so the default URL and its cache are unchanged', () => {
    expect(withWindow('/api/v1/overview', LIVE_WINDOW)).toBe('/api/v1/overview')
  })

  it('adds nothing for a name we do not publish', () => {
    expect(withWindow('/api/v1/overview', '90d')).toBe('/api/v1/overview')
  })

  it('opens the query string when there is none', () => {
    expect(withWindow('/api/v1/overview', '24h')).toBe('/api/v1/overview?window=24h')
  })

  it('appends to a query string that already exists', () => {
    expect(withWindow('/api/v1/overview?tier=city', '7d'))
      .toBe('/api/v1/overview?tier=city&window=7d')
  })
})

describe('windowOptions', () => {
  it('zips the labels positionally onto the choices', () => {
    expect(windowOptions(['Now', 'Last 24 hours', 'Last 48 hours', 'Last week'])).toEqual([
      { value: '', text: 'Now' },
      { value: '24h', text: 'Last 24 hours' },
      { value: '48h', text: 'Last 48 hours' },
      { value: '7d', text: 'Last week' },
    ])
  })

  it('falls back to the window name when a label is missing', () => {
    expect(windowOptions(['Now'])).toEqual([
      { value: '', text: 'Now' },
      { value: '24h', text: '24h' },
      { value: '48h', text: '48h' },
      { value: '7d', text: '7d' },
    ])
  })

  // Live's value is the empty string, which is falsy: without its own fallback
  // the first option would render as a blank line.
  it('never renders live as an empty option', () => {
    expect(windowOptions()[0]).toEqual({ value: '', text: 'live' })
  })
})

describe('chooseWindow', () => {
  it('takes a real change, stores it and asks for a refetch', () => {
    const state = { window: LIVE_WINDOW }
    expect(chooseWindow(state, '48h')).toBe(true)
    expect(state.window).toBe('48h')
    expect(localStorage.getItem(WINDOW_STORAGE_KEY)).toBe('48h')
  })

  it('refuses a name we do not publish, and leaves the state alone', () => {
    const state = { window: '24h' }
    expect(chooseWindow(state, '90d')).toBe(false)
    expect(state.window).toBe('24h')
    expect(localStorage.getItem(WINDOW_STORAGE_KEY)).toBe(null)
  })

  it('refuses the window already showing, so a no-op change refetches nothing', () => {
    const state = { window: '24h' }
    expect(chooseWindow(state, '24h')).toBe(false)
    expect(localStorage.getItem(WINDOW_STORAGE_KEY)).toBe(null)
  })

  it('takes a change back to live', () => {
    const state = { window: '7d' }
    expect(chooseWindow(state, LIVE_WINDOW)).toBe(true)
    expect(state.window).toBe(LIVE_WINDOW)
  })
})

describe('mountWindow', () => {
  const mount = (value = '48h') => {
    const frame = document.createElement('div')
    frame.id = 'map'
    document.body.append(frame)
    return {
      frame,
      ui: mountWindow(frame, {
        label: 'Averaging period',
        options: windowOptions(['Now', '24h', '48h', '7d']),
        value,
      }),
    }
  }

  // A disclosure in the bottom-left cluster, beside the refresh button, rather
  // than a select floating over the top of the map.
  it('builds a labelled button on the frame, showing the current window', () => {
    const { frame, ui } = mount()
    expect(ui.root.parentElement).toBe(frame)
    expect(ui.root.className).toContain('map-window')
    expect(ui.button.getAttribute('aria-label')).toBe('Averaging period')
    expect(ui.button.getAttribute('title')).toBe('Averaging period')
    expect(ui.button.textContent).toContain('48h')
    expect(ui.button.getAttribute('aria-expanded')).toBe('false')
    expect(ui.panel.hidden).toBe(true)
  })

  it('offers every published window, with the current one checked', () => {
    const { ui } = mount()
    const radios = [...ui.panel.querySelectorAll('input[type="radio"]')]
    expect(radios.map((r) => r.value)).toEqual(WINDOW_CHOICES)
    expect(radios.filter((r) => r.checked).map((r) => r.value)).toEqual(['48h'])
    // One group, so picking one un-picks the last: the window is one choice,
    // not a set of independent layers.
    expect(new Set(radios.map((r) => r.name)).size).toBe(1)
  })

  it('opens on click and closes on Escape', () => {
    const { ui } = mount()
    ui.button.click()
    expect(ui.panel.hidden).toBe(false)
    expect(ui.button.getAttribute('aria-expanded')).toBe('true')

    ui.root.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(ui.panel.hidden).toBe(true)
  })

  it('closes on a click outside itself', () => {
    const { ui } = mount()
    ui.button.click()
    document.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    expect(ui.panel.hidden).toBe(true)
  })

  // The caller reloads the map from this; what it owes back is the picked name
  // and a closed panel, with the button now saying what is on screen.
  it('reports the pick, closes, and renames the button', () => {
    const { ui } = mount('')
    const picked = []
    ui.onpick((name) => picked.push(name))
    ui.button.click()

    const radio = ui.panel.querySelector('input[value="7d"]')
    radio.checked = true
    radio.dispatchEvent(new Event('change', { bubbles: true }))

    expect(picked).toEqual(['7d'])
    expect(ui.panel.hidden).toBe(true)
    expect(ui.button.textContent).toContain('7d')
  })

  // No el.style anywhere: the CSP carries no style-src 'unsafe-inline', so a
  // control positioned from JS would simply not be placed.
  it('writes no inline style', () => {
    const { ui } = mount()
    expect(ui.root.getAttribute('style')).toBe(null)
    expect(ui.button.getAttribute('style')).toBe(null)
  })
})
