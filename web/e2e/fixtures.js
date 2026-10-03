import { test as base, expect } from '@playwright/test'

// One browser context for the whole run, because the rate limiter counts the
// static assets too. ratelimit.api (airbg.yaml) is a single per-IP bucket
// wrapping the WHOLE server, and a fresh Playwright context starts with an
// empty disk cache — so every spec file that opened its own context re-spent
// ~30 burst tokens on chunks the browser already had, and the file that
// happened to run when the bucket ran dry failed with its islands missing.
// A page per spec file, all from this one context: separate history, shared
// cache. Worker-scoped, and workers is 1, so the run pays for the assets once.
export const test = base.extend({
  ctx: [async ({ browser }, use) => {
    const context = await browser.newContext()
    await use(context)
    await context.close()
  }, { scope: 'worker' }],

  // The shared context keeps its HTTP cache (see above) but must not keep its
  // state: legend fold, layer toggles, wind and metric choices all persist in
  // localStorage, so a spec that flips one would otherwise hand the next spec
  // on the same origin a different starting page, and which spec that is
  // varies with run order. Cleared after each test, on every page the context
  // still holds. A spec that seeds storage does so with addInitScript on its
  // own page, which runs on the next navigation, after this.
  isolateSharedContext: [async ({ ctx }, use) => {
    await use()
    await ctx.clearCookies()
    // A spec that closed every page leaves nothing above to clear, and the
    // origin's storage outlives it; a throwaway page on the origin clears that.
    const open = ctx.pages().filter((p) => !p.isClosed() && p.url() !== 'about:blank')
    const sweeper = open.length ? null : await ctx.newPage()
    await sweeper?.goto('/robots.txt').catch(() => {})
    for (const page of ctx.pages()) {
      if (page.isClosed() || page.url() === 'about:blank') continue
      await page.evaluate(() => {
        localStorage.clear()
        sessionStorage.clear()
      }).catch(() => {})
    }
    await sweeper?.close()
  }, { auto: true }],
})

// Resolves once the map island has mounted, its style is loaded and its opening
// camera has stopped. The legend, layers menu and pull tab are built by that
// island and the page reflows when it lands, so asserting or measuring earlier
// races hydration: on a slow CI runner the map chunk arrives seconds after the
// load event. waitForFunction is not capped by the 5s expect timeout.
export const mapSettled = (page) => page.waitForFunction(() => {
  const map = document.querySelector('[data-island="map"]')?.__map
  return !!map?.isStyleLoaded?.() && !map.isMoving()
}, null, { timeout: 45_000 })

// A real wheel zoom and mouse drag on the canvas, not a programmatic jump.
// A gesture sent while the opening camera is still settling is swallowed, so
// each one is retried until the camera has actually moved, and the debounced
// write is awaited by value, not by a delay.
const cam = (page) => page.evaluate(() => {
  const map = document.querySelector('[data-island="map"]').__map
  const c = map.getCenter()
  return { lng: c.lng, lat: c.lat, zoom: map.getZoom() }
})

export const userMove = async (page) => {
  const box = await page.locator('[data-island="map"]').boundingBox()
  const cx = box.x + box.width / 2
  const cy = box.y + box.height / 2
  await mapSettled(page)

  const z0 = (await cam(page)).zoom
  await expect(async () => {
    await page.mouse.move(cx, cy)
    await page.mouse.wheel(0, -400)
    await mapSettled(page)
    expect((await cam(page)).zoom).toBeGreaterThan(z0 + 0.1)
  }).toPass({ timeout: 20_000 })

  const c0 = await cam(page)
  await expect(async () => {
    await page.mouse.move(cx, cy)
    await page.mouse.down()
    await page.mouse.move(cx - 120, cy + 60, { steps: 8 })
    await page.mouse.up()
    await mapSettled(page)
    const c = await cam(page)
    expect(Math.abs(c.lng - c0.lng) + Math.abs(c.lat - c0.lat)).toBeGreaterThan(0.001)
  }).toPass({ timeout: 20_000 })

  // The write is debounced per moveend and an earlier camera may already be
  // stored, so wait until the stored view is the camera the map is at now
  // (stored rounded: zoom 2dp, centre 4dp).
  await expect.poll(() => page.evaluate(() => {
    const map = document.querySelector('[data-island="map"]').__map
    const c = map.getCenter()
    const raw = localStorage.getItem('kanarche:map-view')
    if (!raw) return false
    const v = JSON.parse(raw)
    return Math.abs(v.zoom - map.getZoom()) < 0.006 &&
      Math.abs(v.lng - c.lng) < 0.00006 && Math.abs(v.lat - c.lat) < 0.00006
  }), { timeout: 15_000 }).toBe(true)
}

export { expect }
