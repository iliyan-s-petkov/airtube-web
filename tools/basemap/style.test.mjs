/* style.json — validated against the spec, and its POI groups checked for the
 * two properties a category control depends on.
 *
 * WHY
 * ---
 * This file is deployed by hand to /var/lib/airbg/tiles/style.json, so a
 * mistake in it is not caught by any build. And the way a MapLibre style fails
 * is the worst kind: an unknown font stack, a malformed expression or a layer
 * matching nothing renders a BLANK MAP with no error — the failure docs/tiles.md
 * warns about. There is nothing on screen to debug.
 *
 * Two checks, both cheap:
 *
 * 1. The style validates against MapLibre's own spec. The validator is already
 *    in web/node_modules (@maplibre/maplibre-gl-style-spec, a dependency of the
 *    app's vendored renderer), so this adds nothing.
 *
 * 2. Every POI class lands in EXACTLY ONE group — disjoint and exhaustive —
 *    evaluated through MapLibre's own featureFilter rather than by reading the
 *    JSON. The layer control offers one checkbox per group, so a class in two
 *    groups is a POI that two switches fight over, and a class in none is a POI
 *    with no switch at all. The `!in` catch-all is what makes the second
 *    impossible, and this is what proves it.
 *
 * RUN
 *   node tools/basemap/style.test.mjs
 */
import { createRequire } from 'node:module';
const require = createRequire(import.meta.url);
const spec = require('../../web/node_modules/@maplibre/maplibre-gl-style-spec/dist/index.cjs');
/* A path argument makes the suite runnable against a mutated copy, which is
 * how it was checked for the failures it is meant to catch. */
const style = require(process.argv[2] ? require('node:path').resolve(process.argv[2]) : './style.json');

let fail = 0;
const ok = (name, cond, extra = '') => {
  console.log((cond ? '  PASS ' : '  FAIL ') + name + (cond ? '' : ' — ' + extra));
  if (!cond) fail++;
};

console.log('\n1. the style is valid');
const errs = spec.validateStyleMin(style);
ok('0 errors against the MapLibre style spec', errs.length === 0,
   errs.map(e => e.message).join('; '));

console.log('\n2. every label asks for a font the glyph endpoint serves');
/* A stack the endpoint does not carry renders nothing at all, with no error. */
const SERVED = ['Noto Sans Regular', 'Noto Sans Medium'];
const fonts = new Set(style.layers.flatMap(l => l.layout?.['text-font'] ?? []));
ok('only ' + SERVED.join(' / '), [...fonts].every(f => SERVED.includes(f)),
   [...fonts].filter(f => !SERVED.includes(f)).join(', '));

console.log('\n3. every layer is in a group the control can offer');
const ungrouped = style.layers.filter(l => !l.metadata?.['airbg:group']).map(l => l.id);
ok('no layer without airbg:group', ungrouped.length === 0, ungrouped.join(', '));

console.log('\n4. the POI groups are disjoint and exhaustive');
const dots = style.layers.filter(l => l['source-layer'] === 'poi' && l.type === 'circle');
const groups = dots.map((l, i) => ({
  group: l.metadata['airbg:group'],
  f: spec.featureFilter(l.filter, `layers[${i}].filter`),
}));
const named = [...new Set(dots.flatMap(l => {
  const m = JSON.stringify(l.filter).match(/\["literal",\[(.*?)\]\]/);
  return m ? JSON.parse('[' + m[1] + ']') : [];
}))];
const hits = (cls) => groups
  .filter(g => g.f.filter({ zoom: 16 }, { type: 1, properties: { class: cls } }))
  .map(g => g.group);

const multi = named.filter(c => hits(c).length > 1);
const none = named.filter(c => hits(c).length === 0);
ok(`${named.length} enumerated classes, none in two groups`, multi.length === 0,
   multi.map(c => c + ' -> ' + hits(c).join('+')).join(', '));
ok('none unrouted', none.length === 0, none.join(', '));

/* The catch-all is the point: a class nobody thought of still draws. */
const strangers = ['casino', 'veterinary', 'picnic_site', 'zoo', 'townhall', 'nightclub'];
const stray = strangers.filter(c => hits(c).join() !== 'poi-other');
ok('an unenumerated class falls to poi-other', stray.length === 0,
   stray.map(c => c + ' -> ' + (hits(c).join('+') || 'NOTHING')).join(', '));

console.log(fail ? `\n${fail} failed` : '\nall checks passed');
process.exit(fail ? 1 : 0);
