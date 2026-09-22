import { test as base, expect } from './fixtures.js'

// One worker-scoped mobile context for the file: a fresh context per test
// re-downloads the bundle and trips the app's per-IP rate limit (429).
const test = base.extend({
  mobileCtx: [async ({ browser }, use) => {
    const context = await browser.newContext({ isMobile: true, hasTouch: true, deviceScaleFactor: 2 })
    await use(context)
    await context.close()
  }, { scope: 'worker' }],
})

test.describe('phone layout does not widen the viewport', () => {
  for (const path of ['/en', '/en/area/sofia#sensor=101']) {
    test(`${path} stays at 390px wide`, async ({ mobileCtx }) => {
      const page = await mobileCtx.newPage()
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto(path)
      expect(await page.evaluate(() => window.innerWidth)).toBe(390)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
      await page.close()
    })
  }
})

test.describe('landscape phone keeps the map', () => {
  test('/en map has width at 844x390', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 844, height: 390 })
    await page.goto('/en')
    const box = await page.locator('#map').boundingBox()
    expect(box.width).toBeGreaterThan(700)
    expect(box.height).toBeGreaterThan(150)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(844)
    await page.close()
  })

  test('/en rotating 390x844 -> 844x390 keeps the map, no reload', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const before = await page.locator('#map').boundingBox()
    expect(before.width).toBe(390)
    await page.setViewportSize({ width: 844, height: 390 })
    const after = await page.locator('#map').boundingBox()
    expect(after.width).toBeGreaterThan(700)
    expect(after.height).toBeGreaterThan(150)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(844)
    await page.close()
  })
})
