// The chart island is only a mount point: every decision lives in
// ChartPanel.svelte, which owns the selected period and therefore the URL.
// The metric is the one exception — it lives in the shared view state (see
// lib/viewstate.svelte.js) so the page's top switcher and this island's own
// menu both drive the same value.
import { mount as mountComponent } from 'svelte'
import ChartPanel from '../components/ChartPanel.svelte'
import { parseMetricList, zipLabels } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { findSensor } from '../lib/sensors.svelte.js'

// Positional lists, comma-joined by the server — the same shape the metric
// switcher reads. An empty attribute must not become [''].
const list = (s) => (s ? s.split(',') : [])

export function mount(el) {
  const d = el.dataset
  if (!d.slug) return // nothing to draw; leave the server-rendered aggregate
  // The site's whole metric vocabulary, only so this island's call to
  // getViewState validates a metric the same way the switcher and map
  // islands do, whichever of the three constructs the shared store first —
  // see the store's own module-level-singleton comment.
  const vs = getViewState({ metrics: parseMetricList(d.metrics), defaultMetric: d.metric })
  // What THIS area reports, positional against its own labels and units —
  // the menu offers only these, never the global list above.
  const areaMetrics = parseMetricList(d.areaMetrics)
  const areaMetricOptions = zipLabels(areaMetrics, list(d.areaMetricLabels))
  const areaMetricUnits = list(d.areaMetricUnits)
  // No fallbacks on the server-rendered values: the server always renders
  // data-metric, data-period and data-periods, so a missing one must surface as
  // a visible failure, not a quiet substitution.
  mountComponent(ChartPanel, {
    target: el,
    props: {
      slug: d.slug,
      // Same getter-prop idiom islands/panel.js uses for `open`: a sensor is
      // "selected" only if the id resolves through the registry, not merely
      // because vs.sensorId is non-null (a stale/unknown id must not blank
      // this chart).
      get selected() { return findSensor(vs.sensorId) !== null },
      get metric() { return vs.metric },
      metricOptions: areaMetricOptions,
      // Positional against areaMetrics, read by ChartPanel to re-resolve the
      // y-axis unit whenever the metric changes.
      metricUnits: Object.fromEntries(areaMetrics.map((m, i) => [m, areaMetricUnits[i] || ''])),
      onMetricChange: (m) => vs.setMetric(m),
      metricLegend: d.tLegend || '',
      periods: list(d.periods),
      periodLabels: list(d.periodLabels),
      initialPeriod: d.period,
      tier: d.tTier || '',
      periodLegend: d.tPeriodLegend || '',
      customLabel: d.tPeriodCustom || '',
      fromLabel: d.tPeriodFrom || '',
      toLabel: d.tPeriodTo || '',
      nowLabel: d.tPeriodNow || '',
      resetLabel: d.tReset || '',
      rangeInvalid: d.tRangeInvalid || '',
      lineColour: d.lineColour,
      valueLabel: d.tValue || '',
      timeLabel: d.tTime || '',
      empty: d.tEmpty || '',
      unavailable: d.tUnavailable || '',
    },
  })
}
