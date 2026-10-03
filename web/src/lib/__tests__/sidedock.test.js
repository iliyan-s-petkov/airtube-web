// @vitest-environment jsdom
// The desktop bottom panel (OpenProject #684): mountChrome, the real panel island and the shared view state,
// wired as islands/map.js wires them, with a matchMedia whose width the test controls.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('uplot', () => ({ default: vi.fn(function () { this.setSize = vi.fn() }) }))

import { tick } from 'svelte'
import { mountChrome } from '../chrome.js'
import { readConfig } from '../mapconfig.js'
import { mount as mountPanel } from '../../islands/panel.js'
import { setSensors, findSensor } from '../sensors.svelte.js'
import { getViewState, resetViewStateForTests } from '../viewstate.svelte.js'

const BODY = {
  sensors: {
    id: [101, 102], quality: ['ok', 'ok'], station: [101, 102],
    measures: [['P1', 'P2', 'temperature', 'humidity'], ['P1', 'P2', 'temperature', 'humidity']],
    P1: [31, 12], P2: [18, 7], temperature: [21, 19], humidity: [55, 60],
  },
}

// matchMedia that answers the dock's width query from `wide` and lets a test flip it.
function stubViewport(wide) {
  const listeners = new Set()
  const state = { wide }
  vi.stubGlobal('matchMedia', (q) => ({
    get matches() { return q.includes('min-width: 1024px') ? state.wide : false },
    media: q,
    addEventListener: (_t, fn) => { if (q.includes('min-width: 1024px')) listeners.add(fn) },
    removeEventListener: (_t, fn) => listeners.delete(fn),
  }))
  state.set = (on) => { state.wide = on; listeners.forEach((fn) => fn({ matches: on })) }
  return state
}

// docked: the home page's host, which app.css hides from 1024px (only the dock shows the sensor there).
function page({ docked = false } = {}) {
  const shell = document.createElement('div')
  shell.className = 'map-shell'
  const el = document.createElement('div')
  el.className = 'map'
  Object.assign(el.dataset, {
    metric: 'P2', metrics: 'P1,P2,temperature,humidity',
    tClose: 'Close', tSheetHistory: 'Full history below',
    tPanelHistory: 'Full history & nearby sensors', tPanelHistoryShort: 'Full history',
    tPanelFold: 'Fold', tPanelExpand: 'Expand',
  })
  const canvas = document.createElement('canvas')
  canvas.tabIndex = 0
  el.appendChild(canvas)
  shell.appendChild(el)

  const host = document.createElement('div')
  host.dataset.island = 'panel'
  Object.assign(host.dataset, {
    metrics: 'P1,P2,temperature,humidity',
    metricLabels: 'PM10,PM2.5,Temperature,Humidity',
    metric: 'P2', period: '24h', periods: '24h,7d,30d,1y', periodLabels: '24 hours,7 days,30 days,1 year',
    periodShortLabels: '24h,7d,30d,1y', tTitle: 'Sensor', tClose: 'Close', tNoValue: 'no data',
    tChartMetricLegend: 'Metric', tChartPeriodLegend: 'Period', tPeriodCustom: 'Custom range',
    tPeriodFrom: 'From', tPeriodTo: 'To', tPeriodNow: 'Now', tChartReset: 'Reset chart',
    tMore: 'More actions', tShare: 'Share', tEmbed: 'Embed', tShareDone: 'Link copied',
    tEmbedDone: 'Embed code copied', tCopyFailed: 'Could not copy', tDetails: 'About this station',
    tNearbyLegend: 'Nearby sensors', tNearbyOff: 'off', tNearbySingleOnly: 'One metric only',
    tNearbyLow: 'Lowest', tNearbyMedian: 'Median', tNearbyHigh: 'Highest',
  })
  if (docked) host.classList.add('place-host', 'place-host--docked')
  document.body.append(shell, host)

  const chrome = mountChrome(el, readConfig(el))
  mountPanel(host)
  const vs = getViewState({ metrics: ['P1', 'P2', 'temperature', 'humidity'], defaultMetric: 'P2' })
  const stop = chrome.dock.follow(vs, findSensor)
  const stopSheet = chrome.sheet.follow(vs, findSensor)
  return { shell, el, host, canvas, vs, dock: chrome.dock, stop: () => { stop(); stopSheet() }, full: el.querySelector('.map__full') }
}

