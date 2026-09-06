// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { unmount } from 'svelte'
import { mount } from '../finder.js'

let component
afterEach(() => {
  if (component) unmount(component)
  component = null
  document.body.innerHTML = ''
})

function page({ lang = 'bg', areas = true, source } = {}) {
  document.documentElement.setAttribute('lang', lang)
  const el = document.createElement('div')
  el.dataset.island = 'finder'
  if (source) el.dataset.source = source
  el.dataset.tLabel = 'Търсене на район'
  el.dataset.tPlaceholder = 'Име на район'
  el.dataset.tHint = 'Стрелки и Enter'
  el.dataset.tEmpty = 'Няма район с това име'
  document.body.appendChild(el)
  if (areas) {
    const ul = document.createElement('ul')
    ul.className = 'areas'
    ul.innerHTML = '<li><a href="/area/varna">Варна</a></li><li><a href="/area/burgas">Бургас</a></li>'
    document.body.appendChild(ul)
  }
  return el
}

describe('finder island', () => {
  it('builds the field from the list the page already renders', () => {
    const el = page()
    component = mount(el)
    expect([...el.querySelectorAll('.combobox__opt, .combobox__empty')].length).toBeGreaterThan(0)
    expect(el.querySelector('.field__label').textContent).toBe('Търсене на район')
    expect(el.querySelector('input[role="combobox"]').placeholder).toBe('Име на район')
  })

  // A finder over an empty list is a field that can never match anything. The
  // area page renders no such list, which is exactly why this is the test and
  // not a per-page flag in the template.
  it('mounts nothing when there is no list to read', () => {
    const el = page({ areas: false })
    expect(mount(el)).toBeNull()
    expect(el.children.length).toBe(0)
  })

  it('reads whichever list the server named', () => {
    const el = page({ source: '#somewhere-else' })
    expect(mount(el)).toBeNull()
  })

  // The list on the page is ranked by reading; a finder has to be alphabetical
  // or it cannot be scanned. The language that ordering follows comes from the
  // document, which is the one place that already says which it is.
  it('lists the areas alphabetically for the document\'s language', () => {
    const el = page({ lang: 'en', areas: false })
    const ul = document.createElement('ul')
    ul.className = 'areas'
    ul.innerHTML = '<li><a href="/en/area/varna">Varna</a></li>'
      + '<li><a href="/en/area/burgas">Burgas</a></li>'
      + '<li><a href="/en/area/sofia">Sofia</a></li>'
    document.body.appendChild(ul)
    component = mount(el)
    el.querySelector('input[role="combobox"]').dispatchEvent(new Event('focus', { bubbles: true }))
    expect([...el.querySelectorAll('.combobox__opt')].map((li) => li.textContent))
      .toEqual(['Burgas', 'Sofia', 'Varna'])
  })
})
