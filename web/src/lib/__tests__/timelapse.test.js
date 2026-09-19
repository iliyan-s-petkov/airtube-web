// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import {
  FRAME_MS, SPANS, knownSpan, spanFor, timelapseURL, frameBody, frameCount, frameTime,
  cursor, step, seek, mountPlayer, frameCoverage, hasHistory, thinFrames, fillForward,
  DEFAULT_SPEED, SPEEDS, frameDelay, nextSpeed, speedLabel,
} from '../timelapse.js'

const BODY = {
  metric: 'P2',
  resolution_km: 15,
  cells: [[23.0, 42.0], [27.0, 43.0]],
  frames: [
    { t: '2026-09-08T06:00:00Z', v: [10, null] },
    { t: '2026-09-08T07:00:00Z', v: [null, 0] },
  ],
}

describe('the span vocabulary', () => {
  it('is the list the server publishes', () => {
    expect(SPANS).toEqual(['24h', '48h', '7d'])
    expect(knownSpan('24h')).toBe(true)
    expect(knownSpan('48h')).toBe(true)
  })

  // A reader looking at a week and pressing play means the week.
  it('follows the map window when the server animates it', () => {
    expect(spanFor('7d')).toBe('7d')
    expect(spanFor('24h')).toBe('24h')
    // Every window the map offers is animated now; none of them silently
    // plays back a different stretch of time from the one on the button.
    expect(spanFor('48h')).toBe('48h')
  })

  // Live has no animation of its own. Falling back to the day rather than
  // sending a span the server refuses: a 400 on the first press of play is not
  // a feature.
  it('falls back to the day for a window with no animation', () => {
    expect(spanFor('')).toBe('24h')
    expect(spanFor('nonsense')).toBe('24h')
  })

  it('never puts an unpublished span on the URL', () => {
    expect(timelapseURL('P2', 'nonsense')).toBe('/api/v1/timelapse?metric=P2&span=24h')
    expect(timelapseURL('P2', '48h')).toBe('/api/v1/timelapse?metric=P2&span=48h')
    expect(timelapseURL('P1', '7d')).toBe('/api/v1/timelapse?metric=P1&span=7d')
  })

  // A caller that names no resolution must still produce today's URL, so
  // existing callers and cached URLs are unaffected.
  it('omits the resolution when none is named', () => {
    expect(timelapseURL('P2', '24h')).toBe('/api/v1/timelapse?metric=P2&span=24h')
  })

  // The server snaps whatever it is sent onto a published replay tier; the
  // client's job is only to say what it wants.
  it('carries the wanted cell size as resolution_km', () => {
    expect(timelapseURL('P2', '24h', 45)).toBe('/api/v1/timelapse?metric=P2&span=24h&resolution_km=45')
  })
})

describe('reading a frame', () => {
  // The one place the geometry and the numbers are paired. Get it wrong and
  // every cell is drawn with its neighbour's reading.
  it('pairs each cell with the value at its own index', () => {
    const b = frameBody(BODY, 0)
    expect(b.resolution_km).toBe(15)
    expect(b.hexes).toEqual([
      { lon: 23.0, lat: 42.0, values: { P2: 10 }, carried: false },
      { lon: 27.0, lat: 43.0, values: { P2: null }, carried: false },
    ])
  })

  // 0 µg/m³ is a reading and an absent cell is not; the map draws them
  // differently, so this must not collapse the two.
  it('keeps a reported zero apart from an absent one', () => {
    const b = frameBody(BODY, 1)
    expect(b.hexes[0].values.P2).toBeNull()
    expect(b.hexes[1].values.P2).toBe(0)
  })

  // A frame carries no sensor count. A made-up 1 would be a popup claiming the
  // cell holds one device.
  it('states no count', () => {
    expect(frameBody(BODY, 0).hexes[0].n).toBeUndefined()
  })

  it('is empty rather than broken past the last frame', () => {
    expect(frameBody(BODY, 9).hexes).toEqual([])
    expect(frameBody(null, 0).hexes).toEqual([])
    expect(frameCount(null)).toBe(0)
    expect(frameCount(BODY)).toBe(2)
  })

  it('reads a frame time, and refuses an unusable one', () => {
    expect(frameTime(BODY, 0).toISOString()).toBe('2026-09-08T06:00:00.000Z')
    expect(frameTime({ frames: [{ t: 'not a date' }] }, 0)).toBeNull()
    expect(frameTime(BODY, 9)).toBeNull()
  })
})

