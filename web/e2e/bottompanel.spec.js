import { test, expect } from './fixtures.js'

// OpenProject #684: from 1024px a tapped sensor opens a panel along the bottom of the map.
// The helpers below are the ones panelscroll.spec.js uses.
const WIDE = { width: 1440, height: 900 }

async function prepareMap(page, path = '/en/') {
  await page.goto(path)
  await page.waitForFunction(() => document.querySelector('[data-island="map"]')?.__map?.isStyleLoaded?.())
  await page.waitForTimeout(1000)
  await expect.poll(() => page.evaluate(() => document.querySelector('[data-island="map"]').__map.isMoving())).toBe(false)
  await page.evaluate(() => {
    document.querySelector('.map-shell').scrollIntoView({ block: 'start', behavior: 'instant' })
    document.querySelector('[data-island="map"]').__map.jumpTo({ center: [23.32, 42.69], zoom: 11 })
  })
}

// Client points of hex cells naming one station, clear of the map's edges and
// of any overlay, skipping sensor ids in `skip`.
const hexPoints = (page, skip) => page.evaluate((skip) => {
  const map = document.querySelector('[data-island="map"]').__map
  if (!map?.getLayer?.('airbg-hex-fill')) return []
  const box = map.getCanvas().getBoundingClientRect()
  const out = []
  for (const f of map.queryRenderedFeatures({ layers: ['airbg-hex-fill'] })) {
    const id = f.properties?.sensorId
    if (f.geometry.type !== 'Polygon' || id == null || skip.includes(Number(id))) continue
    const ring = f.geometry.coordinates[0].slice(0, -1)
    const c = [0, 1].map((i) => ring.reduce((a, p) => a + p[i], 0) / ring.length)
    const p = map.project(c)
    const y = box.top + p.y
    if (p.x < 30 || p.x > box.width - 30 || y < 80 || p.y > box.height - 30) continue
    const x = box.left + p.x
    if (document.elementFromPoint(x, y) !== map.getCanvas()) continue
    if (out.some((o) => o.id === Number(id))) continue
    out.push({ x, y, id: Number(id), c })
  }
  return out
}, skip)

// low: the cell nearest the map's bottom edge, the one a bottom panel would cover.
async function tapHex(page, skip = [], { low = false } = {}) {
  let pts = []
  await expect.poll(async () => { pts = await hexPoints(page, skip); return pts.length }, { timeout: 20000 }).toBeGreaterThan(0)
  const pick = low ? pts.reduce((a, b) => (b.y > a.y ? b : a)) : pts[0]
  await page.mouse.click(pick.x, pick.y)
  await expect(page).toHaveURL(/#.*sensor=\d+/)
  lastCell = pick.c
  return pick.id
}
let lastCell = null

const box = async (locator) => {
  const b = await locator.boundingBox()
  expect(b).not.toBeNull()
  return b
}
const overlaps = (a, b) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height

async function wideOpen(browser, size = WIDE, opts = {}) {
  const context = await browser.newContext({ viewport: size })
  const page = await context.newPage()
  await prepareMap(page)
  const id = await tapHex(page, [], opts)
  return { context, page, id }
}

const PANEL = '.map-dock'

test('1440: a tapped sensor opens a panel along the bottom of the map', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const context = await browser.newContext({ viewport: WIDE })
  const page = await context.newPage()
  await prepareMap(page)
  const before = await page.evaluate(() => window.scrollY)
  await tapHex(page)
  const panel = page.locator(PANEL)
  await expect(panel).toBeVisible()
  const d = await box(panel)
  const m = await box(page.locator('#map'))
  expect(d.x).toBeGreaterThanOrEqual(m.x)
  expect(d.x + d.width).toBeLessThanOrEqual(m.x + m.width)
  expect(d.y + d.height).toBeLessThanOrEqual(m.y + m.height)
  expect(m.y + m.height - (d.y + d.height), 'panel is not at the map bottom').toBeLessThanOrEqual(16)
  expect(d.width / m.width, 'panel is narrower than 80% of the map').toBeGreaterThanOrEqual(0.8)
  expect(d.height / m.height, 'panel is taller than 45% of the map').toBeLessThanOrEqual(0.46)
  expect(await page.evaluate(() => window.scrollY)).toBe(before)
  await expect(panel.locator('h2')).toContainText(/\S/)
  await context.close()
})

