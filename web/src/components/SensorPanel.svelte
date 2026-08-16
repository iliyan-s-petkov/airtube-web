<script>
  // sensor and chart are documented in the task-8 brief's Interfaces block,
  // but that list predates the component: the actual props this component
  // reads are rows/title/flagText/closeLabel/noValue/onclose/chart. `sensor`
  // is not needed here — panelRows already reduced the sensor to `rows`
  // before this component ever runs, and the caller composes `title` from
  // the (non-templated) i18n label plus the sensor id. `chart` stays because
  // Step 7's own code renders it as an optional snippet.
  let { rows, title, flagText, closeLabel, noValue, onclose, chart = null } = $props()
</script>

<!-- role=dialog + aria-label, and Escape closes: the panel covers the map on a
     phone, so a keyboard user who cannot reach the close control is trapped. -->
<section
  class="sensor-panel"
  role="dialog"
  aria-label={title}
  tabindex="-1"
  onkeydown={(e) => { if (e.key === 'Escape') onclose() }}
>
  <header>
    <h2>{title}</h2>
    <button type="button" data-close onclick={onclose}>{closeLabel}</button>
  </header>

  {#if flagText}<p class="panel-flag">{flagText}</p>{/if}

  <dl>
    {#each rows as row (row.metric)}
      <dt>{row.label}</dt>
      <dd>{#if row.missing}{noValue}{:else}{row.value} {row.unit}{/if}</dd>
    {/each}
  </dl>

  {#if chart}{@render chart()}{/if}
</section>