describe('the playhead', () => {
  it('wraps at the end, because an animation is watched round', () => {
    const c = cursor(2)
    expect(step(c)).toBe(1)
    expect(step(c)).toBe(0)
  })

  it('clamps a scrub to the frames that exist', () => {
    const c = cursor(3)
    expect(seek(c, 5)).toBe(2)
    expect(seek(c, -1)).toBe(0)
    expect(seek(c, 1.7)).toBe(1)
  })

  it('stays put with nothing loaded', () => {
    const c = cursor(0)
    expect(step(c)).toBe(0)
    expect(seek(c, 3)).toBe(0)
  })
})

describe('mountPlayer', () => {
  const labels = { label: 'Time', playLabel: 'Play', pauseLabel: 'Pause', exitLabel: 'Now' }

  function mount() {
    const frame = document.createElement('div')
    frame.id = 'map'
    const host = document.createElement('div')
    document.body.append(frame, host)
    return { ui: mountPlayer(frame, { ...labels, host }), host }
  }

  // Icon-only, so the name has to be somewhere a screen reader reaches.
  it('names the button without a word in it', () => {
    const { ui } = mount()
    expect(ui.button.textContent.trim()).toBe('')
    expect(ui.button.getAttribute('aria-label')).toBe('Play')
    expect(ui.button.getAttribute('title')).toBe('Play')
    expect(ui.button.querySelector('svg').getAttribute('aria-hidden')).toBe('true')
  })

  // The button IS the state: a reader who has started an animation must be able
  // to see that pressing again stops it.
  it('becomes Pause while playing, and Play again after', () => {
    const { ui } = mount()
    const d = () => ui.button.querySelector('path').getAttribute('d')
    const playGlyph = d()

    ui.playing(true)
    expect(ui.button.getAttribute('aria-pressed')).toBe('true')
    expect(ui.button.getAttribute('aria-label')).toBe('Pause')
    expect(d()).not.toBe(playGlyph)

    ui.playing(false)
    expect(ui.button.getAttribute('aria-pressed')).toBe('false')
    expect(ui.button.getAttribute('aria-label')).toBe('Play')
    expect(d()).toBe(playGlyph)
  })

  // A slider over a map that is not animating is a control with nothing behind
  // it, and dragging it would be a promise the page cannot keep.
  it('hides the scrubber and the clock until an animation is loaded', () => {
    const { ui } = mount()
    expect(ui.slider.hidden).toBe(true)
    expect(ui.clock.hidden).toBe(true)

    ui.show(24)
    expect(ui.slider.hidden).toBe(false)
    expect(ui.clock.hidden).toBe(false)
    expect(ui.slider.max).toBe('23')

    ui.show(0)
    expect(ui.slider.hidden).toBe(true)
  })

  // The exit is the way out of the animation, so it appears and disappears with
  // the scrubber it cancels: offering it over a map that is not animating would
  // be a button with nothing to leave.
  it('shows the exit with the scrubber and hides it again', () => {
    const { ui } = mount()
    expect(ui.exit.hidden).toBe(true)

    ui.show(24)
    expect(ui.exit.hidden).toBe(false)

    ui.show(0)
    expect(ui.exit.hidden).toBe(true)
  })

  // Icon-only like the play button beside it, and named for what it does rather
  // than for the state it leaves.
  it('names the exit without a word in it', () => {
    const { ui } = mount()
    expect(ui.exit.textContent.trim()).toBe('')
    expect(ui.exit.getAttribute('aria-label')).toBe('Now')
    expect(ui.exit.getAttribute('title')).toBe('Now')
    expect(ui.exit.querySelector('svg').getAttribute('aria-hidden')).toBe('true')
  })

  it('reports a press, a scrub and an exit', () => {
    const { ui } = mount()
    const pressed = []
    const scrubbed = []
    const exited = []
    ui.ontoggle(() => pressed.push(true))
    ui.onscrub((i) => scrubbed.push(i))
    ui.onexit(() => exited.push(true))

    ui.button.click()
    ui.show(10)
    ui.slider.value = '4'
    ui.slider.dispatchEvent(new Event('input'))
    ui.exit.click()

    expect(pressed).toHaveLength(1)
    expect(scrubbed).toEqual([4])
    expect(exited).toHaveLength(1)
  })

  // The exit must not also fire the play toggle: one press would then stop the
  // animation and immediately start it again.
  it('does not report an exit as a press', () => {
    const { ui } = mount()
    const pressed = []
    ui.ontoggle(() => pressed.push(true))
    ui.show(10)

    ui.exit.click()
    expect(pressed).toEqual([])
  })

  it('puts the playhead and its time on screen', () => {
    const { ui } = mount()
    ui.show(24)
    ui.at(7, '13:00')
    expect(ui.slider.value).toBe('7')
    expect(ui.clock.textContent).toBe('13:00')
  })

  it('is appended into the host', () => {
    const { ui, host } = mount()
    expect(ui.root.parentElement).toBe(host)
  })

  // style-src has no 'unsafe-inline', so anything positioned from here would be
  // dropped by the browser and the control would land wherever the flow put it.
  it('writes no inline style', () => {
    const { ui } = mount()
    for (const el of [ui.root, ui.button, ui.slider, ui.clock, ui.exit]) {
      expect(el.getAttribute('style')).toBeNull()
    }
  })
})

