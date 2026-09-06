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
