import { describe, it, expect, vi, beforeEach } from 'vitest'

// chart.js is now only a mount point: it decides whether to mount at all (the
// no-slug case) and builds the URL from the dataset (the island's business,
// not Chart.svelte's — see chart.js's own comment). Everything past that —
// fetching, the empty/unavailable branches, uPlot construction — moved to
// Chart.svelte and is proven in
// ../../components/__tests__/chart.component.test.js instead.
//
// 'svelte''s mount is mocked so this suite can assert on what chart.js hands
// the component (the target element, the built URL, the passed-through
// props) without needing jsdom or a real Chart.svelte render.
const mountCalls = []
vi.mock('svelte', () => ({
  mount: vi.fn((component, opts) => { mountCalls.push({ component, opts }) }),
}))

const { mount } = await import('../chart.js')

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
  vi.restoreAllMocks()
  mountCalls.length = 0
})

describe('mount, no slug', () => {
  it('leaves the server-rendered content untouched and does not mount the component', async () => {
    const el = fakeEl({ ...CFG, slug: undefined })

    mount(el)

    expect(mountCalls).toHaveLength(0)
    expect(el.textContent).toBe('')
  })
})

// J6 (review round 2): the deleted `el.dataset.metric || 'P2'` and
// `el.dataset.period || '24h'` fallbacks must stay deleted. Proven here by
// reading the URL prop chart.js hands the component: with no fallback,
// encodeURIComponent(undefined) === 'undefined' appears literally in the
// query string; a reinstated fallback would put 'P2'/'24h' there instead,
// which this assertion would catch.
describe('mount, metric and period have no JS-side fallback', () => {
  it('builds a URL from exactly what the dataset carries, not a hardcoded default', () => {
    const el = fakeEl({ slug: 'sofia', tUnavailable: CFG.tUnavailable }) // metric, period absent

    mount(el)

    expect(mountCalls).toHaveLength(1)
    const url = mountCalls[0].opts.props.url
    expect(url).toContain('metric=undefined')
    expect(url).toContain('period=undefined')
    expect(url).not.toContain('P2')
    expect(url).not.toContain('24h')
  })
})
