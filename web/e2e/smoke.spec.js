import { test, expect } from '@playwright/test'

test('the area page renders server-side with JavaScript disabled', async ({ browser }) => {
  // The whole point of server-rendered pages: this must pass with no bundle.
  const context = await browser.newContext({ javaScriptEnabled: false })
  const page = await context.newPage()
  await page.goto('/area/sofia')
  await expect(page.locator('h1')).toContainText('Sofia')
  await context.close()
})

test('the metric switcher is mounted and reflects the default metric', async ({ page }) => {
  await page.goto('/area/sofia')
  const pressed = page.locator('.metric-switcher button[aria-pressed="true"]')
  await expect(pressed).toHaveCount(1)
})