test('1440: the gauges share one row', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const gauges = page.locator(`${PANEL} .gauges .gauge`)
  await expect(gauges.first()).toBeVisible()
  expect(await gauges.count()).toBeGreaterThan(1)
  const tops = await gauges.evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top)))
  expect(new Set(tops).size, `gauge tops differ: ${tops}`).toBe(1)
  await context.close()
})

test('1440: the chart sits in the panel and follows the selected gauge', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const chart = page.locator(`${PANEL} .panel-chart__dock .chart-frame`)
  await expect(chart).toBeVisible()
  expect((await box(chart)).height).toBeGreaterThan(40)
  const plot = page.locator(`${PANEL} .panel-chart__dock`)
  const was = await plot.getAttribute('data-metric')
  await page.locator(`${PANEL} .gauges .gauge[aria-pressed="false"]`).first().click()
  await expect(plot).not.toHaveAttribute('data-metric', was)
  const now = await plot.getAttribute('data-metric')
  const pressed = await page.locator(`${PANEL} .gauges .gauge[aria-pressed="true"]`).count()
  expect(pressed).toBe(1)
  expect(now).toBeTruthy()
  // The only chart controls in the panel are the period chips.
  await expect(page.locator(`${PANEL} select, ${PANEL} .chart-field, ${PANEL} .colmenu`)).toHaveCount(0)
  await context.close()
})

test('1440: a period chip changes the period', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const plot = page.locator(`${PANEL} .panel-chart__dock`)
  const chips = page.locator(`${PANEL} .period-seg button:visible`)
  await expect(chips).toHaveCount(3)
  await expect(chips.nth(0)).toHaveAttribute('aria-pressed', 'true')
  await chips.nth(1).click()
  await expect(chips.nth(1)).toHaveAttribute('aria-pressed', 'true')
  await expect(plot).toHaveAttribute('data-period', '7d')
  await chips.nth(2).click()
  await expect(plot).toHaveAttribute('data-period', '30d')
  await context.close()
})

test('1440: fold hides the chart, shrinks the panel and survives a reload', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page, id } = await wideOpen(browser)
  const panel = page.locator(PANEL)
  const open = await box(panel)
  await panel.getByRole('button', { name: 'Fold' }).click()
  await expect(page.locator(`${PANEL} .chart-frame:visible`)).toHaveCount(0)
  await expect(panel.getByRole('button', { name: 'Expand' })).toBeVisible()
  await expect(panel.locator('.gauges .gauge').first()).toBeVisible()
  expect((await box(panel)).height).toBeLessThan(open.height * 0.6)
  expect(await page.evaluate(() => localStorage.getItem('kanarche:panel-folded'))).toBe('true')

  await page.goto(`/en/#sensor=${id}`)
  await page.reload()
  await expect(page.locator(`${PANEL} .gauges`)).toBeVisible({ timeout: 20000 })
  await expect(page.locator(PANEL).getByRole('button', { name: 'Expand' })).toBeVisible()
  await expect(page.locator(`${PANEL} .chart-frame:visible`)).toHaveCount(0)

  await page.locator(PANEL).getByRole('button', { name: 'Expand' }).click()
  await expect(page.locator(`${PANEL} .chart-frame`)).toBeVisible()
  expect(await page.evaluate(() => localStorage.getItem('kanarche:panel-folded'))).toBe('false')
  await context.close()
})

test('1440: the history button scrolls the section below the map into view', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const section = page.locator('[data-island="panel"] .sensor-panel')
  const before = await page.evaluate(() => window.scrollY)
  await page.locator(PANEL).getByRole('button', { name: /Full history/ }).click()
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(before)
  await expect.poll(async () => {
    const b = await section.boundingBox()
    return b !== null && b.y < 600 && b.y + b.height > 0
  }).toBe(true)
  await context.close()
})

