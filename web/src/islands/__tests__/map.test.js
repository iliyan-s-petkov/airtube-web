// @vitest-environment jsdom
//
// jsdom, not the default node environment: the repaint test below drives
// mount() through a real container element and a real `location.hash` /
// `hashchange`, which the rest of this file's pure-logic tests do not need
// but do not mind either — jsdom is a superset, not a different behaviour,
// for code that touches no DOM.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { applyMarkerZoomRange, refreshHexes, installTimelapse, readConfig, NOT_OFFICIAL, mount, mountChrome, HEX_LABEL_LAYER_ID, hexLabelPaint, CARRIED_OPACITY, FRESH_OPACITY, SETTLING_OPACITY, PLAY_SPEED_KEY, LEGEND_FOLD_KEY, DEEP_LINK_ZOOM } from '../map.js'
import { ARROW_IMAGE_ID, WIND_LAYER_ID, WIND_SOURCE_ID } from '../wind.js'
import { GRID_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM, resolutionForZoom } from '../../lib/hexes.js'
import { clearCache } from '../../lib/api.js'
import { mountPlayer, FRAME_MS } from '../../lib/timelapse.js'
import { resetViewStateForTests, getViewState } from '../../lib/viewstate.svelte.js'
import { findSensor, setSensors } from '../../lib/sensors.svelte.js'
import { setSensorStatus, getSensorStatus, resetSensorFilterForTests } from '../../lib/sensorfilter.svelte.js'
import { setSourceEnabled, resetSourceFilterForTests } from '../../lib/sourcefilter.svelte.js'

// mount() constructs a REAL MapLibreMap, which needs a working WebGL canvas —
// out of reach under jsdom (see the "no jsdom" rule respected everywhere else
// in this file). Mocked here, ONLY for the mountTestMap-based tests below, so
// mount() can be driven end to end (readConfig -> addLayer -> the metric
// subscription) without a real renderer. Every other describe block in this
// file drives map.js's exported functions directly with plain objects, never
// through mount(), so this mock never applies to them in practice — but
// vi.mock is file-scoped, so it is declared once, here.
// The layers the mounted style reports. Hoisted so the vi.mock factory — which
// runs before this module's own body — can close over it, and mutable so a
// test can mount a map whose style already has layers to sit under.
const fakeStyle = vi.hoisted(() => ({ layers: [] }))

vi.mock('maplibre-gl', () => {
  class FakeMap {
    constructor(options) {
      this.options = options
      this.handlers = {}
      this.setPaintProperty = vi.fn()
      this.setFilter = vi.fn()
      this.addSource = vi.fn()
      this.addLayer = vi.fn()
      // Backed by addLayer's own call log, not a separate list: a real map
      // knows about a layer once addLayer has been called for it, which is
      // exactly what map.js's beforeId guard (map.getLayer?.(id)) checks.
      this.getLayer = vi.fn((id) => (this.addLayer.mock.calls.some((c) => c[0]?.id === id) ? { id } : undefined))
      this.addImage = vi.fn()
      this.setLayoutProperty = vi.fn()
      this.setLayerZoomRange = vi.fn()
      this.getZoom = vi.fn(() => 7)
      // The zoom stack asks the camera for its own limits rather than
      // restating them (see installZoom), so a map that cannot answer is not
      // a map this island can mount.
      this.getMinZoom = vi.fn(() => 5)
      this.getMaxZoom = vi.fn(() => 18)
      this.zoomIn = vi.fn()
      this.zoomOut = vi.fn()
      this.flyTo = vi.fn()
      // One object per source id, every setData recorded in order.
      this.painted = []
      this.sources = {}
      this.getSource = vi.fn((id) => {
        this.sources[id] ??= { setData: vi.fn(() => this.painted.push(id)) }
        return this.sources[id]
      })
      this.container = document.createElement('div')
      this.getContainer = vi.fn(() => this.container)
      // The layers menu reads its options off the mounted style, so a map that
      // cannot report one is a map this island cannot mount. Empty here: the
      // basemap layers come from tools/basemap/style.json, which no test
      // fetches, and an empty style is the real state of a map served without
      // tiles — the one the menu has to survive.
      this.getStyle = vi.fn(() => ({ layers: fakeStyle.layers }))
      // Spied so the locateVisitor tests below can assert a "geoip" response
      // jumps the map, and that a "default"/rejected response does not.
      this.jumpTo = vi.fn()
      this.clickHandlers = {}
    }
    // map.on('click', LAYER_ID, cb) carries the layer id as a second
    // argument; every other event map.js registers is map.on(event, cb).
    // Clicks are ALSO kept per layer: the map binds one click handler to the
    // markers and another to the cells, and a single `handlers.click` slot
    // would silently hand every test the last one registered.
    on(event, a, b) {
      if (event !== 'click') { this.handlers[event] = a; return }
      this.handlers.click ??= b
      this.clickHandlers[a] = b
    }

    off(event, handler) {
      if (this.handlers[event] === handler) delete this.handlers[event]
    }
  }
  return { Map: FakeMap, addProtocol: vi.fn() }
})

// The minimum harness this task needs: mount a map island against a fresh
// container and fresh viewstate singleton, fire the 'load' handler mount()
// registers (which is where the metric-follow subscription is wired — see
// map.js), and hand the test the fake map plus the chrome object mount()
// returns. No harness by this name or shape existed before this task; the
// brief assumed one without it being written, so this is built fresh, kept to
// exactly what the two tests below need.
function mountTestMap({ metric, styleLayers = [], dataset = {} }) {
  fakeStyle.layers = styleLayers
  resetViewStateForTests()
  history.replaceState(null, '', `/#metric=${metric}`)

  const el = document.createElement('div')
  el.dataset.metric = metric
  el.dataset.metrics = 'P1,P2,temperature'
  el.dataset.zoom = '7'
  el.dataset.lon = '25.4858'
  el.dataset.lat = '42.7339'
  el.dataset.noDataColour = '#9ca3af'
  el.dataset.unscaledColour = '#94a3b8'
  el.dataset.markerStrokeColour = '#ffffff'
  el.dataset.markerLabelColour = '#161616'
  el.dataset.emptyBasemapColour = '#eef2f5'
  el.dataset.zoomCity = '9'
  el.dataset.zoomSensor = '11'
  el.dataset.hexOpacity = '0.55'
  el.dataset.tWindToggle = 'Wind'
  el.dataset.tViewCellValues = 'Cell values'
  Object.assign(el.dataset, dataset)
  // In the document, in a wrapper of its own: mountChrome puts the tier caption
  // AFTER the map's element, which a detached node has nowhere to put.
  const wrapper = document.createElement('div')
  wrapper.appendChild(el)
  document.body.appendChild(wrapper)

  const { map, chrome } = mount(el)
  // Fired, not awaited: mount()'s 'load' handler registers the metric
  // subscription SYNCHRONOUSLY, before its first `await` (see map.js's own
  // comment on why) — so by the time this call returns to mountTestMap, the
  // subscription already exists, even though the handler's own data-loading
  // tail (initData) is still pending in the microtask queue. A real
  // MapLibreMap fires 'load' itself, asynchronously, once its style is
  // ready; this harness fires it eagerly instead, since the fake map here
  // has no style to wait for.
  map.handlers.load()
  return { map, chrome, el }
}

// The no-data colour is configuration now (arrives as a data-* attribute), not
// a module constant — restated here as a literal because these tests are about
// feature-mapping logic, not about the specific grey.
const NO_DATA_COLOUR = '#9ca3af'


