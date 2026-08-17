import { test, expect } from '@playwright/test'

// EN routes throughout — see metric.spec.js's header comment.
//
// Real permission states are the reason this is an E2E and not a unit test:
// jsdom has no geolocation permission model at all.
//
// One shared context across this file's two tests, granting/clearing
// geolocation permission on it rather than opening a second
// browser.newContext() — see metric.spec.js's header comment on why a
// second cold, asset-fetching page load is worth avoiding here.
test.describe.serial('find me', () => {
  let context
  let page

  test.beforeAll(async ({ browser }) => {
    context = await browser.newContext()
    page = await context.newPage()
  })

  test.afterAll(async () => {
    await context.close()
  })

  test('find-me navigates to the nearest area when permission is granted', async () => {
    await context.grantPermissions(['geolocation'])
    await context.setGeolocation({ longitude: 23.33, latitude: 42.70 })
    await page.goto('/en/')
    // locateMe resolves entirely against the already-loaded area list
    // (nearest.js — no server endpoint takes a point, by design), so the
    // initial country/city-tier fetch that populates that list must land
    // before the click, or the handler reports locateFailed instead of
    // navigating. Real visitors have the same few hundred ms of latency;
    // the network-idle wait below is what a person waiting for the map to
    // settle gets for free.
    await page.waitForLoadState('networkidle')
    await page.getByRole('button', { name: 'Find me' }).click()
    // Matches both /area/sofia and /area/sofia-oblast — see e2e_test.go's
    // seedFixtures comment on why the country tier's nearest area is a
    // second, oblast-kind fixture rather than "sofia" itself.
    await expect(page).toHaveURL(/\/area\/sofia/)
  })

  test('a denied permission explains itself and leaves the map usable', async () => {
    await context.clearPermissions()
    await page.goto('/en/')
    await page.getByRole('button', { name: 'Find me' }).click()
    await expect(page.getByText('Location access was denied.')).toBeVisible()
  })
})
