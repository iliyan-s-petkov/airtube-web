import { describe, it, expect, vi, beforeEach } from 'vitest'
import { clearCache } from '../../lib/api.js'

// uPlot's real constructor manipulates DOM nodes (canvas, appendChild, ...);
// this suite runs with no jsdom, and `el` below is a plain object, not a real
// element. Mocking the module lets the "reaches uPlot" tests below run
// without either pulling in jsdom or leaving the stroke-colour and
// metric/period consumption unproven — the gap the coordinator's review (J5,
// J6) flagged. The mock records exactly the config object mount() builds and
// returns a stub with the one method chart.js calls afterwards.
const uplotCalls = []
vi.mock('uplot', () => ({
  default: class MockUPlot {
    constructor(opts, data, el) {
      uplotCalls.push({ opts, data, el })
    }
    setSize() {}
  },
}))

const { mount } = await import('../chart.js')

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
  uplotCalls.length = 0
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

// J6 (review round 2): the deleted `el.dataset.metric || 'P2'` and
// `el.dataset.period || '24h'` fallbacks must stay deleted. Proven here by
// omitting both attributes and reading the request URL the fetch call
// actually made: with no fallback, encodeURIComponent(undefined) === 'undefined'
// appears literally in the query string; a reinstated fallback would put
// 'P2'/'24h' there instead, which this assertion would catch.
describe('mount, metric and period have no JS-side fallback', () => {
  it('requests exactly what the dataset carries, not a hardcoded default', async () => {
    const fetchSpy = vi.fn(async () => ({ ok: false, status: 500, headers: new Headers() }))
    vi.stubGlobal('fetch', fetchSpy)
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const el = fakeEl({ slug: 'sofia', tUnavailable: CFG.tUnavailable }) // metric, period absent

    await mount(el)

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const requestedURL = fetchSpy.mock.calls[0][0]
    expect(requestedURL).toContain('metric=undefined')
    expect(requestedURL).toContain('period=undefined')
    expect(requestedURL).not.toContain('P2')
    expect(requestedURL).not.toContain('24h')
  })
})

// J5 (review round 2): the chart's line colour must come from
// cfg.lineColour (data-line-colour), not from cfg.title or any other field.
// uPlot's real constructor needs a DOM; mocked above so this test can reach
// the series config mount() actually builds without jsdom.
describe('mount, reaches uPlot construction', () => {
  it('passes cfg.lineColour as the series stroke, and cfg.title as the chart title — not swapped', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: async () => ({ t: ['2026-08-14T00:00:00Z'], v: [12.3] }),
    })))
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
    })
    const el = fakeEl({
      ...CFG,
      lineColour: '#2563eb',
      tTitle: 'PM2.5, Sofia', // deliberately distinct from lineColour, so a
      // mutation swapping stroke: cfg.title for stroke: cfg.lineColour fails
      // this assertion instead of coincidentally matching it.
    })

    await mount(el)

    expect(uplotCalls).toHaveLength(1)
    expect(uplotCalls[0].opts.title).toBe('PM2.5, Sofia')
    expect(uplotCalls[0].opts.series[1].stroke).toBe('#2563eb')
  })
})
