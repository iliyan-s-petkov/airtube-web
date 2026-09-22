// The island seam for the timelapse player: it wires the pure frame layer in
// ./timelapse.js (cursors, timelapseURL, band and threshold logic) to a live
// map and the player UI. Kept separate, same as mapwind.js/wind.js, so the
// pure module stays importable on its own.
import { getJSON } from './api.js'
import { rampColour } from './ramp.js'
import { filterByStatus, getSensorStatus } from './sensorfilter.svelte.js'
import { readChoice, writeChoice } from './storage.js'
import {
  DEFAULT_SPEED, SPEEDS, cursor, fillForward, frameBody, frameCount, frameTime, frameDelay,
  hasHistory, nextSpeed, seek, step, thinFrames, timelapseURL,
} from './timelapse.js'
import { hexFeatures, resolutionForZoom } from './hexes.js'
import { HEX_SOURCE_ID } from './mapids.js'
import { PLAY_SPEED_KEY } from './mapconfig.js'
import { bandsFor } from './mappaint.js'
import { refreshHexes, mapInlineSize } from './mapdata.js'

// installTimelapse swaps a past hour's numbers into the hex layer the map
// already draws, and nothing else — state.hexBody is never written, so the live
// grid survives an animation. Fetched on the first press, not at mount: most
// visitors never press play.
export function installTimelapse(map, state, cfg, chrome, fetchJSON = getJSON) {
  const ui = chrome.player
  if (!ui) return null

  const head = cursor(0)
  let body = null
  let loaded = ''
  // true while a play session is active (paused-for-hidden counts as active);
  // raf is the actual requestAnimationFrame handle, null whenever none is
  // scheduled.
  let running = false
  let raf = null
  // Frames one tick may advance after a stall. Above this the animation is
  // catching up on time nobody watched, which reads as a jump, not motion.
  const MAX_CATCHUP_FRAMES = 4
  let acc = 0
  let last = 0
  // Read once, at install: the speed is a preference, and re-reading storage
  // per frame would let another tab change the rate mid-animation.
  let speed = readChoice(PLAY_SPEED_KEY, SPEEDS, DEFAULT_SPEED, chrome.storage)
  // Recomputed per body, not per frame: which hours are thin depends on the
  // best hour in the same body, so it cannot be decided one frame at a time.
  let thin = new Set()
  // Open once the button has been pressed, closed again by exit or reset —
  // NOT by pause, which leaves the scrubber on screen. A zoom before this is
  // true is the live grid's business, not the replay's.
  let open = false

  const clock = new Intl.DateTimeFormat(cfg.lang || 'bg', {
    weekday: 'short', hour: '2-digit', minute: '2-digit',
  })

  // Which cells carried a digit in the frame drawn before this one, and which
  // of those had only just arrived. null means there is no previous frame to
  // compare against, so nothing in the first frame drawn counts as an arrival
  // and nothing in it fades in.
  let prevValued = null
  let justArrived = new Set()
  // Called on entering and leaving replay and on a metric or tier change, NOT
  // on a scrub or a speed change: those stay inside one run, where the previous
  // frame is still the frame the reader was just looking at.
  const forgetFrames = () => {
    prevValued = null
    justArrived = new Set()
  }

  // Keyed on the drawn geometry, not on the cell index: hexFeatures reorders
  // its output and may merge cells, so a frame's own index does not survive
  // into the feature.
  const cellKey = (f) => (f.geometry.type === 'Point'
    ? f.geometry.coordinates
    : f.geometry.coordinates[0][0]).join(',')

  // A digit appearing where there was none pulls the eye to the arrival rather
  // than to the value, so a new cell climbs two steps to full strength. Reuses
  // the replay clock's own reducedMotion(): under it the end state is drawn at
  // once, since a slower ramp is still motion.
  const markArrivals = (features) => {
    const valued = new Set()
    const arrived = new Set()
    const ramp = prevValued !== null && !reducedMotion()
    for (const f of features) {
      const p = f.properties
      if (p.value === null || p.value === undefined) continue
      const key = cellKey(f)
      valued.add(key)
      // A carried cell is excluded before anything else, matching the paint
      // expression: it is holding a value it already had.
      if (!ramp || p.carried === true) continue
      if (!prevValued.has(key)) {
        p.fresh = 0
        arrived.add(key)
      } else if (justArrived.has(key)) {
        p.fresh = 1
      }
    }
    prevValued = valued
    justArrived = arrived
    return features
  }

  // Held against its two inputs by identity, not computed once: paint() asked
  // bandsFor per frame, but installTimelapse runs before initData awaits the
  // scales, so a run started inside that window would keep the empty table and
  // draw a grey country under a working clock. cfg.metric is in the key for the
  // same reason, not because reset() would miss it.
  let bandsFrom = null
  let bandsMetric = null
  let bands = []
  const currentBands = () => {
    if (state.scales === bandsFrom && cfg.metric === bandsMetric) return bands
    bandsFrom = state.scales
    bandsMetric = cfg.metric
    bands = bandsFor(bandsFrom, bandsMetric)
    return bands
  }

  const paint = (i) => {
    const features = hexFeatures(
      frameBody(body, i), cfg.metric, currentBands(), cfg.noDataColour, rampColour,
      // No network filter: a frame is folded from reading_hourly, which carries
      // no source column, so there is nothing to filter it by.
      resolutionForZoom(Math.round(map.getZoom()), mapInlineSize(map)), null,
    )
    map.getSource(HEX_SOURCE_ID)?.setData({
      type: 'FeatureCollection',
      features: markArrivals(filterByStatus(features, getSensorStatus())),
    })
    const t = frameTime(body, i)
    ui.at(i, t ? clock.format(t) : '')
    // The frame still draws. A near-empty map under a confident clock reads as
    // clean air, so the gap is captioned rather than skipped or frozen over.
    ui.say(thin.has(i) ? (cfg.t?.replayThin || '') : '')
  }

  const stop = async (restore = true) => {
    pauseClock()
    // The live grid goes back up here, so the next press of play opens on a
    // screen the replay did not draw — its first frame is not an arrival.
    forgetFrames()
    head.playing = false
    ui.playing(false)
    // open is already false by the time exit/reset call this — a plain pause
    // (ontoggle) never touches it, so the zoom follow-along above keeps
    // working while paused.
    if (!open) map.off('zoom', onZoom)
    // No refetch: refreshHexes' dedup skips a URL it holds and repaints from the
    // live body it kept, which this never wrote over.
    if (restore) await refreshHexes(map, state, cfg, fetchJSON)
  }

  // Rounded the same way hexesURL rounds it (see its own comment): a
  // fractional zoom mid-flyTo must not earn its own request.
  // Width as well as zoom, the same pair refreshHexes asks with: a replay tier
  // coarser than the live one swaps in cells twice the size of those on screen.
  const wantedURL = () =>
    timelapseURL(cfg.metric, state.window, resolutionForZoom(Math.round(map.getZoom()), mapInlineSize(map)))

  // keepPlayhead is true only for a zoom-driven refetch: a fresh press of play
  // starts the story over, but a reader mid-animation should not be thrown
  // back to frame 0 just because the tier under them changed.
  const load = async (keepPlayhead = false) => {
    const url = wantedURL()
    // show() again on the held body: leaving the animation hides the scrubber,
    // and this is the path that brings it back without a second fetch. Also
    // the dedup that keeps a zoom within the same tier from refetching.
    if (url === loaded && body) {
      ui.show(head.count)
      return head.count > 0
    }
    let measured
    try {
      measured = await fetchJSON(url)
    } catch (err) {
      // Quiet, like refreshHexes': the map underneath is working.
      console.error('timelapse:', err)
      return false
    }
    // Drawn from the held body, judged on the measured one: coverage counts the
    // readings actually taken, so filling gaps in before thinFrames saw them
    // would report every hour as complete and silence the guard.
    body = fillForward(measured)
    // A new tier redraws every cell at a new size, so nothing on screen carries
    // over and the whole map would otherwise read as one mass arrival.
    forgetFrames()
    loaded = url
    thin = thinFrames(measured)
    head.count = frameCount(body)
    head.i = keepPlayhead ? seek(head, head.i) : 0
    ui.show(head.count)
    ui.atSpeed(speed)
    return head.count > 0
  }

  // Follows the reader onto the tier the new zoom would ask the live grid
  // for. Attached only while the player is open (see ontoggle/stop), not for
  // the page's whole life — load()'s url === loaded check is what turns
  // a run of zoom events during a flyTo into at most one request.
  const onZoom = () => {
    // Refetch always, repaint only when the body actually changed AND the
    // animation is running: a flyTo fires a zoom event per frame, and pause
    // has already put the live grid back — redrawing a frame over it would
    // undo the reader's own press of pause.
    const was = loaded
    load(true).then((ok) => {
      if (ok && loaded !== was && head.playing) paint(head.i)
    })
  }

  // matchMedia is missing under jsdom and some old browsers — absent means
  // "no preference", not "reduced".
  const reducedMotion = () => typeof matchMedia === 'function'
    && matchMedia('(prefers-reduced-motion: reduce)').matches

  // Read per run(), not cached at module load: a reader can flip the OS
  // setting mid-session, and a cached value would need a reload to take
  // effect. No speed can outrun the 0.25x floor while reduced motion is on.
  const effectiveDelay = () => {
    const base = frameDelay(speed)
    return reducedMotion() ? Math.max(base, frameDelay(0.25)) : base
  }

  // setInterval keeps firing in a background tab and coalesces under load —
  // wrong for an animation. rAF plus an accumulator advances exactly one
  // frame per elapsed delay, however the ticks themselves land.
  const tick = (now) => {
    acc += now - last
    last = now
    const delay = effectiveDelay()
    // A stall that visibilitychange does not cover — a long GC pause, a bfcache
    // restore — would otherwise replay the whole gap as one synchronous burst.
    if (acc > delay * MAX_CATCHUP_FRAMES) acc = delay * MAX_CATCHUP_FRAMES
    // Advance the playhead over every elapsed frame, then paint once. Painting
    // each of them would build and setData up to four frames only the last of
    // which ever composites, and would spend the late-joiner fade on frames
    // nobody sees — a cell arriving mid-catch-up would be drawn already settled.
    let advanced = 0
    while (acc >= delay) {
      step(head)
      acc -= delay
      advanced++
    }
    if (advanced > 0) paint(head.i)
    raf = requestAnimationFrame(tick)
  }

  // Backgrounding drops the rAF handle rather than letting it run unseen.
  // Resuming resets the accumulator instead of catching up, so the elapsed
  // background time is not replayed as a burst of frames.
  const onVisibility = () => {
    if (document.hidden) {
      if (raf) cancelAnimationFrame(raf)
      raf = null
    } else if (running && !raf) {
      acc = 0
      last = performance.now()
      raf = requestAnimationFrame(tick)
    }
  }

  const pauseClock = () => {
    if (raf) cancelAnimationFrame(raf)
    raf = null
    running = false
    document.removeEventListener('visibilitychange', onVisibility)
  }

  // The one place the clock is (re)started, so a speed change mid-animation
  // and a fresh press of play cannot disagree about the delay.
  const run = () => {
    if (raf) cancelAnimationFrame(raf)
    document.removeEventListener('visibilitychange', onVisibility)
    running = true
    acc = 0
    last = performance.now()
    document.addEventListener('visibilitychange', onVisibility)
    raf = document.hidden ? null : requestAnimationFrame(tick)
  }

  ui.ontoggle(async () => {
    if (running) {
      await stop()
      return
    }
    if (!await load()) return
    // Two of the published metrics carry no history at all. Animating them
    // plays a blank country for nine seconds under a running clock.
    if (!hasHistory(body)) {
      ui.say(cfg.t?.replayNoHistory || '')
      return
    }
    if (!open) {
      open = true
      map.on('zoom', onZoom)
    }
    head.playing = true
    ui.playing(true)
    paint(head.i)
    run()
  })

  // A press while paused is a question about the next play, not a request to
  // start one — so the clock is only rebuilt if it was already running.
  ui.onspeed(() => {
    speed = nextSpeed(speed)
    writeChoice(PLAY_SPEED_KEY, speed, chrome.storage)
    ui.atSpeed(speed)
    if (running) run()
  })

  // A drag is a request to look at one hour: leaving the clock going would move
  // the map off that frame a third of a second later.
  ui.onscrub((i) => {
    if (running) {
      pauseClock()
      head.playing = false
      ui.playing(false)
    }
    if (head.count > 0) paint(seek(head, i))
  })

  // The way out. A scrub pauses on a past hour without restoring anything, and
  // pressing play from there replays rather than returning, so this is the only
  // control that puts the live grid back. It collapses the scrubber too: the
  // held body stays, so the next press of play repaints without a refetch.
  ui.onexit(async () => {
    open = false
    ui.show(0)
    ui.say('')
    await stop()
  })

  // A different window or metric is a different animation; what is held is stale.
  return {
    async reset() {
      open = false
      body = null
      loaded = ''
      head.count = 0
      thin = new Set()
      ui.show(0)
      ui.say('')
      await stop(false)
    },
  }
}
