import { test, expect } from './fixtures.js'

test('the area page renders server-side with JavaScript disabled', async ({ browser }) => {
  // The one context of its own in the suite: JavaScript has to be off before
  // the context exists, and the whole point of server-rendered pages is that
  // this passes with no bundle — so it costs no JS chunks either.
  const context = await browser.newContext({ javaScriptEnabled: false })
  const page = await context.newPage()
  await page.goto('/area/sofia')
  await expect(page.locator('h1')).toContainText('Sofia')
  await context.close()
})

// EN route, because the button names the metric in the page's own language.
test('the metric switcher is mounted and reflects the default metric', async ({ ctx }) => {
  const page = await ctx.newPage()
  await page.goto('/en/area/sofia')
  await expect(page.getByRole('button', { name: 'Metric: PM2.5' })).toBeVisible()
  await page.close()
})
