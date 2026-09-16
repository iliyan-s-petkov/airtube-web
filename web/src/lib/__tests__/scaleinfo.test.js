// @vitest-environment jsdom
//
// jsdom for the dialog half: showModal, the <a rel> and the SVG fill are real
// DOM behaviour, and a string of markup would assert none of them.
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it, expect, vi } from 'vitest'
import { bandRange, scaleFor, scaleInfo } from '../scaleinfo.js'
import { createScaleDialog } from '../scaledialog.js'

// Shaped like /api/v1/scales, including the fields the key never reads: notes
// in both languages and the guideline link, which are the whole point of the
// dialog.
const EAQI = {
  name: 'eaqi', metric: 'P2', unit: 'µg/m³', ceiling: 500,
  notes: 'EAQI bands', notes_bg: 'Ленти по EAQI',
  source: 'https://airindex.eea.europa.eu/',
  bands: [
    { upper: 5, colour: '#3c9', label: 'Good', label_bg: 'Добро' },
    { upper: 10, colour: '#fc3', label: 'Fair', label_bg: 'Задоволително' },
    { upper: null, colour: '#c33', label: 'Poor', label_bg: 'Лошо' },
  ],
}
const METEO = { name: 'meteo', metric: 'temperature', unit: '°C', source: '', notes: 'Weather', notes_bg: 'Време', bands: [] }
const SCALES = [EAQI, METEO]

describe('scaleFor', () => {
  // Matched on metric, never on position: the same rule bandsFor applies, so a
  // reordered response cannot make the dialog describe a table the map is not
  // painting from.
  it('finds the table by metric, not by order', () => {
    expect(scaleFor(SCALES, 'temperature')).toBe(METEO)
    expect(scaleFor(SCALES, 'noise_LAeq')).toBeNull()
    expect(scaleFor(null, 'P2')).toBeNull()
  })
})

describe('bandRange', () => {
  // A band states only its own upper bound; the range a reader needs is between
  // that and the band below. Getting this wrong shifts every row by one band —
  // it would still look like a plausible table.
  it('reads each band lower edge off the band below it', () => {
    expect(EAQI.bands.map((_, i) => bandRange(EAQI.bands, i))).toEqual(['< 5', '5–10', '> 10'])
  })

  it('says nothing for a band bounded at neither end', () => {
    expect(bandRange([{ upper: null }], 0)).toBe('')
  })
})

describe('scaleInfo', () => {
  it('prints the rows in the reader language', () => {
    expect(scaleInfo(EAQI, 'bg').rows.map((r) => r.label)).toEqual(['Добро', 'Задоволително', 'Лошо'])
    expect(scaleInfo(EAQI, 'en').rows.map((r) => r.label)).toEqual(['Good', 'Fair', 'Poor'])
    expect(scaleInfo(EAQI, 'bg').notes).toBe('Ленти по EAQI')
  })

  it('carries the source through, and none where the scale cites none', () => {
    expect(scaleInfo(EAQI, 'en').source).toBe('https://airindex.eea.europa.eu/')
    expect(scaleInfo(METEO, 'en').source).toBe('')
  })

  it('is null for no scale at all, so the caller can withhold the button', () => {
    expect(scaleInfo(null, 'bg')).toBeNull()
  })
})

