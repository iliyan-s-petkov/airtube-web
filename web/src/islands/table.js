// The province table's controls, added to a table that already works without
// them.
//
// Everything this island offers — sorting, the with-data filter, paging — is
// interactive by nature, so none of it is server-rendered: a header that looks
// like a button and does nothing is worse than a header. What the server sends
// is a complete, ranked table and the sort keys for each row; this island
// upgrades it in place. With no JavaScript the reader still gets all 28
// provinces, ranked, which is the answer the page exists to give.
import { mount as mountComponent } from 'svelte'
import TableView from '../components/TableView.svelte'
import { readRows } from '../lib/table.js'

// The header's text becomes the label of a real <button>: a click target that
// is not focusable and not announced as pressable is a control only a mouse
// user has. Built with DOM calls rather than innerHTML — the CSP forbids
// nothing here, but a table cell's text is province copy and belongs in a text
// node, not in a parsed string.
export function upgradeHeaders(table, onsort) {
  const heads = []
  for (const th of table.querySelectorAll('th[data-sort-key]')) {
    const key = th.dataset.sortKey
    const button = th.ownerDocument.createElement('button')
    button.type = 'button'
    button.className = 'th-sort'
    button.textContent = th.textContent.trim()
    button.addEventListener('click', () => onsort(key))
    th.textContent = ''
    th.appendChild(button)
    heads.push({ key, th })
  }
  return heads
}

// aria-sort lives on the <th> and only ever on ONE of them: two columns
// claiming to be the sorted one is a state the table cannot be in, and a screen
// reader reads every one it finds.
export function markSorted(heads, key, dir) {
  for (const head of heads) {
    if (head.key === key) head.th.setAttribute('aria-sort', dir === 'asc' ? 'ascending' : 'descending')
    else head.th.removeAttribute('aria-sort')
  }
}

// The rows are moved, not re-created: they carry the server's links, colours
// and formatted numbers, and re-rendering them in JavaScript would be a second
// copy of the same table that could disagree with the first.
export function applyRows(tbody, all, visible) {
  const shown = new Set(visible.map((r) => r.el))
  for (const row of all) row.el.hidden = !shown.has(row.el)
  for (const row of visible) tbody.appendChild(row.el)
}

// "Showing 14 of 28 provinces — 8 with no recent readings". The silent count is
// of the whole table, not of the page: it is a fact about the network, and a
// number that changed as the reader paged would be describing the page instead.
export function countLine(texts, shown, total, silent) {
  return `${texts.shown} ${shown} ${texts.of} ${total} ${texts.areas} — ${silent} ${texts.silent}`
}

export function mount(el, doc = document) {
  const d = el.dataset
  const table = doc.querySelector(d.table || '.table')
  if (!table) return null
  const tbody = table.querySelector('tbody')
  const rows = readRows(tbody)
  // Nothing to sort, filter or page. A control bar over a table of one row is
  // three questions the reader has no reason to answer.
  if (rows.length < 2) return null

  const silent = rows.filter((r) => r.nodata).length
  const meta = doc.querySelector(d.meta || '.meta')
  const empty = doc.querySelector(d.empty || '.t-empty')

  const texts = {
    filterLegend: d.tFilterLegend || '',
    filterAll: d.tFilterAll || '',
    filterWithData: d.tFilterWithData || '',
    filterNoData: d.tFilterNoData || '',
    perPage: d.tPerPage || '',
    perPageAll: d.tPerPageAll || '',
    pagerLabel: d.tPagerLabel || '',
    page: d.tPage || '',
    pageOf: d.tPageOf || '',
    pageFirst: d.tPageFirst || '',
    pagePrev: d.tPagePrev || '',
    pageNext: d.tPageNext || '',
    pageLast: d.tPageLast || '',
    shown: d.tShown || '',
    of: d.tOf || '',
    areas: d.tAreas || '',
    silent: d.tSilent || '',
  }

  let api = null
  const heads = upgradeHeaders(table, (key) => api && api.sortBy(key))

  const component = mountComponent(TableView, {
    target: el,
    props: {
      rows,
      texts,
      register: (a) => { api = a },
      onview: (view) => {
        applyRows(tbody, rows, view.rows)
        markSorted(heads, view.key, view.dir)
        if (meta) meta.textContent = countLine(texts, view.rows.length, rows.length, silent)
        // An absence stated where the rows would have been, rather than a
        // table that silently empties: "no province matches this filter" is
        // information, a blank is a bug the reader has to diagnose.
        if (empty) empty.hidden = view.rows.length > 0
      },
    },
  })

  // The pager belongs under the table it pages. It is rendered inside this
  // island — one component, one piece of state — and moved here, because the
  // alternative is a second component below the table holding a second copy of
  // the same page number.
  const pager = el.querySelector('.pager')
  if (pager) table.after(pager)

  return component
}
