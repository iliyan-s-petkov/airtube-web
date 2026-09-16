// @vitest-environment jsdom
//
// jsdom because every assertion is about a <details> element's open state and
// the events that change it.
import { describe, it, expect, beforeEach } from 'vitest'
import { soleOpen } from '../disclosure.js'

// The masthead as base.gohtml renders it: the language picker and the theme
// picker, two sibling <details class="langpick"> with nothing tying them
// together. On prod both could be open at once, overlapping each other.
function masthead() {
  const nav = document.createElement('nav')
  for (const name of ['lang', 'theme']) {
    const d = document.createElement('details')
    d.className = 'langpick'
    d.dataset.which = name
    const s = document.createElement('summary')
    s.textContent = name
    d.appendChild(s)
    nav.appendChild(d)
  }
  document.body.appendChild(nav)
  return { nav, lang: nav.children[0], theme: nav.children[1] }
}

// jsdom runs the <details> default action, so the click alone opens it — the
// same sequence a reader produces. Setting `open` first would be the browser's
// job done twice and the click would toggle it straight back shut.
function openBy(menu) {
  menu.querySelector('summary').dispatchEvent(new MouseEvent('click', { bubbles: true }))
}

describe('soleOpen', () => {
  beforeEach(() => {
    document.body.replaceChildren()
  })

  it('closes the other picker when one is opened', () => {
    const { nav, lang, theme } = masthead()
    soleOpen(nav, 'details.langpick')

    openBy(lang)
    expect(lang.open).toBe(true)

    openBy(theme)
    expect(theme.open).toBe(true)
    expect(lang.open).toBe(false)
  })

  it('leaves the open one alone when its own summary is clicked', () => {
    const { nav, lang } = masthead()
    soleOpen(nav, 'details.langpick')

    openBy(lang)
    expect(lang.open).toBe(true)
  })

  it('closes on a click outside the pickers', () => {
    const { nav, lang } = masthead()
    soleOpen(nav, 'details.langpick')
    openBy(lang)

    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true }))

    expect(lang.open).toBe(false)
  })

  it('leaves a picker open when the click lands inside its own list', () => {
    const { nav, lang } = masthead()
    const opt = document.createElement('button')
    lang.appendChild(opt)
    soleOpen(nav, 'details.langpick')
    openBy(lang)

    opt.dispatchEvent(new MouseEvent('click', { bubbles: true }))

    expect(lang.open).toBe(true)
  })

  it('closes on Escape', () => {
    const { nav, lang, theme } = masthead()
    soleOpen(nav, 'details.langpick')
    openBy(lang)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))

    expect(lang.open).toBe(false)
    expect(theme.open).toBe(false)
  })

  it('ignores a keydown that is not Escape', () => {
    const { nav, lang } = masthead()
    soleOpen(nav, 'details.langpick')
    openBy(lang)

    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'a', bubbles: true }))

    expect(lang.open).toBe(true)
  })

  // The theme picker is server-rendered empty and filled by its island after
  // this runs, so the set cannot be captured once at wiring time.
  it('governs a picker added after it was wired', () => {
    const { nav, lang } = masthead()
    soleOpen(nav, 'details.langpick')

    const late = document.createElement('details')
    late.className = 'langpick'
    late.appendChild(document.createElement('summary'))
    nav.appendChild(late)

    // Opened first and closed second: a set captured at wiring time would not
    // contain `late`, so it is closing `late` that proves the set is re-queried.
    openBy(late)
    openBy(lang)

    expect(late.open).toBe(false)
    expect(lang.open).toBe(true)
  })

  it('does nothing without a root', () => {
    expect(() => soleOpen(null)).not.toThrow()
  })
})
