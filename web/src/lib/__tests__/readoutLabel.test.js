import { describe, it, expect } from 'vitest'
import { splitReadoutLabel } from '../readoutLabel.js'

// Same separator as readoutMetric/readoutRest in internal/web/render.go, but
// the metric can sit on either side: the server's own labels put it first
// ("PM2.5 · Highest province figure"), the island's high/low/median labels
// put it last ("Highest · PM2.5"). Locating the known metric text rather than
// assuming a position keeps both paths hiding the same word on a phone.
describe('splitReadoutLabel', () => {
  it('splits a metric-first label the way the server template does', () => {
    expect(splitReadoutLabel('PM2.5 · Highest province figure', 'PM2.5')).toEqual({
      position: 'prefix', metric: 'PM2.5', rest: 'Highest province figure',
    })
  })

  it('splits a metric-last label, as the island composes high/low/median', () => {
    expect(splitReadoutLabel('Highest · PM2.5', 'PM2.5')).toEqual({
      position: 'suffix', metric: 'PM2.5', rest: 'Highest',
    })
  })

  it('leaves a label with no metric segment untouched', () => {
    expect(splitReadoutLabel('This sensor', 'PM2.5')).toEqual({
      position: 'none', metric: '', rest: 'This sensor',
    })
  })

  it('leaves a label untouched when no metric is given', () => {
    expect(splitReadoutLabel('Highest · PM2.5', '')).toEqual({
      position: 'none', metric: '', rest: 'Highest · PM2.5',
    })
  })
})
