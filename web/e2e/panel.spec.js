import { test, expect } from '@playwright/test'

// EN routes throughout — see metric.spec.js's header comment. Sensor 101
// (internal/e2e/e2e_test.go's seedFixtures) is the deep-link target: it is
// the only seeded sensor with both P1 and P2 current values AND 24h of P2
// history, so it is the only one whose chart can actually render a canvas.
//
// One shared page across this file's tests — see metric.spec.js's header
// comment on why: it keeps this file's four navigations to one cold,
// asset-fetching load instead of four.
test.describe.serial('sensor panel', () => {
  let page

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage()
  })

  test.afterAll(async () => {
    await page.close()
  })

  test('a deep-linked sensor opens the panel with a chart', async () => {
    await page.goto('/en/area/sofia#sensor=101')
    const panel = page.getByRole('dialog')
    // A generous timeout, not the assertion default: this is the sensor
    // tier's own first fetch (/api/v1/area/sofia/sensors) settling before
    // the registry's findSensor() reads through to resolve — see
    // lib/sensors.svelte.js's own comment on why a deep link can arrive
    // before the data does.
    await expect(panel).toBeVisible({ timeout: 10000 })
    // The panel's values come from the snapshot the map already holds, so
    // they must be on screen BEFORE the series request settles.
    await expect(panel).toContainText('PM2.5')
    await expect(panel.locator('.chart-host canvas')).toBeVisible()
  })

  test('Back closes the panel and leaves the page loaded', async () => {
    await page.goto('/en/area/sofia')
    await page.goto('/en/area/sofia#sensor=101')
    await page.goBack()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page).toHaveURL(/\/area\/sofia$/)
  })

  test('Escape closes the panel', async () => {
    await page.goto('/en/area/sofia#sensor=101')
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible({ timeout: 10000 })
    // The panel's own onkeydown lives on the dialog element (tabindex="-1",
    // programmatically focusable — SensorPanel.svelte), and a keydown only
    // reaches it if focus is inside it: nothing in this app auto-focuses the
    // panel on open, so a bare keyboard.press would target whatever the
    // PREVIOUS test in this shared page left focused, not the dialog.
    await dialog.focus()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })

  test('a sensor id that is not on this map leaves the page usable', async () => {
    await page.goto('/en/area/sofia#sensor=999999')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.locator('.metric-switcher')).toBeVisible()
  })
})
