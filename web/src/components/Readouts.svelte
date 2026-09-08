<script>
  // The readout strip, in the shape base.gohtml's "readouts" define renders it.
  // Same classes and the same SVG presentation attributes — style-src is 'self'
  // with no 'unsafe-inline', so a style attribute would be dropped and the arc
  // would never paint.
  let { cards } = $props()
</script>

<div class="readouts">
  {#each cards as card (card.label)}
    <div class="readout card">
      <span class="readout__label">{card.label}</span>
      {#if card.gauge}
        <span class="gauge">
          <svg class="gauge__dial" viewBox="0 0 36 36" aria-hidden="true" focusable="false">
            <circle class="gauge__track" cx="18" cy="18" r="15.9155" fill="none" pathLength="100"></circle>
            <circle class="gauge__arc" cx="18" cy="18" r="15.9155" fill="none" pathLength="100"
                    stroke={card.gauge.colour} stroke-dasharray="{card.gauge.percent} 100"
                    transform="rotate(-90 18 18)"></circle>
          </svg>
          <span class="gauge__figure">
            <span class="readout__value">{card.value}</span>
            <span class="readout__unit">{card.unit}</span>
          </span>
        </span>
      {:else}
        <span class="readout__value">{card.value}{#if card.unit}{' '}<span class="readout__unit">{card.unit}</span>{/if}</span>
      {/if}
      <span class="readout__tier">{card.tier}</span>
    </div>
  {/each}
</div>
