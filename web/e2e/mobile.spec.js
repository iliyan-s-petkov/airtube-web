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
