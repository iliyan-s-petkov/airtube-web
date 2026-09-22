// A cursor over the server's frames and a way to turn one back into the hex body
// the map already draws. Nothing here fetches or paints; the island owns both.
import contract from './contract.json'

// Sourced from contract.json, generated from snapshot.FrameSpecs.
export const SPANS = contract.spans.map((s) => s.span)

// A day passes in about eight seconds at full speed.
export const FRAME_MS = 320

// Eight seconds for a day is quick if you are following one cell, so the
// player can be slowed. Multipliers rather than millisecond values: FRAME_MS
// stays the one place the base rate is written down.
export const SPEEDS = [1, 0.5, 0.25]
export const DEFAULT_SPEED = SPEEDS[0]

// Unknown speeds reset rather than pass through: a preference stored by an
// older build, or edited by hand, must not leave the button on a value it can
// never cycle out of. indexOf gives -1 for those, and -1 + 1 is the default's
// own index — so the reset needs no branch of its own.
export function nextSpeed(speed) {
  return SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length]
}

// Slower is a LONGER gap between frames. Dividing the other way round would
// make the slow setting the fast one.
export function frameDelay(speed) {
  return SPEEDS.includes(speed) ? FRAME_MS / speed : FRAME_MS
}

export function speedLabel(speed) {
  return `${speed}\u00d7`
}

export function knownSpan(name) {
  return SPANS.includes(name)
}

// spanFor follows the map's window, falling back to the day for the windows no
// animation is published for — a 400 on the first press of play is not a feature.
export function spanFor(window) {
  return knownSpan(window) ? window : SPANS[0]
}

// resolutionKm is the wanted cell size; the server snaps it onto a published
// replay tier, which is a subset of the hex tiers (see hexes.js). Omitted, a
// caller gets today's URL unchanged, so existing callers and cached URLs are
// unaffected.
export function timelapseURL(metric, span, resolutionKm) {
  const params = new URLSearchParams({ metric, span: spanFor(span) })
  if (typeof resolutionKm === 'number' && Number.isFinite(resolutionKm)) {
    params.set('resolution_km', String(round(resolutionKm, 4)))
  }
  return `/api/v1/timelapse?${params}`
}

function round(v, places) {
  const f = 10 ** places
  return Math.round(v * f) / f
}

// The one place the cell list and a frame's numbers are paired. No `n`: a frame
// carries no sensor count, and a made-up one would be a popup saying so.
export function frameBody(body, i) {
  const frame = body?.frames?.[i]
  if (!frame) return { resolution_km: body?.resolution_km, hexes: [] }
  const cells = body.cells ?? []
  return {
    resolution_km: body.resolution_km,
    hexes: cells.map(([lon, lat], j) => ({
      lon,
      lat,
      values: { [body.metric]: frame.v?.[j] ?? null },
      carried: frame.c?.[j] === true,
    })),
  }
}

export function frameCount(body) {
  return body?.frames?.length ?? 0
}

// Null rather than an invalid Date: this labels the scrubber, and "Invalid Date"
// over the map is worse than no label.
export function frameTime(body, i) {
  const t = body?.frames?.[i]?.t
  if (!t) return null
  const d = new Date(t)
  return Number.isNaN(d.getTime()) ? null : d
}

// The playhead. No timer of its own — the island owns the interval — and it
// wraps, because an animation is watched round.
export function cursor(count) {
  return { i: 0, count, playing: false }
}

export function step(c) {
  if (c.count <= 0) return 0
  c.i = (c.i + 1) % c.count
  return c.i
}

export function seek(c, i) {
  if (c.count <= 0) return 0
  c.i = Math.min(Math.max(0, Math.trunc(i) || 0), c.count - 1)
  return c.i
}

