// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import SensorChart from '../SensorChart.svelte'
import { clearCache } from '../../lib/api.js'
import { setScales } from '../../lib/sensors.svelte.js'

// Same stub as chart.component.test.js: uPlot needs layout jsdom has not got,
// and what this suite is about is which URL was asked for and which series
// reached the constructor.
const uplotCalls = []
vi.mock('uplot', () => ({
  default: vi.fn(function (opts, data, el) {
    uplotCalls.push({ opts, data, el })
    this.setSize = vi.fn()
  }),
}))

const OPTIONS = [
  { metric: 'P2', label: 'ФПЧ2.5' },
  { metric: 'P1', label: 'ФПЧ10' },
  { metric: 'temperature', label: 'Температура' },
]

// The axis rule is per unit, so the units are what the component reads.
const SCALES = [
  { metric: 'P2', unit: 'µg/m³' },
  { metric: 'P1', unit: 'µg/m³' },
  { metric: 'temperature', unit: '°C' },
]

const props = {
  stationId: 42,
  // The station's two boxes: particulate on 42, climate on 43. Distinct ids
  // because charting temperature against the STATION id would silently ask the
  // particulate box for a reading it never took.
  sources: { P2: 42, P1: 42, temperature: 43 },
  options: OPTIONS,
  periods: ['24h', '7d'],
  periodLabels: ['24 часа', '7 дни'],
  initialPeriod: '24h',
  initialMetric: 'P2',
  metricLegend: 'Показател',
  periodLegend: 'Период',
  customLabel: 'Избран период',
  fromLabel: 'От',
  toLabel: 'До',
  resetLabel: 'Върни изгледа',
  rangeInvalid: 'Изберете начало и край.',
  colours: ['rgb(1, 2, 3)', 'rgb(4, 5, 6)', 'rgb(7, 8, 9)'],
  timeLabel: 'Време',
  empty: 'Няма измервания.',
  unavailable: 'Данните не са налични.',
}

let component
afterEach(() => {
  clearCache()
  if (component) unmount(component)
  vi.restoreAllMocks()
  uplotCalls.length = 0
  setScales(null)
})

function seriesResponse() {
  return new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 })
}

function render(extra) {
  setScales(SCALES)
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  const fetched = []
  vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
    fetched.push(String(url))
    return Promise.resolve(seriesResponse())
  })
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(SensorChart, { target, props: { ...props, ...extra } })
  return { target, fetched }
}

function tick(target, metric) {
  const box = target.querySelector(`input[name="panel-metric"][value="${metric}"]`)
  box.checked = !box.checked
  box.dispatchEvent(new Event('change', { bubbles: true }))
}

