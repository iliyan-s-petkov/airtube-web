// The area finder: a way in, not a report. Picking a name takes the reader to
// the area, rather than printing a line saying where it is.
//
// data-source is the switch: it names the list to read, and its absence means
// the names come from the map on the page. On the list tab the finder mounts
// only if that list has something in it — a finder over an empty list is a
// field that can never match anything.
import { mount as mountComponent } from 'svelte'
import AreaFind from '../components/AreaFind.svelte'
import { areaOptions, readAreas } from '../lib/find.js'
import { getMapAreas, selectMapArea } from '../lib/mapareas.svelte.js'

export function mount(el, doc = document) {
  const d = el.dataset
  // The language the page is written in, which is the language the reader is
  // typing and therefore the one the sort has to follow.
  const lang = doc.documentElement.getAttribute('lang') || 'bg'
  const props = {
    lang,
    label: d.tLabel || '',
    placeholder: d.tPlaceholder || '',
    hint: d.tHint || '',
    empty: d.tEmpty || '',
  }

  // Two tabs, two lists and two meanings for a pick. The list tab has the
  // rendered table and picking GOES to that area's page. The map tab has no
  // table — it has a map — so the names come from what the map has loaded and
  // picking moves the map the reader is already looking at.
  if (d.source) {
    const areas = readAreas(doc.querySelector(d.source))
    if (!areas.length) return null
    return mountComponent(AreaFind, {
      target: el,
      // The href is the server's, already carrying the language prefix, so
      // there is no second place that knows how a URL is built.
      props: { ...props, areas, onpick: (m) => { globalThis.location.assign(m.href) } },
    })
  }

  return mountComponent(AreaFind, {
    target: el,
    props: {
      ...props,
      // A getter, not a snapshot: the map's list lands after this mounts.
      get areas() { return areaOptions(getMapAreas(), lang) },
      onpick: (m) => selectMapArea(m.area),
    },
  })
}