// The animation is the hex layer with a past hour's numbers in it. These fix
// what that must NOT disturb: the live grid the map goes back to, and the
// nothing it costs a reader who never presses play.
describe('installTimelapse', () => {
  const cfg = { metric: 'P2', noDataColour: '#9ca3af', lang: 'en' }
  const BODY = {
    metric: 'P2', resolution_km: 15, cells: [[23, 42]],
    frames: [{ t: '2026-09-08T06:00:00Z', v: [10] }, { t: '2026-09-08T07:00:00Z', v: [20] }],
  }

  function harness(fetchJSON, t, storage) {
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


// mount() end to end, through the fake MapLibreMap declared at the top of
// this file: does changing vs.metric (via a hashchange, the same seam a
// Back/Forward navigation or an external link uses) reach the layer's paint
// property, without a new map and without re-registering the layer.
//
// getViewState is a page-lifetime singleton with no reset seam of its own
// (see viewstate.svelte.js) — nothing exercised it until this task, so
// without resetViewStateForTests() in beforeEach, whichever it() runs FIRST
// in this file (or in another file sharing this Vitest worker) would decide
// every later test's starting metric/hash. resetViewStateForTests() is a
// test-only export added specifically for this hazard.
// The camera's floor has to be a floor the camera can actually stand on.
// MapLibre's default minZoom of 0 is not: Transform._constrain stops getZoom()
// somewhere above it so the world keeps covering the container, and the zoom
// stack — which compares getZoom() against getMinZoom() — then leaves the minus
// button live over a camera that has stopped moving. Shipped exactly that once.
describe('mount() gives the camera a reachable floor', () => {
  beforeEach(() => { resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  it('sets minZoom above the constrained floor', () => {
    const { map } = mountTestMap({ metric: 'P1' })
    // Low enough to put Bulgaria in its continent — the floor was 5, which
    // stopped the camera at the country's own edges.
    expect(map.options.minZoom).toBeLessThanOrEqual(3)
    // Above MapLibre's own default, which is the whole point: a default of 0
    // is a floor getZoom() never reaches.
    expect(map.options.minZoom).toBeGreaterThan(0)
    // And below the view the page opens at, or the opening view would already
    // be clamped.
    expect(map.options.minZoom).toBeLessThan(Number(map.options.zoom))
  })
})

// The hex layer's wiring, as opposed to its logic: refreshHexes is tested
// directly further down, but nothing there proves mount() ever calls it. These
// two assert the layer is actually driven — once on load, again when the
// viewport moves — which is the difference between a working grid and dead
// code.
describe('mount() drives the hex layer', () => {
  beforeEach(() => { resetViewStateForTests(); clearCache() })
  afterEach(() => { resetViewStateForTests() })

  function stubHexFetch() {
    return vi.fn(async (url) => ({
      ok: true, status: 200, headers: new Headers(),
      json: async () => (String(url).startsWith('/api/v1/hexes')
        ? { resolution_km: 15, hexes: [] }
        : { areas: [] }),
    }))
  }

  const hexCalls = (f) => f.mock.calls.filter((c) => String(c[0]).startsWith('/api/v1/hexes'))

  it('asks for the grid on first load', async () => {
    const fetchSpy = stubHexFetch()
    vi.stubGlobal('fetch', fetchSpy)

    mountTestMap({ metric: 'P2' })

    await vi.waitFor(() => expect(hexCalls(fetchSpy)).toHaveLength(1))
  })

  it('asks again, at the new resolution, once the viewport settles', async () => {
    const fetchSpy = stubHexFetch()
    vi.stubGlobal('fetch', fetchSpy)
    const { map } = mountTestMap({ metric: 'P2' })
    await vi.waitFor(() => expect(hexCalls(fetchSpy)).toHaveLength(1))

    // A real zoom change, not just another moveend: an identical view produces
    // an identical URL, which refreshHexes deliberately does not refetch.
    map.getZoom.mockReturnValue(13)
    map.handlers.moveend()

    await vi.waitFor(() => {
      const calls = hexCalls(fetchSpy)
      expect(calls).toHaveLength(2)
      expect(calls[1][0]).not.toBe(calls[0][0])
    }, { timeout: 2000 })
  })
})

describe('mount() follows the store metric', () => {
  beforeEach(() => { resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  it('repaints when the store metric changes', async () => {
    const { map } = mountTestMap({ metric: 'P2' })
    // mount() always paints once for the metric the page opened on (see the
    // comment above the explicit call in map.js), so a bare
    // "toHaveBeenCalled()" after the hashchange would pass even if the
    // store subscription itself were gutted into a no-op — that initial
    // call alone satisfies it. Wait for that first paint and record its
    // count, so the assertion below can only pass if the hashchange
    // triggers a REPAINT ON TOP OF it, which is what this test is for.
    await vi.waitFor(() => expect(map.setPaintProperty).toHaveBeenCalled())
    const callsBeforeChange = map.setPaintProperty.mock.calls.length

    location.hash = '#metric=P1'
    dispatchEvent(new HashChangeEvent('hashchange'))

    await vi.waitFor(() => {
      expect(map.setPaintProperty.mock.calls.length).toBeGreaterThan(callsBeforeChange)
    })
  })
})

// Task 9: the sensor tier's response body must reach the panel, not just the
// map layer, and a click on a sensor marker must open it — through the same
// viewstate singleton the switcher already shares, never by the map
// rendering a panel of its own (see islands/panel.js).
function mountSensorTierMap({ metric = 'P2' } = {}) {
  resetViewStateForTests()
  history.replaceState(null, '', '/')

  const el = document.createElement('div')
  // A fixed slug, above zoomSensor: on an area page tierFor picks 'sensors'
  // without needing a click first (see map.js's own comment on state.slug).
  el.dataset.slug = 'sofia'
  el.dataset.metric = metric
  el.dataset.metrics = 'P1,P2'
  el.dataset.zoom = '12'
  el.dataset.lon = '23.3'
  el.dataset.lat = '42.7'
  el.dataset.noDataColour = '#9ca3af'
  el.dataset.unscaledColour = '#94a3b8'
  el.dataset.markerStrokeColour = '#ffffff'
  el.dataset.markerLabelColour = '#161616'
  el.dataset.emptyBasemapColour = '#eef2f5'
  el.dataset.zoomCity = '9'
  el.dataset.zoomSensor = '11'
  el.dataset.tNoSources = 'No networks are shown'

  const { map, chrome, stop } = mount(el)
  // FakeMap.getZoom is hardcoded to 7 (see the vi.mock at the top of this
  // file) — every other test in this file relies on that fixed value, so it
  // is overridden here rather than in the mock itself, to reach the sensors
  // tier without disturbing them.
  map.getZoom = () => 12
  map.handlers.load()
  return { map, chrome, stop, el }
}

function stubSensorTierFetch() {
  return vi.fn(async (url) => {
    if (url === '/api/v1/scales') {
      return { ok: true, status: 200, headers: new Headers(), json: async () => [] }
    }
    return {
      ok: true, status: 200, headers: new Headers(),
      json: async () => ({ sensors: { id: [42], lon: [23.3], lat: [42.7], quality: ['ok'], P2: [12] } }),
    }
  })
}

describe('sensor tier: registry + marker click', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests(); setSensors(null) })
  afterEach(() => { resetViewStateForTests(); setSensors(null) })

  // Mutation 3 from the task brief, adapted to this design: deleting the
  // `if (effective === 'sensors') setSensors(body)` call in map.js's
  // refresh() must fail this test — findSensor would stay null forever,
  // and the panel would never resolve a marker's own click, let alone a
  // deep link that arrived before the fetch did.
  it('publishes the sensor-tier response into the registry', async () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    mountSensorTierMap()

    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    expect(findSensor(42).values.P2).toBe(12)
  })

  it('opens the panel (viewstate.sensorId) when a sensor marker is clicked', async () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())

    map.handlers.click({ features: [{ properties: { id: 42 } }] })

    expect(getViewState({ metrics: ['P2'], defaultMetric: 'P2' }).sensorId).toBe(42)
  })

  // The two branches (slug vs id) are mutually exclusive: an aggregate
  // marker click must not also open the panel.
  it('still selects an area, and does not open the panel, when an aggregate marker is clicked', async () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(map.setPaintProperty).toHaveBeenCalled())

    map.handlers.click({ features: [{ properties: { slug: 'plovdiv' } }] })

    expect(getViewState({ metrics: ['P2'], defaultMetric: 'P2' }).sensorId).toBeNull()
  })

  it('ignores a click with no recognisable feature properties', () => {
    vi.stubGlobal('fetch', stubSensorTierFetch())
    const { map } = mountSensorTierMap()

    expect(() => map.handlers.click({ features: [{ properties: {} }] })).not.toThrow()
    expect(() => map.handlers.click({ features: [] })).not.toThrow()
  })
})


