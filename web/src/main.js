// Island loader: one entry module, one pass over the document.
//
// A registry of DYNAMIC imports rather than static ones, so Rollup splits each
// island into its own chunk and the index page downloads the map chunk and never
// the chart chunk. A `for` loop over [data-island] rather than per-page entry
// points, because the server stays ignorant of which bundles exist — it only
// emits the attributes.
const ISLANDS = {
  map: () => import('./islands/map.js'),
  chart: () => import('./islands/chart.js'),
  switcher: () => import('./islands/switcher.js'),
}

// resolveLoader is the pure lookup at the heart of "leave the server-rendered
// fallback for anything this bundle does not know about". Exported so a test
// can pin the decision (known name -> a function, unknown name -> null)
// without touching the DOM.
export function resolveLoader(islandName) {
  return ISLANDS[islandName] ?? null
}

// runIsland mounts one island and swallows any failure so it cannot break the
// loop over the other islands, or leave an unhandled rejection on the page. It
// is a plain async function — not a `.then`/`.catch` chain built inline in the
// loop — precisely so it can be driven from a test with a plain {dataset}
// object and stub `load`/`mount` functions, with no real DOM element and no
// real MapLibre/Svelte bundle involved.
//
// Logged, not silent: every island's container sits BESIDE server-rendered
// content, never replacing it, so a broken bundle degrades to the
// server-rendered page instead of a blank div — but a developer still needs to
// see the failure in the console.
export async function runIsland(el, load, log = console.error) {
  try {
    const mod = await load()
    mod.mount(el)
    return true
  } catch (err) {
    log('island failed:', el.dataset.island, err)
    return false
  }
}

function init() {
  for (const el of document.querySelectorAll('[data-island]')) {
    const load = resolveLoader(el.dataset.island)
    if (!load) continue // unknown island: leave the server-rendered fallback
    runIsland(el, load)
  }
}

// Guarded so this module can be imported by a Vitest run (no `document`
// global, and none should be added — pure-logic-only, no jsdom) to exercise
// resolveLoader and runIsland without ever executing the DOM-walking loop.
if (typeof document !== 'undefined') init()
