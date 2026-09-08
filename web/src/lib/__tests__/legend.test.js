// @vitest-environment jsdom
//
// jsdom for the renderLegend half: the swatch is built with createElementNS and
// the assertion that matters is on a real attribute, not on a string of markup.
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it, expect, vi } from 'vitest'
import { LEGEND_CLASSES, legendRows, legendTitle, rampGradient, renderLegend } from '../legend.js'

// Shaped like /api/v1/scales: ascending, upper INCLUSIVE, the top band open
// (upper === null), and both label languages present — internal/api/scales.go
// carries label and label_bg, and scales_test.go rejects an empty one. The
// legend therefore needs no catalogue entry for the ramp itself.
const BANDS = [
  { upper: 20, colour: '#3c9', label: 'Good', label_bg: 'Добро' },
  { upper: 50, colour: '#fc3', label: 'Moderate', label_bg: 'Умерено' },
  { upper: null, colour: '#c33', label: 'Poor', label_bg: 'Лошо' },
]

const OPTS = { noDataColour: '#999', noDataLabel: 'Недостатъчно данни', lang: 'bg' }

describe('legendRows', () => {
  it('separates the bands from the no-data row', () => {
    const { bands, noData } = legendRows(BANDS, OPTS)
    expect(bands).toHaveLength(3)
    expect(noData).toEqual({ colour: '#999', label: 'Недостатъчно данни' })
  })

  // The number drawn on a band is its own upper bound, because the bands run
  // highest-first and that bound is the boundary along its TOP edge — shared
  // with the band above it. The topmost band is open and has no such boundary.
  it('gives each band the boundary at its own top edge, and the open band none', () => {
    expect(legendRows(BANDS, OPTS).bands.map((b) => b.edge)).toEqual(['20', '50', ''])
  })

  // Without it the key stops at the last band boundary and never says what the
  // top of the bar means — a reader seeing the top colour cannot tell 60 from 400.
  it('prints the scale ceiling at the top of the bar when the top band carries one', () => {
    const withCeiling = BANDS.map((b, i) => (i === 2 ? { ...b, ceiling: 500 } : b))
    expect(legendRows(withCeiling, OPTS).bands.map((b) => b.edge)).toEqual(['20', '50', '500'])
  })

  it('picks the Bulgarian label for bg and the English one otherwise', () => {
    expect(legendRows(BANDS, OPTS).bands[0].label).toBe('Добро')
    expect(legendRows(BANDS, { ...OPTS, lang: 'en' }).bands[0].label).toBe('Good')
  })

  // A metric with no band table (see metricNote/hasScale in map.js) still gets
  // a legend, because grey dots are still on the map and still need explaining.
  it('returns no bands but still a no-data row when the metric has none', () => {
    const { bands, noData } = legendRows([], OPTS)
    expect(bands).toEqual([])
    expect(noData.label).toBe('Недостатъчно данни')
  })

  it('tolerates a missing scales response', () => {
    expect(legendRows(null, OPTS).bands).toEqual([])
  })
})

