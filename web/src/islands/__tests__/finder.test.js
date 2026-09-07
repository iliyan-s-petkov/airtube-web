// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { unmount, tick } from 'svelte'
import { mount } from '../finder.js'
import { setMapAreas, provideAreaSelect } from '../../lib/mapareas.svelte.js'

let component
afterEach(() => {
  if (component) unmount(component)
  component = null
  document.body.innerHTML = ''
  setMapAreas([])
})

function page({ lang = 'bg', areas = true, source = '.areas' } = {}) {
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

  // The map tab renders no table. Absent data-source, the names come from the
  // area payload the map has already loaded, and a pick moves that map instead
  // of leaving the page.
  it('reads the map\'s own areas when the server names no list', async () => {
    const el = page({ lang: 'en', areas: false, source: null })
    const picked = []
    const unselect = provideAreaSelect((area) => { picked.push(area.slug); return true })
    setMapAreas([
      { slug: 'varna', name_bg: 'Варна', name_en: 'Varna', lon: 27.9, lat: 43.2, zoom: 11 },
      { slug: 'burgas', name_bg: 'Бургас', name_en: 'Burgas', lon: 27.5, lat: 42.5, zoom: 11 },
    ])
    component = mount(el)
    el.querySelector('input[role="combobox"]').dispatchEvent(new Event('focus', { bubbles: true }))
    await tick()
    expect([...el.querySelectorAll('.combobox__opt')].map((li) => li.textContent))
      .toEqual(['Burgas', 'Varna'])

    el.querySelectorAll('.combobox__opt')[1]
      .dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }))
    await tick()
    expect(picked).toEqual(['varna'])
    expect(globalThis.location.pathname).not.toContain('/area/')
    unselect()
  })

  // The field mounts before the first area response lands, so an empty list is
  // a state it passes through rather than a reason not to exist.
  it('fills in when the map\'s areas arrive after it mounted', async () => {
    const el = page({ areas: false, source: null })
    component = mount(el)
    expect(component).not.toBeNull()
    setMapAreas([{ slug: 'sofia', name_bg: 'София', lon: 23.3, lat: 42.7, zoom: 11 }])
    el.querySelector('input[role="combobox"]').dispatchEvent(new Event('focus', { bubbles: true }))
    await tick()
    expect([...el.querySelectorAll('.combobox__opt')].map((li) => li.textContent)).toEqual(['София'])
  })
})
