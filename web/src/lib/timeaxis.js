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

// The uPlot `values` hook for the x axis, bound to one dataset's span. xs is
// assumed sorted, which mergeSeries guarantees.
export function tickValues(xs, locale) {
  const span = xs.length > 1 ? xs[xs.length - 1] - xs[0] : 0
  const mode = tickMode(span)
  return (u, splits) => splits.map((s) => formatTick(s, mode, locale))
}