// This phase's recurring defect (Tasks 9 and 10, both Important review
// findings) is code that is PRESENT but INERT: a function is written,
// unit-tested in isolation, and never actually wired to the DOM event that is
// supposed to trigger it. locateMe's own tests above call it directly; this
// test instead mounts a real map island and dispatches a real click on
// chrome.locateButton, so a mutation that drops the addEventListener call in
// mount() (or points it at the wrong element) fails here even though every
// locateMe test above still passes.
describe('mount() wires the locate button to a real click', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { clearCache(); resetViewStateForTests() })

  function stubOkFetch() {
    return vi.fn(async () => ({ ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }) }))
  }

  it('reaches locateMe, surfaced through the real hint banner', async () => {
    vi.stubGlobal('fetch', stubOkFetch())
    history.replaceState(null, '', '/#metric=P2')

    const el = document.createElement('div')
    el.dataset.metric = 'P2'
    el.dataset.metrics = 'P1,P2'
    el.dataset.zoom = '7'
    el.dataset.lon = '25.4858'
    el.dataset.lat = '42.7339'
    el.dataset.noDataColour = '#9ca3af'
    el.dataset.unscaledColour = '#94a3b8'
    el.dataset.markerStrokeColour = '#ffffff'
    el.dataset.markerLabelColour = '#161616'
    el.dataset.emptyBasemapColour = '#eef2f5'
    el.dataset.zoomCity = '9'
    el.dataset.zoomSensor = '11'
    // Distinct from every other cfg.t string in this test file, so a false
    // pass from some OTHER hint text (e.g. the tier hint) is not possible.
    el.dataset.tLocateDenied = 'LOCATE DENIED MARKER'

    const { map, chrome } = mount(el)
    map.handlers.load()
    await vi.waitFor(() => expect(map.setPaintProperty).toHaveBeenCalled())

    const originalDescriptor = Object.getOwnPropertyDescriptor(navigator, 'geolocation')
    Object.defineProperty(navigator, 'geolocation', {
      configurable: true,
      value: { getCurrentPosition: (_onSuccess, onError) => onError({ code: 1 }) },
    })

    try {
      chrome.locateButton.click()
    } finally {
      if (originalDescriptor) Object.defineProperty(navigator, 'geolocation', originalDescriptor)
      else delete navigator.geolocation
    }

    const hint = el.querySelector('.map-hint')
    expect(hint.textContent).toBe('LOCATE DENIED MARKER')
    expect(hint.hidden).toBe(false)
  })
})


// The point tier needs a layer that paints points. The fill and line layers on
// the hex source only draw Polygons, so without this one the devices arrive and
// nothing appears — the exact failure the site had while the API was already
// serving them.
describe('mount() gives the point tier a layer to paint into', () => {
  beforeEach(() => { resetViewStateForTests(); clearCache() })
  afterEach(() => { resetViewStateForTests() })

  it('adds a circle layer on the hex source', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const layers = map.addLayer.mock.calls.map((c) => c[0])
    const point = layers.find((l) => l.source === 'airbg-hexes' && l.type === 'circle')

    expect(point, 'no circle layer on the hex source').toBeTruthy()
    // Coloured by the same property the cells use, so a device and a cell at
    // the same reading are the same colour.
    expect(point.paint['circle-color']).toEqual(['get', 'colour'])
  })

  // A circle layer draws a circle at EVERY position of the geometry it is
  // handed, and a polygon's positions are its corners: once the point tier
  // started drawing cells, this layer put a dot on all six vertices of every
  // hexagon on the map. It is the fallback for a device with no size to draw
  // at, so it must see points and nothing else.
  it('paints points only, never a cell\u2019s corners', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const point = map.addLayer.mock.calls.map((c) => c[0])
      .find((l) => l.source === 'airbg-hexes' && l.type === 'circle')

    expect(point.filter).toEqual(['==', ['geometry-type'], 'Point'])
  })
})

// The reading belongs INSIDE the cell that describes it. Printed only beside a
// marker, it sat at the device's own coordinate — so at the zooms where cells
// are individually visible the number appeared off-centre in one cell, on the
// edge of the next, and nowhere at all in the rest.
describe('mount() prints the reading inside the cell', () => {
  const hexLabel = () => {
    const { map } = mountTestMap({ metric: 'P2' })
    return map.addLayer.mock.calls.map((c) => c[0])
      .find((l) => l.source === 'airbg-hexes' && l.type === 'symbol')
  }

  it('labels the cells from the source they are drawn from', () => {
    const label = hexLabel()
    expect(label, 'no symbol layer on the hex source').toBeTruthy()
    // The FRACTIONAL handover: hexesURL picks the point tier from
    // Math.round(zoom), which flips at 14.5, and MapLibre applies minzoom to
    // the true zoom. A whole 15 here left half a level where the cells were
    // already one-sensor cells with no number printed in them.
    expect(label.minzoom).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
  })

  it('centres the number rather than offsetting it past a dot', () => {
    const label = hexLabel()
    expect(label.layout['text-anchor']).toBe('center')
    expect(label.layout['text-offset']).toBeUndefined()
  })

  it('prints nothing where there is no reading, and nothing on a bare point', () => {
    const label = hexLabel()
    expect(JSON.stringify(label.filter)).toContain('value')
    expect(JSON.stringify(label.filter)).toContain('Polygon')
  })
})

