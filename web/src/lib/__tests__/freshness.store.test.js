import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createFreshness, getFreshness, resetFreshnessForTests } from '../freshness.svelte.js'
import { AUTO_KEY, AUTO_INTERVAL_MS } from '../freshness.js'

// A fake window whose interval never actually fires: the tests drive it by
// hand, so a five-minute cadence costs nothing and nothing leaks between them.
function fakeWin() {
  const timers = new Map()
  let next = 1
  return {
    timers,
    setInterval(fn, ms) { timers.set(next, { fn, ms }); return next++ },
    clearInterval(id) { timers.delete(id) },
    fire(id) { timers.get(id).fn() },
  }
}

function store(initial = {}) {
  const map = new Map(Object.entries(initial))
  return {
    map,
    getItem(k) { return map.has(k) ? map.get(k) : null },
    setItem(k, v) { map.set(k, v) },
  }
}

let errs
beforeEach(() => { errs = vi.spyOn(console, 'error').mockImplementation(() => {}) })
afterEach(() => { errs.mockRestore(); resetFreshnessForTests() })

describe('createFreshness', () => {
  it('starts with a time, because the server-rendered page was fresh', () => {
    const f = createFreshness({ win: fakeWin(), storage: store(), now: () => 1000 })
    expect(f.at).toBe(1000)
    expect(f.busy).toBe(false)
    expect(f.failed).toBe(false)
  })

  // A map going quietly stale while someone watches it is the one failure this
  // page cannot report — the numbers still look like numbers.
  it('auto-refreshes unless the reader has turned it off', () => {
    expect(createFreshness({ win: fakeWin(), storage: store() }).auto).toBe(true)
    expect(createFreshness({ win: fakeWin(), storage: store({ [AUTO_KEY]: 'false' }) }).auto).toBe(false)
    expect(createFreshness({ win: fakeWin(), storage: store({ [AUTO_KEY]: 'true' }) }).auto).toBe(true)
  })

  it('runs every registered provider and stamps the time on success', async () => {
    const calls = []
    let clock = 100
    const f = createFreshness({ win: fakeWin(), storage: store(), now: () => clock })
    f.provide(async () => { calls.push('a') })
    f.provide(async () => { calls.push('b') })
    clock = 200
    await f.request()
    expect(calls.sort()).toEqual(['a', 'b'])
    expect(f.at).toBe(200)
    expect(f.failed).toBe(false)
  })

  // The status line is the only place a reader can find out that the numbers
  // they are looking at did not move, so a failure must not leave a fresh
  // timestamp behind it.
  it('keeps the old time and reports a failure when a provider throws', async () => {
    let clock = 100
    const f = createFreshness({ win: fakeWin(), storage: store(), now: () => clock })
    f.provide(async () => { throw new Error('offline') })
    clock = 200
    await f.request()
    expect(f.at).toBe(100)
    expect(f.failed).toBe(true)
    expect(f.busy).toBe(false)
    expect(errs).toHaveBeenCalled()
  })

  it('clears a previous failure once a request succeeds', async () => {
    const f = createFreshness({ win: fakeWin(), storage: store() })
    let ok = false
    f.provide(async () => { if (!ok) throw new Error('offline') })
    await f.request()
    expect(f.failed).toBe(true)
    ok = true
    await f.request()
    expect(f.failed).toBe(false)
  })

  // The button is deliberately not disabled while in flight (a disabled button
  // drops keyboard focus), so the guard has to live here instead.
  it('ignores a second request while one is running', async () => {
    let running
    let started = 0
    const f = createFreshness({ win: fakeWin(), storage: store() })
    f.provide(() => { started += 1; return new Promise((resolve) => { running = resolve }) })
    const first = f.request()
    expect(f.busy).toBe(true)
    await f.request()
    expect(started).toBe(1)
    running()
    await first
    expect(f.busy).toBe(false)
  })

  it('unregisters a provider through the handle provide returns', async () => {
    let called = 0
    const f = createFreshness({ win: fakeWin(), storage: store() })
    const off = f.provide(async () => { called += 1 })
    await f.request()
    off()
    await f.request()
    expect(called).toBe(1)
  })

  it('works with no provider at all', async () => {
    const f = createFreshness({ win: fakeWin(), storage: store(), now: () => 7 })
    await expect(f.request()).resolves.toBeUndefined()
    expect(f.at).toBe(7)
  })
})

describe('the auto-refresh timer', () => {
  it('is scheduled at the shared interval when auto is on', () => {
    const win = fakeWin()
    createFreshness({ win, storage: store() })
    expect([...win.timers.values()].map((t) => t.ms)).toEqual([AUTO_INTERVAL_MS])
  })

  it('is not scheduled when the reader has turned auto off', () => {
    const win = fakeWin()
    createFreshness({ win, storage: store({ [AUTO_KEY]: 'false' }) })
    expect(win.timers.size).toBe(0)
  })

  it('fires a real request', async () => {
    const win = fakeWin()
    let called = 0
    const f = createFreshness({ win, storage: store() })
    f.provide(async () => { called += 1 })
    win.fire([...win.timers.keys()][0])
    await Promise.resolve()
    await Promise.resolve()
    expect(called).toBe(1)
  })

  it('is cancelled and rebuilt as the reader toggles auto, never doubled', () => {
    const win = fakeWin()
    const f = createFreshness({ win, storage: store() })
    f.setAuto(false)
    expect(win.timers.size).toBe(0)
    f.setAuto(true)
    expect(win.timers.size).toBe(1)
    // Setting it to the value it already has must not stack a second timer.
    f.setAuto(true)
    expect(win.timers.size).toBe(1)
  })

  it('remembers the choice for the next visit', () => {
    const s = store()
    const f = createFreshness({ win: fakeWin(), storage: s })
    f.setAuto(false)
    expect(s.map.get(AUTO_KEY)).toBe('false')
    f.setAuto(true)
    expect(s.map.get(AUTO_KEY)).toBe('true')
  })

  it('is torn down by destroy', () => {
    const win = fakeWin()
    const f = createFreshness({ win, storage: store() })
    f.destroy()
    expect(win.timers.size).toBe(0)
  })
})

describe('getFreshness', () => {
  // Three islands reporting three different last-refresh times is worse than
  // none: the button, the line and the map must all be talking about the same
  // request.
  it('hands every caller the same store', () => {
    const win = fakeWin()
    expect(getFreshness({ win, storage: store() })).toBe(getFreshness())
  })

  it('is rebuilt after the test-only reset', () => {
    const win = fakeWin()
    const first = getFreshness({ win, storage: store() })
    resetFreshnessForTests()
    expect(getFreshness({ win, storage: store() })).not.toBe(first)
  })
})
