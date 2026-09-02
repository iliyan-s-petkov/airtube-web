// @vitest-environment jsdom
//
// jsdom for the renderLegend half: the swatch is built with createElementNS and
// the assertion that matters is on a real attribute, not on a string of markup.
import { describe, it, expect } from 'vitest'
import { legendRows, renderLegend } from '../legend.js'

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
  it('renders one row per band plus the no-data row last', () => {
    const rows = legendRows(BANDS, OPTS)
    expect(rows).toHaveLength(4)
    expect(rows[3]).toEqual({ colour: '#999', label: 'Недостатъчно данни', range: '' })
  })

  // The reader cannot infer the lower bound: it is the PREVIOUS band's upper,
  // which is nowhere in the band's own record.
  it('derives each band range from the previous band upper', () => {
    const rows = legendRows(BANDS, OPTS)
    expect(rows[0].range).toBe('≤ 20')
    expect(rows[1].range).toBe('20–50')
    expect(rows[2].range).toBe('> 50')
  })

  it('picks the Bulgarian label for bg and the English one otherwise', () => {
    expect(legendRows(BANDS, OPTS)[0].label).toBe('Добро')
    expect(legendRows(BANDS, { ...OPTS, lang: 'en' })[0].label).toBe('Good')
  })

  // A metric with no band table (see metricNote/hasScale in map.js) still gets
  // a legend, because grey dots are still on the map and still need explaining.
  it('returns only the no-data row when the metric has no bands', () => {
    const rows = legendRows([], OPTS)
    expect(rows).toEqual([{ colour: '#999', label: 'Недостатъчно данни', range: '' }])
  })

  it('tolerates a missing scales response', () => {
    expect(legendRows(null, OPTS)).toHaveLength(1)
  })
})

describe('renderLegend', () => {
  const draw = (over = {}) => {
    const el = document.createElement('div')
    renderLegend(el, {
      title: 'Качество на въздуха',
      rows: legendRows(BANDS, OPTS),
      tierText: 'Всяка точка е средно за град',
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
    const rects = el.querySelectorAll('.legend-swatch rect')
    expect([...rects].map((r) => r.getAttribute('fill'))).toEqual(['#3c9', '#fc3', '#c33', '#999'])
    expect(el.querySelectorAll('[style]')).toHaveLength(0)
  })

  it('renders a row per band plus no-data, with the ranges', () => {
    const el = draw()
    expect(el.querySelectorAll('.legend-ramp li')).toHaveLength(4)
    expect([...el.querySelectorAll('.legend-range')].map((n) => n.textContent))
      .toEqual(['≤ 20', '20–50', '> 50'])
  })

  // The reason the legend exists at all: an area page can print a sensor count
  // while showing city aggregates.
  it('states what a dot means, and omits the line when the tier is unknown', () => {
    expect(draw().querySelector('.legend-tier').textContent).toBe('Всяка точка е средно за град')
    expect(draw({ tierText: '' }).querySelector('.legend-tier')).toBeNull()
  })

  // showLegend is called on every refresh, including passes that fetch nothing.
  it('replaces its contents rather than appending on every repaint', () => {
    const el = draw()
    renderLegend(el, { title: 'x', rows: legendRows(BANDS, OPTS), tierText: 'y' })
    expect(el.querySelectorAll('.legend-ramp')).toHaveLength(1)
    expect(el.querySelectorAll('.legend-title')).toHaveLength(1)
  })
})
