// A station is a place with sensors on it. The wire talks about devices.
//
// Upstream publishes one address as SEVERAL sensor ids: the particulate box
// (SDS011, SPS30, PMS5003) and the climate box (BME280, SHT3x) are separate
// devices with separate ids and the same published coordinate, each reporting
// only what its own hardware measures. Nationwide that is 259 devices at 151
// addresses. Drawn one marker per device, the climate box lands exactly under
// the particulate box where nothing can click it, and the particulate box's
// panel answers "no reading" for a temperature being measured a metre away.
//
// The payload keeps saying what each device measured — that is the honest
// record, and it is what the per-sensor chart endpoints are keyed by — and
// carries a `station` column joining the devices at one address (see
// internal/snapshot/build.go's stationIDs). This module is the client half:
// everything the reader sees as one thing on the map is grouped here, once,
// so the markers, the count line and the panel cannot disagree about how many
// there are.

// The columns every sensor row carries that are NOT a metric reading:
//
//   id       - the sensor's identity, not a reading
//   type     - the hardware model, not a reading
//   lon/lat  - the sensor's location, not a reading
//   quality  - the sensor's own data-quality flag, exposed to the panel as
//              `flag` because it is metadata ABOUT the readings
//   station  - the address join key, this file's own subject
//   measures - what each device's hardware measures, see measuresAt
//   first_seen / last_seen
//            - the device's lifetime as our ingest saw it, metadata about the
//              readings rather than one of them
//   source   - which network the row came from, "sensor.community" or "eea"
//
// Every other key in the columnar body is a canonical metric column
// (upstream.CanonicalMetrics, internal/snapshot/build.go). Deriving the metric
// list by exclusion from this fixed list — rather than an allow-list of known
// metrics — is what lets a metric added server-side reach the panel with no
// frontend change.
export const META_COLUMNS = new Set([
  'id', 'type', 'lon', 'lat', 'quality', 'station', 'measures', 'first_seen', 'last_seen',
  'source', 'station_code', 'station_name', 'station_type', 'station_area',
])

// metricColumnsOf is every metric the response carries a column for, in the
// server's own order.
export function metricColumnsOf(body) {
  return Object.keys(body?.sensors ?? {}).filter((key) => !META_COLUMNS.has(key))
}

// measuresAt is what is MEASURED at a station: the metrics some device standing
// there has the hardware for, whether or not it has a usable reading right now.
//
// This is not the same question as "which columns hold a value". Every canonical
// metric gets a column for every device, so a null in the noise column says both
// "this address has no microphone" and "the microphone's reading was rejected" —
// and the panel printed "no reading" for all seven metrics on every station in
// the country as a result. The server answers the first question outright (the
// `measures` column, build.go's measuresOf); this joins its members' answers.
//
// A body without the column — one served before it existed — falls back to every
// metric column there is, which is exactly the old behaviour.
export function measuresAt(body, indices) {
  const columns = metricColumnsOf(body)
  const measures = body?.sensors?.measures
  if (!Array.isArray(measures)) return columns
  const measured = new Set()
  for (const i of indices) {
    for (const metric of measures[i] ?? []) measured.add(metric)
  }
  return columns.filter((metric) => measured.has(metric))
}

// stationsOf groups the columnar body by station, in first-appearance order so
// the marker order stays the server's.
//
// Members are ordered by ascending sensor id, which is what makes every choice
// below ("the first member that has one") deterministic rather than dependent
// on the order rows arrived in.
//
// A body from before the station column existed — or any row missing it —
// falls back to the device's own id, i.e. to one station per device, which is
// exactly the old behaviour.
export function stationsOf(body) {
  const cols = body?.sensors ?? {}
  const ids = cols.id ?? []
  const stationCol = cols.station ?? []
  const order = []
  const byStation = new Map()
  for (let i = 0; i < ids.length; i++) {
    const station = stationCol[i] ?? ids[i]
    if (!byStation.has(station)) {
      byStation.set(station, [])
      order.push(station)
    }
    byStation.get(station).push(i)
  }
  return order.map((station) => ({
    station,
    indices: byStation.get(station).sort((a, b) => Number(ids[a]) - Number(ids[b])),
  }))
}

// stationMembers finds the station one sensor id belongs to, by any of its
// members' ids. A deep link to /sensor/5966 (the climate box) and a click on
// the marker (5965, the particulate box) must land on the same station.
export function stationMembers(body, id) {
  if (id === null || id === undefined) return null
  const ids = body?.sensors?.id ?? []
  const idx = ids.findIndex((v) => Number(v) === Number(id))
  if (idx === -1) return null
  const station = body?.sensors?.station?.[idx] ?? ids[idx]
  return stationsOf(body).find((s) => Number(s.station) === Number(station)) ?? null
}

// readingAt picks one metric's reading for a station: the first member that
// has one, and the id of the device it came from.
//
// The device id is returned, not dropped, because it is what the reader can
// act on next — /api/v1/sensor/{id}/series is keyed by device, so a chart of
// the station's temperature has to ask the box that measured it.
//
// A metric no member has a reading for still answers with a device id (the
// station's own), so a caller never has to handle a half-null result.
export function readingAt(body, indices, metric) {
  const cols = body?.sensors ?? {}
  const ids = cols.id ?? []
  const column = cols[metric]
  for (const i of indices) {
    const value = Array.isArray(column) ? column[i] ?? null : null
    if (value !== null) return { value, sensorId: ids[i] }
  }
  return { value: null, sensorId: ids[indices[0]] ?? null }
}
