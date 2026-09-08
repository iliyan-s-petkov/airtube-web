<script>
  // Map chrome: one icon, no prose. Time and switch name live in the title.
  import RefreshButton from './RefreshButton.svelte'

  let {
    status, auto, autoLabel, onauto, onrefresh,
    button = false, buttonLabel = '', busy = false,
  } = $props()

  const hint = $derived(status ? `${autoLabel} · ${status}` : autoLabel)
</script>

<p class="data-refresh">
  {#if button}
    <RefreshButton label={buttonLabel} {busy} variant="icon" {onrefresh} />
  {/if}
  <!-- Clipped, not removed: a live region is the one reader who cannot hover. -->
  <span class="data-refresh__status sr-only" role="status">{status}</span>
  <button
    type="button"
    class="data-refresh__auto"
    role="switch"
    aria-checked={auto}
    title={hint}
    aria-label={hint}
    onclick={() => onauto(!auto)}
  >
    <svg class="data-refresh__ico" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
      <path d="M13.5 8a5.5 5.5 0 1 1-1.61-3.89" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <path d="M13.5 2.5v3h-3" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="square"/>
      <!-- Off is struck through, so colour is never the only signal. -->
      {#if !auto}
        <path d="M2.5 13.5 13.5 2.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
      {/if}
    </svg>
  </button>
</p>