describe('renderLegend', () => {
  const draw = (over = {}) => {
    // A <details> carrying the container classes, the way mountChrome builds
    // it: the element owns the fold, so the renderer never creates it.
    const el = document.createElement('details')
    el.className = LEGEND_CLASSES
    el.open = true
    renderLegend(el, {
      title: 'Качество на въздуха',
      toggleLabel: 'Легенда',
      ...legendRows(BANDS, OPTS),
      info: { label: 'Какво означават цветовете', onOpen: () => {} },
      ...over,
    })
    return el
  }

  // The CSP has no 'unsafe-inline' for style-src, so a band colour written as
  // style="background:…" is dropped by the browser and the swatch renders
  // invisible — a failure no test that only checks for an element would catch.
  // An SVG fill is a presentation attribute, which style-src does not cover.
  it('paints swatches with an SVG fill attribute and never an inline style', () => {
    const el = draw()
    const fills = [...el.querySelectorAll('rect')].map((r) => r.getAttribute('fill'))
    expect(fills).toEqual(['#c33', '#fc3', '#3c9', '#999'])
    expect(el.querySelectorAll('[style]')).toHaveLength(0)
  })

  // The order IS the meaning: the boundary numbers are positioned on the seam
  // at the top of each band, so a list built in the served (ascending) order
  // puts every number against the wrong pair of colours.
  it('runs the bands highest first, with the boundary on each band top', () => {
    const el = draw()
    expect([...el.querySelectorAll('.scale__band-name')].map((n) => n.textContent))
      .toEqual(['Лошо', 'Умерено', 'Добро'])
    expect([...el.querySelectorAll('.scale__band-edge')].map((n) => n.textContent))
      .toEqual(['', '50', '20'])
  })

  // Grey is not a step on the scale, it is the absence of one — so it sits
  // outside the bar rather than as a fourth segment welded to the bottom of it.
  it('keeps the no-data row out of the bar', () => {
    const el = draw()
    expect(el.querySelectorAll('.scale__bands--vertical .scale__band')).toHaveLength(3)
    expect(el.querySelector('.scale__none').textContent).toBe('Недостатъчно данни')
  })

  // The summary is icon-only — the triangle already says what it does — and an
  // icon-only control still has to be announced as something.
  it('names the fold, which carries no text of its own', () => {
    const toggle = draw().querySelector('summary')
    expect(toggle.textContent).toBe('')
    expect(toggle.getAttribute('aria-label')).toBe('Легенда')
  })

  // The bar is drawn, and it is drawn from THESE bands — the kit mockup's own
  // bar is a hardcoded six-stop EAQI gradient, which over a map painting one of
  // seven served scales would be showing colours the map does not use.
  it('draws the bar from the served colours and nothing else', () => {
    const el = draw()
    expect(el.querySelector('.scale__bar')).not.toBeNull()
    const ramp = el.style.getPropertyValue('--ramp')
    for (const band of BANDS) expect(ramp).toContain(band.colour)
    expect(ramp).not.toContain('#50f0e6') // the mockup's own first stop
  })

  it('turns the progressive bar on only when there is a ramp to draw', () => {
    expect(draw().classList.contains('scale--progressive')).toBe(true)
    const bare = draw({ ...legendRows([], OPTS) })
    expect(bare.classList.contains('scale--progressive')).toBe(false)
    expect(bare.querySelector('.scale__bar')).toBeNull()
    expect(bare.style.getPropertyValue('--ramp')).toBe('')
  })

  // The bar is not a band, and the rows underneath it are the accessible copy.
  it('hides the bar from a screen reader', () => {
    expect(draw().querySelector('.scale__bar').getAttribute('aria-hidden')).toBe('true')
  })

  // The number beside a colour is what makes the bar a scale rather than a
  // "more is worse" arrow, so the rows keep their edges under the bar too.
  it('keeps the boundary numbers when the bar goes on', () => {
    expect([...draw().querySelectorAll('.scale__band-edge')].map((n) => n.textContent))
      .toEqual(['', '50', '20'])
  })

  // The legend is styled by the design kit, so every kit class it emits has to
  // be a class the kit actually defines. A misspelt BEM name is the worst kind
  // of failure here: nothing errors, no test that only looks for the element
  // fails, and the row simply renders unstyled. Read from components.css rather
  // than restated, so the kit renaming a class breaks this and not production.
  it('emits only kit classes that components.css defines', () => {
    // join(dirname(fileURLToPath(...))), not new URL(..., import.meta.url) —
    // see panel.test.js: Vite turns the latter into an asset import and then
    // rejects the path for being outside the project root.
    const here = dirname(fileURLToPath(import.meta.url))
    // The kit plus the app's own sheet: the (i) is not a kit control — the kit
    // has no notion of a scale citing an authority — and app.css is where the
    // app's own BEM classes live (.gauge__dial and the rest).
    const css = readFileSync(
      join(here, '..', '..', '..', '..', 'design-kit', 'components.css'), 'utf8')
      + readFileSync(join(here, '..', '..', '..', '..', 'internal', 'web', 'static', 'app.css'), 'utf8')
    const emitted = new Set(LEGEND_CLASSES.split(' '))
    const el = draw()
    // The container's own classes too: renderLegend adds one to it, and a
    // modifier the kit does not define is the same silent failure there.
    for (const c of el.classList) emitted.add(c)
    for (const node of el.querySelectorAll('*')) {
      // Both separators: the modifiers are what switch this from a block under
      // the map to an overlay on it, and are as easy to misspell as the parts.
      for (const c of node.classList) if (c.includes('__') || c.includes('--')) emitted.add(c)
    }
    expect([...emitted].sort()).toEqual([
      'legend__label', 'legend__row',
      'scale', 'scale--named', 'scale--onmap', 'scale--progressive', 'scale--vertical',
      'scale__band', 'scale__band-edge', 'scale__band-name', 'scale__band-swatch',
      'scale__bands', 'scale__bands--vertical', 'scale__bar', 'scale__info',
      'scale__label', 'scale__none', 'scale__toggle',
    ])
    for (const c of emitted) {
      expect(css, `components.css defines no .${c}`).toMatch(new RegExp(`\\.${c}\\b`))
    }
  })

  // showLegend is called on every refresh, including passes that fetch nothing.
  it('replaces its contents rather than appending on every repaint', () => {
    const el = draw()
    renderLegend(el, { title: 'x', toggleLabel: 'y', ...legendRows(BANDS, OPTS) })
    expect(el.querySelectorAll('.scale__bands')).toHaveLength(1)
    expect(el.querySelectorAll('summary')).toHaveLength(1)
  })

  // A reader who folded the key away must not have it reopened under them by
  // the next refresh. The state lives on the <details>, which the renderer does
  // not touch — this is the assertion that keeps it that way.
  it('leaves the fold as the reader left it', () => {
    const el = draw()
    el.open = false
    renderLegend(el, { title: 'x', toggleLabel: 'y', ...legendRows(BANDS, OPTS) })
    expect(el.open).toBe(false)
  })
})

