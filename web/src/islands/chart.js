// The chart island: one series, one metric, one period (24h PM2.5 today;
// period/metric selectors are Phase 3b). Mounted beside the server-rendered
// aggregate on /area/{slug}, never replacing it — a failed fetch here leaves
// a complete page rather than a hole.
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { toUplotData } from '../lib/series.js'
import { getJSON } from '../lib/api.js'

export async function mount(el) {
  const cfg = {
    slug: el.dataset.slug,
    metric: el.dataset.metric || 'P2',
    period: el.dataset.period || '24h',
    title: el.dataset.tTitle || '',
    empty: el.dataset.tEmpty || '',
    valueLabel: el.dataset.tValue || '',
  }
  if (!cfg.slug) return // nothing to draw; leave the server-rendered aggregate

  const url = `/api/v1/area/${encodeURIComponent(cfg.slug)}/series` +
    `?metric=${encodeURIComponent(cfg.metric)}&period=${encodeURIComponent(cfg.period)}`

  let body
  try {
    body = await getJSON(url)
  } catch (err) {
    // The area page already shows the current aggregate value server-side, so a
    // failed chart leaves a complete page rather than a hole.
    console.error('chart data:', err)
    return
  }

  const data = toUplotData(body)
  if (data[0].length === 0) {
    // uPlot given [[], []] renders an empty frame rather than throwing, but an
    // empty frame with no explanation reads as a broken chart. Say why.
    el.textContent = cfg.empty
    return
  }

  const chart = new uPlot({
    title: cfg.title,
    width: el.clientWidth || 600,
    height: 240,
    // Epoch SECONDS — see lib/series.js. uPlot's default x scale is time, so
    // handing it milliseconds plots every point in 1970 with no error.
    series: [
      {},
      { label: cfg.valueLabel, stroke: '#2563eb', width: 2 },
    ],
    scales: { x: { time: true } },
  }, data, el)

  // Redrawn on resize rather than left at its initial width: the container is
  // fluid, and a chart that keeps its first-paint width is visibly wrong after
  // a phone rotates. uPlot's setSize is cheap (it does not re-fetch or
  // re-process `data`), so calling it straight from the observer callback is
  // fine with no extra debouncing.
  const observer = new ResizeObserver(() => {
    const width = el.clientWidth
    if (width > 0) chart.setSize({ width, height: 240 })
  })
  observer.observe(el)
}
