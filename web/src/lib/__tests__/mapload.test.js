import { readFileSync } from 'node:fs'
import { describe, it, expect } from 'vitest'

// installMapLoad fills a `subs` object the caller owns, and mount()'s returned
// `stop` optional-calls each property. Nothing executes that path in a test:
// the handler needs a real MapLibre map, and no caller in the app invokes
// `stop` at all (islands mount once per page load). So dropping an assignment,
// or misspelling one on either side, is invisible at runtime AND to the suite —
// the teardown just silently stops running and leaks a subscription per mount.
//
// Read from source rather than executed for that reason. It is a weaker test
// than calling the code, and it is the one that can exist today: it pins the
// two lists against each other, which is the whole contract between them.
const load = readFileSync('src/lib/mapload.js', 'utf8')
const island = readFileSync('src/islands/map.js', 'utf8')

const assigned = [...load.matchAll(/\bsubs\.(\w+)\s*=/g)].map((m) => m[1]).sort()
const released = [...island.matchAll(/\bsubs\.(\w+)\?\.\(\)/g)].map((m) => m[1]).sort()

describe('the load handler\'s teardown handles', () => {
  it('assigns the four the island knows about', () => {
    expect(assigned).toEqual(['unfilter', 'unfilterSource', 'unprovide', 'unsubscribe'])
  })

  // Both directions matter. An assignment with no release leaks; a release with
  // no assignment is a no-op hiding a teardown that was meant to run.
  it('releases exactly what it assigns', () => {
    expect(released).toEqual(assigned)
  })
})
