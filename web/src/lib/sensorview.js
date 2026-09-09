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

// detailRows describes the STATION itself, as opposed to what it is currently
// reading: which boxes stand there, what hardware they are, how long they have
// been reporting to us and where they are.
//
// "Reporting since" is deliberately not called a registration date. Upstream
// publishes no such date; what we have is the first time OUR ingest wrote the
// device down (internal/store: SensorReading.FirstSeen), and a device that has
// reported for years but only reached us in June must not be presented as new
// — the label says "in our data", and this is the only place that promise is
// kept.
//
// A row whose value is unknown is omitted rather than printed empty: an
// address with nothing to say about its hardware should show a shorter list,
// not a list of blanks.
export function detailRows(sensor, labels, locale) {
  const devices = sensor?.devices ?? []
  const rows = []

  const ids = devices.map((d) => d.id).filter((id) => id !== null && id !== undefined)
  if (ids.length) rows.push({ key: 'devices', label: labels.devices, value: ids.join(', ') })

  const types = [...new Set(devices.map((d) => d.type).filter(Boolean))]
  if (types.length) rows.push({ key: 'hardware', label: labels.hardware, value: types.join(', ') })

  // The earliest first_seen and the latest last_seen across the boxes: the
  // station has been reporting since its oldest device arrived, and it is as
  // fresh as its most recent one.
  const first = earliest(devices.map((d) => d.firstSeen))
  if (first) rows.push({ key: 'since', label: labels.since, value: formatDate(first, locale) })

  const last = latest(devices.map((d) => d.lastSeen))
  if (last) rows.push({ key: 'updated', label: labels.updated, value: formatDateTime(last, locale) })

  if (Number.isFinite(sensor?.lat) && Number.isFinite(sensor?.lon)) {
    rows.push({
      key: 'coords',
      label: labels.coords,
      value: `${sensor.lat.toFixed(4)}, ${sensor.lon.toFixed(4)}`,
    })
  }

  return rows
}

// stationMeta lists an official station's EEA classification: EoI code,
// station type (background/traffic/industrial) and area type (urban/
// suburban/rural). Empty for a citizen device — 'eea' is the only source
// that carries these columns (internal/snapshot/build.go's sensorPayload).
export function stationMeta(sensor, labels) {
  if (sensor?.source !== 'eea') return []
  const rows = []
  if (sensor.stationCode) rows.push({ key: 'code', label: labels.code, value: sensor.stationCode })
  if (sensor.stationType) rows.push({ key: 'type', label: labels.type, value: sensor.stationType })
  if (sensor.stationArea) rows.push({ key: 'area', label: labels.area, value: sensor.stationArea })
  return rows
}

// networkText names which network published the reading, for every sensor
// — not only official stations. Falls back to 'sensor.community' for a
// sensor projected before the source column existed on the wire.
export function networkText(sensor, label) {
  if (!sensor) return ''
  return `${label}: ${sensor.source || 'sensor.community'}`
}

function stamps(list) {
  return list.map((s) => (s ? Date.parse(s) : NaN)).filter((n) => Number.isFinite(n))
}

function earliest(list) {
  const times = stamps(list)
  return times.length ? new Date(Math.min(...times)) : null
}

function latest(list) {
  const times = stamps(list)
  return times.length ? new Date(Math.max(...times)) : null
}

function formatDate(date, locale) {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(date)
}

function formatDateTime(date, locale) {
  return new Intl.DateTimeFormat(locale, { dateStyle: 'short', timeStyle: 'short' }).format(date)
}
