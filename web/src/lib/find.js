// Finding one area, out of the list the page already renders.
//
// THE LIST IS NOT WRITTEN DOWN A SECOND TIME
// The home page already carries every area as a link — the no-JS fallback and
// the map's text alternative — so the finder reads THAT rather than taking a
// second copy through a data attribute. Two lists of the same 28 names are two
// things that can disagree, and the one the reader can see would not be the one
// that wins. It also settles the empty case for free: no list, no finder.
//
// The matching is here and the DOM is not, because "which areas match what was
// typed" is the part with the rules in it — the fold to lowercase, the
// language-aware sort, where the match starts so it can be marked — and none of
// those rules need a browser to be tested.

// readAreas lifts {name, href} off the rendered list. Anchor text is the name:
// it is what the reader can see and therefore what they will type.
export function readAreas(root) {
  if (!root) return []
  return [...root.querySelectorAll('a[href]')]
    .map((a) => ({ name: a.textContent.trim(), href: a.getAttribute('href') }))
    .filter((x) => x.name)
}

// areaOptions is the other source of names: the map's own area payload, for the
// tab that renders no list. The entry rides along under `area` so the picker
// gets back the coordinates and zoom it needs to move the camera.
//
// name_en is optional in the payload; a missing one falls back to the Bulgarian
// name rather than to an empty option the reader cannot type.
export function areaOptions(entries, lang = 'bg') {
  return (entries || [])
    .map((a) => ({ name: (lang === 'en' ? a.name_en || a.name_bg : a.name_bg) || '', area: a }))
    .filter((o) => o.name)
}

// matchAreas returns the areas whose name contains the query, each carrying
// where the match starts and how long it is, so the option can mark the matched
// run without the renderer searching the string a second time.
//
// Sorted by the reader's language, not by code point: Cyrillic and Latin do not
// interleave the way a byte comparison assumes, and an area list out of
// alphabetical order is a list you cannot scan. An empty query is every area,
// which is what makes focusing the field show the whole list.
export function matchAreas(areas, query, lang = 'bg') {
  const q = (query || '').trim().toLocaleLowerCase(lang)
  const out = []
  for (const area of areas) {
    if (!q) {
      out.push({ ...area, at: -1, len: 0 })
      continue
    }
    const at = area.name.toLocaleLowerCase(lang).indexOf(q)
    if (at !== -1) out.push({ ...area, at, len: q.length })
  }
  const collator = new Intl.Collator(lang)
  return out.sort((a, b) => collator.compare(a.name, b.name))
}

// exactMatch is what Enter is allowed to act on. A half-typed query must never
// navigate, so the field goes somewhere only when the reader has been
// unambiguous: they typed a whole name, or only one area is left standing.
export function exactMatch(matches, query, lang = 'bg') {
  const q = (query || '').trim().toLocaleLowerCase(lang)
  if (!q) return null
  const whole = matches.filter((m) => m.name.toLocaleLowerCase(lang) === q)
  if (whole.length === 1) return whole[0]
  return matches.length === 1 ? matches[0] : null
}

// splitMark cuts a name into the run before the match, the match, and the run
// after it. Returned as three strings rather than as markup: the component
// puts them in three elements, and nothing here builds HTML from a name the
// server rendered.
export function splitMark(name, at, len) {
  if (at < 0 || len <= 0) return { before: name, hit: '', after: '' }
  return { before: name.slice(0, at), hit: name.slice(at, at + len), after: name.slice(at + len) }
}
