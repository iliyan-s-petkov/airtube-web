// What one reading means where it was taken. A sensor card states 3.25 µg/m³;
// only the area around it says whether that is clean air or the best of a bad
// afternoon. These are the numbers behind the four cards the readout strip
// shows while a sensor is open.
//
// Computed from the sensors body the map already loaded (lib/sensors.svelte.js
// says why nothing here re-fetches), so the comparison set is exactly the area
// whose dots are on screen.

// One value per STATION, not per device. A station is one address with several
// devices — an SDS011 beside a BME280 — and counting both would report the same
// spot twice and make a two-sensor street look like four.
export function stationValues(body, metric) {
  const column = body?.sensors?.[metric]
  const stations = body?.sensors?.station
  const ids = body?.sensors?.id
  if (!Array.isArray(column) || !Array.isArray(stations)) return []

  const seen = new Map()
  for (let i = 0; i < column.length; i += 1) {
    const value = column[i]
    if (typeof value !== 'number' || !Number.isFinite(value)) continue
    const station = stations[i] ?? ids?.[i]
    if (station == null || seen.has(station)) continue
    seen.set(station, { station, id: ids?.[i] ?? station, value })
  }
  return [...seen.values()]
}

export function median(values) {
  const s = [...values].sort((a, b) => a - b)
  const mid = Math.floor(s.length / 2)
  // The even case averages the two middle values, matching the server's own
  // median (internal/snapshot/hexes.go) so the two never disagree by a rule.
  return s.length % 2 === 0 ? (s[mid - 1] + s[mid]) / 2 : s[mid]
}

// The station the open sensor stands at. A deep link can name any device at an
// address, and the values above are keyed by station, so the id has to be
// translated before it can be looked up among them.
function stationOf(body, sensorId) {
  if (sensorId == null) return null
  const id = Number(sensorId)
  const i = (body?.sensors?.id ?? []).indexOf(id)
  return i < 0 ? id : (body?.sensors?.station?.[i] ?? id)
}

// areaStats ranks the open sensor among its neighbours for one metric.
//
// `rank` counts from the cleanest reading up, so rank 1 is the best air in the
// area rather than the worst — "3rd of 41" then reads the way a league table
// does. null when this sensor reports nothing for the metric: it still has an
// area around it, and the other three figures are still true.
//
// null overall for an area with a single reporting station, where a highest, a
// lowest and a median are three names for one number.
export function areaStats(body, metric, sensorId) {
  const rows = stationValues(body, metric)
  if (rows.length < 2) return null

  const values = rows.map((r) => r.value)
  const mine = rows.find((r) => r.station === stationOf(body, sensorId)) ?? null

  return {
    high: Math.max(...values),
    low: Math.min(...values),
    median: median(values),
    total: rows.length,
    value: mine ? mine.value : null,
    rank: mine ? values.filter((v) => v < mine.value).length + 1 : null,
  }
}
