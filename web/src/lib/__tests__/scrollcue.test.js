// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { scrollCue } from '../scrollcue.js'

function page() {
  document.body.innerHTML = `
    <a class="scroll-cue" href="#below-map"><span>More below the map</span></a>
    <div id="below-map">readouts</div>`
  const target = document.getElementById('below-map')
  target.scrollIntoView = vi.fn()
  return { cue: document.querySelector('.scroll-cue'), target }
}

const win = (reduce) => ({
  matchMedia: (q) => ({ matches: reduce && q === '(prefers-reduced-motion: reduce)' }),
})

describe('scrollCue', () => {
  beforeEach(() => { history.replaceState(null, '', '/#sensor=101') })

  it('scrolls #below-map into view smoothly and leaves the hash alone', () => {
    const { cue, target } = page()
    scrollCue(document, win(false))
    const ev = new MouseEvent('click', { bubbles: true, cancelable: true })
    cue.querySelector('span').dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    expect(target.scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    expect(location.hash).toBe('#sensor=101')
  })

  it('jumps without animation under prefers-reduced-motion', () => {
    const { cue, target } = page()
    scrollCue(document, win(true))
    cue.click()
    expect(target.scrollIntoView).toHaveBeenCalledWith({ behavior: 'auto', block: 'start' })
  })

  it('does nothing when there is no cue', () => {
    document.body.innerHTML = '<div id="below-map"></div>'
    expect(() => scrollCue(document, win(false))).not.toThrow()
  })
})
