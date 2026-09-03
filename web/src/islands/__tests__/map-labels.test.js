// Pure-logic tests, so no jsdom: labelLayout and labelPaint take a cfg and
// return MapLibre style objects, with no DOM and no map involved.
import { describe, it, expect } from 'vitest'
import { labelLayout, labelPaint } from '../map.js'

const cfg = { lang: 'bg', markerStrokeColour: '#ffffff', labelColour: '#161616' }

describe('the marker label layer', () => {
  // The reason this layer exists. Colour alone carried three different facts —
  // no reading, an unscaled metric, and a real band value — so a reader without
  // colour vision lost all three at once and the legend could only say what the
  // colours WOULD have meant.
  it('prints the reading, so colour is not the only channel', () => {
    const field = labelLayout(cfg)['text-field']
    expect(field[0]).toBe('number-format')
    expect(field[1]).toEqual(['get', 'value'])
  })

  // One decimal on every surface. The province list and the sensor panel both
  // show one; three surfaces showing the same reading to different precision
  // would be three surfaces disagreeing about one number.
  it('shows one decimal, matching the list and the panel', () => {
    const opts = labelLayout(cfg)['text-field'][2]
    expect(opts['min-fraction-digits']).toBe(1)
    expect(opts['max-fraction-digits']).toBe(1)
  })

  // The separator belongs to the language: 12,4 in Bulgarian, 12.4 in English.
  it('localises the decimal separator', () => {
    expect(labelLayout(cfg)['text-field'][2].locale).toBe('bg')
    expect(labelLayout({ ...cfg, lang: 'en' })['text-field'][2].locale).toBe('en')
  })

  // Crowding is handled by MapLibre dropping colliding labels rather than by a
  // zoom threshold guessing when areas converge. If this ever flips to true,
  // numbers start stacking on each other at country zoom.
  it('never lets one number draw on top of another', () => {
    const layout = labelLayout(cfg)
    expect(layout['text-allow-overlap']).toBe(false)
    expect(layout['text-ignore-placement']).toBe(false)
    expect(layout['text-optional']).toBe(true)
  })

  // The fontstack has to be one the basemap style already ships. A name the
  // style does not carry renders nothing at all — silently, with no error.
  it('asks only for a font the basemap already provides', () => {
    expect(labelLayout(cfg)['text-font']).toEqual(['Noto Sans Regular'])
  })

  // Beside the dot, not centred on it: the circle is 5-9px, so a centred number
  // would sit on its own stroke and lose contrast against every band colour.
  it('sits beside the dot rather than on it', () => {
    expect(labelLayout(cfg)['text-offset'][0]).toBeGreaterThan(0)
    expect(labelLayout(cfg)['text-anchor']).toBe('left')
  })

  // The halo is what keeps the number legible over every band from the palest
  // teal to the darkest purple, and over the basemap's own streets, without
  // re-tinting the served colour underneath.
  it('carries a halo so it reads on every band', () => {
    const paint = labelPaint(cfg)
    expect(paint['text-color']).toBe('#161616')
    expect(paint['text-halo-color']).toBe('#ffffff')
    expect(paint['text-halo-width']).toBeGreaterThan(1)
  })
})
