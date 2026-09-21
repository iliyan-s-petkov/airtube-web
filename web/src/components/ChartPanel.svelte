<script>
  import { untrack } from 'svelte'
  import Chart from './Chart.svelte'
  import PeriodPicker from './PeriodPicker.svelte'
  import MetricMenu from './MetricMenu.svelte'
  import ResetButton from './ResetButton.svelte'
  import { CUSTOM, periodQuery } from '../lib/period.js'

  // periods/labels arrive as parallel lists from the server (the config's own
  // vocabulary), never as a list written here: the API rejects any period
  // outside it, so a hard-coded option is a button that returns 400.
  //
  // metric is NOT owned here the way period is: it lives in the shared view
  // state (islands/chart.js passes it as a getter), so a change from the
  // page's top switcher reaches this chart too — one metric for the whole
  // page, not a private copy that can drift from it. metricOptions/metricUnits
  // resolve the heading label and the y-axis unit from that metric without a
  // round trip to the server.
  let {
    slug, selected, metric, metricOptions, metricUnits, onMetricChange, metricLegend,
    periods, periodLabels, initialPeriod,
    tier, periodLegend, customLabel, fromLabel, toLabel, nowLabel,
    resetLabel, rangeInvalid,
    lineColour, valueLabel, timeLabel, empty, unavailable,
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
  const metricLabel = $derived(metricOptions.find((o) => o.metric === metric)?.label ?? metric)
  const valueUnit = $derived(metricUnits[metric] ?? '')
  // Metric · period · tier, the kit's own heading (§ area-detail). Composed
  // here rather than server-side because the middle part changes when the
  // reader picks another window, the first when they pick another metric, and
  // a pre-composed sentence cannot be rewritten without shipping the
  // catalogue to the browser.
  const heading = $derived([metricLabel, periodLabel, tier].filter(Boolean).join(' · '))

  const url = $derived(
    `/api/v1/area/${encodeURIComponent(slug)}/series` +
    `?metric=${encodeURIComponent(metric)}&${query}`,
  )

  // metric is seeded from the SITE default and can be moved to any site
  // metric by the top switcher — neither is constrained to what this area
  // measures. When it names a metric outside metricOptions, correct it
  // through onMetricChange (the same setter the switcher itself writes
  // through) rather than falling back locally: that keeps the heading, the
  // y-axis unit, the map and the switcher all agreeing on one metric instead
  // of this chart quietly plotting one the rest of the page disagrees with.
  // Skipped when metricOptions is empty (area measures nothing) — there is no
  // measured metric to fall back to, so the chart keeps its existing
  // unavailable state for the unconstrained metric.
  $effect(() => {
    if (metricOptions.length > 0 && !metricOptions.some((o) => o.metric === metric)) {
      onMetricChange(metricOptions[0].metric)
    }
  })

  function reset() {
    period = initialPeriod
    from = ''
    to = ''
    resetToken += 1
    // The metric is not reset: it is the page-wide selection, and Reset only
    // undoes what this chart's own period controls did.
  }
</script>

<!-- While a sensor is selected, the sensor card is the whole view: this
     region-wide chart renders nothing, not an empty frame. `period` and
     `resetToken` above stay declared regardless — hiding here, at the
     template level, leaves that $state untouched, so closing the card
     restores the same window without a save/restore dance. -->
{#if !selected}
<div class="chart-head">
  <h2 class="t-section">{heading}</h2>
  <div class="chart-controls">
    {#if metricOptions.length > 1}
      <!-- Hidden for a one-metric area: a menu whose only option is already
           selected offers nothing, and the heading already names the metric. -->
      <MetricMenu
        options={metricOptions}
        selected={metric}
        onselect={onMetricChange}
        legend={metricLegend}
        id="area-chart-metric"
        name="area-chart-metric"
      />
    {/if}
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
{/if}