// Real fullscreen renders the frame and nothing else. The key is anchored to
// the shell — deliberately, so a wide window does not float it over the page —
// which meant going full screen took the colour key off the map, on the one
// view where the map is all there is.
// "Hide the basemap" must take down the ground and leave the readings standing.
// It used to walk every layer carrying an airbg:group — a marker the vector
// style set and the raster-only style cannot: the toggle reported itself on and
// hid nothing.
// Mounted in mountChrome and not in mount(), which is what puts it on both maps
// that carry this island — the home page and an area page — from one call.
describe('the averaging selector', () => {
  it('is mounted on the frame with the server-rendered words', () => {
    const el = document.createElement('div')
    el.dataset.tWindowLabel = 'Averaging period'
    el.dataset.tWindows = 'Now,Last 24 hours,Last 48 hours,Last week'
    document.body.appendChild(el)

    const { windowMenu } = mountChrome(el, readConfig(el))
    expect(windowMenu, 'no window menu in the chrome').toBeTruthy()
    expect(windowMenu.root.parentElement).toBe(el)
    expect(windowMenu.button.getAttribute('aria-label')).toBe('Averaging period')
    const radios = [...windowMenu.panel.querySelectorAll('input[type="radio"]')]
    expect(radios.map((r) => r.value)).toEqual(['', '24h', '48h', '7d'])
    expect(radios.map((r) => r.nextElementSibling.textContent))
      .toEqual(['Now', 'Last 24 hours', 'Last 48 hours', 'Last week'])
  })
})

describe('the basemap toggle', () => {
  it('hides the ground, raster and vector detail alike, and no reading', () => {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const { layerViews } = mountChrome(el, readConfig(el))
    const basemap = layerViews.find((v) => v.id === 'basemap')
    expect(basemap, 'no basemap view in the layers menu').toBeTruthy()

    const set = []
    const map = {
      setLayoutProperty: (...a) => set.push(a),
      // The ground is the raster PLUS the vector detail over it. Every layer
      // carrying an airbg:group came from the vector style; the readings carry
      // none, and must be left standing.
      getStyle: () => ({ layers: [
        { id: 'poi-shop', metadata: { 'airbg:group': 'poi-shop' } },
        { id: 'airbg-hex-fill' },
      ] }),
    }
    basemap.apply(false, map)

    expect(set).toEqual([
      ['airbg-raster-base', 'visibility', 'none'],
      ['poi-shop', 'visibility', 'none'],
    ])
  })
})

// The disclosure is why an unmeasured forecast layer is allowed on a map of
// measurements, so it is never dismissible — but two sentences and a model name
// unrolled over the map is most of a phone screen. Folded, it is a line the
// reader can open.
describe('the wind disclosure', () => {
  // Scoped to its own frame: the disclosure is one of two <details> the chrome
  // builds, and a document-wide query would find whichever came first.
  const chrome = () => {
    const el = document.createElement('div')
    el.className = 'map'
    el.dataset.tWindAbout = 'About the wind layer'
    document.body.appendChild(el)
    return { el, ...mountChrome(el, readConfig(el)) }
  }

  it('arrives folded, with the full text inside it', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const note = c.el.querySelector('.map-wind-label')
    expect(note.tagName).toBe('DETAILS')
    expect(note.open).toBe(false)
    expect(note.hidden).toBe(false)
    expect(note.textContent).toContain('Wind forecast · valid now')
  })

  it('names itself on the summary, so a folded line still says what it is', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const summary = c.el.querySelector('.map-wind-label summary')
    expect(summary).toBeTruthy()
    expect(summary.textContent.trim()).not.toBe('')
  })

  it('goes away with the arrows, and comes back folded', () => {
    const c = chrome()
    c.showWind(true, 'Wind forecast · valid now')
    const note = c.el.querySelector('.map-wind-label')
    note.open = true
    c.showWind(false, '')
    expect(note.hidden).toBe(true)
    c.showWind(true, 'Wind forecast · valid now')
    expect(note.open).toBe(false)
  })
})

// A sensor that has stopped reporting still has a cell on the grid, drawn in
// the no-data colour. At country zoom that is most of what a reader sees on a
// bad day for the network, and it reads as "nothing here" rather than "nobody
// is measuring here". The status filter already governed the markers; the grid
// is the tier that actually covers the country, so it is governed too.
describe('the inactive-sensors toggle', () => {
  const hexCfg = { metric: 'P2', noDataColour: '#cccccc' }
  const scales = [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }, { upper: null, colour: '#ff0000' }] }]
  const mixed = {
    resolution_km: 1,
    hexes: [
      { lon: 23.32, lat: 42.65, n: 1, values: { P2: 5 } },
      { lon: 23.34, lat: 42.66, n: 0, values: {} },
    ],
  }

  function hexMap() {
    const painted = []
    return {
      painted,
      getZoom: () => 12,
      getBounds: () => ({ getWest: () => 23.3, getSouth: () => 42.6, getEast: () => 23.4, getNorth: () => 42.7 }),
      getSource: (id) => (id === 'airbg-hexes' ? { setData: (d) => painted.push(d) } : undefined),
    }
  }

  afterEach(() => resetSensorFilterForTests())

  it('leaves the silent cells off the grid by default', async () => {
    const map = hexMap()
    await refreshHexes(map, { scales, hexUrl: null, hexBody: null }, hexCfg, async () => mixed)

    const values = map.painted[0].features.map((f) => f.properties.value)
    expect(values).toEqual([5])
  })

  it('draws them once the reader asks for them', async () => {
    setSensorStatus('all')
    const map = hexMap()
    await refreshHexes(map, { scales, hexUrl: null, hexBody: null }, hexCfg, async () => mixed)

    const values = map.painted[0].features.map((f) => f.properties.value)
    expect(values).toHaveLength(2)
    expect(values).toContain(null)
  })

  it('offers the option unticked, and flips the shared status', () => {
    const el = document.createElement('div')
    document.body.appendChild(el)
    const { layerViews } = mountChrome(el, readConfig(el))
    const view = layerViews.find((v) => v.id === 'inactiveSensors')
    expect(view, 'no inactive-sensors view in the layers menu').toBeTruthy()

    // Unticked on arrival: the default is the quieter map.
    expect(view.defaultOff).toBe(true)
    view.apply(true)
    expect(getSensorStatus()).toBe('all')
    view.apply(false)
    expect(getSensorStatus()).toBe('active')
  })
})

describe('mountChrome() keeps the key on the map in fullscreen', () => {
  const chromeFrame = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)
    return { shell, el }
  }

  it('moves the key into the frame and back out again', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')
    expect(legend, 'no key on the shell').toBeTruthy()

    el.querySelector('.map__full').click()
    expect(legend.parentElement, 'the key stayed outside the fullscreen frame').toBe(el)

    el.querySelector('.map__full').click()
    expect(legend.parentElement).toBe(shell)
  })

  it('leaves it a details, so it can still be folded away', () => {
    const { el } = chromeFrame()
    mountChrome(el, readConfig(el))
    el.querySelector('.map__full').click()

    const legend = el.querySelector('details.scale')
    expect(legend.tagName).toBe('DETAILS')
    expect(legend.open, 'the key came back folded shut').toBe(true)
  })
})

// The fold and the layers-menu option are two different controls: the menu says
// whether there is a key at all, the triangle says whether it is unrolled. A
// fold that forgets is the one that reads as broken — the reader folds the key
// away, reloads, and it is back over the map.
describe('mountChrome() remembers whether the key is folded', () => {
  const chromeFrame = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)
    return { shell, el }
  }

  // This jsdom has no localStorage of its own, so the seam is stubbed rather
  // than cleared — which also proves the code reaches for the real one.
  let store
  beforeEach(() => {
    store = new Map()
    vi.stubGlobal('localStorage', {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
    })
  })
  // Left stubbed on purpose: vi.unstubAllGlobals would also drop the fetch stub
  // the later suites install, and an empty store reads exactly like the absent
  // localStorage this jsdom otherwise has.

  it('opens the key on a first visit', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    expect(shell.querySelector('details.scale').open).toBe(true)
  })

  it('records the fold when the reader closes it', () => {
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')

    legend.open = false
    legend.dispatchEvent(new Event('toggle'))

    expect(store.get(LEGEND_FOLD_KEY)).toBe('false')
  })

  it('opens folded on the next visit, and records the reopening', () => {
    store.set(LEGEND_FOLD_KEY, 'false')
    const { shell, el } = chromeFrame()
    mountChrome(el, readConfig(el))
    const legend = shell.querySelector('details.scale')
    expect(legend.open, 'the key ignored the remembered fold').toBe(false)

    legend.open = true
    legend.dispatchEvent(new Event('toggle'))
    expect(store.get(LEGEND_FOLD_KEY)).toBe('true')
  })
})

