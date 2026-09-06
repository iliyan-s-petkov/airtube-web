import { describe, it, expect } from 'vitest'
import {
  readRow,
  sortRows,
  firstDir,
  nextSort,
  filterRows,
  queryRows,
  perPageOptions,
  pageCount,
  pageSlice,
  viewRows,
  nextHidden,
  columnLocked,
  sortAfterHide,
} from '../table.js'

const row = (name, value, sensors) => ({
  name,
  value,
  sensors,
  nodata: value === null,
})

const rows = () => [
  row('Пловдив', 52.1, 57),
  row('София', 18.2, 40),
  row('Габрово', 4.2, 3),
  row('Видин', null, 0),
  row('Ямбол', null, 2),
]

const names = (rs) => rs.map((r) => r.name)

describe('readRow', () => {
  it('takes its keys from data attributes, not from the printed cells', () => {
    const tr = { dataset: { name: 'София', value: '18.2', sensors: '40' } }
    expect(readRow(tr)).toMatchObject({ name: 'София', value: 18.2, sensors: 40, nodata: false })
  })

  it('reads a silent row as having no value rather than a zero one', () => {
    const tr = { dataset: { name: 'Видин', sensors: '0', nodata: '' } }
    const r = readRow(tr)
    expect(r.nodata).toBe(true)
    expect(r.value).toBeNull()
  })

  it('keeps a genuine zero reading as a value', () => {
    const r = readRow({ dataset: { name: 'X', value: '0', sensors: '1' } })
    expect(r.value).toBe(0)
    expect(r.nodata).toBe(false)
  })

  // A row with no value IS silent, whether or not it also says so — otherwise
  // it sorts as neither a reading nor an absence.
  it('reads a row with no value at all as silent', () => {
    expect(readRow({ dataset: { name: 'X', sensors: '1' } }).nodata).toBe(true)
  })
})

describe('sortRows', () => {
  it('ranks by value, largest first', () => {
    expect(names(sortRows(rows(), 'value', 'desc')).slice(0, 3)).toEqual(['Пловдив', 'София', 'Габрово'])
  })

  // The one rule that has to survive every column and both directions: a
  // province with no reading is not the smallest reading.
  it('sinks the silent rows in ascending order too', () => {
    const asc = names(sortRows(rows(), 'value', 'asc'))
    expect(asc.slice(0, 3)).toEqual(['Габрово', 'София', 'Пловдив'])
    expect(asc.slice(3)).toEqual(['Видин', 'Ямбол'])
  })

  it('sinks them when sorting by name, in both directions', () => {
    expect(names(sortRows(rows(), 'name', 'asc')).slice(3)).toEqual(['Видин', 'Ямбол'])
    expect(names(sortRows(rows(), 'name', 'desc')).slice(3)).toEqual(['Ямбол', 'Видин'])
  })

  it('sinks them when sorting by sensor count, so a silent row with sensors does not outrank a reporting one', () => {
    // Ямбол has two sensors and Габрово three, but Ямбол has no reading: by
    // count alone it would sit above Габрово in an ascending sort.
    expect(names(sortRows(rows(), 'sensors', 'asc'))).toEqual([
      'Габрово', 'София', 'Пловдив', 'Видин', 'Ямбол',
    ])
  })

  it('leaves the original array alone', () => {
    const original = rows()
    sortRows(original, 'name', 'asc')
    expect(names(original)[0]).toBe('Пловдив')
  })

  // The rows arrive in the server's ranking, which already puts the silent
  // ones last — so a comparator that merely left them alone would look right
  // on that input. This one starts with them at the top.
  it('sinks them from wherever they start', () => {
    const upsideDown = [row('Видин', null, 0), row('Ямбол', null, 2), row('София', 18.2, 40)]
    expect(names(sortRows(upsideDown, 'value', 'desc'))).toEqual(['София', 'Видин', 'Ямбол'])
    expect(names(sortRows(upsideDown, 'value', 'asc'))).toEqual(['София', 'Видин', 'Ямбол'])
    expect(names(sortRows(upsideDown, 'sensors', 'desc'))).toEqual(['София', 'Ямбол', 'Видин'])
  })

  it('leaves two silent rows in the order they came in', () => {
    const silent = [row('Ямбол', null, 2), row('Видин', null, 0)]
    expect(names(sortRows(silent, 'value', 'desc'))).toEqual(['Ямбол', 'Видин'])
  })

  it('keeps the server ranking between rows that tie', () => {
    const tied = [row('A', 5, 1), row('B', 5, 1), row('C', 5, 1)]
    expect(names(sortRows(tied, 'value', 'desc'))).toEqual(['A', 'B', 'C'])
  })
})

