import { test, expect } from './fixtures.js'

// EN routes throughout — see metric.spec.js's header comment.
//
// "The map redraws itself several times per zoom" is only measurable in a real
// browser: the layers are painted by MapLibre from real network responses that
// arrive apart. paintSource (islands/map.js) announces every data-layer repaint
// as an `airbg:paint` event on the map container, and this file counts them.
test.describe.serial('one zoom, one redraw', () => {
  let page

  test.beforeAll(async ({ ctx }) => {
    page = await ctx.newPage()
    // Attached from a frame-by-frame poll installed before any page script
    // runs, not after goto(): the first paint of a cold load lands while
    // goto() is still returning, and a listener attached afterwards misses it.
    await page.addInitScript(() => {
      window.__paints = []
      const attach = () => {
        const el = document.querySelector('[data-island="map"]')
        if (!el) return requestAnimationFrame(attach)
        el.addEventListener('airbg:paint', (e) => {
          window.__paints.push({ source: e.detail.source, at: performance.now() })
        })
      }
      attach()
    })
  })

  test.afterAll(async () => {
    await page.close()
  })

  const resetPaints = () => page.evaluate(() => { window.__paints = [] })

  const paints = () => page.evaluate(() => window.__paints)

  // Both data layers painted at least once, which is what "the load has
  // settled" means here. Not networkidle: the basemap's tiles come from
  // tile.openstreetmap.org and the page refreshes itself on a timer, so the
  // network goes idle late, or never.
  const loaded = () => page.waitForFunction(() => {
    const seen = new Set((window.__paints ?? []).map((p) => p.source))
    return seen.has('airbg-data') && seen.has('airbg-hexes')
  }, null, { timeout: 20000 })

  // A keyboard zoom rather than a wheel: one keypress is one discrete zoom
  // step, where a wheel gesture is a stream of them and would not tell a
  // coalesced redraw from a debounced one. Focused rather than clicked — a
  // click on the canvas can select an area, which is a second camera move.
  async function zoomStep(key) {
    await page.locator('.maplibregl-canvas').focus()
    await page.keyboard.press(key)
    await page.waitForTimeout(3000)
  }

  test('a zoom step paints each layer once, in a single pass', async () => {
    // Sofia opens at the sensor tier (zoom 11), so one step out crosses into
    // the city tier — the markers change with the grid, which is the gesture
    // where two layers redraw and the double draw is visible.
    await page.goto('/en/area/sofia')
    await loaded()
    // The grid held back by most of a second, which is what a real visitor's
    // link does to the larger of the two responses. Against a localhost server
    // both land in the same millisecond and a redraw per layer is invisible.
    await page.route('**/api/v1/hexes*', async (route) => {
      await new Promise((r) => setTimeout(r, 800))
      await route.continue()
    })
    await resetPaints()

    await zoomStep('Minus')

    const seen = await paints()
    expect(seen.map((p) => p.source).sort(), 'both data layers redraw for a zoom')
      .toEqual(['airbg-data', 'airbg-hexes'])
    const perSource = {}
    for (const p of seen) perSource[p.source] = (perSource[p.source] ?? 0) + 1
    for (const [source, count] of Object.entries(perSource)) {
      expect(count, `${source} painted ${count} times for one zoom: ${JSON.stringify(seen)}`).toBe(1)
    }
    // Both layers flushed together: the spread is a tick, not the round trip
    // the route above added to one of them.
    const spread = Math.max(...seen.map((p) => p.at)) - Math.min(...seen.map((p) => p.at))
    expect(spread, `paints spread over ${Math.round(spread)}ms: ${JSON.stringify(seen)}`).toBeLessThan(250)
  })

  // The load path: the camera used to settle after the first paint, so a cold
  // load drew the country, then the placed view, then the moveend that placing
  // queued. No deep link here — an opening sensor panel repaints the markers to
  // mark itself, which is a redraw the reader asked for.
  test('a cold load paints each layer once', async () => {
    // Unthrottled: the delay the zoom test installs on the shared page would
    // push the grid past the load pass and into a moveend of its own.
    await page.unroute('**/api/v1/hexes*')
    await page.goto('/en/')
    await loaded()
    // A moment past the load, so a second, later paint would be caught rather
    // than merely not having happened yet.
    await page.waitForTimeout(2000)

    const seen = await paints()
    const perSource = {}
    for (const p of seen) perSource[p.source] = (perSource[p.source] ?? 0) + 1
    for (const [source, count] of Object.entries(perSource)) {
      expect(count, `${source} painted ${count} times for one load`).toBeLessThanOrEqual(1)
    }
  })
})
