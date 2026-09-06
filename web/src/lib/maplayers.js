// What the basemap draws — the reader's choice, not a fixed picture.
//
// The archive has carried streets, buildings, parks, schools, shops and
// transport all along; what decides whether any of it is drawn is the style's
// selection. With those layers in style.json the opposite risk arrives: a city
// at z16 carrying every shop in it is a second dataset competing with the
// readings the reader came for. So the categories are switchable, and this is
// the control — the kit's .colmenu.map__layers, top-left inside the frame.
//
// THE OPTIONS COME FROM THE STYLE, NOT FROM A LIST HERE
// Every layer in tools/basemap/style.json carries metadata["airbg:group"], and
// installLayers reads those groups off the MOUNTED style. A list of layer ids
// typed into this file would be a second thing free to drift from the style it
// claims to describe. Add a layer to the style tomorrow and it appears here
// with no change to this file. ORDER below is an ordering, not an inventory:
// a group the style does not carry is skipped, and a group with no label falls
// back to its own key — a visible gap rather than a silent omission.
//
// It is not offered when there is nothing to switch. Without tiles (no style
// URL configured, a tile error) there are no basemap layers at all, and a
// control over layers that do not exist is a dead control. The disclosure stays
// hidden until the panel actually holds an option.
//
// Icons and structure are the kit's, and nothing here writes el.style: the
// CSP's style-src has no 'unsafe-inline'.

const SVG_NS = 'http://www.w3.org/2000/svg'

export const STORAGE_KEY = 'airbg:map-layers'

// The order a reader thinks in: the ground first, then what is built on it,
// then what is inside the buildings.
export const LAYER_ORDER = [
  'base', 'water', 'roads', 'street-names', 'buildings', 'places', 'boundaries',
  'poi-education', 'poi-health', 'poi-shop', 'poi-transport', 'poi-other',
]

// The stack-of-sheets glyph, path-for-path from the kit.
function icon() {
  const svg = document.createElementNS(SVG_NS, 'svg')
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('width', '16')
  svg.setAttribute('height', '16')
  svg.setAttribute('aria-hidden', 'true')
  svg.setAttribute('focusable', 'false')
  for (const d of [
    'M8 1.75 1.75 5 8 8.25 14.25 5 8 1.75Z',
    'M1.75 8.5 8 11.75 14.25 8.5M1.75 11.75 8 15l6.25-3.25',
  ]) {
    const path = document.createElementNS(SVG_NS, 'path')
    path.setAttribute('d', d)
    path.setAttribute('fill', 'none')
    path.setAttribute('stroke', 'currentColor')
    path.setAttribute('stroke-width', '1.5')
    path.setAttribute('stroke-linejoin', 'round')
    svg.appendChild(path)
  }
  return svg
}

// A blocked localStorage is a browser that still gets a working menu, not a
// broken page — same contract theme.js states for the theme.
function safeStorage() {
  try {
    return globalThis.localStorage ?? null
  } catch {
    return null
  }
}

export function readState(storage = safeStorage()) {
  try {
    const raw = storage?.getItem(STORAGE_KEY)
    const parsed = raw ? JSON.parse(raw) : null
    // A hostile or stale value must not become a source of options: only a
    // plain object is a state, and only its booleans are read below.
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {}
  } catch {
    return {}
  }
}

export function writeState(state, storage = safeStorage()) {
  try {
    storage?.setItem(STORAGE_KEY, JSON.stringify(state))
  } catch {
    /* private mode, or a quota that is full: the menu still works this visit */
  }
}

