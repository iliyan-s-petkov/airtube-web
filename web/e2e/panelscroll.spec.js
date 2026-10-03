import { test, expect } from './fixtures.js'

// OpenProject #575 (plan Task 1): on a phone a tapped sensor scrolls its card
// into view; close or Back scrolls back to where the reader was.
const PHONES = [
  { name: '393x873', viewport: { width: 393, height: 873 }, isMobile: true, hasTouch: true },
  { name: '873x393', viewport: { width: 873, height: 393 }, isMobile: true, hasTouch: true },
]
const DESKTOP = { name: '1280x800', viewport: { width: 1280, height: 800 } }
const PAGES = ['/', '/area/sofia']

// Map at the top of the viewport, over the seeded sensors, once the opening camera has settled.
async function prepareMap(page, path) {
  await page.goto(path)
  await page.waitForFunction(() => document.querySelector('[data-island="map"]')?.__map?.isStyleLoaded?.())
  await page.waitForTimeout(1000)
  await expect.poll(() => page.evaluate(() => document.querySelector('[data-island="map"]').__map.isMoving())).toBe(false)
  await page.evaluate(() => {
    document.querySelector('.map-shell').scrollIntoView({ block: 'start', behavior: 'instant' })
    document.querySelector('[data-island="map"]').__map.jumpTo({ center: [23.32, 42.69], zoom: 11 })
  })
}

// Client coordinates of a station on `layer`: a marker, or the centre of a hex
// cell naming one station. Clear of the sticky masthead and the frame's edges,
// and not under an overlay (legend, controls, fullscreen sheet) — a point the
// map itself is not what a real tap would land on.
const featurePoint = (page, layer) => page.evaluate((layer) => {
  const map = document.querySelector('[data-island="map"]').__map
  if (!map?.getLayer?.(layer)) return null
  const box = map.getCanvas().getBoundingClientRect()
  for (const f of map.queryRenderedFeatures({ layers: [layer] })) {
    const g = f.geometry
    let c = null
    if (g.type === 'Point' && f.properties?.id != null) c = g.coordinates
    else if (g.type === 'Polygon' && f.properties?.sensorId != null) {
      const ring = g.coordinates[0].slice(0, -1)
      c = [0, 1].map((i) => ring.reduce((a, p) => a + p[i], 0) / ring.length)
    }
    if (!c) continue
    const p = map.project(c)
    const y = box.top + p.y
    if (p.x < 30 || p.x > box.width - 30 || y < 80 || p.y > box.height - 30) continue
    const x = box.left + p.x
    if (document.elementFromPoint(x, y) !== map.getCanvas()) continue
    return { x, y }
  }
  return null
}, layer)

