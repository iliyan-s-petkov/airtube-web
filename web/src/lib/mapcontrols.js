// The map's own controls: full screen, and the three-button zoom stack.
//
// Both are the kit's (.map__full and .map-zoom in components.css), and both go
// INSIDE the .map frame rather than on the shell — unlike the key, they are
// furniture on the canvas and stay over it at every width.
//
// They live here rather than in islands/map.js for one reason: neither needs a
// MapLibre instance to be built, and only the zoom stack needs one to be wired.
// That split is what makes them testable — the "no jsdom for MapLibre" rule
// puts a real map out of reach, so a control whose DOM and whose camera calls
// are one blob is a control no test can reach.
//
// Icons are inline SVG in currentColor: no icon font, no second asset, and no
// inline style — the CSP's style-src has no 'unsafe-inline', so every SVG here
// paints from presentation attributes and CSS classes only.

const SVG_NS = 'http://www.w3.org/2000/svg'

// The kit's glyphs, copied path-for-path from ui_kits/app. The zoom-out mark is
// deliberately a bare minus, and the reset mark is a DIVIDED TERRITORY rather
// than the corner brackets a "fit" button wants — those brackets are the full
// screen control's own glyph, and the two buttons sit 8px apart in the same
// column.
const GLYPHS = {
  fullIn: ['M1.75 5.75v-4h4M14.25 5.75v-4h-4M1.75 10.25v4h4M14.25 10.25v4h-4'],
  fullOut: ['M5.75 1.75v4h-4M10.25 1.75v4h4M5.75 14.25v-4h-4M10.25 14.25v-4h4'],
  in: ['M8 3.25v9.5', 'M3.25 8h9.5'],
  out: ['M3.25 8h9.5'],
  reset: ['M1.75 3.25h12.5v9.5H1.75z', 'M8 3.25v9.5', 'M1.75 8h12.5'],
  // The crosshair every map uses for "where am I": a ring with four ticks
  // breaking out of it. Not a pin — a pin is a place someone chose, and this
  // button is about the reader's own position.
  locate: [
    'M8 3.25a4.75 4.75 0 1 0 0 9.5 4.75 4.75 0 0 0 0-9.5Z',
    'M8 .75v2M8 13.25v2M.75 8h2M13.25 8h2',
  ],
}

function icon(paths, className) {
  const svg = document.createElementNS(SVG_NS, 'svg')
  svg.setAttribute('class', className)
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('width', '16')
  svg.setAttribute('height', '16')
  svg.setAttribute('aria-hidden', 'true')
  // Alongside aria-hidden: focusable="false" is what keeps IE-era and some
  // current engines from putting a decorative SVG in the tab order inside a
  // button that is already focusable.
  svg.setAttribute('focusable', 'false')
  for (const d of paths) {
    const path = document.createElementNS(SVG_NS, 'path')
    path.setAttribute('d', d)
    path.setAttribute('fill', 'none')
    path.setAttribute('stroke', 'currentColor')
    path.setAttribute('stroke-width', '1.5')
    path.setAttribute('stroke-linecap', 'square')
    svg.appendChild(path)
  }
  return svg
}

// mountFullscreen builds the control AND wires it, because it drives the
// element itself rather than the camera — there is nothing here for the caller
// to connect.
//
// The native Fullscreen API is the mechanism, not an in-page imitation: the
// browser owns Escape and the exit affordance, and nothing written here can
// match that. It can still be refused — an iframe without allowfullscreen, an
// embedded preview, Safari on iPhone — so a refusal falls back to the kit's
// .map--faux-full, which pins the frame over the viewport. The control works or
// it says why; it never sits there doing nothing.
//
// `doc` is injectable so a test can drive both the granted and the refused
// branch: jsdom implements neither requestFullscreen nor fullscreenElement.
// onChange is called with the new state on every enter, exit and
// fullscreenchange, including once at mount. Real fullscreen renders the frame
// and nothing else, so anything useful anchored OUTSIDE it — the key, above
// all — disappears the moment a reader goes full screen. This is how the
// caller finds out in time to move it.
export function mountFullscreen(frame, { label, exitLabel, onChange }, doc = document) {
  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'map__full'
  button.setAttribute('aria-pressed', 'false')
  button.appendChild(icon(GLYPHS.fullIn, 'map__full-ico map__full-ico--in'))
  button.appendChild(icon(GLYPHS.fullOut, 'map__full-ico map__full-ico--out'))

  const isFull = () => doc.fullscreenElement === frame || frame.classList.contains('map--faux-full')

  // The NAME says what the next click will do; aria-pressed reports the state.
  // They are opposites on purpose, and both are required — an icon-only control
  // still has to be announced as something.
  const paint = () => {
    const on = isFull()
    button.setAttribute('aria-pressed', String(on))
    const name = on ? exitLabel : label
    button.setAttribute('aria-label', name)
    button.setAttribute('title', name)
    onChange?.(on)
  }

  const faux = () => {
    frame.classList.add('map--faux-full')
    doc.body.classList.add('has-faux-full')
    paint()
  }

  const exit = () => {
    if (doc.fullscreenElement === frame && doc.exitFullscreen) doc.exitFullscreen()
    frame.classList.remove('map--faux-full')
    doc.body.classList.remove('has-faux-full')
    paint()
  }

  const enter = () => {
    if (frame.requestFullscreen) frame.requestFullscreen().then(paint, faux)
    else faux()
  }

  button.addEventListener('click', () => (isFull() ? exit() : enter()))
  // The browser handles Escape in real fullscreen. The fallback is an ordinary
  // element with a class on it, so it has to handle its own.
  doc.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && frame.classList.contains('map--faux-full')) exit()
  })
  doc.addEventListener('fullscreenchange', paint)

  paint()
  frame.appendChild(button)
  return button
}

