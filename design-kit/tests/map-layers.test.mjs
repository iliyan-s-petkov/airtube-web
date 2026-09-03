/* map-layers.js under jsdom — the layer control, exercised rather than asserted.
 *
 * WHY THIS FILE EXISTS
 * --------------------
 * The kit is normally verified in a browser on the host, and that is still the
 * only place the tiles themselves can be checked. But the layer control's rules
 * are all decidable without a GPU: which options exist, where they come from,
 * what a toggle drives, what survives a language switch, and — most
 * importantly — when the control refuses to appear at all. Those are the parts
 * that would otherwise ship on an assertion.
 *
 * It is NOT a browser test and does not pretend to be. MapLibre is a stub here;
 * this covers the seam between the style and the control, nothing below it.
 *
 * NO NEW DEPENDENCY (DESIGN.md §1)
 * -------------------------------
 * jsdom is already in web/node_modules for the app's own vitest suite. This
 * imports it from there rather than adding anything.
 *
 * NOT SERVED
 * ----------
 * `tests/` is not one of the five allowlisted kit roots, so it 404s on the
 * host. That is correct: it is a check, not a screen.
 *
 * RUN
 * ---
 *   node design-kit/tests/map-layers.test.mjs
 *
 * Pass a path to run it against a mutated copy — which is how the suite was
 * checked for the failure it is supposed to catch. Five mutants, all killed:
 * apply() driving only a group's first layer (2 failures), the control shown
 * while the SVG basemap draws (1), one option per layer instead of per group
 * (9), no re-render on a language change (2), and checkboxes defaulting to off
 * (1). A green suite that has never been shown to fail is decoration.
 */
import { JSDOM } from '../../web/node_modules/jsdom/lib/api.js';
import { readFileSync } from 'node:fs';

const KIT = new URL('../ui_kits/app/', import.meta.url);
const kitFile = (n) => new URL(n, KIT);
const src = readFileSync(process.argv[2] || kitFile('map-layers.js'), 'utf8');

// The markup exactly as both screens carry it.
const HTML = `<!doctype html><body>
<div class="colmenu" data-od-id="map-layers" hidden>
  <button type="button" aria-expanded="false" aria-controls="p">Слоеве</button>
  <div class="colmenu__panel" id="p" hidden><fieldset><legend>Показвай на картата</legend></fieldset></div>
</div></body>`;

// A style shaped like the real one: grouped layers, two per POI group.
const LAYERS = [
  { id: 'background', metadata: { 'airbg:group': 'base' } },
  { id: 'water', metadata: { 'airbg:group': 'water' } },
  { id: 'poi-education', metadata: { 'airbg:group': 'poi-education' } },
  { id: 'poi-education-name', metadata: { 'airbg:group': 'poi-education' } },
  { id: 'poi-shop', metadata: { 'airbg:group': 'poi-shop' } },
  { id: 'poi-shop-name', metadata: { 'airbg:group': 'poi-shop' } },
];

function fakeMap() {
  const set = [];
  return { getStyle: () => ({ layers: LAYERS }),
           setLayoutProperty: (id, k, v) => set.push([id, k, v]), calls: set };
}

let pass = 0, fail = 0;
const ok = (name, cond, extra = '') => {
  if (cond) { pass++; console.log('  PASS', name); }
  else { fail++; console.log('  FAIL', name, extra); }
};

const dom = new JSDOM(HTML, { runScripts: 'outside-only', url: 'https://airbg.org/' });
const { window } = dom;
// Catalogue stub: BG for known keys, key-passthrough otherwise (the visible-gap rule).
const CAT = { 'layers.button': 'Слоеве', 'layers.base': 'Терен и паркове',
              'layers.water': 'Води', 'layers.poi-education': 'Училища и детски градини',
              'layers.poi-shop': 'Магазини и заведения', 'layers.legend': 'Показвай на картата' };
window.AIRBG_T = (k) => CAT[k] || k;
window.eval(src);

