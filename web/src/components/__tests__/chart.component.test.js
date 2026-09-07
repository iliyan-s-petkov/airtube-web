// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import Chart from '../Chart.svelte'
import { clearCache } from '../../lib/api.js'

// uPlot needs layout the jsdom environment does not provide, so it is stubbed:
// this test is about which BRANCH runs and what text the reader ends up with,
// which is exactly the part uPlot cannot tell us.
//
// The mock also records the constructor's own arguments (opts, data, el) —
// ported from the old islands/__tests__/chart.test.js "reaches uPlot
// construction" case (J5, review round 2): without this, a mutation that
// swapped `stroke: lineColour` for `stroke: title` (or vice versa) would pass
// every other assertion here.
const uplotCalls = []
vi.mock('uplot', () => ({
  default: vi.fn(function (opts, data, el) {
    uplotCalls.push({ opts, data, el })
    this.setSize = vi.fn()
  }),
}))

const props = {
  url: '/api/v1/area/sofia/series?metric=P2&period=24h',
  lineColour: '#2563eb',
  title: 'PM2.5',
  valueLabel: 'µg/m³',
  empty: 'No readings in this window.',
  unavailable: 'Data is unavailable right now.',
}

let component
afterEach(() => {
  // lib/api.js caches by URL for the page's lifetime, and a module cache
  // outlives a test: without this, a later case asking for a URL an earlier
  // case fetched is answered from the cache and never reaches its own mock.
  clearCache()
  if (component) unmount(component)
  vi.restoreAllMocks()
  uplotCalls.length = 0
})

function render(extra) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(Chart, { target, props: { ...props, ...extra } })
  return target
}

