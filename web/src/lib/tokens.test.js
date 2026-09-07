import { readFileSync } from 'node:fs'
import { describe, it, expect } from 'vitest'

// The tokens a CANVAS needs. A canvas cannot inherit a custom property, so
// islands read these out with getComputedStyle — and a token that is not in a
// sheet the page actually loads reads back as '', which paints nothing at all
// rather than failing loudly.
//
// base.gohtml loads static/theme.css ONLY when there is no built kit theme, so
// it cannot hold them: in production the kit theme wins and theme.css is never
// requested. app.css is loaded on both branches. That is what shipped the bug
// this file guards — the sensor panel's compared metric was drawn with an empty
// stroke and the reader saw one line where they had asked for two.
const APP_CSS = '../internal/web/static/app.css'
const THEME_CSS = '../internal/web/static/theme.css'

describe('canvas colour tokens', () => {
  it('defines --chart-compare in the sheet every page loads', () => {
    expect(readFileSync(APP_CSS, 'utf8')).toMatch(/--chart-compare:\s*#[0-9a-fA-F]{3,8}\s*;/)
  })

  // One definition, not two: a second copy in the branch-only sheet is how the
  // two drift, and the copy that shipped there is the one nobody could see.
  it('does not also define it in the fallback theme sheet', () => {
    expect(readFileSync(THEME_CSS, 'utf8')).not.toMatch(/^\s*--chart-compare:/m)
  })
})