// mountLayers builds the disclosure and owns opening and closing it. It needs
// no map — the options arrive later, from the mounted style — which is the same
// split mountZoom/installZoom uses and for the same reason: a control whose DOM
// and whose camera calls are one blob is a control no test can reach.
//
// It starts hidden. installLayers reveals it once the panel holds something.
export function mountLayers(frame, { label }, doc = document) {
  const root = document.createElement('div')
  root.className = 'colmenu map__layers'
  root.hidden = true

  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'btn btn--icon colmenu__btn'
  button.setAttribute('aria-expanded', 'false')
  button.setAttribute('aria-label', label)
  button.setAttribute('title', label)
  button.appendChild(icon())

  const panel = document.createElement('div')
  panel.className = 'colmenu__panel'
  panel.hidden = true

  const fieldset = document.createElement('fieldset')
  const caption = document.createElement('legend')
  fieldset.appendChild(caption)
  panel.appendChild(fieldset)

  // aria-controls needs an id, and two maps on one page would collide on a
  // fixed one. Derived from the frame's own id, which the templates set.
  const id = `${frame.id || 'map'}-layers-panel`
  panel.id = id
  button.setAttribute('aria-controls', id)

  // One record of one state: aria-expanded already carries whether the panel is
  // open, so nothing toggles a class beside it.
  const open = (yes) => {
    button.setAttribute('aria-expanded', String(yes))
    panel.hidden = !yes
  }
  button.addEventListener('click', () => open(button.getAttribute('aria-expanded') !== 'true'))
  // Escape closes and returns focus to the button; a click outside closes
  // without stealing it.
  root.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !panel.hidden) {
      open(false)
      button.focus()
    }
  })
  doc.addEventListener('mousedown', (e) => {
    if (!panel.hidden && !root.contains(e.target)) open(false)
  })

  root.appendChild(button)
  root.appendChild(panel)
  frame.appendChild(root)
  return { root, button, panel, fieldset, caption, open }
}

// groupsIn returns the style's groups, in LAYER_ORDER, skipping any the style
// does not carry. Exported for its own test: it is the seam that keeps this
// file from holding a second copy of the style's contents.
export function groupsIn(layers) {
  return LAYER_ORDER.filter((g) => layers.some((l) => l.metadata?.['airbg:group'] === g))
}

// installLayers fills the panel from the mounted style and wires each option to
// the camera. Called after 'load': map.getStyle() has no layers before then, and
// a menu built from an empty style is an empty menu.
//
// `views` are toggles about the SCREEN rather than about the basemap — the key,
// and the basemap as a whole. They are listed above the categories rather than
// smuggled in beside "Shops" as if they were one more kind of place, and a view
// that needs a working camera is simply not offered without one.
export function installLayers(map, ui, { labels, caption, views = [], storage }) {
  const state = readState(storage)
  const layers = map.getStyle()?.layers ?? []
  const groups = groupsIn(layers)

  ui.caption.textContent = caption
  for (const opt of [...ui.fieldset.querySelectorAll('.colmenu__opt')]) opt.remove()

  const remember = (key, on) => {
    state[key] = on
    writeState(state, storage)
  }

  // Applies held back until the categories have run — see the view loop below.
  const pending = []

  const addOption = (key, text, extraClass, apply, { defer = false } = {}) => {
    const label = document.createElement('label')
    label.className = extraClass ? `colmenu__opt ${extraClass}` : 'colmenu__opt'
    const input = document.createElement('input')
    input.type = 'checkbox'
    // Everything is on until the reader switches it off: the map they were
    // shown is the map they keep, and a stored `false` is the only thing that
    // changes it.
    input.checked = state[key] !== false
    input.setAttribute('data-layer-key', key)
    const span = document.createElement('span')
    span.textContent = text
    label.appendChild(input)
    label.appendChild(span)
    ui.fieldset.appendChild(label)

    input.addEventListener('change', () => {
      remember(key, input.checked)
      apply(input.checked)
    })
    // Applied at build time too, or a stored `false` would be a checkbox that
    // says the layer is off over a map still drawing it.
    if (defer) pending.push(() => apply(input.checked))
    else apply(input.checked)
    return input
  }

  // The view toggles are BUILT first — they belong above the categories, since
  // they are about the screen rather than about the basemap — but they are
  // APPLIED last. "Hide the basemap" and "show water" both write visibility on
  // the same layers, and whichever runs second wins: applied in DOM order, a
  // remembered basemap:false would be undone a moment later by the category
  // boxes restoring their own layers.
  for (const view of views) {
    if (view.needsMap && !layers.length) continue
    addOption(`view:${view.id}`, view.label, 'colmenu__opt--view', (on) => view.apply(on, map), { defer: true })
  }

  const setGroup = (group, on) => {
    for (const l of map.getStyle()?.layers ?? []) {
      if (l.metadata?.['airbg:group'] === group) {
        map.setLayoutProperty(l.id, 'visibility', on ? 'visible' : 'none')
      }
    }
  }
  for (const group of groups) {
    addOption(group, labels[group] || group, '', (on) => setGroup(group, on))
  }
  for (const run of pending) run()

  // Offered whenever it has anything to offer, and only then. Without tiles the
  // categories are empty but the view toggles are not — so the test is what the
  // panel actually holds, never the group count.
  ui.root.hidden = !ui.fieldset.querySelector('.colmenu__opt')
  return { groups }
}
