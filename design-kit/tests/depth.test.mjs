/* depth.test.mjs — DESIGN.md §4.4, the two rules that keep depth readable.
 *
 * The kit used to carry depth in hairline borders and background layering, and
 * the page read as one flat grid of cells. It now carries it in a tinted page
 * ground plus three shadows. Two things keep that from drifting back:
 *
 *   1. A shadowed element does not also take a border. Both at once is the
 *      old system and the new one arguing, and it reads as neither.
 *   2. The shadow scale is a closed set of three tokens. A literal shadow in a
 *      rule is a fourth step nobody agreed to, and it will not track the dark
 *      theme, where the same lift needs a different value.
 */
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const kit = join(here, '..');
const css = readFileSync(join(kit, 'components.css'), 'utf8').replace(/\/\*[\s\S]*?\*\//g, '');
const tokens = readFileSync(join(kit, 'tokens.css'), 'utf8');

let pass = 0, fail = 0;
function ok(name, cond, detail) {
  if (cond) { pass++; console.log(`  PASS ${name}`); }
  else { fail++; console.log(`  FAIL ${name}${detail ? ' — ' + detail : ''}`); }
}

const rules = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)].map((m) => ({
  sel: m[1].trim().split('\n').pop().trim(),
  body: m[2],
}));

console.log('\n1. the elevation scale has exactly three steps');
{
  const declared = [...tokens.matchAll(/--elev-([a-z]+):/g)].map((m) => m[1]).sort();
  ok('tokens.css declares card, float and raised',
     declared.join(',') === 'card,float,raised', declared.join(','));
}

console.log('\n2. a shadowed element does not also take a border');
{
  const both = rules.filter((r) => /box-shadow:\s*var\(--elev/.test(r.body)
                                && /border:\s*1px/.test(r.body));
  ok('no rule carries both', both.length === 0, both.map((r) => r.sel).join(', '));
}

console.log('\n3. every shadow comes from the scale');
{
  const literal = rules.filter((r) => {
    const m = r.body.match(/box-shadow:\s*([^;]+);/);
    /* --focus-ring is a ring and an inset shadow is a marker drawn inside the
     * box. Neither is a lift, so neither belongs to the scale. */
    return m && !/var\(--(elev|focus-ring)/.test(m[1])
             && !/\bnone\b/.test(m[1]) && !/\binset\b/.test(m[1]);
  });
  ok('no rule spells a shadow out', literal.length === 0, literal.map((r) => r.sel).join(', '));
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
