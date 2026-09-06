// The status filter over the area map's sensors. Owns no state: the choice
// lives in lib/sensorfilter.svelte.js, which the map subscribes to.
import { mount as mountComponent } from 'svelte'
import SensorBar from '../components/SensorBar.svelte'
import { parseMetricList } from '../lib/metrics.js'
import { getViewState } from '../lib/viewstate.svelte.js'

export function mount(el) {
  const d = el.dataset
  const metrics = parseMetricList(d.metrics)
  const vs = getViewState({ metrics, defaultMetric: d.metric })
  return mountComponent(SensorBar, {
    target: el,
    props: {
      // Getter, not vs.metric: see the component's own comment.
      get metric() { return vs.metric },
      texts: {
        legend: d.tLegend || '',
        all: d.tAll || '',
        active: d.tActive || '',
        inactive: d.tInactive || '',
        shown: d.tShown || '',
        of: d.tOf || '',
        sensors: d.tSensors || '',
        silent: d.tSilent || '',
      },
    },
  })
}