describe('createScaleDialog', () => {
  const open = (scale, lang = 'bg') => {
    const d = createScaleDialog(document, {
      closeLabel: 'Затвори', sourceLabel: 'Официалният документ',
      disclaimer: 'Индикативни данни', lang,
    })
    document.body.appendChild(d.el)
    // jsdom implements <dialog> but not showModal in every version the project
    // pins; stubbing it keeps the test about the contents, which is what this
    // module owns.
    d.el.showModal = vi.fn(() => { d.el.open = true })
    d.show(scale)
    return d.el
  }

  it('names the scale, its unit and every band with its range', () => {
    const el = open(EAQI)
    expect(el.querySelector('.scaleinfo__title').textContent).toBe('eaqi, µg/m³')
    expect([...el.querySelectorAll('.scaleinfo__band-range')].map((n) => n.textContent))
      .toEqual(['< 5', '5–10', '> 10'])
  })

  // The same CSP rule legend.js paints under: style-src has no 'unsafe-inline',
  // so a swatch coloured by a style attribute renders invisible.
  it('paints swatches with an SVG fill and no inline style', () => {
    const el = open(EAQI)
    expect([...el.querySelectorAll('rect')].map((r) => r.getAttribute('fill')))
      .toEqual(['#3c9', '#fc3', '#c33'])
    expect(el.querySelectorAll('[style]')).toHaveLength(0)
  })

  // The reason the dialog exists: a reader can check the claim instead of
  // taking the colours on trust.
  it('links the guideline, and opens it without handing over the opener', () => {
    const link = open(EAQI).querySelector('.scaleinfo__source')
    expect(link.hidden).toBe(false)
    expect(link.getAttribute('href')).toBe('https://airindex.eea.europa.eu/')
    expect(link.rel).toBe('noopener noreferrer')
  })

  // A weather axis claims no authority; an "official guideline" link pointing
  // nowhere would claim one for it.
  it('shows no link for a scale that cites nobody', () => {
    const link = open(METEO).querySelector('.scaleinfo__source')
    expect(link.hidden).toBe(true)
    expect(link.hasAttribute('href')).toBe(false)
  })

  // Every page that explains the bands has to say the readings are indicative.
  it('carries the disclaimer', () => {
    expect(open(EAQI).querySelector('.scaleinfo__disclaimer').textContent).toBe('Индикативни данни')
  })

  // The guideline link and the close button shared a line and overlapped, so
  // the end of the link sat underneath the button and could not be clicked.
  it('puts the close button last, after the guideline link', () => {
    const el = open(EAQI)
    const close = el.querySelector('.scaleinfo__close')
    expect(el.lastElementChild).toBe(close)
    expect(close.compareDocumentPosition(el.querySelector('.scaleinfo__source')))
      .toBe(Node.DOCUMENT_POSITION_PRECEDING)
  })

  // A modal that can only be dismissed from the bottom of a long table makes
  // the reader scroll to leave it; the corner mark is where one is looked for.
  it('offers a named dismiss mark in the corner, before anything else', () => {
    const el = open(EAQI)
    const dismiss = el.querySelector('.scaleinfo__dismiss')
    expect(el.firstElementChild).toBe(dismiss)
    expect(dismiss.type).toBe('button')
    expect(dismiss.getAttribute('aria-label')).toBe('Затвори')
    // The glyph is CSS content: a character in the markup is read out beside
    // the label, which is the same rule the (i) button follows.
    expect(dismiss.textContent).toBe('')
  })

  it('closes from the corner mark as well as from the button', () => {
    for (const sel of ['.scaleinfo__dismiss', '.scaleinfo__close']) {
      const el = open(EAQI)
      el.close = vi.fn(() => { el.open = false })
      el.querySelector(sel).click()
      expect(el.close, `${sel} did not close the dialog`).toHaveBeenCalled()
    }
  })

  // Repainting an already-open dialog is what a metric switch behind it does,
  // and showModal throws on an open dialog.
  it('repaints in place rather than reopening', () => {
    const d = createScaleDialog(document, { closeLabel: 'x', sourceLabel: 'y', disclaimer: '', lang: 'en' })
    document.body.appendChild(d.el)
    d.el.showModal = vi.fn(() => { d.el.open = true })
    d.show(EAQI)
    d.show(METEO)
    expect(d.el.showModal).toHaveBeenCalledTimes(1)
    expect(d.el.querySelectorAll('.scaleinfo__band')).toHaveLength(0)
  })
})

// The DOM order alone did not separate them: an <a> and a <button> are both
// inline, so they laid out on one line however they were ordered.
describe('the dialog footer', () => {
  const appCSS = () => {
    const here = dirname(fileURLToPath(import.meta.url))
    return readFileSync(join(here, '..', '..', '..', '..', 'internal', 'web', 'static', 'app.css'), 'utf8')
  }
  const rule = (css, selector) => {
    const at = css.indexOf(`${selector} {`)
    return at < 0 ? '' : css.slice(at, css.indexOf('}', at))
  }

  it('gives the guideline link its own line', () => {
    expect(rule(appCSS(), '.scaleinfo__source'), 'link still inline beside the button')
      .toContain('display: block')
  })

  it('centres the close button under everything', () => {
    const r = rule(appCSS(), '.scaleinfo__close')
    expect(r, 'button still inline').toContain('display: block')
    expect(r, 'button not centred').toContain('margin-inline: auto')
  })

  it('anchors the dismiss mark to the dialog corner', () => {
    const css = appCSS()
    expect(rule(css, '.scaleinfo'), 'dialog is not the positioning ancestor')
      .toContain('position: relative')
    const r = rule(css, '.scaleinfo__dismiss')
    expect(r, 'mark not taken out of flow').toContain('position: absolute')
    expect(r, 'mark not in the top-right corner').toContain('inset-inline-end')
    expect(css, 'mark has no glyph').toContain('.scaleinfo__dismiss::before')
    // The title runs the full width and would slide under the mark.
    expect(rule(css, '.scaleinfo__title'), 'title can run under the mark')
      .toContain('padding-inline-end')
  })
})