// The play control, unwired: the caller says what play, pause, scrub and exit
// do, because all four need the map. The scrubber stays hidden until there is
// something to scrub.
export function mountPlayer(frame, { label, playLabel, pauseLabel, exitLabel, speedLabel: speedName, host = frame }, doc = document) {
  const root = doc.createElement('div')
  root.className = 'map-play'

  const button = doc.createElement('button')
  button.type = 'button'
  button.className = 'btn map-play__btn'
  button.setAttribute('aria-label', playLabel)
  button.setAttribute('title', playLabel)
  button.setAttribute('aria-pressed', 'false')
  // Icon-only, like the refresh button beside it (DESIGN.md §5.2a).
  const glyph = doc.createElementNS('http://www.w3.org/2000/svg', 'svg')
  glyph.setAttribute('viewBox', '0 0 16 16')
  glyph.setAttribute('width', '14')
  glyph.setAttribute('height', '14')
  glyph.setAttribute('aria-hidden', 'true')
  glyph.setAttribute('focusable', 'false')
  const path = doc.createElementNS('http://www.w3.org/2000/svg', 'path')
  path.setAttribute('fill', 'currentColor')
  button.appendChild(glyph)
  glyph.appendChild(path)

  const slider = doc.createElement('input')
  slider.type = 'range'
  slider.className = 'map-play__scrub'
  slider.min = '0'
  slider.max = '0'
  slider.value = '0'
  slider.step = '1'
  slider.setAttribute('aria-label', label)
  slider.hidden = true

  const clock = doc.createElement('span')
  clock.className = 'map-play__clock'
  clock.hidden = true

  // What the clock cannot say: that the hour on screen is thinner than the rest,
  // or that there is no history to play. Live, because it appears mid-animation.
  const note = doc.createElement('span')
  note.className = 'map-play__note'
  note.setAttribute('aria-live', 'polite')
  note.hidden = true

  // Text, not an icon: "half speed" has no glyph a reader would recognise, and
  // the current speed has to be readable without pressing anything to find out.
  const speed = doc.createElement('button')
  speed.type = 'button'
  speed.className = 'btn map-play__btn map-play__speed'
  speed.setAttribute('aria-label', speedName)
  speed.setAttribute('title', speedName)
  speed.textContent = speedLabel(DEFAULT_SPEED)
  speed.hidden = true

  // Its own button rather than a second meaning for the play button: pressing
  // play from a scrubbed frame replays, so without this there is no control that
  // returns the map to the live readings.
  const exit = doc.createElement('button')
  exit.type = 'button'
  exit.className = 'btn map-play__btn map-play__exit'
  exit.setAttribute('aria-label', exitLabel)
  exit.setAttribute('title', exitLabel)
  exit.hidden = true
  const exitGlyph = doc.createElementNS('http://www.w3.org/2000/svg', 'svg')
  exitGlyph.setAttribute('viewBox', '0 0 16 16')
  exitGlyph.setAttribute('width', '14')
  exitGlyph.setAttribute('height', '14')
  exitGlyph.setAttribute('aria-hidden', 'true')
  exitGlyph.setAttribute('focusable', 'false')
  const exitPath = doc.createElementNS('http://www.w3.org/2000/svg', 'path')
  exitPath.setAttribute('fill', 'currentColor')
  exitPath.setAttribute('d', 'M4.4 3.5 8 7.1l3.6-3.6 1 1L9 8.1l3.6 3.6-1 1L8 9.1l-3.6 3.6-1-1L7 8.1 3.4 4.5z')
  exit.appendChild(exitGlyph)
  exitGlyph.appendChild(exitPath)

  const PLAY = 'M5 3.5v9l7-4.5z'
  const PAUSE = 'M4.5 3.5h3v9h-3zM8.5 3.5h3v9h-3z'
  path.setAttribute('d', PLAY)

  const playing = (yes) => {
    path.setAttribute('d', yes ? PAUSE : PLAY)
    button.setAttribute('aria-pressed', String(yes))
    const name = yes ? pauseLabel : playLabel
    button.setAttribute('aria-label', name)
    button.setAttribute('title', name)
  }

  const toggles = []
  const scrubs = []
  const exits = []
  const speeds = []
  speed.addEventListener('click', () => {
    for (const fn of speeds) fn()
  })
  button.addEventListener('click', () => {
    for (const fn of toggles) fn()
  })
  slider.addEventListener('input', () => {
    for (const fn of scrubs) fn(Number(slider.value))
  })
  exit.addEventListener('click', () => {
    // Collapse on the press: the island's exit handler is async, and the row
    // must not stay open across a network round trip.
    root.classList.remove('map-play--open')
    for (const fn of exits) fn()
  })

  root.appendChild(button)
  root.appendChild(slider)
  root.appendChild(clock)
  root.appendChild(note)
  root.appendChild(speed)
  root.appendChild(exit)
  host.appendChild(root)

  return {
    root, button, slider, clock, note, speed, exit,
    playing,
    say: (text) => {
      note.textContent = text ?? ''
      note.hidden = !text
    },
    // All three together: any one of them on screen alone reads as a bug.
    // The class says the same thing to the sheet, which folds the whole row
    // behind the play button on a phone.
    show: (count) => {
      slider.max = String(Math.max(0, count - 1))
      slider.hidden = count <= 0
      clock.hidden = count <= 0
      speed.hidden = count <= 0
      exit.hidden = count <= 0
      root.classList.toggle('map-play--open', count > 0)
    },
    at: (i, text) => {
      slider.value = String(i)
      clock.textContent = text ?? ''
    },
    atSpeed: (s) => {
      speed.textContent = speedLabel(s)
      speed.setAttribute('aria-label', `${speedName}, ${speedLabel(s)}`)
    },
    ontoggle: (fn) => toggles.push(fn),
    onspeed: (fn) => speeds.push(fn),
    onscrub: (fn) => scrubs.push(fn),
    onexit: (fn) => exits.push(fn),
  }
}

