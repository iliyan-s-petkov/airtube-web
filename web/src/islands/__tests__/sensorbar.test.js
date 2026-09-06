// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { flushSync } from 'svelte'
import { mount } from '../sensorbar.js'
import { setSensors } from '../../lib/sensors.svelte.js'
import { getViewState, resetViewStateForTests } from '../../lib/viewstate.svelte.js'
import { resetSensorFilterForTests } from '../../lib/sensorfilter.svelte.js'

const body = { sensors: { id: [1, 2], P1: [1, 2], P2: [5.1, null] } }

function render() {
  const el = document.createElement('div')
  el.dataset.metrics = 'P1,P2'
  el.dataset.metric = 'P2'
  el.dataset.tLegend = 'Show'
  el.dataset.tAll = 'All'
  el.dataset.tActive = 'With data'
  el.dataset.tInactive = 'Without data'
  el.dataset.tShown = 'Showing'
  el.dataset.tOf = 'of'
  el.dataset.tSensors = 'sensors'
  el.dataset.tSilent = 'with no recent readings'
  document.body.append(el)
  mount(el)
  flushSync()
  return el
}

beforeEach(() => { resetViewStateForTests(); resetSensorFilterForTests(); setSensors(null) })
afterEach(() => {
  document.body.innerHTML = ''
  resetViewStateForTests()
  resetSensorFilterForTests()
  setSensors(null)
})

describe('the sensor bar island', () => {
  it('hands the component its translated labels', () => {
    const el = render()
    expect([...el.querySelectorAll('.switcher__opt span')].map((s) => s.textContent))
      .toEqual(['All', 'With data', 'Without data'])
    expect(el.querySelector('legend').textContent).toBe('Show')
  })

  // The metric is passed as a getter, so the count follows the reader's choice
  // in the map's own switcher. A plain value would freeze the count at mount —
  // and the silent number would keep answering about the metric the page opened
  // on rather than the one on screen.
  it('recounts when the metric changes elsewhere on the page', () => {
    const el = render()
    setSensors(body)
    flushSync()
    expect(el.querySelector('.meta').textContent).toContain('1 with no recent readings')

    getViewState({ metrics: ['P1', 'P2'], defaultMetric: 'P2' }).setMetric('P1')
    flushSync()
    expect(el.querySelector('.meta').textContent).toContain('0 with no recent readings')
  })
})
