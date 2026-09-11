// Network filter for the sensor tier. A filter, not a second MapLibre source:
// islands/map.js paints one source ('airbg-data') for both the area tiers and
// the sensor tier, and repaintSensors redraws from state.sensorBody without a
// refetch. Module-level $state, not viewstate.svelte.js — that file mirrors the
// URL hash and this is not in the hash. Mirrors sensorfilter.svelte.js.
// Named because the shape of a cell and of a marker now depends on one of them:
// a layer expression matching a bare 'eea' three files away from this list is a
// literal nobody would think to update.
export const CITIZEN_SOURCE = 'sensor.community'
export const OFFICIAL_SOURCE = 'eea'

export const SOURCES = [CITIZEN_SOURCE, OFFICIAL_SOURCE]

export const DEFAULT_SOURCES = SOURCES

let sources = $state(new Set(DEFAULT_SOURCES))

const listeners = new Set()

export function getSources() {
  return sources
}

export function setSourceEnabled(source, on) {
  if (!SOURCES.includes(source)) return
  const next = new Set(sources)
  if (on) next.add(source)
  else next.delete(source)
  if (next.size === sources.size) return
  sources = next
  for (const fn of listeners) fn(sources)
}

export function onSourceChange(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

// TEST-ONLY reset seam — see sensorfilter.svelte.js's own.
export function resetSourceFilterForTests() {
  sources = new Set(DEFAULT_SOURCES)
  listeners.clear()
}

// sourceOf names the network behind a hex entry or a GeoJSON feature. One
// function for both shapes because the same toggle governs both layers, and a
// payload written before the source column exists carries none — those rows are
// all sensor.community.
export function sourceOf(x) {
  return x?.source || x?.properties?.source || CITIZEN_SOURCE
}

export function filterBySource(features, enabled) {
  return features.filter((f) => enabled.has(sourceOf(f)))
}
