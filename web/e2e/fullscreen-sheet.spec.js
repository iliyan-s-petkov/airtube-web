import { test, expect } from './fixtures.js'

// B3: in fullscreen a tapped sensor's readings show in a sheet inside the
// fullscreen element, since the panel under the map is outside it.

const VIEWPORTS = [
  { name: 'phone 393x873', viewport: { width: 393, height: 873 }, isMobile: true, hasTouch: true },
  { name: 'desktop 1280x800', viewport: { width: 1280, height: 800 } },
]

// Client coordinates of a rendered sensor marker, once the sensor tier has painted.
const markerPoint = (page) => page.evaluate(() => {
  const map = document.querySelector('[data-island="map"]').__map
  if (!map?.isStyleLoaded?.()) return null
  const box = map.getCanvas().getBoundingClientRect()
  const f = map.queryRenderedFeatures({ layers: ['airbg-markers'] })
    .find((x) => x.properties?.id != null && x.geometry.type === 'Point')
  if (!f) return null
  const p = map.project(f.geometry.coordinates)
  if (p.x < 60 || p.y < 60 || p.x > box.width - 60 || p.y > box.height - 60) return null
  return { x: box.left + p.x, y: box.top + p.y }
})

// The frame the button put in fullscreen: the real one, or the faux-full fallback.
const fullFrame = (page) => page.evaluate(() => {
  const map = document.querySelector('[data-island="map"]')
  return document.fullscreenElement === map || map.classList.contains('map--faux-full')
})

for (const vp of VIEWPORTS) {
  test(`${vp.name}: a sensor tapped in fullscreen shows its readings in a sheet`, async ({ browser }, testInfo) => {
    testInfo.setTimeout(60000)
    const { name, ...opts } = vp
    const context = await browser.newContext(opts)
    const page = await context.newPage()
    await page.goto('/en/area/sofia')

    await page.locator('.map__full').click()
    await expect.poll(() => fullFrame(page)).toBe(true)

    await expect.poll(() => markerPoint(page), { timeout: 20000 }).not.toBeNull()
    const pt = await markerPoint(page)
    await page.mouse.click(pt.x, pt.y)
    await expect(page).toHaveURL(/#.*sensor=\d+/)
    const id = Number(new URL(page.url()).hash.match(/sensor=(\d+)/)[1])

    const sheet = page.getByRole('dialog', { name: new RegExp(`${id}$`) })
    await expect(sheet).toBeVisible()
    const inside = await page.evaluate(() => {
      const map = document.querySelector('[data-island="map"]')
      const frame = document.fullscreenElement ?? (map.classList.contains('map--faux-full') ? map : null)
      return !!frame?.querySelector('.map-sensor-sheet')
    })
    expect(inside, 'the sheet is not inside the fullscreen element').toBe(true)
    await expect(sheet.locator('canvas')).toHaveCount(0)

    // The same numbers the sensors API holds for this station.
    const res = await page.request.get('/api/v1/area/sofia/sensors')
    const cols = (await res.json()).sensors
    const idx = cols.id.findIndex((v) => Number(v) === id)
    const metrics = (await page.locator('[data-island="panel"]').getAttribute('data-metrics')).split(',')
    const measured = new Set(cols.measures?.[idx] ?? metrics)
    const expected = metrics.filter((m) => measured.has(m) && cols[m] !== undefined)
      .map((m) => (cols[m][idx] == null ? null : String(cols[m][idx])))
    expect(expected.filter((v) => v !== null).length).toBeGreaterThan(0)
    // A metric with no reading shows the panel's "no data" text rather than a number.
    const shown = (await sheet.locator('.gauge__value').allTextContents())
      .map((t) => (/^-?\d/.test(t) ? t.split(' ')[0] : null))
    expect(shown).toEqual(expected)

    await context.close()
  })
}

// A sensor opened before fullscreen: native fullscreen paints twice, and the sheet must survive both.
for (const vp of VIEWPORTS) {
  for (const path of ['/en/#sensor=101', '/en/area/sofia#sensor=101']) {
    test(`${vp.name} ${path}: a sensor open before fullscreen shows in the sheet`, async ({ browser }, testInfo) => {
      testInfo.setTimeout(60000)
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await page.goto(path)
      await expect(page.locator('[data-island="panel"] .sensor-panel .gauges')).toBeVisible({ timeout: 15000 })

      await page.locator('.map__full').click()
      await expect.poll(() => fullFrame(page)).toBe(true)
      const sheet = page.getByRole('dialog', { name: /101$/ })
      await expect(sheet).toBeVisible()
      // Past the fullscreenchange paint that used to unmount it.
      await page.waitForTimeout(500)
      await expect(sheet).toBeVisible()
      const inside = await page.evaluate(() => {
        const map = document.querySelector('[data-island="map"]')
        const frame = document.fullscreenElement ?? (map.classList.contains('map--faux-full') ? map : null)
        return !!frame?.querySelector('.map-sensor-sheet .gauges')
      })
      expect(inside, 'the gauges are not in a sheet inside the fullscreen element').toBe(true)
      await expect(page.locator('[data-island="panel"] .sensor-panel .gauges')).toHaveCount(0)

      await context.close()
    })
  }
}

