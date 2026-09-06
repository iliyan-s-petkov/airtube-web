// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import SensorBar from '../SensorBar.svelte'
import { setSensors } from '../../lib/sensors.svelte.js'
import { getSensorStatus, resetSensorFilterForTests } from '../../lib/sensorfilter.svelte.js'

const texts = {
  legend: 'Show',
  all: 'All',
  active: 'With data',
  inactive: 'Without data',
  shown: 'Showing',
  of: 'of',
  sensors: 'sensors',
  silent: 'with no recent readings',
}

const body = {
  sensors: { id: [1, 2, 3, 4], P1: [1, 2, 3, 4], P2: [5.1, null, 0, null] },
}

let component
let host
afterEach(() => {
  if (component) unmount(component)
  component = null
  host?.remove()
  setSensors(null)
  resetSensorFilterForTests()
})

function render(metric = 'P2') {
  host = document.createElement('div')
  document.body.append(host)
  component = mount(SensorBar, {
    target: host,
    props: { texts, get metric() { return metric } },
  })
  flushSync()
  return host
}

const line = (el) => el.querySelector('.meta').textContent
const radio = (el, value) => el.querySelector(`input[value="${value}"]`)

describe('SensorBar', () => {
  it('offers the three statuses as one radio group of its own', () => {
    const el = render()
    const names = [...el.querySelectorAll('input[type="radio"]')].map((i) => i.name)
    // A shared name would silently merge this group with the metric switcher's,
    // and picking a status would deselect the metric.
    expect(names).toEqual(['sensor-status', 'sensor-status', 'sensor-status'])
  })

  it('opens on all sensors, matching the store default', () => {
    const el = render()
    expect(radio(el, 'all').checked).toBe(true)
  })

  it('counts nothing before the map has published its sensors', () => {
    const el = render()
    expect(line(el)).toBe('Showing 0 of 0 sensors — 0 with no recent readings')
  })

  // The bar mounts before the map's fetch resolves; a count frozen at mount
  // would read "0 of 0" for the rest of the visit.
  it('picks up the counts when the map publishes them', () => {
    const el = render()
    setSensors(body)
    flushSync()
    expect(line(el)).toBe('Showing 4 of 4 sensors — 2 with no recent readings')
  })

  it('narrows the shown count when a status is picked', () => {
    const el = render()
    setSensors(body)
    flushSync()
    radio(el, 'active').click()
    flushSync()
    expect(getSensorStatus()).toBe('active')
    expect(line(el)).toContain('Showing 2 of 4')
  })

  // Silence is per metric: on P1 every one of these four reports.
  it('recounts against the metric it is given', () => {
    const el = render('P1')
    setSensors(body)
    flushSync()
    expect(line(el)).toBe('Showing 4 of 4 sensors — 0 with no recent readings')
  })

  // The numbers change as a result of the reader's own click on the radios
  // beside them, so they must be announced without moving focus.
  it('announces the count as a status region', () => {
    const el = render()
    expect(el.querySelector('.meta').getAttribute('role')).toBe('status')
  })
})
