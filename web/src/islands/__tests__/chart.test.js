import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '../chart.js'
import { clearCache } from '../../lib/api.js'

// These tests drive mount() with a plain object standing in for the element —
// {dataset, textContent} — exactly as main.test.js drives runIsland and as
// map.test.js drives readConfig. The paths exercised here never touch a DOM
// API: both return before uPlot is constructed, so there is no jsdom and no
// component render, only the decision of what the reader is told.
function fakeEl(dataset) {
  return { dataset, textContent: '', clientWidth: 600 }
}

const CFG = {
  slug: 'sofia',
  metric: 'P2',
  period: '24h',
  tEmpty: 'No readings in the last 24 hours',
  tUnavailable: 'Data is unavailable right now',
}

beforeEach(() => {
  clearCache()
  vi.restoreAllMocks()
})

describe('mount, failed fetch', () => {
  // The point of the whole fix: a 429 or a 5xx used to leave the container
  // empty. On an air-quality page, an empty container is indistinguishable from
  // "nothing to report" — from clean air — which is the worst way this site can
  // fail.
  it('tells the reader the data is unavailable when the request fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false, status: 500, headers: new Headers() })))
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    const el = fakeEl({ ...CFG })

    await mount(el)

    expect(el.textContent).toBe(CFG.tUnavailable)
    // The console.error is kept deliberately: a developer still needs the cause.
    expect(errors).toHaveBeenCalled()
  })

  it('also explains a 429 the retry could not clear', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false,
      status: 429,
      headers: new Headers({ 'Retry-After': '86400' }), // beyond the retry cap
    })))
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const el = fakeEl({ ...CFG })

    await mount(el)

    expect(el.textContent).toBe(CFG.tUnavailable)
  })
})

describe('mount, empty series', () => {
  // The empty case must stay distinct from the failure case: "no readings" and
  // "we could not load the readings" are different statements about the world.
  it('says there are no readings rather than reporting a failure', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => ({ t: [], v: [] }),
    })))
    const el = fakeEl({ ...CFG })

    await mount(el)

    expect(el.textContent).toBe(CFG.tEmpty)
  })
})

describe('mount, no slug', () => {
  it('leaves the server-rendered content untouched and makes no request', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const el = fakeEl({ ...CFG, slug: undefined })

    await mount(el)

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(el.textContent).toBe('')
  })
})
