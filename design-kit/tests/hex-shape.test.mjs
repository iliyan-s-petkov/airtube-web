/* hex-shape.test.mjs — a hex stays a hexagon, everywhere.
 *
 * A hex is an AGGREGATE of sensors within a bin. The bin is a shape on the
 * ground; it does not stop at a province edge or a coastline, and the sensors
 * inside it do not either. Trimming a hex to a border would draw a shape that
 * claims the bin covers less ground than it does, and it would do it precisely
 * where cross-border readings are the whole point (§5.2 — foreign data is real
 * data, not context and not absence).
 *
 * So: six points, always. No clip-path, no mask, no per-province trimming. This
 * file exists because "make the hexes tidy at the border" is a reasonable-
 * sounding request that would silently break what the layer means.
 */
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const kit = join(here, '..');
const render = readFileSync(join(kit, 'ui_kits/app/map-render.js'), 'utf8');
const css = readFileSync(join(kit, 'components.css'), 'utf8');

let pass = 0, fail = 0;
function ok(name, cond, detail) {
  if (cond) { pass++; console.log(`  PASS ${name}`); }
  else { fail++; console.log(`  FAIL ${name}${detail ? ' — ' + detail : ''}`); }
}

console.log('\n1. the hexagon is built from six points');
{
  /* The loop that emits the polygon. If someone changes the vertex count the
   * mark stops being a hexagon, which is the shape the bin actually is. */
  const loop = render.match(/for \(var i = 0; i < 6; i\+\+\)/);
  ok('the vertex loop runs exactly six times', !!loop);

  const angle = render.match(/60 \* i/);
  ok('vertices are 60° apart', !!angle);
}

console.log('\n2. nothing clips or masks the hex layer');
{
  /* Scoped to the hex drawing region rather than the whole file, so an
   * unrelated clip elsewhere does not fail this and, more importantly, a clip
   * added HERE cannot pass it. */
  const start = render.indexOf("el('g', { class: 'map-hexes' })");
  ok('the hex layer is found', start > 0);
  const region = render.slice(start, start + 9000);

  ok('no clip-path on the hex layer',
     !/clip-?path/i.test(region), 'a clipped hex claims a smaller bin than it measured');
  ok('no mask on the hex layer', !/\bmask\b/i.test(region));

  ok('no clip-path in the hex CSS',
     !/\.map-hex[^{]*\{[^}]*clip-path/i.test(css));
}

console.log('\n3. a hex is not filtered out for crossing a border');
{
  const start = render.indexOf("el('g', { class: 'map-hexes' })");
  const region = render.slice(start, start + 9000);
  /* The only cull allowed is the viewport one: a hex entirely off screen is
   * not drawn. A cull by country, province or coastline is not. */
  ok('hexes are culled by the viewport only',
     /cx < -hr \|\| cy < -hr/.test(region));
  ok('no country or province test gates the draw',
     !/if \([^)]*country[^)]*\)\s*return/i.test(region),
     'foreign hexes must draw — they are the cross-border evidence');
}

console.log('\n4. a foreign hex is styled, not hidden');
{
  ok('the foreign class exists', /map-hex--foreign/.test(render));
  ok('foreign hexes are not display:none',
     !/\.map-hex--foreign[^{]*\{[^}]*display:\s*none/i.test(css));
  ok('foreign hexes keep a served fill (no CSS fill override)',
     !/\.map-hex--foreign[^{]*\{[^}]*[^-]fill:/i.test(css),
     'the served colour is an attribute; a CSS fill would win and grey them out');
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