// The world raster is the style mount() opens with, not something inserted on
// 'load' afterwards: there is no longer a vector style for it to be positioned
// relative to, and inserting it late meant one frame of blank canvas.
describe('mount() opens on the world raster', () => {
  it('hands MapLibre a style carrying the raster, not a URL to fetch', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const style = map.options.style

    expect(typeof style, 'still fetching a style document').toBe('object')
    expect(style.layers.some((l) => l.type === 'raster')).toBe(true)
  })

  it('does not add a second raster once loaded', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    expect(map.addSource.mock.calls.filter((c) => c[1]?.type === 'raster')).toHaveLength(0)
    expect(map.addLayer.mock.calls.filter((c) => c[0]?.type === 'raster')).toHaveLength(0)
  })
})

// MapLibre resolves icon-image when the layer renders, and an unresolved one
// draws nothing and says nothing — the exact failure the missing '→' glyph
// already produced once. The ordering is what stops it happening again.
describe('mount() registers the wind arrow before the layer that draws it', () => {
  it('adds the arrow image, then the layer naming it', () => {
    const { map } = mountTestMap({ metric: 'P2' })

    const image = map.addImage.mock.calls.find((c) => c[0] === ARROW_IMAGE_ID)
    expect(image, 'no arrow image registered').toBeDefined()
    expect(image[1].data).toHaveLength(image[1].width * image[1].height * 4)

    const at = map.addLayer.mock.calls.findIndex((c) => c[0]?.id === WIND_LAYER_ID)
    expect(at, 'no wind layer added').toBeGreaterThanOrEqual(0)
    expect(map.addLayer.mock.calls[at][0].layout['icon-image']).toBe(ARROW_IMAGE_ID)
    expect(map.addImage.mock.invocationCallOrder[0])
      .toBeLessThan(map.addLayer.mock.invocationCallOrder[at])
  })
})

// The mute is only worth anything if the layer that draws the digits actually
// asks for it; wiring plain labelPaint here would carry the readings forward
// and draw every held one as if it had been measured.
describe('mount() fades the held readings on the hex label layer', () => {
  it('builds the hex label layer with the carried-aware paint', () => {
    const { map } = mountTestMap({ metric: 'P2' })

    const labels = map.addLayer.mock.calls.find((c) => c[0]?.id === HEX_LABEL_LAYER_ID)
    expect(labels, 'no hex label layer added').toBeDefined()
    expect(labels[0].paint['text-opacity']).toEqual([
      'case',
      ['==', ['get', 'carried'], true], CARRIED_OPACITY,
      ['==', ['get', 'fresh'], 0], FRESH_OPACITY,
      ['==', ['get', 'fresh'], 1], SETTLING_OPACITY,
      1,
    ])
  })
})

// The wind layer used to be appended last, drawing over the hex value labels
// and hiding the digits — icon-allow-overlap/icon-ignore-placement keep the
// arrows from yielding, so stacking order is the only thing that decides this.
describe('mount() draws the wind arrows beneath the hex labels', () => {
  it('adds the wind layer before the hex label layer', () => {
    const { map } = mountTestMap({ metric: 'P2' })

    const wind = map.addLayer.mock.calls.find((c) => c[0]?.id === WIND_LAYER_ID)
    expect(wind[1]).toBe(HEX_LABEL_LAYER_ID)
  })
})

// The markers are what a visitor clicked to open a sensor, and above the
// handover zoom they are gone. The cells inherit the click: at the point tier
// each carries the sensor_id of the device it was built from, so the panel
// stays reachable at exactly the zooms the dots stopped covering.
describe('mount() opens a sensor from the cell that carries one', () => {
  it('binds a click to the cells and opens the sensor it names', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const onCell = map.clickHandlers['airbg-hex-fill']
    expect(onCell, 'no click handler on the cells').toBeTypeOf('function')

    onCell({ features: [{ properties: { sensorId: 4242, value: 7 } }] })
    expect(getViewState().sensorId).toBe(4242)
  })

  // The hash alone opened nothing: the panel reads the registry, which the home
  // page (no slug) never fills.
  it('loads a sensor the map does not hold yet, so the panel opens without a reload', async () => {
    clearCache()
    setSensors(null)
    const asked = []
    vi.stubGlobal('fetch', vi.fn(async (url) => {
      asked.push(String(url))
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ id: 4242, lon: 23.31, lat: 42.69, slug: 'sofia', areas: [] }),
      }
    }))
    const { map } = mountTestMap({ metric: 'P2' })
    // After the load pass, which flies to a #sensor= of its own. Waited on the
    // markers, not on /api/v1/locate: that request is started ahead of the
    // scales now (see prefetchPlacement), so it no longer marks the end of the
    // pass — and a click landing before the pass reads the viewstate would make
    // the load pass itself the deep link.
    await vi.waitFor(() => expect(asked.some((u) => u.includes('/api/v1/overview'))).toBe(true))
    await new Promise((r) => setTimeout(r, 0))
    map.jumpTo.mockClear()

    map.clickHandlers['airbg-hex-fill']({ features: [{ properties: { sensorId: 4242 } }] })

    await vi.waitFor(() => {
      expect(asked.some((u) => u.includes('/api/v1/sensor/4242/locate'))).toBe(true)
    })
    await new Promise((r) => setTimeout(r, 0))
    // In place: the reader is already looking at the cell they clicked.
    expect(map.jumpTo.mock.calls.every((c) => c[0]?.zoom !== DEEP_LINK_ZOOM)).toBe(true)
  })

  // The placement decides the opening camera, and until it answers the map is
  // showing a view it is about to leave. Asked before the scales, not after
  // them: the camera needs no band table, and awaiting one to ask for the other
  // is a round trip of national view on a slow link.
  it('asks where to open before it asks for the colour scales', async () => {
    clearCache()
    setSensors(null)
    const asked = []
    vi.stubGlobal('fetch', vi.fn(async (url) => {
      asked.push(String(url))
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({ areas: [] }),
      }
    }))

    mountTestMap({ metric: 'P2' })
    await vi.waitFor(() => expect(asked.some((u) => u.endsWith('/api/v1/locate'))).toBe(true))

    expect(asked.findIndex((u) => u.endsWith('/api/v1/locate')))
      .toBeLessThan(asked.findIndex((u) => u.endsWith('/api/v1/scales')))
  })

  it('opens no panel for an aggregate cell, which names no device', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    map.clickHandlers['airbg-hex-fill']({
      features: [{ properties: { n: 9, value: 7 } }],
      lngLat: { lng: 23.32, lat: 42.7 },
    })
    expect(getViewState().sensorId ?? null).toBeNull()
  })
})

