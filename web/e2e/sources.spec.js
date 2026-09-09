import { test, expect } from './fixtures.js'

// EN routes throughout (see metric.spec.js). One shared context per file, as in
// locate.spec.js: a second cold load spends the rate limiter's burst on assets.
test.describe.serial('the network layers', () => {
  let page

  test.beforeAll(async ({ ctx }) => {
    page = await ctx.newPage()
    const grid = page.waitForResponse(/\/api\/v1\/hexes/)
    // /area/sofia, not '/en/': it opens at zoom_sensor (see redraw.spec.js and
    // seedFixtures' own comment on area kind "city"), which is what makes the
    // network toggle below observable at all — repaintSensors is deliberately
    // a no-op away from the sensor tier (islands/map.js), so a toggle on the
    // index page's zoom-7 country view would never paint anything to catch.
    await page.goto('/en/area/sofia')
    await grid
  })

  test.afterAll(async () => { await page.close() })

  test('both networks are offered and both are on', async () => {
    await page.getByRole('button', { name: 'Layers' }).click()
    await expect(page.getByRole('checkbox', { name: 'Citizen sensors' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'Official stations' })).toBeChecked()
  })

  test('switching a network off repaints without a request', async () => {
    const requests = []
    page.on('request', (r) => { if (r.url().includes('/api/v1/')) requests.push(r.url()) })
    const painted = page.evaluate(() => new Promise((resolve) => {
      document.querySelector('[data-island="map"]')
        .addEventListener('airbg:paint', (e) => resolve(e.detail.source), { once: true })
    }))
    await page.getByRole('checkbox', { name: 'Official stations' }).uncheck()
    expect(await painted).toBe('airbg-data')
    expect(requests).toHaveLength(0)
  })

  test('a metric only one network measures explains itself', async () => {
    await page.getByRole('button', { name: /^Metric:/ }).click()
    await page.getByRole('radio', { name: 'Ozone' }).check()
    // The metric switcher is its own disclosure, outside the layers root, so
    // picking a metric there closes the layers panel (mountLayers' own
    // outside-mousedown handler) — reopen it to reach the checkbox.
    await page.getByRole('button', { name: 'Layers' }).click()
    const citizen = page.getByRole('checkbox', { name: /Citizen sensors/ })
    await expect(citizen).toBeDisabled()
    await expect(page.getByText(/does not measure this/)).toBeVisible()
  })
})

test('the footer credits both programmes', async ({ ctx }) => {
  const page = await ctx.newPage()
  await page.goto('/en/')
  await expect(page.getByRole('link', { name: 'Official government map' }))
    .toHaveAttribute('href', 'https://eea.government.bg/kav/')
  await expect(page.getByRole('link', { name: 'sensor.community map' }))
    .toHaveAttribute('href', 'https://maps.sensor.community/')
  await page.close()
})
