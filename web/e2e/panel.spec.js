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

  // The panel's copy reaches the browser ONLY as data-t-* attributes (no
  // 'unsafe-inline' in the CSP, so no inline bootstrap script can carry it),
  // and every one of those attributes is optional at the JS level — a dropped
  // wiring renders an empty string, which looks like a design choice rather
  // than a bug. These two tests are the only place the real catalogue copy is
  // asserted on a real screen; the mount-path unit tests
  // (src/islands/__tests__/panel.test.js) prove the same three wirings against
  // fixtures. EN routes, so the assertions quote internal/i18n/en.json.
  //
  // Sensor 104: seeded with a 'stuck' P2 reading (e2e_test.go's seedFixtures),
  // the only seeded sensor whose quality flag is not 'ok'.
  test('a flagged sensor shows the warning sentence and a labelled close control', async () => {
    await page.goto('/en/area/sofia#sensor=104')
    const panel = page.getByRole('dialog')
    await expect(panel).toBeVisible({ timeout: 10000 })
    await expect(panel.getByText('This reading has not changed in a while.')).toBeVisible()
    await expect(panel.getByRole('button', { name: 'Close' })).toBeVisible()
  })

  // Sensor 103: seeded with a P2 reading and NO P1 row at all, so the columnar
  // response carries a P1 column (its neighbours report it) that is null at
  // this sensor — "reports PM10, no reading right now", which the panel must
  // spell out rather than leave blank.
  test('a reported metric with no reading shows the placeholder, not an empty row', async () => {
    await page.goto('/en/area/sofia#sensor=103')
    const panel = page.getByRole('dialog')
    await expect(panel).toBeVisible({ timeout: 10000 })
    // The PM10 row's OWN value cell, not merely "the placeholder appears
    // somewhere in the panel": every canonical metric gets a column in the
    // columnar payload (sensorPayloadFrom), so the panel of ANY seeded sensor
    // carries several placeholder rows and a bare text assertion would pass
    // without 103's absent P1 having anything to do with it.
    const pm10Value = panel.locator('dt', { hasText: 'PM10' }).locator('xpath=following-sibling::dd[1]')
    await expect(pm10Value).toHaveText('no reading')
  })

  test('a sensor id that is not on this map leaves the page usable', async () => {
    await page.goto('/en/area/sofia#sensor=999999')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.locator('.metric-switcher')).toBeVisible()
  })
})
