// The toolbar's refresh button. It knows nothing about the map: it asks the
// shared freshness store for a reload, and the map is what registered how to
// do one. Two islands that never meet, which is the point — the button renders
// with or without a map on the page.
import { mount as mountComponent } from 'svelte'
import RefreshButton from '../components/RefreshButton.svelte'
import { getFreshness } from '../lib/freshness.svelte.js'

export function mount(el) {
  const d = el.dataset
  const fresh = getFreshness()
  return mountComponent(RefreshButton, {
    target: el,
    props: {
      label: d.tLabel || '',
      variant: d.variant || 'toolbar',
      get busy() { return fresh.busy },
      onrefresh: () => fresh.request(),
    },
  })
}
