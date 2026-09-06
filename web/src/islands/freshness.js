// The freshness line: what time the numbers are from, and whether they keep
// themselves current. On an area page it carries the refresh button too — the
// kit puts it there rather than in that page's toolbar, because on a page about
// one place the reload is chrome beside the data and not a screen-level action.
import { mount as mountComponent } from 'svelte'
import DataFreshness from '../components/DataFreshness.svelte'
import { getFreshness } from '../lib/freshness.svelte.js'
import { statusText } from '../lib/freshness.js'

export function mount(el, doc = document) {
  const d = el.dataset
  const fresh = getFreshness()
  const t = { updated: d.tUpdated || '', loading: d.tLoading || '', failed: d.tFailed || '' }
  const lang = doc.documentElement.getAttribute('lang') || 'bg'
  return mountComponent(DataFreshness, {
    target: el,
    props: {
      autoLabel: d.tAuto || '',
      button: d.button === 'true',
      buttonLabel: d.tLabel || '',
      get status() { return statusText(fresh, t, lang) },
      get auto() { return fresh.auto },
      get busy() { return fresh.busy },
      onauto: (on) => fresh.setAuto(on),
      onrefresh: () => fresh.request(),
    },
  })
}
