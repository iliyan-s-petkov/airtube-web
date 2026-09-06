<script>
  // The province table's controls: which rows to show, in what order, how many
  // at a time. It owns that state and nothing else — the table itself stays the
  // server's, and every change leaves here as one `onview` call describing what
  // should now be visible. The island (islands/table.js) is what touches the
  // rows.
  //
  // Split that way because the table is server-rendered and must keep working
  // with no JavaScript at all: the rows a reader sees are the ones the Go
  // template printed, re-ordered in place, never re-rendered from a second copy
  // of the data.
  import {
    viewRows,
    perPageOptions,
    nextSort,
    nextHidden,
    columnLocked,
    sortAfterHide,
  } from '../lib/table.js'
  import { matchAreas, splitMark } from '../lib/find.js'
  import { closeOnEscape, closeOnOutside } from '../lib/menu.js'
  import MetricSwitcher from './MetricSwitcher.svelte'

  // `columns` are the table's data columns, read off its headers by the island:
  // key and printed label. The value column's label carries the current metric
  // and its unit, so naming the columns here would name a column the table does
  // not have.
  let { rows, texts, onview, register, lang = 'bg', columns = [] } = $props()

  let mode = $state('all')
  let query = $state('')
  // Whether the suggestion list is open, and where the keyboard cursor is in
  // it. -1 is "still typing": the table is already filtered by then, so Enter
  // has nothing left to confirm and does nothing.
  let listOpen = $state(false)
  let active = $state(-1)
  let key = $state('value')
  let dir = $state('desc')
  // 'all' rather than a number: it means "the whole set", whatever the filter
  // has left the set to be, and so stays valid when the count changes.
  let perPage = $state('all')
  let page = $state(1)
  // Which data columns the reader has turned off, and whether the menu that
  // turns them off is open. Both start where the server left the table: nothing
  // hidden, nothing open.
  let hidden = $state([])
  let menuOpen = $state(false)
  let menuEl = $state()
  let menuBtn = $state()

  const dataKeys = $derived(columns.map((c) => c.key))

  const view = $derived(viewRows(rows, { mode, query, lang, key, dir, perPage, page }))

  // The suggestions come from the rows the FILTER has left, not from all of
  // them: with "without data" chosen, offering a province that has readings
  // would be offering a name that narrows the table to nothing.
  const suggestions = $derived(matchAreas(filtered(), query, lang))
  const activeId = $derived(
    active >= 0 && active < suggestions.length ? `table-search-opt-${active}` : null,
  )
  const options = $derived(perPageOptions(rows.length))
  // The filter's own options, in the kit's order (§5.4). MetricSwitcher is the
  // radio set for "one of these, mutually exclusive"; only its file name is
  // about metrics.
  const modes = $derived([
    { metric: 'all', label: texts.filterAll },
    { metric: 'withdata', label: texts.filterWithData },
    { metric: 'nodata', label: texts.filterNoData },
  ])

  // The island hands the header buttons their behaviour through this: the
  // headers are the server's markup, upgraded in place, so they cannot be
  // children of this component.
  // Once, at mount, and deliberately: the island passes one callback for the
  // life of the page, and re-registering on every state change would hand it a
  // new object per keystroke.
  // svelte-ignore state_referenced_locally
  register({
    sortBy(k) {
      const next = nextSort({ key, dir }, k)
      key = next.key
      dir = next.dir
      // A new order is a new first page. Staying on page 3 after re-sorting
      // shows the reader the middle of an answer to a question they just
      // changed.
      page = 1
    },
  })

  $effect(() => {
    onview({ rows: view.rows, page: view.page, pages: view.pages, total: view.total, key, dir, hidden })
  })

  // Dismissal is lib/menu.js's, shared with the metric menu: two copies of
  // "close when the reader clicks past it" is how one of them stops closing.
  $effect(() => {
    if (!menuOpen) return
    return closeOnOutside(menuEl, () => { menuOpen = false })
  })

  function toggleColumn(k) {
    const next = nextHidden(hidden, k, dataKeys)
    if (next === hidden) return
    hidden = next
    const order = sortAfterHide({ key, dir }, hidden)
    key = order.key
    dir = order.dir
  }

  function closeMenu() {
    menuOpen = false
    // Focus goes back to the control that opened the panel: a reader who
    // dismissed it with the keyboard would otherwise be returned to the top of
    // the document.
    menuBtn?.focus()
  }

  function filtered() {
    return viewRows(rows, { mode, lang, perPage: 'all', page: 1 }).rows
  }

  function setMode(next) {
    mode = next
    page = 1
  }

  // Typing IS the filter — every keystroke re-narrows the table, exactly as the
  // kit's own hint describes it. A new query is a new first page for the same
  // reason a new sort is.
  function setQuery(next) {
    query = next
    page = 1
    active = -1
  }

  function moveActive(step) {
    if (!suggestions.length) return
    listOpen = true
    const n = suggestions.length
    active = active < 0 ? (step > 0 ? 0 : n - 1) : (active + step + n) % n
  }

  function onSearchKeydown(e) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        moveActive(1)
        return
      case 'ArrowUp':
        e.preventDefault()
        moveActive(-1)
        return
      case 'Enter':
        // Confirming a suggestion narrows the table to that one province. Only
        // from the cursor: a half-typed query already shows every province it
        // matches, and guessing which one was meant would hide the rest.
        if (active < 0) return
        e.preventDefault()
        setQuery(suggestions[active].name)
        listOpen = false
        return
      case 'Escape':
        // Two stages, as in the home page's finder: the list goes first, the
        // text second, so one Escape cannot throw away a query the reader only
        // wanted to see past.
        if (listOpen) {
          e.preventDefault()
          listOpen = false
          active = -1
          return
        }
        if (query) {
          e.preventDefault()
          setQuery('')
        }
        return
      default:
    }
  }

  function setPerPage(value) {
    perPage = value === 'all' ? 'all' : Number(value)
    page = 1
  }

  function go(where) {
    if (where === 'first') page = 1
    else if (where === 'prev') page = Math.max(1, view.page - 1)
    else if (where === 'next') page = Math.min(view.pages, view.page + 1)
    else page = view.pages
  }
