<script>
  import { untrack } from 'svelte'
  import Chart from './Chart.svelte'
  import MetricSwitcher from './MetricSwitcher.svelte'
  import { unitFor } from '../lib/metrics.js'
  import { getScales } from '../lib/sensors.svelte.js'

  // The panel's chart and its controls: which metric, which window, and
  // optionally a second metric drawn against it.
  //
  // options are the metrics THIS STATION measures (the panel's own rows), not
  // the seven the map switches between: offering pressure for an address with
  // no barometer is a control that can only ever draw an empty frame.
  //
  // periods/periodLabels come from the server's own vocabulary (the same pair
  // ChartPanel takes) — the API rejects anything outside it, so a period
  // written here would be a button that returns 400.
  let {
    stationId, sources, options,
    periods, periodLabels, initialPeriod, initialMetric,
    metricLegend, periodLegend, compareLabel, compareNone,
    primaryColour, compareColour,
    timeLabel, empty, unavailable,
  } = $props()

  // Seeded from the server's defaults, owned here afterwards. The default
  // metric is the map's, and a station that does not measure it (a climate box
  // beside no particulate box) falls back to the first metric it does.
  const seedMetric = untrack(() =>
    (options.some((o) => o.metric === initialMetric) ? initialMetric : options[0]?.metric) ?? null)
  let metric = $state(seedMetric)
  let period = $state(untrack(() => initialPeriod))
  // '' is "no comparison", the state the panel opens in: one line, one axis,
  // and a second request only once the reader asks for one.
  let compare = $state('')

  const periodOptions = $derived(periods.map((p, i) => ({ metric: p, label: periodLabels[i] ?? p })))
  const compareOptions = $derived(options.filter((o) => o.metric !== metric))
  const labelOf = (m) => options.find((o) => o.metric === m)?.label ?? m

  // Keyed by DEVICE, not by station: the series endpoint is per device, and the
  // temperature at this address was recorded by the box beside the particulate
  // one. sources carries that mapping (lib/sensors.svelte.js).
  const urlFor = (m) =>
    `/api/v1/sensor/${encodeURIComponent(sources?.[m] ?? stationId)}/series` +
    `?metric=${encodeURIComponent(m)}&period=${encodeURIComponent(period)}`

  // The compared metric gets its own y scale. Micrograms against degrees on one
  // axis flattens whichever has the smaller range into a straight line at the
  // bottom of the plot — a chart that says "nothing happens here" about data
  // that moved all day.
  // Same scales table the panel's rows take their units from.
  const unitOf = (m) => unitFor(getScales(), m)

  const chartSources = $derived([
    ...(metric
      ? [{ url: urlFor(metric), label: labelOf(metric), colour: primaryColour, scale: 'y', unit: unitOf(metric) }]
      : []),
    ...(compare
      ? [{ url: urlFor(compare), label: labelOf(compare), colour: compareColour, scale: 'y2', unit: unitOf(compare) }]
      : []),
  ])
</script>

<div class="panel-chart">
  <div class="panel-chart__controls">
    <MetricSwitcher
      options={options}
      selected={metric}
      onselect={(m) => { if (compare === m) compare = ''; metric = m }}
      legend={metricLegend}
      name="panel-metric"
    />
    <MetricSwitcher
      options={periodOptions}
      selected={period}
      onselect={(p) => { period = p }}
      legend={periodLegend}
      name="panel-window"
    />
    <!-- A select, not a second radio set: comparison is optional and the list
         is as long as the station's metric list, which is more choices than a
         row of radios can carry beside two other rows of them. -->
    <label class="panel-chart__compare">
      <span>{compareLabel}</span>
      <select bind:value={compare}>
        <option value="">{compareNone}</option>
        {#each compareOptions as option (option.metric)}
          <option value={option.metric}>{option.label}</option>
        {/each}
      </select>
    </label>
  </div>

  {#if chartSources.length}
    <Chart
      sources={chartSources}
      {timeLabel}
      {empty}
      {unavailable}
      title=""
    />
  {/if}
</div>
