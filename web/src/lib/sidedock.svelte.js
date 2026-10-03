// The open sensor as a panel along the bottom of the map on wide screens: title, gauges, a chart.
// The panel's own .gauges node is moved in and put back, the same move-not-duplicate idiom as sensorsheet.
// The chart is drawn a second time while the panel is open (see SensorChart); getJSON serves it from cache.
import { tick } from 'svelte'
import { createStarSlot } from './panelstar.js'
import { panelDock } from './paneldock.svelte.js'
import { sectionHidden } from './panelhost.js'
import { readFlag, writeFlag } from './storage.js'

const WIDE = '(min-width: 1024px)'
const TITLE_ID = 'map-dock-title'
const FOLD_KEY = 'kanarche:panel-folded'
const SHORT_PX = 352
const SVG_NS = 'http://www.w3.org/2000/svg'

function icon(doc, cls, d) {
  const svg = doc.createElementNS(SVG_NS, 'svg')
  svg.setAttribute('class', cls)
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('aria-hidden', 'true')
  const path = doc.createElementNS(SVG_NS, 'path')
  path.setAttribute('d', d)
  path.setAttribute('fill', 'none')
  path.setAttribute('stroke', 'currentColor')
  path.setAttribute('stroke-width', '1.5')
  path.setAttribute('stroke-linecap', 'round')
  path.setAttribute('stroke-linejoin', 'round')
  svg.appendChild(path)
  return svg
}