test('1440: the legend and locate button sit above the panel, open or folded', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const check = async (when) => {
    const p = await box(page.locator(PANEL))
    for (const sel of ['.map-locate', '.scale--onmap', '.map-freshness', '.maplibregl-ctrl-attrib']) {
      const c = page.locator(sel).first()
      if (await c.count() === 0 || !(await c.isVisible())) continue
      expect(overlaps(p, await box(c)), `${sel} sits under the panel (${when})`).toBe(false)
    }
  }
  await check('open')
  await page.locator(PANEL).getByRole('button', { name: 'Fold' }).click()
  await expect(page.locator(PANEL).getByRole('button', { name: 'Expand' })).toBeVisible()
  await expect.poll(async () => (await box(page.locator('.map-locate'))).y + 16).toBeLessThan((await box(page.locator(PANEL))).y)
  await check('folded')
  await context.close()
})

test('1440: the selected hexagon stays above the panel and padding resets on close', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser, WIDE, { low: true })
  const cell = lastCell
  const probe = () => page.evaluate((c) => {
    const map = document.querySelector('[data-island="map"]').__map
    return { y: map.project(c).y, pad: map.getPadding().bottom, moving: map.isMoving() }
  }, cell)
  const panelTop = async () => (await box(page.locator(PANEL))).y - (await box(page.locator('#map'))).y
  await expect.poll(async () => { const s = await probe(); return !s.moving && s.y < (await panelTop()) }).toBe(true)
  expect((await probe()).pad).toBeGreaterThan(100)
  await page.locator(PANEL).getByRole('button', { name: 'Fold' }).click()
  await expect.poll(async () => (await probe()).pad).toBeLessThan((await box(page.locator(PANEL))).height + 40)
  await page.locator(PANEL).getByRole('button', { name: 'Expand' }).click()
  await expect.poll(async () => (await probe()).pad).toBeGreaterThan(100)
  await page.locator(`${PANEL} .map-dock__close`).click()
  await expect(page.locator(PANEL)).toHaveCount(0)
  await expect.poll(async () => (await probe()).pad).toBe(0)
  await context.close()
})

test('1440: a second hexagon swaps the panel in place', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page, id } = await wideOpen(browser)
  const first = await page.locator(`${PANEL} h2`).textContent()
  await tapHex(page, [id])
  await expect(page.locator(`${PANEL} h2`)).not.toHaveText(first)
  await expect(page.locator(PANEL)).toHaveCount(1)
  await expect(page.locator(`${PANEL} .panel-chart__dock`)).toHaveCount(1)
  await context.close()
})

test('1440: the close button and Escape close the panel, clear the hash and reset padding', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page, id } = await wideOpen(browser)
  const pad = () => page.evaluate(() => document.querySelector('[data-island="map"]').__map.getPadding().bottom)
  await expect(page.locator(PANEL)).toBeVisible()
  await expect.poll(pad).toBeGreaterThan(100)
  await page.locator(`${PANEL} .map-dock__close`).click()
  await expect(page.locator(PANEL)).toBeHidden()
  expect(page.url()).not.toContain('sensor=')
  await expect.poll(pad).toBe(0)

  await tapHex(page, [id])
  await expect(page.locator(PANEL)).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator(PANEL)).toBeHidden()
  expect(page.url()).not.toContain('sensor=')
  await expect.poll(pad).toBe(0)
  await context.close()
})

test('1440: a deep-linked sensor opens the panel on load', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const context = await browser.newContext({ viewport: WIDE })
  const page = await context.newPage()
  await page.goto('/en/#sensor=101')
  await expect(page.locator(`${PANEL} .gauges`)).toBeVisible({ timeout: 15000 })
  await context.close()
})

test('1440 to 900: the card returns under the map with one set of gauges and one chart', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  await expect(page.locator(PANEL)).toBeVisible()
  await expect(page.locator(`${PANEL} .chart-frame`)).toHaveCount(1)
  await page.setViewportSize({ width: 900, height: 600 })
  await expect(page.locator(PANEL)).toHaveCount(0)
  await expect(page.locator('[data-island="panel"] .sensor-panel .gauges')).toHaveCount(1)
  await expect(page.locator('.gauges')).toHaveCount(1)
  await expect(page.locator('.chart-frame')).toHaveCount(1)
  await expect(page.locator('[data-island="panel"] .chart-frame')).toHaveCount(1)
  expect(await page.evaluate(() => document.querySelector('[data-island="map"]').__map.getPadding().bottom)).toBe(0)
  await page.setViewportSize(WIDE)
  await expect(page.locator(`${PANEL} .gauges .gauge`).first()).toBeVisible()
  await expect(page.locator('.gauges')).toHaveCount(1)
  await context.close()
})

