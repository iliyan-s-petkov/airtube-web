// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import MetricSwitcher from '../MetricSwitcher.svelte'

const options = [
  { metric: 'P2', label: 'PM2.5' },
  { metric: 'P1', label: 'PM10' },
  { metric: 'temperature', label: 'Temperature' },
]

let component
afterEach(() => { if (component) unmount(component) })

function render(props) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(MetricSwitcher, { target, props: { options, legend: 'Metric', ...props } })
  return target
}

describe('MetricSwitcher.svelte', () => {
  it('renders one control per metric, labelled by the server', () => {
    const target = render({ selected: 'P2', onselect: () => {} })
    const buttons = target.querySelectorAll('button')
    expect(buttons.length).toBe(3)
    expect([...buttons].map((b) => b.textContent.trim())).toEqual(['PM2.5', 'PM10', 'Temperature'])
  })

  // aria-pressed, not a class: a screen-reader user must be able to tell which
  // metric the map is showing, and a colour change alone does not say it.
  it('marks the selected metric for assistive technology', () => {
    const target = render({ selected: 'P1', onselect: () => {} })
    const pressed = [...target.querySelectorAll('button')].filter((b) => b.getAttribute('aria-pressed') === 'true')
    expect(pressed.map((b) => b.textContent.trim())).toEqual(['PM10'])
  })

  it('reports the chosen metric by its canonical name, not its label', () => {
    const onselect = vi.fn()
    const target = render({ selected: 'P2', onselect })
    target.querySelectorAll('button')[2].click()
    expect(onselect).toHaveBeenCalledWith('temperature')
  })
})
