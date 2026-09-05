// @vitest-environment jsdom
//
// jsdom because every assertion here is about real DOM: which element the
// picker builds, what its accessible name says, and what happens to
// documentElement.dataset when a choice is made.
import { describe, it, expect, beforeEach } from 'vitest'
import { THEMES, STORAGE_KEY, readTheme, writeTheme, render, mount } from '../theme.js'

// A storage double rather than jsdom's localStorage: two of these tests need a
// store that THROWS, which the real one will not do on demand.
function fakeStore(initial = {}, { throwOn = null } = {}) {
  const data = { ...initial }
  return {
    getItem: (k) => (throwOn === 'get' ? raise() : (data[k] ?? null)),
    setItem: (k, v) => (throwOn === 'set' ? raise() : void (data[k] = v)),
    removeItem: (k) => (throwOn === 'remove' ? raise() : void delete data[k]),
    data,
  }
}
const raise = () => {
  throw new Error('storage is blocked')
}

// The labels the server puts on the element. Written here as the template
// writes them, so a test failure points at the data-* contract and not at a
// string this file invented.
function picker() {
  const el = document.createElement('details')
  el.dataset.island = 'theme'
  el.dataset.label = 'Тема'
  el.dataset.auto = 'Автоматична'
  el.dataset.light = 'Светла'
  el.dataset.dark = 'Тъмна'
  el.hidden = true
  return el
}

beforeEach(() => {
  delete document.documentElement.dataset.theme
})

describe('readTheme', () => {
  const bare = () => document.createElement('html')

  it('reads a stored override', () => {
    expect(readTheme(bare(), fakeStore({ [STORAGE_KEY]: 'dark' }))).toBe('dark')
  })

  // "auto" is the ABSENCE of a stored value, so nothing on disk and a literal
  // "auto" on disk have to mean the same thing — otherwise theme-init.js, which
  // only ever looks for light/dark, would disagree with this module.
  it('reads an empty store and a stored "auto" as the same state', () => {
    expect(readTheme(bare(), fakeStore())).toBe('auto')
    expect(readTheme(bare(), fakeStore({ [STORAGE_KEY]: 'auto' }))).toBe('auto')
  })

  it('ignores a value that is not one of the three states', () => {
    expect(readTheme(bare(), fakeStore({ [STORAGE_KEY]: 'sepia' }))).toBe('auto')
  })

  it('falls back to auto when there is no storage at all', () => {
    expect(readTheme(bare(), null)).toBe('auto')
  })

  // The reason the attribute is consulted first: it is what paints. A browser
  // that refused the write still carries the reader's choice on the element,
  // and a picker disagreeing with the page it sits on is the bug this prevents.
  it('prefers the applied attribute over a stale stored value', () => {
    const root = bare()
    root.dataset.theme = 'light'
    expect(readTheme(root, fakeStore({ [STORAGE_KEY]: 'dark' }))).toBe('light')
  })

  it('survives a store that throws when read', () => {
    expect(readTheme(bare(), fakeStore({}, { throwOn: 'get' }))).toBe('auto')
  })
})


