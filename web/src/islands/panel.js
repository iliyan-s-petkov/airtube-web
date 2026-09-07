// The panel island. Wires SensorPanel.svelte to the same viewstate singleton
// the map and switcher share (see viewstate.svelte.js), and to the sensor
// registry the map island publishes into (lib/sensors.svelte.js) — never a
// fetch of its own for the panel's own content, see that file's comment.
//
// No component tree here reacts imperatively: every prop below is a plain
// getter, the same idiom islands/switcher.js already uses for `selected`.
// SensorPanel.svelte's own $props()/template reads are the reactive
// consumer, so a read of vs.sensorId or the registry made INSIDE one of
// these getters is tracked exactly as if it happened in a .svelte file —
// dependency tracking follows the read, not the file it is written in. That
// is what lets this file stay plain .js (no $effect, no wrapper component)
// while still repainting on a metric-store-shared mutation it did not
// itself trigger (a marker click handled in islands/map.js).
import { mount as mountComponent, unmount, createRawSnippet } from 'svelte'
import SensorPanel from '../components/SensorPanel.svelte'
import SensorChart from '../components/SensorChart.svelte'
import { panelRows, detailRows } from '../lib/sensorview.js'
import { parseMetricList, zipLabels } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { findSensor, getScales, normaliseSensor } from '../lib/sensors.svelte.js'

// Re-exported, not re-implemented: the projection lives in
// lib/sensors.svelte.js because findSensor (the registry's own lookup) needs
// it too, and a lib module cannot import from an island (islands depend on
// lib, never the reverse — see every other island in this directory).
// Exported here as well because this is the file the task's own interface
// names as normaliseSensor's home.
export { normaliseSensor }

// Only 'out_of_range', 'stuck' and 'spatial_outlier' have catalogue entries
// (panel.flag.*, internal/i18n/{bg,en}.json) — 'ok' and 'no_neighbours'
// deliberately do not, because neither is a failure. flagTextFor's lookup
// miss (any flag not a key of `catalogue`, including those two, and
// including anything this frontend does not yet recognise) falls through to
// '', never to the server's i18n miss-marker ('!key!'): that marker is a
// Go-side concept (internal/i18n/i18n.go) produced by Catalogue.T, and
// mount() below never calls it — it only reads the three data-t-flag-*
// attributes the template renders (see area.gohtml), so '!key!' cannot
// reach this function's input in the first place. Exported (rather than a
// closure inside mount()) so this guard is provable without mounting a
// component or touching the DOM.
export function flagTextFor(flag, catalogue) {
  return catalogue[flag] || ''
}

export function mount(el) {
  const d = el.dataset
  const metrics = parseMetricList(d.metrics)
  const options = zipLabels(metrics, parseMetricList(d.metricLabels))
  const vs = getViewState({ metrics, defaultMetric: d.metric })

  const flagCatalogue = {
    out_of_range: d.tFlagOutOfRange || '',
    stuck: d.tFlagStuck || '',
    spatial_outlier: d.tFlagSpatialOutlier || '',
  }

  // Memoised by sensor id: `chart` below is read on EVERY reactive
  // re-evaluation of the props object (a pan that brings in new sensors, a
  // metric switch that does not even touch this sensor's rows, scales
  // finally loading) — none of those should tear down and remount the chart
  // that is already on screen and mid-fetch. Only the OPEN sensor changing
  // should.
  let chartId = null
  let chartSnippet = null
  function chartFor(sensor) {
    const id = sensor?.id ?? null
    if (id === chartId) return chartSnippet
    chartId = id
    chartSnippet = id === null ? null : buildChartSnippet(sensor, options, d)
    return chartSnippet
  }

  const detailLabels = {
    devices: d.tDetailDevices || '',
    hardware: d.tDetailHardware || '',
    since: d.tDetailSince || '',
    updated: d.tDetailUpdated || '',
    coords: d.tDetailCoords || '',
  }
  // The page's own language, so dates read the way the rest of the page does.
  // document.documentElement.lang is what the server rendered; undefined (the
  // browser's own locale) only if the attribute is missing.
  const locale = document.documentElement.lang || undefined

  mountComponent(SensorPanel, {
    target: el,
    props: {
      get open() { return findSensor(vs.sensorId) !== null },
      get rows() {
        const sensor = findSensor(vs.sensorId)
        return sensor ? panelRows(sensor, options, getScales()) : []
      },
      // Composed from the (non-templated) i18n label plus the sensor id —
      // see SensorPanel.svelte's own comment on why `sensor` itself is not
      // one of its props.
      get title() {
        const sensor = findSensor(vs.sensorId)
        return sensor ? `${d.tTitle || ''} ${sensor.id}` : ''
      },
      get flagText() {
        const sensor = findSensor(vs.sensorId)
        return sensor ? flagTextFor(sensor.flag, flagCatalogue) : ''
      },
      closeLabel: d.tClose || '',
      noValue: d.tNoValue || '',
      detailsLabel: d.tDetails || '',
      get details() {
        const sensor = findSensor(vs.sensorId)
        return sensor ? detailRows(sensor, detailLabels, locale) : []
      },
      onclose: () => vs.closeSensor(),
      // Keyed by STATION now, not by the device charted: the chart component
      // owns which metric it is drawing (and therefore which device it asks),
      // so remounting it when the reader switches metric would throw away the
      // selection that caused the switch.
      get chart() {
        return chartFor(findSensor(vs.sensorId))
      },
    },
  })
}

