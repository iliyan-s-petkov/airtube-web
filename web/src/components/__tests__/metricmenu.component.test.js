// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount, tick } from 'svelte'
import MetricMenu from '../MetricMenu.svelte'
import { getViewState, resetViewStateForTests } from '../../lib/viewstate.svelte.js'

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
  component = mount(MetricMenu, { target, props: { options, legend: 'Metric', ...props } })
  return target
}

const button = (t) => t.querySelector('button')
const panel = (t) => t.querySelector('.colmenu__panel')
const radios = (t) => [...t.querySelectorAll('input[type="radio"]')]

describe('MetricMenu.svelte', () => {
  // The button is the whole point of the pop-up: seven segments took four lines
  // on a phone and pushed the map below the fold. One line, and it says which
  // metric the map is painting.
  it('names the current metric on the button, closed', () => {
    const t = render({ selected: 'P1', onselect: () => {} })
    expect(button(t).textContent.trim()).toBe('Metric: PM10')
    expect(panel(t).hidden).toBe(true)
    expect(button(t).getAttribute('aria-expanded')).toBe('false')
  })

  // The label is the server's, carried by the option — never a second copy
  // written in the component, which would still read as the old name the day
  // the catalogue renames a metric.
  it('takes the button label from the selected option', () => {
    const t = render({ selected: 'temperature', onselect: () => {} })
    expect(button(t).textContent.trim()).toBe('Metric: Temperature')
  })

  it('opens the panel, and says which panel it opens', async () => {
    const t = render({ selected: 'P2', onselect: () => {} })
    button(t).click()
    await tick()
    expect(panel(t).hidden).toBe(false)
    expect(button(t).getAttribute('aria-expanded')).toBe('true')
    expect(button(t).getAttribute('aria-controls')).toBe(panel(t).id)
  })

  it('offers every metric the server sent', async () => {
    const t = render({ selected: 'P2', onselect: () => {} })
    button(t).click()
    await tick()
    expect([...panel(t).querySelectorAll('.colmenu__opt span')].map((s) => s.textContent.trim()))
      .toEqual(['PM2.5', 'PM10', 'Temperature'])
  })

  // Still one radio set, for the reason it always was: the metrics are mutually
  // exclusive, so the reader gets one Tab stop and arrow-key roving, and the
  // choice is announced as selected rather than inferred from a colour.
  it('keeps the options a single radio group', async () => {
    const t = render({ selected: 'P1', onselect: () => {} })
    button(t).click()
    await tick()
    expect(new Set(radios(t).map((r) => r.name)).size).toBe(1)
    expect(radios(t).filter((r) => r.checked).map((r) => r.value)).toEqual(['P1'])
  })

  it('reports the chosen metric by its canonical name, not its label', async () => {
    const onselect = vi.fn()
    const t = render({ selected: 'P2', onselect })
    button(t).click()
    await tick()
    radios(t)[2].click()
    expect(onselect).toHaveBeenCalledWith('temperature')
  })

  // The panel covers the map. Left open, it would hide the change the reader
  // just asked for.
  it('closes on a choice and gives the button back the focus', async () => {
    const t = render({ selected: 'P2', onselect: () => {} })
    button(t).click()
    await tick()
    radios(t)[1].click()
    await tick()
    expect(panel(t).hidden).toBe(true)
    expect(document.activeElement).toBe(button(t))
  })

  it('closes on Escape from wherever the reader is', async () => {
    const t = render({ selected: 'P2', onselect: () => {} })
    button(t).click()
    await tick()
    window.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', cancelable: true }))
    await tick()
    expect(panel(t).hidden).toBe(true)
    expect(document.activeElement).toBe(button(t))
  })

  it('closes on a press outside it', async () => {
    const t = render({ selected: 'P2', onselect: () => {} })
    button(t).click()
    await tick()
    document.body.dispatchEvent(new window.MouseEvent('mousedown', { bubbles: true }))
    await tick()
    expect(panel(t).hidden).toBe(true)
  })

  // The metric also changes from the URL and from the map, so both the button
  // and the checked radio have to follow the store rather than whatever was
  // last clicked here.
  it('follows a selection made somewhere other than this control', async () => {
    resetViewStateForTests()
    const vs = getViewState({ metrics: options.map((o) => o.metric), defaultMetric: 'P2' })
    const target = document.createElement('div')
    document.body.appendChild(target)
    component = mount(MetricMenu, {
      target,
      props: {
        options,
        legend: 'Metric',
        onselect: (m) => vs.setMetric(m),
        get selected() { return vs.metric },
      },
    })
    expect(button(target).textContent.trim()).toBe('Metric: PM2.5')

    vs.setMetric('temperature')
    await tick()
    expect(button(target).textContent.trim()).toBe('Metric: Temperature')
  })
})
