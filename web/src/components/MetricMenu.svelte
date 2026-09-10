<script>
  // The metric chooser as a pop-up, in the kit's .colmenu idiom — the same
  // control the table's Колони menu uses, so the two menus on this site look
  // and behave alike.
  //
  // A row of one segment per metric was ~810px of buttons at seven metrics, and
  // is wider now: it wrapped to two lines on a desktop frame and to four on a
  // phone, and it pushed the map — the thing the metric is about — below the
  // fold. A button that names the current metric says the same thing in one line
  // and hands the rest to the reader who wants them.
  //
  // Inside the panel it is still a radio set, for the reason it always was
  // (DESIGN.md §5.6): the metrics are mutually exclusive, so the reader gets one
  // Tab stop with arrow-key roving and the chosen metric announced as selected
  // rather than as one pressed button in a row.
  import { closeOnEscape, closeOnOutside } from '../lib/menu.js'

  // `id` because a page may carry two of these — the area page has its own
  // switcher — and aria-controls has to point at ONE panel.
  let { options, selected, onselect, legend, name = 'metric', id = 'metric-menu' } = $props()

  let open = $state(false)
  let menuEl = $state()
  let menuBtn = $state()

  // The label the button carries is the selected option's own, never a second
  // copy written here: the server ships the labels translated, and a metric the
  // server has renamed must not still read as its old name on the button.
  const current = $derived(options.find((o) => o.metric === selected))

  $effect(() => {
    if (!open) return
    return closeOnOutside(menuEl, () => { open = false })
  })

  function close() {
    open = false
    // Focus returns to the button that opened the panel; otherwise a reader who
    // dismissed it with the keyboard is put back at the top of the document.
    menuBtn?.focus()
  }

  // Choosing closes. The panel covers the map, and a menu that stayed open over
  // the answer would hide the change the reader just asked for.
  function pick(metric) {
    onselect(metric)
    close()
  }
</script>

<!-- svelte:window has to sit at the top level of the component, so it cannot
     live beside the menu it serves. -->
<svelte:window onkeydown={closeOnEscape(() => open, close)} />

<!-- --start because this control is the first on its row: the kit's default
     end-anchored panel is wider than the button and would open off the page. -->
<div class="colmenu colmenu--start" bind:this={menuEl}>
  <button
    type="button"
    class="btn btn--secondary"
    {id}
    bind:this={menuBtn}
    aria-expanded={open}
    aria-controls="{id}-panel"
    onclick={() => { open = !open }}
  >{legend}: {current ? current.label : ''}</button>
  <div class="colmenu__panel" id="{id}-panel" hidden={!open}>
    <fieldset>
      <legend>{legend}</legend>
      {#each options as option (option.metric)}
        <label class="colmenu__opt">
          <input
            type="radio"
            {name}
            value={option.metric}
            checked={option.metric === selected}
            onchange={() => pick(option.metric)}
          >
          <span>{option.label}</span>
        </label>
      {/each}
    </fieldset>
  </div>
</div>
