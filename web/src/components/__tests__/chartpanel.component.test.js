// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import ChartPanel from '../ChartPanel.svelte'
// lib/api.js caches by URL for the page's lifetime, and a test file is one
// "page": without this the second case to mount on 24h would be answered from
// the first case's cache and record no fetch at all.
import { clearCache } from '../../lib/api.js'

// uPlot needs layout jsdom does not provide. This suite is about the panel's
// own two jobs — the composed heading and the period switcher's effect on the
// URL — so the plot itself is stubbed out.
vi.mock('uplot', () => ({
  default: vi.fn(function () { this.setSize = vi.fn() }),
}))

// Every fetch the panel makes, in order: the URL is the observable proof that
// picking a period changed what the chart asks for.
const urls = []

const props = {
  slug: 'sofia',
  metric: 'P2',
  periods: ['24h', '7d', '30d', '1y'],
  periodLabels: ['24 hours', '7 days', '30 days', '1 year'],
  initialPeriod: '24h',
  metricLabel: 'PM2.5',
  tier: 'province average',
  periodLegend: 'Period',
  lineColour: '#2563eb',
  valueLabel: 'µg/m³',
  timeLabel: 'Time',
  empty: 'No readings in the selected period',
  unavailable: 'Data is unavailable right now',
}

let component
afterEach(() => {
  if (component) unmount(component)
  vi.restoreAllMocks()
  urls.length = 0
  clearCache()
})

function render(extra) {
  vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
    urls.push(String(url))
    return Promise.resolve(new Response('{"points":[]}', {
      status: 200, headers: { 'Content-Type': 'application/json' },
    }))
  })
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(ChartPanel, { target, props: { ...props, ...extra } })
  return target
}

describe('ChartPanel.svelte', () => {
  // The kit's heading is metric · period · tier. Three separate parts, because
  // the middle one is rewritten when the reader picks another window.
  it('composes the heading from the metric, the period and the tier', () => {
    const target = render()
    const h2 = target.querySelector('.chart-head .t-section')
    expect(h2.textContent).toBe('PM2.5 · 24 hours · province average')
  })

  // A radio set, not a row of buttons: the periods are mutually exclusive, and
  // the one in force must be announced as selected rather than merely painted.
  it('offers one radio per configured period, with the current one checked', () => {
    const target = render()
    const inputs = [...target.querySelectorAll('.chart-controls input[type=radio]')]
    expect(inputs.map((i) => i.value)).toEqual(['24h', '7d', '30d', '1y'])
    expect(inputs.map((i) => i.nextElementSibling.textContent))
      .toEqual(['24 hours', '7 days', '30 days', '1 year'])
    expect(inputs.filter((i) => i.checked).map((i) => i.value)).toEqual(['24h'])
  })

  // The legend is what tells a screen reader what the group of radios is for.
  it('labels the switcher with the server-supplied legend', () => {
    const target = render()
    expect(target.querySelector('.chart-controls legend').textContent).toBe('Period')
  })

  // The point of the control: a new period must reach the API, and the heading
  // must stop claiming the old window.
  it('refetches and relabels when another period is picked', async () => {
    const target = render()
    await vi.waitFor(() => expect(urls).toHaveLength(1))
    expect(urls[0]).toContain('period=24h')

    const weekly = target.querySelector('input[value="7d"]')
    weekly.checked = true
    weekly.dispatchEvent(new Event('change', { bubbles: true }))

    await vi.waitFor(() => expect(urls).toHaveLength(2))
    expect(urls[1]).toContain('/api/v1/area/sofia/series')
    expect(urls[1]).toContain('metric=P2')
    expect(urls[1]).toContain('period=7d')
    expect(target.querySelector('.chart-head .t-section').textContent)
      .toBe('PM2.5 · 7 days · province average')
  })

  // "No readings in the selected period" is a claim about the period on
  // screen. Once the reader picks another one, that claim is about a window
  // nobody has asked the server about yet, so it must go before the answer
  // arrives — not when it arrives.
  it('drops the previous window\'s empty message while the new one loads', async () => {
    const target = render()
    await vi.waitFor(() => expect(target.textContent).toContain(props.empty))

    // A request that never settles: what is on screen in the meantime is
    // exactly what this case is about.
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise(() => {}))
    const monthly = target.querySelector('input[value="30d"]')
    monthly.checked = true
    monthly.dispatchEvent(new Event('change', { bubbles: true }))

    await vi.waitFor(() => expect(target.textContent).not.toContain(props.empty))
  })

  // The switcher's name groups the radios. Two groups sharing a name on one
  // page silently become one, so this pins the chart's own.
  it('keeps the chart switcher in its own radio group', () => {
    const target = render()
    const input = target.querySelector('.chart-controls input[type=radio]')
    expect(input.name).toBe('chart-window')
    expect(input.name).not.toBe('metric')
  })

  // The heading is an <h2>, not a styled <div>: it is the section's place in
  // the document outline, which the page's heading-order test depends on.
  it('gives the chart a real section heading', () => {
    const target = render()
    expect(target.querySelector('.chart-head > h2')).not.toBeNull()
  })
})
