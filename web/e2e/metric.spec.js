import { test, expect } from './fixtures.js'

// EN routes throughout: i18n.DefaultLang is "bg", and these specs assert the
// EN catalogue strings verbatim (metric.P1 = "PM10", metric.temperature =
// "Temperature" — internal/i18n/en.json), so every navigation below goes
// through the /en prefix rather than the unprefixed BG route.
//
// One shared page across this file's tests, not the default one-per-test
// context: ratelimit.api (airbg.yaml) is a single per-IP bucket wrapping the
// WHOLE server, static assets included (internal/server/server.go's chain
// wraps root, not just /api/), and Playwright's default fresh context per
// test throws away the browser's disk cache along with it — every "cold"
// area-page load re-fetches all ~15 immutably-cached JS/CSS chunks and
// re-spends burst tokens no real visitor would spend twice. This suite
// deliberately runs against the SAME airbg.yaml the binary ships with (see
// internal/e2e/e2e_test.go), so the fix belongs here, in how many cold loads
// the spec causes, not in the limiter's numbers.
test.describe.serial('metric switcher', () => {
  let page

  test.beforeAll(async ({ ctx }) => {
    page = await ctx.newPage()
  })

  test.afterAll(async () => {
    await page.close()
  })

  // The switcher is a pop-up (MetricMenu.svelte): the button names the metric
  // in force, the radios inside the panel change it.
  const chooser = () => page.getByRole('button', { name: /^Metric: / })

  async function choose(label) {
    await chooser().click()
    await page.getByRole('radio', { name: label }).click()
  }

  test('switching metric rewrites the hash without growing the back stack', async () => {
    await page.goto('/en/area/sofia')
    await choose('PM10')
    await expect(chooser()).toHaveText('Metric: PM10')
    await expect(page).toHaveURL(/#metric=P1/)
    // Back must leave the page, not undo the metric — replaceState is the
    // whole reason this assertion exists and the only way to prove it in a
    // real browser.
    await page.goBack()
    await expect(page).not.toHaveURL(/\/area\/sofia/)
  })

  test('a deep-linked metric is selected on load', async () => {
    await page.goto('/en/area/sofia#metric=temperature')
    await expect(chooser()).toHaveText('Metric: Temperature')
    await chooser().click()
    await expect(page.getByRole('radio', { name: 'Temperature' })).toBeChecked()
  })

  test('an unknown metric in the hash falls back to the default', async () => {
    await page.goto('/en/area/sofia#metric=plutonium')
    await expect(chooser()).toHaveText('Metric: PM2.5')
  })
})
