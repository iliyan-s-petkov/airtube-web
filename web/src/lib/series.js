// uPlot wants [xs, ys] with x in epoch SECONDS, so the only transform is a
// divide by 1000. Handing it milliseconds renders every point in 1970 and
// throws no error, which is why the unit is asserted directly in the test.
//
// uPlot also requires x to be monotonically increasing, and this function
// does not sort or check that — deliberately: sorting here would be dead code
// today and would silently hide a server regression tomorrow. The precondition
// holds because every series query this consumes ends `ORDER BY time` or
// `ORDER BY bucket`, ascending (internal/store/aggregate.go: AreaSeries,
// AllAreaSeries) — the server is the source of that guarantee, not this file.
export function toUplotData(body) {
  const times = body?.t ?? []
  const values = body?.v ?? []
  // Truncated to the shorter of the two rather than padded. A payload with
  // mismatched column lengths is a server bug; plotting a value against a
  // missing timestamp would invent a data point.
  const n = Math.min(times.length, values.length)
  const xs = new Array(n)
  const ys = new Array(n)
  for (let i = 0; i < n; i++) {
    xs[i] = Date.parse(times[i]) / 1000
    ys[i] = values[i]
  }
  return [xs, ys]
}
