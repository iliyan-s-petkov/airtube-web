// The open sensor's readings as a sheet inside the fullscreen frame, where the
// panel under the map cannot be seen. The panel's own .gauges node is moved in
// and put back on exit, the same move-not-duplicate idiom as the legend.
import { tick } from 'svelte'

const TITLE_ID = 'map-sensor-sheet-title'

export function createSensorSheet(frame, { closeLabel = '', historyLabel = '', exitFull = () => {} } = {}) {
  const doc = frame.ownerDocument
  let full = false
  let gauges = null
  let marker = null
  let returnTo = null
  let scrollOnExit = false
  let onClose = () => {}

  const el = doc.createElement('div')
  el.className = 'map-sensor-sheet'
  el.setAttribute('role', 'dialog')
  el.setAttribute('aria-labelledby', TITLE_ID)
  el.tabIndex = -1

  const head = doc.createElement('div')
  head.className = 'map-sensor-sheet__head'
  const title = doc.createElement('h2')
  title.id = TITLE_ID
  title.className = 'map-sensor-sheet__title'
  const close = doc.createElement('button')
  close.type = 'button'
  close.className = 'map-sensor-sheet__close'
  close.setAttribute('aria-label', closeLabel)
  close.title = closeLabel
  const svg = doc.createElementNS('http://www.w3.org/2000/svg', 'svg')
  svg.setAttribute('class', 'map-sensor-sheet__close-ico')
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('aria-hidden', 'true')
  const path = doc.createElementNS('http://www.w3.org/2000/svg', 'path')
  path.setAttribute('d', 'M3.5 3.5l9 9M12.5 3.5l-9 9')
  path.setAttribute('fill', 'none')
  path.setAttribute('stroke', 'currentColor')
  path.setAttribute('stroke-width', '1.5')
  svg.appendChild(path)
  close.appendChild(svg)
  head.append(title, close)
  el.appendChild(head)

  const body = doc.createElement('div')
  body.className = 'map-sensor-sheet__body'
  el.appendChild(body)

  // A button, not an <a href="#…">: a fragment link would rewrite the hash the view state lives in.
  let history = null
  if (historyLabel) {
    history = doc.createElement('button')
    history.type = 'button'
    history.className = 'map-sensor-sheet__history'
    history.textContent = historyLabel
    el.appendChild(history)
  }

  const panel = () => doc.querySelector('[data-island="panel"] .sensor-panel')

  const mounted = () => el.parentNode === frame

  // Puts the gauges back before the comment that marks where they came from.
  function restoreGauges() {
    if (marker && gauges) marker.replaceWith(gauges)
    marker = null
    gauges = null
  }

  function unmount({ focus = true } = {}) {
    if (!mounted()) return
    restoreGauges()
    el.remove()
    const back = returnTo
    returnTo = null
    if (focus && back?.isConnected && typeof back.focus === 'function') back.focus({ preventScroll: true })
  }

  // Idempotent: reads the panel as it is now and mounts, updates or unmounts.
  function sync() {
    const p = panel()
    const next = p?.querySelector('.gauges') ?? null
    if (!full || !p || !next || frame.contains(p)) {
      unmount()
      return
    }
    title.textContent = p.querySelector('h2')?.textContent ?? ''
    if (next !== gauges) {
      restoreGauges()
      marker = doc.createComment('gauges')
      next.before(marker)
      gauges = next
      body.appendChild(next)
    }
    if (!mounted()) {
      returnTo = doc.activeElement
      frame.appendChild(el)
      el.focus({ preventScroll: true })
    }
  }

  function dismiss() {
    unmount()
    onClose()
  }

  close.addEventListener('click', dismiss)
  history?.addEventListener('click', () => {
    scrollOnExit = true
    exitFull()
  })

  // Capture phase, so it runs before mountFullscreen's faux-full Escape handler on the document.
  doc.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || !mounted()) return
    e.stopPropagation()
    e.preventDefault()
    dismiss()
  }, true)

  return {
    el,
    sync,
    setFull(on) {
      full = on
      sync()
      if (on || !scrollOnExit) return
      scrollOnExit = false
      const p = panel()
      if (!p) return
      // After the frame has left fullscreen and the page has its layout back.
      const go = () => {
        p.scrollIntoView({ block: 'start' })
        p.focus?.({ preventScroll: true })
      }
      if (typeof requestAnimationFrame === 'function') requestAnimationFrame(go)
      else go()
    },
    // Re-syncs after the panel has rendered the sensor the view state names.
    follow(vs, findSensor) {
      onClose = () => vs.closeSensor()
      return $effect.root(() => {
        $effect(() => {
          findSensor(vs.sensorId)
          tick().then(sync)
        })
      })
    },
  }
}