const settle = async () => { await tick(); await tick(); await Promise.resolve() }

describe('the desktop bottom panel', () => {
  let ctx
  let vp
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})))
    const store = new Map()
    vi.stubGlobal('localStorage', {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    })
    Element.prototype.scrollIntoView = vi.fn()
    resetViewStateForTests()
    history.replaceState(null, '', '/')
    setSensors(BODY)
  })
  afterEach(() => {
    ctx?.stop()
    resetViewStateForTests()
    setSensors(null)
    document.body.replaceChildren()
    document.body.className = ''
    vi.unstubAllGlobals()
  })

  it('docks the open sensor s title and own gauges when the viewport is wide', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()

    const dock = ctx.el.querySelector('.map-dock')
    expect(dock, 'no dock inside the map frame').toBeTruthy()
    expect(dock.querySelector('h2').textContent).toBe('Sensor 101')
    expect(dock.querySelectorAll('.gauge')).toHaveLength(4)
    expect(ctx.host.querySelector('.sensor-panel .gauges'), 'the gauges were copied, not moved').toBeNull()
    expect(ctx.shell.classList.contains('map-shell--docked')).toBe(true)
  })

  it('does not dock on a narrow viewport', async () => {
    vp = stubViewport(false)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    expect(document.querySelector('.map-dock')).toBeNull()
    expect(ctx.host.querySelector('.sensor-panel .gauges')).toBeTruthy()
    expect(ctx.shell.classList.contains('map-shell--docked')).toBe(false)
  })

  it('returns the gauges to their original place when the viewport narrows, and docks again when it widens', async () => {
    vp = stubViewport(false)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const panel = ctx.host.querySelector('.sensor-panel')
    const gauges = panel.querySelector('.gauges')
    const before = gauges.previousElementSibling

    vp.set(true)
    await settle()
    expect(ctx.el.querySelector('.map-dock .gauges')).toBe(gauges)

    vp.set(false)
    await settle()
    expect(ctx.el.querySelector('.map-dock')).toBeNull()
    expect(panel.querySelector('.gauges')).toBe(gauges)
    expect(gauges.previousElementSibling).toBe(before)
    expect(document.querySelectorAll('.gauges')).toHaveLength(1)
    expect([...panel.childNodes].some((n) => n.nodeType === Node.COMMENT_NODE && n.data === 'gauges')).toBe(false)
    expect(ctx.shell.classList.contains('map-shell--docked')).toBe(false)

    vp.set(true)
    await settle()
    expect(ctx.el.querySelectorAll('.map-dock')).toHaveLength(1)
    expect(document.querySelectorAll('.gauges')).toHaveLength(1)
  })

  it('swaps the title in place for another sensor and keeps one dock', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    ctx.vs.openSensor(102)
    await settle()
    expect(ctx.el.querySelectorAll('.map-dock')).toHaveLength(1)
    expect(ctx.el.querySelector('.map-dock h2').textContent).toBe('Sensor 102')
    expect(ctx.el.querySelectorAll('.map-dock .gauge')).toHaveLength(4)
    expect(document.querySelectorAll('.gauges')).toHaveLength(1)
    expect(ctx.el.querySelectorAll('.panel-chart__dock'), 'the old sensor chart was left in the panel').toHaveLength(1)
  })

  it('goes away when the sensor closes', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    ctx.vs.closeSensor()
    await settle()
    expect(ctx.el.querySelector('.map-dock')).toBeNull()
    expect(ctx.shell.classList.contains('map-shell--docked')).toBe(false)
  })

  it('stands down while fullscreen and comes back on exit with one set of gauges', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    ctx.full.click()
    await settle()
    expect(ctx.el.querySelector('.map-dock')).toBeNull()
    expect(ctx.el.querySelector('.map-sensor-sheet .gauges')).toBeTruthy()
    expect(document.querySelectorAll('.gauges')).toHaveLength(1)

    ctx.full.click()
    await settle()
    expect(ctx.el.querySelector('.map-sensor-sheet')).toBeNull()
    expect(ctx.el.querySelector('.map-dock .gauges')).toBeTruthy()
    expect(document.querySelectorAll('.gauges')).toHaveLength(1)
  })

  it('the close button clears the open sensor', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const close = ctx.el.querySelector('.map-dock button[aria-label="Close"]')
    expect(close, 'no named close button').toBeTruthy()
    close.click()
    await settle()
    expect(ctx.vs.sensorId).toBeNull()
    expect(ctx.el.querySelector('.map-dock')).toBeNull()
  })

  it('Escape closes the dock and returns focus to the map', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.canvas.focus()
    ctx.vs.openSensor(101)
    await settle()
    expect(ctx.el.querySelector('.map-dock').contains(document.activeElement), 'focus did not move into the dock').toBe(true)
    document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await settle()
    expect(ctx.vs.sensorId).toBeNull()
    expect(document.activeElement).toBe(ctx.canvas)
  })

  it('moves the panel info button into the dock header, opens the station sheet and leaves none behind', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const info = ctx.el.querySelector('.map-dock__head .panel-info')
    expect(info, 'no info button in the dock header').toBeTruthy()
    expect(ctx.host.querySelector('.sensor-panel .panel-info'), 'the info button was copied, not moved').toBeNull()
    info.click()
    await settle()
    expect(document.querySelector('.about-sheet'), 'the station sheet did not open').toBeTruthy()
    document.querySelector('.about-sheet__close').click()
    await settle()
    ctx.el.querySelector('.map-dock__close').click()
    await settle()
    expect(ctx.el.querySelector('.panel-info'), 'the info button was left in the map frame').toBeNull()
  })

  it('Escape with the station sheet open closes the sheet and keeps the dock', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    ctx.el.querySelector('.map-dock__head .panel-info').click()
    await settle()
    document.querySelector('.about-sheet').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await settle()
    expect(document.querySelector('.about-sheet')).toBeNull()
    expect(ctx.el.querySelector('.map-dock'), 'the dock closed with the sheet').toBeTruthy()
    expect(ctx.vs.sensorId).toBe(101)
  })

  it('the history button scrolls the card under the map into view', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const more = ctx.el.querySelector('.map-dock__more')
    expect(more.hidden).toBe(false)
    expect(more.textContent).toBe('Full history & nearby sensors')
    expect(more.querySelector('svg'), 'no icon on the button').toBeTruthy()
    more.click()
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled()
    expect(ctx.vs.sensorId).toBe(101)
  })

  it('moves a second chart into the panel and leaves the one under the map', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const dock = ctx.el.querySelector('.map-dock')
    expect(dock.querySelectorAll('.panel-chart__dock .chart-frame')).toHaveLength(1)
    expect(ctx.host.querySelectorAll('.panel-chart__dock'), 'the panel chart was copied, not moved').toHaveLength(0)
    expect(ctx.host.querySelectorAll('.chart-frame'), 'the section under the map lost its chart').toHaveLength(1)
  })

  it('charts the metric of the selected gauge', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const plot = ctx.el.querySelector('.panel-chart__dock')
    expect(plot.dataset.metric).toBe('P2')
    const other = [...ctx.el.querySelectorAll('.map-dock .gauge')].find((g) => g.getAttribute('aria-pressed') === 'false')
    other.click()
    await settle()
    expect(plot.dataset.metric).not.toBe('P2')
    expect(ctx.el.querySelectorAll('.map-dock .gauge[aria-pressed="true"]')).toHaveLength(1)
  })

  // OpenProject #697 PR B: the dock carries every chart control of the section under the map.
  it('carries the section s chart controls: metric, period, reset and share', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const dock = ctx.el.querySelector('.map-dock')
    expect(dock.querySelector('#dock-metric-menu'), 'no metric menu in the dock').toBeTruthy()
    expect(dock.querySelector('#dock-period-select'), 'no period select in the dock').toBeTruthy()
    expect(dock.querySelector('.chart-reset'), 'no reset in the dock').toBeTruthy()
    expect(dock.querySelector('.panel-more'), 'no share menu in the dock').toBeTruthy()
    const values = (sel) => [...document.querySelectorAll(`${sel} option`)].map((o) => o.value)
    expect(values('#dock-period-select')).toEqual(values('#panel-period-select'))
    expect(values('#dock-period-select')).toContain('custom')
    // No area loaded: the section offers no nearby sensors, so neither does the dock.
    expect(ctx.el.querySelector('#dock-nearby-menu')).toBeNull()
    expect(ctx.host.querySelector('#panel-nearby-menu')).toBeNull()
  })

  it('offers nearby sensors in the dock only where the section does, and only for one metric', async () => {
    setSensors(BODY, 'sofia')
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const nearby = ctx.el.querySelector('.map-dock #dock-nearby-menu')
    expect(nearby, 'no nearby menu in the dock').toBeTruthy()
    expect(nearby.disabled).toBe(false)
    ctx.el.querySelector('#dock-metric-menu').click()
    await settle()
    ctx.el.querySelector('.map-dock input[name="dock-metric"][value="P1"]').click()
    await settle()
    expect(nearby.disabled, 'nearby stayed on with two metrics').toBe(true)
  })

  it('a period picked in the dock drives the panel chart and the section', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const plot = ctx.el.querySelector('.panel-chart__dock')
    expect(plot.dataset.period).toBe('24h')
    const select = ctx.el.querySelector('#dock-period-select')
    select.value = '7d'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await settle()
    expect(plot.dataset.period).toBe('7d')
    expect(ctx.host.querySelector('#panel-period-select').value).toBe('7d')
  })

  it('a custom range in the dock gives from and to fields and a Now button', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const select = ctx.el.querySelector('#dock-period-select')
    select.value = 'custom'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await settle()
    const fields = ctx.el.querySelectorAll('.map-dock .chart-range input[type="datetime-local"]')
    expect(fields).toHaveLength(2)
    ctx.el.querySelector('.map-dock .chart-range__now').click()
    await settle()
    expect(ctx.el.querySelector('#dock-period-to').value).not.toBe('')
  })

  it('metrics ticked in the dock draw together, and Reset there restores the opening view', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const plot = ctx.el.querySelector('.panel-chart__dock')
    ctx.el.querySelector('#dock-metric-menu').click()
    await settle()
    ctx.el.querySelector('.map-dock input[name="dock-metric"][value="P1"]').click()
    await settle()
    expect(plot.dataset.metric).toBe('P2,P1')
    const select = ctx.el.querySelector('#dock-period-select')
    select.value = '30d'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await settle()
    ctx.el.querySelector('.map-dock .chart-reset').click()
    await settle()
    expect(plot.dataset.metric).toBe('P2')
    expect(plot.dataset.period).toBe('24h')
  })

  it('copies the embed code from the dock menu and says so in the dock', async () => {
    vp = stubViewport(true)
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    ctx.el.querySelector('.map-dock .panel-more').click()
    await settle()
    const items = [...ctx.el.querySelectorAll('.map-dock .panel-menu [role="menuitem"]')].map((b) => b.textContent.trim())
    expect(items).toEqual(['Share', 'Embed'])
    ctx.el.querySelectorAll('.map-dock .panel-menu [role="menuitem"]')[1].click()
    await settle()
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('<iframe'))
    expect(ctx.el.querySelector('.map-dock [role="status"]').textContent).toBe('Embed code copied')
  })

  it('Escape with the dock metric menu open closes the menu first, then the dock', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const button = ctx.el.querySelector('#dock-metric-menu')
    button.focus()
    button.click()
    await settle()
    const menu = ctx.el.querySelector('#dock-metric-menu-panel')
    expect(menu.hidden).toBe(false)
    document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await settle()
    expect(menu.hidden, 'the menu stayed open').toBe(true)
    expect(ctx.el.querySelector('.map-dock'), 'Escape closed the dock under an open menu').toBeTruthy()
    expect(ctx.vs.sensorId).toBe(101)
    document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await settle()
    expect(ctx.el.querySelector('.map-dock')).toBeNull()
  })

  it('Escape with the dock share menu open closes the menu first, then the dock', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    ctx.el.querySelector('.map-dock .panel-more').click()
    await settle()
    expect(ctx.el.querySelector('.map-dock .panel-menu')).toBeTruthy()
    document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await settle()
    expect(ctx.el.querySelector('.map-dock .panel-menu'), 'the menu stayed open').toBeNull()
    expect(ctx.el.querySelector('.map-dock'), 'Escape closed the dock under an open menu').toBeTruthy()
    document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await settle()
    expect(ctx.el.querySelector('.map-dock')).toBeNull()
  })

  it('on the home page, whose section is hidden from 1024px, there is no history button', async () => {
    vp = stubViewport(true)
    ctx = page({ docked: true })
    ctx.vs.openSensor(101)
    await settle()
    expect(ctx.el.querySelector('.map-dock__more').hidden).toBe(true)
  })

  it('a short panel lays the gauges beside the chart and shortens the history link; a tall one goes back', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const dock = ctx.el.querySelector('.map-dock')
    const more = dock.querySelector('.map-dock__more')
    const long = more.textContent
    let height = 180
    Object.defineProperty(dock, 'clientHeight', { configurable: true, get: () => height })
    ctx.dock.measure()
    expect(dock.classList.contains('map-dock--short')).toBe(true)
    expect(more.textContent.length).toBeLessThan(long.length)
    // 1024x768 home: under the 24rem cap the gauge row would leave the chart no height.
    height = 330
    ctx.dock.measure()
    expect(dock.classList.contains('map-dock--short')).toBe(true)
    height = 384
    ctx.dock.measure()
    expect(dock.classList.contains('map-dock--short')).toBe(false)
    expect(more.textContent).toBe(long)
  })

  it('narrowing leaves exactly one chart, under the map', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    vp.set(false)
    await settle()
    expect(document.querySelectorAll('.panel-chart__dock')).toHaveLength(0)
    expect(document.querySelectorAll('.chart-frame')).toHaveLength(1)
    expect(ctx.host.querySelectorAll('.chart-frame')).toHaveLength(1)
  })

  it('folds and expands, names the button for what it will do, and remembers the choice', async () => {
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    const dock = ctx.el.querySelector('.map-dock')
    const fold = dock.querySelector('.map-dock__fold')
    expect(fold.getAttribute('aria-label')).toBe('Fold')
    expect(dock.classList.contains('map-dock--folded')).toBe(false)
    fold.click()
    await settle()
    expect(dock.classList.contains('map-dock--folded')).toBe(true)
    expect(fold.getAttribute('aria-label')).toBe('Expand')
    expect(dock.querySelector('.map-dock__more').textContent).toBe('Full history')
    expect(localStorage.getItem('kanarche:panel-folded')).toBe('true')
    expect(dock.querySelectorAll('.gauge')).toHaveLength(4)
    fold.click()
    expect(fold.getAttribute('aria-label')).toBe('Fold')
    expect(dock.querySelector('.map-dock__more').textContent).toBe('Full history & nearby sensors')
    expect(localStorage.getItem('kanarche:panel-folded')).toBe('false')
  })

  it('opens folded when the folded choice was saved', async () => {
    localStorage.setItem('kanarche:panel-folded', 'true')
    vp = stubViewport(true)
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    expect(ctx.el.querySelector('.map-dock').classList.contains('map-dock--folded')).toBe(true)
    expect(ctx.el.querySelector('.map-dock__fold').getAttribute('aria-label')).toBe('Expand')
  })

  it('reports the height it covers, and 0 once it is gone', async () => {
    vp = stubViewport(true)
    ctx = page()
    const seen = []
    ctx.dock.onLayout((h) => seen.push(h))
    const rect = (top, bottom) => () => ({ top, bottom, left: 0, right: 0, width: 0, height: bottom - top })
    ctx.el.getBoundingClientRect = rect(0, 600)
    ctx.vs.openSensor(101)
    await settle()
    const dock = ctx.el.querySelector('.map-dock')
    dock.getBoundingClientRect = rect(400, 592)
    ctx.dock.measure()
    expect(seen.at(-1)).toBe(200)
    ctx.vs.closeSensor()
    await settle()
    expect(seen.at(-1)).toBe(0)
  })
})
