import { unitFor } from './metrics.js'

// Rows follow the SWITCHER's order, not the object key order: JS object key
// order is insertion order, which here is the server's JSON order — stable
// today, and not something the panel's layout should depend on.
//
// A metric absent from sensor.values is not reported by this sensor and is
// omitted. A metric present with a null value IS reported and is kept, marked
// missing — "no reading right now" and "does not measure this" are different
// facts about the hardware.
//
// sensor.values is a plain object keyed by metric name (e.g. { P1: 30, P2:
// 12 }), assembled by the caller from the wire response. The wire response
// itself (internal/snapshot/build.go's sensorPayload) is COLUMNAR — one array
// per metric, all sensors sharing an index — because that shape is what
// MapLibre's feature properties and typed arrays want (see sensorFeatures in
// web/src/islands/map.js). Reshaping one sensor's columns into this per-sensor
// object is the caller's job (the click-handler wiring, not this task); this
// function only ever sees the single-sensor projection.
export function panelRows(sensor, options, scales) {
  const values = sensor?.values ?? {}
  return options
    .filter(({ metric }) => Object.hasOwn(values, metric))
    .map(({ metric, label }) => ({
      metric,
      label,
      value: values[metric] ?? null,
      unit: unitFor(scales, metric),
      missing: values[metric] === null || values[metric] === undefined,
    }))
}
