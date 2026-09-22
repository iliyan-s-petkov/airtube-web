// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import MetricPicker from '../MetricPicker.svelte'

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
  component = mount(MetricPicker, { target, props: { options, legend: 'Metric', ...props } })
  return target
}

const button = (t) => t.querySelector('button')

describe('MetricPicker.svelte', () => {
  // Legend and colon live in their own span (app.css .colmenu__legend) so the
  // phone toolbar can clip them, leaving only the chosen metrics.
  it('wraps the legend in .colmenu__legend, ahead of the chosen metrics', () => {
    const t = render({ selected: ['P1'], onchange: () => {} })
    const legend = button(t).querySelector('.colmenu__legend')
    expect(legend).not.toBeNull()
    expect(legend.textContent.trim()).toBe('Metric:')
    expect(button(t).textContent.trim()).toBe('Metric: PM10')
  })
})
