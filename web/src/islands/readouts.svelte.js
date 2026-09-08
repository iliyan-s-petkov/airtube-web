// The readout strip, while a sensor is open.
//
// The server's four national cards stay in the DOM untouched; this island
// mounts a second strip beside them and hides one or the other. Rebuilding the
// national figures in JS would mean a second implementation of numbers the page
// already carries, free to drift from them — and closing the panel has to put
// back exactly what was there, not a recomputation of it.
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

  const host = doc.createElement('div')
  el.appendChild(host)

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

  // The national strip is the server's first child; it goes away only while
  // there is a full replacement to show. A sensor with no comparable
  // neighbours leaves it standing rather than blanking the strip.
  // .svelte.js, for this one $effect: the swap is a side effect on a node the
  // server rendered, which no getter prop can express.
  const served = el.querySelector('.readouts')
  const stop = $effect.root(() => {
    $effect(() => {
      const shown = cards().length > 0
      host.hidden = !shown
      if (served) served.hidden = shown
    })
  })

  const component = mountComponent(Readouts, { target: host, props: { get cards() { return cards() } } })
  return () => { stop(); unmount(component) }
}
