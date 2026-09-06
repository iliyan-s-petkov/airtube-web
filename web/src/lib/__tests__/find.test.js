// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { readAreas, matchAreas, exactMatch, splitMark } from '../find.js'

const AREAS = [
  { name: 'Варна', href: '/area/varna' },
  { name: 'Бургас', href: '/area/burgas' },
  { name: 'София', href: '/area/sofia' },
  { name: 'Велико Търново', href: '/area/veliko-tarnovo' },
]

function list(html) {
  const ul = document.createElement('ul')
  ul.innerHTML = html
  return ul
}

describe('readAreas', () => {
  it('lifts the name and href off the rendered list', () => {
    const ul = list(`
      <li><a href="/en/area/varna">Varna</a><span class="count">12</span></li>
      <li><a href="/en/area/sofia">Sofia</a></li>`)
    expect(readAreas(ul)).toEqual([
      { name: 'Varna', href: '/en/area/varna' },
      { name: 'Sofia', href: '/en/area/sofia' },
    ])
  })

  // The href is taken verbatim, never rebuilt from the slug: the server already
  // put the language prefix on it, and a second URL builder here is a second
  // thing that can send an English reader back to Bulgarian.
  it('keeps the server\'s href exactly as rendered', () => {
    expect(readAreas(list('<li><a href="/en/area/veliko-tarnovo">Veliko Tarnovo</a></li>'))[0].href)
      .toBe('/en/area/veliko-tarnovo')
  })

  it('ignores anchors with no name and a missing list', () => {
    expect(readAreas(list('<li><a href="/a"></a></li><li><a href="/b">B</a></li>')))
      .toEqual([{ name: 'B', href: '/b' }])
    expect(readAreas(null)).toEqual([])
  })
})

describe('matchAreas', () => {
  // Focusing the field shows the whole list: an empty query is not "no match".
  it('returns every area for an empty query', () => {
    expect(matchAreas(AREAS, '').length).toBe(4)
    expect(matchAreas(AREAS, '   ').length).toBe(4)
  })

  it('matches anywhere in the name, not only at the start', () => {
    expect(matchAreas(AREAS, 'Търново').map((m) => m.name)).toEqual(['Велико Търново'])
  })

  it('ignores case in the reader\'s own alphabet', () => {
    expect(matchAreas(AREAS, 'софия').map((m) => m.name)).toEqual(['София'])
    expect(matchAreas(AREAS, 'ВАРНА').map((m) => m.name)).toEqual(['Варна'])
  })

  // A finder's list has to be scannable, and the page's own ranking is by
  // reading, not by name. Code-point order is not alphabetical order in either
  // script, so the collator follows the language.
  it('sorts alphabetically for the language, not by code point', () => {
    expect(matchAreas(AREAS, '').map((m) => m.name))
      .toEqual(['Бургас', 'Варна', 'Велико Търново', 'София'])
  })

  it('reports where the match starts so the option can mark it', () => {
    const [m] = matchAreas(AREAS, 'търново')
    expect({ at: m.at, len: m.len }).toEqual({ at: 7, len: 7 })
  })

  it('marks nothing when nothing was typed', () => {
    expect(matchAreas(AREAS, '')[0].at).toBe(-1)
  })

  it('returns an empty list rather than everything when nothing matches', () => {
    expect(matchAreas(AREAS, 'Лондон')).toEqual([])
  })
})

describe('exactMatch', () => {
  // Enter is a navigation. A query that could still become several names must
  // not take the reader anywhere.
  it('refuses an ambiguous query', () => {
    expect(exactMatch(matchAreas(AREAS, 'в'), 'в')).toBeNull()
  })

  it('takes a name typed in full even when it prefixes another', () => {
    const areas = [{ name: 'Стара Загора', href: '/a' }, { name: 'Стара Загора юг', href: '/b' }]
    expect(exactMatch(matchAreas(areas, 'стара загора'), 'стара загора').href).toBe('/a')
  })

  it('takes the last one standing', () => {
    expect(exactMatch(matchAreas(AREAS, 'бург'), 'бург').name).toBe('Бургас')
  })

  it('refuses an empty query even when one area exists', () => {
    expect(exactMatch(matchAreas([AREAS[0]], ''), '')).toBeNull()
  })
})

describe('splitMark', () => {
  it('cuts the name into before, hit and after', () => {
    expect(splitMark('Велико Търново', 7, 7))
      .toEqual({ before: 'Велико ', hit: 'Търново', after: '' })
  })

  it('leaves the name whole when there is nothing to mark', () => {
    expect(splitMark('Варна', -1, 0)).toEqual({ before: 'Варна', hit: '', after: '' })
    expect(splitMark('Варна', 0, 0)).toEqual({ before: 'Варна', hit: '', after: '' })
  })
})
