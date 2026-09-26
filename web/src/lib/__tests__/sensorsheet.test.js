// @vitest-environment jsdom
// The fullscreen sensor sheet: mountChrome's fullscreen toggle, the real panel
// island and the shared view state, wired as islands/map.js wires them.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// panel.js pulls in uplot through the chart, which touches matchMedia on import.
vi.mock('uplot', () => ({ default: vi.fn(function () { this.setSize = vi.fn() }) }))

import { tick } from 'svelte'
import { mountChrome } from '../chrome.js'
import { readConfig } from '../mapconfig.js'
import { mount as mountPanel } from '../../islands/panel.js'
import { setSensors, findSensor } from '../sensors.svelte.js'
import { getViewState, resetViewStateForTests } from '../viewstate.svelte.js'

const BODY = {
  sensors: {
    id: [101], quality: ['ok'], station: [101],
    measures: [['P1', 'P2', 'temperature', 'humidity']],
    P1: [31], P2: [18], temperature: [21], humidity: [55],
  },
}

function page() {
  const shell = document.createElement('div')
  shell.className = 'map-shell'
  const el = document.createElement('div')
  el.className = 'map'
  Object.assign(el.dataset, {
    metric: 'P2', metrics: 'P1,P2,temperature,humidity',
    tClose: 'Close', tSheetHistory: 'Full history below',
  })
  // Stands in for the MapLibre canvas the reader tapped.
  const canvas = document.createElement('canvas')
  canvas.tabIndex = 0
  el.appendChild(canvas)
  shell.appendChild(el)

  const host = document.createElement('div')
  host.dataset.island = 'panel'
  Object.assign(host.dataset, {
    metrics: 'P1,P2,temperature,humidity',
    metricLabels: 'PM10,PM2.5,Temperature,Humidity',
    metric: 'P2', period: '24h', tTitle: 'Sensor', tClose: 'Close', tNoValue: 'no data',
  })
  document.body.append(shell, host)

  const chrome = mountChrome(el, readConfig(el))
  mountPanel(host)
  const vs = getViewState({ metrics: ['P1', 'P2', 'temperature', 'humidity'], defaultMetric: 'P2' })
  const stop = chrome.sheet.follow(vs, findSensor)
  return { el, host, canvas, vs, stop, full: el.querySelector('.map__full') }
}

// Two ticks: one for the panel to render, one for the sheet's own tick().then(sync).
const settle = async () => { await tick(); await tick(); await Promise.resolve() }
const press = (key) => document.activeElement.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }))

describe('the fullscreen sensor sheet', () => {
  let ctx
  beforeEach(() => {
    // The panel's chart asks for a series; nothing here needs it to answer.
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})))
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

  it('openSensor while fullscreen mounts the sheet with the panel s own gauges', async () => {
    ctx = page()
    ctx.full.click()
    expect(ctx.el.classList.contains('map--faux-full')).toBe(true)
    ctx.canvas.focus()
    ctx.vs.openSensor(101)
    await settle()

    const sheet = ctx.el.querySelector('.map-sensor-sheet')
    expect(sheet, 'no sensor sheet inside the fullscreen frame').toBeTruthy()
    expect(sheet.getAttribute('role')).toBe('dialog')
    const title = document.getElementById(sheet.getAttribute('aria-labelledby'))
    expect(title.textContent).toBe('Sensor 101')
    expect(sheet.querySelectorAll('.gauge')).toHaveLength(4)
    expect(ctx.host.querySelector('.sensor-panel .gauges'), 'the gauges were copied, not moved').toBeNull()
    expect(sheet.querySelector('.chart-host, canvas')).toBeNull()
  })

  it('exit restores the gauges to the panel and returns focus to the tapped cell', async () => {
    ctx = page()
    ctx.full.click()
    ctx.canvas.focus()
    ctx.vs.openSensor(101)
    await settle()
    const gauges = ctx.el.querySelector('.map-sensor-sheet .gauges')

    ctx.full.click()
    expect(ctx.el.querySelector('.map-sensor-sheet')).toBeNull()
    const panel = ctx.host.querySelector('.sensor-panel')
    expect(gauges.parentElement).toBe(panel)
    expect(panel.querySelector('.gauges')).toBe(gauges)
    expect([...panel.childNodes].some((n) => n.nodeType === Node.COMMENT_NODE && n.data === 'gauges')).toBe(false)
    expect(document.activeElement).toBe(ctx.canvas)
  })

  it('does not mount without fullscreen', async () => {
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    expect(document.querySelector('.map-sensor-sheet')).toBeNull()
    expect(ctx.host.querySelector('.sensor-panel .gauges')).toBeTruthy()
  })

  it('mounts when fullscreen starts with a sensor already open', async () => {
    ctx = page()
    ctx.vs.openSensor(101)
    await settle()
    ctx.full.click()
    expect(ctx.el.querySelector('.map-sensor-sheet .gauges')).toBeTruthy()
  })

  it('Escape closes the sheet first, then leaves fullscreen', async () => {
    ctx = page()
    ctx.full.click()
    ctx.canvas.focus()
    ctx.vs.openSensor(101)
    await settle()

    press('Escape')
    expect(ctx.el.querySelector('.map-sensor-sheet'), 'Escape left the sheet open').toBeNull()
    expect(ctx.el.classList.contains('map--faux-full'), 'Escape left fullscreen before closing the sheet').toBe(true)
    expect(ctx.vs.sensorId).toBeNull()
    expect(document.activeElement).toBe(ctx.canvas)

    press('Escape')
    expect(ctx.el.classList.contains('map--faux-full')).toBe(false)
  })

  it('the close button closes the sensor', async () => {
    ctx = page()
    ctx.full.click()
    ctx.vs.openSensor(101)
    await settle()
    ctx.el.querySelector('.map-sensor-sheet__close').click()
    expect(ctx.el.querySelector('.map-sensor-sheet')).toBeNull()
    expect(ctx.vs.sensorId).toBeNull()
  })

  it('Full history below leaves fullscreen and scrolls to the panel', async () => {
    ctx = page()
    ctx.full.click()
    ctx.vs.openSensor(101)
    await settle()
    const scrolled = vi.fn()
    ctx.host.querySelector('.sensor-panel').scrollIntoView = scrolled

    const link = ctx.el.querySelector('.map-sensor-sheet__history')
    expect(link.textContent).toBe('Full history below')
    link.click()
    await new Promise((r) => requestAnimationFrame(r))

    expect(ctx.el.classList.contains('map--faux-full')).toBe(false)
    expect(ctx.el.querySelector('.map-sensor-sheet')).toBeNull()
    expect(ctx.host.querySelector('.sensor-panel .gauges')).toBeTruthy()
    expect(scrolled).toHaveBeenCalled()
    expect(ctx.vs.sensorId).toBe(101)
  })
})