describe('sort direction', () => {
  it('opens a name column alphabetically and a measurement on the largest', () => {
    expect(firstDir('name')).toBe('asc')
    expect(firstDir('value')).toBe('desc')
    expect(firstDir('sensors')).toBe('desc')
  })

  it('toggles the column already sorted and resets one that is not', () => {
    expect(nextSort({ key: 'value', dir: 'desc' }, 'value')).toEqual({ key: 'value', dir: 'asc' })
    expect(nextSort({ key: 'value', dir: 'asc' }, 'value')).toEqual({ key: 'value', dir: 'desc' })
    expect(nextSort({ key: 'value', dir: 'asc' }, 'name')).toEqual({ key: 'name', dir: 'asc' })
  })
})

describe('filterRows', () => {
  it('keeps everything by default', () => {
    expect(filterRows(rows(), 'all')).toHaveLength(5)
  })

  it('splits the table into the provinces with a reading and the ones without', () => {
    expect(names(filterRows(rows(), 'withdata'))).toEqual(['Пловдив', 'София', 'Габрово'])
    expect(names(filterRows(rows(), 'nodata'))).toEqual(['Видин', 'Ямбол'])
  })
})

describe('queryRows', () => {
  it('keeps every row for an empty or blank query', () => {
    expect(queryRows(rows(), '')).toHaveLength(5)
    expect(queryRows(rows(), '   ')).toHaveLength(5)
  })

  // Substring, not prefix: a reader after "Велико Търново" types "търново".
  it('matches anywhere in the name, not only at the front', () => {
    const set = [...rows(), row('Велико Търново', 9, 12)]
    expect(names(queryRows(set, 'търново'))).toEqual(['Велико Търново'])
  })

  // The fold is language-aware for the same reason the finder's is: a Cyrillic
  // query folded with the wrong locale stops matching its own name.
  it('ignores case in both alphabets', () => {
    expect(names(queryRows(rows(), 'ПЛОВДИВ'))).toEqual(['Пловдив'])
    expect(names(queryRows([row('Ruse', 3, 4)], 'RUSE'))).toEqual(['Ruse'])
  })

  it('returns nothing for a name the table does not carry', () => {
    expect(queryRows(rows(), 'Атлантида')).toEqual([])
  })

  // A silent province is still a province the reader can search for. The
  // with-data filter is the control that removes those, not the search box.
  it('does not quietly drop the rows with no reading', () => {
    expect(names(queryRows(rows(), 'видин'))).toEqual(['Видин'])
  })
})

describe('perPageOptions', () => {
  // The kit's mockup offers 21 for its 28 rows while its own comment says the
  // options are divisors. The rule wins over the list.
  it('offers only divisors, so no page is a stub', () => {
    expect(perPageOptions(28)).toEqual([14, 7])
    for (const n of perPageOptions(28)) expect(28 % n).toBe(0)
  })

  it('offers nothing below five rows, and never the total itself', () => {
    expect(perPageOptions(28)).not.toContain(4)
    expect(perPageOptions(28)).not.toContain(28)
    expect(perPageOptions(7)).toEqual([])
  })
})

