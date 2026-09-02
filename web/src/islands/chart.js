// The chart island is now only a mount point: every decision lives in
// Chart.svelte, and the URL is built here because the dataset is the island's
// business, not the component's.
import { mount as mountComponent } from 'svelte'
import Chart from '../components/Chart.svelte'

export function mount(el) {
  const d = el.dataset
  if (!d.slug) return // nothing to draw; leave the server-rendered aggregate
  // No fallbacks: the server always renders data-metric and data-period, so a
  // missing one must surface as a visible failure, not a quiet substitution.
  const url = `/api/v1/area/${encodeURIComponent(d.slug)}/series` +
    `?metric=${encodeURIComponent(d.metric)}&period=${encodeURIComponent(d.period)}`
  mountComponent(Chart, {
    target: el,
    props: {
      url,
      lineColour: d.lineColour,
      title: d.tTitle || '',
      valueLabel: d.tValue || '',
      timeLabel: d.tTime || '',
      empty: d.tEmpty || '',
      unavailable: d.tUnavailable || '',
    },
  })
}
