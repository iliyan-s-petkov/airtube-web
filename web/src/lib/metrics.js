// Which metrics exist, and which of them the map can colour.
//
// Neither question is answered by a list in this file. The metric list is the
// server's (upstream.CanonicalMetrics, rendered as data-metrics) and "is it
// scaled" is derived from /api/v1/scales — so publishing pressure bands
// server-side would colour pressure with no frontend release. A hardcoded
// ['P1','P2'] here would be a second home for a fact the server already owns.

export function parseMetricList(raw) {
  return String(raw || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

// A metric is scaled if and only if the server publishes a NON-EMPTY band table
// for it. The emptiness check is load-bearing: colourFor with [] bands returns
// the no-data colour for every value, so an empty table renders exactly like a
// metric with no table at all — and must therefore be treated as one.
export function hasScale(scales, metric) {
  if (!Array.isArray(scales)) return false
  return scales.some((s) => s.metric === metric && Array.isArray(s.bands) && s.bands.length > 0)
}

export function unitFor(scales, metric) {
  if (!Array.isArray(scales)) return ''
  return scales.find((s) => s.metric === metric)?.unit ?? ''
}
