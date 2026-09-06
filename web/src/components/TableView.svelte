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
  import { viewRows, perPageOptions, nextSort } from '../lib/table.js'
  import MetricSwitcher from './MetricSwitcher.svelte'

  let { rows, texts, onview, register } = $props()

  let mode = $state('all')
  let key = $state('value')
  let dir = $state('desc')
  // 'all' rather than a number: it means "the whole set", whatever the filter
  // has left the set to be, and so stays valid when the count changes.
  let perPage = $state('all')
  let page = $state(1)

  const view = $derived(viewRows(rows, { mode, key, dir, perPage, page }))
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
    onview({ rows: view.rows, page: view.page, pages: view.pages, total: view.total, key, dir })
  })

  function setMode(next) {
    mode = next
    page = 1
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

<div class="table-controls">
  <MetricSwitcher options={modes} selected={mode} onselect={setMode} legend={texts.filterLegend} name="datafilter" />
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
