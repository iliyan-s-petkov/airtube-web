<script>
  // How old the numbers are, and the switch that decides whether they stay
  // young. The two belong together and neither belongs beside the button: the
  // button is the action, these are the answer to "how fresh is this, and is it
  // staying fresh".
  import RefreshButton from './RefreshButton.svelte'

  let {
    status, auto, autoLabel, onauto, onrefresh,
    button = false, buttonLabel = '', busy = false,
  } = $props()
</script>

<p class="data-refresh">
  {#if button}
    <RefreshButton label={buttonLabel} {busy} variant="line" {onrefresh} />
  {/if}
  <!-- A live region, so the new timestamp is announced where the reader
       already is instead of having to be hunted for. role="status" and not
       "alert": a refresh finishing is not an interruption. The element is
       always present, empty when there is nothing to say — a live region added
       to the page at the moment it gets content announces nothing. -->
  <span class="data-refresh__status" role="status">{status}</span>
  <label class="data-refresh__auto">
    <input type="checkbox" checked={auto} onchange={(e) => onauto(e.currentTarget.checked)}>
    <span>{autoLabel}</span>
  </label>
</p>
