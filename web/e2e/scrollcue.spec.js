import { test, expect } from './fixtures.js'

// 390x844 is the iPhone-class phone; 393x873 at 2.75 is the Xiaomi 12X.
const PHONES = [
  { name: '390x844', viewport: { width: 390, height: 844 }, deviceScaleFactor: 2 },
  { name: 'xiaomi-12x', viewport: { width: 393, height: 873 }, deviceScaleFactor: 2.75 },
]

const phone = (browser, p) =>
  browser.newContext({ viewport: p.viewport, deviceScaleFactor: p.deviceScaleFactor, isMobile: true, hasTouch: true })

for (const p of PHONES) {
  test.describe(`scroll cue on ${p.name}`, () => {
    test('/en: the strip starts inside the first viewport', async ({ browser }) => {
      const context = await phone(browser, p)
      const page = await context.newPage()
      await page.goto('/en')
      const cue = page.locator('.scroll-cue')
      await expect(cue).toBeVisible()
      // The whole strip, not just its top pixel: a sliver at the fold is not a target.
      const box = await cue.boundingBox()
      expect(box.height).toBeGreaterThanOrEqual(44)
      expect(box.y + box.height).toBeLessThanOrEqual(p.viewport.height)
      await context.close()
    })

    // Area chrome varies, so the guarantee is for the map scrolled to the top.
    test('/en/area/sofia: with the map at the top, the whole strip is on screen', async ({ browser }) => {
      const context = await phone(browser, p)
      const page = await context.newPage()
      await page.goto('/en/area/sofia')
      await page.evaluate(() => document.querySelector('.map-shell').scrollIntoView({ block: 'start', behavior: 'instant' }))
      const m = await page.evaluate(() => ({
        shellTop: document.querySelector('.map-shell').getBoundingClientRect().top,
        cueBottom: document.querySelector('.scroll-cue').getBoundingClientRect().bottom,
        vh: innerHeight,
      }))
      expect(Math.abs(m.shellTop)).toBeLessThanOrEqual(1)
      expect(m.cueBottom).toBeLessThanOrEqual(m.vh)
      await context.close()
    })

    for (const path of ['/en', '/en/area/sofia']) {
      test(`${path}: clicking the strip brings #below-map to the top`, async ({ browser }) => {
        const context = await phone(browser, p)
        const page = await context.newPage()
        await page.goto(path)
        const hash = await page.evaluate(() => location.hash)
        await page.locator('.scroll-cue').click()
        // The home page is shorter than top-of-#below-map plus one viewport, so
        // there the scroll can only reach the document end; the area page is strict.
        const strict = path.includes('/area/')
        await expect.poll(() => page.evaluate((strict) => {
          const top = Math.abs(document.getElementById('below-map').getBoundingClientRect().top)
          const atEnd = scrollY >= document.documentElement.scrollHeight - innerHeight - 1
          return top <= 8 || (!strict && atEnd && scrollY > 0)
        }, strict)).toBe(true)
        expect(await page.evaluate(() => location.hash)).toBe(hash)
        await context.close()
      })
    }
  })
}

test('390x844 /en: the strip is hidden while the map is full screen', async ({ browser }) => {
  const context = await phone(browser, PHONES[0])
  const page = await context.newPage()
  await page.goto('/en')
  await expect(page.locator('.scroll-cue')).toBeVisible()
  await page.locator('.map__full').click()
  // Real fullscreen or the faux-full fallback, whichever the button applied.
  await expect.poll(() => page.evaluate(() => {
    const map = document.querySelector('#map')
    return document.fullscreenElement === map || map.classList.contains('map--faux-full')
  })).toBe(true)
  await expect(page.locator('.scroll-cue')).toBeHidden()
  await context.close()
})

test('the strip is not shown on a 1280x800 desktop', async ({ ctx }) => {
  const page = await ctx.newPage()
  await page.setViewportSize({ width: 1280, height: 800 })
  for (const path of ['/en', '/en/area/sofia']) {
    await page.goto(path)
    await expect(page.locator('#map, #area-map').first()).toBeVisible()
    await expect(page.locator('.scroll-cue')).toBeHidden()
  }
  await page.close()
})
