// How many sensors the map is drawing, and how many of them are silent.
//
// Counted from the RAW columnar body the map published into sensors.svelte.js,
// not from the GeoJSON features the map built: features exist only inside the
// map island, and a second component reaching into another island's scratch
// state to count what is on screen is the wrong seam. The body and the features
// are built from the same column, so the two agree by construction.
//
// Plain .js, no runes: this is arithmetic over a payload. The reactivity lives
// in the component that calls it.

// `value === null` is the test for silence, as in lib/sensorfilter.js — 0 µg/m³
// is a reading, and the cleanest sensor in the area is exactly the one a falsy
// test would misfile.
export function countSensors(responseBody, metric) {
  const column = responseBody?.sensors?.[metric]
  const ids = responseBody?.sensors?.id ?? []
  const total = ids.length

  // The metric column can be absent entirely — an area where no sensor reports
  // this metric at all. Every sensor is then silent FOR THIS METRIC, which is
  // what the map paints, so that is what the line must say.
  if (!Array.isArray(column)) return { total, active: 0, silent: total }

  let active = 0
  for (let i = 0; i < total; i++) if ((column[i] ?? null) !== null) active++
  return { total, active, silent: total - active }
}

// The count line, composed from the catalogue's parts — the same idiom as
// islands/table.js's countLine, and for the same reason: i18n.Catalogue.T takes
// no parameters, so a sentence with numbers in it is assembled here.
//
// `shown` follows the filter, because the line sits under the filter and
// answers the question the filter just raised. The silent tail stays on every
// status: it is the coverage fact, and it does not stop being true when the
// reader hides the silent sensors.
export function sensorCountLine(texts, counts, status) {
  const shown =
    status === 'active' ? counts.active : status === 'inactive' ? counts.silent : counts.total
  return `${texts.shown} ${shown} ${texts.of} ${counts.total} ${texts.sensors} — ${counts.silent} ${texts.silent}`
}
