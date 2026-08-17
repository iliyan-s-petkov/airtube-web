// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from 'vitest'
import { createViewState } from '../viewstate.svelte.js'

const opts = { metrics: ['P1', 'P2', 'temperature'], defaultMetric: 'P2' }

beforeEach(() => {
  history.replaceState(null, '', '/area/sofia')
})

describe('createViewState', () => {
  it('adopts the hash present at construction', () => {
    history.replaceState(null, '', '/area/sofia#metric=P1&sensor=42')
    const vs = createViewState(opts)
    expect(vs.metric).toBe('P1')
    expect(vs.sensorId).toBe(42)
    vs.destroy()
  })

  // A metric switch is a view setting, not a destination: five toggles must not
  // put five entries in the back stack for the visitor to press Back through.
  it('switches metric without growing the history stack', () => {
    const vs = createViewState(opts)
    const before = history.length
    vs.setMetric('P1')
    vs.setMetric('temperature')
    expect(history.length).toBe(before)
    expect(location.hash).toBe('#metric=temperature')
    vs.destroy()
  })

  // Opening a sensor IS a destination: Back must close the panel.
  it('pushes a history entry when a sensor opens', () => {
    const vs = createViewState(opts)
    const before = history.length
    vs.openSensor(42)
    expect(history.length).toBe(before + 1)
    expect(location.hash).toBe('#sensor=42')
    vs.destroy()
  })

  it('clears the hash entirely when returning to the default state', () => {
    const vs = createViewState(opts)
    vs.setMetric('P1')
    vs.setMetric('P2')
    expect(location.hash).toBe('')
    vs.destroy()
  })

  it('follows an external hash change', () => {
    const vs = createViewState(opts)
    history.replaceState(null, '', '#metric=temperature')
    dispatchEvent(new HashChangeEvent('hashchange'))
    expect(vs.metric).toBe('temperature')
    vs.destroy()
  })

  it('stops listening after destroy', () => {
    const vs = createViewState(opts)
    vs.destroy()
    history.replaceState(null, '', '#metric=temperature')
    dispatchEvent(new HashChangeEvent('hashchange'))
    expect(vs.metric).toBe('P2')
  })

  // Real browsers never fire hashchange synchronously from pushState/
  // replaceState, so jsdom can't exercise the `writing` re-entrancy guard the
  // way the rest of this file does. This fake `win` (the injection seam
  // createViewState accepts specifically so callers aren't stuck with real
  // `window`) stands in for a platform that *does* echo synchronously, and
  // does so with the hash still holding its OLD value — the worst case the
  // guard exists to make harmless. Without the guard, that reentrant call
  // would re-parse the stale hash and revert `metric` mid-write.
  it('ignores a reentrant hashchange fired synchronously from its own write', () => {
    let listener
    const win = {
      location: { pathname: '/area/sofia', search: '', hash: '' },
      addEventListener: (_type, fn) => { listener = fn },
      removeEventListener: () => { listener = null },
      history: {
        pushState: (_state, _title, url) => applyUrl(url),
        replaceState: (_state, _title, url) => applyUrl(url),
      },
    }
    function applyUrl(url) {
      if (listener) listener() // reentrant echo, hash not yet updated
      const i = url.indexOf('#')
      win.location.hash = i === -1 ? '' : url.slice(i)
    }

    const vs = createViewState({ ...opts, win })
    vs.setMetric('P1')
    expect(vs.metric).toBe('P1')
    vs.destroy()
  })

  // Real browsers enforce a pushState/replaceState rate limit and can throw
  // SecurityError. If `writing` were left true after a throw, the store would
  // silently stop reacting to every future hashchange — Back/Forward and
  // external hash links going dead with nothing surfaced. This fake `win`
  // makes replaceState throw to prove the guard is still cleared afterwards.
  it('keeps listening after a history write throws', () => {
    let listener
    const win = {
      location: { pathname: '/area/sofia', search: '', hash: '' },
      addEventListener: (_type, fn) => { listener = fn },
      removeEventListener: () => { listener = null },
      history: {
        pushState: () => { throw new DOMException('rate limited', 'SecurityError') },
        replaceState: () => { throw new DOMException('rate limited', 'SecurityError') },
      },
    }

    const vs = createViewState({ ...opts, win })
    expect(() => vs.setMetric('P1')).toThrow()

    // An external hash change after the throw must still be observed — proof
    // that `writing` was cleared, not left stuck true.
    win.location.hash = '#metric=temperature'
    listener()
    expect(vs.metric).toBe('temperature')
    vs.destroy()
  })
})
