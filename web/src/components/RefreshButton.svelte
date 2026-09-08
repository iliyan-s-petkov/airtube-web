<script>
  // Icon and word in one control: the glyph is recognised at a glance, the word
  // says which content is refreshed (DESIGN.md §5.2a — the glyph is the
  // affordance, the name is still required).
  //
  // Three dresses, one behaviour: toolbar action, line chrome, and — drawn on
  // the map — the glyph alone, with the name moved to title/aria-label.
  let { label, busy = false, variant = 'toolbar', onrefresh } = $props()

  const CLASSES = {
    toolbar: 'btn btn--secondary toolbar__refresh',
    line: 'btn btn--ghost btn--compact data-refresh__btn',
    icon: 'data-refresh__btn data-refresh__btn--icon',
  }

  const bare = $derived(variant === 'icon')
</script>

<!-- Not disabled while in flight. A disabled button loses focus to the body,
     which drops a keyboard reader out of the toolbar for the length of a
     request; aria-busy says the same thing without moving anyone. The store
     ignores a second request while one is running. -->
<button
  type="button"
  class={CLASSES[variant] ?? CLASSES.toolbar}
  aria-busy={busy}
  title={bare ? label : undefined}
  aria-label={bare ? label : undefined}
  onclick={onrefresh}
>
  <svg class="data-refresh__ico" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" focusable="false">
    <path d="M13.5 8a5.5 5.5 0 1 1-1.61-3.89" fill="none" stroke="currentColor" stroke-width="1.5"/>
    <path d="M13.5 2.5v3h-3" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="square"/>
  </svg>
  {#if !bare}<span>{label}</span>{/if}
</button>
