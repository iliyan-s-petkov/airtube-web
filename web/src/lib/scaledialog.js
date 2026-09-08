import { scaleInfo } from './scaleinfo.js'

// The (i) beside the key, and the dialog it opens.
//
// A native <dialog> with showModal(), not a div: the focus trap, the Escape
// key, the inert background and the return of focus to the button are all
// browser behaviour here, and every hand-rolled version of them is a
// keyboard-accessibility bug waiting to be filed.
//
// Swatches are SVG <rect fill="…"> for the reason legend.js gives: band colours
// are server data, style-src has no 'unsafe-inline', and a presentation
// attribute is not an inline style.

// createScaleDialog builds the dialog once and returns a handle. `show` repaints
// it from a scale and opens it; the button's own visibility is the caller's
// business, because only the caller knows whether a scale has loaded yet.
export function createScaleDialog(doc, { closeLabel, sourceLabel, disclaimer, lang }) {
  const el = doc.createElement('dialog')
  el.className = 'scaleinfo'

  const heading = doc.createElement('h2')
  heading.className = 'scaleinfo__title'
  el.appendChild(heading)

  const notes = doc.createElement('p')
  notes.className = 'scaleinfo__notes'
  el.appendChild(notes)

  const list = doc.createElement('ol')
  list.className = 'scaleinfo__bands'
  el.appendChild(list)

  const note = doc.createElement('p')
  note.className = 'scaleinfo__disclaimer'
  note.textContent = disclaimer || ''
  note.hidden = !disclaimer
  el.appendChild(note)

  const source = doc.createElement('a')
  source.className = 'link scaleinfo__source'
  source.textContent = sourceLabel || ''
  // The guideline is somebody else's site: rel keeps the opener unreachable
  // from it, which is the one thing target="_blank" would otherwise hand over.
  source.target = '_blank'
  source.rel = 'noopener noreferrer'
  el.appendChild(source)

  const close = doc.createElement('button')
  close.type = 'button'
  close.className = 'scaleinfo__close'
  close.textContent = closeLabel || ''
  close.addEventListener('click', () => el.close())
  el.appendChild(close)

  return {
    el,
    show(scale) {
      const info = scaleInfo(scale, lang)
      if (!info) return
      heading.textContent = info.unit ? `${info.name}, ${info.unit}` : info.name
      notes.textContent = info.notes
      notes.hidden = !info.notes

      list.replaceChildren()
      for (const row of info.rows) {
        const item = doc.createElement('li')
        item.className = 'scaleinfo__band'
        item.appendChild(swatch(doc, row.colour))

        const name = doc.createElement('span')
        name.className = 'scaleinfo__band-name'
        name.textContent = row.label
        item.appendChild(name)

        const range = doc.createElement('span')
        range.className = 'scaleinfo__band-range'
        range.textContent = row.range
        item.appendChild(range)

        list.appendChild(item)
      }

      // A table that cites nobody gets no link at all rather than one pointing
      // nowhere: the weather bands orient a reader and claim no authority, and
      // a dead "official guideline" would claim one for them.
      source.hidden = !info.source
      if (info.source) source.href = info.source
      else source.removeAttribute('href')

      // showModal throws if it is already open — repainting an open dialog is
      // what a metric switch behind it does.
      if (!el.open) el.showModal()
    },
  }
}

function swatch(doc, colour) {
  const NS = 'http://www.w3.org/2000/svg'
  const svg = doc.createElementNS(NS, 'svg')
  svg.setAttribute('class', 'scaleinfo__swatch')
  svg.setAttribute('viewBox', '0 0 1 1')
  svg.setAttribute('preserveAspectRatio', 'none')
  svg.setAttribute('aria-hidden', 'true')
  const rect = doc.createElementNS(NS, 'rect')
  rect.setAttribute('width', '1')
  rect.setAttribute('height', '1')
  rect.setAttribute('fill', colour)
  svg.appendChild(rect)
  return svg
}
