// @vitest-environment jsdom
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest'
import { unmount, flushSync } from 'svelte'
import { mount as mountRefresh } from '../refresh.js'
import { mount as mountFreshness } from '../freshness.js'
import { getFreshness, resetFreshnessForTests } from '../../lib/freshness.svelte.js'

let components = []
beforeEach(() => resetFreshnessForTests())
afterEach(() => {
  for (const c of components) unmount(c)
  components = []
  document.body.innerHTML = ''
  resetFreshnessForTests()
})

function island(dataset) {
  const el = document.createElement('div')
  Object.assign(el.dataset, dataset)
  document.body.appendChild(el)
  return el
}

function keep(c) { components.push(c); return c }

describe('refresh island', () => {
  it('renders the toolbar button from the attributes the server wrote', () => {
    const el = island({ island: 'refresh', tLabel: 'Обнови' })
    keep(mountRefresh(el))
    const btn = el.querySelector('button')
    expect(btn.textContent.trim()).toBe('Обнови')
    expect(btn.className).toBe('btn btn--secondary toolbar__refresh')
  })

  // The button knows nothing about the map: it asks the shared store, and the
  // map is what registered how to reload. This is the whole seam.
  it('drives whatever the store has been told a reload means', async () => {
    const el = island({ island: 'refresh', tLabel: 'Обнови' })
    let reloads = 0
    getFreshness().provide(async () => { reloads += 1 })
    keep(mountRefresh(el))
    el.querySelector('button').click()
    flushSync()
    await Promise.resolve()
    expect(reloads).toBe(1)
  })

  it('renders with no provider registered at all', () => {
    const el = island({ island: 'refresh', tLabel: 'Обнови' })
    keep(mountRefresh(el))
    expect(() => el.querySelector('button').click()).not.toThrow()
  })
})

describe('freshness island', () => {
  const attrs = {
    island: 'freshness',
    tLabel: 'Обнови',
    tUpdated: 'Данни от',
    tLoading: 'Обновяване…',
    tFailed: 'Обновяването не успя',
    tAuto: 'Автоматично обновяване',
  }

  it('states a time from the first paint, because the page was rendered fresh', () => {
    document.documentElement.setAttribute('lang', 'bg')
    const el = island({ ...attrs, button: 'false' })
    keep(mountFreshness(el))
    expect(el.querySelector('.data-refresh__status').textContent).toMatch(/^Данни от \d{2}:\d{2}$/)
  })

  // data-button is the only difference between the two pages, and it arrives
  // as the string an HTML attribute always is.
  it('carries the button only when the template asks for one', () => {
    const home = island({ ...attrs, button: 'false' })
    keep(mountFreshness(home))
    expect(home.querySelector('button')).toBe(null)

    const area = island({ ...attrs, button: 'true' })
    keep(mountFreshness(area))
    expect(area.querySelector('button.data-refresh__btn').textContent.trim()).toBe('Обнови')
  })

  it('writes the reader choice back to the store', () => {
    const el = island({ ...attrs, button: 'false' })
    keep(mountFreshness(el))
    const box = el.querySelector('input[type="checkbox"]')
    expect(box.checked).toBe(true)
    box.checked = false
    box.dispatchEvent(new Event('change', { bubbles: true }))
    flushSync()
    expect(getFreshness().auto).toBe(false)
  })

  // A line and a button on the same page are two views of one request: if they
  // disagreed, the reader would see a button doing nothing.
  it('follows the same store the button drives', async () => {
    const el = island({ ...attrs, button: 'true' })
    let release
    getFreshness().provide(() => new Promise((resolve) => { release = resolve }))
    keep(mountFreshness(el))
    el.querySelector('button').click()
    flushSync()
    expect(el.querySelector('.data-refresh__status').textContent).toBe('Обновяване…')
    release()
    await new Promise((resolve) => setTimeout(resolve, 0))
    flushSync()
    expect(el.querySelector('.data-refresh__status').textContent).toMatch(/^Данни от /)
  })

  it('reports a failure rather than a stale time', async () => {
    const errs = vi.spyOn(console, 'error').mockImplementation(() => {})
    const el = island({ ...attrs, button: 'true' })
    getFreshness().provide(async () => { throw new Error('offline') })
    keep(mountFreshness(el))
    await getFreshness().request()
    flushSync()
    expect(el.querySelector('.data-refresh__status').textContent).toBe('Обновяването не успя')
    errs.mockRestore()
  })
})
