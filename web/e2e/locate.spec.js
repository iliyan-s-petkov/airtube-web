import { test, expect } from './fixtures.js'

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

  test.beforeAll(async ({ ctx }) => {
    context = ctx
    page = await ctx.newPage()
  })

  test.afterAll(async () => {
    await context.clearPermissions()
    await page.close()
  })

  test('find-me loads the sensors of the area holding the fix', async () => {
    await context.grantPermissions(['geolocation'])
    await context.setGeolocation({ longitude: 23.33, latitude: 42.70 })
    // locateMe resolves entirely against the already-loaded area list
    // (nearest.js — no server endpoint takes a point, by design), so the
    // country-tier fetch that populates that list must land before the click,
    // or the handler reports locateFailed instead of moving the map. That one
    // response, not networkidle: the basemap's tiles come from
    // tile.openstreetmap.org and the page refreshes itself on a timer.
    // The grid request, not the overview one: the map asks for it only after
    // the overview body has been turned into the area list, so it is the
    // moment locate can succeed rather than the moment the data arrived.
    const gridLoaded = page.waitForResponse(/\/api\/v1\/hexes/)
    await page.goto('/en/')
    await gridLoaded
    // find-me stays on this page and zooms the map to the nearest sensor
    // (showNearestSensor, islands/map.js), so what it does that is visible
    // from outside the browser is fetch the sensors of the area holding the
    // fix. Matches both sofia and sofia-oblast — see e2e_test.go's
    // seedFixtures comment on why the country tier's nearest area is a
    // second, oblast-kind fixture rather than "sofia" itself.
    // Armed before the click, and the click is not retried: getJSON caches by
    // URL, so a second locate makes no second request and a retry loop would
    // wait for something that can only ever happen once.
    const sensors = page.waitForRequest(/\/api\/v1\/area\/sofia[^/]*\/sensors/)
    await page.getByRole('button', { name: 'Find me' }).click()
    await sensors
    await expect(page).toHaveURL(/\/en\/$/)
  })

  test('a denied permission explains itself and leaves the map usable', async () => {
    await context.clearPermissions()
    await page.goto('/en/')
    await page.getByRole('button', { name: 'Find me' }).click()
    await expect(page.getByText('Location access was denied.')).toBeVisible()
  })
})
