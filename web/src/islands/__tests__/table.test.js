// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { unmount, flushSync } from 'svelte'
import { mount, upgradeHeaders, markSorted, countLine } from '../table.js'

let component
afterEach(() => {
  if (component) unmount(component)
  component = null
  document.body.innerHTML = ''
})

const PROVINCES = [
  { name: 'Пловдив', value: '52.1000', text: '52,1', sensors: 57 },
  { name: 'София', value: '18.2000', text: '18,2', sensors: 40 },
  { name: 'Габрово', value: '4.2000', text: '4,2', sensors: 3 },
  { name: 'Видин', value: null, text: 'Няма скорошни данни', sensors: 0 },
]

function page(provinces = PROVINCES) {
  const el = document.createElement('div')
  el.dataset.island = 'table'
  Object.assign(el.dataset, {
    tFilterLegend: 'Показване',
    tFilterAll: 'Всички',
    tFilterWithData: 'С данни',
    tFilterNoData: 'Без данни',
    tPerPage: 'Реда на страница',
    tPerPageAll: 'Всички',
    tPagerLabel: 'Страници',
    tPage: 'Страница',
    tPageOf: 'от',
    tPageFirst: 'Първа',
    tPagePrev: 'Предишна',
    tPageNext: 'Следваща',
    tPageLast: 'Последна',
    tShown: 'Показани',
    tOf: 'от',
    tAreas: 'области',
    tSilent: 'без скорошни данни',
    tSearchLabel: 'Търсене на област',
    tSearchPlaceholder: 'Например: Габрово',
    tSearchHint: 'Пишете, за да филтрирате таблицата.',
    tSearchEmpty: 'Няма област с това име',
    tColumns: 'Колони',
    tVisibleColumns: 'Видими колони',
  })
  document.body.appendChild(el)

  const rows = provinces
    .map((p) => {
      const keys = p.value === null ? 'data-nodata' : `data-value="${p.value}"`
      const cell = p.value === null ? `<td class="nodata">${p.text}</td>` : `<td class="num">${p.text}</td>`
      return `<tr data-name="${p.name}" data-sensors="${p.sensors}" ${keys}><td class="name"><a class="link" href="/area/x">${p.name}</a></td>${cell}<td class="sensors">${p.sensors}</td></tr>`
    })
    .join('')
  const table = document.createElement('table')
  table.className = 'table'
  table.innerHTML =
    '<thead><tr><th scope="col" data-sort-key="name">Област</th>' +
    '<th scope="col" class="num" data-sort-key="value" aria-sort="descending">ФПЧ2.5</th>' +
    '<th scope="col" class="sensors" data-sort-key="sensors">Сензори</th></tr></thead>' +
    `<tbody>${rows}</tbody>`
  document.body.appendChild(table)

  const empty = document.createElement('p')
  empty.className = 't-empty'
  empty.hidden = true
  empty.textContent = 'Няма област с това име.'
  document.body.appendChild(empty)

  const meta = document.createElement('p')
  meta.className = 'meta'
  meta.textContent = '4 области — 1 без скорошни данни'
  document.body.appendChild(meta)

  return el
}

const shownNames = () =>
  [...document.querySelectorAll('.table tbody tr')].filter((tr) => !tr.hidden).map((tr) => tr.dataset.name)

const click = (node) => {
  node.click()
  flushSync()
}

describe('upgradeHeaders', () => {
  it('turns each keyed header into a real button carrying its label', () => {
    const el = page()
    component = mount(el)
    const button = document.querySelector('th[data-sort-key="name"] .th-sort')
    expect(button.tagName).toBe('BUTTON')
    expect(button.type).toBe('button')
    expect(button.textContent).toBe('Област')
  })

  it('leaves a header with no sort key alone', () => {
    const table = document.createElement('table')
    table.innerHTML = '<thead><tr><th>Плейн</th></tr></thead>'
    expect(upgradeHeaders(table, () => {})).toHaveLength(0)
    expect(table.querySelector('.th-sort')).toBeNull()
  })
})

describe('markSorted', () => {
  // Two columns claiming to be the sorted one is a state the table cannot be
  // in, and a screen reader announces every aria-sort it finds.
  it('marks one column and clears every other', () => {
    const doc = document.implementation.createHTMLDocument()
    const heads = ['name', 'value'].map((key) => {
      const th = doc.createElement('th')
      th.setAttribute('aria-sort', 'descending')
      return { key, th }
    })
    markSorted(heads, 'name', 'asc')
    expect(heads[0].th.getAttribute('aria-sort')).toBe('ascending')
    expect(heads[1].th.hasAttribute('aria-sort')).toBe(false)
  })
})

