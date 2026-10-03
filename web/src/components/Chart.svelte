<script>
  import uPlot from 'uplot'
  import 'uplot/dist/uPlot.min.css'
  import { mergeSeries } from '../lib/series.js'
  import { tickSpace, tickValues } from '../lib/timeaxis.js'
  import { getJSON } from '../lib/api.js'
  import { legibleStroke } from '../lib/axiscolour.js'

  // Two ways in, one way through. `url`/`lineColour`/`valueLabel` describe a
  // single line — the area chart, which has only ever had one — and `sources`
  // describes a list of them, which is what the sensor panel needs to draw
  // temperature against PM2.5. Everything below the normalisation works on the
  // list, so there is no second code path to keep in step.
  //
  // A source is { url, label, colour, scale, unit, column, dash }. scale names
  // the y axis the line is measured against: two metrics in the same unit share
  // one, and µg/m³ against °C must not, or one of them is flattened into the
  // other's range. unit is what that axis's numbers are counted in — without it
  // a plot of two metrics shows two columns of bare numbers and leaves the
  // reader to guess which quantity each one belongs to.
  //
  // column names which array of its url's payload the line reads, defaulting to
  // "v". It is what lets one banded response draw three lines, and dash is how
  // those three are told apart: the sensor is solid, its surroundings dashed.
  //
  // fill sizes the plot to whatever height its container gives the frame, with no drag handle:
  // the sensor panel over the map hands the chart the room left after its header and gauges.
  //
  // resizable gives the plot a drag handle: analysing a day of four metrics
  // wants more than the 240px a summary chart needs.
  let {
    url, lineColour, valueLabel, valueUnit = '', sources = null,
    title, timeLabel, empty, unavailable, resizable = false, fill = false,
  } = $props()

  const defs = $derived(sources ?? [{ url, label: valueLabel, colour: lineColour, scale: 'y', unit: valueUnit }])

  // Three states, one variable: the reader must always be told which one they
  // are in. 'loading' renders nothing rather than a spinner — the panel around
  // this component is already on screen with the current values.
  let status = $state('loading')
  let host
  let frame

  // The plot's own height. A resized frame carries an inline height the reader
  // dragged, and uPlot's legend sits inside it, so its strip comes off the plot
  // rather than overflowing the box.
  const BASE_HEIGHT = 240
  const LEGEND_STRIP = 34
  // Measured once the legend exists, because 34 was only its height in one
  // font at one language: wherever it ran taller the plot overflowed the
  // clipped frame and the x-axis title was cut off against the strip below.
  function legendStrip() {
    return host?.querySelector('.u-legend')?.offsetHeight || LEGEND_STRIP
  }
  function plotSize() {
    const measured = (resizable || fill) && frame?.clientHeight
    // A fill chart's legend floats over the plot (style below), so it takes no strip.
    const dragged = measured ? frame.clientHeight - (fill ? 0 : legendStrip()) : BASE_HEIGHT
    return { width: host.clientWidth || 600, height: Math.max(fill ? 80 : 160, dragged) }
  }

  // Token reads, not hex literals (literals.test.js bans those in web/src).
  // Fallbacks are the kit's light-theme values as rgb(), for when the
  // stylesheet hasn't loaded yet.
  const FALLBACK_AXIS_COLOUR = 'rgb(82, 82, 82)' // --fg-2 light
  const FALLBACK_GRID_COLOUR = 'rgb(224, 224, 224)' // --border-soft light
  const FALLBACK_BG = 'rgb(255, 255, 255)' // --bg light
  function token(name, fallback) {
    if (typeof document === 'undefined') return fallback
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback
  }
  const axisColour = () => token('--fg-2', FALLBACK_AXIS_COLOUR)
  const gridColour = () => token('--border-faint', FALLBACK_GRID_COLOUR)
  const bgColour = () => token('--bg', FALLBACK_BG)

  // uPlot paints the canvas once and never re-reads CSS, so a theme toggle
  // needs this to repaint the axes rather than a stylesheet swap.
  function restyleAxes(u, scales, lines) {
    const tick = axisColour()
    const grid = gridColour()
    const bg = bgColour()
    u.axes[0].stroke = tick
    u.axes[0].ticks.stroke = tick
    u.axes[0].grid.stroke = grid
    scales.forEach((scale, i) => {
      const line = lines.find((s) => (s.scale ?? 'y') === scale)
      const axis = u.axes[i + 1]
      axis.stroke = line?.colour ? legibleStroke(line.colour, bg) : tick
      axis.ticks.stroke = tick
      axis.grid.stroke = grid
    })
    u.redraw()
  }

  $effect(() => {
    let chart
    let observer
    let themeObserver
    let media
    let onMediaChange
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

    // One getJSON per line, not per distinct url: three lines off one banded
    // response name the same url and differ only in which column they read, and
    // getJSON already collapses concurrent callers of a url onto one request
    // (see inFlight in lib/api.js). Deduping again here would be a second copy
    // of that rule, provable only by deleting the first.
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

      const data = mergeSeries(bodies, lines.map((s) => s.column ?? 'v'))
      // Two points, not one: uPlot draws a single reading as a full plot with
      // axes and a legend, which reads as a trend the reader can follow. One
      // point is a number, and the empty state says so honestly.
      if (data[0].length < 2) { status = 'empty'; return }
      status = 'ok'

      // One axis per distinct scale, in the order the lines name them: the
      // first on the left as usual, a second on the right, so two units can
      // share the plot without either being squashed into the other's range.
      const scales = [...new Set(lines.map((s) => s.scale ?? 'y'))]
      const axisStroke = axisColour()
      const grid = gridColour()
      const bg = bgColour()

      const size = plotSize()
      chart = new uPlot({
        title,
        width: size.width,
        height: size.height,
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
            ...(s.dash ? { dash: s.dash } : {}),
          })),
        ],
        axes: [
          // Labels from the data's span, not uPlot's tick spacing — timeaxis.js.
          // Explicit colours: uPlot's default axis stroke is black, unreadable
          // on dark surfaces.
          {
            values: tickValues(data[0], document.documentElement.lang || undefined),
            space: tickSpace(data[0], document.documentElement.lang || undefined),
            // A fill chart (the map's panel) gives the axis title's row to the plot; the hover readout still names it.
            label: fill ? undefined : timeLabel || undefined,
            ...(fill ? { size: 32 } : {}),
            stroke: axisStroke,
            ticks: { stroke: axisStroke },
            grid: { stroke: grid },
          },
          ...scales.map((scale, i) => {
            // Colour and unit of the line measured against it — two unlabelled
            // columns of numbers cannot be attributed to either line.
            const line = lines.find((s) => (s.scale ?? 'y') === scale)
            return {
              scale,
              side: i === 0 ? 3 : 1,
              // Keeps the metric's colour identity, lifted just enough to
              // clear 4.5:1 on the current --bg (legibleStroke).
              stroke: line?.colour ? legibleStroke(line.colour, bg) : axisStroke,
              label: line?.unit || undefined,
              ticks: { stroke: axisStroke },
              // One grid only: two is a lattice nobody can read a value off.
              grid: { show: i === 0, stroke: grid },
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
      // E2E-only handle for the x labels' positions, which uPlot draws on canvas; stripped from a plain build.
      if (import.meta.env.VITE_E2E_MAP_HANDLE) chart.root.__uplot = chart

      // The first size was computed before uPlot had drawn its legend, so the
      // strip could only be assumed. Re-fit once against the real one; the
      // frame's height is fixed, so this cannot feed the observer below.
      if (resizable || fill) {
        const fitted = plotSize()
        if (fitted.width > 0 && fitted.height !== chart.height) chart.setSize(fitted)
      }

      // The container is fluid; a chart left at its first-paint width is
      // visibly wrong after a phone rotates. setSize does not re-fetch.
      observer = new ResizeObserver(() => {
        const next = plotSize()
        if (next.width > 0) chart.setSize(next)
      })
      observer.observe(resizable || fill ? frame : host)

      // theme.js toggles data-theme with no reload; "auto" follows the OS
      // scheme instead. Either needs a repaint or the chart keeps the old
      // theme's axis colours until the reader reloads.
      if (typeof MutationObserver !== 'undefined') {
        themeObserver = new MutationObserver(() => restyleAxes(chart, scales, lines))
        themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
      }
      if (typeof matchMedia === 'function') {
        media = matchMedia('(prefers-color-scheme: dark)')
        onMediaChange = () => restyleAxes(chart, scales, lines)
        media.addEventListener?.('change', onMediaChange)
      }
    })()

    return () => {
      cancelled = true
      observer?.disconnect()
      themeObserver?.disconnect()
      media?.removeEventListener?.('change', onMediaChange)
      chart?.destroy?.()
    }
  })
</script>

<div bind:this={frame} class="chart-frame" class:chart-frame--resizable={resizable} class:chart-frame--fill={fill}>
  <div bind:this={host} class="chart-host"></div>
</div>
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
  /* The map's panel has no height for a legend strip; its hover readout sits over the plot's top corner. */
  :global(.chart-frame--fill .chart-host) { position: relative; }
  :global(.chart-frame--fill .u-legend) { position: absolute; inset-block-start: 0; inset-inline-end: 0; margin: 0; background: var(--bg); border-radius: var(--radius); }
</style>
