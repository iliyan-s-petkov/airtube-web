// The readout strip, while a sensor is open.
//
// Two rows: this island's cards for the open sensor's own area on top, the
// server's cards below them, always. The lower row is never rebuilt in JS —
// that would be a second implementation of numbers the page already carries,
// free to drift from them — and closing the panel only has to drop the row
// above it.
import { mount as mountComponent, unmount } from 'svelte'
import Readouts from '../components/Readouts.svelte'
import { areaStats } from '../lib/areastats.js'
import { areaCards } from '../lib/areacards.js'
import { getViewState } from '../lib/viewstate.svelte.js'
import { getSensors, getSensorArea, getScales } from '../lib/sensors.svelte.js'
import { getMapAreas } from '../lib/mapareas.svelte.js'
import { parseMetricList, zipLabels } from '../lib/metrics.js'

// The area's own name, from the list the map last loaded. Absent when the
// reader zoomed straight past the tier that carries it, and the caller then
// falls back to a tier line that counts the sensors without naming the place.
export function areaName(areas, slug, lang) {
  const area = (areas ?? []).find((a) => a.slug === slug)
  if (!area) return ''
  return (lang === 'bg' ? area.name_bg : area.name_en) || ''
}

export function mount(el, doc = document) {
  const d = el.dataset
  const lang = doc.documentElement.getAttribute('lang') || 'bg'
  const metrics = parseMetricList(d.metrics)
  const labels = zipLabels(metrics, parseMetricList(d.metricLabels))
  const vs = getViewState({ metrics, defaultMetric: d.metric })

  const t = {
    high: d.tHigh || '', low: d.tLow || '', median: d.tMedian || '',
    thisSensor: d.tThisSensor || '', ofTotal: d.tOfTotal || '',
    aboveMedian: d.tAbove || '', belowMedian: d.tBelow || '', atMedian: d.tAt || '',
    areaSensors: d.tAreaSensors || '', sensorsOnly: d.tSensorsOnly || '',
  }

  // The server's strip is the first child; the sensor row goes above it.
  const served = el.querySelector('.readouts')
  const host = doc.createElement('div')
  el.insertBefore(host, served)

  function cards() {
    if (vs.sensorId == null) return []
    const metric = vs.metric
    const scale = (getScales() ?? []).find((s) => s.metric === metric) ?? null
    return areaCards(areaStats(getSensors(), metric, vs.sensorId), {
      metric,
      metricLabel: labels.find((o) => o.metric === metric)?.label || metric,
      scale,
      area: areaName(getMapAreas(), getSensorArea(), lang),
      lang,
      t,
    })
  }

  // .svelte.js, for this one $effect: hiding an empty row is a side effect on
  // a node outside the component, which no getter prop can express.
  const stop = $effect.root(() => {
    $effect(() => { host.hidden = cards().length === 0 })
  })

  const component = mountComponent(Readouts, { target: host, props: { get cards() { return cards() } } })
  return () => { stop(); unmount(component) }
}
