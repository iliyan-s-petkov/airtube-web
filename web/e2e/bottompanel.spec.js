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
  const context = await browser.newContext({ viewport: size, ...(opts.context ?? {}) })
  const page = await context.newPage()
  await prepareMap(page, opts.path)
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
  await context.close()
})

test('1440: the period select and the custom range in the panel drive its chart', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const plot = page.locator(`${PANEL} .panel-chart__dock`)
  const select = page.locator(`${PANEL} #dock-period-select`)
  await expect(select).toBeVisible()
  await select.selectOption('7d')
  await expect(plot).toHaveAttribute('data-period', '7d')
  await select.selectOption('custom')
  const fields = page.locator(`${PANEL} .chart-range input[type="datetime-local"]`)
  await expect(fields).toHaveCount(2)
  await fields.first().fill('2026-01-01T00:00')
  await page.locator(`${PANEL} .chart-range__now`).click()
  await expect(page.locator(`${PANEL} #dock-period-to`)).not.toHaveValue('')
  const m = await box(page.locator('#map'))
  for (const el of [fields.first(), fields.last(), page.locator(`${PANEL} .chart-range__now`)]) {
    const b = await box(el)
    expect(b.y + b.height, 'a range field is cut off by the map').toBeLessThanOrEqual(m.y + m.height)
    expect(await hittable(el), 'a range field is covered').toBe(true)
  }
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

test('1440 home: the section under the map is hidden and the panel has no history button', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  await expect(page.locator(`${PANEL} .gauges`)).toBeVisible()
  await expect(page.locator('[data-island="panel"]')).toBeHidden()
  await expect(page.locator(PANEL).getByRole('button', { name: /Full history/ })).toHaveCount(0)
  await context.close()
})

test('1440 area page: the section stays and the history button scrolls it into view', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser, WIDE, { path: '/en/area/sofia' })
  const section = page.locator('[data-island="panel"] .sensor-panel')
  await expect(section).toBeVisible()
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
  await expect(page.locator('[data-island="panel"] .panel-chart__controls #panel-period-select')).toBeVisible()
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
// Every corner and the centre, not just the centre: a control overlapping one end of a menu item still covers it.
const uncovered = (locator) => locator.evaluate((el) => {
  const r = el.getBoundingClientRect()
  const pts = [[0.5, 0.5], [0.05, 0.1], [0.95, 0.1], [0.05, 0.9], [0.95, 0.9]]
  return pts.every(([fx, fy]) => {
    const hit = document.elementFromPoint(r.left + r.width * fx, r.top + r.height * fy)
    return !!hit && el.contains(hit)
  })
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

// OpenProject #697 PR B: the panel carries every chart control of the section under the map.
const TOOLS = ['#dock-metric-menu', '#dock-period-select', '.chart-reset', '.panel-more']

test('1440: the panel carries the metric, period, reset and share controls', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  const d = await box(page.locator(PANEL))
  for (const sel of TOOLS) {
    const c = page.locator(`${PANEL} ${sel}`)
    await expect(c, `${sel} is not in the panel`).toBeVisible()
    const b = await box(c)
    expect(b.x >= d.x && b.x + b.width <= d.x + d.width, `${sel} overflows the panel`).toBe(true)
  }
  // Above the chart, below the gauges.
  const tools = await box(page.locator(`${PANEL} .panel-chart__tools`))
  expect(tools.y).toBeGreaterThanOrEqual((await box(page.locator(`${PANEL} .gauges`))).y)
  expect(tools.y + tools.height).toBeLessThanOrEqual((await box(page.locator(`${PANEL} .chart-frame`))).y + 1)
  await context.close()
})

test('1024: the toolbar fits in the panel, also with the custom range open (home, and an area page with nearby)', async ({ browser }, testInfo) => {
  testInfo.setTimeout(120000)
  for (const path of ['/en/', '/en/area/sofia']) {
  const { context, page } = await wideOpen(browser, { width: 1024, height: 768 }, { path })
  const fits = async (when) => {
    const d = await box(page.locator(PANEL))
    for (const sel of [...TOOLS, '#dock-nearby-menu', '.chart-range input', '.chart-range__now']) {
      for (const c of await page.locator(`${PANEL} ${sel}`).all()) {
        const b = await box(c)
        expect(b.x >= d.x - 0.5 && b.x + b.width <= d.x + d.width + 0.5, `${sel} overflows the panel (${path}, ${when})`).toBe(true)
        expect(b.y + b.height <= d.y + d.height + 0.5, `${sel} falls out of the panel (${path}, ${when})`).toBe(true)
      }
    }
    expect(await page.locator(PANEL).evaluate((el) => el.scrollWidth <= el.clientWidth), `the panel scrolls sideways (${when})`).toBe(true)
    expect((await box(page.locator(`${PANEL} .chart-frame`))).height, `the chart collapsed (${when})`).toBeGreaterThan(40)
  }
  await fits('closed')
  await page.locator(`${PANEL} #dock-period-select`).selectOption('custom')
  await expect(page.locator(`${PANEL} .chart-range input`)).toHaveCount(2)
  await page.locator(`${PANEL} #dock-period-from`).fill('2026-01-01T00:00')
  await page.locator(`${PANEL} .chart-range__now`).click()
  await expect(page.locator(`${PANEL} .chart-frame`)).toBeVisible()
  await fits('custom range')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await context.close()
  }
})

test('1440: folded, the panel shows only its header and the gauges', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  await page.locator(PANEL).getByRole('button', { name: 'Fold' }).click()
  await expect(page.locator(`${PANEL} .gauges .gauge`).first()).toBeVisible()
  await expect(page.locator(`${PANEL} .panel-chart__tools`)).toBeHidden()
  for (const sel of TOOLS) await expect(page.locator(`${PANEL} ${sel}`)).toBeHidden()
  await context.close()
})

