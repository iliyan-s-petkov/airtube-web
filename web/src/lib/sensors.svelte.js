import { stationMembers, readingAt, measuresAt } from './stations.js'

// What the map has last loaded, published for the panel to read — not
// refetched. Refetching would double every page's request count against a
// per-IP enumeration limiter that counts distinct sensor ids, so the panel
// would burn the visitor's budget twice for data the map already has.
//
// A $state so the panel re-renders when a pan brings new sensors in — and so
// a deep-linked #sensor= that arrives before the data does resolves as soon
// as it lands (see islands/panel.js, which reads through findSensor via a
// plain getter prop; Svelte's fine-grained tracking follows the read into
// this module regardless of which file declared the $state).
//
// Holds the RAW response body (`{ generated_at, sensors: {...} }`) from
// GET /api/v1/area/{slug}/sensors, not GeoJSON features. The map's features
// (islands/map.js's sensorFeatures) carry only the CURRENTLY SELECTED
// metric's value and scratch presentation fields (colour); the panel needs
// every metric a sensor reports, which only the columnar body itself has.
let body = $state(null)

// scales is published from the SAME place map.js already fetches it
// (loadScales, /api/v1/scales, cached — see map.js's initData) rather than
// re-fetched here. Panel.js could import loadScales from islands/map.js
// directly, but that would mean either duplicating map.js's chrome-banner
// error handling or silencing it with a stub chrome, for a value the map
// island already has in hand. Publishing it into this registry the moment
// map.js's initData resolves it means panel.js needs no network code at all
// and reacts the same way findSensor does: a plain $state read.
let scales = $state(null)

// The slug the body was loaded for. The body itself names no area, and the
// readout strip has to be able to say WHICH area the figures beside an open
// sensor describe — an unnamed "highest nearby" is a number with no set.
let areaSlug = $state(null)

export function setSensors(next, slug = null) {
  body = next
  areaSlug = slug
}

export function getSensorArea() {
  return areaSlug
}

export function setScales(next) {
  scales = next
}

export function getScales() {
  return scales
}

// The whole body, for the callers that count rather than project one sensor.
// Reading it inside a $derived tracks it like any other $state read, so a pan
// that publishes new sensors updates the count with no subscription of its own.
export function getSensors() {
  return body
}

// normaliseSensor projects ONE STATION out of the columnar body into the shape
// lib/sensorview.js's panelRows expects: { id, flag, values, sources }.
//
// A station, not a device (lib/stations.js says why): the reader clicked one
// dot on one address, and the address is where the temperature is measured as
// much as the particulate matter is. So the seven rows are filled from every
// device standing there, and `sources` records which device each reading came
// from — the chart endpoint is keyed by device, so the panel has to be able to
// say which one to ask.
//
// Resolves by id, not by index, and by ANY member's id: a deep link naming the
// climate box and a click on the particulate box are the same station.
//
// The keys of `values` are what is MEASURED at this address (measuresAt), not
// every column the response carries. A metric measured here with no usable
// reading right now lands as null and the panel says so; a metric no device
// here measures never becomes a key at all, and the panel omits the row. That
// is the distinction lib/sensorview.js's panelRows filters on — before the
// server published `measures`, every station claimed all seven metrics and the
// panel said "no reading" for the four it has no hardware for.
export function normaliseSensor(responseBody, id) {
  const members = stationMembers(responseBody, id)
  if (!members) return null
  const cols = responseBody?.sensors ?? {}

  const values = {}
  const sources = {}
  for (const key of measuresAt(responseBody, members.indices)) {
    const { value, sensorId } = readingAt(responseBody, members.indices, key)
    values[key] = value
    sources[key] = sensorId
  }

  return {
    // The station's id, which is its lowest member's — stable across
    // snapshots, and the id the marker for this address carries.
    id: members.station,
    // quality -> flag: SensorPanel's flag lookup (see islands/panel.js) keys
    // off `flag`. The wire vocabulary (a data-quality field, matching the
    // store/API's own naming) must not leak into the panel's own vocabulary
    // unrenamed — a future reader grepping the panel for "quality" would
    // find nothing, and grepping the wire format for "flag" would find
    // nothing either, without this comment.
    //
    // The first member flagged as anything other than ok, if any: a station
    // with one misbehaving device has a problem the reader should see, and
    // averaging or hiding it behind the healthy device would be the panel
    // deciding not to mention it.
    flag: stationFlag(cols, members.indices),
    values,
    sources,
    // The station's own coordinate, taken from its first member: every device
    // at a station shares it by definition (they are grouped by exact equality
    // — see internal/snapshot/build.go's stationIDs).
    lon: cols.lon?.[members.indices[0]] ?? null,
    lat: cols.lat?.[members.indices[0]] ?? null,
    // Station identity columns (Task 10's addition to the wire): 'eea' for
    // an official reference station, 'sensor.community' for a citizen
    // device. The EoI code/type/area only exist for 'eea' rows.
    source: cols.source?.[members.indices[0]] ?? '',
    stationCode: cols.station_code?.[members.indices[0]] ?? '',
    stationName: cols.station_name?.[members.indices[0]] ?? '',
    stationType: cols.station_type?.[members.indices[0]] ?? '',
    stationArea: cols.station_area?.[members.indices[0]] ?? '',
    // One entry per box standing here, so the panel can say what the hardware
    // is rather than leaving "Сензор 5965" to stand for a pair of instruments.
    devices: members.indices.map((i) => ({
      id: cols.id?.[i] ?? null,
      type: cols.type?.[i] ?? '',
      firstSeen: cols.first_seen?.[i] ?? null,
      lastSeen: cols.last_seen?.[i] ?? null,
    })),
  }
}

function stationFlag(cols, indices) {
  for (const i of indices) {
    const flag = cols.quality?.[i] ?? ''
    if (flag && flag !== 'ok') return flag
  }
  return cols.quality?.[indices[0]] ?? ''
}

export function findSensor(id) {
  // vs.sensorId is null when no sensor is open. Number(null) is 0, which
  // would wrongly match a real sensor id of 0 — guard explicitly rather than
  // rely on the id column never containing 0.
  if (id === null || id === undefined) return null
  return normaliseSensor(body, id)
}
