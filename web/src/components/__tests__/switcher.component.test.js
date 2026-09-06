// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount, tick } from 'svelte'
import MetricSwitcher from '../MetricSwitcher.svelte'
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
  component = mount(MetricSwitcher, { target, props: { options, legend: 'Metric', ...props } })
  return target
}

const radios = (target) => [...target.querySelectorAll('input[type="radio"]')]

describe('MetricSwitcher.svelte', () => {
  it('renders one control per metric, labelled by the server', () => {
    const target = render({ selected: 'P2', onselect: () => {} })
    expect(radios(target).length).toBe(3)
    expect([...target.querySelectorAll('.switcher__opt span')].map((s) => s.textContent.trim()))
      .toEqual(['PM2.5', 'PM10', 'Temperature'])
  })

  // The metrics are mutually exclusive, so they must be ONE control, not three
  // independent toggles: a shared name is what gives the set arrow-key roving
  // and a single Tab stop (DESIGN.md §5.6).
  it('groups the metrics into a single radio set', () => {
    const target = render({ selected: 'P2', onselect: () => {} })
    expect(new Set(radios(target).map((r) => r.name)).size).toBe(1)
    expect(target.querySelector('fieldset.switcher')).not.toBeNull()
    expect(target.querySelector('legend').textContent.trim()).toBe('Metric')
  })

  // The selected metric must be conveyed by the control's own state, not by a
  // colour: a screen-reader user has to be able to tell what the map is showing.
  it('marks the selected metric for assistive technology', () => {
    const target = render({ selected: 'P1', onselect: () => {} })
    const checked = radios(target).filter((r) => r.checked)
    expect(checked.map((r) => r.value)).toEqual(['P1'])
  })

  it('reports the chosen metric by its canonical name, not its label', () => {
    const onselect = vi.fn()
    const target = render({ selected: 'P2', onselect })
    radios(target)[2].click()
    expect(onselect).toHaveBeenCalledWith('temperature')
  })

  // The metric also changes from the URL and from the map, so the checked state
  // has to follow the store. An uncontrolled radio set keeps whatever the user
  // last clicked and then disagrees with the map it is supposed to describe.
  //
  // Driven through the real view state rather than a local $state because a
  // rune cannot live in a plain .js file, and this is the source the island
  // actually binds to — so the test exercises the production wiring.
  it('follows a selection made somewhere other than this control', async () => {
    resetViewStateForTests()
    const vs = getViewState({ metrics: options.map((o) => o.metric), defaultMetric: 'P2' })
    const target = document.createElement('div')
    document.body.appendChild(target)
    component = mount(MetricSwitcher, {
      target,
      props: {
        options,
        legend: 'Metric',
        onselect: (m) => vs.setMetric(m),
        get selected() { return vs.metric },
      },
    })
    expect(radios(target).find((r) => r.checked).value).toBe('P2')

    vs.setMetric('temperature')
    await tick()
    expect(radios(target).find((r) => r.checked).value).toBe('temperature')
  })

  // Two switchers on one page must not merge into one group, which is what a
  // hardcoded name would do: clicking a metric in one would clear the other.
  it('can be given its own group name', () => {
    const target = render({ selected: 'P2', onselect: () => {}, name: 'area-metric' })
    expect(new Set(radios(target).map((r) => r.name))).toEqual(new Set(['area-metric']))
  })
})
