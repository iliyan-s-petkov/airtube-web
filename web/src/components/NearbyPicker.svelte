<script>
  // The panel chart's "nearby sensors" chooser: the same .colmenu pop-up as
  // MetricPicker, with two differences that are the whole reason it is a second
  // component rather than a prop on the first.
  //
  // Empty is allowed. MetricPicker refuses to leave nothing ticked, because a
  // chart with no series is a frame the reader cannot get out of; here nothing
  // ticked is the DEFAULT and means "just this sensor".
  //
  // It can be disabled. The three lines are the spread of one metric across the
  // area, so they only mean anything while one metric is drawn — with two
  // metrics on the plot there is no single quantity for a median to be the
  // median OF. Disabled with the reason on it rather than hidden: a control that
  // vanishes reads as a bug, and the reader has no way to learn what to do to
  // get it back.
  import { closeOnEscape, closeOnOutside } from '../lib/menu.js'

  let {
    options, selected, onchange, legend, offLabel,
    disabled = false, disabledHint = '',
    name = 'panel-nearby', id = 'panel-nearby-menu',
  } = $props()

  let open = $state(false)
  let menuEl = $state()
  let menuBtn = $state()

  const chosen = $derived(options.filter((o) => selected.includes(o.key)).map((o) => o.label))

  $effect(() => {
    if (!open) return
    return closeOnOutside(menuEl, () => { open = false })
  })

  function close() {
    open = false
    menuBtn?.focus()
  }

  function toggle(key) {
    onchange(selected.includes(key) ? selected.filter((k) => k !== key) : [...selected, key])
  }
</script>

<svelte:window onkeydown={closeOnEscape(() => open, close)} />

<div class="colmenu colmenu--start" bind:this={menuEl}>
  <button
    type="button"
    class="btn btn--secondary"
    {id}
    {disabled}
    title={disabled ? disabledHint : undefined}
    bind:this={menuBtn}
    aria-expanded={open}
    aria-controls="{id}-panel"
    onclick={() => { open = !open }}
  >{legend}: {chosen.length ? chosen.join(', ') : offLabel}</button>
  <div class="colmenu__panel" id="{id}-panel" hidden={!open}>
    <fieldset>
      <legend>{legend}</legend>
      {#each options as option (option.key)}
        <label class="colmenu__opt">
          <input
            type="checkbox"
            {name}
            value={option.key}
            checked={selected.includes(option.key)}
            onchange={() => toggle(option.key)}
          >
          <span>{option.label}</span>
        </label>
      {/each}
    </fieldset>
  </div>
</div>
