// The URL hash is the single source of truth for client state. Pure on purpose:
// no DOM, no framework, no globals — the fallback rules below are the ones most
// likely to be got wrong, and they are provable here with plain values.
//
// The hash, not the query string: it never reaches the server, so no page's
// cache key or canonical URL changes when a visitor switches metric.

import { SOURCES, CITIZEN_SOURCE, OFFICIAL_SOURCE } from './sourcefilter.svelte.js'

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
    sources: parseSources(params.get('layers')),
  }
}

// Short URL tokens for #layers=, distinct from the source strings themselves
// (those are upstream vendor ids, not chosen for a URL). Order here is the
// order tokens are written in by serialiseSources.
const LAYER_TOKENS = [
  ['community', CITIZEN_SOURCE],
  ['official', OFFICIAL_SOURCE],
]

// A key present but with no token that survives is the same as the key being
// absent: both mean "the reader typed nothing usable", and both fall back to
// every source on. 'none' is the one raw value that must NOT fall back — it
// is how an all-off selection round-trips, since an empty token list is
// indistinguishable from an absent key otherwise.
function parseSources(raw) {
  if (raw === null) return new Set(SOURCES)
  if (raw === 'none') return new Set()
  const tokens = raw.split(',')
  const found = new Set()
  for (const [token, source] of LAYER_TOKENS) {
    if (tokens.includes(token)) found.add(source)
  }
  return found.size ? found : new Set(SOURCES)
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
export function serialiseHash({ metric, sensorId, sources }, defaultMetric) {
  const params = new URLSearchParams()
  if (metric && metric !== defaultMetric) params.set('metric', metric)
  if (sensorId !== null && sensorId !== undefined) params.set('sensor', String(sensorId))
  const layers = serialiseSources(sources)
  if (layers !== null) params.set('layers', layers)
  const query = params.toString()
  return query ? `#${query}` : ''
}

// null means "omit the key", used for the default (every source on) and for a
// caller that passed no sources at all. An empty set has no token to spell
// itself with, so it is written as 'none' rather than omitted — omitting it
// would parse back as the default instead.
function serialiseSources(sources) {
  if (!sources) return null
  if (sources.size === SOURCES.length) return null
  if (sources.size === 0) return 'none'
  return LAYER_TOKENS.filter(([, source]) => sources.has(source)).map(([token]) => token).join(',')
}
