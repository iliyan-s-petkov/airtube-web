// A cursor over the server's frames and a way to turn one back into the hex body
// the map already draws. Nothing here fetches or paints; the island owns both.

// A deliberate duplicate of snapshot.FrameSpecs, like mapwindow.js's WindowSpecs.
export const SPANS = ['24h', '7d']

// A day passes in about eight seconds.
export const FRAME_MS = 320

export function knownSpan(name) {
  return SPANS.includes(name)
}

// spanFor follows the map's window, falling back to the day for the windows no
// animation is published for — a 400 on the first press of play is not a feature.
export function spanFor(window) {
  return knownSpan(window) ? window : SPANS[0]
}

export function timelapseURL(metric, span) {
  return `/api/v1/timelapse?metric=${encodeURIComponent(metric)}&span=${encodeURIComponent(spanFor(span))}`
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
export function mountPlayer(frame, { label, playLabel, pauseLabel, exitLabel, host = frame }, doc = document) {
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
  button.addEventListener('click', () => {
    for (const fn of toggles) fn()
  })
  slider.addEventListener('input', () => {
    for (const fn of scrubs) fn(Number(slider.value))
  })
  exit.addEventListener('click', () => {
    for (const fn of exits) fn()
  })

  root.appendChild(button)
  root.appendChild(slider)
  root.appendChild(clock)
  root.appendChild(exit)
  host.appendChild(root)

  return {
    root, button, slider, clock, exit,
    playing,
    // All three together: any one of them on screen alone reads as a bug.
    show: (count) => {
      slider.max = String(Math.max(0, count - 1))
      slider.hidden = count <= 0
      clock.hidden = count <= 0
      exit.hidden = count <= 0
    },
    at: (i, text) => {
      slider.value = String(i)
      clock.textContent = text ?? ''
    },
    ontoggle: (fn) => toggles.push(fn),
    onscrub: (fn) => scrubs.push(fn),
    onexit: (fn) => exits.push(fn),
  }
}
