<script>
  // The window chooser: a select of the server's own period vocabulary, plus a
  // custom range. A select rather than the old row of segments because the list
  // now has five entries and the panel already carries a second control beside
  // it.
  //
  // Fully controlled — the parent owns period/from/to, so the chart's URL and
  // this control cannot disagree about which window is on screen.
  import { CUSTOM } from '../lib/period.js'

  let {
    periods, periodLabels, period, from = '', to = '',
    legend, customLabel, fromLabel, toLabel, onchange, id = 'period',
  } = $props()
</script>

<div class="chart-field">
  <label class="chart-field__label" for="{id}-select">{legend}</label>
  <select
    id="{id}-select"
    value={period}
    onchange={(e) => onchange({ period: e.currentTarget.value, from, to })}
  >
    {#each periods as p, i (p)}
      <option value={p}>{periodLabels[i] ?? p}</option>
    {/each}
    <option value={CUSTOM}>{customLabel}</option>
  </select>

  {#if period === CUSTOM}
    <!-- datetime-local, not date: the shortest configured window is 24 hours,
         so a date-only range cannot express most of what this control is for. -->
    <div class="chart-range">
      <label class="chart-field__label" for="{id}-from">{fromLabel}</label>
      <input
        id="{id}-from"
        type="datetime-local"
        value={from}
        onchange={(e) => onchange({ period, from: e.currentTarget.value, to })}
      >
      <label class="chart-field__label" for="{id}-to">{toLabel}</label>
      <input
        id="{id}-to"
        type="datetime-local"
        value={to}
        onchange={(e) => onchange({ period, from, to: e.currentTarget.value })}
      >
    </div>
  {/if}
</div>
