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
        // uPlot's legend IS the hover readout, and with no cursor on the plot
        // it renders the series labels beside em-dash placeholders. Switching
        // it off would take the readout away with it, so it is hidden by CSS
        // (see below) and revealed only while the cursor is over a point.
        //
        // idx != null, not a truthy test: index 0 is the leftmost point of
        // every chart, and `if (idx)` would leave it the one value nobody can
        // read.
        hooks: {
          setCursor: [(u) => host.classList.toggle('chart-live', u.cursor.idx != null)],
        },
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

<style>
  /* Fully :global on both sides, deliberately. uPlot builds .u-legend itself at
     runtime so compile-time scoping never reaches it — and `chart-live` is added
     by the setCursor hook, never by this template, so Svelte prunes any selector
     mentioning it as unused and the rule silently never ships. (It did: the
     build warned "Unused CSS selector".)

     visibility, not display: the legend keeps its box either way, so revealing
     it does not shift the page under the pointer that is reading it. */
  :global(.chart-host .u-legend) { visibility: hidden; }
  :global(.chart-host.chart-live .u-legend) { visibility: visible; }
</style>
