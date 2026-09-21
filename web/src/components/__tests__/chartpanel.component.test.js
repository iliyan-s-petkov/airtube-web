// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, unmount, tick } from 'svelte'
import ChartPanel from '../ChartPanel.svelte'
// lib/api.js caches by URL for the page's lifetime, and a test file is one
// "page": without this the second case to mount on 24h would be answered from
// the first case's cache and record no fetch at all.
import { clearCache } from '../../lib/api.js'
import { getViewState, resetViewStateForTests } from '../../lib/viewstate.svelte.js'
import { setSensors } from '../../lib/sensors.svelte.js'

// uPlot needs layout jsdom does not provide. This suite is about the panel's
// own two jobs — the composed heading and the period switcher's effect on the
// URL — so the plot itself is stubbed out. Imported (not just mocked) so the
// metric-menu tests below can read what Chart.svelte handed the constructor —
// the y-axis unit lives in the opts, not anywhere the DOM exposes with the
// plot itself faked out.
vi.mock('uplot', () => ({
  default: vi.fn(function () { this.setSize = vi.fn() }),
}))
import uPlot from 'uplot'

// Every fetch the panel makes, in order: the URL is the observable proof that
// picking a period changed what the chart asks for.
const urls = []

const props = {
  slug: 'sofia',
  selected: false,
  metric: 'P2',
  metricOptions: [{ metric: 'P2', label: 'PM2.5' }, { metric: 'P1', label: 'PM10' }],
  metricUnits: { P2: 'µg/m³', P1: 'µg/m³' },
  onMetricChange: () => {},
  metricLegend: 'Metric',
  periods: ['24h', '7d', '30d', '1y'],
  periodLabels: ['24 hours', '7 days', '30 days', '1 year'],
  initialPeriod: '24h',
  tier: 'province average',
  periodLegend: 'Period',
  customLabel: 'Custom range',
  fromLabel: 'From',
  toLabel: 'To',
  nowLabel: 'Now',
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
  vi.unstubAllGlobals()
  uPlot.mockClear()
  urls.length = 0
  clearCache()
  setSensors(null)
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
  // No sensor selected: the region chart mounts and asks for its own series.
  it('mounts and requests its series URL when nothing is selected', async () => {
    const target = render({ selected: false })
    expect(target.querySelector('.chart-controls')).not.toBeNull()
    await vi.waitFor(() => expect(urls).toHaveLength(1))
    expect(urls[0]).toContain('/api/v1/area/sofia/series')
  })

  // The kit's heading is metric · period · tier. Three separate parts, because
  // the middle one is rewritten when the reader picks another window. It now
  // renders as a caption below the chart frame, not a heading above it.
  it('composes the caption from the metric, the period and the tier', () => {
    const target = render()
    const caption = target.querySelector('.chart-caption')
    expect(caption.textContent).toBe('PM2.5 · 24 hours · province average')
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
    expect(target.querySelector('.chart-caption').textContent)
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

    target.querySelector('.chart-controls .chart-reset').click()
    await vi.waitFor(() => expect(periodSelect(target).value).toBe('24h'))
    expect(target.querySelector('.chart-caption').textContent)
      .toBe('PM2.5 · 24 hours · province average')
  })

  // The composed text sits below the chart it describes, so it is no longer
  // acting as a heading: an <h2> printed after its content breaks the page's
  // heading outline. It renders as a caption paragraph instead, and nothing
  // in the app references it as a heading (no aria-labelledby, no locator).
  it('renders the caption as a paragraph, not a section heading', () => {
    const target = render()
    expect(target.querySelector('h2')).toBeNull()
    expect(target.querySelector('p.chart-caption.t-caption')).not.toBeNull()
  })

  // The metric menu is the toolbar's own leftmost control now that the
  // heading no longer shares the row and pushes it right of centre.
  it('places the metric menu first among the toolbar controls', () => {
    const target = render()
    const controls = target.querySelector('.chart-controls')
    expect(controls.firstElementChild.querySelector('#area-chart-metric'))
      .not.toBeNull()
  })

  // The point of the control: picking another metric must reach the API and
  // relabel the heading and the y-axis, the same proof the period picker gets
  // above — but with the period held still. metric is wired the same way
  // switcher.js and map.js wire it in production — a getter over the shared
  // view state — so these cases prove the real reactivity path, not a prop
  // the test re-supplies by hand.
  describe('the metric menu', () => {
    const metricOptions = [
      { metric: 'P2', label: 'PM2.5' },
      { metric: 'temperature', label: 'Temperature' },
    ]
    const metricUnits = { P2: 'µg/m³', temperature: '°C' }
    const metricButton = (t) => t.querySelector('#area-chart-metric')
    const metricRadio = (t, metric) => t.querySelector(`input[type="radio"][value="${metric}"]`)

    // At least two points: Chart.svelte treats a single point as 'empty' and
    // never constructs uPlot, and the y-axis unit only reaches uPlot's opts.
    function fetchTwoPoints() {
      vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
        urls.push(String(url))
        return Promise.resolve(new Response(
          '{"t":["2026-08-01T00:00:00Z","2026-08-01T01:00:00Z"],"v":[1,2]}',
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ))
      })
      // Two real points reach uPlot construction (unlike the empty-payload
      // cases above), which wires a ResizeObserver jsdom does not provide.
      vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} })
    }

    function renderWithViewState() {
      resetViewStateForTests()
      // setMetric writes the real hash (see viewstate.svelte.js), so a
      // leftover hash from a prior case would seed the new store's metric
      // before this test's own click ever happens.
      history.replaceState(null, '', '/')
      fetchTwoPoints()
      const vs = getViewState({ metrics: metricOptions.map((o) => o.metric), defaultMetric: 'P2' })
      const target = document.createElement('div')
      document.body.appendChild(target)
      component = mount(ChartPanel, {
        target,
        props: {
          ...props,
          metricOptions,
          metricUnits,
          onMetricChange: (m) => vs.setMetric(m),
          get metric() { return vs.metric },
        },
      })
      return { target, vs }
    }

    afterEach(() => resetViewStateForTests())

    it('offers a menu when the area reports more than one metric', () => {
      const { target } = renderWithViewState()
      expect(metricButton(target)).not.toBeNull()
    })

    it('changes the requested URL to the chosen metric, keeping the period', async () => {
      const { target } = renderWithViewState()
      await vi.waitFor(() => expect(urls).toHaveLength(1))
      expect(urls[0]).toContain('metric=P2')
      expect(urls[0]).toContain('period=24h')

      metricButton(target).click()
      metricRadio(target, 'temperature').click()

      await vi.waitFor(() => expect(urls).toHaveLength(2))
      expect(urls[1]).toContain('metric=temperature')
      expect(urls[1]).toContain('period=24h')
    })

    it('updates the heading label and the y-axis unit for the chosen metric', async () => {
      const { target } = renderWithViewState()
      await vi.waitFor(() => expect(urls).toHaveLength(1))

      metricButton(target).click()
      metricRadio(target, 'temperature').click()

      await vi.waitFor(() => expect(target.querySelector('.chart-caption').textContent)
        .toBe('Temperature · 24 hours · province average'))
      await vi.waitFor(() => expect(uPlot).toHaveBeenCalledTimes(2))
      const secondCallOpts = uPlot.mock.calls[1][0]
      expect(secondCallOpts.axes[1].label).toBe('°C')
    })

    // The chart follows a metric change made through the shared view state —
    // i.e. by the top switcher, not only through its own menu.
    it('follows a metric change made through the shared view state', async () => {
      const { target, vs } = renderWithViewState()
      await vi.waitFor(() => expect(urls).toHaveLength(1))

      vs.setMetric('temperature')
      await tick()

      await vi.waitFor(() => expect(urls).toHaveLength(2))
      expect(urls[1]).toContain('metric=temperature')
      expect(target.querySelector('.chart-caption').textContent)
        .toBe('Temperature · 24 hours · province average')
    })

    it('renders without a menu when the area reports a single metric', () => {
      const target = render({
        metricOptions: [{ metric: 'P2', label: 'PM2.5' }],
        metricUnits: { P2: 'µg/m³' },
      })
      expect(metricButton(target)).toBeNull()
      expect(target.querySelector('.chart-caption').textContent)
        .toBe('PM2.5 · 24 hours · province average')
    })

    // The page-wide metric is seeded from the SITE default (here 'P2'), which
    // this area does not measure — only P1. The chart must not write that
    // correction back to the shared metric (that was the regression: it
    // stomped an explicit deep link and the switcher's own choice for the
    // whole page — see web/e2e/metric.spec.js "a deep-linked metric is
    // selected on load"). Instead it renders its own unavailable state and
    // leaves the shared metric alone; the top switcher remains the way back
    // to a metric this area does measure.
    describe('an area measuring only a non-default metric', () => {
      function renderPm10Only() {
        resetViewStateForTests()
        history.replaceState(null, '', '/')
        fetchTwoPoints()
        const vs = getViewState({ metrics: ['P2', 'P1'], defaultMetric: 'P2' })
        const target = document.createElement('div')
        document.body.appendChild(target)
        component = mount(ChartPanel, {
          target,
          props: {
            ...props,
            metricOptions: [{ metric: 'P1', label: 'PM10' }],
            metricUnits: { P1: 'µg/m³' },
            onMetricChange: (m) => vs.setMetric(m),
            get metric() { return vs.metric },
          },
        })
        return { target, vs }
      }

      afterEach(() => resetViewStateForTests())

      // The regression, as a component test: the deep-linked/default metric
      // this area does not measure must not be rewritten. The top switcher
      // (vs.metric) still names it, even though this chart cannot plot it.
      it('leaves the shared view state on the page-wide metric, unmeasured or not', () => {
        const { vs } = renderPm10Only()
        expect(vs.metric).toBe('P2')
      })

      // No heading naming a metric this area does not report, no y-axis unit
      // lookup miss, and no request for a series that cannot exist — the
      // chart renders the unavailable state instead.
      it('renders the unavailable state instead of the chart, and fetches nothing', async () => {
        const { target } = renderPm10Only()

        expect(target.querySelector('.chart-controls')).toBeNull()
        expect(target.querySelector('.data-frame .chart-message').textContent)
          .toBe(props.unavailable)
        await Promise.resolve()
        expect(urls).toHaveLength(0)
        expect(uPlot).not.toHaveBeenCalled()
      })

      // A metric change made through the top switcher (the shared view
      // state) still reaches the chart normally once it names something this
      // area measures.
      it('renders the chart once the shared metric moves to one this area measures', async () => {
        const { target, vs } = renderPm10Only()
        expect(target.querySelector('.chart-controls')).toBeNull()

        vs.setMetric('P1')
        await tick()

        await vi.waitFor(() => expect(target.querySelector('.chart-caption')?.textContent)
          .toBe('PM10 · 24 hours · province average'))
        await vi.waitFor(() => expect(urls.at(-1)).toContain('metric=P1'))
      })
    })

    // No measured metric at all (see internal/web/render.go's
    // TestChartIslandOffersNoMetricsForASilentArea for the server-side half
    // of this contract): metricOptions.some() over an empty list is always
    // false, so this falls into the same unavailable branch as an area that
    // measures something but not the page's current metric — there is no
    // menu, no toolbar, no caption, and the shared metric is left alone.
    it('renders the unavailable state and does not touch the shared metric when the area measures nothing', () => {
      resetViewStateForTests()
      history.replaceState(null, '', '/')
      const vs = getViewState({ metrics: ['P2', 'P1'], defaultMetric: 'P2' })
      const target = render({
        metricOptions: [],
        metricUnits: {},
        onMetricChange: (m) => vs.setMetric(m),
        get metric() { return vs.metric },
      })
      expect(metricButton(target)).toBeNull()
      expect(target.querySelector('.chart-controls')).toBeNull()
      expect(target.querySelector('.data-frame .chart-message').textContent).toBe(props.unavailable)
      expect(vs.metric).toBe('P2')
      resetViewStateForTests()
    })
  })

  // The sensor card renders in the same slot when a sensor is open; this
  // region-wide chart must yield to it entirely, not sit underneath it.
  describe('a sensor is selected', () => {
    it('mounts nothing: no caption, no toolbar, no data frame', () => {
      const target = render({ selected: true })
      expect(target.querySelector('.chart-caption')).toBeNull()
      expect(target.querySelector('.chart-controls')).toBeNull()
      expect(target.querySelector('.data-frame')).toBeNull()
    })

    it('makes no series request while hidden', async () => {
      render({ selected: true })
      await Promise.resolve()
      expect(urls).toHaveLength(0)
    })

    // Real vs.sensorId/findSensor, the same wiring islands/chart.js uses, so
    // this proves the close button's actual path: openSensor/closeSensor
    // toggling `selected`, not a hand-supplied boolean.
    describe('closing the card', () => {
      afterEach(() => resetViewStateForTests())

      it('restores the chart with the metric and period it had, not the defaults', async () => {
        resetViewStateForTests()
        history.replaceState(null, '', '/')
        setSensors({ sensors: { id: [42], quality: ['ok'], P2: [1] } })
        const vs = getViewState({ metrics: ['P2', 'temperature'], defaultMetric: 'P2' })
        const target = document.createElement('div')
        document.body.appendChild(target)
        vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
          urls.push(String(url))
          return Promise.resolve(new Response('{"points":[]}', {
            status: 200, headers: { 'Content-Type': 'application/json' },
          }))
        })
        component = mount(ChartPanel, {
          target,
          props: {
            ...props,
            get selected() { return vs.sensorId === 42 },
            get metric() { return vs.metric },
            onMetricChange: (m) => vs.setMetric(m),
          },
        })
        await vi.waitFor(() => expect(urls).toHaveLength(1))

        setValue(periodSelect(target), '7d')
        await vi.waitFor(() => expect(urls).toHaveLength(2))
        expect(urls[1]).toContain('period=7d')

        vs.openSensor(42)
        await tick()
        expect(target.querySelector('.chart-controls')).toBeNull()

        vs.closeSensor()
        await tick()
        expect(periodSelect(target).value).toBe('7d')
        expect(target.querySelector('.chart-caption').textContent)
          .toBe('PM2.5 · 7 days · province average')
      })
    })

    it('selecting a second sensor while one is open still renders nothing', async () => {
      resetViewStateForTests()
      history.replaceState(null, '', '/')
      setSensors({ sensors: { id: [42, 43], quality: ['ok', 'ok'], P2: [1, 2] } })
      const vs = getViewState({ metrics: ['P2'], defaultMetric: 'P2' })
      const target = document.createElement('div')
      document.body.appendChild(target)
      vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
        urls.push(String(url))
        return Promise.resolve(new Response('{"points":[]}', {
          status: 200, headers: { 'Content-Type': 'application/json' },
        }))
      })
      component = mount(ChartPanel, {
        target,
        props: { ...props, get selected() { return vs.sensorId === 42 || vs.sensorId === 43 } },
      })
      vs.openSensor(42)
      await tick()
      expect(target.querySelector('.chart-controls')).toBeNull()

      vs.openSensor(43)
      await tick()
      expect(target.querySelector('.chart-controls')).toBeNull()
      expect(target.querySelectorAll('.data-frame')).toHaveLength(0)
      resetViewStateForTests()
    })
  })
})