// A cell that goes silent for an hour keeps its no-data colour and drops its
// digit, so an animation of a network with ~6% intermittent cells reads as
// numbers blinking on and off at random rather than as gaps. fillForward holds
// each cell at its last reading instead, and flags the held hours in a parallel
// `c` array so they can be drawn muted — a held number is not a measured one.
//
// Forward only: a reading can be held over, but an hour before the cell first
// reported is an hour nobody measured, and backfilling it would invent one.
export function fillForward(body) {
  const frames = body?.frames ?? []
  const last = []
  return {
    ...body,
    frames: frames.map((f) => {
      const vs = f?.v ?? []
      const v = []
      const c = []
      vs.forEach((value, j) => {
        const held = value === null || value === undefined
        if (!held) last[j] = value
        const carried = held && last[j] !== undefined
        v.push(carried ? last[j] : (held ? null : value))
        c.push(carried)
      })
      return { ...f, v, c }
    }),
  }
}

// A frame is thin below a quarter of the best hour in the same body. Relative,
// not absolute: the networks differ by two orders of magnitude in size, so an
// absolute floor would caption every hour of a small-but-complete network.
export const THIN_COVERAGE = 0.25

// Cells per frame that actually carry a reading. Not a sensor count — the
// payload carries none — so this measures breadth of the map, not depth.
export function frameCoverage(body) {
  return (body?.frames ?? []).map(
    (f) => (f?.v ?? []).reduce((n, v) => n + (v === null || v === undefined ? 0 : 1), 0),
  )
}

// Whether this body is worth animating at all. A metric with no history plays
// as a blank country under a running clock, which reads as clean air.
export function hasHistory(body) {
  return frameCoverage(body).some((n) => n > 0)
}

// The frames to caption as partial, judged against the body's own best hour.
// A body with no history yields nothing here — its best hour is 0, so no frame
// is under a quarter of it — and hasHistory speaks for that body instead.
export function thinFrames(body) {
  const cov = frameCoverage(body)
  const floor = Math.max(0, ...cov) * THIN_COVERAGE
  const thin = new Set()
  cov.forEach((n, i) => {
    if (n < floor) thin.add(i)
  })
  return thin
}