function setValue(el, value) {
  el.value = value
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

const periodSelect = (target) => target.querySelector('#panel-period-select')

// The two range inputs are only rendered once the select says custom.
async function chooseCustom(target) {
  setValue(periodSelect(target), 'custom')
  await vi.waitFor(() => expect(target.querySelector('#panel-period-from')).not.toBeNull())
}

describe('SensorChart.svelte', () => {
  it('asks the device that recorded the metric, not the station', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))
    expect(fetched[0]).toContain('/api/v1/sensor/42/series')

    tick(target, 'temperature')
    await vi.waitFor(() => expect(fetched).toHaveLength(2))
    expect(fetched[1]).toContain('/api/v1/sensor/43/series')
    expect(fetched[1]).toContain('metric=temperature')
  })

  it('falls back to the station id for a metric with no source of its own', async () => {
    const { fetched } = render({ sources: null })
    await vi.waitFor(() => expect(fetched).toHaveLength(1))
    expect(fetched[0]).toContain('/api/v1/sensor/42/series')
  })

  it('re-requests the same metric over the window the reader picked', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))
    expect(fetched[0]).toContain('period=24h')

    setValue(periodSelect(target), '7d')
    await vi.waitFor(() => expect(fetched).toHaveLength(2))
    expect(fetched[1]).toContain('period=7d')
    expect(fetched[1]).toContain('metric=P2')
  })

  it('opens on the server default metric', async () => {
    const { target } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    expect(target.querySelector('input[name="panel-metric"][value="P2"]').checked).toBe(true)
  })

  // A climate-only address does not measure the map's default. Seeding the
  // switcher with it would open the panel on an empty frame for a station that
  // has data to show.
  it('opens on a metric the station measures when it does not measure the default', async () => {
    const { fetched } = render({
      options: [OPTIONS[2]],
      sources: { temperature: 43 },
      initialMetric: 'P2',
    })
    await vi.waitFor(() => expect(fetched).toHaveLength(1))
    expect(fetched[0]).toContain('metric=temperature')
  })

  it('draws a second metric of another unit on its own scale, in its own colour', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))

    tick(target, 'temperature')
    await vi.waitFor(() => expect(fetched).toHaveLength(2))
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(3))
    const { series } = uplotCalls.at(-1).opts
    expect(series[1].scale).toBe('y')
    expect(series[1].stroke).toBe(props.colours[0])
    expect(series[2].scale).toBe('y2')
    expect(series[2].stroke).toBe(props.colours[1])
  })

  // Two metrics counted in micrograms are the same quantity: two scales would
  // draw them against two different ranges and make the smaller look the larger.
  it('keeps two metrics of the same unit on one scale', async () => {
    const { target } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))

    tick(target, 'P1')
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(3))
    const { series } = uplotCalls.at(-1).opts
    expect(series[1].scale).toBe('y')
    expect(series[2].scale).toBe('y')
  })

  // A chart with no series is an empty frame the reader cannot get out of
  // except by finding the tick again.
  it('refuses to untick the last metric', async () => {
    const { target } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))

    tick(target, 'P2')
    await Promise.resolve()
    expect(target.querySelector('input[name="panel-metric"][value="P2"]').checked).toBe(true)
    expect(uplotCalls.at(-1).opts.series).toHaveLength(2)
  })

  it('asks for the custom window only once both ends are set, earliest first', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))

    await chooseCustom(target)
    setValue(target.querySelector('#panel-period-from'), '2026-08-14T00:00')
    await Promise.resolve()
    expect(fetched).toHaveLength(1)
    expect(target.querySelector('.chart-message').textContent).toContain(props.rangeInvalid)

    setValue(target.querySelector('#panel-period-to'), '2026-08-15T00:00')
    await vi.waitFor(() => expect(fetched).toHaveLength(2))
    expect(fetched[1]).toContain('period=custom')
    expect(fetched[1]).toContain('from=')
    expect(fetched[1]).toContain('to=')
  })

  it('says nothing can be drawn when the range runs backwards', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))

    await chooseCustom(target)
    setValue(target.querySelector('#panel-period-from'), '2026-08-15T00:00')
    setValue(target.querySelector('#panel-period-to'), '2026-08-14T00:00')
    await Promise.resolve()
    expect(fetched).toHaveLength(1)
    expect(target.querySelector('.chart-message').textContent).toContain(props.rangeInvalid)
  })

  it('reset returns the metric and the window to the view the panel opened on', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))

    tick(target, 'temperature')
    setValue(periodSelect(target), '7d')
    await vi.waitFor(() => expect(fetched.at(-1)).toContain('period=7d'))

    // Last of the row: the metric menu's own button is a .btn--secondary too.
    const buttons = target.querySelectorAll('.panel-chart__controls button.btn--secondary')
    buttons[buttons.length - 1].click()
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(2))
    // No new request: the opening view is still in the cache. What reset owes
    // the reader is that view back, so the controls are what this asserts.
    expect(periodSelect(target).value).toBe('24h')
    expect(target.querySelector('input[name="panel-metric"][value="P2"]').checked).toBe(true)
    expect(target.querySelector('input[name="panel-metric"][value="temperature"]').checked).toBe(false)
  })
})
