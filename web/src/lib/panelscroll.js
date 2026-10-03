// Phones and tablets (below the dock's 1024px): brings the sensor card under the map into view on open and returns to the map on close.
// `panel` may be a getter, because the section only exists while a sensor is open.
import { sectionHidden } from './panelhost.js'

export function createPanelScroll({ win = window, panel, isFull = () => false }) {
  let returnY = null
  const mq = (q) => win.matchMedia?.(q).matches ?? false
  const behavior = () => (mq('(prefers-reduced-motion: reduce)') ? 'auto' : 'smooth')
  const phone = () => mq('(hover: none), (max-width: 1023px)')

  return {
    opened({ initial }) {
      const el = typeof panel === 'function' ? panel() : panel
      if (initial || !el || !phone() || isFull()) return
      // A touch screen from 1024px docks the sensor over the map; the home page hides this section then.
      if (sectionHidden(win, el)) return
      // Already in the upper part of the screen: a second tap must not jerk the page.
      const top = el.getBoundingClientRect().top
      if (top >= 0 && top < win.innerHeight * 0.6) return
      if (returnY === null) returnY = win.scrollY
      el.scrollIntoView({ behavior: behavior(), block: 'start' })
      el.focus?.({ preventScroll: true })
    },
    // True when it scrolled back, so the caller can hand focus to the map.
    closed() {
      if (returnY === null) return false
      const top = returnY
      returnY = null
      if (isFull()) return false
      win.scrollTo({ top, behavior: behavior() })
      return true
    },
  }
}
