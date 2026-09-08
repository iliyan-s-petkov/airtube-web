// @vitest-environment jsdom
import { describe, it, expect, afterEach, beforeEach } from 'vitest'
import { flushSync } from 'svelte'
import { mount, areaName } from '../readouts.svelte.js'
import { getViewState, resetViewStateForTests } from '../../lib/viewstate.svelte.js'
import { setSensors, setScales } from '../../lib/sensors.svelte.js'
import { setMapAreas } from '../../lib/mapareas.svelte.js'

const ATTRS = {
  island: 'readouts',
  metrics: 'P1,P2',
  metricLabels: 'ФПЧ10,ФПЧ2.5',
  metric: 'P2',
  tHigh: 'Максимум · {metric}',
  tLow: 'Минимум · {metric}',
  tMedian: 'Медиана · {metric}',
  tThisSensor: 'Този сензор',
  tOfTotal: 'от {total}',
  tAbove: 'Над медианата',
  tBelow: 'Под медианата',
  tAt: 'На медианата',
  tAreaSensors: '{area} · {total} сензора',
  tSensorsOnly: '{total} сензора',
}

const BODY = { sensors: { id: [1, 2, 3], station: [1, 2, 3], P2: [4, 12, 30] } }
const SCALES = [{ metric: 'P2', unit: 'µg/m³', ceiling: 50, bands: [{ upper: null, colour: '#a00' }] }]

let stop = null

beforeEach(() => {
  resetViewStateForTests()
  setSensors(null, null)
  setScales(null)
  setMapAreas(null)
})

afterEach(() => {
  if (stop) stop()
  stop = null
  document.body.innerHTML = ''
  resetViewStateForTests()
  setSensors(null, null)
})

// The server's own strip, exactly as base.gohtml writes it. The island must
// leave it in the DOM: closing the sensor has to restore it, not rebuild it.
function island() {
  const el = document.createElement('div')
  Object.assign(el.dataset, ATTRS)
  el.innerHTML = '<div class="readouts"><div class="readout card">national</div></div>'
  document.body.appendChild(el)
  stop = mount(el)
  flushSync()
  return el
}

const shown = (el) => [...el.querySelectorAll('.readouts')].filter((s) => !s.closest('[hidden]') && !s.hidden)

describe('readouts island', () => {
  it('leaves the server strip alone while no sensor is open', () => {
    setSensors(BODY, 'ovcha-kupel')
    const el = island()
    expect(shown(el)).toHaveLength(1)
    expect(shown(el)[0].textContent).toContain('national')
  })

  it('swaps in the area figures around the open sensor', () => {
    setSensors(BODY, 'ovcha-kupel')
    setScales(SCALES)
    setMapAreas([{ slug: 'ovcha-kupel', name_bg: 'Овча купел', name_en: 'Ovcha Kupel' }])
    const el = island()

    getViewState().openSensor(1)
    flushSync()

    const strip = shown(el)
    expect(strip).toHaveLength(1)
    expect(strip[0].textContent).not.toContain('national')
    expect(strip[0].textContent).toContain('Максимум · ФПЧ2.5')
    expect(strip[0].textContent).toContain('Овча купел · 3 сензора')
  })

  // The whole reason the national cells are hidden rather than replaced.
  it('brings the national cells back when the sensor is closed', () => {
    setSensors(BODY, 'ovcha-kupel')
    setScales(SCALES)
    const el = island()

    getViewState().openSensor(1)
    flushSync()
    expect(shown(el)[0].textContent).not.toContain('national')

    getViewState().closeSensor()
    flushSync()
    expect(shown(el)).toHaveLength(1)
    expect(shown(el)[0].textContent).toContain('national')
  })

  it('follows the metric the map is showing', () => {
    setSensors({ sensors: { id: [1, 2], station: [1, 2], P1: [50, 70], P2: [4, 12] } }, 'ovcha-kupel')
    const el = island()
    const vs = getViewState()
    vs.openSensor(1)
    flushSync()
    expect(shown(el)[0].textContent).toContain('Медиана · ФПЧ2.5')

    vs.setMetric('P1')
    flushSync()
    expect(shown(el)[0].textContent).toContain('Медиана · ФПЧ10')
  })

  // One reporting station gives a highest, a lowest and a median that are three
  // names for one number. The national figures are the better answer.
  it('keeps the national cells where the area cannot be compared', () => {
    setSensors({ sensors: { id: [1], station: [1], P2: [4] } }, 'ovcha-kupel')
    const el = island()
    getViewState().openSensor(1)
    flushSync()
    expect(shown(el)[0].textContent).toContain('national')
  })
})

describe('areaName', () => {
  const areas = [{ slug: 'sofia', name_bg: 'София', name_en: 'Sofia' }]

  it('names the area in the page language', () => {
    expect(areaName(areas, 'sofia', 'bg')).toBe('София')
    expect(areaName(areas, 'sofia', 'en')).toBe('Sofia')
  })

  it('has no name for an area the map never loaded', () => {
    expect(areaName(areas, 'plovdiv', 'bg')).toBe('')
    expect(areaName(null, null, 'bg')).toBe('')
  })
})
