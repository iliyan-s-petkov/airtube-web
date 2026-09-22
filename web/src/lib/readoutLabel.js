// Splits a composed readout label into its metric word and the rest, so a
// component can wrap the metric in its own span (hidden on a phone by
// .readout__metric) without a second copy of the i18n string that built it.
// Same separator as readoutMetric/readoutRest in internal/web/render.go, but
// the metric can sit on either side: the server's own labels put it first
// ("PM2.5 · Highest province figure"), the island's high/low/median labels
// put it last ("Highest · PM2.5") — read.sensor.high is "Highest · {metric}".
// Locating the known metric text rather than assuming a position keeps both
// paths hiding the same word.
const SEP = ' · '

export function splitReadoutLabel(label, metric) {
  const text = label ?? ''
  if (metric && text.startsWith(metric + SEP)) {
    return { position: 'prefix', metric, rest: text.slice(metric.length + SEP.length) }
  }
  if (metric && text.endsWith(SEP + metric)) {
    return { position: 'suffix', metric, rest: text.slice(0, text.length - metric.length - SEP.length) }
  }
  return { position: 'none', metric: '', rest: text }
}
