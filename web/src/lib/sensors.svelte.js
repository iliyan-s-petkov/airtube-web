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

export function setSensors(next) {
  body = next
}

export function setScales(next) {
  scales = next
}

export function getScales() {
  return scales
}

// The columns every sensor row carries that are NOT a metric reading —
// verified against internal/snapshot/build.go's sensorColumns.MarshalJSON
// (build.go:73-79), which flattens Metrics as sibling keys of exactly these
// five fixed columns:
//   id      - the sensor's identity, not a reading
//   type    - the hardware model, not a reading
//   lon/lat - the sensor's location, not a reading
//   quality - the sensor's own data-quality flag; exposed separately below
//             as `flag`, not folded into `values`, since it is metadata
//             ABOUT the readings rather than a reading itself
// Every other key in the columnar body is a canonical metric column
// (upstream.CanonicalMetrics, build.go:186). Deriving by exclusion from this
// fixed list — rather than an allow-list of known metrics — is what lets a
// metric added server-side show up in the panel with no frontend change.
const META_COLUMNS = new Set(['id', 'type', 'lon', 'lat', 'quality'])

// normaliseSensor projects ONE sensor out of the columnar body into the shape
// lib/sensorview.js's panelRows expects: { id, flag, values }.
//
// Projects by id, not by index: every caller (findSensor, and ultimately
// viewstate's sensorId) already has an id, never an index, so requiring an
// index here would just push an id->index lookup onto every caller instead
// of doing it once, here.
//
// A metric column that EXISTS but holds null at this sensor's index lands in
// `values` as null (still reported, no current reading — see
// lib/sensorview.js's own comment on why that distinction matters). A metric
// column the response does not carry AT ALL is simply never visited by the
// loop below, so it never becomes a key of `values` — "reported" and
// "measured by this hardware" stay distinct all the way through.
export function normaliseSensor(responseBody, id) {
  const cols = responseBody?.sensors ?? {}
  const ids = cols.id ?? []
  const idx = ids.findIndex((v) => Number(v) === Number(id))
  if (idx === -1) return null

  const values = {}
  for (const [key, col] of Object.entries(cols)) {
    if (META_COLUMNS.has(key)) continue
    values[key] = Array.isArray(col) ? col[idx] ?? null : null
  }

  return {
    id: ids[idx],
    // quality -> flag: SensorPanel's flag lookup (see islands/panel.js) keys
    // off `flag`. The wire vocabulary (a data-quality field, matching the
    // store/API's own naming) must not leak into the panel's own vocabulary
    // unrenamed — a future reader grepping the panel for "quality" would
    // find nothing, and grepping the wire format for "flag" would find
    // nothing either, without this comment.
    flag: cols.quality?.[idx] ?? '',
    values,
  }
}

export function findSensor(id) {
  // vs.sensorId is null when no sensor is open. Number(null) is 0, which
  // would wrongly match a real sensor id of 0 — guard explicitly rather than
  // rely on the id column never containing 0.
  if (id === null || id === undefined) return null
  return normaliseSensor(body, id)
}
