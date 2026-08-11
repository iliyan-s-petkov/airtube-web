// uPlot wants [xs, ys] with x in epoch SECONDS, so the only transform is a
// divide by 1000. Handing it milliseconds renders every point in 1970 and
// throws no error, which is why the unit is asserted directly in the test.
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
