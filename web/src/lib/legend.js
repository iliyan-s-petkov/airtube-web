import { rampGradient } from './ramp.js'

// The map legend: the ramp, and what a dot means at the current zoom.
//
// Both halves exist because neither is inferable from the map. The ramp is
// server-defined (/api/v1/scales) and its band labels ship translated, so no
// catalogue entry is needed here. The tier line is the harder one: an area page
// prints a sensor count while its dots may be city aggregates, and without a
// line saying so the two honest numbers read as a contradiction.
//
// The tier line is NOT built here any more: the kit puts it under the map as
// prose, not inside the key, and the key is now an overlay ON the map with no
// panel behind it — a three-line paragraph haloed over a choropleth is not
// readable. mountChrome owns that paragraph; this file owns the key.

// The kit classes the container itself must carry. They live here, next to the
// classes renderLegend writes, rather than as a literal in mountChrome: these
// four are what position the whole key and hide it from the overlay treatment
// on a phone, so a typo in one of them is the most expensive misspelling in the
// file — and here they are covered by the test that checks every kit class this
// module names against components.css.
export const LEGEND_CLASSES = 'scale scale--named scale--vertical scale--onmap'

// What the key is a key TO. The kit writes the current metric and its unit —
// "ФПЧ2.5, µg/m³" — and that is the honest caption for a map whose metric is
// switchable: "Air quality" is simply wrong when the map is painting
// temperature, and it is the same phrase whichever metric is showing.
//
// The unit is second and the name first, because the name is what the reader
// is looking for; a key with no unit still says something, a key with only a
// unit does not. So a missing unit degrades to the name alone, and only a
// missing name falls back to the generic title.
export function legendTitle({ label, unit, fallback }) {
  const name = (label || '').trim()
  const measure = (unit || '').trim()
  if (!name) return fallback || ''
  return measure ? `${name}, ${measure}` : name
}

// legendRows turns the band table into the two parts the key renders: the bands
// themselves and the no-data row. Pure, so the edge arithmetic is testable
// without a DOM.
//
// Two parts rather than one flat list because they are not the same thing. The
// bands are a scale — they touch, they are ordered, they carry a boundary
// number. No-data is not a step on that scale; it is the absence of one, and
// the kit renders it outside the bar as .scale__none. A flat array made the
// renderer slice the last element off and hope, which is a shape that silently
// mis-renders the moment a band table is empty.
//
// A band knows only its own inclusive upper bound. That upper bound is also the
// number that gets DRAWN: the bands run highest-first, so a band's upper bound
// is the boundary at its top edge, shared with the band above it.
//
// The topmost band is open (upper === null), so the number at the very top of
// the key is the scale's ceiling — the value the ramp is drawn to. Without it
// the key ran 5, 10, 20, 25, 50 and then stopped, which says nothing about what
// the top of the bar means; a reader seeing a magenta dot could not tell 60 from
// 400. A scale with no ceiling still draws no top edge rather than inventing one.
export function legendRows(bands, { noDataColour, noDataLabel, lang }) {
  return {
    bands: (bands ?? []).map((band) => {
      const edge = band.upper ?? band.ceiling
      return {
        colour: band.colour,
        label: lang === 'bg' ? band.label_bg : band.label,
        edge: edge == null ? '' : String(edge),
      }
    }),
    noData: { colour: noDataColour, label: noDataLabel },
  }
}

