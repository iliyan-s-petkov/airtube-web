// The URL hash is the single source of truth for client state. Pure on purpose:
// no DOM, no framework, no globals — the fallback rules below are the ones most
// likely to be got wrong, and they are provable here with plain values.
//
// The hash, not the query string: it never reaches the server, so no page's
// cache key or canonical URL changes when a visitor switches metric.

// parseHash reads the two keys INDEPENDENTLY. A junk sensor id must not
// invalidate a good metric, and vice versa — a single "valid hash" check would
// throw away both on one typo.
//
// metrics and defaultMetric are arguments, never module constants: the metric
// list is the server's (upstream.CanonicalMetrics via data-metrics) and the
// default is series.default_metric. A copy here would be a second home for a
// value airbg.yaml already owns.
export function parseHash(hash, { metrics, defaultMetric }) {
  const params = new URLSearchParams(String(hash || '').replace(/^#/, ''))
  const metric = params.get('metric')
  return {
    metric: metrics.includes(metric) ? metric : defaultMetric,
    sensorId: parseSensorId(params.get('sensor')),
  }
}

// A sensor id is a positive integer. Rejecting '0', '-5' and '1.5' explicitly:
// Number('') is 0 and Number('1.5') is 1.5, so a bare Number() cast would let
// all three through and produce a request for a sensor that cannot exist.
function parseSensorId(raw) {
  if (raw === null || raw === '') return null
  const n = Number(raw)
  if (!Number.isInteger(n) || n <= 0) return null
  return n
}

// serialiseHash writes the WHOLE hash, so the two keys can never disagree with
// what is rendered. Defaults are omitted rather than written out: '#metric=P2'
// on every shared link is noise, and an empty return lets the caller strip the
// '#' from the address bar entirely.
export function serialiseHash({ metric, sensorId }, defaultMetric) {
  const params = new URLSearchParams()
  if (metric && metric !== defaultMetric) params.set('metric', metric)
  if (sensorId !== null && sensorId !== undefined) params.set('sensor', String(sensorId))
  const query = params.toString()
  return query ? `#${query}` : ''
}
