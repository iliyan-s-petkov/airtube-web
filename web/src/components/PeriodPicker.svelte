<script>
  // The window chooser: a select of the server's own period vocabulary, plus a
  // custom range. A select rather than the old row of segments because the list
  // now has five entries and the panel already carries a second control beside
  // it.
  //
  // Fully controlled — the parent owns period/from/to, so the chart's URL and
  // this control cannot disagree about which window is on screen.
  import { CUSTOM, localNow } from '../lib/period.js'

  let {
    periods, periodLabels, period, from = '', to = '',
    legend, customLabel, fromLabel, toLabel, nowLabel = '', onchange, id = 'period',
  } = $props()

  // The browser's own calendar and clock, rather than a hand-built one: it is
  // localised, keyboard-operable and touch-sized already, and it costs no
  // dependency. Chrome and Safari otherwise open it only from the small glyph
  // at the end of the field, which readers do not find — clicking the field is
  // what they try. Guarded because Firefox exposes showPicker on date inputs
  // only from 101, and there the field still types.
  function openPicker(e) {
    e.currentTarget.showPicker?.()
  }
</script>

<div class="chart-field">
  <label class="chart-field__label" for="{id}-select">{legend}</label>
  <select
    id="{id}-select"
    class="chart-field__control"
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
        onclick={openPicker}
        onchange={(e) => onchange({ period, from: e.currentTarget.value, to })}
      >
      <label class="chart-field__label" for="{id}-to">{toLabel}</label>
      <input
        id="{id}-to"
        type="datetime-local"
        value={to}
        onclick={openPicker}
        onchange={(e) => onchange({ period, from, to: e.currentTarget.value })}
      >
      <!-- The end of a range is "now" more often than it is any other instant,
           and it is the one instant a reader cannot copy off a calendar. -->
      <button
        type="button"
        class="btn btn--secondary btn--compact chart-range__now"
        onclick={() => onchange({ period, from, to: localNow() })}
      >{nowLabel}</button>
    </div>
  {/if}
</div>
