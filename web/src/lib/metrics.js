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

// Like parseMetricList, but it KEEPS the empty entries. A metric list has no
// blank members and dropping them is right there; a label or unit list is
// positional against it, so a metric with no unit is a legitimate empty slot
// and dropping it shifts every unit after it onto the wrong metric. Same
// reason zipLabels refuses to zip lists of different lengths silently.
export function splitAttr(raw) {
  const s = String(raw || '').trim()
  return s === '' ? [] : s.split(',').map((v) => v.trim())
}

// The metric-keyed form of a positional attribute, for callers that look up by
// name rather than walk the list. A metric with no value gets '', never
// undefined: the callers treat an absent string as "say less", and undefined
// would print as the word.
export function byMetric(metrics, values) {
  return Object.fromEntries(metrics.map((m, i) => [m, values[i] ?? '']))
}

// Positional pairing of the two server attributes. Extra labels are dropped and
// missing ones fall back to the metric's own name: a mislabelled control is
// worse than an unlabelled one, and silently shifting labels by one is exactly
// what an unchecked zip does when the two lists disagree.
export function zipLabels(metrics, labels) {
  return metrics.map((metric, i) => ({ metric, label: labels[i] || metric }))
}
