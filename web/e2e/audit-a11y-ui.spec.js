import { test as base, expect, mapSettled } from './fixtures.js'

// Findings from the 2026-10-01 live audit (A01, A02, U01, U02, K01, U04).
// Each assertion measures what a reader sees, in a real browser, because the
// failures were all in computed style and layout that no unit test can reach.

const test = base.extend({
  phoneCtx: [async ({ browser }, use) => {
    const context = await browser.newContext({ isMobile: true, hasTouch: true, deviceScaleFactor: 2 })
    await use(context)
    await context.close()
  }, { scope: 'worker' }],
})

test.afterEach(async ({ ctx, phoneCtx }) => {
  for (const c of [ctx, phoneCtx]) {
    for (const p of c.pages()) if (!p.isClosed()) await p.close().catch(() => {})
  }
})

const withTheme = (page, theme) => page.addInitScript((v) => localStorage.setItem('kanarche:theme', v), theme)

const mockWind = (page) => page.route('**/api/v1/wind', (route) => route.fulfill({
  status: 200,
  contentType: 'application/json',
  body: JSON.stringify({
    generated_at: new Date().toISOString(),
    valid_at: new Date().toISOString(),
    model: 'Test Model',
    model_resolution_deg: 0.25,
    resolution_km: 25,
    forecast: true,
    vectors: [{ lon: 23.3, lat: 42.68, speed_ms: 3.2, direction_deg: 180 }],
  }),
}))

// WCAG contrast of an element's text colour against the page background token.
// The element's own background is ignored on purpose: these controls are
// transparent and sit on --bg.
const contrastOf = (page, selector) => page.evaluate((sel) => {
  const parse = (c) => c.match(/[\d.]+/g).slice(0, 3).map(Number)
  const lum = ([r, g, b]) => {
    const f = (v) => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4 }
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
  }
  const probe = document.createElement('i')
  probe.style.color = 'var(--bg)'
  document.body.appendChild(probe)
  const bg = parse(getComputedStyle(probe).color)
  probe.remove()
  const fg = parse(getComputedStyle(document.querySelector(sel)).color)
  const [hi, lo] = [lum(fg), lum(bg)].sort((a, b) => b - a)
  return (hi + 0.05) / (lo + 0.05)
}, selector)

test.describe('A01: the header pickers are legible in the light theme', () => {
  test('language and theme buttons pass AA on the light page', async ({ ctx }) => {
    const page = await ctx.newPage()
    await withTheme(page, 'light')
    await page.goto('/en/areas')
    const buttons = page.locator('.langpick__btn')
    // The theme picker mounts after first paint, so wait for it.
    await expect.poll(() => buttons.count()).toBeGreaterThanOrEqual(2)
    for (let i = 0; i < await buttons.count(); i++) {
      await buttons.nth(i).evaluate((el, n) => el.setAttribute('data-probe', n), i)
      expect(await contrastOf(page, `.langpick__btn[data-probe="${i}"]`)).toBeGreaterThanOrEqual(4.5)
    }
  })

  test('the dark theme keeps its colour', async ({ ctx }) => {
    const page = await ctx.newPage()
    await withTheme(page, 'dark')
    await page.goto('/en/areas')
    const colour = await page.locator('.langpick__btn').first().evaluate((el) => getComputedStyle(el).color)
    expect(colour).toBe('rgb(198, 198, 198)')
  })
})

test.describe('A02: the areas meta line passes AA', () => {
  for (const path of ['/en/areas', '/en/area/sofia']) {
    test(`${path} in the light theme`, async ({ ctx }) => {
      const page = await ctx.newPage()
      await withTheme(page, 'light')
      await page.goto(path)
      const meta = page.locator('p.meta, .meta').first()
      await expect(meta).toBeVisible()
      await meta.evaluate((el) => el.setAttribute('data-probe', 'meta'))
      expect(await contrastOf(page, '[data-probe="meta"]')).toBeGreaterThanOrEqual(4.5)
    })
  }
})