// Coverage guard. The numbers in these bodies are the shapes production actually
// serves — measured 2026-09-18 at the 15 km tier.
describe('coverage', () => {
  const frames = (...counts) => ({
    metric: 'NO2',
    cells: Array.from({ length: Math.max(0, ...counts) }, (_, i) => [23 + i, 42]),
    frames: counts.map((n, i) => ({
      t: `2026-09-18T${String(i).padStart(2, '0')}:00:00Z`,
      v: Array.from({ length: Math.max(0, ...counts) }, (_, j) => (j < n ? 5 : null)),
    })),
  })

  it('counts the cells in a frame that carry a reading', () => {
    expect(frameCoverage(frames(3, 0, 1))).toEqual([3, 0, 1])
  })

  // A cell reading exactly 0 µg/m³ is a measurement, not an absence — the same
  // distinction the payload draws by making V a pointer.
  it('counts a zero reading as covered', () => {
    const body = { cells: [[23, 42]], frames: [{ t: 'x', v: [0] }] }
    expect(frameCoverage(body)).toEqual([1])
  })

  it('reports no history when every frame is empty', () => {
    // noise_LAeq on production: 0 cells, 28 frames, all of them blank.
    expect(hasHistory(frames(...Array(28).fill(0)))).toBe(false)
    expect(hasHistory({ cells: [], frames: [] })).toBe(false)
  })

  it('reports history when any frame carries a reading', () => {
    expect(hasHistory(frames(0, 0, 1))).toBe(true)
  })

  // NO2 over 24h: the EEA hours land irregularly, so most frames are empty and
  // a few are partial. The empty ones are thin; 7 against a best of 20 is not.
  it('marks a frame thin only below a quarter of the body its own best hour', () => {
    const thin = thinFrames(frames(0, 0, 7, 20, 10, 15, 0, 4))
    expect([...thin].sort((a, b) => a - b)).toEqual([0, 1, 6, 7])
  })

  // A small network is not a broken one. NOX publishes one cell nationwide, and
  // an animation of its one honest cell must not be captioned as defective.
  it('marks no frame thin when every frame carries the same coverage', () => {
    expect(thinFrames(frames(1, 1, 1, 1)).size).toBe(0)
    expect(thinFrames(frames(280, 277, 284)).size).toBe(0)
  })

  // hasHistory is what speaks for this body; captioning all 28 frames as thin
  // would put a "partial data" note on an animation that is not playing at all.
  it('marks no frame thin when there is no history to compare against', () => {
    expect(thinFrames(frames(0, 0, 0)).size).toBe(0)
  })

  it('survives a body with no frames', () => {
    expect(frameCoverage(undefined)).toEqual([])
    expect(hasHistory(undefined)).toBe(false)
    expect(thinFrames(undefined).size).toBe(0)
  })
})

