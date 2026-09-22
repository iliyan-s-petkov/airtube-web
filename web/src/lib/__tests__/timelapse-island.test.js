// @vitest-environment jsdom
//
// jsdom: mountPlayer builds the player's real DOM, which these drive directly.
import { describe, it, expect, vi } from 'vitest'
import { installTimelapse } from '../timelapse-island.js'
import { refreshHexes } from '../mapdata.js'
import { hexLabelPaint, CARRIED_OPACITY, FRESH_OPACITY, SETTLING_OPACITY } from '../mappaint.js'
import { PLAY_SPEED_KEY } from '../mapconfig.js'
import { resolutionForZoom, hexesURL } from '../hexes.js'
import { mountPlayer, FRAME_MS } from '../timelapse.js'

// The animation is the hex layer with a past hour's numbers in it. These fix
// what that must NOT disturb: the live grid the map goes back to, and the
// nothing it costs a reader who never presses play.
describe('installTimelapse', () => {
  const cfg = { metric: 'P2', noDataColour: '#9ca3af', lang: 'en' }
  const BODY = {
    metric: 'P2', resolution_km: 15, cells: [[23, 42]],
    frames: [{ t: '2026-09-08T06:00:00Z', v: [10] }, { t: '2026-09-08T07:00:00Z', v: [20] }],
  }

  function harness(fetchJSON, t, storage, width) {
    const painted = []
    let zoom = 12
    const zoomHandlers = []
    const map = {
      getZoom: () => zoom,
      // Fires the same handlers a real MapLibreMap would, so a test can move
      // the map the way a reader's pinch or scroll does.
      setZoom: (z) => { zoom = z; for (const fn of zoomHandlers) fn() },
      getBounds: () => ({ getWest: () => 23, getSouth: () => 42, getEast: () => 24, getNorth: () => 43 }),
      getSource: () => ({ setData: (d) => painted.push(d) }),
      getContainer: () => (width ? { clientWidth: width } : undefined),
      on: (evt, fn) => { if (evt === 'zoom') zoomHandlers.push(fn) },
      off: (evt, fn) => {
        if (evt !== 'zoom') return
        const i = zoomHandlers.indexOf(fn)
        if (i >= 0) zoomHandlers.splice(i, 1)
      },
    }
    const ui = mountPlayer(document.createElement('div'), { label: 'Time', playLabel: 'Play', pauseLabel: 'Pause', exitLabel: 'Now', speedLabel: 'Speed' })
    const state = { scales: null, hexUrl: null, window: '24h' }
    const ctl = installTimelapse(map, state, { ...cfg, t: { ...cfg.t, ...t } }, { player: ui, storage }, fetchJSON)
    return { painted, ui, state, ctl, map }
  }

  // The two shapes production actually serves that the guard exists for,
  // measured 2026-09-18: noise_LAeq has no history at all, and NO2 over 24h
  // lands in bursts, leaving most hours blank between them.
  const NO_HISTORY = {
    metric: 'noise_LAeq', resolution_km: 15, cells: [],
    frames: [{ t: '2026-09-08T06:00:00Z', v: [] }, { t: '2026-09-08T07:00:00Z', v: [] }],
  }
  const PATCHY = {
    metric: 'NO2', resolution_km: 15, cells: [[23, 42], [23.2, 42], [23.4, 42], [23.6, 42]],
    frames: [
      { t: '2026-09-08T06:00:00Z', v: [1, 2, 3, 4] },
      { t: '2026-09-08T07:00:00Z', v: [null, null, null, null] },
      { t: '2026-09-08T08:00:00Z', v: [1, 2, 3, 4] },
    ],
  }
  // Same shape as PATCHY but on the metric this harness draws, so the cells
  // actually reach the layer.
  const GAPPY = {
    metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42], [23.4, 42], [23.6, 42]],
    frames: [
      { t: '2026-09-08T06:00:00Z', v: [1, 2, 3, 4] },
      { t: '2026-09-08T07:00:00Z', v: [null, null, null, null] },
      { t: '2026-09-08T08:00:00Z', v: [1, 2, 3, 4] },
    ],
  }
  const T = { replayThin: 'Partial data for this hour', replayNoHistory: 'Not enough history yet' }

  // Speed changes the gap between frames and nothing else: the same frames, in
  // the same order, from the same body.
  describe('playback speed', () => {
    const storageFor = (raw) => {
      const kv = new Map()
      if (raw !== undefined) kv.set(PLAY_SPEED_KEY, raw)
      return { kv, getItem: (k) => (kv.has(k) ? kv.get(k) : null), setItem: (k, v) => kv.set(k, v) }
    }

    it('opens at full speed and cycles on each press', async () => {
      const { ui } = harness(async () => BODY, T)
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      expect(ui.speed.textContent).toBe('1\u00d7')
      ui.speed.click()
      expect(ui.speed.textContent).toBe('0.5\u00d7')
      ui.speed.click()
      expect(ui.speed.textContent).toBe('0.25\u00d7')
      ui.speed.click()
      expect(ui.speed.textContent).toBe('1\u00d7')
    })

    it('remembers the speed for the next visit', async () => {
      const store = storageFor()
      const { ui } = harness(async () => BODY, T, store)
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      ui.speed.click()
      expect(store.kv.get(PLAY_SPEED_KEY)).toBe('0.5')
    })

    it('opens at the remembered speed', async () => {
      const { ui } = harness(async () => BODY, T, storageFor('0.25'))
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      expect(ui.speed.textContent).toBe('0.25\u00d7')
    })

    // The whole point of the control. At half speed the map must still be on
    // the same frame after one full-speed interval has passed.
    it('holds each frame longer at a slower speed', async () => {
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T, storageFor('0.5'))
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS)
        expect(painted.length, 'advanced a frame at half speed').toBe(after)
        await vi.advanceTimersByTimeAsync(FRAME_MS)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })

    // Pressing the speed button mid-animation must take effect now, not at the
    // next press of play — the timer is already running at the old delay.
    it('applies a speed change to a running animation', async () => {
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        ui.speed.click()
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS)
        expect(painted.length, 'still on the old fast timer').toBe(after)
        // +16: the fake clock's rAF ticks land on its own 16ms grid, not on
        // this delay's boundary, so the tick that crosses it can be up to one
        // frame later than the exact millisecond.
        await vi.advanceTimersByTimeAsync(FRAME_MS + 16)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })

    // A reader who has paused and pressed the speed button is asking what the
    // next play will look like, not for the animation to start again.
    it('does not start the animation when paused', async () => {
      const { painted, ui } = harness(async () => BODY, T)
      ui.button.click()
      await vi.waitFor(() => expect(ui.speed.hidden).toBe(false))
      ui.button.click()
      await vi.waitFor(() => expect(ui.button.getAttribute('aria-pressed')).toBe('false'))
      const after = painted.length
      ui.speed.click()
      expect(ui.button.getAttribute('aria-pressed')).toBe('false')
      expect(painted.length).toBe(after)
    })
  })

  // Pressing play is user-initiated, so replay must still run under reduced
  // motion — only the pace changes, floored at the 0.25x delay.
  describe('reduced motion', () => {
    const mockReducedMotion = (matches) => {
      globalThis.matchMedia = vi.fn((q) => ({ media: q, matches }))
    }

    it('floors the delay at 0.25x even at full speed', async () => {
      vi.useFakeTimers()
      try {
        mockReducedMotion(true)
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        // Full speed's own delay (FRAME_MS) is nowhere near the 0.25x floor
        // (4 * FRAME_MS) — three of it must still produce nothing.
        await vi.advanceTimersByTimeAsync(FRAME_MS * 3)
        expect(painted.length, 'reduced motion must floor the delay').toBe(after)
        await vi.advanceTimersByTimeAsync(FRAME_MS * 2 + 16)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
        delete globalThis.matchMedia
      }
    })

    // matchMedia is read fresh on every run(), not cached at install, so a
    // reader flipping the OS setting mid-session takes effect on the very
    // next play without a reload.
    it('re-reads the preference on every run(), not once at install', async () => {
      vi.useFakeTimers()
      try {
        mockReducedMotion(false)
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))

        mockReducedMotion(true)
        ui.speed.click()
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS * 3)
        expect(painted.length, 'now floored, mid-session').toBe(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
        delete globalThis.matchMedia
      }
    })

    // jsdom, like some old browsers, has no matchMedia at all — that must
    // read as "no preference", not throw.
    it('treats a missing matchMedia as no preference', async () => {
      expect(typeof matchMedia).toBe('undefined')
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS + 16)
        expect(painted.length).toBeGreaterThan(after)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })
  })

  describe('the rAF clock', () => {
    // The point of the accumulator: a naive "paint on every rAF tick" bug
    // would paint far more than 3 times across a span this long, since the
    // fake clock's rAF fires roughly every 16ms.
    it('advances one frame per elapsed delay, not once per rAF tick', async () => {
      vi.useFakeTimers()
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS * 3 + 16)
        expect(painted.length - after).toBe(3)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
      }
    })

    it('pauses while the tab is hidden, and resumes without a catch-up burst', async () => {
      vi.useFakeTimers()
      const setHidden = (v) => Object.defineProperty(document, 'hidden', { configurable: true, value: v })
      try {
        const { painted, ui } = harness(async () => BODY, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))

        setHidden(true)
        document.dispatchEvent(new Event('visibilitychange'))
        const hiddenSince = painted.length
        // Ten frame-delays of background time: a clock that keeps ticking
        // while hidden would paint several frames across this span.
        await vi.advanceTimersByTimeAsync(FRAME_MS * 10)
        expect(painted.length, 'no paint while hidden').toBe(hiddenSince)

        setHidden(false)
        document.dispatchEvent(new Event('visibilitychange'))
        // Immediately on resume — the elapsed background time must not be
        // replayed as a burst of frames.
        await vi.advanceTimersByTimeAsync(16)
        expect(painted.length, 'no catch-up burst on resume').toBe(hiddenSince)

        await vi.advanceTimersByTimeAsync(FRAME_MS + 16)
        expect(painted.length).toBeGreaterThan(hiddenSince)
        ui.exit.click()
      } finally {
        vi.useRealTimers()
        setHidden(false)
      }
    })

    it('is fully torn down on exit: no further paints, and the zoom listener is gone', async () => {
      vi.useFakeTimers()
      try {
        const asked = []
        const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))

        ui.exit.click()
        await vi.advanceTimersByTimeAsync(0)
        const after = painted.length
        await vi.advanceTimersByTimeAsync(FRAME_MS * 5)
        expect(painted.length, 'no further paints after exit').toBe(after)

        const askedBefore = asked.length
        map.setZoom(2)
        await vi.advanceTimersByTimeAsync(0)
        expect(asked.length, 'zoom listener removed on exit').toBe(askedBefore)
      } finally {
        vi.useRealTimers()
      }
    })
  })

  // A single rAF tick can carry an arbitrary delta — a long GC pause, a bfcache
  // restore — and the fake clock cannot produce one, since its rAF fires on its
  // own 16ms grid. Driving the callback by hand is the only way to hand the
  // clock one enormous tick.
  function manualClock(start = 100000) {
    const realRAF = globalThis.requestAnimationFrame
    const realCAF = globalThis.cancelAnimationFrame
    const nowSpy = vi.spyOn(performance, 'now').mockReturnValue(start)
    let at = start
    let cb = null
    globalThis.requestAnimationFrame = (fn) => { cb = fn; return 1 }
    globalThis.cancelAnimationFrame = () => { cb = null }
    return {
      tick(delta) {
        at += delta
        const fn = cb
        cb = null
        fn?.(at)
      },
      restore() {
        globalThis.requestAnimationFrame = realRAF
        globalThis.cancelAnimationFrame = realCAF
        nowSpy.mockRestore()
      },
    }
  }

  describe('the catch-up clamp', () => {
    const LONG = {
      metric: 'P2', resolution_km: 15, cells: [[23, 42]],
      frames: Array.from({ length: 40 }, (_, i) => ({
        t: new Date(Date.UTC(2026, 8, 8, 6 + i)).toISOString(), v: [i + 1],
      })),
    }

    const playing = async () => {
      const h = harness(async () => LONG, T)
      h.ui.button.click()
      await vi.waitFor(() => expect(h.painted.length).toBe(1))
      return h
    }

    // The playhead, not the paint count: one tick advances as many frames as
    // elapsed but paints only the frame that composites, so counting setData
    // would no longer measure the clamp.
    const at = (ui) => Number(ui.slider.value)

    it('replays at most four frames after a long stall', async () => {
      const clock = manualClock()
      try {
        const { ui } = await playing()
        const after = at(ui)
        clock.tick(FRAME_MS * 30)
        expect(at(ui) - after, 'a 30-frame backlog must not replay whole').toBe(4)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })

    // The near miss: the clamp must not cost a normally-paced tick its frame,
    // and a tick landing exactly on the limit is still a tick the reader saw.
    it('leaves a normal tick and an exactly-four-frame tick alone', async () => {
      const clock = manualClock()
      try {
        const { ui } = await playing()
        let after = at(ui)
        clock.tick(FRAME_MS)
        expect(at(ui) - after, 'a normal tick advances one frame').toBe(1)
        after = at(ui)
        clock.tick(FRAME_MS * 4)
        expect(at(ui) - after, 'exactly at the limit still advances four').toBe(4)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })

    // Four synchronous paints in one animation frame are four hexFeatures
    // builds and four setData calls, of which only the last ever composites.
    it('paints once however many frames one tick swallows', async () => {
      const clock = manualClock()
      try {
        const { painted, ui } = await playing()
        const wasAt = at(ui)
        const wasPainted = painted.length
        clock.tick(FRAME_MS * 30)
        expect(at(ui) - wasAt, 'the playhead still advanced four').toBe(4)
        expect(painted.length - wasPainted, 'one setData for the one frame shown').toBe(1)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })
  })

  // The band table is held across frames rather than rebuilt per frame, and the
  // one window in which that could go stale is the one the player is installed
  // in: installTimelapse runs before initData awaits the scales, so a reader who
  // presses play on a slow link starts the run with no table at all. Caching it
  // for the life of the run would leave that country grey under a working clock
  // until they pressed exit.
  it('picks up a band table that arrives after the run started', async () => {
    const clock = manualClock()
    try {
      const { painted, ui, state } = harness(async () => BODY, T)
      ui.button.click()
      await vi.waitFor(() => expect(painted.length).toBe(1))
      expect(painted[0].features[0].properties.colour, 'no scales yet, so no colour to give it').toBe(cfg.noDataColour)
      state.scales = [{ metric: 'P2', bands: [{ upper: 5, colour: '#50f0e6' }] }]
      clock.tick(FRAME_MS)
      expect(painted.length).toBeGreaterThan(1)
      expect(painted.at(-1).features[0].properties.colour).not.toBe(cfg.noDataColour)
      ui.exit.click()
    } finally {
      clock.restore()
    }
  })

  // A digit that appears where there was none pulls the eye to the arrival
  // rather than to the value. It must ramp up instead of popping.
  describe('late joiners', () => {
    // A always reports, B joins at frame 1, C goes silent at frame 1 and so is
    // held (carried) there before reporting again.
    const JOINERS = {
      metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42], [23.4, 42]],
      frames: [
        { t: '2026-09-08T06:00:00Z', v: [10, null, 30] },
        { t: '2026-09-08T07:00:00Z', v: [10, 20, null] },
        { t: '2026-09-08T08:00:00Z', v: [10, 20, 30] },
        { t: '2026-09-08T09:00:00Z', v: [10, 20, 30] },
      ],
    }
    const A = 10
    const B = 20
    const C = 30

    const cell = (frame, value) => frame.features.find((f) => f.properties.value === value)?.properties

    // Evaluates the layer's own 'case' expression: these tests are about what a
    // cell is DRAWN at, not only about what it is tagged with.
    const opacityOf = (expr, props) => {
      for (let i = 1; i < expr.length - 1; i += 2) {
        const [, [, key], want] = expr[i]
        if ((props[key] ?? null) === want) return expr[i + 1]
      }
      return expr[expr.length - 1]
    }

    // delay is the tick size: reduced motion floors the frame delay at 0.25x,
    // so a FRAME_MS tick would advance nothing there.
    const play = async (frames, delay = FRAME_MS) => {
      const clock = manualClock()
      const { painted, ui } = harness(async () => JOINERS, T)
      ui.button.click()
      await vi.waitFor(() => expect(painted.length).toBe(1))
      for (let i = 1; i < frames; i += 1) clock.tick(delay)
      // Snapshot before exit: leaving the player repaints the live grid, and
      // that paint is not one of the replay's frames.
      const replayed = painted.slice()
      ui.exit.click()
      clock.restore()
      return replayed
    }

    it('ramps a newly arrived cell up over two frames, then settles it', async () => {
      const painted = await play(4)
      expect(cell(painted[1], B).fresh, 'the frame B arrives on').toBe(0)
      expect(cell(painted[2], B).fresh, 'one frame later, no longer freshly arrived').toBe(1)
      expect(cell(painted[3], B).fresh, 'settled').toBeUndefined()
    })

    // A catch-up tick advances several frames and paints one. The arrival
    // tracking has to describe that painted frame: a cell whose first reading
    // fell in the swallowed span is new to the reader, who never saw the frames
    // it arrived on.
    it('fades a cell in that first appeared inside a skipped span', async () => {
      const LATE = {
        metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42]],
        frames: [
          { t: '2026-09-08T06:00:00Z', v: [10, null] },
          { t: '2026-09-08T07:00:00Z', v: [10, 20] },
          { t: '2026-09-08T08:00:00Z', v: [10, 20] },
          { t: '2026-09-08T09:00:00Z', v: [10, 20] },
        ],
      }
      const clock = manualClock()
      try {
        const { painted, ui } = harness(async () => LATE, T)
        ui.button.click()
        await vi.waitFor(() => expect(painted.length).toBe(1))
        // One tick worth three frames: frames 1 and 2 are never composited.
        clock.tick(FRAME_MS * 3)
        expect(Number(ui.slider.value), 'the playhead is on frame 3').toBe(3)
        expect(cell(painted.at(-1), B).fresh, 'new to the reader on the frame shown').toBe(0)
        ui.exit.click()
      } finally {
        clock.restore()
      }
    })

    it('never marks a cell that reported in both frames', async () => {
      const painted = await play(4)
      for (const frame of painted) expect(cell(frame, A).fresh).toBeUndefined()
    })

    // The opening frame is the start of the story, not an arrival: fading the
    // whole map in on every press of play is the pop this task is about, moved.
    it('treats the opening frame as settled, not as a mass arrival', async () => {
      const painted = await play(1)
      expect(cell(painted[0], A).fresh).toBeUndefined()
      expect(cell(painted[0], C).fresh).toBeUndefined()
    })

    it('keeps a carried cell muted and never treats it as fresh', async () => {
      const painted = await play(3)
      const held = cell(painted[1], C)
      expect(held.carried, 'C is held on frame 1').toBe(true)
      expect(held.fresh).toBeUndefined()
      expect(opacityOf(hexLabelPaint({})['text-opacity'], held)).toBe(CARRIED_OPACITY)
      expect(cell(painted[2], C).fresh, 'a held cell reporting again is not an arrival').toBeUndefined()
    })

    // Scrubbing back to before a cell's first reading and forward past its gap
    // is the one way a held cell can meet a previous frame that never drew it.
    // Without the guard it would be tagged as an arrival and drawn brighter
    // than the held reading it is.
    it('keeps a held cell held when the reader scrubs back past its first hour', async () => {
      const LATE = {
        metric: 'P2', resolution_km: 15, cells: [[23, 42], [23.2, 42]],
        frames: [
          { t: '2026-09-08T06:00:00Z', v: [10, null] },
          { t: '2026-09-08T07:00:00Z', v: [10, 7] },
          { t: '2026-09-08T08:00:00Z', v: [10, null] },
        ],
      }
      const { painted, ui } = harness(async () => LATE, T)
      ui.button.click()
      await vi.waitFor(() => expect(painted.length).toBe(1))
      const scrub = (i) => {
        ui.slider.value = String(i)
        ui.slider.dispatchEvent(new Event('input'))
      }
      scrub(0)
      scrub(2)
      const held = cell(painted.at(-1), 7)
      expect(held.carried, 'the late cell is held on the last hour').toBe(true)
      expect(held.fresh).toBeUndefined()
      expect(opacityOf(hexLabelPaint({})['text-opacity'], held)).toBe(CARRIED_OPACITY)
    })

    // Belt and braces with the guard above: even handed a feature tagged both
    // ways, the expression must draw it as held rather than as an arrival.
    it('draws a cell tagged both ways as held', () => {
      expect(opacityOf(hexLabelPaint({})['text-opacity'], { carried: true, fresh: 0 })).toBe(CARRIED_OPACITY)
    })

    it('ramps through the opacities the layer draws', async () => {
      const painted = await play(3)
      const expr = hexLabelPaint({})['text-opacity']
      expect(opacityOf(expr, cell(painted[1], B))).toBe(FRESH_OPACITY)
      expect(opacityOf(expr, cell(painted[2], B))).toBe(SETTLING_OPACITY)
      expect(opacityOf(expr, cell(painted[1], A))).toBe(1)
    })

    // Reduced motion gets the end state at once — a slower ramp is still a
    // ramp, and app.css suppresses transitions outright under the same query.
    it('draws an arrival at full opacity under reduced motion', async () => {
      globalThis.matchMedia = vi.fn((q) => ({ media: q, matches: true }))
      try {
        const painted = await play(3, FRAME_MS * 4)
        for (const frame of painted) {
          for (const f of frame.features) expect(f.properties.fresh).toBeUndefined()
        }
        expect(opacityOf(hexLabelPaint({})['text-opacity'], cell(painted[1], B))).toBe(1)
      } finally {
        delete globalThis.matchMedia
      }
    })
  })

  // Twenty-eight blank frames under a running clock read as clean air, not as
  // missing data. Saying so is the whole point of the guard.
  it('refuses to animate a metric with no history, and says why', async () => {
    const { painted, ui } = harness(async () => NO_HISTORY, T)

    ui.button.click()
    await vi.waitFor(() => expect(ui.note.textContent).toBe('Not enough history yet'))
    expect(painted).toEqual([])
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
  })

  // Not skipped and not frozen: the gap is the story, so the frame draws and
  // the caption says the hour is thin rather than letting it read as clean air.
  it('captions a thin frame and clears the caption on a full one', async () => {
    vi.useFakeTimers()
    try {
      const { ui } = harness(async () => PATCHY, T)
      ui.button.click()
      await vi.waitFor(() => expect(ui.clock.textContent).not.toBe(''))
      expect(ui.note.textContent).toBe('')

      await vi.advanceTimersByTimeAsync(FRAME_MS)
      expect(ui.note.textContent).toBe('Partial data for this hour')

      await vi.advanceTimersByTimeAsync(FRAME_MS)
      expect(ui.note.textContent).toBe('')
      ui.exit.click()
    } finally {
      vi.useRealTimers()
    }
  })

  // The silent hour is drawn at the previous hour's readings rather than
  // dropping its digits, which is what made the replay look like numbers
  // blinking on and off at random.
  it('holds a silent cell at its last reading, marked as carried', async () => {
    vi.useFakeTimers()
    try {
      const { painted, ui } = harness(async () => GAPPY, T)
      ui.button.click()
      await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
      await vi.advanceTimersByTimeAsync(FRAME_MS)

      const drawn = painted.map((p) => p.features).filter((fs) => fs.length > 0)
      const gap = drawn.find((fs) => fs.every((f) => f.properties.carried === true))
      expect(gap, 'a frame drawn entirely from carried readings').toBeDefined()
      expect(gap.map((f) => f.properties.value).sort()).toEqual([1, 2, 3, 4])
      ui.exit.click()
    } finally {
      vi.useRealTimers()
    }
  })

  it('clears the caption on the way out', async () => {
    const { ui } = harness(async () => PATCHY, T)
    ui.button.click()
    await vi.waitFor(() => expect(ui.clock.textContent).not.toBe(''))
    ui.onscrub.length
    ui.slider.value = '1'
    ui.slider.dispatchEvent(new Event('input'))
    expect(ui.note.textContent).toBe('Partial data for this hour')

    ui.exit.click()
    await vi.waitFor(() => expect(ui.note.textContent).toBe(''))
  })

  // A visitor who never presses play must not pay for the history.
  it('fetches nothing until the button is pressed', async () => {
    const asked = []
    const { ui } = harness(async (url) => { asked.push(url); return BODY })
    expect(asked).toEqual([])

    ui.button.click()
    await vi.waitFor(() => expect(asked).toHaveLength(1))
    expect(asked[0]).toContain('/api/v1/timelapse')
    ui.button.click()
  })

  it('paints the frame it is on, and says which hour that is', async () => {
    const { painted, ui } = harness(async () => BODY)

    ui.button.click()
    await vi.waitFor(() => expect(painted).toHaveLength(1))
    expect(painted[0].features).toHaveLength(1)
    expect(painted[0].features[0].properties.value).toBe(10)
    expect(ui.clock.textContent).not.toBe('')
    ui.button.click()
  })

  // The frame on screen is a past hour's. Pressing stop must put the live grid
  // back, which only happens if the dedup key refreshHexes holds is cleared.
  it('goes back to the live grid when stopped', async () => {
    const LIVE = { resolution_km: 15, hexes: [{ lon: 23, lat: 42, values: { P2: 99 } }] }
    const fetchJSON = async (url) => (url.includes('timelapse') ? BODY : LIVE)
    const { ui, painted, state, map } = harness(fetchJSON)

    // The live grid FIRST, so refreshHexes is holding a dedup key by the time
    // the animation runs. Without that key being cleared on stop, the second
    // call sees the URL it already fetched and repaints nothing — leaving a
    // past hour on screen under a map that says it is showing now.
    await refreshHexes(map, state, cfg, fetchJSON)
    ui.button.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(10))

    ui.button.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(99))
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
  })

  // Dragging is a request to look at one hour; leaving the timer running would
  // move the map off it a third of a second later.
  it('stops playing when the reader scrubs', async () => {
    const { ui, painted } = harness(async () => BODY)

    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    ui.slider.value = '1'
    ui.slider.dispatchEvent(new Event('input'))

    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
    expect(painted.at(-1).features[0].properties.value).toBe(20)
  })

  // Scrubbing pauses without restoring, so a reader who drags the slider and
  // stops is left on a past hour. The exit is the only way back to now from
  // there: pressing play again would replay, not return.
  it('goes back to the live grid from a scrubbed frame', async () => {
    const LIVE = { resolution_km: 15, hexes: [{ lon: 23, lat: 42, values: { P2: 99 } }] }
    const fetchJSON = async (url) => (url.includes('timelapse') ? BODY : LIVE)
    const { ui, painted, state, map } = harness(fetchJSON)

    await refreshHexes(map, state, cfg, fetchJSON)
    ui.button.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(10))

    ui.slider.value = '1'
    ui.slider.dispatchEvent(new Event('input'))
    expect(painted.at(-1).features[0].properties.value).toBe(20)

    ui.exit.click()
    await vi.waitFor(() => expect(painted.at(-1).features[0].properties.value).toBe(99))
  })

  // Leaving the animation collapses the control back to the play button: a
  // scrubber left on screen over a live map is a control with nothing behind it.
  // The held body survives, so coming back costs no second fetch.
  it('collapses the scrubber on exit, and reopens it without a refetch', async () => {
    const asked = []
    const LIVE = { resolution_km: 15, hexes: [] }
    const { ui } = harness(async (url) => {
      asked.push(url)
      return url.includes('timelapse') ? BODY : LIVE
    })

    ui.button.click()
    await vi.waitFor(() => expect(ui.slider.hidden).toBe(false))
    expect(ui.exit.hidden).toBe(false)

    ui.exit.click()
    await vi.waitFor(() => expect(ui.slider.hidden).toBe(true))
    expect(ui.exit.hidden).toBe(true)
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')

    ui.button.click()
    await vi.waitFor(() => expect(ui.slider.hidden).toBe(false))
    expect(asked.filter((u) => u.includes('timelapse'))).toHaveLength(1)
    ui.button.click()
  })

  // A different window is a different animation: keeping the old body would
  // replay the day while the map claimed to be showing the week.
  it('drops what it holds on a reset, and refetches after', async () => {
    const asked = []
    const { ui, ctl } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    await vi.waitFor(() => expect(ui.button.getAttribute('aria-pressed')).toBe('true'))
    await ctl.reset()
    expect(ui.slider.hidden).toBe(true)

    ui.button.click()
    await vi.waitFor(() => expect(asked.filter((u) => u.includes('timelapse'))).toHaveLength(2))
    ui.button.click()
  })

  // The live grid asks for the tier its zoom draws at; the replay must ask for
  // the same one, or the hexes it swaps in are a different size from the ones
  // still on screen a moment before.
  it('asks for the tier matching the current zoom', async () => {
    const asked = []
    const { ui } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    await vi.waitFor(() => expect(asked).toHaveLength(1))
    const url = new URL(asked[0], 'http://x')
    expect(Number(url.searchParams.get('resolution_km'))).toBeCloseTo(resolutionForZoom(12), 4)
    ui.button.click()
  })

  // The live grid halves its target on a phone, so a replay that kept the
  // desktop tier would swap in cells twice the size of the ones on screen.
  it('asks for the same tier the live grid asks for at a phone width', async () => {
    const asked = []
    const { ui } = harness(async (url) => { asked.push(url); return BODY }, undefined, undefined, 390)

    ui.button.click()
    await vi.waitFor(() => expect(asked).toHaveLength(1))
    const replay = new URL(asked[0], 'http://x').searchParams.get('resolution_km')
    const live = new URL(hexesURL(12, null, 390), 'http://x').searchParams.get('resolution_km')
    expect(Number(replay)).toBeCloseTo(Number(live), 4)
    expect(Number(replay)).toBeCloseTo(resolutionForZoom(12, 390), 4)
    expect(Number(replay)).not.toBeCloseTo(resolutionForZoom(12), 4)
    ui.button.click()
  })

  // A zoom-to-street flight fires many zoom events on the way; only the tier
  // it lands on is worth a request.
  it('follows the reader onto the tier for the new zoom', async () => {
    const asked = []
    const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    // Waits on the paint, not the fetch: the paint happens after the player
    // is marked open, and it is openness the zoom handler below needs.
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    const before = painted.length

    map.setZoom(2)
    await vi.waitFor(() => expect(asked).toHaveLength(2))
    const url = new URL(asked[1], 'http://x')
    expect(Number(url.searchParams.get('resolution_km'))).toBeCloseTo(resolutionForZoom(2), 4)
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(before))
    ui.button.click()
  })

  // Rounded the same way hexesURL rounds it: a fraction of a zoom level is not
  // a new tier, and refetching for it would turn a smooth zoom into a burst.
  it('does not refetch or repaint when zooming within the same tier', async () => {
    const asked = []
    const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    // On the paint, not the fetch: the fetch resolves before the player is
    // marked open, and a zoom while it is still closed proves nothing.
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    const before = painted.length

    map.setZoom(12.4)
    await Promise.resolve()
    await Promise.resolve()
    expect(asked).toHaveLength(1)
    // Still playing here, so this is what pins the same-tier dedup itself
    // rather than the paused guard: FRAME_MS is far longer than two microtasks.
    expect(painted).toHaveLength(before)
    ui.button.click()
  })

  // Nor repaint. A flyTo fires a zoom event per frame; feeding the source the
  // body it already holds sixty times a second is the jank the dedup prevents.
  it('does not repaint when zooming within the same tier', async () => {
    const { ui, painted, map } = harness(async () => BODY)

    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    // Pause restores the live grid, which is itself a paint — settle it first,
    // or the repaint being measured is that one.
    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(1))
    const before = painted.length

    map.setZoom(12.4)
    await Promise.resolve()
    await Promise.resolve()
    expect(painted).toHaveLength(before)
  })

  // A reader watching the animation should not be thrown back to the first
  // hour because the tier under them changed.
  // Fake timers, because the assertion is about a specific frame: with the real
  // interval the playhead would have stepped on before the zoom was measured.
  it('keeps the playhead where it was when the tier changes', async () => {
    vi.useFakeTimers()
    try {
      const { ui, painted, map } = harness(async () => BODY)

      ui.button.click()
      await vi.advanceTimersByTimeAsync(FRAME_MS)
      expect(ui.slider.value).toBe('1')
      const before = painted.length

      map.setZoom(2)
      await vi.advanceTimersByTimeAsync(0)
      expect(painted.length).toBeGreaterThan(before)
      expect(ui.slider.value).toBe('1')
      ui.button.click()
    } finally {
      vi.useRealTimers()
    }
  })

  // Pause puts the live grid back on screen. A zoom is not a press of play, so
  // it must not drag a replay frame back over it — the new tier is fetched and
  // held, and the next press of play or drag of the scrubber draws it.
  it('does not redraw a frame over the live grid while paused', async () => {
    const asked = []
    const { ui, painted, map } = harness(async (url) => { asked.push(url); return BODY })

    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(0))
    ui.button.click()
    await vi.waitFor(() => expect(painted.length).toBeGreaterThan(1))
    const before = painted.length

    map.setZoom(2)
    await vi.waitFor(() => expect(asked.filter((u) => u.includes('timelapse'))).toHaveLength(2))
    await Promise.resolve()
    expect(painted).toHaveLength(before)
  })

  // A zoom event before the button has ever been pressed must not fetch —
  // that is the live grid's job, not the replay's.
  it('ignores a zoom before the player has ever been opened', async () => {
    const asked = []
    const { map } = harness(async (url) => { asked.push(url); return BODY })

    map.setZoom(2)
    await Promise.resolve()
    expect(asked).toEqual([])
  })
})