// Keyed off the marker tier, the caption told a reader zoomed onto one device
// that every cell was an area average.
describe('cellTier', () => {
  it('is what the caption under the key is written from', async () => {
    clearCache()
    setSensors(null)
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true, status: 200, headers: new Headers(), json: async () => ({ areas: [] }),
    })))
    const { map, el } = mountTestMap({
      metric: 'P2',
      dataset: { tTierCountry: 'each cell averages', tTierCity: 'each cell averages', tTierSensors: 'each cell is one sensor' },
    })
    map.getZoom = vi.fn(() => POINT_TIER_MIN_ZOOM + 1)

    map.handlers.moveend()

    await vi.waitFor(() => {
      expect(el.parentNode?.querySelector('.legend__tier')?.textContent ?? el.querySelector('.legend__tier')?.textContent)
        .toBe('each cell is one sensor')
    }, { timeout: 2000 })
  })
})

// The two ways of showing one reading must never be on screen at once: the dot
// is the device's position, the cell is the ground around it. Drawn together
// they put a labelled dot off-centre inside a labelled cell. The dots stop
// exactly where the cells take the number over.
describe('mount() hands the reading from the dots to the cells', () => {
  it('stops the aggregate markers where the cells start, not where the dots do', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const markers = map.addLayer.mock.calls.map((c) => c[0])
      .filter((l) => l.source === 'airbg-data')

    // Circles, official diamonds, labels — every layer the sensor source
    // feeds, so a fourth one added later has to answer this question too.
    expect(markers).toHaveLength(3)
    // Mounted on the country tier: those markers are province/municipality
    // circles, and the cells cover the same ground from GRID_MIN_ZOOM up.
    // Held at the point tier they were drawn OVER six zoom levels of hexes —
    // the dots-on-hexes the map showed.
    for (const l of markers) expect(l.maxzoom).toBe(GRID_MIN_ZOOM_FRACTIONAL)
  })

  it('moves the handover when the tier changes under a mounted map', () => {
    const ranges = []
    const map = {
      getLayer: (id) => ({ id }),
      setLayerZoomRange: (id, min, max) => ranges.push([id, min, max]),
    }

    applyMarkerZoomRange(map, 'sensors')
    expect(ranges).toEqual([
      ['airbg-markers', 0, POINT_TIER_MIN_ZOOM_FRACTIONAL],
      // The official diamonds hand over with the circles they stand beside.
      // Left out, they would have outlived the network they belong to.
      ['airbg-markers-official', 0, POINT_TIER_MIN_ZOOM_FRACTIONAL],
      ['airbg-marker-labels', 0, POINT_TIER_MIN_ZOOM_FRACTIONAL],
    ])
  })

  it('skips layers the style does not carry', () => {
    const ranges = []
    const map = {
      getLayer: () => undefined,
      setLayerZoomRange: (...a) => ranges.push(a),
    }
    applyMarkerZoomRange(map, 'country')
    expect(ranges).toEqual([])
  })

  it('does not draw the grid below the zoom its coarsest tier can fill', () => {
    // The server's coarsest cell is 15 km. Below GRID_MIN_ZOOM one of them is
    // under a pixel wide, which is what turned the whole grid into a field of
    // dots when zoomed out.
    const { map } = mountTestMap({ metric: 'P2' })
    const grid = map.addLayer.mock.calls.map((c) => c[0])
      .filter((l) => l.source === 'airbg-hexes' && l.type !== 'symbol')

    expect(grid.length).toBeGreaterThan(0)
    for (const l of grid) expect(l.minzoom).toBe(GRID_MIN_ZOOM_FRACTIONAL)
  })
})

// Where the key and the tier line LAND is load-bearing, not decoration, and
// both defects it guards were found in a browser rather than here.
//
// The key is absolutely positioned against .map-shell. Put it inside #map and
// the kit's phone rule — which turns it static so it sits UNDER the map below
// 672px — leaves it under the map but still inside it, over the canvas corner.
// Put the tier paragraph inside the shell and the shell grows taller than the
// map, so the key's inset-block-end:16px is measured from a bottom edge 16px
// below the map's own: measured live at -16px before this.
// The cfg every mountChrome test hands in. metricLabels and metricUnits are
// keyed by metric because that is what readConfig produces (see byMetric): the
// key's caption is looked up by name, never by position.
const chromeCfg = (over = {}) => ({
  t: { tier: {} },
  noDataColour: '#999',
  lang: 'bg',
  metric: 'P2',
  metricLabels: { P1: 'ФПЧ10', P2: 'ФПЧ2.5' },
  metricUnits: { P1: 'µg/m³', P2: 'µg/m³' },
  ...over,
})

