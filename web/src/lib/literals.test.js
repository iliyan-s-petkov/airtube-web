import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect } from 'vitest'

// Paint values handed to WebGL layers and the chart canvas are configuration:
// no CSS rule can reach them, so theme.css cannot hold them and they arrive as
// data-* attributes. A hex literal anywhere in web/src is one that escaped.
const roots = ['src/lib', 'src/islands', 'src/components']

describe('no literal colours in web/src', () => {
  for (const root of roots) {
    for (const name of readdirSync(root).filter((f) => (f.endsWith('.js') || f.endsWith('.svelte')) && !f.endsWith('.test.js'))) {
      it(`${root}/${name}`, () => {
        const src = readFileSync(join(root, name), 'utf8')
        // Strip comments first: the rationale comments legitimately name colours.
        const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
        expect(code.match(/#[0-9a-fA-F]{3,8}\b/g)).toBeNull()
      })
    }
  }
})

// Values restated here instead of read from contract.json. The equality test in
// __tests__/contract.test.js catches a copy that DISAGREES; this catches a copy
// that agrees today and is free to drift tomorrow. Quote-agnostic: the single
// quotes this list used to require were evadable by typing double ones.
//
// '0.25', '100' and '0' are deliberately not on this list: they are ordinary
// numbers this code needs for other reasons (THIN_COVERAGE, SPEEDS, tier
// indices) and banning them would make the test fail on code that has nothing
// to do with the contract.
const contractConsumers = [
  'src/lib/hexes.js',
  'src/lib/timelapse.js',
  'src/lib/mapwindow.js',
  'src/islands/wind.js',
]
const bannedLiterals = [/["'`]24h["'`]/, /["'`]48h["'`]/, /["'`]7d["'`]/, /\b42\.7\d*\b/, /\b6371\b/]

describe('no restated contract literals in the generated contract\'s consumers', () => {
  for (const path of contractConsumers) {
    it(path, () => {
      const src = readFileSync(path, 'utf8')
      const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
      for (const pattern of bannedLiterals) {
        expect(code).not.toMatch(pattern)
      }
    })
  }
})