// Home has no point tier until an area is picked; a cell naming one station opens it there.
async function tapSensor(page) {
  const layer = new URL(page.url()).pathname.includes('/area/') ? 'airbg-markers' : 'airbg-hex-fill'
  let pt = null
  // Fullscreen resizes the canvas after the click; take the point once it holds still for 200ms.
  await expect.poll(async () => {
    const a = await featurePoint(page, layer)
    await page.waitForTimeout(200)
    pt = await featurePoint(page, layer)
    return a && pt && a.x === pt.x && a.y === pt.y
  }, { timeout: 20000 }).toBe(true)
  await page.mouse.click(pt.x, pt.y)
  await expect(page).toHaveURL(/#.*sensor=\d+/)
}

// Resolves once scrollY has held still for 300ms (smooth scrolls take a while).
const settled = (page) => page.evaluate(() => new Promise((resolve) => {
  let last = window.scrollY
  let still = 0
  const id = setInterval(() => {
    if (window.scrollY === last) still += 50
    else { still = 0; last = window.scrollY }
    if (still >= 300) { clearInterval(id); resolve(window.scrollY) }
  }, 50)
}))

async function inViewport(page, locator) {
  const b = await locator.boundingBox()
  const vh = page.viewportSize().height
  return b !== null && b.y >= 0 && b.y + b.height <= vh
}

for (const vp of PHONES) {
  for (const path of PAGES) {
    const shot = `${vp.name}-${path === '/' ? 'home' : 'area'}`

    test(`${vp.name} ${path}: tapping a sensor brings its card into view, close returns to the map`, async ({ browser }, testInfo) => {
      testInfo.setTimeout(60000)
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await prepareMap(page, path)
      const before = await settled(page)

      await tapSensor(page)
      const panel = page.locator('[data-island="panel"] .sensor-panel')
      await expect(panel.locator('h2')).toBeVisible()
      const opened = await settled(page)
      expect(opened, 'the page did not scroll to the card').toBeGreaterThan(before + 50)
      expect(await inViewport(page, panel.locator('h2'))).toBe(true)
      expect(await inViewport(page, panel.locator('.panel-close'))).toBe(true)
      expect(await inViewport(page, panel.locator('.gauges'))).toBe(true)
      await page.screenshot({ path: `/tmp/mux21-${shot}-after-open.png` })

      // Reading down the card: the close pill stays pinned.
      await page.evaluate(() => window.scrollBy({ top: 250, behavior: 'instant' }))
      await settled(page)
      expect(await inViewport(page, panel.locator('.panel-close')), 'close scrolled away').toBe(true)
      await page.screenshot({ path: `/tmp/mux21-${shot}-before-close.png` })

      await panel.locator('.panel-close').click()
      await expect(panel).toHaveCount(0)
      const back = await settled(page)
      expect(Math.abs(back - before), `scrollY ${back} vs ${before} before opening`).toBeLessThanOrEqual(2)
      await context.close()
    })

    test(`${vp.name} ${path}: Back after an auto-scrolled open returns to the map`, async ({ browser }, testInfo) => {
      testInfo.setTimeout(60000)
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await prepareMap(page, path)
      // Chromium restores scroll on a same-document Back by itself; the mode is stored
      // per entry, so it is set before the open to prove our own restore runs.
      await page.evaluate(() => { history.scrollRestoration = 'manual' })
      const before = await settled(page)

      await tapSensor(page)
      await expect(page.locator('[data-island="panel"] .sensor-panel h2')).toBeVisible()
      expect(await settled(page)).toBeGreaterThan(before + 50)

      await page.goBack()
      await expect(page.locator('[data-island="panel"] .sensor-panel')).toHaveCount(0)
      const back = await settled(page)
      expect(Math.abs(back - before), `scrollY ${back} vs ${before} before opening`).toBeLessThanOrEqual(2)
      await context.close()
    })

    test(`${vp.name} ${path}: a deep-linked sensor does not scroll the page on load`, async ({ browser }, testInfo) => {
      testInfo.setTimeout(60000)
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await page.goto(`${path}#sensor=101`)
      await expect(page.locator('[data-island="panel"] .sensor-panel .gauges')).toBeVisible({ timeout: 15000 })
      expect(await settled(page)).toBe(0)
      await context.close()
    })
  }

  test(`${vp.name}: a sensor tapped in fullscreen does not scroll the page`, async ({ browser }, testInfo) => {
    testInfo.setTimeout(60000)
    const { name, ...opts } = vp
    const context = await browser.newContext(opts)
    const page = await context.newPage()
    await prepareMap(page, '/area/sofia')
    // The document does not scroll behind a fullscreen frame, so count the attempts instead,
    // with the card pushed well below the fold so an unguarded open would try.
    await page.evaluate(() => {
      document.querySelector('[data-island="panel"]').style.marginBlockStart = '2000px'
      window.__panelScrolls = 0
      const orig = Element.prototype.scrollIntoView
      Element.prototype.scrollIntoView = function (...a) {
        if (this.matches('.sensor-panel')) window.__panelScrolls++
        return orig.apply(this, a)
      }
    })
    await page.locator('.map__full').click()
    await expect.poll(() => page.evaluate(() => !!document.querySelector('.map:fullscreen, .map--faux-full'))).toBe(true)
    await tapSensor(page)
    await expect(page.locator('.map-sensor-sheet')).toBeVisible()
    await page.waitForTimeout(300)
    expect(await page.evaluate(() => window.__panelScrolls)).toBe(0)
    await context.close()
  })
}

test(`${DESKTOP.name}: a tapped sensor docks over the map and does not scroll the page`, async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { name, ...opts } = DESKTOP
  const context = await browser.newContext(opts)
  const page = await context.newPage()
  await prepareMap(page, '/area/sofia')
  const before = await settled(page)
  await tapSensor(page)
  await expect(page.locator('.map-dock h2')).toBeVisible()
  expect(await settled(page)).toBe(before)
  await context.close()
})

test('900x600: a tapped sensor scrolls the card under the map into view', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const context = await browser.newContext({ viewport: { width: 900, height: 600 } })
  const page = await context.newPage()
  await prepareMap(page, '/area/sofia')
  const before = await settled(page)
  await tapSensor(page)
  await expect(page.locator('[data-island="panel"] .sensor-panel h2')).toBeVisible()
  expect(await settled(page)).toBeGreaterThan(before + 50)
  await context.close()
})

// OpenProject #697 PR B: a touch screen from 1024px on the home page shows the sensor in the map's panel only;
// the section under the map is hidden, so the open must not scroll to it.
test('touch 1280x800 /: a tapped sensor docks over the map and the page does not scroll', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, isMobile: true, hasTouch: true })
  const page = await context.newPage()
  await prepareMap(page, '/')
  const before = await settled(page)
  await tapSensor(page)
  await expect(page.locator('.map-dock h2')).toBeVisible()
  await expect(page.locator('[data-island="panel"]')).toBeHidden()
  expect(await settled(page)).toBe(before)
  await context.close()
})
