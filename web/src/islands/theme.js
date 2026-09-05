// The theme picker: auto / light / dark, in the masthead.
//
// The server renders an empty, hidden <details> and this island fills it. It is
// built here rather than in the template because without JavaScript the control
// cannot do anything — there is no storage to write and no attribute to toggle,
// and the OS preference already reaches the page through the kit's
// prefers-color-scheme block. A rendered-but-inert picker would be a promise
// the page cannot keep.
//
// Applying the stored choice is NOT this island's job: /static/theme-init.js
// does it before the first paint. This island only offers the choice.

// The three states, in the order the kit's mockup lists them. "auto" is the
// absence of an override, so it stores nothing — see theme-init.js.
export const THEMES = ['auto', 'light', 'dark']

export const STORAGE_KEY = 'airbg:theme'

// readTheme and writeTheme are the whole storage contract, in one place so the
// island and theme-init.js cannot disagree about what "auto" looks like on
// disk. Both swallow storage errors: a blocked localStorage is a browser that
// still gets the OS theme, not a broken page.
// The ATTRIBUTE is read first, not storage. It is what actually paints the
// page: theme-init.js has already copied storage onto it before the first
// paint, so on load the two agree — but when a write to storage fails (Safari
// private browsing, a blocked third-party context) only the attribute holds the
// reader's choice. Reading storage first made the picker snap back to "auto"
// after every choice in exactly those browsers, while the page stayed dark.
export function readTheme(root = document.documentElement, store = safeStorage()) {
  const applied = root?.dataset?.theme
  if (applied === 'light' || applied === 'dark') return applied
  let stored = null
  try {
    stored = store?.getItem(STORAGE_KEY)
  } catch {
    // A blocked store reads as "no override" — the OS preference still paints.
  }
  return stored === 'light' || stored === 'dark' ? stored : 'auto'
}

export function writeTheme(theme, root = document.documentElement, store = safeStorage()) {
  if (theme === 'auto') {
    delete root.dataset.theme
    try {
      store?.removeItem(STORAGE_KEY)
    } catch {
      // nothing to do: the attribute is already correct for this page load
    }
    return
  }
  root.dataset.theme = theme
  try {
    store?.setItem(STORAGE_KEY, theme)
  } catch {
    // as above — the choice holds for this page, just not the next one
  }
}

function safeStorage() {
  try {
    return globalThis.localStorage ?? null
  } catch {
    return null
  }
}

// Each state's icon, as the kit draws it: a half-filled circle for auto, a sun
// for light, a moon for dark. Inline SVG rather than an icon font or an <img>,
// so the mark inherits currentColor and follows the masthead's hover states.
const ICONS = Object.freeze({
  auto: '<circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M8 1.75a6.25 6.25 0 0 0 0 12.5z" fill="currentColor"/>',
  light:
    '<circle cx="8" cy="8" r="3.25" fill="currentColor"/><g stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 1v1.6M8 13.4V15M1 8h1.6M13.4 8H15M3.05 3.05l1.13 1.13M11.82 11.82l1.13 1.13M3.05 12.95l1.13-1.13M11.82 4.18l1.13-1.13"/></g>',
  dark: '<path d="M13.2 9.8A5.6 5.6 0 0 1 6.2 2.8a5.6 5.6 0 1 0 7 7z" fill="currentColor"/>',
})

function icon(theme) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  svg.setAttribute('class', 'pick-ico')
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('width', '16')
  svg.setAttribute('height', '16')
  svg.setAttribute('aria-hidden', 'true')
  svg.setAttribute('focusable', 'false')
  // innerHTML with a frozen module constant, looked up by a key this function
  // rejects unless it is one of the three literals above. Nothing served, typed
  // or stored can reach it, so there is no string for an injection to travel in.
  if (!Object.hasOwn(ICONS, theme)) throw new Error(`no icon for theme ${theme}`)
  svg.innerHTML = ICONS[theme]
  return svg
}

function mark(theme) {
  const span = document.createElement('span')
  span.className = 'langpick__mark'
  span.appendChild(icon(theme))
  return span
}

// render builds the picker's contents into `el` and returns it. Split from
// mount so a test can drive it with a plain <details> and no page around it.
//
// Labels arrive as data-* attributes rather than being written here: the
// catalogue owns every string the reader sees, and an island that hardcoded
// "Dark" would be a second place a new language has to be added.
export function render(el, current) {
  const label = el.dataset.label ?? ''
  const names = { auto: el.dataset.auto, light: el.dataset.light, dark: el.dataset.dark }

  el.replaceChildren()
  el.className = 'langpick'
  el.hidden = false

  const summary = document.createElement('summary')
  summary.className = 'langpick__btn'
  // The button shows the state's icon, not its name — the masthead is 48px of
  // horizontal budget shared with two tabs and a language picker. The name
  // survives in the accessible name, which is what the kit's §5.2a requires of
  // an icon-only control.
  summary.setAttribute('aria-label', `${label}: ${names[current]}`)
  summary.appendChild(mark(current))
  const caret = document.createElement('span')
  caret.className = 'langpick__caret'
  caret.setAttribute('aria-hidden', 'true')
  summary.appendChild(caret)
  el.appendChild(summary)

  const list = document.createElement('ul')
  list.className = 'langpick__list'
  for (const theme of THEMES) {
    const item = document.createElement('li')
    const opt = document.createElement('button')
    opt.type = 'button'
    opt.className = 'langpick__opt'
    opt.dataset.theme = theme
    if (theme === current) opt.setAttribute('aria-current', 'true')
    opt.appendChild(mark(theme))
    opt.append(names[theme] ?? theme)
    item.appendChild(opt)
    list.appendChild(item)
  }
  el.appendChild(list)
  return el
}

export function mount(el) {
  render(el, readTheme())

  // Delegated, so re-rendering after a choice does not have to rebind three
  // listeners — and so the listener survives the replaceChildren() inside
  // render().
  el.addEventListener('click', (event) => {
    const opt = event.target.closest?.('[data-theme]')
    if (!opt || !el.contains(opt)) return
    writeTheme(opt.dataset.theme)
    render(el, readTheme())
    el.open = false
  })
}
