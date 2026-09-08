// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import RefreshButton from '../RefreshButton.svelte'
import DataFreshness from '../DataFreshness.svelte'

let component
afterEach(() => {
  if (component) unmount(component)
  component = null
  document.body.innerHTML = ''
})

function render(Component, props) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(Component, { target, props })
  return target
}

describe('RefreshButton', () => {
  it('names the action in words, not only in a glyph', () => {
    const el = render(RefreshButton, { label: 'Обнови' })
    const btn = el.querySelector('button')
    expect(btn.textContent.trim()).toBe('Обнови')
    expect(btn.type).toBe('button')
    // The icon is decoration beside the name, so it must not be announced.
    expect(el.querySelector('svg').getAttribute('aria-hidden')).toBe('true')
  })

  // The kit gives this control three homes and one behaviour. The variant picks
  // the dress; an unknown one must still render a usable button rather than a
  // class-less one.
  it('wears the toolbar dress by default and the others on request', () => {
    const toolbar = render(RefreshButton, { label: 'x' })
    expect(toolbar.querySelector('button').className).toBe('btn btn--secondary toolbar__refresh')
    unmount(component)
    const line = render(RefreshButton, { label: 'x', variant: 'line' })
    expect(line.querySelector('button').className).toBe('btn btn--ghost btn--compact data-refresh__btn')
    unmount(component)
    const icon = render(RefreshButton, { label: 'x', variant: 'icon' })
    expect(icon.querySelector('button').className).toBe('data-refresh__btn data-refresh__btn--icon')
    unmount(component)
    const junk = render(RefreshButton, { label: 'x', variant: 'nonsense' })
    expect(junk.querySelector('button').className).toBe('btn btn--secondary toolbar__refresh')
  })

  // Dropping the word silently would leave a button with no name at all.
  it('keeps its name when the word is dropped for the map', () => {
    const el = render(RefreshButton, { label: 'Обнови', variant: 'icon' })
    const btn = el.querySelector('button')
    expect(btn.textContent.trim()).toBe('')
    expect(btn.getAttribute('aria-label')).toBe('Обнови')
    expect(btn.title).toBe('Обнови')
  })

  // A title beside the visible word is a tooltip repeating what is on screen.
  it('adds no tooltip where the word is present', () => {
    const el = render(RefreshButton, { label: 'Обнови', variant: 'line' })
    expect(el.querySelector('button').hasAttribute('title')).toBe(false)
    expect(el.querySelector('button').hasAttribute('aria-label')).toBe(false)
  })

  // aria-busy rather than `disabled`: a disabled button loses focus to the
  // body, dropping a keyboard reader out of the toolbar mid-request.
  it('marks itself busy without going unfocusable', () => {
    const el = render(RefreshButton, { label: 'x', busy: true })
    const btn = el.querySelector('button')
    expect(btn.getAttribute('aria-busy')).toBe('true')
    expect(btn.disabled).toBe(false)
  })

  it('asks for a refresh when clicked', () => {
    let asked = 0
    const el = render(RefreshButton, { label: 'x', onrefresh: () => { asked += 1 } })
    el.querySelector('button').click()
    flushSync()
    expect(asked).toBe(1)
  })
})

describe('DataFreshness', () => {
  const base = { status: '', auto: true, autoLabel: 'Автоматично обновяване', onauto: () => {} }

  // Clipped, never hidden: display:none would silence the announcement.
  it('carries a live region even before it has anything to report', () => {
    const el = render(DataFreshness, { ...base })
    const status = el.querySelector('.data-refresh__status')
    expect(status).not.toBe(null)
    expect(status.getAttribute('role')).toBe('status')
    expect(status.classList.contains('sr-only')).toBe(true)
    expect(status.textContent).toBe('')
  })

  it('shows whatever the store says', () => {
    const el = render(DataFreshness, { ...base, status: 'Данни от 14:07' })
    expect(el.querySelector('.data-refresh__status').textContent).toBe('Данни от 14:07')
  })

  it('puts the time and the switch name where a hover reaches them', () => {
    const el = render(DataFreshness, { ...base, status: 'Данни от 14:07' })
    const sw = el.querySelector('.data-refresh__auto')
    expect(sw.title).toBe('Автоматично обновяване · Данни от 14:07')
    expect(sw.getAttribute('aria-label')).toBe(sw.title)
  })

  // A dangling "·" is the tell that the empty status was interpolated anyway.
  it('states the name alone when there is no reading to date', () => {
    const el = render(DataFreshness, { ...base })
    expect(el.querySelector('.data-refresh__auto').title).toBe('Автоматично обновяване')
  })

  // The button is inside the line on an area page and in the toolbar on the
  // home page — one flag, so the same partial serves both.
  it('holds the button only where the page asks for it', () => {
    const without = render(DataFreshness, { ...base })
    expect(without.querySelector('.data-refresh__btn')).toBe(null)
    unmount(component)
    const withBtn = render(DataFreshness, { ...base, button: true, buttonLabel: 'Обнови' })
    const btn = withBtn.querySelector('.data-refresh__btn')
    expect(btn.textContent.trim()).toBe('')
    expect(btn.getAttribute('aria-label')).toBe('Обнови')
  })

  it('reflects and reports the auto-refresh choice', () => {
    const seen = []
    const el = render(DataFreshness, { ...base, auto: false, onauto: (on) => seen.push(on) })
    const sw = el.querySelector('.data-refresh__auto')
    // With the words gone, aria-checked is the whole of the state.
    expect(sw.getAttribute('role')).toBe('switch')
    expect(sw.getAttribute('aria-checked')).toBe('false')
    sw.click()
    flushSync()
    expect(seen).toEqual([true])
  })

  // Colour alone would say nothing to a reader who cannot see the difference.
  it('draws the off state rather than only recolouring it', () => {
    const on = render(DataFreshness, { ...base, auto: true })
    const drawnOn = on.querySelectorAll('.data-refresh__auto path').length
    unmount(component)
    const off = render(DataFreshness, { ...base, auto: false })
    expect(off.querySelectorAll('.data-refresh__auto path').length).toBe(drawnOn + 1)
  })
})
