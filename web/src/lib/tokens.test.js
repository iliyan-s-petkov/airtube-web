import { readFileSync } from 'node:fs'
import { describe, it, expect } from 'vitest'

// uPlot paints a canvas, which inherits no custom property, so both chart line
// colours are config the server renders onto the island. Read from a sheet,
// the compared line shipped unstroked: the token's sheet does not load in
// production. These assert the config key and the attribute that carries it.
const CONFIG = '../airbg.yaml'
const APP_CSS = '../internal/web/static/app.css'
const THEME_CSS = '../internal/web/static/theme.css'
const INDEX = '../internal/web/templates/index.gohtml'
const AREA = '../internal/web/templates/area.gohtml'

describe('chart line colours', () => {
  it('are configured, both of them', () => {
    const yaml = readFileSync(CONFIG, 'utf8')
    expect(yaml).toMatch(/^\s+chart_line_colour:\s*"#[0-9a-fA-F]{6}"/m)
    expect(yaml).toMatch(/^\s+chart_compare_colour:\s*"#[0-9a-fA-F]{6}"/m)
  })

  it('reach the panel island on every page that mounts it', () => {
    for (const path of [INDEX, AREA]) {
      expect(readFileSync(path, 'utf8')).toContain('data-compare-colour="{{.ChartCompareColour}}"')
    }
  })

  it('are not also a CSS token', () => {
    expect(readFileSync(APP_CSS, 'utf8')).not.toMatch(/--chart-compare\b/)
    expect(readFileSync(THEME_CSS, 'utf8')).not.toMatch(/--chart-compare\b/)
  })
})
