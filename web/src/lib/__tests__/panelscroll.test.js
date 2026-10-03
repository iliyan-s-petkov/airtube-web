import { describe, it, expect, vi } from 'vitest'
import { createPanelScroll } from '../panelscroll.js'

function env({ phone = true, panelTop = 917, scrollY = 0, full = false, reduce = false, narrow = false, wide = false, docked = false } = {}) {
  const win = {
    scrollY, innerHeight: 873,
    matchMedia: (q) => ({
      matches: q.includes('min-width: 1024px') ? wide
        : q.includes('hover: none') || q.includes('max-width: 1023px') ? (phone || narrow) : q.includes('reduced-motion') ? reduce : false,
    }),
    scrollTo: vi.fn(),
  }
  const panel = {
    getBoundingClientRect: () => ({ top: panelTop }), scrollIntoView: vi.fn(), focus: vi.fn(),
    closest: (sel) => (docked && sel === '.place-host--docked' ? {} : null),
  }
  const e = { win, panel, isFull: () => e.full, full }
  return e
}

describe('panel scroll', () => {
  it('scrolls a below-the-fold panel into view on a user open', () => {
    const e = env(); const s = createPanelScroll(e)
    s.opened({ initial: false })
    expect(e.panel.scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    expect(e.panel.focus).toHaveBeenCalledWith({ preventScroll: true })
  })
  it('scrolls on a narrow non-touch window too, where the card is not docked', () => {
    const e = env({ phone: false, narrow: true }); const s = createPanelScroll(e)
    s.opened({ initial: false })
    expect(e.panel.scrollIntoView).toHaveBeenCalled()
  })
  it('scrolls a panel that sits above the viewport', () => {
    const e = env({ panelTop: -400 }); const s = createPanelScroll(e)
    s.opened({ initial: false })
    expect(e.panel.scrollIntoView).toHaveBeenCalled()
  })
  it('accepts the panel as a getter, since the section only exists while open', () => {
    const e = env(); const el = e.panel
    const s = createPanelScroll({ ...e, panel: () => el })
    s.opened({ initial: false })
    expect(el.scrollIntoView).toHaveBeenCalled()
  })
  it('does not scroll on the initial deep-link load', () => {
    const e = env(); const s = createPanelScroll(e)
    s.opened({ initial: true })
    expect(e.panel.scrollIntoView).not.toHaveBeenCalled()
  })
  it('does not scroll when the panel is already on screen', () => {
    const e = env({ panelTop: 300 }); const s = createPanelScroll(e)
    s.opened({ initial: false })
    expect(e.panel.scrollIntoView).not.toHaveBeenCalled()
  })
  it('does nothing on desktop or in fullscreen', () => {
    for (const e of [env({ phone: false }), env({ full: true })]) {
      createPanelScroll(e).opened({ initial: false })
      expect(e.panel.scrollIntoView).not.toHaveBeenCalled()
    }
  })
  // A touch tablet from 1024px on the home page: the sensor is in the map's panel and the section is hidden.
  it('does not scroll to a section the home page hides behind the map panel', () => {
    const e = env({ wide: true, docked: true }); const s = createPanelScroll(e)
    s.opened({ initial: false })
    expect(e.panel.scrollIntoView).not.toHaveBeenCalled()
    expect(s.closed()).toBe(false)
  })
  it('still scrolls on a wide touch screen where the section is shown', () => {
    const e = env({ wide: true }); const s = createPanelScroll(e)
    s.opened({ initial: false })
    expect(e.panel.scrollIntoView).toHaveBeenCalled()
  })
  it('uses an instant scroll under reduced motion', () => {
    const e = env({ reduce: true }); const s = createPanelScroll(e)
    s.opened({ initial: false }); e.win.scrollY = 700
    s.closed()
    expect(e.panel.scrollIntoView).toHaveBeenCalledWith({ behavior: 'auto', block: 'start' })
    expect(e.win.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'auto' })
  })
  it('returns to the remembered position on close after an auto-scroll', () => {
    const e = env({ scrollY: 120 }); const s = createPanelScroll(e)
    s.opened({ initial: false }); e.win.scrollY = 700
    expect(s.closed()).toBe(true)
    expect(e.win.scrollTo).toHaveBeenCalledWith({ top: 120, behavior: 'smooth' })
  })
  it('keeps the first position across a second open', () => {
    const e = env({ scrollY: 0 }); const s = createPanelScroll(e)
    s.opened({ initial: false }); e.win.scrollY = 700
    s.opened({ initial: false })
    s.closed()
    expect(e.win.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: 'smooth' })
  })
  it('does not move the page on close when it did not auto-scroll', () => {
    const e = env({ panelTop: 300 }); const s = createPanelScroll(e)
    s.opened({ initial: false })
    expect(s.closed()).toBe(false)
    expect(e.win.scrollTo).not.toHaveBeenCalled()
  })
  it('forgets the position once used', () => {
    const e = env(); const s = createPanelScroll(e)
    s.opened({ initial: false }); s.closed(); s.closed()
    expect(e.win.scrollTo).toHaveBeenCalledTimes(1)
  })
  it('does not scroll back while fullscreen', () => {
    const e = env(); const s = createPanelScroll(e)
    s.opened({ initial: false }); e.full = true
    expect(s.closed()).toBe(false)
    expect(e.win.scrollTo).not.toHaveBeenCalled()
  })
})
