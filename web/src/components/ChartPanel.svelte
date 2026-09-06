<script>
  import { untrack } from 'svelte'
  import Chart from './Chart.svelte'
  import MetricSwitcher from './MetricSwitcher.svelte'

  // periods/labels arrive as parallel lists from the server (the config's own
  // vocabulary), never as a list written here: the API rejects any period
  // outside it, so a hard-coded option is a button that returns 400.
  let {
    slug, metric, periods, periodLabels, initialPeriod,
    metricLabel, tier, periodLegend,
    lineColour, valueLabel, timeLabel, empty, unavailable,
  } = $props()

  // Seeded from the server's default and owned here after that — untrack says
  // so out loud, which is also what silences Svelte's state_referenced_locally
  // warning about reading a prop into state.
  let period = $state(untrack(() => initialPeriod))

  // MetricSwitcher is the radio set for "one of these, mutually exclusive";
  // only its file name is about metrics, and its option key is `metric`.
  const options = $derived(periods.map((p, i) => ({ metric: p, label: periodLabels[i] ?? p })))

  const periodLabel = $derived(periodLabels[periods.indexOf(period)] ?? period)
  // Metric · period · tier, the kit's own heading (§ area-detail). Composed
  // here rather than server-side because the middle part changes when the
  // reader picks another window, and a pre-composed sentence cannot be
  // rewritten without shipping the catalogue to the browser.
  const heading = $derived([metricLabel, periodLabel, tier].filter(Boolean).join(' · '))

  const url = $derived(
    `/api/v1/area/${encodeURIComponent(slug)}/series` +
    `?metric=${encodeURIComponent(metric)}&period=${encodeURIComponent(period)}`,
  )
</script>

<div class="chart-head">
  <h2 class="t-section">{heading}</h2>
  <div class="chart-controls">
    <MetricSwitcher
      {options}
      selected={period}
      onselect={(p) => { period = p }}
      legend={periodLegend}
      name="chart-window"
    />
  </div>
</div>

<!-- No {#key url} around this: Chart's effect already re-runs on a new url,
     destroying the old plot and returning itself to 'loading', so remounting
     the component would only repeat work the effect does.

     title="" because the heading above IS the title — uPlot would otherwise
     paint a second copy of it inside the plot. -->
<div class="data-frame chart">
  <Chart
    {url}
    {lineColour}
    {valueLabel}
    {timeLabel}
    {empty}
    {unavailable}
    title=""
  />
</div>
