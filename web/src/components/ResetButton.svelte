<script>
  // The control that undoes what the other controls in the row did. Glyph
  // alone: the row already reads as three sentences ("Metric: PM2.5", "Nearby
  // sensors: off", "Period"), and a fourth is what made it a wall of text. The
  // name moves to title/aria-label so a reader who cannot see the glyph still
  // gets it (DESIGN.md §5.2a — icon-only is allowed, nameless is not).
  let { label, onreset } = $props()
</script>

<button
  type="button"
  class="btn btn--secondary chart-reset"
  title={label}
  aria-label={label}
  onclick={onreset}
>
  <!-- A counter-clockwise arrow: back to how this started, not "load again". -->
  <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
    <path d="M2.5 8a5.5 5.5 0 1 0 1.61-3.89" fill="none" stroke="currentColor" stroke-width="1.5"/>
    <path d="M2.5 2.5v3h3" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="square"/>
  </svg>
</button>
