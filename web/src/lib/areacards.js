// The four readout cards an open sensor puts in place of the national ones.
//
// Same shape as the server's Readout (internal/web/render.go) — label, value,
// unit, tier and an optional gauge — so one component renders either set and
// the swap cannot change how a card looks, only what it says.

const GAUGE_UNIT = 'µg/m³'

// The arc's ceiling is the scale's DRAWN ceiling, the same top the map ramp and
// the legend stop at, mirroring gaugePercent in internal/web/colour.go. A
// metric counted in anything else gets no arc: 944 hPa is an ordinary day, and
// a ring at 97% would invent an alarm out of an axis.
export function gaugeFor(scale, value) {
  if (!scale || scale.unit !== GAUGE_UNIT) return null
  const bands = scale.bands ?? []
  const ceiling = scale.ceiling > 0
    ? scale.ceiling
    : bands.reduce((top, b) => (typeof b.upper === 'number' && b.upper > top ? b.upper : top), 0)
  if (!(ceiling > 0)) return null
  return {
    percent: Math.max(0, Math.min(100, Math.round((value / ceiling) * 100))),
    colour: bandColour(bands, value),
  }
}

// Upper is inclusive, so a value exactly on a boundary belongs to the lower
// band; a null upper is the open-ended top.
export function bandColour(bands, value) {
  for (const band of bands ?? []) {
    if (band.upper == null || value <= band.upper) return band.colour
  }
  return ''
}

function fill(text, values) {
  return Object.entries(values).reduce(
    (s, [key, v]) => s.replaceAll(`{${key}}`, String(v)),
    text ?? '',
  )
}

function number(value, lang) {
  return value.toLocaleString(lang === 'bg' ? 'bg-BG' : 'en-GB', { maximumFractionDigits: 1 })
}

// areaCards returns [] when there is nothing honest to say — no stats, or a
// metric the open sensor's area does not report — and the caller then leaves
// the national strip standing rather than showing four blanks.
export function areaCards(stats, { metric, metricLabel, scale, area, lang, t }) {
  if (!stats) return []
  const unit = scale?.unit ?? ''
  const where = area ? fill(t.areaSensors, { area, total: stats.total }) : fill(t.sensorsOnly, { total: stats.total })

  const figure = (labelKey, value) => ({
    label: fill(t[labelKey], { metric: metricLabel }),
    value: number(value, lang),
    unit,
    tier: where,
    gauge: gaugeFor(scale, value),
  })

  const cards = [
    figure('high', stats.high),
    figure('low', stats.low),
    figure('median', stats.median),
  ]

  // The fourth card is this sensor's place among the other three. Dropped when
  // the sensor reports nothing for the metric: a rank of nothing is not a rank,
  // and the area's own three figures are still true without it.
  if (stats.rank != null) {
    const above = stats.value - stats.median
    cards.push({
      label: t.thisSensor,
      value: String(stats.rank),
      unit: fill(t.ofTotal, { total: stats.total }),
      tier: above > 0 ? t.aboveMedian : above < 0 ? t.belowMedian : t.atMedian,
      gauge: null,
    })
  }
  return cards
}
