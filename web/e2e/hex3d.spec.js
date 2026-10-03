import { test, expect } from './fixtures.js'

// Rise on tilt: the hex cells are flat top-down and become value-height columns when pitched.
const SIZES = [{ width: 1440, height: 900 }, { width: 393, height: 873 }]
const EXTRUSION = 'airbg-hex-extrusion'

async function prepareMap(page) {
  await page.goto('/en/')
  await page.waitForFunction(() => document.querySelector('[data-island="map"]')?.__map?.isStyleLoaded?.())
  await page.waitForTimeout(1000)
  await expect.poll(() => page.evaluate(() => document.querySelector('[data-island="map"]').__map.isMoving())).toBe(false)
  await page.evaluate(() => {
    document.querySelector('.map-shell').scrollIntoView({ block: 'start', behavior: 'instant' })
    document.querySelector('[data-island="map"]').__map.jumpTo({ center: [23.32, 42.69], zoom: 12.5 })
  })
}

const pitchTo = (page, pitch) => page.evaluate((pitch) => {
  const map = document.querySelector('[data-island="map"]').__map
  map.jumpTo({ pitch, bearing: pitch ? 20 : 0 })
}, pitch)

// The extrusion's state as drawn: layer opacity and the rise/height of rendered columns.
const columns = (page) => page.evaluate((layer) => {
  const map = document.querySelector('[data-island="map"]').__map
  const fills = map.queryRenderedFeatures({ layers: ['airbg-hex-fill'] })
  const flat = fills.length
  // Distinct values on the flat grid: above 1 once the fine tier at the target zoom has painted.
  const flatValues = new Set(fills.map((f) => f.properties.value).filter((v) => v != null)).size
  if (!map.getLayer(layer)) return { flat, flatValues, opacity: null, cells: [] }
  const cells = map.queryRenderedFeatures({ layers: [layer] })
    .filter((f) => f.geometry.type === 'Polygon')
    .map((f) => ({
      value: f.properties.value ?? null,
      height: f.properties.height ?? 0,
      rise: map.getFeatureState({ source: 'airbg-hex-columns', id: f.id }).rise ?? 0,
    }))
  return { flat, flatValues, opacity: map.getPaintProperty(layer, 'fill-extrusion-opacity'), cells }
}, EXTRUSION)

// A sample of the columns once the map has stopped repainting them. After a zoom the column source briefly
// holds the old tier's cells beside the new tier's, and a column's height scales with its tier's cell width,
// so heights from two tiers do not order by value. Settled means the map is loaded and idle, every value
// stands at one height, and two samples a beat apart agree.
async function settledColumns(page) {
  let prev = null
  let settled = null
  await expect.poll(async () => {
    const idle = await page.evaluate(() => {
      const map = document.querySelector('[data-island="map"]').__map
      return map.loaded() && !map.isMoving()
    })
    const s = await columns(page)
    const heights = new Map()
    for (const c of s.cells) {
      if (c.value === null) continue
      if (!heights.has(c.value)) heights.set(c.value, new Set())
      heights.get(c.value).add(c.height)
    }
    const oneTier = [...heights.values()].every((h) => h.size === 1)
    const key = JSON.stringify(s.cells)
    const stable = key === prev
    prev = key
    if (idle && oneTier && stable && heights.size > 1 && s.cells.every((c) => c.rise === 1)) settled = s
    return settled !== null
  }, { timeout: 20000, intervals: [400] }).toBe(true)
  return settled
}

// A client point over a column naming one station, with the id the column there names.
const columnPoint = (page) => page.evaluate((layer) => {
  const map = document.querySelector('[data-island="map"]').__map
  const box = map.getCanvas().getBoundingClientRect()
  for (const f of map.queryRenderedFeatures({ layers: [layer] })) {
    if (f.geometry.type !== 'Polygon') continue
    const ring = f.geometry.coordinates[0].slice(0, -1)
    const c = [0, 1].map((i) => ring.reduce((a, p) => a + p[i], 0) / ring.length)
    const p = map.project(c)
    if (p.x < 30 || p.x > box.width - 30 || p.y < 80 || p.y > box.height - 30) continue
    const top = map.queryRenderedFeatures([p.x, p.y], { layers: [layer] })[0]
    const id = top?.properties?.sensorId
    if (id == null) continue
    const x = box.left + p.x
    const y = box.top + p.y
    if (document.elementFromPoint(x, y) !== map.getCanvas()) continue
    return { x, y, id: Number(id) }
  }
  return null
}, EXTRUSION)

for (const size of SIZES) {
  test(`${size.width}: cells rise into columns on tilt and flatten back`, async ({ browser }, testInfo) => {
    testInfo.setTimeout(90000)
    const context = await browser.newContext({ viewport: size })
    const page = await context.newPage()
    await prepareMap(page)
    await expect.poll(async () => (await columns(page)).flatValues, { timeout: 20000 }).toBeGreaterThan(1)

    // Top-down: no extrusion drawn.
    let s = await columns(page)
    expect(s.opacity).toBe(0)
    expect(s.cells).toHaveLength(0)

    // Tilted: columns at full rise, taller for a higher value.
    await pitchTo(page, 50)
    await expect.poll(async () => (await columns(page)).opacity).toBeGreaterThan(0)
    s = await settledColumns(page)
    // One entry per distinct value; a cell is listed once per tile it crosses.
    const valued = [...new Map(s.cells.filter((c) => c.value !== null).map((c) => [c.value, c])).values()]
      .sort((a, b) => a.value - b.value)
    expect(valued.length, JSON.stringify(s.cells)).toBeGreaterThan(1)
    for (let i = 1; i < valued.length; i++) expect(valued[i].height).toBeGreaterThan(valued[i - 1].height)
    expect(valued[0].height * valued[0].rise).toBeGreaterThan(0)

    // A click on a column opens its sensor.
    let pt = null
    await expect.poll(async () => { pt = await columnPoint(page); return pt !== null }, { timeout: 20000 }).toBe(true)
    await page.mouse.click(pt.x, pt.y)
    await expect(page).toHaveURL(new RegExp(`#.*sensor=${pt.id}\\b`))

    // Flat again.
    await pitchTo(page, 0)
    await expect.poll(async () => (await columns(page)).opacity).toBe(0)
    await expect.poll(async () => (await columns(page)).cells.length).toBe(0)
    expect((await columns(page)).flat).toBeGreaterThan(0)
    await context.close()
  })
}
