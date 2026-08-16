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
