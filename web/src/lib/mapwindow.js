// The averaging window: which question the map is answering.
//
// "What is the air like right now" is one reading from each sensor, and it is
// what every map on this site showed until now. It is also the reading a single
// bad hour, a passing lorry or one dropped device can swing. "What has it been
// like lately" is the other question, and the server answers it from the same
// endpoints with a ?window= parameter (see internal/snapshot/window.go) — same
// tiers, same wire shape, only the numbers differ.
//
// So the client half is small on purpose: a name, a URL, and a remembered
// choice. Everything that decides anything here is a pure function, because the
// map island that calls them needs a real MapLibre instance and is therefore
// out of a test's reach.

import { safeStorage } from './storage.js'

export const WINDOW_STORAGE_KEY = 'airbg:map-window'

// The live view, and the empty name that asks for it. Empty so the default
// view's URL carries no parameter at all — the front page's canonical address
// is unchanged by this feature, and every cached copy of it stays valid.
export const LIVE_WINDOW = ''

// The windows the server publishes, coarsest last. A DELIBERATE DUPLICATE of
// snapshot.WindowSpecs, for the reason hexes.js duplicates the projection
// constants: one is Go and one is JS, they cannot be imported across, and they
// must agree. A name here the server does not know is a 400 on every request the
// reader makes after picking it.
export const WINDOWS = ['24h', '48h', '7d']

// The whole choice, in the order the selector offers it: now first, then further
// back. Exported because the label list is positional against it.
export const WINDOW_CHOICES = [LIVE_WINDOW, ...WINDOWS]

// What the live option is called when the server sent no label for it. Every
// other option falls back to its own wire name; this one has none, being the
// empty string, and an option with no text is an option nobody can pick.
const LIVE_FALLBACK = 'live'

export function knownWindow(name) {
  return name === LIVE_WINDOW || WINDOWS.includes(name)
}

// A stored name that is no longer published — this list changed, or the value
// was never ours — is treated as unset. Falling back to live is the safe
// direction: it is the view with no parameter, so it cannot 400.
export function readWindow(storage = safeStorage()) {
  try {
    const raw = storage?.getItem(WINDOW_STORAGE_KEY)
    return knownWindow(raw) ? raw : LIVE_WINDOW
  } catch {
    return LIVE_WINDOW
  }
}

export function writeWindow(name, storage = safeStorage()) {
  try {
    storage?.setItem(WINDOW_STORAGE_KEY, name)
  } catch {
    /* private mode, or a full quota: the selector still works this visit */
  }
}

/**
 * withWindow puts the reader's choice on a request.
 *
 * The live view adds nothing, so a map nobody has touched the selector on makes
 * exactly the requests it made before this feature existed. An unknown name adds
 * nothing either: the server would answer 400, and a URL we know is refused is
 * not worth sending — the reader gets the live map instead of an error banner.
 */
export function withWindow(url, name) {
  if (!knownWindow(name) || name === LIVE_WINDOW) return url
  return `${url}${url.includes('?') ? '&' : '?'}window=${encodeURIComponent(name)}`
}

/**
 * windowOptions zips the server's label list onto WINDOW_CHOICES.
 *
 * Positional, like every other label attribute on the map island (see
 * metrics.js's zipLabels), and a missing label falls back to the wire name
 * rather than to English typed here: the copy is Go's, and a second catalogue in
 * this file would drift on the first edit.
 */
export function windowOptions(labels = []) {
  return WINDOW_CHOICES.map((value, i) => ({
    value,
    text: labels[i] || value || LIVE_FALLBACK,
  }))
}

/**
 * chooseWindow records a pick, and reports whether anything changed.
 *
 * This is the whole decision behind the selector's change event: the handler
 * around it reloads the map, which needs a camera and a network. Re-picking the
 * current window reports false so that a change event the browser fires for a
 * value that did not move costs no requests.
 */
export function chooseWindow(state, name) {
  if (!knownWindow(name) || name === state.window) return false
  state.window = name
  writeWindow(name)
  return true
}

/**
 * mountWindow builds the selector, unwired: the caller owns what a pick does,
 * because that needs the map.
 *
 * A <select> rather than a row of buttons, and top-centre rather than in the
 * layers menu. The menu is checkboxes — things that are on or off, independently
 * — and the window is one choice among four, which is the one shape a checkbox
 * list cannot express. Top-centre because the other three corners are taken (the
 * key, the layers button, the zoom stack and the find-me button), and because
 * this control changes what every number on the map MEANS: it is not furniture.
 *
 * Labelled by aria-label and title only. The option texts say what they are —
 * "Now", "Last 24 hours" — so a visible caption beside them would be a second
 * word for the same fact, on a control that has to fit across a phone.
 */
export function mountWindow(frame, { label, options, value }) {
  const select = document.createElement('select')
  select.className = 'map-window'
  select.setAttribute('aria-label', label)
  select.setAttribute('title', label)
  for (const opt of options) {
    const el = document.createElement('option')
    el.value = opt.value
    el.textContent = opt.text
    select.appendChild(el)
  }
  select.value = value
  frame.appendChild(select)
  return select
}
