// The map legend: the ramp, and what a dot means at the current zoom.
//
// Both halves exist because neither is inferable from the map. The ramp is
// server-defined (/api/v1/scales) and its band labels ship translated, so no
// catalogue entry is needed here. The tier line is the harder one: an area page
// prints a sensor count while its dots may be city aggregates, and without a
// line saying so the two honest numbers read as a contradiction.

// legendRows turns the band table into display rows. Pure, so the range
// arithmetic is testable without a DOM.
//
// A band knows only its own inclusive upper bound; the lower bound is the
// PREVIOUS band's upper and appears nowhere in its own record. The no-data row
// is always last and always present — grey dots are on the map even for a
// metric with no band table at all.
export function legendRows(bands, { noDataColour, noDataLabel, lang }) {
  const rows = []
  let lower = null
  for (const band of bands ?? []) {
    rows.push({
      colour: band.colour,
      label: lang === 'bg' ? band.label_bg : band.label,
      range: rangeText(lower, band.upper),
    })
    lower = band.upper
  }
  rows.push({ colour: noDataColour, label: noDataLabel, range: '' })
  return rows
}

function rangeText(lower, upper) {
  if (upper == null) return `> ${lower}`
  if (lower == null) return `≤ ${upper}`
  return `${lower}–${upper}`
}

// renderLegend replaces the legend's contents in place.
//
// Swatches are SVG <rect fill="…">, not a styled <span>. Band colours are
// server data, so the only CSS route would be a style attribute — and the CSP
// has no 'unsafe-inline' for style-src, so the browser drops it silently and
// the swatch renders invisible. A presentation attribute is not an inline
// style and is not covered by style-src.
export function renderLegend(el, { title, rows, tierText }) {
  el.replaceChildren()

  const heading = document.createElement('p')
  heading.className = 'legend-title'
  heading.textContent = title
  el.appendChild(heading)

  const list = document.createElement('ul')
  list.className = 'legend-ramp'
  for (const row of rows) {
    const item = document.createElement('li')
    item.appendChild(swatch(row.colour))

    const label = document.createElement('span')
    label.className = 'legend-label'
    label.textContent = row.label
    item.appendChild(label)

    if (row.range) {
      const range = document.createElement('span')
      range.className = 'legend-range'
      range.textContent = row.range
      item.appendChild(range)
    }
    list.appendChild(item)
  }
  el.appendChild(list)

  // Unconditional, unlike the old zoom hint: that only appeared when the sensor
  // tier was refused for want of a slug, which is never true on an area page —
  // exactly the page where the reader most needs to know what a dot aggregates.
  if (tierText) {
    const tier = document.createElement('p')
    tier.className = 'legend-tier'
    tier.textContent = tierText
    el.appendChild(tier)
  }
}

function swatch(colour) {
  const NS = 'http://www.w3.org/2000/svg'
  const svg = document.createElementNS(NS, 'svg')
  svg.setAttribute('class', 'legend-swatch')
  svg.setAttribute('viewBox', '0 0 8 8')
  svg.setAttribute('aria-hidden', 'true')
  const rect = document.createElementNS(NS, 'rect')
  rect.setAttribute('width', '8')
  rect.setAttribute('height', '8')
  rect.setAttribute('fill', colour)
  svg.appendChild(rect)
  return svg
}
