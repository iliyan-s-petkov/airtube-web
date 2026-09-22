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

  test('/en first viewport: masthead one line, map above the fold', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const masthead = await page.locator('.masthead').boundingBox()
    expect(masthead.height).toBeLessThanOrEqual(56)
    const toolbar = await page.locator('.toolbar').boundingBox()
    expect(toolbar.height).toBeLessThanOrEqual(64)
    const map = await page.locator('#map').boundingBox()
    expect(map.y).toBeLessThanOrEqual(200)
    expect(map.height).toBeGreaterThanOrEqual(0.6 * 844)
    await page.close()
  })

  // The on-map chrome: the legend pill sits on the map, every overlay is a
  // real touch target, and the zoom pair yields to pinch on a coarse pointer.
  test('/en on-map chrome: legend on the map, 44px targets, zoom hidden', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const map = await page.locator('#map').boundingBox()
    const scale = await page.locator('.scale--onmap').boundingBox()
    expect(scale.x).toBeGreaterThanOrEqual(map.x)
    expect(scale.y).toBeGreaterThanOrEqual(map.y)
    expect(scale.x + scale.width).toBeLessThanOrEqual(map.x + map.width)
    expect(scale.y + scale.height).toBeLessThanOrEqual(map.y + map.height)
    for (const sel of ['.map__layers', '.map-locate', '.map__full']) {
      const box = await page.locator(sel).boundingBox()
      expect(box.width).toBeGreaterThanOrEqual(44)
      expect(box.height).toBeGreaterThanOrEqual(44)
    }
    await expect(page.locator('.map-zoom__btn[data-act="in"]')).toBeHidden()
    await expect(page.locator('.map-zoom__btn[data-act="out"]')).toBeHidden()
    await page.close()
  })

  // MapLibre's compact attribution opens itself on load; collapsed to the (i)
  // it does not sit open over the map, and the button is a real touch target.
  test('/en attribution collapses to the (i), at a 44px target', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const attrib = page.locator('.maplibregl-ctrl-attrib')
    await expect(attrib).toBeVisible()
    await expect(attrib).not.toHaveClass(/maplibregl-compact-show/)
    const box = await page.locator('.maplibregl-ctrl-attrib-button').boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(44)
    expect(box.height).toBeGreaterThanOrEqual(44)
    // The wind note stays hidden without a seeded forecast (/api/v1/wind
    // 503s in this fixture) — force it open the same way chrome.showWind(true, …)
    // does, so the phone rules below get a real element to measure.
    await page.evaluate(() => { document.querySelector('.map-wind-label').hidden = false })
    const label = page.locator('.map-wind-label__text-label')
    if (await label.count()) {
      const labelBox = await label.boundingBox()
      expect(labelBox.width).toBeLessThanOrEqual(1)
    }
    const toggle = page.locator('.map-wind-label__toggle')
    if (await toggle.isVisible()) {
      const mapBox = await page.locator('#map').boundingBox()
      const toggleBox = await toggle.boundingBox()
      expect(toggleBox.y + toggleBox.height).toBeLessThanOrEqual(mapBox.y + mapBox.height - 44)
    }
    await page.close()
  })

  // Folded by default on a phone (Task 5b); tapping it unfolds to a real
  // width and rides above .map-freshness rather than under it.
  test('/en legend pill: folded by default, full width and on top when open', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const scale = page.locator('.scale--onmap')
    await expect(scale).not.toHaveAttribute('open', '')
    const toggle = page.locator('.scale__toggle')
    await expect(toggle).toBeVisible()
    // Retrying poll rather than a single boundingBox() read: a debounced
    // repaint (moveend, once the opening jumpTo settles) can replace the
    // legend's children between two separate round trips to the browser.
    await expect.poll(async () => (await toggle.boundingBox())?.height ?? 0)
      .toBeGreaterThanOrEqual(44)

    await toggle.click()

    await expect(scale).toHaveAttribute('open', '')
    await expect.poll(async () => (await scale.boundingBox())?.width ?? 0)
      .toBeGreaterThanOrEqual(250)
    await expect.poll(async () => (await page.locator('.scale__bands--vertical').boundingBox())?.width ?? 0)
      .toBeGreaterThanOrEqual(250)
    const openBox = await scale.boundingBox()
    const fresh = await page.locator('.map-freshness').boundingBox()
    const scaleZ = await scale.evaluate((el) => Number(getComputedStyle(el).zIndex))
    const freshZ = await page.locator('.map-freshness').evaluate((el) => Number(getComputedStyle(el).zIndex))
    const scaleAboveFresh = openBox.y + openBox.height <= fresh.y || scaleZ > freshZ
    expect(scaleAboveFresh).toBe(true)
    await page.close()
  })
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
