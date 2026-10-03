// What the x axis prints under a chart, decided from how much time the chart
// covers.
//
// uPlot's default time axis picks its labels from tick SPACING, not from the
// span: ask for a year of a station that has only existed for a week and the
// ticks land hours apart, so the axis prints clock times under a plot the
// reader asked to see a year of. The reader reads "1 година" on the control
// and "12am 6am 12pm" on the axis and cannot reconcile the two.
//
// Deciding from the span instead keeps the label answering the question that
// was asked: hours for a day or two, dates for weeks, months beyond that.
export const DAY = 86400

// The boundaries: two days is where a clock time stops being the useful unit
// (three days of hourly ticks is a row of repeating "12am"), and two months is
// where a day-and-month label stops fitting across the axis.
export function tickMode(spanSeconds) {
  if (!(spanSeconds > 2 * DAY)) return 'hour'
  if (spanSeconds <= 60 * DAY) return 'day'
  return 'month'
}

const FORMATS = {
  hour: { hour: '2-digit', minute: '2-digit' },
  day: { day: 'numeric', month: 'short' },
  month: { month: 'short', year: 'numeric' },
}

// seconds, not milliseconds: the whole x series is epoch seconds (lib/series.js).
export function formatTick(seconds, mode, locale) {
  const fmt = new Intl.DateTimeFormat(locale, FORMATS[mode] ?? FORMATS.hour)
  return fmt.format(new Date(seconds * 1000))
}

// uPlot's own floor between x ticks, and the clear space kept between two labels.
const MIN_SPACE = 50
const GAP = 12
const HOUR = 3600

// Instants whose labels are the widest a mode prints: every hour of a day, the 28th of every month, every month.
function samples(mode) {
  const base = Date.UTC(2026, 0, 28, 0, 0) / 1000
  if (mode === 'hour') return Array.from({ length: 24 }, (_, h) => base + h * HOUR)
  return Array.from({ length: 12 }, (_, m) => Date.UTC(2026, m, 28, 12) / 1000)
}

// The widest label in the axis font, in CSS px; jsdom has no canvas, so a rough per-character width stands in.
function canvasMeasure(u) {
  const doc = u?.root?.ownerDocument
  const ctx = doc?.createElement('canvas').getContext?.('2d')
  if (!ctx) return (text) => text.length * 7
  // uPlot scales the axis font by the device pixel ratio.
  ctx.font = u.axes?.[0]?.font?.[0] ?? '12px sans-serif'
  const ratio = doc.defaultView?.devicePixelRatio || 1
  return (text) => ctx.measureText(text).width / ratio
}

// The uPlot `space` hook for the x axis: ticks at least one label apart, so a 12-hour "01:00 AM" never runs into the next.
export function tickSpace(xs, locale, measure = canvasMeasure) {
  const span = xs.length > 1 ? xs[xs.length - 1] - xs[0] : 0
  const mode = tickMode(span)
  const labels = samples(mode).map((s) => formatTick(s, mode, locale))
  let space = 0
  return (u) => {
    if (!space) {
      const width = measure(u)
      space = Math.max(MIN_SPACE, Math.ceil(Math.max(...labels.map(width))) + GAP)
    }
    return space
  }
}

// The uPlot `values` hook for the x axis, bound to one dataset's span. xs is
// assumed sorted, which mergeSeries guarantees.
export function tickValues(xs, locale) {
  const span = xs.length > 1 ? xs[xs.length - 1] - xs[0] : 0
  const mode = tickMode(span)
  // uPlot spaces its ticks by pixels, so several of them land inside one day —
  // and a day label repeated four times reads as four days that all happened on
  // the 31st. A repeat is blanked rather than dropped: the tick itself still
  // marks the position, it just does not claim to be a new date.
  return (u, splits) => {
    let last = null
    return splits.map((s) => {
      const label = formatTick(s, mode, locale)
      if (label === last) return ''
      last = label
      return label
    })
  }
}
