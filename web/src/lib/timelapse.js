// The animation: how the air moved, rather than what it is.
//
// The server prepares one body per (metric, span) — the cell list once, then a
// bare array of numbers per frame (see internal/snapshot/timelapse.go). So the
// client half is a cursor over an array and a way to turn one frame back into
// the hex body the map already knows how to draw. Nothing here fetches or
// paints; the island owns both, because both need the map.

// The spans the server publishes. A DELIBERATE DUPLICATE of snapshot.FrameSpecs,
// for the reason mapwindow.js duplicates WindowSpecs: one is Go and one is JS,
// they cannot be imported across, and a name here the server does not know is a
// 400 on the reader's first press of play.
export const SPANS = ['24h', '7d']

// How long one frame is held on screen. Fast enough that a day passes in about
// eight seconds — long enough that a reader can see each frame arrive rather
// than watching the map flicker.
export const FRAME_MS = 320

export function knownSpan(name) {
  return SPANS.includes(name)
}

/**
 * spanFor picks the animation that matches the map's averaging window.
 *
 * A reader who has set the map to a week and presses play means the week. The
 * live view and the windows we publish no animation for fall back to the day,
 * which is the shortest span and the one the map is nearest to showing.
 */
export function spanFor(window) {
  return knownSpan(window) ? window : SPANS[0]
}

export function timelapseURL(metric, span) {
  return `/api/v1/timelapse?metric=${encodeURIComponent(metric)}&span=${encodeURIComponent(spanFor(span))}`
}

/**
 * frameBody turns one frame back into the shape hexFeatures reads.
 *
 * The geometry is the body's single cell list and the numbers are positional
 * against it, so this is the only place that pairing is made — get it wrong and
 * every cell is drawn with its neighbour's reading.
 *
 * `n` is deliberately absent. A frame carries no sensor count, and a made-up 1
 * would be a popup telling the reader a cell holds one device when the server
 * never said so.
 */
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

/**
 * frameTime is the instant a frame covers, as a Date, or null.
 *
 * Null rather than an invalid Date for a body without one: the caller labels the
 * scrubber from this, and "Invalid Date" printed over the map is worse than no
 * label at all.
 */
export function frameTime(body, i) {
  const t = body?.frames?.[i]?.t
  if (!t) return null
  const d = new Date(t)
  return Number.isNaN(d.getTime()) ? null : d
}

/**
 * cursor is the playhead: which frame is showing, and what pressing play does.
 *
 * A plain object with a tick function rather than a timer, so the island owns
 * the interval and a test can step frames without waiting for wall-clock time.
 * Playing past the last frame wraps to the first — an animation of a day is
 * something a reader watches round more than once.
 */
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

/**
 * mountPlayer builds the play control, unwired: the caller registers what play,
 * pause and scrub do, because all three need the map.
 *
 * It joins the refresh pill and the window button in the bottom-left cluster,
 * for the reason the window button moved there — the map's controls belong in
 * one place at the edge, not scattered over the thing they describe.
 *
 * The scrubber is hidden until there is something to scrub. An empty slider over
 * a map that is not animating is a control with nothing behind it, and a reader
 * who drags it has been told a lie about what the map can do.
 */
export function mountPlayer(frame, { label, playLabel, pauseLabel, host = frame }, doc = document) {
  const root = doc.createElement('div')
  root.className = 'map-play'

  const button = doc.createElement('button')
  button.type = 'button'
  button.className = 'btn map-play__btn'
  button.setAttribute('aria-label', playLabel)
  button.setAttribute('title', playLabel)
  button.setAttribute('aria-pressed', 'false')
  // Icon-only, like the refresh button beside it (DESIGN.md §5.2a): the name
  // lives in title and aria-label, and the glyph is marked decorative so a
  // screen reader is not told "triangle" on top of "Play".
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
  button.addEventListener('click', () => {
    for (const fn of toggles) fn()
  })
  slider.addEventListener('input', () => {
    for (const fn of scrubs) fn(Number(slider.value))
  })

  root.appendChild(button)
  root.appendChild(slider)
  root.appendChild(clock)
  host.appendChild(root)

  return {
    root, button, slider, clock,
    playing,
    // Shown together: the scrubber and the clock only mean anything while an
    // animation is loaded, and half of the pair on screen alone reads as a bug.
    show: (count) => {
      slider.max = String(Math.max(0, count - 1))
      slider.hidden = count <= 0
      clock.hidden = count <= 0
    },
    at: (i, text) => {
      slider.value = String(i)
      clock.textContent = text ?? ''
    },
    ontoggle: (fn) => toggles.push(fn),
    onscrub: (fn) => scrubs.push(fn),
  }
}
