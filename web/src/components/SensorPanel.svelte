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
  //
  // `details` (added with the panel's chart controls) describes the station
  // rather than its readings — hardware, lifetime, coordinates. Defaults to
  // empty so a caller that has nothing to say renders no second list.
  // `meta` (task 12) is the station's EEA classification (EoI code, type,
  // area) — precomposed rows like `details`, but shown directly under the
  // heading rather than behind a disclosure: unlike the hardware inventory,
  // this is what the station IS, not a fact a reader has to open a drawer
  // for. `network` is a single precomposed sentence, the same idiom as
  // `flagText`. Both default empty so every sensor without them (every
  // citizen device today) renders neither block.
  let {
    rows, title, flagText, closeLabel, noValue, onclose,
    details = [], detailsLabel = '', chart = null, open = true,
    meta = [], network = '',
  } = $props()
</script>

{#if open}
<!-- A named region, not a dialog. It was a dialog while it floated over the map,
     where a keyboard user who could not reach the close control was trapped
     behind it; the kit puts this card UNDER the map instead (.place-host), in
     the flow, covering nothing — so dialog semantics would now promise a modal
     that does not exist. Svelte said as much: a <section> cannot carry an
     interactive role. Escape still closes, because the reader whose focus is
     inside the card should not have to Tab back out to the close button. -->
<section
  class="sensor-panel"
  aria-labelledby="sensor-panel-title"
  tabindex="-1"
  onkeydown={(e) => { if (e.key === 'Escape') onclose() }}
>
  <!-- The close control sits on the title row, at its right edge: a bare word
       under the heading read as a link into somewhere rather than as the way
       out of this card. The glyph is inline SVG (the site ships no icon font),
       aria-hidden because the label beside it already names the action. -->
  <header>
    <h2 id="sensor-panel-title">{title}</h2>
    <button type="button" class="panel-close" data-close onclick={onclose}>
      <svg class="panel-close__ico" width="16" height="16" viewBox="0 0 16 16"
           fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
      </svg>
      {closeLabel}
    </button>
  </header>

  {#if flagText}<p class="panel-flag">{flagText}</p>{/if}

  {#if meta.length}
    <dl class="panel-meta">
      {#each meta as row (row.key)}
        <dt>{row.label}</dt>
        <dd>{row.value}</dd>
      {/each}
    </dl>
  {/if}

  {#if network}<p class="panel-network">{network}</p>{/if}

  <dl>
    {#each rows as row (row.metric)}
      <dt>{row.label}</dt>
      <dd>{#if row.missing}{noValue}{:else}{row.value} {row.unit}{/if}</dd>
    {/each}
  </dl>

  {#if details.length}
    <!-- A native <details>, like the language picker: the same three states
         (closed, open, labelled) with no script and no aria bookkeeping. Closed
         is the default because this block describes the HARDWARE — devices,
         firmware, coordinates — and on a phone it stood between the readings
         and the chart, which are what a reader opens a station for. -->
    <details class="panel-details">
      <summary>{detailsLabel}</summary>
      <dl>
        {#each details as row (row.key)}
          <dt>{row.label}</dt>
          <dd>{row.value}</dd>
        {/each}
      </dl>
    </details>
  {/if}

  {#if chart}{@render chart()}{/if}
</section>
{/if}