test('900x600: a tapped sensor scrolls its card into view', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser, { width: 900, height: 600 })
  await expect(page.locator(PANEL)).toHaveCount(0)
  const h2 = page.locator('[data-island="panel"] .sensor-panel h2')
  await expect(h2).toBeVisible()
  await expect.poll(async () => {
    const b = await h2.boundingBox()
    return b !== null && b.y >= 0 && b.y + b.height <= 600
  }).toBe(true)
  await context.close()
})

test('1440: fullscreen keeps the sensor sheet and no panel', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  await expect(page.locator(PANEL)).toBeVisible()
  await page.locator('.map__full').click()
  await expect(page.locator('.map-sensor-sheet')).toBeVisible()
  await expect(page.locator(PANEL)).toHaveCount(0)
  await expect(page.locator('.gauges')).toHaveCount(1)
  await expect(page.locator('.chart-frame')).toHaveCount(1)
  await page.locator('.map__full').click()
  await expect(page.locator('.map-sensor-sheet')).toHaveCount(0)
  await expect(page.locator(`${PANEL} .gauges`)).toHaveCount(1)
  await expect(page.locator('.gauges')).toHaveCount(1)
  await context.close()
})

// The page must not scroll when the panel opens, folds or expands.
test('1440: opening, folding and expanding the panel never scrolls the page', async ({ browser }, testInfo) => {
  testInfo.setTimeout(90000)
  const context = await browser.newContext({ viewport: WIDE })
  const page = await context.newPage()
  await page.goto('/en/')
  await page.waitForFunction(() => document.querySelector('[data-island="map"]')?.__map?.isStyleLoaded?.())
  await page.waitForTimeout(1000)
  await page.evaluate(() => document.querySelector('[data-island="map"]').__map.jumpTo({ center: [23.32, 42.69], zoom: 11 }))
  await page.waitForTimeout(1200)
  const scrollY = () => page.evaluate(() => window.scrollY)
  const start = await scrollY()
  await tapHex(page)
  await expect(page.locator(`${PANEL} .gauges`)).toBeVisible()
  await page.waitForTimeout(600)
  const opened = await scrollY()
  await page.locator(PANEL).getByRole('button', { name: /^(Fold|Expand)$/ }).click()
  await page.waitForTimeout(600)
  const folded = await scrollY()
  await page.locator(PANEL).getByRole('button', { name: /^(Fold|Expand)$/ }).click()
  await page.waitForTimeout(600)
  const expanded = await scrollY()
  expect({ opened, folded, expanded }).toEqual({ opened: start, folded: start, expanded: start })

  // deep link
  await page.goto('/en/#sensor=102')
  await page.reload()
  await expect(page.locator(`${PANEL} .gauges`)).toBeVisible({ timeout: 20000 })
  await page.waitForTimeout(1000)
  const deep = await scrollY()
  expect(deep).toBe(0)
  await context.close()
})

// Master's orientation button and wind note share the map with the panel.
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

// True when the element's centre is painted by the element itself, not by something over it.
const hittable = (locator) => locator.evaluate((el) => {
  const r = el.getBoundingClientRect()
  const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)
  return !!hit && el.contains(hit)
})

