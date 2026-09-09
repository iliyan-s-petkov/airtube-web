import { test, expect } from './fixtures.js'

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

  test.beforeAll(async ({ ctx }) => {
    page = await ctx.newPage()
  })

  test.afterAll(async () => {
    await page.close()
  })

  // The card is a named region under the map, not a dialog: it floated over
  // the map once and no longer does (SensorPanel.svelte).
  const sensorPanel = () => page.getByRole('region', { name: /^Sensor / })

  test('a deep-linked sensor opens the panel with a chart', async () => {
    await page.goto('/en/area/sofia#sensor=101')
    const panel = sensorPanel()
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
    await expect(sensorPanel()).toHaveCount(0)
    await expect(page).toHaveURL(/\/area\/sofia$/)
  })

  test('Escape closes the panel', async () => {
    await page.goto('/en/area/sofia#sensor=101')
    const panel = sensorPanel()
    await expect(panel).toBeVisible({ timeout: 10000 })
    // The panel's own onkeydown lives on the section (tabindex="-1",
    // programmatically focusable — SensorPanel.svelte), and a keydown only
    // reaches it if focus is inside it: nothing in this app auto-focuses the
    // panel on open, so a bare keyboard.press would target whatever the
    // PREVIOUS test in this shared page left focused.
    await panel.focus()
    await page.keyboard.press('Escape')
    await expect(sensorPanel()).toHaveCount(0)
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
    const panel = sensorPanel()
    await expect(panel).toBeVisible({ timeout: 10000 })
    await expect(panel.getByText('This reading has not changed in a while.')).toBeVisible()
    await expect(panel.getByRole('button', { name: 'Close' })).toBeVisible()
    // 'stuck' is not a usable quality (store/aggregate.go's usableQuality), so
    // this station measures PM2.5 and has no number for it — the placeholder,
    // in that row's OWN value cell rather than anywhere in the panel.
    const pm25Value = panel.locator('dt', { hasText: 'PM2.5' }).locator('xpath=following-sibling::dd[1]')
    await expect(pm25Value).toHaveText('no reading')
  })

  // Sensor 103: no P1 row at all, so nothing standing there measures PM10 and
  // the panel says nothing about it. The other half of the same distinction —
  // "no reading right now" and "does not measure this" are different facts.
  test('a metric no hardware here measures gets no row', async () => {
    await page.goto('/en/area/sofia#sensor=103')
    const panel = sensorPanel()
    await expect(panel).toBeVisible({ timeout: 10000 })
    await expect(panel.locator('dt', { hasText: 'PM10' })).toHaveCount(0)
    await expect(panel.locator('dt', { hasText: 'PM2.5' })).toHaveCount(1)
  })

  test('a sensor id that is not on this map leaves the page usable', async () => {
    await page.goto('/en/area/sofia#sensor=999999')
    await expect(sensorPanel()).toHaveCount(0)
    await expect(page.getByRole('button', { name: /^Metric: / })).toBeVisible()
  })
})
