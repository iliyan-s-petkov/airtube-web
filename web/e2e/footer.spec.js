import { test, expect } from './fixtures.js'

// Footer layout. Screenshots are written only when AIRBG_FOOTER_SHOTS is set.
const SHOTS = process.env.AIRBG_FOOTER_SHOTS

const open = async (ctx, width, height, scheme) => {
  const page = await ctx.newPage()
  await page.setViewportSize({ width, height })
  await page.emulateMedia({ colorScheme: scheme })
  await page.goto('/en/areas')
  await page.locator('footer.footer').scrollIntoViewIfNeeded()
  return page
}

test('desktop footer is a row of brand, three columns and social', async ({ ctx }) => {
  const page = await open(ctx, 1440, 900, 'light')
  const tops = await page.locator('footer .footer__grid > *').evaluateAll(
    (els) => els.map((e) => Math.round(e.getBoundingClientRect().top)))
  expect(tops).toHaveLength(5)
  expect(new Set(tops).size).toBe(1)
  await expect(page.locator('footer nav')).toHaveCount(3)
  await expect(page.locator('footer .langpick')).toHaveCount(0)
  if (SHOTS) {
    for (const s of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: s })
      await page.locator('footer').screenshot({ path: `/tmp/airbg-verify/footer-1440x900-${s}.png` })
    }
  }
  await page.close()
})

test('phone footer stacks: brand on top, columns two-up, stacked bar', async ({ ctx }) => {
  const page = await open(ctx, 393, 873, 'light')
  const brand = await page.locator('footer .footer__brand').first().boundingBox()
  const navs = await page.locator('footer nav').evaluateAll(
    (els) => els.map((e) => { const r = e.getBoundingClientRect(); return { x: r.left, y: r.top } }))
  expect(navs[0].y).toBeGreaterThan(brand.y + brand.height - 1)
  expect(Math.abs(navs[0].y - navs[1].y)).toBeLessThan(2)
  expect(navs[2].y).toBeGreaterThan(navs[0].y + 20)
  const bar = await page.locator('footer .footer__bar > *').evaluateAll(
    (els) => els.map((e) => Math.round(e.getBoundingClientRect().top)))
  expect(new Set(bar).size).toBe(bar.length)
  const social = await page.locator('footer .footer__social-link').first().boundingBox()
  expect(social.width).toBeGreaterThanOrEqual(44)
  expect(social.height).toBeGreaterThanOrEqual(44)
  if (SHOTS) {
    for (const s of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme: s })
      await page.locator('footer').screenshot({ path: `/tmp/airbg-verify/footer-393x873-${s}.png` })
    }
  }
  await page.close()
})

test('footer does not overflow horizontally at 320', async ({ ctx }) => {
  const page = await open(ctx, 320, 640, 'light')
  const over = await page.locator('footer').evaluate((f) => f.scrollWidth - f.clientWidth)
  expect(over).toBeLessThanOrEqual(0)
  const wide = await page.locator('footer *').evaluateAll(
    (els) => els.filter((e) => e.getBoundingClientRect().right > 320.5).length)
  expect(wide).toBe(0)
  await page.close()
})

// Link text and its licence tag stay on one line at desktop widths, BG and EN.
for (const [width, path] of [[1440, '/'], [1440, '/en/'], [1024, '/'], [1024, '/en/']]) {
  test(`footer links render on one line at ${width} ${path}`, async ({ ctx }) => {
    const page = await ctx.newPage()
    await page.setViewportSize({ width, height: 900 })
    await page.goto(path)
    await page.locator('footer.footer').scrollIntoViewIfNeeded()
    const rows = await page.locator('.footer__col li').evaluateAll((lis) => lis.map((li) => {
      const cs = getComputedStyle(li.querySelector('a'))
      const line = parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.5
      return { text: li.textContent.trim(), h: li.getBoundingClientRect().height, line }
    }))
    expect(rows.length).toBeGreaterThan(8)
    for (const r of rows) expect(r.h, r.text).toBeLessThan(r.line * 1.5)
    await page.close()
  })
}

test('footer does not overflow horizontally at 672, 800, 1024', async ({ ctx }) => {
  for (const path of ['/', '/en/']) {
    for (const width of [672, 800, 1024]) {
      const page = await ctx.newPage()
      await page.setViewportSize({ width, height: 900 })
      await page.goto(path)
      const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
      expect(over, `${path} at ${width}`).toBeLessThanOrEqual(0)
      const foot = await page.locator('footer').evaluate((f) => f.scrollWidth - f.clientWidth)
      expect(foot, `${path} footer at ${width}`).toBeLessThanOrEqual(0)
      await page.close()
    }
  }
})

test('footer screenshots at 1440 and 1024 BG', async ({ ctx }) => {
  test.skip(!SHOTS, 'screenshots only with AIRBG_FOOTER_SHOTS')
  for (const w of [1440, 1024]) {
    const page = await ctx.newPage()
    await page.setViewportSize({ width: w, height: 900 })
    await page.goto('/')
    await page.locator('footer').screenshot({ path: `/tmp/airbg-verify/footer-bg-${w}.png` })
    await page.close()
  }
})