describe('countLine', () => {
  it('says how many rows of how many, and how many are silent', () => {
    const texts = { shown: 'Показани', of: 'от', areas: 'области', silent: 'без скорошни данни' }
    expect(countLine(texts, 14, 28, 8)).toBe('Показани 14 от 28 области — 8 без скорошни данни')
  })
})

describe('table island', () => {
  it('adds the filter above the table and moves the pager under it', () => {
    const el = page()
    component = mount(el)
    expect(el.querySelector('.table-controls')).not.toBeNull()
    // The pager renders inside the island and is moved: it belongs under the
    // table it pages.
    expect(el.querySelector('.pager')).toBeNull()
    expect(document.querySelector('.table').nextElementSibling.className).toBe('pager')
  })

  it('does not mount over a table with nothing to sort', () => {
    const el = page([PROVINCES[0]])
    expect(mount(el)).toBeNull()
    expect(document.querySelector('.th-sort')).toBeNull()
  })

  it('leaves the server order alone until asked', () => {
    const el = page()
    component = mount(el)
    expect(shownNames()).toEqual(['Пловдив', 'София', 'Габрово', 'Видин'])
  })

  it('reorders the rows the server rendered rather than rebuilding them', () => {
    const el = page()
    component = mount(el)
    const before = document.querySelector('.table tbody tr')
    click(document.querySelector('th[data-sort-key="name"] .th-sort'))
    expect(shownNames().slice(0, 3)).toEqual(['Габрово', 'Пловдив', 'София'])
    // The same element, moved: the row carries the server's link, colour and
    // formatted number, and a rebuilt row is a second copy that can disagree.
    expect(document.querySelector('.table tbody tr[data-name="Пловдив"]')).toBe(before)
  })

  it('sinks the silent province in every order the reader can ask for', () => {
    const el = page()
    component = mount(el)
    const value = document.querySelector('th[data-sort-key="value"] .th-sort')
    click(value) // value ascending
    expect(shownNames().at(-1)).toBe('Видин')
    click(document.querySelector('th[data-sort-key="name"] .th-sort'))
    expect(shownNames().at(-1)).toBe('Видин')
  })

  it('moves aria-sort to the column it sorted by', () => {
    const el = page()
    component = mount(el)
    click(document.querySelector('th[data-sort-key="sensors"] .th-sort'))
    expect(document.querySelector('th[data-sort-key="sensors"]').getAttribute('aria-sort')).toBe('descending')
    expect(document.querySelector('th[data-sort-key="value"]').hasAttribute('aria-sort')).toBe(false)
  })

  it('hides the rows a filter excludes and says how many are left', () => {
    const el = page()
    component = mount(el)
    const withData = [...el.querySelectorAll('input[name="datafilter"]')][1]
    withData.checked = true
    withData.dispatchEvent(new Event('change', { bubbles: true }))
    flushSync()
    expect(shownNames()).toEqual(['Пловдив', 'София', 'Габрово'])
    expect(document.querySelector('.meta').textContent).toBe('Показани 3 от 4 области — 1 без скорошни данни')
  })

  it('states the absence when a filter leaves no rows', () => {
    const el = page(PROVINCES.slice(0, 3))
    component = mount(el)
    const noData = [...el.querySelectorAll('input[name="datafilter"]')][2]
    noData.checked = true
    noData.dispatchEvent(new Event('change', { bubbles: true }))
    flushSync()
    expect(shownNames()).toEqual([])
    // The sentence is the server's, only unhidden — an empty table is a bug the
    // reader has to diagnose.
    expect(document.querySelector('.t-empty').hidden).toBe(false)
  })

  it('offers no page navigation until the reader creates pages', () => {
    const el = page()
    component = mount(el)
    expect(document.querySelector('.pager__nav')).toBeNull()
    // Four rows: two is the only divisor at or above… none, so the select is
    // absent too rather than offering a single meaningless option.
    expect(document.querySelector('#table-perpage')).toBeNull()
  })

  it('pages the table when the row count offers a divisor', () => {
    const many = Array.from({ length: 10 }, (_, i) => ({
      name: `О${i}`,
      value: `${10 - i}.0000`,
      text: `${10 - i},0`,
      sensors: 1,
    }))
    const el = page(many)
    component = mount(el)
    const select = document.querySelector('#table-perpage')
    expect([...select.options].map((o) => o.value)).toEqual(['all', '5'])
    select.value = '5'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    flushSync()
    expect(shownNames()).toHaveLength(5)
    expect(document.querySelector('.pager__status').textContent).toBe('Страница 1 от 2')

    click(document.querySelector('.pager__nav button[aria-label="Следваща"]'))
    expect(shownNames()[0]).toBe('О5')
    expect(document.querySelector('.pager__status').textContent).toBe('Страница 2 от 2')
  })

  it('returns to the first page when the order changes under the reader', () => {
    const many = Array.from({ length: 10 }, (_, i) => ({
      name: `О${i}`,
      value: `${10 - i}.0000`,
      text: `${10 - i},0`,
      sensors: 1,
    }))
    const el = page(many)
    component = mount(el)
    const select = document.querySelector('#table-perpage')
    select.value = '5'
    select.dispatchEvent(new Event('change', { bubbles: true }))
    flushSync()
    click(document.querySelector('.pager__nav button[aria-label="Следваща"]'))
    click(document.querySelector('th[data-sort-key="name"] .th-sort'))
    // Page 3 of a question the reader just changed is the middle of an answer
    // to something else.
    expect(document.querySelector('.pager__status').textContent).toBe('Страница 1 от 2')
  })
})

