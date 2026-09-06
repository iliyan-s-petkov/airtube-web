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
// `el.dataset.period || '24h'` fallbacks must stay deleted. The URL now lives
// in ChartPanel, so the absence is proven one step earlier: the props the
// island hands over must be undefined rather than a substituted default.
describe('mount, metric and period have no JS-side fallback', () => {
  it('passes exactly what the dataset carries, not a hardcoded default', () => {
    const el = fakeEl({ slug: 'sofia', tUnavailable: CFG.tUnavailable }) // metric, period absent

    mount(el)

    expect(mountCalls).toHaveLength(1)
    const props = mountCalls[0].opts.props
    expect(props.metric).toBeUndefined()
    expect(props.initialPeriod).toBeUndefined()
  })
})

// The period vocabulary is the server's, arriving as two comma-joined lists
// read by index. Splitting an empty attribute with String.split yields [''] —
// one nameless option in the switcher — so the empty case is pinned.
describe('mount, the period vocabulary', () => {
  it('splits the parallel lists the server rendered', () => {
    const el = fakeEl({ ...CFG, periods: '24h,7d,30d,1y', periodLabels: '24 hours,7 days,30 days,1 year' })

    mount(el)

    const props = mountCalls[0].opts.props
    expect(props.periods).toEqual(['24h', '7d', '30d', '1y'])
    expect(props.periodLabels).toEqual(['24 hours', '7 days', '30 days', '1 year'])
    expect(props.initialPeriod).toBe('24h')
  })

  it('treats an absent list as no options rather than one blank one', () => {
    const el = fakeEl({ ...CFG })

    mount(el)

    expect(mountCalls[0].opts.props.periods).toEqual([])
    expect(mountCalls[0].opts.props.periodLabels).toEqual([])
  })
})