test.describe('U01: the phone bottom-right corner', () => {
  for (const theme of ['light', 'dark']) {
    test(`${theme}: collapsed attribution is one control, locate and (i) have a card behind them`, async ({ phoneCtx }) => {
      const page = await phoneCtx.newPage()
      await mockWind(page)
      await withTheme(page, theme)
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto('/en')
      await page.waitForSelector('.maplibregl-ctrl-attrib')
      await expect(page.locator('.maplibregl-ctrl-attrib')).not.toHaveClass(/maplibregl-compact-show/)

      // The collapsed container is exactly the button: no second box peeking out.
      const geometry = await page.evaluate(() => {
        const r = (el) => el.getBoundingClientRect()
        const box = document.querySelector('.maplibregl-ctrl-attrib')
        const btn = box.querySelector('.maplibregl-ctrl-attrib-button')
        return { box: r(box), btn: r(btn), boxBg: getComputedStyle(box).backgroundColor }
      })
      expect(geometry.box.width).toBeLessThanOrEqual(geometry.btn.width + 1)
      expect(geometry.box.height).toBeLessThanOrEqual(geometry.btn.height + 1)
      expect(geometry.boxBg).toBe('rgba(0, 0, 0, 0)')

      // Locate, (i) wind note and attribution all carry an opaque card.
      await page.evaluate(() => { document.querySelector('.map-wind-label').hidden = false })
      const fills = await page.evaluate(() => {
        const alpha = (c) => { const m = c.match(/[\d.]+/g); return m.length > 3 ? Number(m[3]) : 1 }
        return ['.map-locate', '.map-wind-label__toggle', '.maplibregl-ctrl-attrib-button'].map((s) => {
          const bg = getComputedStyle(document.querySelector(s)).backgroundColor
          return { s, alpha: alpha(bg) }
        })
      })
      for (const f of fills) expect(f.alpha, f.s).toBeGreaterThanOrEqual(0.9)
    })
  }

  test('expanded attribution opens as one card clear of the locate button', async ({ phoneCtx }) => {
    const page = await phoneCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    await page.waitForSelector('.maplibregl-ctrl-attrib')
    // The map's load handler strips compact-show; a click before it is undone.
    await mapSettled(page)
    await page.waitForFunction(() => document.querySelector('[data-island="map"]').__map.loaded())
    await expect(page.locator('.maplibregl-ctrl-attrib')).not.toHaveClass(/maplibregl-compact-show/)
    await page.locator('.maplibregl-ctrl-attrib-button').click()
    const box = page.locator('.maplibregl-ctrl-attrib')
    await expect(box).toHaveClass(/maplibregl-compact-show/)
    const [attrib, locate] = await Promise.all([box.boundingBox(), page.locator('.map-locate').boundingBox()])
    const overlap = attrib.x < locate.x + locate.width && attrib.x + attrib.width > locate.x &&
      attrib.y < locate.y + locate.height && attrib.y + attrib.height > locate.y
    expect(overlap).toBe(false)
  })
})

// The open attribution card must stay clear of every other control in the
// corner and along the bottom edge, in both themes and both languages (the
// Bulgarian text is the longer one).
test.describe('U01: the open attribution card overlaps nothing', () => {
  for (const theme of ['light', 'dark']) {
    for (const lang of ['/en', '/']) {
      test(`${theme} ${lang}`, async ({ phoneCtx }) => {
        const page = await phoneCtx.newPage()
        await mockWind(page)
        await withTheme(page, theme)
        await page.setViewportSize({ width: 390, height: 844 })
        await page.goto(lang)
        await page.waitForSelector('.maplibregl-ctrl-attrib')
        await page.locator('.maplibregl-ctrl-attrib-button').click()
        await expect(page.locator('.maplibregl-ctrl-attrib')).toHaveClass(/maplibregl-compact-show/)
        const rects = await page.evaluate(() => {
          const r = (el) => { const b = el.getBoundingClientRect(); return { l: b.left, t: b.top, r: b.right, b: b.bottom, w: b.width, h: b.height } }
          const card = document.querySelector('.maplibregl-ctrl-attrib-inner')
          const others = ['.scale--onmap', '.map-freshness', '.map-play', '.map-locate', '.map-wind-label__toggle', '.maplibregl-ctrl-attrib-button']
          const found = {}
          for (const s of others) {
            const el = document.querySelector(s)
            if (el && el.getBoundingClientRect().width > 0) found[s] = r(el)
          }
          return { card: r(card), found }
        })
        // MapLibre dims the open toggle to a 5% wash; it has to stay a card.
        const toggleBg = await page.locator('.maplibregl-ctrl-attrib-button').evaluate((el) => getComputedStyle(el).backgroundColor)
        const alpha = toggleBg.match(/[\d.]+/g).length > 3 ? Number(toggleBg.match(/[\d.]+/g)[3]) : 1
        expect(alpha, toggleBg).toBeGreaterThanOrEqual(0.9)
        expect(rects.card.w).toBeGreaterThan(0)
        expect(Object.keys(rects.found)).toEqual(expect.arrayContaining(['.scale--onmap', '.map-locate', '.maplibregl-ctrl-attrib-button']))
        for (const [sel, o] of Object.entries(rects.found)) {
          const hit = rects.card.l < o.r && rects.card.r > o.l && rects.card.t < o.b && rects.card.b > o.t
          expect(hit, `${sel} overlaps the open attribution`).toBe(false)
        }
      })
    }
  }
})