describe('rampGradient', () => {
  // The bar is a blend now, and the map is too. One position per stop, never a
  // pair: a pair is a hard step, and a stepped key over a blended map would
  // hide every distinction the hexes draw.
  it('gives every stop one position, so the colours run together', () => {
    expect(rampGradient(BANDS)).toBe(
      'linear-gradient(to top, #3c9 0.000%, #3c9 16.667%, #fc3 50.000%, #c33 83.333%, #c33 100.000%)',
    )
  })

  // The rows are 34px each whatever their bands span, so the anchors have to be
  // evenly spaced. Spaced by value, every boundary number would sit against the
  // wrong pair of colours.
  it('spaces the anchors by row, not by how much value a band covers', () => {
    const wide = [
      { upper: 1, colour: '#a' },
      { upper: 500, colour: '#b' },
      { upper: null, colour: '#c' },
    ]
    expect(rampGradient(wide)).toBe(rampGradient(BANDS).replace(/#3c9|#fc3|#c33/g,
      (c) => ({ '#3c9': '#a', '#fc3': '#b', '#c33': '#c' })[c]))
  })

  // Bottom to top, matching the list above it, which runs highest-first.
  it('reads upward, the way the numbers beside it do', () => {
    expect(rampGradient(BANDS).startsWith('linear-gradient(to top, #3c9')).toBe(true)
  })

  it('draws nothing for a scale that is not one', () => {
    expect(rampGradient([])).toBe('')
    expect(rampGradient(null)).toBe('')
  })
})

// The caption of the key. "Air quality" is the same phrase for all seven
// metrics and is wrong outright once the map paints temperature.
describe('legendTitle', () => {
  it('names the metric and what it is measured in', () => {
    expect(legendTitle({ label: 'ФПЧ2.5', unit: 'µg/m³', fallback: 'x' })).toBe('ФПЧ2.5, µg/m³')
  })

  // A key with no unit still says something; a key with only a unit does not.
  it('degrades to the name alone with no unit', () => {
    expect(legendTitle({ label: 'ФПЧ2.5', unit: '', fallback: 'x' })).toBe('ФПЧ2.5')
    expect(legendTitle({ label: 'ФПЧ2.5', fallback: 'x' })).toBe('ФПЧ2.5')
  })

  it('falls back only when the metric has no name', () => {
    expect(legendTitle({ label: '', unit: 'µg/m³', fallback: 'Air quality' })).toBe('Air quality')
    expect(legendTitle({ fallback: 'Air quality' })).toBe('Air quality')
  })

  it('never renders the empty string as a name', () => {
    expect(legendTitle({ label: '   ', unit: 'µg/m³', fallback: 'Air quality' })).toBe('Air quality')
    expect(legendTitle({})).toBe('')
  })

  it('trims both halves', () => {
    expect(legendTitle({ label: ' PM2.5 ', unit: ' µg/m³ ' })).toBe('PM2.5, µg/m³')
  })
})

// The (i) is the only route to the guideline behind the colours, so it has to
// survive a repaint — and it must not appear before a scale has loaded, when it
// would open a dialog with nothing in it.
describe('the scale info button', () => {
  const drawWith = (info) => {
    const el = document.createElement('details')
    renderLegend(el, { title: 't', toggleLabel: 'l', ...legendRows(BANDS, OPTS), info })
    return el
  }

  it('is named, icon-only, and calls back on click', () => {
    const onOpen = vi.fn()
    const button = drawWith({ label: 'Какво означават цветовете', onOpen }).querySelector('.scale__info')
    expect(button.textContent).toBe('')
    expect(button.getAttribute('aria-label')).toBe('Какво означават цветовете')
    expect(button.type).toBe('button')
    button.click()
    expect(onOpen).toHaveBeenCalledTimes(1)
  })

  it('is absent while there is no scale to describe', () => {
    expect(drawWith(null).querySelector('.scale__info')).toBeNull()
  })
})

