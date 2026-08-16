// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import Chart from '../Chart.svelte'

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
})