// renderLegend replaces the key's contents in place. `el` is the <details> that
// carries the kit's .scale--onmap classes; the element itself owns the open
// state, so repainting its children never collapses it.
//
// Swatches are SVG <rect fill="…">, not a styled <span>. Band colours are
// server data, so the only CSS route would be a style attribute — and the CSP
// has no 'unsafe-inline' for style-src, so the browser drops it silently and
// the swatch renders invisible. A presentation attribute is not an inline
// style and is not covered by style-src. This is also why the kit's
// .chip__swatch is not used here: it paints from var(--chip-ramp), a property
// the app can only set per-row through the attribute the CSP forbids.
//
// The bar IS emitted, but never the kit mockup's copy of it: that one paints a
// hardcoded six-stop EAQI gradient, and this key is drawn for every metric,
// whose bands are served and differ — temperature's scale is not PM2.5's.
// rampGradient below builds it from the same band table the hexes are painted
// from, so the key cannot show a colour the map does not use.
// `info`, when given, adds the (i) that opens the scale dialog. Optional
// because the key is drawn once before any scale has loaded, and an (i) that
// opens an empty dialog is worse than no (i) at all.
export function renderLegend(el, { title, toggleLabel, bands, noData, info }) {
  el.replaceChildren()

  // The progressive bar replaces the stacked blocks, which is what makes the
  // key small enough to sit ON the map: six named rows are a panel, one 20px
  // column with numbers beside it is a key. The class goes on only when there
  // is a ramp to draw — a metric with no band table keeps the blocks.
  const ramp = rampGradient(bands)
  el.classList.toggle('scale--progressive', ramp !== '')
  // A custom property has no attribute form, so this one value goes through
  // CSSOM. That is not what style-src blocks: the CSP drops style attributes
  // and <style> blocks the PARSER sees, and a script writing to el.style is
  // neither. The swatches below still use presentation attributes, because
  // those are per-element and this is one declaration on the container.
  if (ramp) el.style.setProperty('--ramp', ramp)
  else el.style.removeProperty('--ramp')

  // Icon-only: the triangle already says what it does, and a word beside it
  // pushed the whole bar to the right of itself. An icon-only control still
  // needs a name, so the name moves to aria-label.
  const toggle = document.createElement('summary')
  toggle.className = 'scale__toggle'
  toggle.setAttribute('aria-label', toggleLabel)
  el.appendChild(toggle)

  const label = document.createElement('span')
  label.className = 'scale__label'
  label.textContent = title
  el.appendChild(label)

  // <ol>, not <ul>: the bands are ordered, and the order is the meaning.
  // Highest at the top, the way a thermometer reads — the boundary numbers sit
  // on the seams between the segments they divide, so the sequence has to run
  // one way and it is the served order reversed.
  const list = document.createElement('ol')
  list.className = 'scale__bands scale__bands--vertical'
  if (ramp) {
    // An <li>, because an <ol> may hold nothing else — and the bar is not a
    // band: it is all of them, painted over the rows it spans. aria-hidden for
    // the same reason the edge numbers are: the rows underneath are what a
    // screen reader reads.
    const bar = document.createElement('li')
    bar.className = 'scale__bar'
    bar.setAttribute('aria-hidden', 'true')
    list.appendChild(bar)
  }
  for (const band of [...bands].reverse()) {
    const item = document.createElement('li')
    item.className = 'scale__band'
    item.appendChild(swatch(band.colour, 'scale__band-swatch'))

    const name = document.createElement('span')
    name.className = 'scale__band-name'
    name.textContent = band.label
    item.appendChild(name)

    // Always present, even when empty: it is positioned absolutely against its
    // band, and a row that omits it on the open top band would be the only row
    // whose box differs. aria-hidden because the number is a duplicate — the
    // band name beside it is what the key is actually saying.
    const edge = document.createElement('span')
    edge.className = 'scale__band-edge'
    edge.setAttribute('aria-hidden', 'true')
    edge.textContent = band.edge
    item.appendChild(edge)

    list.appendChild(item)
  }
  el.appendChild(list)

  // Outside the bar, because grey is not a step on the scale. Present even for
  // a metric with no band table at all: grey dots are on the map either way.
  const none = document.createElement('p')
  none.className = 'scale__none'
  const row = document.createElement('span')
  row.className = 'legend__row'
  row.appendChild(swatch(noData.colour, 'legend-swatch'))
  const noneLabel = document.createElement('span')
  noneLabel.className = 'legend__label'
  noneLabel.textContent = noData.label
  row.appendChild(noneLabel)
  none.appendChild(row)
  el.appendChild(none)

  // Last, and inside the fold: the reader who wants the guideline behind the
  // colours has already found the key and opened it. Icon-only for the same
  // reason the toggle is — the key sits ON the map and a word here would widen
  // it — so the name goes to aria-label.
  if (info) {
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'scale__info'
    button.setAttribute('aria-label', info.label)
    button.addEventListener('click', info.onOpen)
    el.appendChild(button)
  }
}

// The bar's gradient, and the map's colours, are now one function in ramp.js —
// re-exported here because this is where the key's callers already look for it.
//
// It used to be hard stops, one pair per band: the map snapped every reading to
// its band's colour, so a blended bar would have shown colours nothing on screen
// used. Now the map blends too, and the same rule points the other way — a
// stepped bar would hide every distinction the map draws.
//
// The band colours still land on their own rows. Each band is one evenly spaced
// row, and the ramp anchors a band's colour at the middle of its share of the
// bar, so each row's centre is exactly its own colour and the blending happens
// across the seams, where the boundary numbers are.
export { rampGradient }

// preserveAspectRatio="none" because the caller sizes the element from CSS and
// the two uses are different shapes: the band swatch is a 20px column stretched
// to its band's height, the no-data swatch is a small square. The default
// letterboxes the rect inside whichever box it lands in and leaves a gap the
// band beside it does not have.
function swatch(colour, className) {
  const NS = 'http://www.w3.org/2000/svg'
  const svg = document.createElementNS(NS, 'svg')
  svg.setAttribute('class', className)
  svg.setAttribute('viewBox', '0 0 1 1')
  svg.setAttribute('preserveAspectRatio', 'none')
  svg.setAttribute('aria-hidden', 'true')
  const rect = document.createElementNS(NS, 'rect')
  rect.setAttribute('width', '1')
  rect.setAttribute('height', '1')
  rect.setAttribute('fill', colour)
  svg.appendChild(rect)
  return svg
}
