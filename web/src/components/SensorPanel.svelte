<script>
  // sensor and chart are documented in the task-8 brief's Interfaces block,
  // but that list predates the component: the actual props this component
  // reads are rows/title/flagText/closeLabel/noValue/onclose/chart. `sensor`
  // is not needed here — panelRows already reduced the sensor to `rows`
  // before this component ever runs, and the caller composes `title` from
  // the (non-templated) i18n label plus the sensor id. `chart` stays because
  // Step 7's own code renders it as an optional snippet.
  //
  // `open` is task 9's addition: the panel is mounted once, for the page's
  // lifetime, by islands/panel.js — whether it is SHOWING is a function of
  // whether a sensor is currently selected (viewstate's sensorId resolved
  // through the registry), which changes many times without a remount.
  // Defaults true so this component's own tests (which never pass it) are
  // unaffected: they are about what renders once shown, not about the
  // showing/hiding decision itself, which belongs to the caller that knows
  // about sensorId and the registry.
  let { rows, title, flagText, closeLabel, noValue, onclose, chart = null, open = true } = $props()
</script>

{#if open}
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
{/if}
