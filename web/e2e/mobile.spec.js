import { test, expect } from './fixtures.js'

// A context of its own, like the JS-disabled smoke test: the phone viewport
// has to be set before the context exists, so it cannot ride the shared one.
const PHONE = { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 }

test.describe('phone layout does not widen the viewport', () => {
  for (const path of ['/en', '/en/area/sofia#sensor=101']) {
    test(`${path} stays at 390px wide`, async ({ browser }) => {
      const context = await browser.newContext(PHONE)
      const page = await context.newPage()
      await page.goto(path)
      expect(await page.evaluate(() => window.innerWidth)).toBe(390)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
      await context.close()
    })
  }
})

const LANDSCAPE = { viewport: { width: 844, height: 390 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 }

test.describe('landscape phone keeps the map', () => {
  test('/en map has width at 844x390', async ({ browser }) => {
    const context = await browser.newContext(LANDSCAPE)
    const page = await context.newPage()
    await page.goto('/en')
    const box = await page.locator('#map').boundingBox()
    expect(box.width).toBeGreaterThan(700)
    expect(box.height).toBeGreaterThan(150)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(844)
    await context.close()
  })

  test('/en rotating 390x844 -> 844x390 keeps the map, no reload', async ({ browser }) => {
    const context = await browser.newContext(PHONE)
    const page = await context.newPage()
    await page.goto('/en')
    const before = await page.locator('#map').boundingBox()
    expect(before.width).toBe(390)
    await page.setViewportSize({ width: 844, height: 390 })
    const after = await page.locator('#map').boundingBox()
    expect(after.width).toBeGreaterThan(700)
    expect(after.height).toBeGreaterThan(150)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(844)
    await context.close()
  })
})
