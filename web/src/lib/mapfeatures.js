import { rampColour } from './ramp.js'
import { stationsOf, readingAt } from './stations.js'
import { bandsFor } from './mappaint.js'

// areaFeatures maps the choropleth payload straight onto point features.
//
// covered === false renders in the neutral no-data grey with no value label.
// Fewer than three distinct STATIONS is not data — three boxes at one address
// are one place — and drawing it in a band colour
// would imply a confidence the pipeline explicitly refuses.
export function areaFeatures(body, metric, scales, noDataColour) {
  const bands = bandsFor(scales, metric)
  return (body?.areas ?? []).map((a) => ({
    type: 'Feature',
    geometry: { type: 'Point', coordinates: [a.lon, a.lat] },
    properties: {
      slug: a.slug,
      colour: a.covered ? rampColour(a.values?.[metric], bands, noDataColour) : noDataColour,
      value: a.covered ? a.values?.[metric] ?? null : null,
      sensor_count: a.sensor_count,
    },
  }))
}

// sensorFeatures reads the COLUMNAR payload: parallel arrays, each metric a
// sibling key of the fixed columns. That shape was chosen precisely for this
// consumer, so it maps onto features with no reshaping.
//
// A null in a metric column means the sensor does not report that metric, which
// is distinct from reporting zero and must stay distinct.
export function sensorFeatures(body, metric, scales, noDataColour) {
  const bands = bandsFor(scales, metric)
  const s = body?.sensors ?? {}
  const features = []
  // One dot per STATION, not per device: the two boxes at one address carry
  // the same coordinate, so a dot each drew one exactly on top of the other
  // and left the underneath one unclickable. See lib/stations.js.
  for (const { station, indices } of stationsOf(body)) {
    // The reading is the first member that HAS one for this metric — the
    // climate box has no P2 and must not paint the address grey when the
    // particulate box beside it is reporting.
    const { value } = readingAt(body, indices, metric)
    const i = indices[0]
    features.push({
      type: 'Feature',
      geometry: { type: 'Point', coordinates: [s.lon[i], s.lat[i]] },
      properties: {
        id: station,
        colour: rampColour(value, bands, noDataColour),
        value,
        quality: s.quality?.[i] ?? '',
        source: s.source?.[i] ?? 'sensor.community',
      },
    })
  }
  return features
}

export function emptyCollection() {
  return { type: 'FeatureCollection', features: [] }
}
