// The chart lines that say what the sensors AROUND this one were doing.
//
// One request, three lines: the area series endpoint returns the lowest sensor,
// the median and the highest per bucket when it is asked to band (?band=1), so
// the three share a url and differ only in which column they read. Chart.svelte
// fetches one url once — see its `column` handling.
//
// The band is only offered for a single metric. It is the spread of ONE
// quantity, and against two metrics on one plot there is nothing for a median
// to be the median of.

// The order the lines are drawn and listed in: lowest, median, highest, which
// is the order they sit on the plot.
export const NEARBY_KEYS = ['low', 'median', 'high']

// Which column of the banded payload each line reads. "v" is the median the
// endpoint has always returned; the extremes are the two columns it adds.
const COLUMN = { low: 'lo', median: 'v', high: 'hi' }

// Dashes rather than three colours. All three answer the same question about
// the same place, so they read as one annotation on the sensor's own solid
// line; three separate hues would read as three more metrics. The lengths
// differ so the legend is not the only way to tell them apart.
const DASH = { low: [2, 4], median: [7, 4], high: [12, 4] }

export function nearbyOptions(labels) {
  return NEARBY_KEYS.map((key) => ({ key, label: labels[key] || key }))
}

// keys is the reader's selection, in whatever order they ticked it; the output
// follows NEARBY_KEYS so the legend does not reshuffle as boxes are ticked.
export function nearbySources({ slug, metric, query, keys, colour, scale, unit, labels }) {
  if (!slug || !metric || !query) return []

  const url = `/api/v1/area/${encodeURIComponent(slug)}/series` +
    `?metric=${encodeURIComponent(metric)}&band=1&${query}`

  return NEARBY_KEYS.filter((key) => keys.includes(key)).map((key) => ({
    url,
    column: COLUMN[key],
    dash: DASH[key],
    label: labels[key] || key,
    colour,
    scale,
    unit,
  }))
}
