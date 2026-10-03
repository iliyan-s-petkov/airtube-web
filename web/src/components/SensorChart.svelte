<script>
  import { untrack } from 'svelte'
  import Chart from './Chart.svelte'
  import MetricPicker from './MetricPicker.svelte'
  import NearbyPicker from './NearbyPicker.svelte'
  import PeriodPicker from './PeriodPicker.svelte'
  import ResetButton from './ResetButton.svelte'
  import PanelMoreMenu from './PanelMoreMenu.svelte'
  import { createPanelLink } from '../lib/panellink.svelte.js'
  import { unitFor } from '../lib/metrics.js'
  import { getScales, getSensorArea } from '../lib/sensors.svelte.js'
  import { nearbyOptions, nearbySources } from '../lib/nearby.js'
  import { CUSTOM, periodQuery } from '../lib/period.js'
  import { panelDock } from '../lib/paneldock.svelte.js'

  // The panel's chart and its controls: which metrics (several at once), which
  // window, and a reset back to the view the panel opened on.
  //
  // options are the metrics THIS STATION measures (the panel's own rows), not
  // the whole set the map switches between: offering pressure for an address with
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
    periodShortLabels = [], moreLabel = '', aboutLabel = '', shareLabel = '', embedLabel = '',
    shareDone = '', embedDone = '', copyFailed = '',
    link = createPanelLink(),
  } = $props()

  // Seeded from the server's defaults, owned here afterwards. The default
  // metric is the map's, and a station that does not measure it (a climate box
  // beside no particulate box) falls back to the first metric it does.
  const seedMetric = untrack(() =>
    (options.some((o) => o.metric === initialMetric) ? initialMetric : options[0]?.metric) ?? null)

  // The metric selection lives in `link` so the panel's gauges can drive it.
  untrack(() => { if (link.metrics.length === 0 && seedMetric) link.metrics = [seedMetric] })
  const metrics = $derived(link.metrics)
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
    link.metrics = seedMetric ? [seedMetric] : []
    nearby = []
    period = initialPeriod
    from = ''
    to = ''
    resetToken += 1
  }

  // Phone controls: the segmented period and the "more" menu. Desktop keeps the
  // pickers above; CSS shows one set or the other.
  const shortLabel = (p, i) => periodShortLabels[i] || periodLabels[i] || p
  const nearbyChoices = $derived(nearbyOptions(nearbyLabels))
  const nearbyDetail = $derived(
    nearbyChoices.filter((o) => nearby.includes(o.key)).map((o) => o.label).join(', ') || nearbyOff)

  let status = $state('')
  let statusTimer
  function say(text) {
    status = text
    clearTimeout(statusTimer)
    statusTimer = setTimeout(() => { status = '' }, 4000)
  }

  async function copy(text, done) {
    try {
      await navigator.clipboard.writeText(text)
      say(done)
    } catch {
      say(copyFailed)
    }
  }

  // The native share sheet where the browser has one; otherwise the link is copied.
  async function share() {
    const url = window.location.href
    if (!navigator.share) return copy(url, shareDone)
    try {
      await navigator.share({ url })
    } catch (e) {
      if (e?.name !== 'AbortError') say(copyFailed)
    }
  }

  function embed() {
    const prefix = document.querySelector('[data-lang-prefix]')?.dataset.langPrefix ?? ''
    const src = `${window.location.origin}${prefix}/embed?metric=${encodeURIComponent(metrics[0] ?? '')}`
    // The masthead carries the brand in the page language (seo.title_brand).
    const brand = document.querySelector('.masthead__brand')?.getAttribute('aria-label') || 'Kanarche'
    copy(`<iframe src="${src}" width="100%" height="520" style="border:0" loading="lazy" title="${brand}"></iframe>`, embedDone)
  }

  const menuItems = $derived([
    {
      key: 'nearby', label: nearbyLegend, detail: nearbyDetail, sub: true,
      disabled: !bandable, hint: areaSlug ? nearbySingleOnly : '',
    },
    { key: 'custom', label: `${customLabel}…`, run: () => { period = CUSTOM } },
    { key: 'reset', label: resetLabel, run: reset },
    { key: 'about', label: aboutLabel, separator: true, run: () => { link.aboutOpen = true } },
    { key: 'share', label: shareLabel, run: share },
    { key: 'embed', label: embedLabel, run: embed },
  ])
  // The panel over the map has its own pickers; its menu keeps only what they lack.
  const dockItems = $derived(menuItems.filter((i) => i.key === 'share' || i.key === 'embed'))
</script>

<!-- The desktop chart controls, drawn under the map and again in the panel over it; the prefix keeps their ids apart. -->
{#snippet controls(prefix)}
  <MetricPicker
    {options}
    selected={metrics}
    onchange={(next) => { link.metrics = next }}
    legend={metricLegend}
    name="{prefix}-metric"
    id="{prefix}-metric-menu"
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
      name="{prefix}-nearby"
      id="{prefix}-nearby-menu"
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
    id="{prefix}-period"
    onchange={(next) => { period = next.period; from = next.from; to = next.to }}
  />
  <!-- In the slot the old "Compare with" select had: the control that undoes
       everything the other two (and the drag handle) did. -->
  <ResetButton label={resetLabel} onreset={reset} />
{/snippet}

<!-- The panel over the map (lib/sidedock.svelte.js) moves this node out while it is open. It reads the
     metric and period the controls below own, so it needs no state of its own and no second fetch:
     getJSON answers the same URLs from its cache. A lone root node, so Svelte can remove it from anywhere. -->
{#if panelDock.on}
  <div class="panel-chart__dock" data-metric={metrics.join(',')} data-period={period}>
    <div class="panel-chart__tools">
      {@render controls('dock')}
      <p class="panel-chart__status" role="status">{status}</p>
      <PanelMoreMenu label={moreLabel} items={dockItems} />
    </div>
    <div class="panel-chart__dockplot">
      {#if period === CUSTOM && !query}
        <p class="chart-message">{rangeInvalid}</p>
      {:else if chartSources.length}
        {#key resetToken}
          <Chart sources={chartSources} {timeLabel} {empty} {unavailable} fill title="" />
        {/key}
      {/if}
    </div>
  </div>
{/if}

<div class="panel-chart">
  <div class="panel-chart__phone">
    <div class="period-seg" role="group" aria-label={periodLegend}>
      {#each periods as p, i (p)}
        <button
          type="button"
          aria-pressed={period === p}
          onclick={() => { period = p; from = ''; to = '' }}
        >{shortLabel(p, i)}</button>
      {/each}
    </div>
    <PanelMoreMenu label={moreLabel} items={menuItems}>
      {#snippet sub()}
        {#each nearbyChoices as option (option.key)}
          <label class="panel-menu__opt">
            <input
              type="checkbox"
              name="phone-nearby"
              value={option.key}
              checked={nearby.includes(option.key)}
              onchange={() => { nearby = nearby.includes(option.key) ? nearby.filter((k) => k !== option.key) : [...nearby, option.key] }}
            >
            <span>{option.label}</span>
          </label>
        {/each}
      {/snippet}
    </PanelMoreMenu>
  </div>
  <p class="panel-chart__status" role="status">{status}</p>
  <div class="panel-chart__controls">
    {@render controls('panel')}
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
