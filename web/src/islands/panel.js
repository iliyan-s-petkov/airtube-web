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
import Chart from '../components/Chart.svelte'
import { panelRows } from '../lib/sensorview.js'
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
  function chartFor(id) {
    if (id === chartId) return chartSnippet
    chartId = id
    chartSnippet = id === null ? null : buildChartSnippet(id, d)
    return chartSnippet
  }

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
      onclose: () => vs.closeSensor(),
      get chart() {
        const sensor = findSensor(vs.sensorId)
        return chartFor(sensor?.id ?? null)
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
// url is built the same way chart.js (the area-level chart island) builds
// its own: from data-metric/data-period on the panel's OWN container, not
// from vs.metric. The panel lists every metric a sensor reports regardless
// of which metric the map is currently coloured by (see lib/sensorview.js),
// so tying the embedded chart to the map's live metric would make the panel
// disagree with itself the moment a visitor switches metric while it is
// open.
function buildChartSnippet(id, d) {
  const url = `/api/v1/sensor/${encodeURIComponent(id)}/series` +
    `?metric=${encodeURIComponent(d.metric)}&period=${encodeURIComponent(d.period)}`
  return createRawSnippet(() => ({
    render: () => '<div></div>',
    setup: (node) => {
      const component = mountComponent(Chart, {
        target: node,
        props: {
          url,
          lineColour: d.lineColour,
          title: d.tChartTitle || '',
          valueLabel: d.tChartValue || '',
          timeLabel: d.tChartTime || '',
          empty: d.tChartEmpty || '',
          unavailable: d.tChartUnavailable || '',
        },
      })
      return () => unmount(component)
    },
  }))
}
