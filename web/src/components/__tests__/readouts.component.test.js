// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import Readouts from '../Readouts.svelte'

let component = null
afterEach(() => {
  if (component) unmount(component)
  component = null
  document.body.innerHTML = ''
})

function render(cards) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(Readouts, { target, props: { cards } })
  return target
}

const gauged = { label: 'Максимум · ФПЧ2.5', value: '30', unit: 'µg/m³', tier: 'Овча купел · 9 сензора', gauge: { percent: 60, colour: '#a00' } }
const plain = { label: 'Този сензор', value: '1', unit: 'от 9', tier: 'Под медианата', gauge: null }

describe('Readouts', () => {
  it('renders one card per figure, in the classes the server uses', () => {
    const el = render([gauged, plain])
    expect(el.querySelectorAll('.readout.card')).toHaveLength(2)
    expect(el.querySelector('.readout__label').textContent).toBe('Максимум · ФПЧ2.5')
    expect(el.querySelector('.readout__tier').textContent).toBe('Овча купел · 9 сензора')
  })

  // style-src is 'self' with no 'unsafe-inline': a style attribute would be
  // dropped and the arc would never paint. Presentation attributes, as in
  // base.gohtml's own strip.
  it('paints the arc with presentation attributes and no inline style', () => {
    const arc = render([gauged]).querySelector('.gauge__arc')
    expect(arc.getAttribute('stroke')).toBe('#a00')
    expect(arc.getAttribute('stroke-dasharray')).toBe('60 100')
    expect(arc.getAttribute('style')).toBe(null)
  })

  // A count has no scale to be a fraction of, so an arc around it would be an
  // invented proportion.
  it('leaves a card with no gauge a plain figure', () => {
    const el = render([plain])
    expect(el.querySelector('.gauge__dial')).toBe(null)
    // The unit sits inside the value, as it does in base.gohtml — with the
    // space Svelte would otherwise trim out of the markup.
    expect(el.querySelector('.readout__value').textContent).toBe('1 от 9')
    expect(el.querySelector('.readout__unit').textContent).toBe('от 9')
  })

  it('renders an empty strip for no cards at all', () => {
    expect(render([]).querySelectorAll('.readout')).toHaveLength(0)
  })
})
