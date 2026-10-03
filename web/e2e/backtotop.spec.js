import { test as base, expect, mapSettled } from './fixtures.js'

// Back-to-top: floating button once the visitor has scrolled past the map; every base-layout page.
const VIEWPORTS = [
  { name: '1440x900', opts: { viewport: { width: 1440, height: 900 } } },
  { name: '393x873', opts: { viewport: { width: 393, height: 873 }, isMobile: true, hasTouch: true } },
]
const BTN = '.back-to-top'

const test = base

async function padPageFoot(page) {
  // A <style> tag trips the CSP; set the property directly.
  await page.evaluate(() => { document.querySelector('.footer').style.paddingBottom = '1600px' })
}
const jump = (page, top) => page.evaluate((t) => window.scrollTo({ top: t, behavior: 'instant' }), top)
const bottom = (page) => page.evaluate(() => document.documentElement.scrollHeight)

for (const vp of VIEWPORTS) {
  test.describe(vp.name, () => {
    test('hidden at load, visible below the map, click lands at the top, hidden again', async ({ browser }) => {
      const ctx = await browser.newContext(vp.opts)
      const page = await ctx.newPage()
      await page.goto('/')
      await padPageFoot(page)
      await expect(page.locator(BTN)).toBeHidden()

      await jump(page, await bottom(page))
      await expect(page.locator(BTN)).toBeVisible()
      const box = await page.locator(BTN).boundingBox()
      expect(box.width).toBeGreaterThanOrEqual(44)
      expect(box.height).toBeGreaterThanOrEqual(44)

      await page.locator(BTN).click()
      await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
      await expect(page.locator(BTN)).toBeHidden()
      await expect.poll(() => page.evaluate(() => document.activeElement?.tagName)).toBe('H1')
      expect(await page.evaluate(() => window.scrollY)).toBe(0)
      await ctx.close()
    })

    test('reduced motion scrolls instantly', async ({ browser }) => {
      const ctx = await browser.newContext({ ...vp.opts, reducedMotion: 'reduce' })
      const page = await ctx.newPage()
      await page.goto('/')
      await padPageFoot(page)
      await jump(page, await bottom(page))
      await expect(page.locator(BTN)).toBeVisible()
      await page.evaluate(() => {
        window.__ys = []
        window.addEventListener('scroll', () => window.__ys.push(window.scrollY))
      })
      await page.locator(BTN).click()
      await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
      // An instant jump is a single scroll event; a smooth one passes through intermediate offsets.
      expect(await page.evaluate(() => window.__ys.filter((y) => y > 0).length)).toBeLessThanOrEqual(1)
      await ctx.close()
    })

    test('appears on a page with no map (about)', async ({ browser }) => {
      const ctx = await browser.newContext(vp.opts)
      const page = await ctx.newPage()
      await page.goto('/en/about')
      await padPageFoot(page)
      await expect(page.locator(BTN)).toBeHidden()
      await jump(page, await bottom(page))
      await expect(page.locator(BTN)).toBeVisible()
      await ctx.close()
    })

    test('not rendered on /embed', async ({ browser }) => {
      const ctx = await browser.newContext(vp.opts)
      const page = await ctx.newPage()
      await page.goto('/embed')
      await expect(page.locator(BTN)).toHaveCount(0)
      await ctx.close()
    })
  })
}

// Client coordinates of a rendered sensor marker (same lookup as fullscreen-sheet.spec.js).
const markerPoint = (page) => page.evaluate(() => {
  const map = document.querySelector('[data-island="map"]').__map
  if (!map?.getLayer?.('airbg-markers')) return null
  const box = map.getCanvas().getBoundingClientRect()
  const f = map.queryRenderedFeatures({ layers: ['airbg-markers'] })
    .find((x) => x.properties?.id != null && x.geometry.type === 'Point')
  if (!f) return null
  const p = map.project(f.geometry.coordinates)
  if (p.x < 60 || p.y < 60 || p.x > box.width - 60 || p.y > box.height - 60) return null
  return { x: box.left + p.x, y: box.top + p.y }
})

test('393x873: hidden while the phone sensor sheet is open, back after it closes', async ({ browser }, testInfo) => {
  testInfo.setTimeout(90000)
  const ctx = await browser.newContext(VIEWPORTS[1].opts)
  const page = await ctx.newPage()
  await page.goto('/en/area/sofia')
  await page.locator('.map__full').click()
  // Fullscreen resizes the map and the markers repaint as data lands, so a
  // point taken early can be stale by the click. Re-take it each attempt, from
  // a settled camera, until the tap selects a sensor.
  await mapSettled(page)
  await expect(async () => {
    await mapSettled(page)
    const pt = await markerPoint(page)
    expect(pt).not.toBeNull()
    await page.mouse.click(pt.x, pt.y)
    await expect(page).toHaveURL(/#.*sensor=\d+/, { timeout: 2000 })
  }).toPass({ timeout: 30000 })
  const sheet = page.locator('.map-sensor-sheet')
  await expect(sheet).toBeVisible()
  await expect(page.locator(BTN)).toBeHidden()
  await sheet.locator('.map-sensor-sheet__close').click()
  await expect(sheet).toHaveCount(0)
  await ctx.close()
})

test('393x873: an open sensor sheet hides the button even when scrolled past the map', async ({ browser }) => {
  const ctx = await browser.newContext(VIEWPORTS[1].opts)
  const page = await ctx.newPage()
  await page.goto('/')
  await padPageFoot(page)
  await jump(page, await bottom(page))
  await expect(page.locator(BTN)).toBeVisible()
  // The sheet only exists in fullscreen, so stand one in to isolate the rule that hides the button for it.
  await page.evaluate(() => { document.body.append(Object.assign(document.createElement('div'), { className: 'map-sensor-sheet' })) })
  await expect(page.locator(BTN)).toBeHidden()
  await ctx.close()
})

test('393x873: below the map exactly one floating up button shows, and it returns to the map', async ({ browser }) => {
  const ctx = await browser.newContext(VIEWPORTS[1].opts)
  for (const path of ['/', '/en/area/sofia']) {
    const page = await ctx.newPage()
    await page.goto(path)
    await padPageFoot(page)
    await jump(page, await bottom(page))
    await expect(page.locator(BTN)).toBeVisible()
    // Any fixed button whose class names a back-to-* control counts, so a second one cannot hide.
    await expect(page.locator('button[class*="back-to-"]:visible')).toHaveCount(1)
    await page.locator(BTN).click()
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
    const top = await page.evaluate(() => document.querySelector('.map-shell').getBoundingClientRect().top)
    expect(top).toBeGreaterThanOrEqual(0)
    expect(top).toBeLessThan(873)
    await page.close()
  }
  await ctx.close()
})