</script>

<!-- Escape closes the column menu from wherever the reader's focus is: the
     panel is open over the page, and a <div> wrapper is not a thing that should
     carry a keyboard handler of its own. svelte:window has to sit at the top
     level of the component, so it cannot live beside the menu it serves. -->
<svelte:window onkeydown={closeOnEscape(() => menuOpen, closeMenu)} />

<div class="table-controls">
  <!-- The kit's search combobox. It FILTERS rather than navigating, which is
       what makes it different from the finder in the masthead: that one opens a
       province's page, this one narrows the table the reader is reading.

       Focus never leaves the input — the active option is pointed at with
       aria-activedescendant — so the caret stays where the reader is typing. -->
  <div class="field field--search combobox">
    <label class="field__label" for="table-search">{texts.searchLabel}</label>
    <input
      class="input"
      id="table-search"
      type="search"
      name="q"
      autocomplete="off"
      role="combobox"
      aria-expanded={listOpen}
      aria-controls="table-search-listbox"
      aria-autocomplete="list"
      aria-activedescendant={activeId}
      aria-describedby="table-search-hint"
      placeholder={texts.searchPlaceholder}
      value={query}
      oninput={(e) => { setQuery(e.currentTarget.value); listOpen = true }}
      onfocus={() => { listOpen = true }}
      onkeydown={onSearchKeydown}
      onblur={() => setTimeout(() => { listOpen = false }, 0)}
    >
    <!-- Announced, never painted: a sighted reader infers the arrow keys from
         the open list; a screen-reader user does not. -->
    <span class="sr-only" id="table-search-hint">{texts.searchHint}</span>
    <ul class="combobox__list" id="table-search-listbox" role="listbox" hidden={!listOpen}>
      {#if suggestions.length === 0}
        <li class="combobox__empty">{texts.searchEmpty}</li>
      {:else}
        {#each suggestions as match, i (match.name)}
          {@const parts = splitMark(match.name, match.at, match.len)}
          <!-- mousedown, not click: click arrives after blur has closed the
               list, so a mouse pick would land on nothing. -->
          <li
            class="combobox__opt"
            id="table-search-opt-{i}"
            role="option"
            aria-selected={i === active}
            onmousedown={(e) => { e.preventDefault(); setQuery(match.name); listOpen = false }}
          >{parts.before}<mark>{parts.hit}</mark>{parts.after}</li>
        {/each}
      {/if}
    </ul>
  </div>
  <MetricSwitcher options={modes} selected={mode} onselect={setMode} legend={texts.filterLegend} name="datafilter" />
  <!-- The kit's Колони menu (§5.11). Two things it will not do: hide the names,
       which are the table's subject and carry every link out of it, and hide
       the last data column, which would leave 28 provinces and no readings.
       The second is enforced by disabling that checkbox rather than by ignoring
       the click — a box that unticks and ticks itself back is lying. -->
  {#if columns.length > 1}
    <div class="colmenu" bind:this={menuEl}>
      <button
        type="button"
        class="btn btn--secondary"
        id="table-colmenu-btn"
        bind:this={menuBtn}
        aria-expanded={menuOpen}
        aria-controls="table-colmenu-panel"
        onclick={() => { menuOpen = !menuOpen }}
      >{texts.columns}</button>
      <div class="colmenu__panel" id="table-colmenu-panel" hidden={!menuOpen}>
        <fieldset>
          <legend>{texts.visibleColumns}</legend>
          {#each columns as col (col.key)}
            <label class="colmenu__opt">
              <input
                type="checkbox"
                data-col={col.key}
                checked={!hidden.includes(col.key)}
                disabled={columnLocked(hidden, col.key, dataKeys)}
                onchange={() => toggleColumn(col.key)}
              >
              <span>{col.label}</span>
            </label>
          {/each}
        </fieldset>
      </div>
    </div>
  {/if}
</div>

<!-- The bar below the table. The island moves this node under the table after
     mount: it belongs there on screen, and it cannot be rendered there from
     here without a second component holding the same state twice.

     The select is not hidden at a single page, only the navigation is — the
     select is what creates pages in the first place, so hiding it would remove
     the only way back to more than one. -->
<nav class="pager" aria-label={texts.pagerLabel}>
  {#if options.length}
    <div class="pager__perpage">
      <label for="table-perpage">{texts.perPage}</label>
      <span class="select-wrap">
        <select class="select" id="table-perpage" onchange={(e) => setPerPage(e.currentTarget.value)}>
          <option value="all">{texts.perPageAll}</option>
          {#each options as n (n)}
            <option value={n}>{n}</option>
          {/each}
        </select>
        <span class="select-wrap__caret" aria-hidden="true"></span>
      </span>
    </div>
  {/if}
  <span class="pager__spacer"></span>
  {#if view.pages > 1}
    <div class="pager__nav">
      <div class="pager__group">
        <button type="button" class="btn btn--ghost pager__btn" aria-label={texts.pageFirst}
                disabled={view.page === 1} onclick={() => go('first')}>&lsaquo;&lsaquo;</button>
        <button type="button" class="btn btn--ghost pager__btn" aria-label={texts.pagePrev}
                disabled={view.page === 1} onclick={() => go('prev')}>&lsaquo;</button>
      </div>
      <!-- role="status": the page number is the result of pressing the button
           beside it, and a reader who cannot see the table needs the change
           announced. -->
      <p class="pager__status" role="status">{texts.page} {view.page} {texts.pageOf} {view.pages}</p>
      <div class="pager__group">
        <button type="button" class="btn btn--ghost pager__btn" aria-label={texts.pageNext}
                disabled={view.page === view.pages} onclick={() => go('next')}>&rsaquo;</button>
        <button type="button" class="btn btn--ghost pager__btn" aria-label={texts.pageLast}
                disabled={view.page === view.pages} onclick={() => go('last')}>&rsaquo;&rsaquo;</button>
      </div>
    </div>
  {/if}
</nav>