// The kit's search combobox inside .table-controls. It narrows the table the
// reader is already reading, which is what separates it from the masthead
// finder: that one navigates to a province's page.
describe('the table search', () => {
  const search = () => document.querySelector('#table-search')
  const options = () => [...document.querySelectorAll('#table-search-listbox .combobox__opt')]

  const type = (text) => {
    const input = search()
    input.value = text
    input.dispatchEvent(new Event('input', { bubbles: true }))
    flushSync()
  }

  const key = (name) => {
    search().dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true }))
    flushSync()
  }

  it('is a combobox that owns its listbox', () => {
    component = mount(page())
    const input = search()
    expect(input.getAttribute('role')).toBe('combobox')
    expect(input.getAttribute('aria-controls')).toBe('table-search-listbox')
    expect(document.querySelector('#table-search-listbox').getAttribute('role')).toBe('listbox')
    // The label is a real <label for>, not a placeholder doing a label's job:
    // a placeholder disappears the moment the reader types.
    expect(document.querySelector('label[for="table-search"]').textContent).toBe('Търсене на област')
  })

  it('narrows the table as the reader types', () => {
    component = mount(page())
    type('плов')
    expect(shownNames()).toEqual(['Пловдив'])
  })

  it('puts every province back when the query is cleared', () => {
    component = mount(page())
    type('плов')
    type('')
    expect(shownNames()).toHaveLength(4)
  })

  // The count line is what tells the reader the rest of the table still exists.
  it('restates the count for the query', () => {
    component = mount(page())
    type('плов')
    expect(document.querySelector('.meta').textContent).toBe(
      'Показани 1 от 4 области — 1 без скорошни данни',
    )
  })

  // An absence stated where the rows would be, rather than a table that
  // silently empties.
  it('says so when nothing matches', () => {
    component = mount(page())
    type('Атлантида')
    expect(shownNames()).toEqual([])
    expect(document.querySelector('.t-empty').hidden).toBe(false)
  })

  it('suggests the matching provinces and marks the matched run', () => {
    component = mount(page())
    type('в')
    const texts = options().map((li) => li.textContent)
    expect(texts).toEqual(['Видин', 'Габрово', 'Пловдив'])
    expect(options()[0].querySelector('mark').textContent).toBe('В')
  })

  // Focus never leaves the input: the cursor is published through
  // aria-activedescendant so the caret stays where the reader is typing.
  it('moves a cursor through the suggestions without taking focus', () => {
    component = mount(page())
    search().focus()
    type('в')
    key('ArrowDown')
    expect(search().getAttribute('aria-activedescendant')).toBe('table-search-opt-0')
    expect(options()[0].getAttribute('aria-selected')).toBe('true')
    expect(document.activeElement).toBe(search())
  })

  it('wraps the cursor around both ends of the list', () => {
    component = mount(page())
    type('в')
    key('ArrowUp')
    expect(search().getAttribute('aria-activedescendant')).toBe('table-search-opt-2')
    key('ArrowDown')
    expect(search().getAttribute('aria-activedescendant')).toBe('table-search-opt-0')
  })

  it('narrows to the province the reader confirms with Enter', () => {
    component = mount(page())
    type('в')
    key('ArrowDown')
    key('Enter')
    expect(shownNames()).toEqual(['Видин'])
    expect(document.querySelector('#table-search-listbox').hidden).toBe(true)
  })

  // A half-typed query already shows every province it matches; guessing which
  // one was meant would hide the rest.
  it('does nothing on Enter while the reader is still typing', () => {
    component = mount(page())
    type('в')
    key('Enter')
    expect(shownNames()).toEqual(['Пловдив', 'Габрово', 'Видин'])
  })

  // Two stages, as in the masthead finder: one Escape must not throw away a
  // query the reader only wanted to see past.
  it('closes the list on the first Escape and clears the text on the second', () => {
    component = mount(page())
    type('плов')
    key('Escape')
    expect(document.querySelector('#table-search-listbox').hidden).toBe(true)
    expect(search().value).toBe('плов')
    key('Escape')
    expect(search().value).toBe('')
    expect(shownNames()).toHaveLength(4)
  })

  // The suggestions come from the rows the filter has left. Offering a province
  // with readings while "without data" is chosen offers a name that narrows the
  // table to nothing.
  it('suggests only the provinces the current filter still shows', () => {
    component = mount(page())
    click(document.querySelector('input[name="datafilter"][value="nodata"]'))
    search().focus()
    type('')
    expect(options().map((li) => li.textContent)).toEqual(['Видин'])
  })
})

