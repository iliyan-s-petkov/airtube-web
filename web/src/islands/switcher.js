// The switcher owns no state: it renders what the store says and calls back
// into it. The store, not this island, decides that a metric switch is a
// replaceState — see lib/viewstate.svelte.js.
import { mount as mountComponent } from 'svelte'
import MetricSwitcher from '../components/MetricSwitcher.svelte'
import { parseMetricList, zipLabels } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'

export function mount(el) {
  const d = el.dataset
  const metrics = parseMetricList(d.metrics)
  const vs = getViewState({ metrics, defaultMetric: d.metric })
  mountComponent(MetricSwitcher, {
    target: el,
    props: {
      options: zipLabels(metrics, parseMetricList(d.metricLabels)),
      legend: d.tLegend,
      get selected() { return vs.metric },
      onselect: (m) => vs.setMetric(m),
    },
  })
}
