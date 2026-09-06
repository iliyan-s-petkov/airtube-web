// The area finder: a way in, not a report.
//
// Picking a name GOES to that area's page. The alternative — centre the map,
// then print a line saying the map was centred, then offer a link to the page —
// puts two controls and a sentence between the reader and the thing they just
// named. A reader who types an area is asking to see it.
//
// It mounts only where there is a list to read, which today is the home page.
// A finder over an empty list is a field that can never match anything.
import { mount as mountComponent } from 'svelte'
import AreaFind from '../components/AreaFind.svelte'
import { readAreas } from '../lib/find.js'

export function mount(el, doc = document) {
  const d = el.dataset
  const areas = readAreas(doc.querySelector(d.source || '.areas'))
  if (!areas.length) return null
  return mountComponent(AreaFind, {
    target: el,
    props: {
      areas,
      // The language the page is written in, which is the language the reader
      // is typing and therefore the one the sort has to follow.
      lang: doc.documentElement.getAttribute('lang') || 'bg',
      label: d.tLabel || '',
      placeholder: d.tPlaceholder || '',
      hint: d.tHint || '',
      empty: d.tEmpty || '',
      // The href is the server's, already carrying the language prefix, so
      // there is no second place that knows how a URL is built.
      onpick: (href) => { globalThis.location.assign(href) },
    },
  })
}
