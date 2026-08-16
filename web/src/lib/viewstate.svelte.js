// The reactive mirror of the URL hash. Every decision lives in viewstate.js;
// this file holds $state, the listener, and the one policy that needs the
// History API: which writes are destinations and which are settings.
import { parseHash, serialiseHash } from './viewstate.js'

export function createViewState({ metrics, defaultMetric, win = globalThis }) {
  const initial = parseHash(win.location.hash, { metrics, defaultMetric })
  let metric = $state(initial.metric)
  let sensorId = $state(initial.sensorId)

  // Programmatic writes change the hash, which fires hashchange, which would
  // re-parse and re-assign what we just set. Harmless for values, but it also
  // fires while the caller is mid-update. The flag makes our own echo a no-op.
  // A real browser never fires hashchange synchronously from pushState/
  // replaceState, so this guard is defensive in production, not load-bearing
  // there — only a fake `win` that echoes synchronously (as one of this
  // file's tests does) actually exercises it.
  let writing = false

  const onHashChange = () => {
    if (writing) return
    const next = parseHash(win.location.hash, { metrics, defaultMetric })
    metric = next.metric
    sensorId = next.sensorId
  }
  win.addEventListener('hashchange', onHashChange)

  // write() always serialises the WHOLE state, so the two keys cannot disagree
  // with what is rendered. An empty serialisation is written as the bare path,
  // not as '#' — otherwise the address bar keeps a dangling hash forever.
  function write(push) {
    const hash = serialiseHash({ metric, sensorId }, defaultMetric)
    const url = win.location.pathname + win.location.search + hash
    writing = true
    // finally, not a bare assignment after: real browsers enforce a
    // pushState/replaceState rate limit and can throw SecurityError. Without
    // this, a throw here would leave `writing` stuck true forever and the
    // store would silently stop reacting to every future hashchange — Back/
    // Forward and external hash links going dead with nothing surfaced.
    try {
      if (push) win.history.pushState(null, '', url)
      else win.history.replaceState(null, '', url)
    } finally {
      writing = false
    }
  }

  return {
    get metric() { return metric },
    get sensorId() { return sensorId },
    // A view setting: replaceState, so the back stack stays navigational.
    setMetric(next) {
      if (!metrics.includes(next) || next === metric) return
      metric = next
      write(false)
    },
    // A destination: pushState, so Back closes the panel and Back again leaves.
    openSensor(id) {
      if (id === sensorId) return
      sensorId = id
      write(true)
    },
    closeSensor() {
      if (sensorId === null) return
      sensorId = null
      write(true)
    },
    destroy() { win.removeEventListener('hashchange', onHashChange) },
  }
}

// One store per page, shared by every island. Two independent stores would each
// write the hash and clobber the other's key — the switcher would close the
// panel on every metric change.
let shared = null
export function getViewState(opts) {
  if (shared === null) shared = createViewState(opts)
  return shared
}
