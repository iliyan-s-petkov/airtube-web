// How old the numbers on screen are, and what to say about it.
//
// Every rule that does not need a browser lives here; the reactive state and
// the timer are in freshness.svelte.js. The split is the same one viewstate
// makes, and for the same reason: a rune only compiles in a .svelte.js file,
// and the decisions are worth testing without one.

export const AUTO_KEY = 'airbg:auto-refresh'

// Five minutes. The network's own cadence is "every few minutes" (the footer
// says so), so anything faster asks the origin for numbers that have not
// changed. Anything much slower and a wall display drifts far enough that the
// timestamp beside it stops being reassurance and starts being a warning.
export const AUTO_INTERVAL_MS = 5 * 60 * 1000

// statusText is the whole of what the freshness line says, in one place.
//
// The three states are not decorations of one another: "refreshing" is a
// promise about the near future, a time is a fact about the past, and a
// failure is neither. An absence is stated plainly rather than dressed as an
// error (DESIGN.md §2.3) — a page that has never loaded says nothing at all
// rather than announcing that it has no time to show.
export function statusText({ busy, at, failed }, t, lang = 'bg') {
  if (busy) return t.loading
  if (failed) return t.failed
  if (at == null) return ''
  return `${t.updated} ${formatTime(at, lang)}`
}

// The clock the reader's own locale writes, not a fixed HH:MM: 24-hour in
// Bulgarian, and whatever English asks for where English is read.
export function formatTime(at, lang = 'bg') {
  return new Date(at).toLocaleTimeString(lang, { hour: '2-digit', minute: '2-digit' })
}
