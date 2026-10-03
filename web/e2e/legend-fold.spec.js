import { test, expect, mapSettled } from './fixtures.js'

// #579/#580: the legend and the layers list are the map's two on-map
// popovers; at most one is open at a time, and the open legend folds on a
// tap anywhere inside it, not just the triangle.

const VIEWPORTS = [
  { name: 'phone 393x873', viewport: { width: 393, height: 873 }, isMobile: true, hasTouch: true },
  { name: 'desktop 1280x800', viewport: { width: 1280, height: 800 } },
]

const openLayers = async (page) => {
  await page.locator('.map__layers .colmenu__btn').click()
  await expect(page.locator('.map__layers .colmenu__panel')).toBeVisible()
}

// Resolves after every task already queued (the details `toggle` event) has run.
const afterQueuedTasks = (page) => page.evaluate(() => new Promise((r) => setTimeout(r, 0)))

// Forces the legend open/closed on arrival, overriding the folded-by-default
// start so every test begins from a known state.
const withLegend = (page, open) =>
  page.addInitScript((v) => localStorage.setItem('kanarche:legend-open', v), String(open))

for (const vp of VIEWPORTS) {
  test.describe(vp.name, () => {
    test('opening the layers list folds an open legend', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, true)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      await expect(legend).toHaveAttribute('open', '')

      await openLayers(page)
      await expect(legend).not.toHaveAttribute('open', '')

      await context.close()
    })

    test('closing the layers list restores the legend it folded', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, true)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      await expect(legend).toHaveAttribute('open', '')

      const layersBtn = page.locator('.map__layers .colmenu__btn')
      await openLayers(page)
      await expect(legend).not.toHaveAttribute('open', '')

      await layersBtn.click()
      await expect(page.locator('.map__layers .colmenu__panel')).toBeHidden()
      await expect(legend).toHaveAttribute('open', '')

      await context.close()
    })

    test('the layers list folding/restoring the legend does not persist, even once the toggle event settles', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, true)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      const layersBtn = page.locator('.map__layers .colmenu__btn')
      await expect(legend).toHaveAttribute('open', '')

      await openLayers(page)
      await expect(legend).not.toHaveAttribute('open', '')
      // <details> fires `toggle` as a queued task, not synchronously with the
      // .open write. A task queued now runs after it, so read once it has run.
      await afterQueuedTasks(page)
      expect(await page.evaluate(() => localStorage.getItem('kanarche:legend-open'))).toBe('true')

      await layersBtn.click()
      await expect(page.locator('.map__layers .colmenu__panel')).toBeHidden()
      await expect(legend).toHaveAttribute('open', '')
      await afterQueuedTasks(page)
      expect(await page.evaluate(() => localStorage.getItem('kanarche:legend-open'))).toBe('true')

      await context.close()
    })

    test('the layers list does not re-open the legend once the reader folded it themself', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, false)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      await expect(legend).not.toHaveAttribute('open', '')

      const layersBtn = page.locator('.map__layers .colmenu__btn')
      await openLayers(page)
      await layersBtn.click()
      await expect(page.locator('.map__layers .colmenu__panel')).toBeHidden()
      await expect(legend).not.toHaveAttribute('open', '')

      await context.close()
    })

    test('opening the legend closes an open layers list', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, false)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      const layersBtn = page.locator('.map__layers .colmenu__btn')
      await expect(legend).not.toHaveAttribute('open', '')

      await openLayers(page)
      await expect(layersBtn).toHaveAttribute('aria-expanded', 'true')

      // Keyboard, not a coordinate click: the open layers panel can overlap
      // the legend's screen position, and a real click risks landing on the
      // panel instead of the summary underneath it.
      await legend.locator(':scope > .scale__toggle').focus()
      await page.keyboard.press('Enter')

      await expect(legend).toHaveAttribute('open', '')
      await expect(layersBtn).toHaveAttribute('aria-expanded', 'false')
      await expect(page.locator('.map__layers .colmenu__panel')).toBeHidden()

      await context.close()
    })

    test('a tap anywhere inside the open legend folds it', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, true)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      await expect(legend).toHaveAttribute('open', '')
      await expect(page.locator('.map__layers .colmenu__btn')).toBeVisible()

      // The band label, well clear of the info button and the triangle. No
      // force: renderLegend() briefly detaches and rebuilds the key after
      // load, and Playwright's own actionability wait rides that out.
      await legend.locator('.scale__label').click()
      await expect(legend).not.toHaveAttribute('open', '')

      await context.close()
    })

    test('tapping the (i) info button opens the scale dialog instead of folding', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, true)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      const info = legend.locator('.scale__info')
      await expect(legend).toHaveAttribute('open', '')

      if (await info.count()) {
        await info.click()
        await expect(legend).toHaveAttribute('open', '', { timeout: 2000 })
      }

      await context.close()
    })

    test('Escape folds the open legend', async ({ browser }) => {
      const { name, ...opts } = vp
      const context = await browser.newContext(opts)
      const page = await context.newPage()
      await withLegend(page, true)
      await page.goto('/en')
      await mapSettled(page)

      const legend = page.locator('.scale--onmap')
      await expect(legend).toHaveAttribute('open', '')
      // The Escape handler is wired with the layers button; islands mount after first paint.
      await expect(page.locator('.map__layers .colmenu__btn')).toBeVisible()

      // renderLegend() detaches and reappends the toggle on its post-load
      // refresh, which drops a focus set before that runs; phone keeps
      // fetching (wind, refresh polling) so networkidle never settles. Retry
      // the focus until it sticks, rather than wait for a quiet network.
      const toggle = legend.locator(':scope > .scale__toggle')
      await expect(async () => {
        await toggle.focus()
        expect(await toggle.evaluate((el) => el === document.activeElement)).toBe(true)
        // Escape and its effect are inside the retry: a re-render between the
        // focus check and the keypress would send Escape to the body.
        await page.keyboard.press('Escape')
        await expect(legend).not.toHaveAttribute('open', '', { timeout: 1000 })
      }).toPass({ timeout: 15_000 })

      await context.close()
    })
  })
}
