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
  customLabel: 'Custom range',
  fromLabel: 'From',
  toLabel: 'To',
  resetLabel: 'Reset view',
  rangeInvalid: 'Choose a start and an end.',
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

function setValue(el, value) {
  el.value = value
  el.dispatchEvent(new Event('change', { bubbles: true }))
}

const periodSelect = (target) => target.querySelector('#area-period-select')

describe('ChartPanel.svelte', () => {
  // The kit's heading is metric · period · tier. Three separate parts, because
  // the middle one is rewritten when the reader picks another window.
  it('composes the heading from the metric, the period and the tier', () => {
    const target = render()
    const h2 = target.querySelector('.chart-head .t-section')
    expect(h2.textContent).toBe('PM2.5 · 24 hours · province average')
  })

  // A select, not a row of segments: the list now carries a custom range too,
  // and the option in force is the one the server opened the page on.
  it('offers one option per configured period, plus the custom range', () => {
    const target = render()
    const options = [...target.querySelectorAll('#area-period-select option')]
    expect(options.map((o) => o.value)).toEqual(['24h', '7d', '30d', '1y', 'custom'])
    expect(options.map((o) => o.textContent))
      .toEqual(['24 hours', '7 days', '30 days', '1 year', 'Custom range'])
    expect(periodSelect(target).value).toBe('24h')
  })

  // The label is what tells a screen reader what the select is for.
  it('labels the switcher with the server-supplied legend', () => {
    const target = render()
    expect(target.querySelector('label[for="area-period-select"]').textContent).toBe('Period')
  })

  // The point of the control: a new period must reach the API, and the heading
  // must stop claiming the old window.
  it('refetches and relabels when another period is picked', async () => {
    const target = render()
    await vi.waitFor(() => expect(urls).toHaveLength(1))
    expect(urls[0]).toContain('period=24h')

    setValue(periodSelect(target), '7d')

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
    setValue(periodSelect(target), '30d')

    await vi.waitFor(() => expect(target.textContent).not.toContain(props.empty))
  })

  // The panel and the area chart both mount a PeriodPicker on the same page.
  // Distinct ids are what keep each label pointing at its own select.
  it('gives the area switcher its own id', () => {
    const target = render()
    expect(periodSelect(target)).not.toBeNull()
    expect(target.querySelector('#panel-period-select')).toBeNull()
  })

  it('asks for the custom window only once both ends are set, earliest first', async () => {
    const target = render()
    await vi.waitFor(() => expect(urls).toHaveLength(1))

    setValue(periodSelect(target), 'custom')
    await vi.waitFor(() => expect(target.querySelector('#area-period-from')).not.toBeNull())
    setValue(target.querySelector('#area-period-from'), '2026-08-15T00:00')
    setValue(target.querySelector('#area-period-to'), '2026-08-14T00:00')
    await Promise.resolve()
    expect(urls).toHaveLength(1)
    expect(target.querySelector('.chart-message').textContent).toContain(props.rangeInvalid)

    setValue(target.querySelector('#area-period-to'), '2026-08-16T00:00')
    await vi.waitFor(() => expect(urls).toHaveLength(2))
    expect(urls[1]).toContain('period=custom')
    expect(urls[1]).toContain('from=')
    expect(urls[1]).toContain('to=')
  })

  it('reset returns the window to the one the page opened on', async () => {
    const target = render()
    await vi.waitFor(() => expect(urls).toHaveLength(1))

    setValue(periodSelect(target), '7d')
    await vi.waitFor(() => expect(urls).toHaveLength(2))

    target.querySelector('.chart-controls button.btn--secondary').click()
    await vi.waitFor(() => expect(periodSelect(target).value).toBe('24h'))
    expect(target.querySelector('.chart-head .t-section').textContent)
      .toBe('PM2.5 · 24 hours · province average')
  })

  // The heading is an <h2>, not a styled <div>: it is the section's place in
  // the document outline, which the page's heading-order test depends on.
  it('gives the chart a real section heading', () => {
    const target = render()
    expect(target.querySelector('.chart-head > h2')).not.toBeNull()
  })
})
