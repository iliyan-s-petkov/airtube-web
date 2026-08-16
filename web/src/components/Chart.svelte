<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { toUplotData } from '../lib/series.js'
  import { getJSON } from '../lib/api.js'

  let { url, lineColour, title, valueLabel, empty, unavailable } = $props()

  // Three states, one variable: the reader must always be told which one they
  // are in. 'loading' renders nothing rather than a spinner — the panel around
  // this component is already on screen with the current values.
  let status = $state('loading')
  let host

  $effect(() => {
    let chart
    let observer
    let cancelled = false

    ;(async () => {
      let body
      try {
        body = await getJSON(url)
      } catch (err) {
        if (!cancelled) status = 'unavailable'
        console.error('chart data:', err)
        return
      }
      if (cancelled) return

      const data = toUplotData(body)
      if (data[0].length === 0) { status = 'empty'; return }
      status = 'ok'

      chart = new uPlot({
        title,
        width: host.clientWidth || 600,
        height: 240,
        // Epoch SECONDS — see lib/series.js. uPlot's x scale is time by
        // default, so milliseconds would plot every point in 1970 silently.
        series: [{}, { label: valueLabel, stroke: lineColour, width: 2 }],
        scales: { x: { time: true } },
      }, data, host)

      // The container is fluid; a chart left at its first-paint width is
      // visibly wrong after a phone rotates. setSize does not re-fetch.
      observer = new ResizeObserver(() => {
        const width = host.clientWidth
        if (width > 0) chart.setSize({ width, height: 240 })
      })
      observer.observe(host)
    })()

    return () => { cancelled = true; observer?.disconnect(); chart?.destroy?.() }
  })
</script>

<div bind:this={host} class="chart-host"></div>
{#if status === 'unavailable'}<p class="chart-message">{unavailable}</p>{/if}
{#if status === 'empty'}<p class="chart-message">{empty}</p>{/if}