// mountZoom builds the stack and returns its three buttons unwired. The camera
// arrives later — mount() constructs the MapLibre map after mountChrome has
// already run — so behaviour is installZoom's job, below.
//
// Three buttons, not two. Plus and minus are obvious; the third is the way
// back. Reconstructing the opening view by clicking minus the right number of
// times is not a way back, it is a guess the reader has to make.
export function mountZoom(frame, { inLabel, outLabel, resetLabel }) {
  const el = document.createElement('div')
  el.className = 'map-zoom'
  const buttons = {}
  const spec = [['in', inLabel], ['out', outLabel], ['reset', resetLabel]]
  for (const [act, name] of spec) {
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'map-zoom__btn'
    button.setAttribute('data-act', act)
    button.setAttribute('aria-label', name)
    button.setAttribute('title', name)
    button.appendChild(icon(GLYPHS[act], 'map-zoom__ico'))
    el.appendChild(button)
    buttons[act] = button
  }
  frame.appendChild(el)
  return { el, buttons }
}

// mountLocate builds the find-me button, unwired: the click handler needs the
// area list and the camera, neither of which exists when the chrome is built.
//
// Icon-only, and a sibling of the attribution's own (i) rather than a word
// sitting on top of it. It used to be a text button pinned to the same corner
// MapLibre puts the attribution in, so the two overlapped: the reader saw one
// control, clicked what looked like the middle of it, and got whichever was on
// top. Two buttons of the same size, side by side, are two controls.
export function mountLocate(frame, { label }) {
  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'map-locate'
  button.setAttribute('aria-label', label)
  button.setAttribute('title', label)
  button.appendChild(icon(GLYPHS.locate, 'map-locate__ico'))
  frame.appendChild(button)
  return button
}

// installZoom connects the stack to a camera.
//
// The buttons own no arithmetic and hold no copy of the zoom: they call
// MapLibre and then read back what it did. A second record of the zoom here
// would be a second thing free to disagree with what is actually drawn.
//
// `home` is the view the page opened at — the country fit on /, the area's own
// centre on /area/{slug} — which is why reset needs no branch on which page it
// is: both are cfg.lon/lat/zoom, server-rendered.
//
// Reset is never disabled. The camera can always be returned, including from
// the home view itself after a pan, and a disabled state that only tracked the
// ZOOM would lie about that.
export function installZoom(map, buttons, home) {
  const paint = () => {
    const z = map.getZoom()
    // A hair of slack: MapLibre's own zoomIn lands on getMaxZoom() with the
    // float error of however many steps got it there, and an exact comparison
    // leaves the button live at the ceiling.
    buttons.out.disabled = z <= map.getMinZoom() + 1e-6
    buttons.in.disabled = z >= map.getMaxZoom() - 1e-6
  }

  buttons.in.addEventListener('click', () => map.zoomIn())
  buttons.out.addEventListener('click', () => map.zoomOut())
  // flyTo, not jumpTo: this one IS a deliberate click, so the flight explains
  // where the reader was taken. locateVisitor's jump is the opposite case — a
  // move nobody asked for.
  buttons.reset.addEventListener('click', () => map.flyTo({ center: home.centre, zoom: home.zoom }))

  // 'zoom', not 'zoomend': the buttons must go dead at the limit during the
  // animation, not a beat after it, or a held click keeps firing past the stop.
  map.on('zoom', paint)
  paint()
  return paint
}
