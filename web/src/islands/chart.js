// The chart island is only a mount point: every decision lives in
// ChartPanel.svelte, which owns the selected period and therefore the URL.
import { mount as mountComponent } from 'svelte'
import ChartPanel from '../components/ChartPanel.svelte'

// Positional lists, comma-joined by the server — the same shape the metric
// switcher reads. An empty attribute must not become [''].
const list = (s) => (s ? s.split(',') : [])

export function mount(el) {
  const d = el.dataset
  if (!d.slug) return // nothing to draw; leave the server-rendered aggregate
  // No fallbacks on the server-rendered values: the server always renders
  // data-metric, data-period and data-periods, so a missing one must surface as
  // a visible failure, not a quiet substitution.
  mountComponent(ChartPanel, {
    target: el,
    props: {
      slug: d.slug,
      metric: d.metric,
      periods: list(d.periods),
      periodLabels: list(d.periodLabels),
      initialPeriod: d.period,
      metricLabel: d.tMetric || '',
      tier: d.tTier || '',
      periodLegend: d.tPeriodLegend || '',
      lineColour: d.lineColour,
      valueLabel: d.tValue || '',
      timeLabel: d.tTime || '',
      empty: d.tEmpty || '',
      unavailable: d.tUnavailable || '',
    },
  })
}
