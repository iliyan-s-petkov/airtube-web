<script>
  // Draws the model from lib/gauge.js; no arithmetic here.
  import { arcPath } from '../lib/gauge.js'

  let { label, value, unit, model = { fraction: null, colour: null, stops: [] }, missing = false } = $props()

  const ariaLabel = $derived(missing ? `${label}: ${value}` : `${label}: ${value} ${unit}`)
</script>

<div class="gauge" role="group" aria-label={ariaLabel}>
  <svg class="gauge__svg" viewBox="0 0 100 60" aria-hidden="true" focusable="false">
    {#if model.stops.length}
      <!-- Band track under the fill, at reduced opacity. -->
      {#each model.stops as stop, i (i)}
        <path
          class="gauge__band"
          d={arcPath(i === 0 ? 0 : model.stops[i - 1].fraction, stop.fraction)}
          stroke={stop.colour}
        />
      {/each}
    {:else}
      <path class="gauge__track-path" d={arcPath(0, 1)} />
    {/if}
    {#if model.fraction !== null}
      <path class="gauge__fill" d={arcPath(0, model.fraction)} stroke={model.colour ?? 'var(--accent)'} />
    {/if}
  </svg>
  <div class="gauge__value">{missing ? value : `${value} ${unit}`}</div>
  <div class="gauge__label">{label}</div>
</div>
