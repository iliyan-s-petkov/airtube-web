<script>
  import { untrack } from 'svelte'
  import Chart from './Chart.svelte'
  import MetricPicker from './MetricPicker.svelte'
  import NearbyPicker from './NearbyPicker.svelte'
  import PeriodPicker from './PeriodPicker.svelte'
  import ResetButton from './ResetButton.svelte'
  import { unitFor } from '../lib/metrics.js'
  import { getScales, getSensorArea } from '../lib/sensors.svelte.js'
  import { nearbyOptions, nearbySources } from '../lib/nearby.js'
  import { CUSTOM, periodQuery } from '../lib/period.js'

  // The panel's chart and its controls: which metrics (several at once), which
  // window, and a reset back to the view the panel opened on.
  //
  // options are the metrics THIS STATION measures (the panel's own rows), not
  // the seven the map switches between: offering pressure for an address with
  // no barometer is a control that can only ever draw an empty frame.
  //
  // periods/periodLabels come from the server's own vocabulary — the API rejects
  // anything outside it, so a period written here would be a control that
  // returns 400.
  let {
    stationId, sources, options,
    periods, periodLabels, initialPeriod, initialMetric,
    metricLegend, periodLegend, customLabel, fromLabel, toLabel, nowLabel,
    resetLabel, rangeInvalid,
    nearbyLegend = '', nearbyOff = '', nearbySingleOnly = '', nearbyLabels = {},
    colours = [],
    timeLabel, empty, unavailable,
  } = $props()

  // Seeded from the server's defaults, owned here afterwards. The default
  // metric is the map's, and a station that does not measure it (a climate box
  // beside no particulate box) falls back to the first metric it does.
  const seedMetric = untrack(() =>
    (options.some((o) => o.metric === initialMetric) ? initialMetric : options[0]?.metric) ?? null)

  let metrics = $state(seedMetric ? [seedMetric] : [])
  let period = $state(untrack(() => initialPeriod))
  let from = $state('')
  let to = $state('')
  // Bumped by Reset, and only by Reset: remounting the chart is also what
  // discards the height the reader dragged the frame to.
  let resetToken = $state(0)
  // Which of the area's three lines the reader asked for. Empty is the default
  // and means "just this sensor" — the chart the panel has always drawn.
  let nearby = $state([])

  const labelOf = (m) => options.find((o) => o.metric === m)?.label ?? m
  const unitOf = (m) => unitFor(getScales(), m)

  const query = $derived(periodQuery(period, from, to))

  // Keyed by DEVICE, not by station: the series endpoint is per device, and the
  // temperature at this address was recorded by the box beside the particulate
  // one. sources carries that mapping (lib/sensors.svelte.js).
  const urlFor = (m) =>
    `/api/v1/sensor/${encodeURIComponent(sources?.[m] ?? stationId)}/series` +
    `?metric=${encodeURIComponent(m)}&${query}`

  // One y scale PER UNIT. Micrograms against degrees on one axis flattens
  // whichever has the smaller range into a straight line at the bottom of the
  // plot; two metrics counted in the same unit belong on one axis and would
  // otherwise be drawn against two different ranges of the same quantity.
  function scaleNames(list) {
    const byUnit = new Map()
    return list.map((m) => {
      const unit = unitOf(m)
      if (!byUnit.has(unit)) byUnit.set(unit, byUnit.size === 0 ? 'y' : `y${byUnit.size + 1}`)
      return byUnit.get(unit)
    })
  }

  // The area this sensor stands in, from the body the map already loaded. Absent
  // when the reader zoomed past the tier that carries it, and the control then
  // has no area to ask about.
  const areaSlug = $derived(getSensorArea())
  const bandable = $derived(metrics.length === 1 && !!areaSlug)

  const chartSources = $derived.by(() => {
    if (!query) return []
    const scales = scaleNames(metrics)
    const own = metrics.map((m, i) => ({
      url: urlFor(m),
      label: labelOf(m),
      colour: colours[i % colours.length],
      scale: scales[i],
      unit: unitOf(m),
    }))
    if (!bandable) return own

    // colours[1] is the compare colour, free here: the overlay only appears
    // while ONE metric is drawn, so nothing else is using it.
    return [...own, ...nearbySources({
      slug: areaSlug,
      metric: metrics[0],
      query,
      keys: nearby,
      colour: colours[1 % colours.length],
      scale: scales[0],
      unit: unitOf(metrics[0]),
      labels: nearbyLabels,
    })]
  })

  function reset() {
    metrics = seedMetric ? [seedMetric] : []
    nearby = []
    period = initialPeriod
    from = ''
    to = ''
    resetToken += 1
  }
</script>

<div class="panel-chart">
  <div class="panel-chart__controls">
    <MetricPicker
      {options}
      selected={metrics}
      onchange={(next) => { metrics = next }}
      legend={metricLegend}
    />
    {#if areaSlug}
      <NearbyPicker
        options={nearbyOptions(nearbyLabels)}
        selected={nearby}
        onchange={(next) => { nearby = next }}
        legend={nearbyLegend}
        offLabel={nearbyOff}
        disabled={!bandable}
        disabledHint={nearbySingleOnly}
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
      id="panel-period"
      onchange={(next) => { period = next.period; from = next.from; to = next.to }}
    />
    <!-- In the slot the old "Compare with" select had: the control that undoes
         everything the other two (and the drag handle) did. -->
    <ResetButton label={resetLabel} onreset={reset} />
  </div>

  {#if period === CUSTOM && !query}
    <p class="chart-message">{rangeInvalid}</p>
  {:else if chartSources.length}
    {#key resetToken}
      <Chart
        sources={chartSources}
        {timeLabel}
        {empty}
        {unavailable}
        resizable
        title=""
      />
    {/key}
  {/if}
</div>