test('1440: the metric menu opens over the map and its controls; Escape closes it before the panel', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page, id } = await wideOpen(browser)
  const button = page.locator(`${PANEL} #dock-metric-menu`)
  await button.click()
  const menu = page.locator('#dock-metric-menu-panel')
  await expect(menu).toBeVisible()
  const m = await box(page.locator('#map'))
  const b = await box(menu)
  expect(b.y >= m.y && b.y + b.height <= m.y + m.height, 'the menu is cut off by the map').toBe(true)
  // Upward, so a long list has the map's height to grow into, not the strip under the toolbar.
  expect(b.y + b.height, 'the menu opens down into the panel').toBeLessThanOrEqual((await box(button)).y)
  for (const opt of await menu.locator('.colmenu__opt').all()) {
    expect(await uncovered(opt), 'a menu option is covered').toBe(true)
  }
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()
  await expect(page.locator(PANEL)).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`sensor=${id}`))
  await expect(button).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.locator(PANEL)).toHaveCount(0)
  await context.close()
})

test('1440: the share menu copies the embed code and says so in the panel; Escape closes it first', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser, WIDE, { context: { permissions: ['clipboard-read', 'clipboard-write'] } })
  const more = page.locator(`${PANEL} .panel-more`)
  await more.click()
  const menu = page.locator(`${PANEL} .panel-menu`)
  await expect(menu).toBeVisible()
  const m = await box(page.locator('#map'))
  const b = await box(menu)
  expect(b.y >= m.y && b.y + b.height <= m.y + m.height, 'the menu is cut off by the map').toBe(true)
  expect(b.y + b.height, 'the menu opens down into the panel').toBeLessThanOrEqual((await box(more)).y)
  // It rises past the legend and the map's corner controls; none of them may cover an item.
  for (const item of await menu.getByRole('menuitem').all()) {
    expect(await uncovered(item), 'a menu item is covered').toBe(true)
  }
  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)
  await expect(page.locator(PANEL)).toBeVisible()
  await more.click()
  await menu.getByRole('menuitem', { name: 'Embed' }).click()
  await expect(page.locator(`${PANEL} [role="status"]`)).toContainText('Embed code copied')
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('<iframe')
  await context.close()
})

test('1440: the keyboard reaches every control in the panel', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await wideOpen(browser)
  await page.locator(`${PANEL} .map-dock__close`).focus()
  const seen = new Set()
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press('Tab')
    const hit = await page.evaluate((sels) => sels.filter((s) => document.activeElement?.matches(`.map-dock ${s}`)), TOOLS)
    hit.forEach((s) => seen.add(s))
    if (seen.size === TOOLS.length) break
  }
  expect([...seen].sort()).toEqual([...TOOLS].sort())
  await context.close()
})

test('1440 home: the fullscreen sheet has no link to the hidden section; the area page keeps it', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  for (const [path, links] of [['/en/', 0], ['/en/area/sofia', 1]]) {
    const { context, page } = await wideOpen(browser, WIDE, { path })
    await expect(page.locator(`${PANEL} .gauges`)).toBeVisible()
    await page.locator('.map__full').click()
    await expect(page.locator('.map-sensor-sheet')).toBeVisible()
    await expect(page.locator('.map-sensor-sheet__history:visible'), path).toHaveCount(links)
    await context.close()
  }
})

