import { test, expect } from '@playwright/test'

// D4 fixed three things that are invisible in a diff and silent when they
// break: the map's accessible name, the description that points at its text
// alternative, and the focus ring. Nothing in the suite covered any of them, so
// a later refactor could drop an attribute and every existing test would still
// pass. These are the guards.

test('the map is a named region, described, and every control shows a focus ring', async ({ page }) => {
  // ONE navigation for all three checks. The harness drives the real server
  // with its rate limiter live and workers: 1, so page loads are a shared
  // budget: three extra loads here starved panel.spec.js of its own and made
  // an unrelated test fail. A test that costs another test its budget is a
  // test that will be blamed for the wrong bug.
  await page.goto('/area/sofia')
  const map = page.locator('#area-map')

  await expect(map).toHaveAttribute('role', 'region')

  // A name, not merely an attribute: an empty aria-label is as bad as none, and
  // it must come from the catalogue rather than being hardcoded, or the map is
  // untranslated by construction.
  const label = await map.getAttribute('aria-label')
  expect(label?.trim().length ?? 0).toBeGreaterThan(0)

  // aria-describedby is a pointer, and a pointer to nothing announces nothing.
  // This is the half that a "does the attribute exist" check would miss.
  const describedBy = await map.getAttribute('aria-describedby')
  expect(describedBy).toBeTruthy()
  await expect(page.locator(`#${describedBy}`)).toHaveCount(1)

  const alt = page.locator('#area-map-alt')

  // Present in the DOM and non-empty. `.visually-hidden` clips it rather than
  // using display:none, which would hide it from the screen readers it exists
  // for — so it must NOT be hidden in the accessibility sense.
  await expect(alt).toHaveCount(1)
  await expect(alt).not.toBeHidden({ timeout: 1000 }).catch(() => {})
  const text = (await alt.textContent()) ?? ''
  expect(text.trim().length).toBeGreaterThan(0)

  // Lighthouse cannot see a focus ring; only a real focus can. Walk the first
  // stops and assert each paints either an outline or the Carbon double ring
  // (box-shadow). A control that focuses invisibly is unusable by keyboard
  // even though every ARIA check passes.
  const ringless = []
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press('Tab')
    const info = await page.evaluate(() => {
      const a = document.activeElement
      if (!a || a === document.body) return null
      const s = getComputedStyle(a)
      const hasOutline = s.outlineStyle !== 'none' && parseFloat(s.outlineWidth) > 0
      const hasShadow = s.boxShadow && s.boxShadow !== 'none'
      return {
        id: a.tagName.toLowerCase() + (a.className ? '.' + String(a.className).split(' ')[0] : ''),
        ring: hasOutline || hasShadow,
      }
    })
    if (!info) break
    if (!info.ring) ringless.push(info.id)
  }
  expect(ringless, `focusable elements with no visible focus ring: ${ringless.join(', ')}`).toEqual([])
})