describe('Chart.svelte', () => {
  it('says the data is unavailable when the fetch fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('boom'))
    // Ported from the old island suite: the console.error is kept
    // deliberately (a developer still needs the cause), so its call is
    // still proven here even though the visible behaviour is the text.
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    const target = render({ url: '/api/v1/area/fail/series' })
    await vi.waitFor(() => expect(target.textContent).toContain(props.unavailable))
    expect(errors).toHaveBeenCalled()
  })

  // Ported from the old island suite's "also explains a 429 the retry could
  // not clear": lib/api.js's own retry-cap logic is that module's test
  // responsibility, but this proves the component's catch branch still
  // stringifies whatever getJSON throws, 429-shaped or not, into the same
  // 'unavailable' text — not a distinct branch that could silently regress.
  it('says the data is unavailable when a 429 exceeds the retry cap', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 429, headers: { 'Retry-After': '86400' } }),
    )
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const target = render({ url: '/api/v1/area/limited/series' })
    await vi.waitFor(() => expect(target.textContent).toContain(props.unavailable))
  })

  // An empty frame with no words on an air-quality page reads as "nothing to
  // report", i.e. as clean air. It must say why instead.
  it('says the window is empty rather than drawing an empty frame', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ t: [], v: [] }), { status: 200 }),
    )
    const target = render({ url: '/api/v1/area/empty/series' })
    await vi.waitFor(() => expect(target.textContent).toContain(props.empty))
  })

  // Ported from the old island suite's "reaches uPlot construction" case
  // (J5, review round 2): lineColour must land on the series stroke and
  // title on the chart title — not swapped. Deliberately distinct values so
  // a mutation swapping them fails this instead of coincidentally matching.
  it('passes lineColour as the series stroke and title as the chart title, not swapped', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 }),
    )
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    render({ title: 'PM2.5, Sofia', lineColour: '#2563eb' })

    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    expect(uplotCalls[0].opts.title).toBe('PM2.5, Sofia')
    expect(uplotCalls[0].opts.series[1].stroke).toBe('#2563eb')
  })

  // The x series carried no label, so uPlot supplied its own built-in English
  // "Time" — visible in the hover readout (the legend above IS that readout) on
  // an otherwise Bulgarian page. A default in someone else's library is still a
  // string this site shows its readers, so it comes from the catalogue.
  it('labels the time axis from the catalogue rather than letting uPlot default to English', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 }),
    )
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    render({ timeLabel: 'Време' })

    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    expect(uplotCalls[0].opts.series[0].label).toBe('Време')
  })

  // Two metrics on one plot is the panel's whole reason for this prop. Each
  // line keeps its own colour and its own y scale — with one shared scale,
  // °C is a flat line along the bottom of a µg/m³ range.
  it('draws one line per source, each on the scale it names', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) =>
      Promise.resolve(new Response(JSON.stringify(
        String(input).includes('temperature')
          ? { t: ['2026-08-14T00:00:00Z'], v: [21] }
          : { t: ['2026-08-14T00:00:00Z'], v: [12.3] },
      ), { status: 200 })))
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    render({
      url: undefined,
      timeLabel: 'Време',
      sources: [
        { url: '/api/v1/sensor/1/series?metric=P2', label: 'ПМ2.5', colour: '#111', scale: 'y' },
        { url: '/api/v1/sensor/2/series?metric=temperature', label: '°C', colour: '#f90', scale: 'y2' },
      ],
    })

    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    const { opts, data } = uplotCalls[0]
    expect(opts.series.map((s) => s.label)).toEqual(['Време', 'ПМ2.5', '°C'])
    expect(opts.series[1].scale).toBe('y')
    expect(opts.series[2].scale).toBe('y2')
    expect(opts.series[2].stroke).toBe('#f90')
    // x, then one y column per source — and the values not swapped between them.
    expect(data).toEqual([[1786665600], [12.3], [21]])
    // A second axis on the right, so the second unit has its own numbers.
    expect(opts.axes.map((a) => a.side)).toEqual([undefined, 3, 1])
  })

  // Distinct colours and units, so a swapped mapping cannot pass.
  it('paints each y axis in its own line s colour and labels it with that line s unit', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) =>
      Promise.resolve(new Response(JSON.stringify(
        String(input).includes('temperature')
          ? { t: ['2026-08-14T00:00:00Z'], v: [21] }
          : { t: ['2026-08-14T00:00:00Z'], v: [12.3] },
      ), { status: 200 })))
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    render({
      url: undefined,
      timeLabel: 'Време',
      sources: [
        { url: '/api/v1/sensor/1/series?metric=P2', label: 'ПМ2.5', colour: '#111', scale: 'y', unit: 'µg/m³' },
        { url: '/api/v1/sensor/2/series?metric=temperature', label: 'Температура', colour: '#f90', scale: 'y2', unit: '°C' },
      ],
    })

    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    const { opts } = uplotCalls[0]
    expect(opts.axes.map((a) => a.stroke)).toEqual([undefined, '#111', '#f90'])
    expect(opts.axes.map((a) => a.label)).toEqual(['Време', 'µg/m³', '°C'])
  })

  // The area page's path: its unit arrives as a prop, not in a sources list.
  it('labels the y axis of a one-line chart from valueUnit', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 }),
    )
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    render({ valueUnit: 'µg/m³', lineColour: '#2563eb' })

    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    expect(uplotCalls[0].opts.axes[1].label).toBe('µg/m³')
    expect(uplotCalls[0].opts.axes[1].stroke).toBe('#2563eb')
  })

  // No unit in the scales table must not print an empty label box.
  it('leaves the axis unlabelled when the metric has no unit', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 }),
    )
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    render({ valueUnit: '' })

    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    expect(uplotCalls[0].opts.axes[1].label).toBeUndefined()
  })

  // A metric whose request fails must not leave a plot that looks complete
  // with one line silently missing.
  it('says the data is unavailable when one of several sources fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) =>
      String(input).includes('temperature')
        ? Promise.reject(new Error('boom'))
        : Promise.resolve(new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 })))
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const target = render({
      url: undefined,
      sources: [
        { url: '/api/v1/sensor/1/series?metric=P2', label: 'ПМ2.5', colour: '#111', scale: 'y' },
        { url: '/api/v1/sensor/2/series?metric=temperature', label: '°C', colour: '#f90', scale: 'y2' },
      ],
    })
    await vi.waitFor(() => expect(target.textContent).toContain(props.unavailable))
  })

  // uPlot's legend IS its hover readout, and at rest it renders the series
  // label beside a literal em-dash placeholder — "µg/m³ --" under a chart
  // nobody has touched yet, which reads as unfinished markup rather than as
  // "hover me". Hiding the legend outright would take the readout with it, so
  // the component gates its visibility on the cursor instead.
  //
  // Asserted through the hook rather than through a real mouse event: uPlot is
  // stubbed here (it needs layout jsdom does not have), so the hook is the only
  // honest seam. It is also the thing that would break — a mutation dropping
  // the hook, or inverting the idx test, fails this.
  it('shows the hover readout only while the cursor is on the plot', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }), { status: 200 }),
    )
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    render()

    await vi.waitFor(() => expect(uplotCalls).toHaveLength(1))
    const { opts, el } = uplotCalls[0]
    const setCursor = opts.hooks.setCursor[0]

    // At rest — which is the state the reader sees on page load.
    setCursor({ cursor: { idx: null } })
    expect(el.classList.contains('chart-live')).toBe(false)

    setCursor({ cursor: { idx: 0 } })
    expect(el.classList.contains('chart-live')).toBe(true)

    // idx 0 is a real point, not "no point": a truthiness test instead of a
    // null test would leave the leftmost point of every chart unreadable.
    setCursor({ cursor: { idx: null } })
    expect(el.classList.contains('chart-live')).toBe(false)
  })
})
