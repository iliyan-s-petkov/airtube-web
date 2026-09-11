import { test, expect } from './fixtures.js'

// EN routes throughout (see metric.spec.js). One shared context per file, as in
// locate.spec.js: a second cold load spends the rate limiter's burst on assets.
test.describe.serial('the network layers', () => {
  let page

  test.beforeAll(async ({ ctx }) => {
    page = await ctx.newPage()
    const grid = page.waitForResponse(/\/api\/v1\/hexes/)
    // '/en/', the opening map, deliberately: the toggles used to be disabled
    // anywhere but the sensor tier, so this is the page where the bug lived.
    await page.goto('/en/')
    await grid
  })

  test.afterAll(async () => { await page.close() })

  test('both networks are offered, on, and live on the opening map', async () => {
    await page.getByRole('button', { name: 'Layers' }).click()
    await expect(page.getByRole('checkbox', { name: /Citizen sensors/ })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: /Citizen sensors/ })).toBeEnabled()
    await expect(page.getByRole('checkbox', { name: /Official stations/ })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: /Official stations/ })).toBeEnabled()
  })

  test('switching a network off repaints the grid without a request', async () => {
    const requests = []
    page.on('request', (r) => { if (r.url().includes('/api/v1/')) requests.push(r.url()) })
    const painted = page.evaluate(() => new Promise((resolve) => {
      document.querySelector('[data-island="map"]')
        .addEventListener('airbg:paint', (e) => resolve(e.detail.source), { once: true })
    }))
    await page.getByRole('checkbox', { name: /Citizen sensors/ }).uncheck()
    expect(await painted).toBe('airbg-hexes')
    expect(requests).toHaveLength(0)
    await page.getByRole('checkbox', { name: /Citizen sensors/ }).check()
  })

  test('the layer menu counts stations and explains zero coverage, at the default metric', async () => {
    await expect(page.getByText(/Citizen sensors — \d+ with data/)).toBeVisible()
    await expect(page.getByText(/Official stations — does not measure this/)).toBeVisible()
    await expect(page.getByRole('checkbox', { name: /Official stations/ })).toBeEnabled()
  })

  test('a metric only one network measures explains itself', async () => {
    await page.getByRole('button', { name: /^Metric:/ }).click()
    await page.getByRole('radio', { name: 'Ozone' }).check()
    // The metric switcher is its own disclosure, outside the layers root, so
    // picking a metric there closes the layers panel (mountLayers' own
    // outside-mousedown handler) — reopen it to reach the label.
    await page.getByRole('button', { name: 'Layers' }).click()
    await expect(page.getByText(/Citizen sensors — does not measure this/)).toBeVisible()
    await expect(page.getByRole('checkbox', { name: /Citizen sensors/ })).toBeEnabled()
    await expect(page.getByText(/Official stations — \d+ with data/)).toBeVisible()
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