test.describe('U02: the open wind note on a phone', () => {
  test('is opaque and sits above the playback bar', async ({ phoneCtx }) => {
    const page = await phoneCtx.newPage()
    await mockWind(page)
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    await page.waitForSelector('.map-wind-label', { state: 'attached' })
    await expect.poll(async () => page.locator('.map-wind-label').evaluate((el) => el.hidden)).toBe(false)
    await page.locator('.map-wind-label__toggle').click()
    const note = page.locator('.map-wind-label')
    await expect(note).toHaveAttribute('open', '')

    const bg = await note.evaluate((el) => getComputedStyle(el).backgroundColor)
    expect(bg.startsWith('rgb(') || /,\s*1\)$/.test(bg), bg).toBe(true)

    await expect.poll(async () => {
      const [n, bar] = await Promise.all([note.boundingBox(), page.locator('.map-freshness').boundingBox()])
      return n && bar ? bar.y - (n.y + n.height) : null
    }).toBeGreaterThanOrEqual(0)
  })
})

test.describe('K01: the area search is one Tab stop', () => {
  for (const path of ['/en', '/']) {
    test(`${path}: Tab from the input leaves the list alone and never lands on body`, async ({ ctx }) => {
      const page = await ctx.newPage()
      await page.goto(path)
      const input = page.locator('input[role="combobox"]')
      await input.focus()
      await input.fill('s')
      await expect(page.locator('ul.combobox__list')).toHaveAttribute('tabindex', '-1')
      await page.keyboard.press('Tab')
      const where = await page.evaluate(() => {
        const a = document.activeElement
        return { body: a === document.body, inList: !!a.closest('.combobox__list'), inCombobox: !!a.closest('.combobox') }
      })
      expect(where.body).toBe(false)
      expect(where.inList).toBe(false)
      expect(where.inCombobox).toBe(false)
    })
  }
})

test.describe('U04: the metric button keeps a visible space after its legend', () => {
  test('there is a gap between "Metric:" and the value', async ({ ctx }) => {
    const page = await ctx.newPage()
    await page.setViewportSize({ width: 1280, height: 900 })
    await page.goto('/en/area/sofia')
    await page.waitForSelector('[data-island="switcher"] .colmenu__legend')
    const gap =await page.evaluate(() => {
      const legend = document.querySelector('[data-island="switcher"] .colmenu__legend')
      if (!legend || legend.getBoundingClientRect().width < 4) return null
      const btn = legend.parentElement
      const text = [...btn.childNodes].find((n) => n.nodeType === 3 && n.textContent.trim())
      const range = document.createRange()
      range.selectNodeContents(text)
      const first = range.getClientRects()[0]
      return first.left - legend.getBoundingClientRect().right
    })
    expect(gap).not.toBeNull()
    expect(gap).toBeGreaterThanOrEqual(3)
  })
})

test.describe('U03: the legend no-data row keeps its inline-end padding', () => {
  test('the label ends inside the open legend panel, not on its edge', async ({ ctx }) => {
    const page = await ctx.newPage()
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.addInitScript(() => localStorage.setItem('kanarche:legend-open', 'true'))
    await page.goto('/en')
    await expect(page.locator('.scale--onmap .scale__none .legend__label')).toBeVisible()
    const room = await page.evaluate(() => {
      const panel = document.querySelector('.scale--onmap').getBoundingClientRect()
      const l = document.querySelector('.scale--onmap .scale__none .legend__label').getBoundingClientRect()
      return panel.right - l.right
    })
    expect(room).toBeGreaterThanOrEqual(8)
    await page.close()
  })
})

test.describe('U06: the area breadcrumb is not flush under the masthead', () => {
  test('the desktop breadcrumb has top spacing', async ({ ctx }) => {
    const page = await ctx.newPage()
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/en/area/sofia')
    const gap = await page.evaluate(() => {
      const bar = document.querySelector('header').getBoundingClientRect()
      return document.querySelector('.breadcrumb').getBoundingClientRect().top - bar.bottom
    })
    expect(gap).toBeGreaterThanOrEqual(16)
    await page.close()
  })
})
