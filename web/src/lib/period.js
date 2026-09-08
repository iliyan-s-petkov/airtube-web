// The window a chart asks for, expressed as query parameters. A named period is
// its own name; a custom one is two instants, which the API takes in RFC 3339.
export const CUSTOM = 'custom'

// A datetime-local input yields "2026-09-07T14:00" in the READER's zone. Date
// parses that as local time and toISOString states the offset, which is what the
// API requires — sending the naive string would be read as UTC there and shift
// the chart by the reader's own offset.
export function toInstant(local) {
  if (!local) return null
  const t = new Date(local)
  return Number.isNaN(t.getTime()) ? null : t.toISOString()
}

// The inverse of toInstant: an instant as the naive local string a
// datetime-local input takes. toISOString cannot be used — it states UTC, and
// the input would read it as wall clock and land the reader's own offset away
// from the moment they asked for.
export function localNow(date = new Date()) {
  const pad = (n) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
    `T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function customRangeValid(from, to) {
  const a = toInstant(from)
  const b = toInstant(to)
  return a !== null && b !== null && a < b
}

// null for a custom range that is not yet usable — half filled in, or backwards.
// The caller draws its "choose a range" message instead of requesting a window
// the API would refuse.
export function periodQuery(period, from, to) {
  if (period !== CUSTOM) return `period=${encodeURIComponent(period)}`
  if (!customRangeValid(from, to)) return null
  return `period=${CUSTOM}` +
    `&from=${encodeURIComponent(toInstant(from))}` +
    `&to=${encodeURIComponent(toInstant(to))}`
}
