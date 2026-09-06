// The province table's sort, filter and paging — as pure functions over plain
// row objects, so every rule here is testable without a DOM and without a
// component. The island (islands/table.js) reads the rendered <tr>s into these
// objects and puts the result back; nothing in this file touches an element.
//
// The rows come FROM the server-rendered table rather than from a second fetch:
// the table is already the no-JavaScript fallback and the map's text
// alternative, and a second copy of the same 28 provinces is a second thing
// that can disagree with the one the reader can see.

// A row's sort keys come from data attributes rather than from its printed
// cells: the value is written 12,4 in Bulgarian and 12.4 in English, and a
// sort that had to parse the localised text would order the table differently
// in the two languages.
export function readRow(tr) {
  const d = tr.dataset
  // null, not 0: a province with no reading has no value, and 0 µg/m³ is a
  // legitimate one.
  const value = d.value === undefined ? null : Number(d.value)
  return {
    el: tr,
    name: d.name || '',
    value,
    sensors: Number(d.sensors || 0),
    // A row with no value IS a silent row, whether or not it also says so. The
    // two are one state, and letting them be two would leave a row that sorts
    // as neither a reading nor an absence.
    nodata: d.nodata !== undefined || value === null,
  }
}

export function readRows(tbody) {
  if (!tbody) return []
  return Array.from(tbody.querySelectorAll('tr')).map(readRow)
}

// Rows with no reading sink in EVERY order, ascending included. They are not
// the smallest reading — they are the absence of one, and letting them lead an
// ascending sort would put the eight provinces the network cannot measure at
// the top of a table about measurements. This is the one rule that survives
// every column and both directions.
function compare(a, b, key, dir) {
  if (a.nodata !== b.nodata) return a.nodata ? 1 : -1
  const sign = dir === 'asc' ? 1 : -1
  if (key === 'name') return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }) * sign
  // Past the check above, the two rows agree about being silent — so on the
  // value column either both sides are readings or both are null, and null
  // minus null is 0, which is what two absences should compare as anyway.
  const av = key === 'value' ? a.value : a.sensors
  const bv = key === 'value' ? b.value : b.sensors
  return (av - bv) * sign
}

export function sortRows(rows, key, dir = 'desc') {
  // A copy, and Array.prototype.sort is stable in every engine this ships to,
  // so equal rows keep the server's ranking rather than being shuffled.
  return [...rows].sort((a, b) => compare(a, b, key, dir))
}

// Which direction a column should take when the reader first clicks it. Names
// read alphabetically; a column of measurements is being asked "where is it
// worst", so it opens on the largest.
export function firstDir(key) {
  return key === 'name' ? 'asc' : 'desc'
}

export function nextSort(current, key) {
  if (current.key === key) return { key, dir: current.dir === 'asc' ? 'desc' : 'asc' }
  return { key, dir: firstDir(key) }
}

export function filterRows(rows, mode) {
  if (mode === 'withdata') return rows.filter((r) => !r.nodata)
  if (mode === 'nodata') return rows.filter((r) => r.nodata)
  return rows
}

// Rows per page, offered only as divisors of the row count, so no page is ever
// a stub of two rows after three full ones. The kit's mockup lists 21 for its
// 28 rows, which is not a divisor of 28 — the rule it states beside the list is
// the one followed here, not the list.
//
// Nothing below 5: a "page" of four provinces out of 28 is paging for its own
// sake. And no option equal to the total, because that is what "all" already
// means.
export function perPageOptions(total) {
  const out = []
  for (let n = total - 1; n >= 5; n--) {
    if (total % n === 0) out.push(n)
  }
  return out
}

export function pageCount(total, perPage) {
  if (perPage === 'all' || !perPage) return 1
  return Math.max(1, Math.ceil(total / perPage))
}

// Paging is applied LAST — after the filter and the sort — because it is a
// window onto the result, not part of it. Applied first, "page 2 of the
// unfiltered table" would silently change which rows a filter could see.
export function pageSlice(rows, perPage, page) {
  const pages = pageCount(rows.length, perPage)
  // A page number outside the range is clamped rather than rejected: the
  // reader who is on page 3 and then filters down to one page has not made an
  // error, and an empty table would be a strange way to tell them so.
  const current = Math.min(Math.max(1, page), pages)
  if (perPage === 'all') return { rows, page: current, pages }
  const start = (current - 1) * perPage
  return { rows: rows.slice(start, start + perPage), page: current, pages }
}

// The whole pipeline in the one order that is correct, so no caller can get it
// wrong: filter, then sort, then page.
export function viewRows(rows, { mode = 'all', key = 'value', dir = 'desc', perPage = 'all', page = 1 } = {}) {
  const kept = filterRows(rows, mode)
  return { ...pageSlice(sortRows(kept, key, dir), perPage, page), total: kept.length }
}
