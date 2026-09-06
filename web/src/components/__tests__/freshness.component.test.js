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

  // The kit gives this control two homes and one behaviour. The variant picks
  // the dress; an unknown one must still render a usable button rather than a
  // class-less one.
  it('wears the toolbar dress by default and the line dress on request', () => {
    const toolbar = render(RefreshButton, { label: 'x' })
    expect(toolbar.querySelector('button').className).toBe('btn btn--secondary toolbar__refresh')
    unmount(component)
    const line = render(RefreshButton, { label: 'x', variant: 'line' })
    expect(line.querySelector('button').className).toBe('btn btn--ghost btn--compact data-refresh__btn')
    unmount(component)
    const junk = render(RefreshButton, { label: 'x', variant: 'nonsense' })
    expect(junk.querySelector('button').className).toBe('btn btn--secondary toolbar__refresh')
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

  // Always present, empty when there is nothing to say: a live region added to
  // the page at the moment it gets content announces nothing.
  it('carries a live region even before it has anything to report', () => {
    const el = render(DataFreshness, { ...base })
    const status = el.querySelector('.data-refresh__status')
    expect(status).not.toBe(null)
    expect(status.getAttribute('role')).toBe('status')
    expect(status.textContent).toBe('')
  })

  it('shows whatever the store says', () => {
    const el = render(DataFreshness, { ...base, status: 'Данни от 14:07' })
    expect(el.querySelector('.data-refresh__status').textContent).toBe('Данни от 14:07')
  })

  // The button is inside the line on an area page and in the toolbar on the
  // home page — one flag, so the same partial serves both.
  it('holds the button only where the page asks for it', () => {
    const without = render(DataFreshness, { ...base })
    expect(without.querySelector('button')).toBe(null)
    unmount(component)
    const withBtn = render(DataFreshness, { ...base, button: true, buttonLabel: 'Обнови' })
    expect(withBtn.querySelector('button.data-refresh__btn').textContent.trim()).toBe('Обнови')
  })

  it('reflects and reports the auto-refresh choice', () => {
    const seen = []
    const el = render(DataFreshness, { ...base, auto: false, onauto: (on) => seen.push(on) })
    const box = el.querySelector('input[type="checkbox"]')
    expect(box.checked).toBe(false)
    // The label wraps the input, so the text names the control without an id.
    expect(el.querySelector('label').textContent.trim()).toBe('Автоматично обновяване')
    box.checked = true
    box.dispatchEvent(new Event('change', { bubbles: true }))
    flushSync()
    expect(seen).toEqual([true])
  })
})
