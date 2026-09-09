import { test as base, expect } from '@playwright/test'

// One browser context for the whole run, because the rate limiter counts the
// static assets too. ratelimit.api (airbg.yaml) is a single per-IP bucket
// wrapping the WHOLE server, and a fresh Playwright context starts with an
// empty disk cache — so every spec file that opened its own context re-spent
// ~30 burst tokens on chunks the browser already had, and the file that
// happened to run when the bucket ran dry failed with its islands missing.
// A page per spec file, all from this one context: separate history, shared
// cache. Worker-scoped, and workers is 1, so the run pays for the assets once.
export const test = base.extend({
  ctx: [async ({ browser }, use) => {
    const context = await browser.newContext()
    await use(context)
    await context.close()
  }, { scope: 'worker' }],
})

export { expect }
