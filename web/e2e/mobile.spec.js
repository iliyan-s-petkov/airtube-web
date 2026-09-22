import { test as base, expect } from './fixtures.js'

// One worker-scoped mobile context for the file: a fresh context per test
// re-downloads the bundle and trips the app's per-IP rate limit (429).
const test = base.extend({
  mobileCtx: [async ({ browser }, use) => {
    const context = await browser.newContext({ isMobile: true, hasTouch: true, deviceScaleFactor: 2 })
    await use(context)
    await context.close()
  }, { scope: 'worker' }],
})

test.describe('phone layout does not widen the viewport', () => {
  for (const path of ['/en', '/en/area/sofia#sensor=101']) {
    test(`${path} stays at 390px wide`, async ({ mobileCtx }) => {
      const page = await mobileCtx.newPage()
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto(path)
      expect(await page.evaluate(() => window.innerWidth)).toBe(390)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
      await page.close()
    })
  }

  test('/en first viewport: masthead one line, map above the fold', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const masthead = await page.locator('.masthead').boundingBox()
    expect(masthead.height).toBeLessThanOrEqual(56)
    const toolbar = await page.locator('.toolbar').boundingBox()
    expect(toolbar.height).toBeLessThanOrEqual(64)
    const map = await page.locator('#map').boundingBox()
    expect(map.y).toBeLessThanOrEqual(200)
    expect(map.height).toBeGreaterThanOrEqual(0.6 * 844)
    await page.close()
  })

  // The on-map chrome: the legend pill sits on the map, every overlay is a
  // real touch target, and the zoom pair yields to pinch on a coarse pointer.
  test('/en on-map chrome: legend on the map, 44px targets, zoom hidden', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const map = await page.locator('#map').boundingBox()
    const scale = await page.locator('.scale--onmap').boundingBox()
    expect(scale.x).toBeGreaterThanOrEqual(map.x)
    expect(scale.y).toBeGreaterThanOrEqual(map.y)
    expect(scale.x + scale.width).toBeLessThanOrEqual(map.x + map.width)
    expect(scale.y + scale.height).toBeLessThanOrEqual(map.y + map.height)
    for (const sel of ['.map__layers', '.map-locate', '.map__full']) {
      const box = await page.locator(sel).boundingBox()
      expect(box.width).toBeGreaterThanOrEqual(44)
      expect(box.height).toBeGreaterThanOrEqual(44)
    }
    await expect(page.locator('.map-zoom__btn[data-act="in"]')).toBeHidden()
    await expect(page.locator('.map-zoom__btn[data-act="out"]')).toBeHidden()
    await page.close()
  })

  // MapLibre's compact attribution opens itself on load; collapsed to the (i)
  // it does not sit open over the map, and the button is a real touch target.
  test('/en attribution collapses to the (i), at a 44px target', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const attrib = page.locator('.maplibregl-ctrl-attrib')
    await expect(attrib).toBeVisible()
    await expect(attrib).not.toHaveClass(/maplibregl-compact-show/)
    const box = await page.locator('.maplibregl-ctrl-attrib-button').boundingBox()
    expect(box.width).toBeGreaterThanOrEqual(44)
    expect(box.height).toBeGreaterThanOrEqual(44)
    // The glyph is a 24px background-image; at a 44px target it tiled 2x2.
    const repeat = await page.locator('.maplibregl-ctrl-attrib-button')
      .evaluate((el) => getComputedStyle(el).backgroundRepeat)
    expect(repeat).toBe('no-repeat')
    // The wind note stays hidden without a seeded forecast (/api/v1/wind
    // 503s in this fixture) — force it open the same way chrome.showWind(true, …)
    // does, so the phone rules below get a real element to measure.
    await page.evaluate(() => { document.querySelector('.map-wind-label').hidden = false })
    const label = page.locator('.map-wind-label__text-label')
    if (await label.count()) {
      const labelBox = await label.boundingBox()
      expect(labelBox.width).toBeLessThanOrEqual(1)
    }
    const toggle = page.locator('.map-wind-label__toggle')
    if (await toggle.isVisible()) {
      const mapBox = await page.locator('#map').boundingBox()
      const toggleBox = await toggle.boundingBox()
      expect(toggleBox.y + toggleBox.height).toBeLessThanOrEqual(mapBox.y + mapBox.height - 44)
    }
    await page.close()
  })

  // Folded by default on a phone (Task 5b); tapping it unfolds to a real
  // width and rides above .map-freshness rather than under it.
  test('/en legend pill: folded by default, full width and on top when open', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const scale = page.locator('.scale--onmap')
    await expect(scale).not.toHaveAttribute('open', '')
    const toggle = page.locator('.scale__toggle')
    await expect(toggle).toBeVisible()
    // Retrying poll rather than a single boundingBox() read: a debounced
    // repaint (moveend, once the opening jumpTo settles) can replace the
    // legend's children between two separate round trips to the browser.
    await expect.poll(async () => (await toggle.boundingBox())?.height ?? 0)
      .toBeGreaterThanOrEqual(44)

    await toggle.click()

    await expect(scale).toHaveAttribute('open', '')
    await expect.poll(async () => (await scale.boundingBox())?.width ?? 0)
      .toBeGreaterThanOrEqual(250)
    await expect.poll(async () => (await page.locator('.scale__bands--vertical').boundingBox())?.width ?? 0)
      .toBeGreaterThanOrEqual(250)
    const openBox = await scale.boundingBox()
    const fresh = await page.locator('.map-freshness').boundingBox()
    const scaleZ = await scale.evaluate((el) => Number(getComputedStyle(el).zIndex))
    const freshZ = await page.locator('.map-freshness').evaluate((el) => Number(getComputedStyle(el).zIndex))
    const scaleAboveFresh = openBox.y + openBox.height <= fresh.y || scaleZ > freshZ
    expect(scaleAboveFresh).toBe(true)
    await page.close()
  })

  // Owner feedback from prod (Task 7b): the folded pill overshot the 44px
  // floor (measured 56px), its label read oversized, and it overlapped the
  // freshness row above it by 8px. All three fixed by one padding/offset pass.
  test('/en folded legend pill: 44px tall, small label, clear of the freshness row', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const scale = page.locator('.scale--onmap')
    // A stored fold preference outlives one test in this worker-scoped
    // context (Task 5b's own default-folded test opens it), so this closes
    // it rather than assuming the fresh-profile default.
    if (await scale.evaluate((el) => el.hasAttribute('open'))) {
      await page.locator('.scale__toggle').click()
    }
    await expect(scale).not.toHaveAttribute('open', '')

    await expect.poll(async () => (await scale.boundingBox())?.height ?? 0)
      .toBeLessThanOrEqual(44)

    const labelSize = await page.locator('.scale__toggle-label')
      .evaluate((el) => parseFloat(getComputedStyle(el).fontSize))
    expect(labelSize).toBeLessThanOrEqual(15)

    await expect.poll(async () => {
      const pillBox = await scale.boundingBox()
      const freshBox = await page.locator('.map-freshness').boundingBox()
      if (!pillBox || !freshBox) return null
      return pillBox.y - (freshBox.y + freshBox.height)
    }).toBeGreaterThanOrEqual(8)
    await page.close()
  })

  // Collapsed, the wind note is an icon, not a card with empty space beside it.
  test('/en collapsed wind note is icon-sized, no empty box', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    // No seeded forecast in this fixture (/api/v1/wind 503s), so force the
    // note visible and closed the same way the attribution test does. Waited
    // for first: chrome.js mounts it once the map island loads, and evaluate
    // ran ahead of that mount without this.
    // A debounced repaint (moveend, once the opening jumpTo settles) can
    // replace the note between two round trips (see the legend spec above),
    // reverting `hidden` — so this re-sets it on every poll instead of once.
    const note = page.locator('.map-wind-label')
    await expect.poll(async () => {
      await page.evaluate(() => {
        const el = document.querySelector('.map-wind-label')
        if (el) el.hidden = false
      })
      return (await note.boundingBox())?.width ?? 999
    }).toBeLessThanOrEqual(48)
    await expect(note).not.toHaveAttribute('open', '')
    await page.close()
  })

  // Owner feedback (Task 7c): the collapsed note used to draw as an ~80px
  // white card. Icon only now, no fill, parked in the bottom-right column
  // above the locate button rather than floating in empty map.
  test('/en collapsed wind note: icon only, transparent, stacked above locate', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const note = page.locator('.map-wind-label')
    const map = page.locator('#map')
    await expect.poll(async () => {
      await page.evaluate(() => {
        const el = document.querySelector('.map-wind-label')
        if (el) el.hidden = false
      })
      const box = await note.boundingBox()
      return box ? Math.max(box.width, box.height) : 999
    }).toBeLessThanOrEqual(44)
    const bg = await note.evaluate((el) => getComputedStyle(el).backgroundColor)
    expect(bg).toBe('rgba(0, 0, 0, 0)')
    const noteBox = await note.boundingBox()
    const mapBox = await map.boundingBox()
    expect(mapBox.x + mapBox.width - (noteBox.x + noteBox.width)).toBeLessThanOrEqual(8)
    const locateBox = await page.locator('.map-locate').boundingBox()
    expect(noteBox.y + noteBox.height).toBeLessThanOrEqual(locateBox.y)
    await page.close()
  })

  // Review round 1 (Task 7c): the open note's z-index (1) lost to the
  // freshness card (3) and the open legend key (2) in the same corner —
  // a click on the note's own text hit whichever card was drawn on top.
  test('/en open wind note draws above the freshness card, clear of the map edge', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    await page.waitForSelector('.map-wind-label', { state: 'attached' })
    // No seeded forecast in this fixture, so the empty note never grows wide
    // enough to actually overlap the freshness card — a real forecast
    // sentence (see chrome.js's showWind) is two sentences and a model name,
    // which is what pushes the open card out toward max-inline-size.
    await page.evaluate(() => {
      const el = document.querySelector('.map-wind-label')
      el.hidden = false
      el.querySelector('.map-wind-label__text').textContent =
        'Forecast wind arrows are modelled, not measured, and may diverge from the sensors below. Source: a placeholder weather model used for this test.'
    })
    await page.locator('.map-wind-label__toggle').click()
    const note = page.locator('.map-wind-label')
    await expect(note).toHaveAttribute('open', '')

    const map = await page.locator('#map').boundingBox()
    await expect.poll(async () => {
      const box = await note.boundingBox()
      return box ? box.x + box.width : 999
    }).toBeLessThanOrEqual(map.x + map.width)

    // The overlap point the review flagged: the freshness card's own centre,
    // which the open note's wide card now covers. elementFromPoint there
    // must resolve inside the note, not the card underneath it.
    const hit = await page.evaluate(() => {
      const note = document.querySelector('.map-wind-label')
      const fresh = document.querySelector('.map-freshness')
      const nr = note.getBoundingClientRect()
      const fr = fresh.getBoundingClientRect()
      const x = Math.max(nr.left, fr.left) + Math.min(nr.right, fr.right - Math.max(nr.left, fr.left)) / 2
      const y = Math.max(nr.top, fr.top) + Math.min(nr.bottom, fr.bottom - Math.max(nr.top, fr.top)) / 2
      const overlaps = nr.left < fr.right && nr.right > fr.left && nr.top < fr.bottom && nr.bottom > fr.top
      const top = document.elementFromPoint(x, y)
      return { overlaps, insideNote: note.contains(top) || note === top }
    })
    expect(hit.overlaps).toBe(true)
    expect(hit.insideNote).toBe(true)
    await page.close()
  })

  // Owner feedback (Task 7c): the shared bottom-left card was 56px tall with
  // 44/48px buttons inside; both fold to a 44px card.
  test('/en freshness card and idle play button are 44/36px, not 56/48px', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    await expect.poll(async () => (await page.locator('.map-freshness').boundingBox())?.height ?? 0)
      .toBeLessThanOrEqual(44)
    await expect.poll(async () => (await page.locator('.map-freshness').boundingBox())?.height ?? 0)
      .toBeGreaterThanOrEqual(40)
    const play = page.getByRole('button', { name: 'Play the animation' })
    await expect.poll(async () => (await play.boundingBox())?.height ?? 0).toBeLessThanOrEqual(36)
    const windowBtn = page.locator('.map-window__btn')
    await expect.poll(async () => (await windowBtn.boundingBox())?.height ?? 0).toBeLessThanOrEqual(36)
    await page.close()
  })

  // Owner feedback (Task 7c): a white circle read as furniture; bare now.
  test('/en locate button has no background fill', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const bg = await page.locator('.map-locate').evaluate((el) => getComputedStyle(el).backgroundColor)
    expect(bg).toBe('rgba(0, 0, 0, 0)')
    await page.close()
  })

  // Task 9: two readout cards per row, a compact card, and a footer whose
  // links are still real touch targets.
  test('/en readouts: two per row, compact card, metric prefix hidden', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const cards = page.locator('.readout')
    const first = cards.nth(0)
    const second = cards.nth(1)
    await expect.poll(async () => {
      const a = await first.boundingBox()
      const b = await second.boundingBox()
      return a && b ? a.y === b.y : null
    }).toBe(true)
    const firstBox = await first.boundingBox()
    expect(firstBox.height).toBeLessThanOrEqual(175)

    // Metric span: visually gone (a near-zero box, not a real reading a
    // sighted user could mistake for content) but still in the DOM text a
    // screen reader gets — innerText honours display:none, textContent
    // doesn't, so this pair only agrees if the span is merely clipped.
    const metric = first.locator('.readout__metric')
    const metricBox = await metric.boundingBox()
    expect(metricBox.width).toBeLessThanOrEqual(1)
    expect(metricBox.height).toBeLessThanOrEqual(1)
    const label = first.locator('.readout__label')
    const [inner, raw] = await Promise.all([label.innerText(), label.evaluate((el) => el.textContent)])
    expect(inner.replace(/\s+/g, ' ').trim()).toBe(raw.replace(/\s+/g, ' ').trim())

    // The caption is one line at the same size as the footnote, not the
    // body-text default that would wrap a two-line label.
    await expect(label).toHaveCSS('font-size', '12px')
    await expect(label).toHaveCSS('white-space', 'nowrap')

    const footerLink = page.locator('.footer a').first()
    const footerBox = await footerLink.boundingBox()
    expect(footerBox.height).toBeGreaterThanOrEqual(40)
    await page.close()
  })
})

