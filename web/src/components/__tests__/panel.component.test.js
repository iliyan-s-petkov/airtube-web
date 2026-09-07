// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import SensorPanel from '../SensorPanel.svelte'

const rows = [
  { metric: 'P2', label: 'PM2.5', value: 12.4, unit: 'µg/m³', missing: false },
  { metric: 'P1', label: 'PM10', value: null, unit: 'µg/m³', missing: true },
]

let component
afterEach(() => { if (component) unmount(component) })

function render(props) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(SensorPanel, {
    target,
    props: { rows, title: 'Sensor 42', flagText: '', closeLabel: 'Close', noValue: 'no reading', onclose: () => {}, ...props },
  })
  return target
}

describe('SensorPanel.svelte', () => {
  it('shows each row with its value and unit', () => {
    const target = render()
    expect(target.textContent).toContain('PM2.5')
    expect(target.textContent).toContain('12.4')
    expect(target.textContent).toContain('µg/m³')
  })

  // A blank cell reads as zero on an air-quality page. It must say so in words.
  it('spells out a missing value instead of leaving a blank', () => {
    const target = render()
    expect(target.textContent).toContain('no reading')
  })

  it('shows the quality warning only when there is one', () => {
    expect(render({ flagText: 'These readings look suspect.' }).textContent).toContain('suspect')
    expect(render({ flagText: '' }).querySelector('.panel-flag')).toBeNull()
  })

  it('closes on the close control and on Escape', () => {
    const onclose = vi.fn()
    const target = render({ onclose })
    target.querySelector('[data-close]').click()
    expect(onclose).toHaveBeenCalledTimes(1)
    target.querySelector('.sensor-panel').dispatchEvent(
      new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }),
    )
    expect(onclose).toHaveBeenCalledTimes(2)
  })

  // Closed by default: the readings are what the reader came for, and the
  // hardware inventory pushed them below the chart on a phone.
  it('lists the station description in a disclosure that starts closed', () => {
    const target = render({
      detailsLabel: 'About this station',
      details: [
        { key: 'devices', label: 'Devices', value: '42, 43' },
        { key: 'since', label: 'In our data since', value: '5 Mar 2024' },
      ],
    })
    const details = target.querySelector('.panel-details')
    expect(details.tagName).toBe('DETAILS')
    expect(details.open).toBe(false)
    expect(details.querySelector('summary').textContent).toBe('About this station')
    expect([...details.querySelectorAll('dt')].map((n) => n.textContent))
      .toEqual(['Devices', 'In our data since'])
    expect([...details.querySelectorAll('dd')].map((n) => n.textContent))
      .toEqual(['42, 43', '5 Mar 2024'])
  })

  // An empty description list under a heading is a promise the panel cannot
  // keep: a station we know nothing about shows the readings and stops.
  it('renders no description list when there is nothing to describe', () => {
    expect(render({ details: [] }).querySelector('.panel-details')).toBeNull()
  })
})
