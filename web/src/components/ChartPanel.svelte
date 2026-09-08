<script>
  import { untrack } from 'svelte'
  import Chart from './Chart.svelte'
  import PeriodPicker from './PeriodPicker.svelte'
  import ResetButton from './ResetButton.svelte'
  import { CUSTOM, periodQuery } from '../lib/period.js'

  // periods/labels arrive as parallel lists from the server (the config's own
  // vocabulary), never as a list written here: the API rejects any period
  // outside it, so a hard-coded option is a button that returns 400.
  let {
    slug, metric, periods, periodLabels, initialPeriod,
    metricLabel, tier, periodLegend, customLabel, fromLabel, toLabel, nowLabel,
    resetLabel, rangeInvalid,
    lineColour, valueLabel, valueUnit = '', timeLabel, empty, unavailable,
  } = $props()

  // Seeded from the server's default and owned here after that — untrack says
  // so out loud, which is also what silences Svelte's state_referenced_locally
  // warning about reading a prop into state.
  let period = $state(untrack(() => initialPeriod))
  let from = $state('')
  let to = $state('')
  // Bumped by Reset: a remount also discards the height the reader dragged to.
  let resetToken = $state(0)

  const query = $derived(periodQuery(period, from, to))
  const periodLabel = $derived(
    period === CUSTOM ? customLabel : (periodLabels[periods.indexOf(period)] ?? period))
  // Metric · period · tier, the kit's own heading (§ area-detail). Composed
  // here rather than server-side because the middle part changes when the
  // reader picks another window, and a pre-composed sentence cannot be
  // rewritten without shipping the catalogue to the browser.
  const heading = $derived([metricLabel, periodLabel, tier].filter(Boolean).join(' · '))

  const url = $derived(
    `/api/v1/area/${encodeURIComponent(slug)}/series` +
    `?metric=${encodeURIComponent(metric)}&${query}`,
  )

  function reset() {
    period = initialPeriod
    from = ''
    to = ''
    resetToken += 1
  }
</script>

<div class="chart-head">
  <h2 class="t-section">{heading}</h2>
  <div class="chart-controls">
    <PeriodPicker
      {periods}
      {periodLabels}
      {period}
      {from}
      {to}
      legend={periodLegend}
      {customLabel}
      {fromLabel}
      {toLabel}
      {nowLabel}
      id="area-period"
      onchange={(next) => { period = next.period; from = next.from; to = next.to }}
    />
    <ResetButton label={resetLabel} onreset={reset} />
  </div>
</div>

<!-- No {#key url} around this: Chart's effect already re-runs on a new url,
     destroying the old plot and returning itself to 'loading', so remounting
     the component would only repeat work the effect does.

     title="" because the heading above IS the title — uPlot would otherwise
     paint a second copy of it inside the plot. -->
<div class="data-frame chart">
  {#if period === CUSTOM && !query}
    <p class="chart-message">{rangeInvalid}</p>
  {:else}
    {#key resetToken}
      <Chart
        {url}
        {lineColour}
        {valueLabel}
        {valueUnit}
        {timeLabel}
        {empty}
        {unavailable}
        resizable
        title=""
      />
    {/key}
  {/if}
</div>
