// localStorage, for code that must not care whether it exists.
//
// A browser in private mode, or one with site data blocked, throws on the mere
// ACCESS of globalThis.localStorage — not on the read. So the try wraps the
// property access, and everything that remembers a preference (the layer menu,
// auto-refresh) gets a working control that simply forgets, rather than a
// broken page. The same contract theme.js states for the theme.
export function safeStorage() {
  try {
    return globalThis.localStorage ?? null
  } catch {
    return null
  }
}

// readFlag/writeFlag are the one-boolean case, which is most of them. A stored
// value that is not one of the two spellings is treated as unset: an unreadable
// preference must fall back to the default, never to `false` by accident.
export function readFlag(key, fallback, storage = safeStorage()) {
  try {
    const raw = storage?.getItem(key)
    if (raw === 'true') return true
    if (raw === 'false') return false
    return fallback
  } catch {
    return fallback
  }
}

export function writeFlag(key, value, storage = safeStorage()) {
  try {
    storage?.setItem(key, String(value))
  } catch {
    /* private mode, or a full quota: the control still works this visit */
  }
}

// readChoice/writeChoice are readFlag/writeFlag for a preference that is one
// of a fixed few values rather than a boolean. It returns a member of
// `allowed`, never the stored string: localStorage holds "0.5", and a caller
// comparing that against 0.5 with === would silently never match.
export function readChoice(key, allowed, fallback, storage = safeStorage()) {
  try {
    const raw = storage?.getItem(key)
    return allowed.find((v) => String(v) === raw) ?? fallback
  } catch {
    return fallback
  }
}

export function writeChoice(key, value, storage = safeStorage()) {
  try {
    storage?.setItem(key, String(value))
  } catch {
    /* private mode, or a full quota: the control still works this visit */
  }
}