describe('paging', () => {
  it('counts one page when the reader asked for all of them', () => {
    expect(pageCount(28, 'all')).toBe(1)
    expect(pageCount(28, 7)).toBe(4)
  })

  it('never reports zero pages for an empty result', () => {
    expect(pageCount(0, 7)).toBe(1)
  })

  it('clamps a page past the end rather than showing nothing', () => {
    // The reader on page 3 who then filters down to one page has not made a
    // mistake; an empty table would be a strange way to say so.
    const slice = pageSlice(rows(), 2, 9)
    expect(slice.page).toBe(3)
    expect(slice.rows).toHaveLength(1)
  })

  it('slices the requested window', () => {
    expect(names(pageSlice(rows(), 2, 2).rows)).toEqual(['Габрово', 'Видин'])
  })
})

describe('viewRows', () => {
  // Paging last is what makes the filter mean what it says: applied first,
  // "page 2" would decide which rows the filter was even allowed to see.
  it('filters, then sorts, then pages — in that order', () => {
    const out = viewRows(rows(), { mode: 'withdata', key: 'value', dir: 'asc', perPage: 5, page: 1 })
    expect(names(out.rows)).toEqual(['Габрово', 'София', 'Пловдив'])
    expect(out.pages).toBe(1)
  })

  it('reports the filtered total, so the count line can say "shown X of Y"', () => {
    expect(viewRows(rows(), { mode: 'nodata' }).total).toBe(2)
  })

  // The search narrows what the FILTER left. The other order would let a query
  // reach rows the reader has already asked not to see.
  it('applies the query after the filter, not instead of it', () => {
    const out = viewRows(rows(), { mode: 'withdata', query: 'видин' })
    expect(out.rows).toEqual([])
    expect(out.total).toBe(0)
  })

  it('counts the query result, so the line reads "shown 1 of 28"', () => {
    expect(viewRows(rows(), { query: 'пловдив' }).total).toBe(1)
  })

  // Paging is a window onto the answer, so a query re-cuts the window rather
  // than being applied inside it.
  it('searches the whole table, not just the page the reader is on', () => {
    const out = viewRows(rows(), { query: 'ямбол', perPage: 2, page: 1 })
    expect(names(out.rows)).toEqual(['Ямбол'])
  })

  it('defaults to the order the server already rendered', () => {
    expect(names(viewRows(rows()).rows)).toEqual(['Пловдив', 'София', 'Габрово', 'Видин', 'Ямбол'])
  })
})

describe('the column menu', () => {
  // The kit's rule, and the reason the menu is safe to offer at all: a table
  // whose every data column is hidden is a list of 28 names, which is not what
  // the reader came for and not a state any control should be able to reach.
  const DATA = ['value', 'sensors']

  it('hides a column the reader turns off', () => {
    expect(nextHidden([], 'sensors', DATA)).toEqual(['sensors'])
  })

  it('brings it back when they turn it on again', () => {
    expect(nextHidden(['sensors'], 'sensors', DATA)).toEqual([])
  })

  it('keeps the last data column whatever the reader clicks', () => {
    expect(nextHidden(['sensors'], 'value', DATA)).toEqual(['sensors'])
  })

  // The name column carries the link to each province's page. It is the table's
  // subject, not one of its measurements, so the menu never offers it.
  it('never hides the names', () => {
    expect(nextHidden([], 'name', DATA)).toEqual([])
  })

  it('locks the checkbox of the last column standing', () => {
    expect(columnLocked(['sensors'], 'value', DATA)).toBe(true)
    expect(columnLocked([], 'value', DATA)).toBe(false)
  })

  it('does not lock a column that is already hidden', () => {
    expect(columnLocked(['sensors'], 'sensors', DATA)).toBe(false)
  })

  // Hiding the column the table is sorted by leaves an order the reader can no
  // longer see the reason for. It falls back to the names, ascending, which is
  // the one order that is legible with any column hidden.
  it('re-sorts by name when the sorted column goes', () => {
    expect(sortAfterHide({ key: 'value', dir: 'desc' }, ['value'])).toEqual({ key: 'name', dir: 'asc' })
  })

  it('leaves the sort alone when another column goes', () => {
    expect(sortAfterHide({ key: 'value', dir: 'asc' }, ['sensors'])).toEqual({ key: 'value', dir: 'asc' })
  })
})
