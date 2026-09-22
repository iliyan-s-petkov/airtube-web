// @vitest-environment jsdom
import { describe, it, expect, afterEach } from 'vitest'
import { mount, unmount } from 'svelte'
import RefreshButton from '../RefreshButton.svelte'

let component
afterEach(() => { if (component) unmount(component) })

function render(props) {
  const target = document.createElement('div')
  document.body.appendChild(target)
  component = mount(RefreshButton, { target, props })
  return target
}

describe('RefreshButton.svelte', () => {
  // The toolbar dress wraps the word in its own span so the phone layout can
  // clip it visually (app.css .toolbar__refresh .btn__label) without taking
  // the accessible name away from the button.
  it('wraps the toolbar label in .btn__label', () => {
    const target = render({ label: 'Обнови', variant: 'toolbar', onrefresh: () => {} })
    const btn = target.querySelector('button')
    const span = btn.querySelector('.btn__label')
    expect(span).not.toBeNull()
    expect(span.textContent.trim()).toBe('Обнови')
    expect(btn.textContent.trim()).toBe('Обнови')
  })

  // The icon variant carries no visible label at all — title/aria-label speak
  // for it — so it must not grow a .btn__label span it would then have to hide.
  it('renders no label span in the icon variant', () => {
    const target = render({ label: 'Обнови', variant: 'icon', onrefresh: () => {} })
    expect(target.querySelector('.btn__label')).toBeNull()
    expect(target.querySelector('button').getAttribute('aria-label')).toBe('Обнови')
  })
})