export function createSideDock(frame, { closeLabel = '', moreLabel = '', moreShortLabel = '', foldLabel = '', expandLabel = '' } = {}) {
  const doc = frame.ownerDocument
  const win = doc.defaultView
  const shell = frame.closest('.map-shell')
  // The embed page never docks: its card is not under the map either.
  const enabled = !doc.body.classList.contains('embed')
  const mq = win?.matchMedia?.(WIDE) ?? null
  let full = false
  let gauges = null
  let marker = null
  let returnTo = null
  let onClose = () => {}
  const starSlot = createStarSlot(doc)
  const infoSlot = createStarSlot(doc, '.panel-info')

  const el = doc.createElement('div')
  el.className = 'map-dock'
  el.setAttribute('role', 'group')
  el.setAttribute('aria-labelledby', TITLE_ID)
  el.tabIndex = -1

  const head = doc.createElement('div')
  head.className = 'map-dock__head'
  const title = doc.createElement('h2')
  title.id = TITLE_ID
  title.className = 'map-dock__title'
  const close = doc.createElement('button')
  close.type = 'button'
  close.className = 'map-dock__close'
  close.setAttribute('aria-label', closeLabel)
  close.title = closeLabel
  close.appendChild(icon(doc, 'map-dock__close-ico', 'M3.5 3.5l9 9M12.5 3.5l-9 9'))

  // Named for what it does next, like the fullscreen button; the chevron points the way it will move.
  const fold = doc.createElement('button')
  fold.type = 'button'
  fold.className = 'map-dock__fold'
  fold.appendChild(icon(doc, 'map-dock__fold-ico', 'M3.5 6l4.5 4.5L12.5 6'))
  head.append(title, fold, close)
  el.appendChild(head)

  const body = doc.createElement('div')
  body.className = 'map-dock__body'
  el.appendChild(body)

  // A button, not an <a href="#…">: a fragment link would rewrite the hash the view state lives in.
  const more = doc.createElement('button')
  more.type = 'button'
  more.className = 'map-dock__more'
  more.appendChild(icon(doc, 'map-dock__more-ico', 'M8 3v9M4 8.5l4 4 4-4'))
  const moreText = doc.createElement('span')
  more.appendChild(moreText)
  if (moreLabel) el.appendChild(more)

  let folded = readFlag(FOLD_KEY, false)
  let short = false
  function paintFold() {
    el.classList.toggle('map-dock--folded', folded)
    el.classList.toggle('map-dock--short', short)
    const label = folded ? expandLabel : foldLabel
    fold.setAttribute('aria-label', label)
    fold.title = label
    moreText.textContent = (folded || short) && moreShortLabel ? moreShortLabel : moreLabel
  }
  paintFold()

  const panel = () => doc.querySelector('[data-island="panel"] .sensor-panel')
  const mounted = () => el.parentNode === frame

  function restoreGauges() {
    if (marker && gauges) marker.replaceWith(gauges)
    marker = null
    gauges = null
  }

  // How much of the map's bottom the panel covers, inset included: the controls and the camera clear this much.
  const listeners = new Set()
  let lastHeight = -1
  function measure() {
    const h = mounted() ? Math.max(0, Math.round(frame.getBoundingClientRect().bottom - el.getBoundingClientRect().top)) : 0
    // Under its 24rem cap (1024px, area pages) the open panel has too little height for a gauge row above the chart.
    if (mounted() && !folded && el.clientHeight > 0 && (el.clientHeight < SHORT_PX) !== short) {
      short = !short
      paintFold()
    }
    if (h !== lastHeight) {
      if (h > 0) shell?.style.setProperty('--map-panel-h', `${h}px`)
      else shell?.style.removeProperty('--map-panel-h')
      lastHeight = h
    }
    listeners.forEach((fn) => fn(h))
  }
  const watcher = typeof win?.ResizeObserver === 'function' ? new win.ResizeObserver(measure) : null

  // The chart's panel copy is rendered by SensorChart under the map, then moved here. Svelte removes it
  // from anywhere when only its own flag goes, but not when a whole chart is torn down, so stale ones are dropped here.
  const chartNodes = () => [...el.children].filter((n) => n.classList.contains('panel-chart__dock'))
  function adoptChart() {
    if (!mounted()) return
    const node = panel()?.querySelector('.panel-chart__dock')
    if (node && node.parentNode !== el) el.insertBefore(node, more.parentNode === el ? more : null)
    if (node) chartNodes().filter((n) => n !== node).forEach((n) => n.remove())
    measure()
  }

  function unmount() {
    if (!mounted()) return
    restoreGauges()
    starSlot.release()
    infoSlot.release()
    panelDock.on = false
    chartNodes().forEach((n) => n.remove())
    watcher?.disconnect()
    el.remove()
    shell?.classList.remove('map-shell--docked')
    measure()
    const back = returnTo
    returnTo = null
    if (back?.isConnected && back !== doc.body && typeof back.focus === 'function') back.focus({ preventScroll: true })
  }

  // Idempotent: reads the panel as it is now and mounts, updates or unmounts.
  function sync() {
    const p = panel()
    // Gauges already moved out of this panel are its own, not a panel without them.
    const own = gauges && body.contains(gauges) && p?.contains(marker) ? gauges : null
    const next = p?.querySelector('.gauges') ?? own
    if (!enabled || full || !mq?.matches || !p || !next) {
      unmount()
      return
    }
    title.textContent = p.querySelector('h2')?.textContent ?? ''
    // No way down to a section that is not shown.
    more.hidden = sectionHidden(win, p)
    if (next !== gauges) {
      restoreGauges()
      marker = doc.createComment('gauges')
      next.before(marker)
      gauges = next
      body.appendChild(next)
    }
    starSlot.take(p, head, fold)
    infoSlot.take(p, head, fold)
    if (!mounted()) {
      returnTo = doc.activeElement
      frame.appendChild(el)
      shell?.classList.add('map-shell--docked')
      watcher?.observe(el)
      panelDock.on = true
      el.focus({ preventScroll: true })
    }
    adoptChart()
    // The chart node exists only after Svelte has rendered the flag set above.
    tick().then(adoptChart)
  }

  function dismiss() {
    unmount()
    onClose()
  }

  close.addEventListener('click', dismiss)
  fold.addEventListener('click', () => {
    folded = !folded
    writeFlag(FOLD_KEY, folded)
    paintFold()
    measure()
  })
  more.addEventListener('click', () => panel()?.scrollIntoView({ block: 'start', behavior: 'smooth' }))
  mq?.addEventListener?.('change', sync)

  // Capture phase, like the sheet, so a faux-fullscreen Escape handler never sees a dock Escape.
  doc.addEventListener('keydown', (e) => {
    // The station sheet opened from the info button owns its own Escape.
    if (e.key !== 'Escape' || !mounted() || !el.isConnected || doc.querySelector('.about-sheet')) return
    // An open menu in the panel closes first; its own handler takes this Escape.
    if (el.querySelector('.colmenu__panel:not([hidden]), .panel-menu')) return
    e.stopPropagation()
    e.preventDefault()
    dismiss()
  }, true)

  return {
    el,
    sync,
    measure,
    // fn(height) on every size change and on close (0); returns the unsubscribe.
    onLayout(fn) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    setFull(on) {
      full = on
      sync()
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