// The x labels are drawn on canvas: their boxes come from uPlot's own ticks, positions and axis font.
const xLabelBoxes = (page, scope) => page.locator(`${scope} .uplot`).first().evaluate((root) => {
  const u = root.__uplot
  const axis = u.axes[0]
  const ctx = document.createElement('canvas').getContext('2d')
  ctx.font = axis.font[0]
  const out = []
  ;(axis._splits ?? []).forEach((v, i) => {
    const text = axis._values?.[i]
    if (!text) return
    const x = u.valToPos(v, 'x')
    if (x < 0 || x > u.bbox.width / devicePixelRatio) return
    const w = ctx.measureText(text).width / devicePixelRatio
    out.push({ text, left: x - w / 2, right: x + w / 2 })
  })
  return out
})

const collisions = (boxes) => boxes.slice(1).filter((b, i) => b.left < boxes[i].right).map((b, i) => `${boxes[i].text}|${b.text}`)

// Sensor 101 is the one seeded with a day of P2 history, so its chart has an x axis to read.
async function openCharted(browser, size, path) {
  const context = await browser.newContext({ viewport: size })
  const page = await context.newPage()
  await prepareMap(page, path)
  let pick = null
  await expect.poll(async () => { pick = (await hexPoints(page, [])).find((p) => p.id === 101); return !!pick }, { timeout: 20000 }).toBe(true)
  await page.mouse.click(pick.x, pick.y)
  await expect(page).toHaveURL(/#.*sensor=101/)
  return { context, page }
}

test('the x-axis labels of the panel chart never run into each other (1024, 1440; EN, BG; 24h, 7d, 30d)', async ({ browser }, testInfo) => {
  testInfo.setTimeout(240000)
  for (const size of [{ width: 1024, height: 768 }, WIDE]) {
    for (const path of ['/en/', '/']) {
      const { context, page } = await openCharted(browser, size, path)
      for (const period of ['24h', '7d', '30d']) {
        await page.locator(`${PANEL} #dock-period-select`).selectOption(period)
        await expect(page.locator(`${PANEL} .panel-chart__dock`)).toHaveAttribute('data-period', period)
        await expect.poll(async () => (await xLabelBoxes(page, PANEL)).length, `${size.width} ${path} ${period}: no x labels`).toBeGreaterThan(1)
        const boxes = await xLabelBoxes(page, PANEL)
        expect(collisions(boxes), `${size.width} ${path} ${period}: ${boxes.map((b) => b.text).join(' ')}`).toEqual([])
      }
      await context.close()
    }
  }
})

test('1440 area page: the x-axis labels of the chart under the map never run into each other', async ({ browser }, testInfo) => {
  testInfo.setTimeout(60000)
  const { context, page } = await openCharted(browser, WIDE, '/en/area/sofia')
  const scope = '[data-island="panel"] .panel-chart'
  await expect.poll(async () => (await xLabelBoxes(page, scope)).length).toBeGreaterThan(1)
  expect(collisions(await xLabelBoxes(page, scope))).toEqual([])
  await context.close()
})


// The toolbar and gauges must leave the chart a plot to read, not an x axis with a sliver above it.
test('the panel chart keeps a readable plot (1024 and 1440 home, 1024 area page)', async ({ browser }, testInfo) => {
  testInfo.setTimeout(120000)
  const cases = [[{ width: 1024, height: 768 }, '/en/'], [{ width: 1024, height: 768 }, '/en/area/sofia'], [WIDE, '/en/']]
  for (const [size, path] of cases) {
    const { context, page } = await openCharted(browser, size, path)
    const plot = () => page.locator(`${PANEL} .uplot`).first().evaluate((root) => root.__uplot.bbox.height / devicePixelRatio)
    await expect.poll(plot, `${size.width} ${path}: the plot is squeezed out`).toBeGreaterThanOrEqual(60)
    const frame = await box(page.locator(`${PANEL} .chart-frame`))
    const canvas = await box(page.locator(`${PANEL} .uplot canvas`).first())
    expect(canvas.y + canvas.height, `${size.width} ${path}: the chart runs past its frame`).toBeLessThanOrEqual(frame.y + frame.height + 1)
    await context.close()
  }
})