describe('mountPlayer note', () => {
  const mountNote = () => {
    const host = document.createElement('div')
    return mountPlayer(host, { label: 'Time', playLabel: 'Play', pauseLabel: 'Pause', exitLabel: 'Now' })
  }

  it('starts with nothing to say', () => {
    const ui = mountNote()
    expect(ui.note.hidden).toBe(true)
    expect(ui.note.textContent).toBe('')
  })

  it('shows and clears a note', () => {
    const ui = mountNote()
    ui.say('Partial data for this hour')
    expect(ui.note.hidden).toBe(false)
    expect(ui.note.textContent).toBe('Partial data for this hour')

    ui.say('')
    expect(ui.note.hidden).toBe(true)
  })

  // It appears mid-animation, so a reader whose attention is on the map rather
  // than the control gets told the frame is thin rather than shown a bare gap.
  it('announces itself politely', () => {
    expect(mountNote().note.getAttribute('aria-live')).toBe('polite')
  })

  it('writes no inline style', () => {
    expect(mountNote().note.getAttribute('style')).toBeNull()
  })
})

describe('fillForward', () => {
  // Cell 0 reports every hour; cell 1 goes silent for one hour in the middle and
  // comes back. Cell 2 has nothing until the third hour.
  const GAPPY = {
    metric: 'P2',
    resolution_km: 15,
    cells: [[23.0, 42.0], [27.0, 43.0], [25.0, 41.0]],
    frames: [
      { t: '2026-09-19T00:00:00Z', v: [10, 20, null] },
      { t: '2026-09-19T01:00:00Z', v: [11, null, null] },
      { t: '2026-09-19T02:00:00Z', v: [12, 22, 30] },
    ],
  }

  it('holds a silent cell at its last reading', () => {
    const out = fillForward(GAPPY)
    expect(out.frames.map((f) => f.v[1])).toEqual([20, 20, 22])
  })

  it('marks only the held hours as carried', () => {
    const out = fillForward(GAPPY)
    expect(out.frames.map((f) => f.c[1])).toEqual([false, true, false])
  })

  it('leaves a cell that has never reported alone', () => {
    const out = fillForward(GAPPY)
    expect(out.frames.map((f) => f.v[2])).toEqual([null, null, 30])
    expect(out.frames.map((f) => f.c[2])).toEqual([false, false, false])
  })

  // Never backfills: a reading can be held over, but an hour before the cell
  // first reported is an hour nobody measured.
  it('does not fill backwards', () => {
    const out = fillForward(GAPPY)
    expect(out.frames[0].v[2]).toBeNull()
  })

  it('carries nothing for a cell that reports every hour', () => {
    const out = fillForward(GAPPY)
    expect(out.frames.map((f) => f.c[0])).toEqual([false, false, false])
  })

  it('leaves the original body untouched', () => {
    fillForward(GAPPY)
    expect(GAPPY.frames[1].v[1]).toBeNull()
    expect(GAPPY.frames[1].c).toBeUndefined()
  })

  it('keeps the geometry and the frame times', () => {
    const out = fillForward(GAPPY)
    expect(out.cells).toEqual(GAPPY.cells)
    expect(out.metric).toBe('P2')
    expect(out.resolution_km).toBe(15)
    expect(out.frames.map((f) => f.t)).toEqual(GAPPY.frames.map((f) => f.t))
  })

  it('survives a body with no frames', () => {
    expect(fillForward({ cells: [], frames: [] }).frames).toEqual([])
    expect(fillForward(undefined).frames).toEqual([])
  })

  // Zero is a reading, not an absence, so it must be held like any other.
  it('hands frameBody the carried flag per cell', () => {
    const out = fillForward(GAPPY)
    expect(frameBody(out, 1).hexes.map((h) => h.carried)).toEqual([false, true, false])
    expect(frameBody(out, 1).hexes[1].values.P2).toBe(20)
  })

  it('treats zero as a reading', () => {
    const out = fillForward({
      cells: [[23, 42]],
      frames: [{ t: 'a', v: [0] }, { t: 'b', v: [null] }],
    })
    expect(out.frames[1].v[0]).toBe(0)
    expect(out.frames[1].c[0]).toBe(true)
  })
})


