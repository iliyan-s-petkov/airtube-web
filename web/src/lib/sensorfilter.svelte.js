// Which sensors the area map draws: all of them, only the ones reporting the
// selected metric, or only the silent ones.
//
// A shared module-level $state rather than a key in viewstate.svelte.js: that
// file is the reactive mirror of the URL HASH, and this is not in the hash.
// The registry in sensors.svelte.js is the precedent — cross-island state that
// no link needs to carry.
//
// DEFAULT 'active', as the kit's mockup has it (the radio carrying `checked`).
// This site opened on 'all' for a while, reasoning that a silent sensor is
// already painted in the no-data colour and named in the key, so hiding it
// overstates coverage. The kit governs, and it has an answer to that: the count
// line beside the control publishes the silent number on every position, so the
// sensors the default hides are still stated in words rather than dropped
// silently. A reader who wants them has one click, and the map opens showing
// only dots that mean something.
export const STATUSES = ['all', 'active', 'inactive']

// Named, not written twice: the reset seam below also has to open where the
// page opens, and two literals would let a test suite observe a default the
// browser never sees — which is exactly what a mutation of a bare $state()
// slipped past, since every assertion ran after a reset had already restored
// the other literal.
export const DEFAULT_STATUS = 'active'

let status = $state(DEFAULT_STATUS)

// Plain (non-rune) subscriber list, for the same reason viewstate has one:
// islands/map.js is deliberately plain .js, so it cannot use $effect. See that
// file's own comment.
const listeners = new Set()

export function getSensorStatus() {
  return status
}

export function setSensorStatus(next) {
  if (!STATUSES.includes(next) || next === status) return
  status = next
  for (const fn of listeners) fn(status)
}

export function onSensorStatusChange(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

// TEST-ONLY reset seam. Module state has no invalidation but a page reload in
// production; a Vitest run is one process for many `it()` blocks, and without
// this the first test to set a status decides what every later test observes.
export function resetSensorFilterForTests() {
  status = DEFAULT_STATUS
  listeners.clear()
}

// filterByStatus is the whole meaning of the control, kept pure and separate
// from both the store and the map so it can be read and tested on its own.
//
// `value === null` is the test, not falsiness: 0 µg/m³ is a reading, and a
// falsy test would file the cleanest sensor in the area under "no data".
export function filterByStatus(features, status) {
  if (status === 'active') return features.filter((f) => f.properties.value !== null)
  if (status === 'inactive') return features.filter((f) => f.properties.value === null)
  return features
}
