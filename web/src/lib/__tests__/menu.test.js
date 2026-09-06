// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest'
import { closeOnEscape, closeOnOutside } from '../menu.js'

describe('closeOnOutside', () => {
  const panel = () => {
    const el = document.createElement('div')
    el.innerHTML = '<button>inside</button>'
    document.body.appendChild(el)
    return el
  }

  it('closes on a press that lands anywhere else', () => {
    const close = vi.fn()
    const off = closeOnOutside(panel(), close)
    document.body.dispatchEvent(new window.MouseEvent('mousedown', { bubbles: true }))
    expect(close).toHaveBeenCalled()
    off()
  })

  // A press on the menu's own contents is the reader USING it — a menu that
  // closed under its own buttons would be unusable with a mouse.
  it('leaves the menu open for a press inside it', () => {
    const close = vi.fn()
    const el = panel()
    const off = closeOnOutside(el, close)
    el.querySelector('button').dispatchEvent(new window.MouseEvent('mousedown', { bubbles: true }))
    expect(close).not.toHaveBeenCalled()
    off()
  })

  // mousedown, not click: click arrives after the pointer has already committed,
  // and by then the menu has been open over whatever was pressed.
  it('listens on the way down, not on the click', () => {
    const close = vi.fn()
    const off = closeOnOutside(panel(), close)
    document.body.dispatchEvent(new window.MouseEvent('click', { bubbles: true }))
    expect(close).not.toHaveBeenCalled()
    off()
  })

  // The listener is on the window and outlives the menu, so a caller that
  // cannot take it off leaves one behind on every open.
  it('hands back the way to remove itself', () => {
    const close = vi.fn()
    closeOnOutside(panel(), close)()
    document.body.dispatchEvent(new window.MouseEvent('mousedown', { bubbles: true }))
    expect(close).not.toHaveBeenCalled()
  })
})

describe('closeOnEscape', () => {
  const press = (key) => {
    const e = new window.KeyboardEvent('keydown', { key, cancelable: true })
    return e
  }

  it('closes an open menu', () => {
    const close = vi.fn()
    const e = press('Escape')
    closeOnEscape(() => true, close)(e)
    expect(close).toHaveBeenCalled()
    expect(e.defaultPrevented).toBe(true)
  })

  // Escape also clears the search box and closes the suggestion list. Swallowing
  // it while no menu is open would take those away.
  it('leaves Escape alone when no menu is open', () => {
    const close = vi.fn()
    const e = press('Escape')
    closeOnEscape(() => false, close)(e)
    expect(close).not.toHaveBeenCalled()
    expect(e.defaultPrevented).toBe(false)
  })

  it('ignores every other key', () => {
    const close = vi.fn()
    closeOnEscape(() => true, close)(press('Enter'))
    expect(close).not.toHaveBeenCalled()
  })
})
