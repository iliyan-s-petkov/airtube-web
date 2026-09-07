// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import SensorChart from '../SensorChart.svelte'
import { clearCache } from '../../lib/api.js'

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
  { metric: 'temperature', label: 'Температура' },
]

const props = {
  stationId: 42,
  // The station's two boxes: particulate on 42, climate on 43. Distinct ids
  // because charting temperature against the STATION id would silently ask the
  // particulate box for a reading it never took.
  sources: { P2: 42, temperature: 43 },
  options: OPTIONS,
  periods: ['24h', '7d'],
  periodLabels: ['24 часа', '7 дни'],
  initialPeriod: '24h',
  initialMetric: 'P2',
  metricLegend: 'Показател',
  periodLegend: 'Период',
  compareLabel: 'Сравни с',
  compareNone: 'нищо',
  primaryColour: 'rgb(1, 2, 3)',
  compareColour: 'rgb(4, 5, 6)',
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
})

function seriesResponse() {
  return new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 })
}

function render(extra) {
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

function pick(target, name, value) {
  const input = target.querySelector(`input[name="${name}"][value="${value}"]`)
  input.checked = true
  input.dispatchEvent(new Event('change', { bubbles: true }))
}

describe('SensorChart.svelte', () => {
  it('asks the device that recorded the metric, not the station', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))
    expect(fetched[0]).toContain('/api/v1/sensor/42/series')

    pick(target, 'panel-metric', 'temperature')
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

    pick(target, 'panel-window', '7d')
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
      options: [OPTIONS[1]],
      sources: { temperature: 43 },
      initialMetric: 'P2',
    })
    await vi.waitFor(() => expect(fetched).toHaveLength(1))
    expect(fetched[0]).toContain('metric=temperature')
  })

  it('draws the compared metric on its own scale, in its own colour', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))

    const select = target.querySelector('.panel-chart__compare select')
    select.value = 'temperature'
    select.dispatchEvent(new Event('change', { bubbles: true }))

    await vi.waitFor(() => expect(fetched).toHaveLength(2))
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(3))
    const { series } = uplotCalls.at(-1).opts
    expect(series[1].scale).toBe('y')
    expect(series[1].stroke).toBe(props.primaryColour)
    expect(series[2].scale).toBe('y2')
    expect(series[2].stroke).toBe(props.compareColour)
  })

  it('never offers the charted metric as its own comparison', async () => {
    const { target } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    const values = () => [...target.querySelectorAll('.panel-chart__compare option')]
      .map((o) => o.value)
    expect(values()).toEqual(['', 'temperature'])

    pick(target, 'panel-metric', 'temperature')
    await vi.waitFor(() => expect(values()).toEqual(['', 'P2']))
  })

  // Picking the compared metric as the primary one would otherwise leave the
  // same series drawn twice, on two scales, in two colours.
  it('drops the comparison when the reader charts that metric instead', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(fetched).toHaveLength(1))

    const select = target.querySelector('.panel-chart__compare select')
    select.value = 'temperature'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(3))

    pick(target, 'panel-metric', 'temperature')
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(2))
    expect(select.value).toBe('')
  })
})