// Replay on a phone: one play button in the corner until there is something to
// play, and the refresh controls parked inside the window panel rather than
// taking a second slot in the same corner.
test.describe('phone replay folds behind one button', () => {
  test('/en play button unfolds the row, exit folds it again', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')

    const map = await page.locator('#map').boundingBox()
    const play = page.locator('.map-play__btn[aria-pressed]')
    await expect(play).toBeVisible()
    // Retrying poll rather than one boundingBox() read: the corner row is
    // repainted by the map's own debounced refresh (see the legend spec).
    await expect.poll(async () => {
      const box = await page.locator('.map-freshness').boundingBox()
      if (!box) return false
      return box.x >= map.x && box.y >= map.y
        && box.x + box.width <= map.x + map.width
        && box.y + box.height <= map.y + map.height
    }).toBe(true)

    // The refresh controls moved into the window panel, so the corner holds
    // the window button and the play button and nothing else.
    await expect(page.locator('.map-window__panel .map-window__footer .data-refresh')).toHaveCount(1)

    await expect(page.locator('.map-play__scrub')).toBeHidden()

    // Open the key first: pressing play has to FOLD it, not hide it, and an
    // already-folded key would prove nothing.
    const scale = page.locator('.scale--onmap')
    if (!await scale.evaluate((el) => el.hasAttribute('open'))) {
      await page.locator('.scale__toggle').click()
    }
    await expect(scale).toHaveAttribute('open', '')
    // A surface on a phone, not the kit's haloed text: the key sits over the
    // replay bar's corner and has to stay legible.
    expect(await scale.evaluate((el) => getComputedStyle(el).boxShadow)).not.toBe('none')

    // Keyboard, not a click: the open key covers this corner by design (it
    // rides above the row, Task 5b), and that is the state under test.
    await play.focus()
    await play.press('Enter')
    await expect(page.locator('.map-play--open')).toHaveCount(1)
    await expect(page.locator('.map-play__scrub')).toBeVisible()
    await expect.poll(async () => (await page.locator('.map-play').boundingBox())?.height ?? 0)
      .toBeLessThanOrEqual(56)
    // Folded, still on the map: the reader can unroll it again mid-replay.
    await expect(scale).toBeVisible()
    await expect(scale).not.toHaveAttribute('open', '')

    const exit = page.locator('.map-play__exit')
    await exit.evaluate((el) => el.scrollIntoView({ block: 'center' }))
    await exit.click()
    await expect(page.locator('.map-play--open')).toHaveCount(0)
    await expect(page.locator('.map-play__scrub')).toBeHidden()
    await page.close()
  })
})

test.describe('landscape phone keeps the map', () => {
  test('/en map has width at 844x390', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 844, height: 390 })
    await page.goto('/en')
    const box = await page.locator('#map').boundingBox()
    expect(box.width).toBeGreaterThan(700)
    expect(box.height).toBeGreaterThan(150)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(844)
    await page.close()
  })

  test('/en rotating 390x844 -> 844x390 keeps the map, no reload', async ({ mobileCtx }) => {
    const page = await mobileCtx.newPage()
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/en')
    const before = await page.locator('#map').boundingBox()
    expect(before.width).toBe(390)
    await page.setViewportSize({ width: 844, height: 390 })
    const after = await page.locator('#map').boundingBox()
    expect(after.width).toBeGreaterThan(700)
    expect(after.height).toBeGreaterThan(150)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(844)
    await page.close()
  })
})
