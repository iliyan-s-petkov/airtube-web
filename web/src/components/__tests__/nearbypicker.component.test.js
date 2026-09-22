// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import NearbyPicker from '../NearbyPicker.svelte'

const options = [
  { key: 'min', label: 'Min' },
  { key: 'max', label: 'Max' },
  { key: 'median', label: 'Median' },
]

let component
afterEach(() => { if (component) unmount(component) })

function render(props) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(NearbyPicker, {
    target,
    props: { options, legend: 'Nearby', offLabel: 'Off', selected: [], onchange: () => {}, ...props },
  })
  return target
}

const button = (t) => t.querySelector('button')

describe('NearbyPicker.svelte', () => {
  it('names the offLabel when nothing is ticked', () => {
    const t = render({})
    expect(button(t).textContent.trim()).toBe('Nearby: Off')
  })

  // The caret hints that the button opens options; decorative, so it must not
  // add to the button's accessible name.
  it('shows exactly one aria-hidden caret, last in the button', () => {
    const t = render({})
    const carets = button(t).querySelectorAll('.colmenu__caret')
    expect(carets.length).toBe(1)
    expect(carets[0].getAttribute('aria-hidden')).toBe('true')
    expect(button(t).lastElementChild).toBe(carets[0])
  })
})