// buildChartSnippet wraps Chart.svelte in a snippet built from vanilla JS via
// createRawSnippet — the escape hatch for exactly this: panel.js is plain
// .js, so it cannot write `{#snippet}...{/snippet}` (compiler syntax, only
// valid inside .svelte files), but SensorPanel.svelte's `chart` prop is a
// snippet, not a component reference.
//
// The metric and period the chart OPENS on come from data-metric/data-period
// on the panel's own container, not from vs.metric: the panel lists every
// metric a sensor reports regardless of which metric the map is currently
// coloured by (see lib/sensorview.js), so tying the embedded chart to the
// map's live metric would make the panel disagree with itself the moment a
// visitor switches metric while it is open. After that the reader owns both,
// inside SensorChart.
//
// The metric list offered is this station's, not the map's seven: the switcher
// must not offer a metric whose only possible answer is an empty plot.
function buildChartSnippet(sensor, options, d) {
  const measured = options.filter(({ metric }) => Object.hasOwn(sensor.values, metric))
  return createRawSnippet(() => ({
    render: () => '<div></div>',
    setup: (node) => {
      const component = mountComponent(SensorChart, {
        target: node,
        props: {
          stationId: sensor.id,
          sources: sensor.sources,
          options: measured,
          periods: parseMetricList(d.periods),
          periodLabels: parseMetricList(d.periodLabels),
          initialPeriod: d.period,
          initialMetric: d.metric,
          metricLegend: d.tChartMetricLegend || '',
          periodLegend: d.tChartPeriodLegend || '',
          compareLabel: d.tChartCompare || '',
          compareNone: d.tChartCompareNone || '',
          primaryColour: d.lineColour,
          compareColour: compareColour(),
          timeLabel: d.tChartTime || '',
          empty: d.tChartEmpty || '',
          unavailable: d.tChartUnavailable || '',
        },
      })
      return () => unmount(component)
    },
  }))
}

// The second line's colour comes from the stylesheet, not from config: it is a
// theme decision like every other colour on the page, and reading the token
// keeps the light and dark themes in charge of it. A canvas cannot inherit a
// CSS variable, which is why it has to be read out explicitly.
// The token has to live in app.css, not in theme.css: base.gohtml loads
// theme.css only in its {{else}} branch, so in production — where the built kit
// theme wins — --chart-compare resolved to '', uPlot drew the compared metric
// with no stroke, and a reader who picked a second metric saw the same single
// line. app.css is the one sheet loaded on both branches.
function compareColour() {
  return getComputedStyle(document.documentElement).getPropertyValue('--chart-compare').trim()
}
