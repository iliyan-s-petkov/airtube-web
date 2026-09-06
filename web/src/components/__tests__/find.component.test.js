// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount, tick } from 'svelte'
import AreaFind from '../AreaFind.svelte'

const areas = [
  { name: 'Варна', href: '/area/varna' },
  { name: 'Бургас', href: '/area/burgas' },
  { name: 'Велико Търново', href: '/area/veliko-tarnovo' },
]

let component
afterEach(() => { if (component) unmount(component) })

function render(props = {}) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(AreaFind, {
    target,
    props: {
      areas,
      label: 'Търсене на район',
      placeholder: 'Име на район',
      hint: 'Стрелки и Enter',
      empty: 'Няма район с това име',
      onpick: () => {},
      ...props,
    },
  })
  return target
}

const input = (t) => t.querySelector('input[role="combobox"]')
const opts = (t) => [...t.querySelectorAll('.combobox__opt')]
const names = (t) => opts(t).map((li) => li.textContent)

async function type(t, value) {
  const el = input(t)
  el.value = value
  el.dispatchEvent(new Event('input', { bubbles: true }))
  await tick()
}

async function key(t, k) {
  const e = new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true })
  input(t).dispatchEvent(e)
  await tick()
  return e
}

describe('AreaFind.svelte', () => {
  it('wears the kit\'s classes and labels its own field', () => {
    const t = render()
    expect(t.querySelector('.field.field--search.combobox.toolbar__find')).not.toBeNull()
    const label = t.querySelector('.field__label')
    expect(label.textContent).toBe('Търсене на район')
    expect(label.getAttribute('for')).toBe(input(t).id)
    expect(input(t).placeholder).toBe('Име на район')
  })

  // The ARIA 1.2 combobox: the input owns the listbox by id, and says so before
  // it is ever opened. A listbox nothing points at is a list a screen reader
  // never finds.
  it('declares the listbox it owns', () => {
    const t = render()
    const list = t.querySelector('ul.combobox__list')
    expect(input(t).getAttribute('aria-controls')).toBe(list.id)
    expect(list.getAttribute('role')).toBe('listbox')
    expect(input(t).getAttribute('aria-autocomplete')).toBe('list')
    expect(input(t).getAttribute('aria-expanded')).toBe('false')
    expect(list.hidden).toBe(true)
  })

  // The keyboard hint is announced and never painted: a sighted reader infers
  // arrow keys from the open list, a screen-reader user does not.
  it('describes the keyboard to assistive technology only', () => {
    const t = render()
    const hint = t.querySelector('.sr-only')
    expect(hint.textContent).toBe('Стрелки и Enter')
    expect(input(t).getAttribute('aria-describedby')).toBe(hint.id)
  })

  it('opens on focus with every area listed', async () => {
    const t = render()
    input(t).dispatchEvent(new Event('focus', { bubbles: true }))
    await tick()
    expect(input(t).getAttribute('aria-expanded')).toBe('true')
    expect(names(t)).toEqual(['Бургас', 'Варна', 'Велико Търново'])
  })

  it('narrows to what was typed and marks the matched run', async () => {
    const t = render()
    await type(t, 'търново')
    expect(names(t)).toEqual(['Велико Търново'])
    expect(opts(t)[0].querySelector('mark').textContent).toBe('Търново')
  })

  // An absence stated plainly, not an error: typing a name this network has no
  // area for is an ordinary thing to do.
  it('says so when nothing matches, without an option to pick', async () => {
    const t = render()
    await type(t, 'Лондон')
    expect(opts(t)).toEqual([])
    expect(t.querySelector('.combobox__empty').textContent).toBe('Няма район с това име')
  })

  // Focus stays in the input — the reader is still typing — so the active
  // option is pointed at rather than focused.
  it('moves a cursor with the arrow keys without moving focus', async () => {
    const t = render()
    input(t).dispatchEvent(new Event('focus', { bubbles: true }))
    await tick()
    await key(t, 'ArrowDown')
    expect(input(t).getAttribute('aria-activedescendant')).toBe(opts(t)[0].id)
    expect(opts(t)[0].getAttribute('aria-selected')).toBe('true')
    await key(t, 'ArrowDown')
    expect(input(t).getAttribute('aria-activedescendant')).toBe(opts(t)[1].id)
    expect(document.activeElement).not.toBe(opts(t)[1])
  })

  it('wraps at both ends and reaches them with Home and End', async () => {
    const t = render()
    input(t).dispatchEvent(new Event('focus', { bubbles: true }))
    await tick()
    await key(t, 'ArrowUp')
    expect(input(t).getAttribute('aria-activedescendant')).toBe(opts(t)[2].id)
    await key(t, 'ArrowDown')
    expect(input(t).getAttribute('aria-activedescendant')).toBe(opts(t)[0].id)
    await key(t, 'End')
    expect(input(t).getAttribute('aria-activedescendant')).toBe(opts(t)[2].id)
    await key(t, 'Home')
    expect(input(t).getAttribute('aria-activedescendant')).toBe(opts(t)[0].id)
  })

  it('goes to the area under the cursor on Enter', async () => {
    const onpick = vi.fn()
    const t = render({ onpick })
    input(t).dispatchEvent(new Event('focus', { bubbles: true }))
    await tick()
    await key(t, 'ArrowDown')
    await key(t, 'Enter')
    expect(onpick).toHaveBeenCalledWith('/area/burgas')
  })

  // A half-typed name must never navigate: Enter without a cursor acts only on
  // an unambiguous query.
  it('refuses Enter on an ambiguous query and takes the last one standing', async () => {
    const onpick = vi.fn()
    const t = render({ onpick })
    await type(t, 'в')
    await key(t, 'Enter')
    expect(onpick).not.toHaveBeenCalled()
    await type(t, 'вел')
    await key(t, 'Enter')
    expect(onpick).toHaveBeenCalledWith('/area/veliko-tarnovo')
  })

  // click fires after blur has already closed the list, so a mouse pick has to
  // be taken on mousedown or it lands on nothing.
  it('picks with the mouse on mousedown', async () => {
    const onpick = vi.fn()
    const t = render({ onpick })
    await type(t, 'варна')
    opts(t)[0].dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }))
    await tick()
    expect(onpick).toHaveBeenCalledWith('/area/varna')
    expect(input(t).value).toBe('Варна')
    expect(t.querySelector('ul.combobox__list').hidden).toBe(true)
  })

  // Two stages: one Escape that both closed the list and cleared the field
  // would throw away a query the reader only wanted to see past.
  it('closes on the first Escape and clears on the second', async () => {
    const t = render()
    await type(t, 'вар')
    expect(t.querySelector('ul.combobox__list').hidden).toBe(false)
    await key(t, 'Escape')
    expect(t.querySelector('ul.combobox__list').hidden).toBe(true)
    expect(input(t).value).toBe('вар')
    await key(t, 'Escape')
    expect(input(t).value).toBe('')
  })

  it('leaves Escape to the page once there is nothing left to undo', async () => {
    const t = render()
    const e = await key(t, 'Escape')
    expect(e.defaultPrevented).toBe(false)
  })
})
