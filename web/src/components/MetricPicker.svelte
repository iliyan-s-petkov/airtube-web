<script>
  // The panel's metric chooser: the same .colmenu pop-up MetricMenu uses, but
  // with checkboxes, because a panel chart may now draw several metrics at once.
  // That replaces the old "Compare with" select, which could only ever add a
  // second line.
  //
  // The menu does NOT close on a pick: choosing several metrics is the whole
  // point, and closing after the first would make the second choice cost a
  // second trip through the button.
  import { closeOnEscape, closeOnOutside } from '../lib/menu.js'

  let { options, selected, onchange, legend, name = 'panel-metric', id = 'panel-metric-menu' } = $props()

  let open = $state(false)
  let menuEl = $state()
  let menuBtn = $state()

  const chosen = $derived(options.filter((o) => selected.includes(o.metric)).map((o) => o.label))

  $effect(() => {
    if (!open) return
    return closeOnOutside(menuEl, () => { open = false })
  })

  function close() {
    open = false
    menuBtn?.focus()
  }

  // Unticking the last metric is a no-op: a chart with no series is an empty
  // frame the reader cannot get out of except by finding the tick again. The
  // box is put back by hand because refusing leaves `selected` unchanged, and
  // an unchanged value re-renders nothing — the tick would stay off on screen
  // while the line it governs is still drawn.
  function toggle(metric, box) {
    const next = selected.includes(metric)
      ? selected.filter((m) => m !== metric)
      : [...selected, metric]
    if (next.length === 0) {
      box.checked = true
      return
    }
    onchange(next)
  }
</script>

<svelte:window onkeydown={closeOnEscape(() => open, close)} />

<div class="colmenu colmenu--start" bind:this={menuEl}>
  <button
    type="button"
    class="btn btn--secondary"
    {id}
    bind:this={menuBtn}
    aria-expanded={open}
    aria-controls="{id}-panel"
    onclick={() => { open = !open }}
  >{legend}: {chosen.join(', ')}</button>
  <div class="colmenu__panel" id="{id}-panel" hidden={!open}>
    <fieldset>
      <legend>{legend}</legend>
      {#each options as option (option.metric)}
        <label class="colmenu__opt">
          <input
            type="checkbox"
            {name}
            value={option.metric}
            checked={selected.includes(option.metric)}
            onchange={(e) => toggle(option.metric, e.currentTarget)}
          >
          <span>{option.label}</span>
        </label>
      {/each}
    </fieldset>
  </div>
</div>