// The animation ran at one speed, eight seconds for a day, which is quick if
// you are trying to follow one cell across the country.
describe('playback speed', () => {
  it('cycles through the three speeds and wraps', () => {
    expect(SPEEDS).toEqual([1, 0.5, 0.25])
    expect(nextSpeed(1)).toBe(0.5)
    expect(nextSpeed(0.5)).toBe(0.25)
    expect(nextSpeed(0.25)).toBe(1)
  })

  // A stored preference from an older build, or a hand-edited one, must not
  // leave the player on a speed it cannot cycle out of.
  it('treats an unknown speed as the default', () => {
    expect(nextSpeed(3)).toBe(DEFAULT_SPEED)
    expect(nextSpeed(null)).toBe(DEFAULT_SPEED)
    expect(nextSpeed(undefined)).toBe(DEFAULT_SPEED)
    expect(nextSpeed('0.5')).toBe(DEFAULT_SPEED)
  })

  it('opens at full speed', () => {
    expect(DEFAULT_SPEED).toBe(1)
    expect(SPEEDS[0]).toBe(DEFAULT_SPEED)
  })

  // Slower means a LONGER gap between frames. Inverting this is the one bug
  // this arithmetic can have, and it would make the slow setting the fast one.
  it('turns a speed into a frame delay', () => {
    expect(frameDelay(1)).toBe(FRAME_MS)
    expect(frameDelay(0.5)).toBe(FRAME_MS * 2)
    expect(frameDelay(0.25)).toBe(FRAME_MS * 4)
    expect(frameDelay(0.5)).toBeGreaterThan(frameDelay(1))
  })

  it('falls back to the default delay for an unknown speed', () => {
    expect(frameDelay(0)).toBe(FRAME_MS)
    expect(frameDelay(null)).toBe(FRAME_MS)
    expect(frameDelay(-1)).toBe(FRAME_MS)
  })

  it('labels each speed for the button face', () => {
    expect(speedLabel(1)).toBe('1\u00d7')
    expect(speedLabel(0.5)).toBe('0.5\u00d7')
    expect(speedLabel(0.25)).toBe('0.25\u00d7')
  })
})

// The button is text, not an icon, because "half speed" has no glyph a reader
// would recognise. It still needs a name a screen reader can read.
describe('mountPlayer speed button', () => {
  const mountSpeed = () => {
    const host = document.createElement('div')
    return mountPlayer(host, {
      label: 'Time', playLabel: 'Play', pauseLabel: 'Pause', exitLabel: 'Now', speedLabel: 'Playback speed',
    })
  }

  it('starts hidden, at full speed, named', () => {
    const ui = mountSpeed()
    expect(ui.speed.hidden).toBe(true)
    expect(ui.speed.textContent).toBe('1\u00d7')
    expect(ui.speed.getAttribute('aria-label')).toBe('Playback speed')
    expect(ui.speed.getAttribute('title')).toBe('Playback speed')
    expect(ui.speed.type).toBe('button')
  })

  // Same rule the scrubber, clock and exit follow: a speed control with no
  // animation behind it is a button that does nothing.
  it('appears and disappears with the rest of the replay controls', () => {
    const ui = mountSpeed()
    ui.show(24)
    expect(ui.speed.hidden).toBe(false)
    ui.show(0)
    expect(ui.speed.hidden).toBe(true)
  })

  it('shows the speed it was set to', () => {
    const ui = mountSpeed()
    ui.atSpeed(0.25)
    expect(ui.speed.textContent).toBe('0.25\u00d7')
    ui.atSpeed(1)
    expect(ui.speed.textContent).toBe('1\u00d7')
  })

  it('updates the aria-label to track the current speed', () => {
    const ui = mountSpeed()
    ui.atSpeed(0.5)
    const label = ui.speed.getAttribute('aria-label')
    expect(label).toContain('0.5')
  })

  it('reports a press to whoever asked', () => {
    const ui = mountSpeed()
    const presses = []
    ui.onspeed(() => presses.push(true))
    ui.speed.click()
    ui.speed.click()
    expect(presses).toHaveLength(2)
  })
})