test('1440: the orientation control and wind note stay clear of the panel, open or folded', async ({ browser }, testInfo) => {
  testInfo.setTimeout(90000)
  const context = await browser.newContext({ viewport: WIDE })
  const page = await context.newPage()
  await mockWind(page)
  await prepareMap(page)
  await expect.poll(() => page.locator('.map-wind-label').evaluate((el) => el.hidden)).toBe(false)
  await tapHex(page)
  await expect(page.locator(PANEL)).toBeVisible()
  const check = async (when) => {
    await page.waitForTimeout(700)
    const p = await box(page.locator(PANEL))
    for (const sel of ['.map-orient__btn', '.map-wind-label', '.map-wind-label__toggle', '.map-locate']) {
      const c = page.locator(sel).first()
      expect(overlaps(p, await box(c)), `${sel} sits under the panel (${when})`).toBe(false)
      expect(await hittable(c), `${sel} is covered (${when})`).toBe(true)
    }
    const button = page.locator('.map-orient__btn')
    await button.click()
    const pop = page.locator('.map-orient__panel')
    await expect(pop).toBeVisible()
    expect(overlaps(p, await box(pop)), `the popover sits under the panel (${when})`).toBe(false)
    expect(await hittable(page.locator('.map-orient__tilt')), `the tilt slider is covered (${when})`).toBe(true)
    await page.locator('.map-orient__tilt').fill('40')
    await expect.poll(() => page.evaluate(() => document.querySelector('[data-island="map"]').__map.getPitch())).toBe(40)
    await page.locator('.map-orient__north-btn').click()
    await expect.poll(() => page.evaluate(() => document.querySelector('[data-island="map"]').__map.getPitch()), { timeout: 15000 }).toBe(0)
    await button.click()
    await expect(pop).toBeHidden()
  }
  await check('open')
  await page.locator(PANEL).getByRole('button', { name: 'Fold' }).click()
  await expect(page.locator(PANEL).getByRole('button', { name: 'Expand' })).toBeVisible()
  await check('folded')
  await context.close()
})

// OpenProject #697: the panel's depth, its gauge row and the station-info button.
test('1440: the gauge row spreads across the panel instead of clustering left', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const gauges = page.locator(`${PANEL} .gauges .gauge`)
  await expect(gauges.first()).toBeVisible()
  const dock = await box(page.locator(PANEL))
  const first = await box(gauges.first())
  const last = await box(gauges.last())
  expect((last.x + last.width - first.x) / dock.width, 'gauges span less than half of the panel').toBeGreaterThanOrEqual(0.5)
  for (const g of await gauges.all()) expect((await box(g)).width, 'a gauge is wider than its cap').toBeLessThanOrEqual(135)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await context.close()
})

test('1024: the spread gauge row does not overflow the panel', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser, { width: 1024, height: 768 })
  const row = await box(page.locator(`${PANEL} .gauges`))
  const dock = await box(page.locator(PANEL))
  expect(row.x + row.width).toBeLessThanOrEqual(dock.x + dock.width)
  const tops = await page.locator(`${PANEL} .gauges .gauge`).evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top)))
  expect(new Set(tops).size, `gauge tops differ: ${tops}`).toBe(1)
  await context.close()
})

test('1440: the panel is lifted off the map and its header is set apart from the body', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const style = (sel, prop) => page.locator(sel).first().evaluate((e, p) => getComputedStyle(e)[p], prop)
  expect(await style(PANEL, 'boxShadow')).not.toBe('none')
  expect(await style(PANEL, 'borderTopWidth')).toBe('1px')
  expect(await style(`${PANEL} .map-dock__head`, 'borderBottomWidth')).toBe('1px')
  await context.close()
})

test('1440: the info button opens the station sheet; Escape and its close button return focus to it', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page, id } = await wideOpen(browser)
  const info = page.locator(`${PANEL} .map-dock__head .panel-info`)
  await expect(info).toBeVisible()
  await expect(info).toHaveAttribute('aria-label', /\S/)
  await info.click()
  const sheet = page.locator('.about-sheet')
  await expect(sheet).toBeVisible()
  // Escape closes the sheet only; the panel and the sensor stay.
  await page.keyboard.press('Escape')
  await expect(sheet).toHaveCount(0)
  await expect(page.locator(PANEL)).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`sensor=${id}`))
  await expect(info).toBeFocused()
  await info.click()
  await sheet.locator('.about-sheet__close').click()
  await expect(sheet).toHaveCount(0)
  await expect(info).toBeFocused()
  await context.close()
})

test('1440: the info button stays in the panel header when folded and leaves with the panel', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  await page.locator(PANEL).getByRole('button', { name: 'Fold' }).click()
  await expect(page.locator(`${PANEL} .map-dock__head .panel-info`)).toBeVisible()
  await page.locator(`${PANEL} .map-dock__close`).click()
  await expect(page.locator(PANEL)).toHaveCount(0)
  await expect(page.locator('.panel-info')).toHaveCount(0)
  await context.close()
})
