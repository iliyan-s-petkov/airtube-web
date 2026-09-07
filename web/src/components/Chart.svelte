<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { mergeSeries } from '../lib/series.js'
  import { tickValues } from '../lib/timeaxis.js'
  import { getJSON } from '../lib/api.js'

  // Two ways in, one way through. `url`/`lineColour`/`valueLabel` describe a
  // single line — the area chart, which has only ever had one — and `sources`
  // describes a list of them, which is what the sensor panel needs to draw
  // temperature against PM2.5. Everything below the normalisation works on the
  // list, so there is no second code path to keep in step.
  //
  // A source is { url, label, colour, scale, unit }. scale names the y axis the
  // line is measured against: two metrics in the same unit share one, and µg/m³
  // against °C must not, or one of them is flattened into the other's range.
  // unit is what that axis's numbers are counted in — without it a plot of two
  // metrics shows two columns of bare numbers and leaves the reader to guess
  // which quantity each one belongs to.
  let {
    url, lineColour, valueLabel, valueUnit = '', sources = null,
    title, timeLabel, empty, unavailable,
  } = $props()

  const defs = $derived(sources ?? [{ url, label: valueLabel, colour: lineColour, scale: 'y', unit: valueUnit }])

  // Three states, one variable: the reader must always be told which one they
  // are in. 'loading' renders nothing rather than a spinner — the panel around
  // this component is already on screen with the current values.
  let status = $state('loading')
  let host

  $effect(() => {
    let chart
    let observer
    let cancelled = false

    // Back to 'loading' before the new url is fetched. The url changes when the
    // reader picks another period, and without this the previous window's "no
    // readings" message would stay on screen over the new window's request —
    // claiming an answer about a period nobody has asked the server about yet.
    status = 'loading'

    // Read here, outside the async body, so the effect depends on them: a
    // read after the first await happens outside the tracking context and the
    // chart would never redraw when the reader picks another metric.
    const lines = defs

    ;(async () => {
      let bodies
      try {
        bodies = await Promise.all(lines.map((s) => getJSON(s.url)))
      } catch (err) {
        // One failure fails the plot. A chart drawn from the metrics that did
        // answer, with no word about the one that did not, is a chart the
        // reader would take as complete.
        if (!cancelled) status = 'unavailable'
        console.error('chart data:', err)
        return
      }
      if (cancelled) return

      const data = mergeSeries(bodies)
      if (data[0].length === 0) { status = 'empty'; return }
      status = 'ok'

      // One axis per distinct scale, in the order the lines name them: the
      // first on the left as usual, a second on the right, so two units can
      // share the plot without either being squashed into the other's range.
      const scales = [...new Set(lines.map((s) => s.scale ?? 'y'))]

      chart = new uPlot({
        title,
        width: host.clientWidth || 600,
        height: 240,
        // Epoch SECONDS — see lib/series.js. uPlot's x scale is time by
        // default, so milliseconds would plot every point in 1970 silently.
        //
        // The x series carries a label because uPlot supplies its own English
        // "Time" when it has none, and that label is visible in the hover
        // readout below — the one English word on a Bulgarian page.
        series: [
          { label: timeLabel },
          // spanGaps false, the default, spelled out: mergeSeries writes null
          // where a device reported nothing, and joining across that null
          // would draw a straight line through hours nobody measured.
          ...lines.map((s) => ({
            label: s.label,
            stroke: s.colour,
            width: 2,
            scale: s.scale ?? 'y',
            spanGaps: false,
          })),
        ],
        axes: [
          // Labels from the data's span, not uPlot's tick spacing — timeaxis.js.
          {
            values: tickValues(data[0], document.documentElement.lang || undefined),
            label: timeLabel || undefined,
          },
          ...scales.map((scale, i) => {
            // Colour and unit of the line measured against it — two unlabelled
            // columns of numbers cannot be attributed to either line.
            const line = lines.find((s) => (s.scale ?? 'y') === scale)
            return {
              scale,
              side: i === 0 ? 3 : 1,
              stroke: line?.colour,
              label: line?.unit || undefined,
              // One grid only: two is a lattice nobody can read a value off.
              grid: { show: i === 0 },
            }
          }),
        ],
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
