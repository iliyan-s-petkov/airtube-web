// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import SensorChart from '../SensorChart.svelte'
import { clearCache } from '../../lib/api.js'
import { setScales, setSensors } from '../../lib/sensors.svelte.js'

// Same uPlot stub as the other chart suites: jsdom has no layout, and what this
// suite is about is which urls were asked for and which series reached the
// constructor.
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

const SCALES = [
  { metric: 'P2', unit: 'µg/m³' },
  { metric: 'temperature', unit: '°C' },
]

const NEARBY_LABELS = { low: 'Наблизо · минимум', median: 'Наблизо · медиана', high: 'Наблизо · максимум' }

const props = {
  stationId: 42,
  sources: { P2: 42, temperature: 43 },
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
  nearbyLegend: 'Сензори наблизо',
  nearbyOff: 'изключени',
  nearbySingleOnly: 'Достъпно при един показател',
  nearbyLabels: NEARBY_LABELS,
  colours: ['rgb(1, 2, 3)', 'rgb(4, 5, 6)', 'rgb(7, 8, 9)'],
  timeLabel: 'Време',
  empty: 'Няма измервания.',
  unavailable: 'Данните не са налични.',
}

// The map's own body, which is where the panel learns which area this sensor
// stands in — no second request, and no slug the component invents.
const SENSORS = {
  sensors: { id: [42], station: [42], P2: [12], temperature: [20] },
}

let component
afterEach(() => {
  clearCache()
  if (component) unmount(component)
  vi.restoreAllMocks()
  uplotCalls.length = 0
  setScales(null)
  setSensors(null, null)
})

function render(slug = 'ovcha-kupel') {
  setScales(SCALES)
  setSensors(SENSORS, slug)
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
  const fetched = []
  vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
    fetched.push(String(url))
    return Promise.resolve(new Response(JSON.stringify({
      t: ['2026-08-14T00:00:00Z'], v: [12.3], lo: [4], hi: [40],
    }), { status: 200 }))
  })
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(SensorChart, { target, props })
  return { target, fetched }
}

const nearbyBox = (target, key) => target.querySelector(`input[name="panel-nearby"][value="${key}"]`)

function tickNearby(target, key) {
  const box = nearbyBox(target, key)
  box.checked = !box.checked
  box.dispatchEvent(new Event('change', { bubbles: true }))
}

function tickMetric(target, metric) {
  const box = target.querySelector(`input[name="panel-metric"][value="${metric}"]`)
  box.checked = !box.checked
  box.dispatchEvent(new Event('change', { bubbles: true }))
}

describe('the nearby-sensors overlay', () => {
  // Nothing ticked is the default. The panel has always opened on this sensor
  // alone, and a reader who wants a comparison asks for one.
  it('draws the sensor alone until a line is ticked', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    expect(fetched).toHaveLength(1)
    expect(fetched[0]).toContain('/api/v1/sensor/42/series')
    expect(target.querySelector('#panel-nearby-menu').textContent).toContain(props.nearbyOff)
  })

  // The area's spread for the metric on screen, over the window on screen: an
  // overlay drawn from another metric or another window would be a comparison
  // against something the reader is not looking at.
  it('asks the sensor area for a banded series of the metric on screen', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))

    tickNearby(target, 'median')
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(2))

    const band = fetched.find((u) => u.includes('/api/v1/area/'))
    expect(band).toContain('/api/v1/area/ovcha-kupel/series')
    expect(band).toContain('metric=P2')
    expect(band).toContain('period=24h')
    expect(band).toContain('band=1')
  })

  // Three overlay lines plus the sensor is four series and TWO requests, not
  // four: the band arrives in one body.
  it('draws all three lines off one extra request', async () => {
    const { target, fetched } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))

    for (const key of ['low', 'median', 'high']) tickNearby(target, key)
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(5))

    const areaRequests = fetched.filter((u) => u.includes('/api/v1/area/'))
    expect(new Set(areaRequests).size).toBe(1)

    const labels = uplotCalls.at(-1).opts.series.slice(2).map((s) => s.label)
    expect(labels).toEqual([NEARBY_LABELS.low, NEARBY_LABELS.median, NEARBY_LABELS.high])
  })

  // The three lines are the spread of ONE quantity. With two metrics drawn there
  // is nothing for a median to be the median of, so the control says so rather
  // than disappearing — a control that vanishes reads as a bug.
  it('is disabled, with its reason, while more than one metric is drawn', async () => {
    const { target } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    const button = target.querySelector('#panel-nearby-menu')
    expect(button.disabled).toBe(false)

    tickMetric(target, 'temperature')
    await vi.waitFor(() => expect(button.disabled).toBe(true))
    expect(button.title).toBe(props.nearbySingleOnly)
  })

  // Ticked, then a second metric added: the overlay has to come off the plot,
  // not stay on it against an axis it no longer belongs to.
  it('drops the overlay when a second metric joins the plot', async () => {
    const { target } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))

    tickNearby(target, 'high')
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(3))

    // Asserted on the labels, not on the count: the plot is three series wide
    // both before and after, so a length check passes against the stale render.
    tickMetric(target, 'temperature')
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series.map((s) => s.label))
      .toEqual([props.timeLabel, 'ФПЧ2.5', 'Температура']))
  })

  // No area means nothing to compare against. Offering the control anyway would
  // give the reader a menu whose every choice does nothing.
  it('is absent when the map never said which area this sensor is in', async () => {
    const { target } = render(null)
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    expect(target.querySelector('#panel-nearby-menu')).toBeNull()
  })

  // Reset undoes everything the controls did, and the overlay is one of them.
  it('is cleared by Reset', async () => {
    const { target } = render()
    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))

    tickNearby(target, 'median')
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(3))

    target.querySelector('.chart-reset').click()
    await vi.waitFor(() => expect(uplotCalls.at(-1).opts.series).toHaveLength(2))
    expect(nearbyBox(target, 'median').checked).toBe(false)
  })
})
