// Network filter for the sensor tier. A filter, not a second MapLibre source:
// islands/map.js paints one source ('airbg-data') for both the area tiers and
// the sensor tier, and repaintSensors redraws from state.sensorBody without a
// refetch. Module-level $state, not viewstate.svelte.js — that file mirrors the
// URL hash and this is not in the hash. Mirrors sensorfilter.svelte.js.
export const SOURCES = ['sensor.community', 'eea']

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
  return x?.source || x?.properties?.source || 'sensor.community'
}

export function filterBySource(features, enabled) {
  return features.filter((f) => enabled.has(sourceOf(f)))
}

// Metric coverage per network as of 2026-09-09. A metric in neither set is
// treated as measured by both, so a metric added server-side does not blank a
// layer until this table is updated.
const MEASURED = {
  'sensor.community': new Set([
    'P1', 'P2', 'temperature', 'humidity', 'pressure', 'noise_LAeq', 'noise_LA_max',
  ]),
  eea: new Set(['P1', 'P2', 'SO2', 'O3', 'NO2', 'NOX', 'CO', 'C6H6']),
}

// measuredBy reports whether source has any data for metric; the layer control
// disables the checkbox when it does not.
export function measuredBy(source, metric) {
  const known = new Set([...MEASURED['sensor.community'], ...MEASURED.eea])
  if (!known.has(metric)) return true
  return MEASURED[source]?.has(metric) === true
}