// Wind is an overlay like the rest of what the map draws, so its control sits
// with them. A button of its own in the corner said it was a different kind of
// thing, and left the corner carrying two stacked buttons and a disclosure.
// Below the point tier a cell is an average of many sensors, and the number in
// it is the one thing the colour cannot say precisely. It stays off by default
// — a country of printed numbers is unreadable and the ramp is the primary
// reading — but a reader comparing two neighbourhoods should not have to zoom
// to sensor level, one cell at a time, to get the figures.
describe('the cell-values toggle', () => {
  const range = (map) =>
    map.setLayerZoomRange.mock.calls.filter((c) => c[0] === HEX_LABEL_LAYER_ID).at(-1)

  it('offers cell values as a layers option, off until asked for', () => {
    const { el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:cellValues"]')

    expect(box, 'no cell-values option in the layers menu').not.toBe(null)
    expect(box.checked).toBe(false)
    expect(box.closest('.colmenu__opt').textContent).toBe('Cell values')
  })

  it('leaves the labels at the point tier while it is off', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    expect(range(map)[1]).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
  })

  // The same zoom the cells themselves start at: a number that appeared at some
  // zoom of its own would print over ground that has no cell drawn under it.
  it('prints a number in every drawn cell once it is ticked', () => {
    const { map, el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:cellValues"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))

    expect(range(map)[1]).toBe(GRID_MIN_ZOOM_FRACTIONAL)
  })

  it('gives the point tier back when it is unticked', () => {
    const { map, el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:cellValues"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    box.checked = false
    box.dispatchEvent(new Event('change'))

    expect(range(map)[1]).toBe(POINT_TIER_MIN_ZOOM_FRACTIONAL)
  })
})

describe('the wind toggle lives in the layers menu, not in the corner', () => {
  it('mounts no wind button of its own', () => {
    const el = document.createElement('div')
    mountChrome(el, chromeCfg({ t: { tier: {}, windToggle: 'Вятър' } }))
    expect(el.querySelector('.map-wind')).toBe(null)
  })

  it('offers wind as a layers option, off until it is asked for', () => {
    const { el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:wind"]')

    expect(box, 'no wind option in the layers menu').not.toBe(null)
    // Every other option starts on. This one is not part of the map the reader
    // was shown, and turning it on costs a request.
    expect(box.checked).toBe(false)
    expect(box.closest('.colmenu__opt').textContent).toBe('Wind')
  })

  it('shows the arrows when the option is ticked', async () => {
    const { map, el } = mountTestMap({ metric: 'P2' })
    const box = el.querySelector('[data-layer-key="view:wind"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    await vi.waitFor(() => {
      expect(map.setLayoutProperty).toHaveBeenCalledWith(WIND_LAYER_ID, 'visibility', 'visible')
    })
  })

  // The arrow lattice is sized to the viewport, so the arrows a zoomed-in map
  // needs do not exist until the move is over. Without this the layer keeps the
  // field it was switched on with and empties out as the reader zooms in.
  it('redraws the field after a move, while the layer is on', async () => {
    const source = { setData: vi.fn() }
    const { map, el } = mountTestMap({ metric: 'P2' })
    // Only the wind source is spied: refresh and refreshHexes set data on their
    // own sources on the same moveend, and a shared spy could not tell them apart.
    map.getSource = vi.fn((id) => (id === WIND_SOURCE_ID ? source : { setData: vi.fn() }))
    const box = el.querySelector('[data-layer-key="view:wind"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled())

    source.setData.mockClear()
    map.getZoom.mockReturnValue(14)
    map.handlers.moveend()

    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled(), { timeout: 2000 })
  })

  // Switched on and then off again, the forecast body is still cached — so the
  // "have I got data" test is not enough to keep a hidden layer from being
  // repainted on every move.
  it('does not redraw the field after a move while the layer is off', async () => {
    const source = { setData: vi.fn() }
    const { map, el } = mountTestMap({ metric: 'P2' })
    // Only the wind source is spied: refresh and refreshHexes set data on their
    // own sources on the same moveend, and a shared spy could not tell them apart.
    map.getSource = vi.fn((id) => (id === WIND_SOURCE_ID ? source : { setData: vi.fn() }))
    const box = el.querySelector('[data-layer-key="view:wind"]')

    box.checked = true
    box.dispatchEvent(new Event('change'))
    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled())
    box.checked = false
    box.dispatchEvent(new Event('change'))
    source.setData.mockClear()

    map.getZoom.mockReturnValue(14)
    map.handlers.moveend()

    await new Promise((r) => setTimeout(r, 600))
    expect(source.setData).not.toHaveBeenCalled()
  })
})

// One zoom used to draw twice: the markers when they landed, the grid a request
// later. Both are held now until both have answered.
describe('a move paints its layers in one pass', () => {
  beforeEach(() => { clearCache(); resetViewStateForTests() })
  afterEach(() => { resetViewStateForTests() })

  it('holds the marker paint until the grid has answered too', async () => {
    let gate = null
    vi.stubGlobal('fetch', vi.fn(async (url) => {
      if (String(url).includes('/api/v1/hexes') && gate) await gate
      const json = String(url).includes('/api/v1/scales')
        ? [{ metric: 'P2', bands: [{ upper: 10, colour: '#00ff00' }] }]
        : { areas: [{ slug: 'sofia', lon: 23.3, lat: 42.7, covered: true, values: { P2: 5 }, sensor_count: 9 }], hexes: [] }
      return { ok: true, status: 200, headers: new Headers(), json: async () => json }
    }))

    const { map } = mountTestMap({ metric: 'P2' })
    await vi.waitFor(() => expect(map.painted).toContain('airbg-data'))

    let release
    gate = new Promise((r) => { release = r })
    map.painted.length = 0
    map.getZoom.mockReturnValue(10)
    map.handlers.moveend()

    // Long enough for the markers to have fetched, parsed and — before this
    // change — painted, while the grid is still out.
    await new Promise((r) => setTimeout(r, 600))
    expect(map.painted).toEqual([])

    release()
    await vi.waitFor(() => {
      expect(map.painted).toContain('airbg-data')
      expect(map.painted).toContain('airbg-hexes')
    })
  })
})

describe('mountChrome anchors the key to the shell and the tier line outside it', () => {
  const chrome = () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map map--hero'
    shell.appendChild(el)
    const host = document.createElement('div')
    host.append(shell)
    mountChrome(el, chromeCfg())
    return { shell, el, host }
  }

  it('puts the key in the shell, not in the map', () => {
    const { shell, el } = chrome()
    expect(el.querySelector('.scale--onmap')).toBeNull()
    expect(shell.querySelector(':scope > .scale--onmap')).not.toBeNull()
  })

  it('puts the tier line after the shell, so the shell stays the map box', () => {
    const { shell, host } = chrome()
    expect(shell.querySelector('.map-tier')).toBeNull()
    expect(host.lastElementChild.className).toContain('map-tier')
  })

  // The banners stay children of the map: they are messages about the map and
  // they do not have the phone rule the key has.
  it('leaves the hint and note inside the map', () => {
    const { el } = chrome()
    expect(el.querySelector('.map-hint')).not.toBeNull()
    expect(el.querySelector('.map-note')).not.toBeNull()
  })

  // Without a shell the key still has to render. It anchors to the map instead,
  // which loses the phone layout but shows a key rather than throwing.
  it('falls back to the map when no shell wraps it', () => {
    const el = document.createElement('div')
    document.createElement('div').appendChild(el)
    mountChrome(el, chromeCfg())
    expect(el.querySelector('.scale--onmap')).not.toBeNull()
  })
})

// The key's caption is the metric and its unit, not a fixed phrase. "Качество
// на въздуха" is simply false when the map is painting temperature, and it is
// the same words for every metric — so it says nothing about which one is on
// screen. The unit comes from the server-rendered catalogue rather than
// /api/v1/scales, which the key must caption without waiting on.
describe('the key names the metric it is a key to', () => {
  const captionOf = (cfg) => {
    const el = document.createElement('div')
    document.createElement('div').appendChild(el)
    return { el, chrome: mountChrome(el, cfg) }
  }

  it('composes the name and the unit on the first paint', () => {
    const { el } = captionOf(chromeCfg())
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ2.5, µg/m³')
  })

  // cfg.metric is already the new metric by the time onMetricChange calls
  // refresh, and refresh passes it through — so the caption follows the
  // switcher without the key subscribing to anything.
  it('follows the metric switch', () => {
    const { el, chrome } = captionOf(chromeCfg())
    chrome.showLegend({ bands: [], tier: null, metric: 'P1' })
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ10, µg/m³')
  })

  // A metric the catalogue has no unit for still gets its name.
  it('drops to the name alone when the metric has no unit', () => {
    const cfg = chromeCfg({ metricUnits: { P1: '', P2: '' } })
    const { el } = captionOf(cfg)
    expect(el.querySelector('.scale__label').textContent).toBe('ФПЧ2.5')
  })

  // Only a metric with no name at all falls back, because a caption reading
  // just "µg/m³" would name nothing.
  it('falls back to the generic title when the metric has no name', () => {
    const cfg = chromeCfg({ metricLabels: {}, t: { tier: {}, legend: 'Качество на въздуха' } })
    const { el } = captionOf(cfg)
    expect(el.querySelector('.scale__label').textContent).toBe('Качество на въздуха')
  })
})