describe('the column menu', () => {
  const menu = () => document.querySelector('.colmenu')
  const button = () => document.querySelector('.colmenu > button')
  const boxes = () => [...document.querySelectorAll('.colmenu__panel input[type="checkbox"]')]
  const box = (key) => document.querySelector(`.colmenu__panel input[data-col="${key}"]`)
  const cellsOf = (key) => {
    const th = document.querySelector(`th[data-sort-key="${key}"]`)
    const i = th.cellIndex
    return [th, ...[...document.querySelectorAll('.table tbody tr')].map((tr) => tr.cells[i])]
  }

  it('opens the panel the button says it controls', () => {
    component = mount(page())
    expect(button().getAttribute('aria-expanded')).toBe('false')
    const panel = document.getElementById(button().getAttribute('aria-controls'))
    expect(panel.hidden).toBe(true)
    click(button())
    expect(button().getAttribute('aria-expanded')).toBe('true')
    expect(panel.hidden).toBe(false)
  })

  // The labels are read off the headers rather than translated a second time:
  // the value column's header already carries the current metric and its unit,
  // and a menu that said "Стойност" would name a column the table does not have.
  it('offers the data columns, labelled as their headers are', () => {
    component = mount(page())
    click(button())
    expect(boxes().map((b) => b.dataset.col)).toEqual(['value', 'sensors'])
    expect(boxes().map((b) => b.closest('.colmenu__opt').textContent.trim())).toEqual(['ФПЧ2.5', 'Сензори'])
  })

  // The name column is the table's subject and carries every link out of it.
  it('does not offer to hide the names', () => {
    component = mount(page())
    click(button())
    expect(box('name')).toBeNull()
  })

  it('hides the header and every cell of a column turned off', () => {
    component = mount(page())
    click(button())
    click(box('sensors'))
    expect(cellsOf('sensors').every((c) => c.hidden)).toBe(true)
    expect(cellsOf('value').some((c) => c.hidden)).toBe(false)
  })

  it('brings the column back when it is turned on again', () => {
    component = mount(page())
    click(button())
    click(box('sensors'))
    click(box('sensors'))
    expect(cellsOf('sensors').some((c) => c.hidden)).toBe(false)
  })

  // A table of 28 names and no measurements is not the page the reader opened.
  it('locks the last data column standing', () => {
    component = mount(page())
    click(button())
    click(box('sensors'))
    expect(box('value').disabled).toBe(true)
    expect(box('sensors').disabled).toBe(false)
  })

  it('re-sorts by name when the sorted column is hidden', () => {
    component = mount(page())
    click(button())
    click(box('value'))
    expect(document.querySelector('th[data-sort-key="name"]').getAttribute('aria-sort')).toBe('ascending')
    expect(document.querySelector('th[data-sort-key="value"]').getAttribute('aria-sort')).toBeNull()
    expect(shownNames()).toEqual(['Габрово', 'Пловдив', 'София', 'Видин'])
  })

  it('closes on Escape and gives the button its focus back', () => {
    component = mount(page())
    click(button())
    menu().dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    flushSync()
    expect(button().getAttribute('aria-expanded')).toBe('false')
    expect(document.activeElement).toBe(button())
  })

  it('closes when the reader clicks past it', () => {
    component = mount(page())
    click(button())
    document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    flushSync()
    expect(button().getAttribute('aria-expanded')).toBe('false')
  })
})