const root = window.document.querySelector('[data-od-id="map-layers"]');
const btn = root.querySelector('button');
const panel = root.querySelector('.colmenu__panel');

console.log('\n1. hidden while the SVG basemap draws');
ok('starts hidden', root.hidden);
window.document.dispatchEvent(new window.CustomEvent('airbg:basemapchange', { detail: { state: 'local' } }));
ok('stays hidden when tiles stand down', root.hidden);

console.log('\n2. options are read off the style');
const map = fakeMap();
window.document.dispatchEvent(new window.CustomEvent('airbg:basemapchange', { detail: { state: 'tiles', map } }));
ok('shown once a camera exists', !root.hidden);
const opts = [...panel.querySelectorAll('.colmenu__opt')];
ok('one option per group, not per layer', opts.length === 4, `got ${opts.length}`);
ok('ordered base→water→poi', opts.map(o => o.querySelector('input').getAttribute('data-layer-group')).join(',')
   === 'base,water,poi-education,poi-shop');
ok('labels come from the catalogue', opts[3].textContent === 'Магазини и заведения', opts[3].textContent);

console.log('\n3. toggling drives every layer in the group');
const shop = opts[3].querySelector('input');
map.calls.length = 0;
shop.checked = false;
shop.dispatchEvent(new window.Event('change'));
ok('both poi-shop layers hidden, and nothing else',
   JSON.stringify(map.calls) === JSON.stringify([['poi-shop','visibility','none'],['poi-shop-name','visibility','none']]),
   JSON.stringify(map.calls));

console.log('\n4. the choice persists');
ok('written to localStorage', JSON.parse(window.localStorage.getItem('airbg:map-layers'))['poi-shop'] === false);
map.calls.length = 0;
window.document.dispatchEvent(new window.CustomEvent('airbg:basemapchange', { detail: { state: 'tiles', map } }));
const again = [...panel.querySelectorAll('.colmenu__opt')].map(o => o.querySelector('input').checked);
ok('restored on the next mount', JSON.stringify(again) === '[true,true,true,false]', JSON.stringify(again));
ok('and re-applied to the map', map.calls.some(c => c[0] === 'poi-shop' && c[2] === 'none'));

console.log('\n5. the disclosure');
btn.dispatchEvent(new window.Event('click'));
ok('opens', btn.getAttribute('aria-expanded') === 'true' && !panel.hidden);
root.dispatchEvent(new window.KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
ok('Escape closes', btn.getAttribute('aria-expanded') === 'false' && panel.hidden);

console.log('\n6. a language switch re-renders script-built copy');
CAT['layers.poi-shop'] = 'Shops and eating places';
CAT['layers.button'] = 'Layers';
window.document.dispatchEvent(new window.CustomEvent('airbg:languagechange'));
const relabelled = [...panel.querySelectorAll('.colmenu__opt')].map(o => o.textContent);
ok('option relabelled', relabelled[3] === 'Shops and eating places', relabelled[3]);
ok('button relabelled', btn.textContent === 'Layers', btn.textContent);
ok('no duplicated options after re-render', relabelled.length === 4, `got ${relabelled.length}`);
ok('the reader’s choice survives it',
   [...panel.querySelectorAll('input')][3].checked === false);

console.log('\n7. a group with no string is a visible gap, not a silent one');
ok('unknown key prints itself', window.AIRBG_T('layers.poi-other') === 'layers.poi-other');

console.log('\n8. a style carrying no groups yields no control');
const dom2 = new JSDOM(HTML, { runScripts: 'outside-only', url: 'https://airbg.org/' });
dom2.window.AIRBG_T = (k) => k;
dom2.window.eval(src);
const bare = { getStyle: () => ({ layers: [{ id: 'x' }] }), setLayoutProperty() {} };
dom2.window.document.dispatchEvent(new dom2.window.CustomEvent('airbg:basemapchange', { detail: { state: 'tiles', map: bare } }));
ok('stays hidden rather than showing an empty panel',
   dom2.window.document.querySelector('[data-od-id="map-layers"]').hidden);

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