// The sensor status filter (lib/sensorfilter.svelte.js) decides which sensors
// the map draws. Driven through mount() rather than through repaintSensors
// alone, because the wiring — the subscription, and the payload the map keeps
// in hand so a filter change needs no second fetch — is the part that can
// silently rot.
describe('the sensor status filter', () => {
  // Two sensors, one of them silent on P2: the whole point of the filter is
  // that these two are not the same number.
  function stubMixedSensorFetch() {
    return vi.fn(async (url) => {
      if (url === '/api/v1/scales') {
        return { ok: true, status: 200, headers: new Headers(), json: async () => [] }
      }
      return {
        ok: true, status: 200, headers: new Headers(),
        json: async () => ({
          sensors: {
            id: [42, 43], lon: [23.3, 23.4], lat: [42.7, 42.8],
            quality: ['ok', 'ok'], P2: [12, null],
          },
        }),
      }
    })
  }

  // A stable source stub: FakeMap.getSource hands back a fresh mock per call,
  // so without this every setData lands on an object the test cannot see.
  // A stable source stub, keyed by id: FakeMap.getSource hands back a fresh
  // mock per call, so without this every setData lands on an object the test
  // cannot see — and one shared stub would mix the sensor layer's paints with
  // the wind layer's, whose last call is an empty collection.
  function withStableSource(map) {
    const sources = new Map()
    map.getSource = vi.fn((id) => {
      if (!sources.has(id)) sources.set(id, { setData: vi.fn() })
      return sources.get(id)
    })
    return {
      get setData() { return sources.get('airbg-data')?.setData ?? { mock: { calls: [] } } },
    }
  }

  const drawn = (source) => source.setData.mock.calls.at(-1)[0].features

  beforeEach(() => { clearCache(); resetViewStateForTests(); setSensors(null); resetSensorFilterForTests(); resetSourceFilterForTests() })
  afterEach(() => { resetViewStateForTests(); setSensors(null); resetSensorFilterForTests(); resetSourceFilterForTests() })

  // The store opens on the kit's default, "with data", so the FIRST paint is
  // already filtered — the silent sensor never reaches the map until asked for.
  it('opens on the sensors with data, before the reader touches anything', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    const source = withStableSource(map)

    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    await vi.waitFor(() => expect(source.setData).toHaveBeenCalled())
    expect(drawn(source).map((f) => f.properties.id)).toEqual([42])
  })

  // The repaint must come from the payload already in hand. A filter change
  // touches neither the tier, the slug nor the metric, so a refetch would be a
  // request for data the island is holding.
  it('repaints from the payload it already has, with no second request', async () => {
    const fetchSpy = stubMixedSensorFetch()
    vi.stubGlobal('fetch', fetchSpy)
    mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())

    const before = fetchSpy.mock.calls.length
    setSensorStatus('all')
    expect(fetchSpy.mock.calls.length).toBe(before)
  })

  it('brings the silent sensors back when the reader asks for all of them', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    const source = withStableSource(map)

    setSensorStatus('all')

    expect(drawn(source).map((f) => f.properties.id)).toEqual([42, 43])
  })

  it('keeps only the silent ones on the other side of the filter', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    const source = withStableSource(map)

    setSensorStatus('inactive')

    expect(drawn(source).map((f) => f.properties.id)).toEqual([43])
  })

  // The filter is a control over sensors. Zoomed out to province aggregates
  // there are no sensors on screen, so a click on it must leave the aggregates
  // alone rather than repaint the last sensor payload over them.
  it('does not repaint once the map has left the sensor tier', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())

    // Aggregate tier: a lower zoom, and an area marker click to drive the
    // refresh synchronously rather than through the debounced moveend.
    map.getZoom = () => 7
    const after = vi.fn(() => ({ setData: vi.fn() }))
    map.getSource = after
    map.handlers.click({ features: [{ properties: { slug: 'plovdiv' } }] })
    // The aggregate paint, not the sensor one: waiting on the spy installed
    // above is what guarantees the tier change has actually landed.
    await vi.waitFor(() => expect(after).toHaveBeenCalled())

    map.getSource = vi.fn(() => ({ setData: vi.fn() }))
    setSensorStatus('active')

    expect(map.getSource).not.toHaveBeenCalled()
  })

  // The banner, not the paint: unticking both networks empties the marker
  // source, and the source-change handler is the only thing that recomputes
  // the hint without a refresh behind it.
  it('explains the blank map as soon as both networks are unticked', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { el } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    const banner = el.querySelector('.map-hint')

    setSourceEnabled('sensor.community', false)
    setSourceEnabled('eea', false)

    expect(banner.hidden).toBe(false)
    expect(banner.textContent).not.toBe('')
  })

  // A subscription that outlives the island repaints a destroyed map on the
  // next status change.
  it('stops listening once the island is stopped', async () => {
    vi.stubGlobal('fetch', stubMixedSensorFetch())
    const { map, stop } = mountSensorTierMap()
    await vi.waitFor(() => expect(findSensor(42)).not.toBeNull())
    withStableSource(map)

    stop()
    setSensorStatus('active')

    // getSource, not setData: a stopped island never reaches the source at
    // all, so there is no spy on the sensor layer to interrogate.
    expect(map.getSource).not.toHaveBeenCalled()
  })
})


// The map is a canvas, so the banner is the only part of its running commentary
// a screen reader can reach. Without aria-live the text changes silently.
describe('mountChrome() announces the hint banner', () => {
  it('marks it as a polite live region', () => {
    const shell = document.createElement('div')
    shell.className = 'map-shell'
    const el = document.createElement('div')
    el.className = 'map'
    shell.appendChild(el)
    document.body.appendChild(shell)

    mountChrome(el, readConfig(el))
    expect(el.querySelector('.map-hint')?.getAttribute('aria-live')).toBe('polite')
  })
})


// The official stations draw as diamonds, the citizen ones as circles, and no
// marker is allowed to fall between the two layers or land in both.
describe('the official marker layer', () => {
  it('splits the source in two along the network, leaving areas with the dots', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const [circles, diamonds] = map.addLayer.mock.calls.map((c) => c[0])
      .filter((l) => l.source === 'airbg-data' && l.id !== 'airbg-marker-labels')

    expect(circles.type).toBe('circle')
    expect(diamonds.type).toBe('symbol')
    expect(circles.filter).toEqual(NOT_OFFICIAL)
    expect(diamonds.filter).toEqual(['==', ['get', 'source'], 'eea'])
  })

  it('registers the diamond as an SDF, which is what lets the ramp colour it', () => {
    const { map } = mountTestMap({ metric: 'P2' })
    const [id, image, opts] = map.addImage.mock.calls.find((c) => c[0] === 'airbg-diamond')

    expect(id).toBe('airbg-diamond')
    expect(image.width).toBe(image.height)
    expect(opts.sdf).toBe(true)
  })

})

// mountChrome returns a storage handle so player and legend prefs can thread
// through injected storage in tests and use the same handle in production.
describe('mountChrome storage handle', () => {
  it('threads the injected storage handle through to the player', async () => {
    const store = new Map()
    store.set(PLAY_SPEED_KEY, '0.5') // Set initial speed
    const fakeStorage = {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, v),
    }

    const { map } = mountTestMap({ metric: 'P2' })

    // The whole point of the task: chrome, built by mountChrome, is the object
    // installTimelapse is handed. Hand-building `{player, storage}` here would
    // pass whether or not mountChrome returns a storage handle at all.
    const el = document.createElement('div')
    document.body.appendChild(el)
    const chrome = mountChrome(el, chromeCfg({ storage: fakeStorage }))
    const ui = chrome.player

    installTimelapse(map, {}, { metric: 'P2', lang: 'en', t: {} }, chrome, async () => ({
      metric: 'P2', resolution_km: 15, cells: [[23, 42]],
      frames: [{ t: '2026-09-08T06:00:00Z', v: [10] }],
    }))

    ui.button.click()
    await vi.waitFor(() => expect(ui.speed.textContent).not.toBe(''))

    // Verify the initial speed was read from the fake storage
    expect(ui.speed.textContent).toBe('0.5×')

    // Change the speed
    ui.speed.click()
    expect(ui.speed.textContent).toBe('0.25×')

    // Verify it was written to the fake storage
    expect(store.get(PLAY_SPEED_KEY)).toBe('0.25')

    ui.button.click() // close player
  })
})