describe('writeTheme', () => {
  it('sets both the attribute and the stored value', () => {
    const store = fakeStore()
    writeTheme('light', document.documentElement, store)
    expect(document.documentElement.dataset.theme).toBe('light')
    expect(store.data[STORAGE_KEY]).toBe('light')
  })

  // Removing the attribute is the whole mechanism: the kit's dark block is
  // `:root:not([data-theme="light"])` under prefers-color-scheme, so an
  // attribute left behind would pin the theme and "auto" would never return.
  it('removes the attribute and the stored value for auto', () => {
    const store = fakeStore({ [STORAGE_KEY]: 'dark' })
    document.documentElement.dataset.theme = 'dark'
    writeTheme('auto', document.documentElement, store)
    expect(document.documentElement.dataset.theme).toBeUndefined()
    expect(store.data[STORAGE_KEY]).toBeUndefined()
  })

  // Safari in private browsing throws from setItem. The choice must still hold
  // for this page load — the attribute is what paints, not the storage.
  it('still applies the choice when storage throws', () => {
    expect(() =>
      writeTheme('dark', document.documentElement, fakeStore({}, { throwOn: 'set' })),
    ).not.toThrow()
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('still clears the attribute when removeItem throws', () => {
    document.documentElement.dataset.theme = 'dark'
    expect(() =>
      writeTheme('auto', document.documentElement, fakeStore({}, { throwOn: 'remove' })),
    ).not.toThrow()
    expect(document.documentElement.dataset.theme).toBeUndefined()
  })
})

describe('render', () => {
  it('unhides the element the server rendered hidden', () => {
    const el = render(picker(), 'auto')
    expect(el.hidden).toBe(false)
  })

  it('offers every state, marking the current one', () => {
    const el = render(picker(), 'dark')
    const opts = [...el.querySelectorAll('.langpick__opt')]
    expect(opts.map((o) => o.dataset.theme)).toEqual(THEMES)
    expect(opts.filter((o) => o.hasAttribute('aria-current')).map((o) => o.dataset.theme))
      .toEqual(['dark'])
  })

  // Every visible word comes from the catalogue through a data-* attribute. A
  // hardcoded English "Dark" here would be a second place a new language has to
  // be added, and it would not be found by anyone reading the catalogue.
  it('takes every label from the element rather than from the module', () => {
    const el = render(picker(), 'light')
    expect([...el.querySelectorAll('.langpick__opt')].map((o) => o.textContent))
      .toEqual(['Автоматична', 'Светла', 'Тъмна'])
  })

  // The summary shows an icon and no word, so its accessible name is the only
  // thing that says what the control is and what it is currently set to.
  it('names the icon-only summary with the label and the current state', () => {
    const el = render(picker(), 'light')
    expect(el.querySelector('summary').getAttribute('aria-label')).toBe('Тема: Светла')
  })

  it('draws a different icon for each state', () => {
    const el = render(picker(), 'auto')
    const drawn = [...el.querySelectorAll('.langpick__list .pick-ico')].map((s) => s.innerHTML)
    expect(new Set(drawn).size).toBe(THEMES.length)
  })

  // renderLegend has the same contract and the same reason: style-src has no
  // 'unsafe-inline', so anything painted through a style attribute is dropped
  // silently and renders invisible.
  it('emits no inline style attribute', () => {
    expect(render(picker(), 'dark').querySelectorAll('[style]')).toHaveLength(0)
  })

  it('replaces its contents rather than appending on every repaint', () => {
    const el = render(picker(), 'auto')
    render(el, 'dark')
    expect(el.querySelectorAll('summary')).toHaveLength(1)
    expect(el.querySelectorAll('.langpick__opt')).toHaveLength(THEMES.length)
  })
})

describe('mount', () => {
  it('applies a choice, re-marks it, and closes the menu', () => {
    const el = picker()
    document.body.appendChild(el)
    mount(el)
    el.open = true

    el.querySelector('[data-theme="dark"]').click()

    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(el.querySelector('[aria-current]').dataset.theme).toBe('dark')
    expect(el.open).toBe(false)
    el.remove()
  })

  // The listener is delegated precisely so it survives the replaceChildren()
  // that render() performs: a second choice has to work as well as the first.
  it('keeps working after the first choice rebuilt the list', () => {
    const el = picker()
    document.body.appendChild(el)
    mount(el)

    el.querySelector('[data-theme="dark"]').click()
    el.querySelector('[data-theme="auto"]').click()

    expect(document.documentElement.dataset.theme).toBeUndefined()
    el.remove()
  })

  it('ignores a click that lands on no option', () => {
    const el = picker()
    document.body.appendChild(el)
    mount(el)
    el.querySelector('summary').click()
    expect(document.documentElement.dataset.theme).toBeUndefined()
    el.remove()
  })
})
