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

console.log('\n1. what is offered while the SVG basemap draws');
/* SUPERSEDED. This asserted the control stays hidden until a basemap reports
 * in — which encoded a real defect as a contract. map-tiles.js loads BEFORE
 * this file and its WebGL check is synchronous, so where WebGL is unavailable
 * it announces the stand-down during its own evaluation, before this file's
 * listener exists. Waiting for that event meant waiting forever, and the
 * control never appeared on the SVG fallback: the one surface where the legend
 * toggle inside it has no other route.
 *
 * The rule now: build from derived state at load, and let the event UPDATE it.
 * A listener registered after the announcement hears nothing. */
ok('offered at load, without waiting for a basemap report',
   !root.hidden && !!panel.querySelector('.colmenu__opt'));
window.document.dispatchEvent(new window.CustomEvent('airbg:basemapchange', { detail: { state: 'local' } }));
/* SUPERSEDED with the rule above it: a stood-down basemap has no layer
 * categories to switch, but the legend toggle still means something — and this
 * is the surface with no other way to reach it. What must NOT appear is a
 * basemap toggle, because there is no map to apply it to. */
const p1 = window.document.querySelector('.colmenu__panel');
ok('offered after a stand-down, carrying the view toggles',
   root.hidden === false && p1.querySelectorAll('[data-view-toggle]').length >= 1);
ok('no layer categories, because there is no style to read them from',
   p1.querySelectorAll('[data-layer-group]').length === 0);
ok('and NO basemap toggle, because there is no map to apply it to',
   !p1.querySelector('[data-view-toggle="basemap"]'));

console.log('\n2. options are read off the style');
const map = fakeMap();
window.document.dispatchEvent(new window.CustomEvent('airbg:basemapchange', { detail: { state: 'tiles', map } }));
ok('shown once a camera exists', !root.hidden);
/* The panel carries TWO kinds of option now: layer categories, which come from
 * the style, and view toggles (legend, basemap), which are about what the
 * screen shows. Everything below is about the LAYER half, so it selects on
 * [data-layer-group] rather than on every checkbox in the panel.
 *
 * That distinction is exactly why nine of these failed when the view toggles
 * landed: a generic `.colmenu__opt` selector counted 6 where 4 groups exist and
 * shifted every index by two. The assertions were right; the selector had
 * stopped meaning what it was written to mean. */
const layerOpts = (p) =>
  [...p.querySelectorAll('.colmenu__opt')].filter(o => o.querySelector('[data-layer-group]'));

const opts = layerOpts(panel);
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
const again = layerOpts(panel).map(o => o.querySelector('input').checked);
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
const relabelled = layerOpts(panel).map(o => o.textContent);
ok('option relabelled', relabelled[3] === 'Shops and eating places', relabelled[3]);
ok('button relabelled', btn.textContent === 'Layers', btn.textContent);
ok('no duplicated options after re-render', relabelled.length === 4, `got ${relabelled.length}`);
ok('the reader’s choice survives it',
   [...panel.querySelectorAll('[data-layer-group]')][3].checked === false);

console.log('\n7. a group with no string is a visible gap, not a silent one');
ok('unknown key prints itself', window.AIRBG_T('layers.poi-other') === 'layers.poi-other');

console.log('\n8. a style carrying no groups yields no control');
const dom2 = new JSDOM(HTML, { runScripts: 'outside-only', url: 'https://airbg.org/' });
dom2.window.AIRBG_T = (k) => k;
dom2.window.eval(src);
const bare = { getStyle: () => ({ layers: [{ id: 'x' }] }), setLayoutProperty() {} };
dom2.window.document.dispatchEvent(new dom2.window.CustomEvent('airbg:basemapchange', { detail: { state: 'tiles', map: bare } }));
/* SUPERSEDED, deliberately. This asserted the whole disclosure hides when the
 * style carries no groups, which was right while the panel held only layer
 * categories. It now also holds the view toggles, and the legend switch is the
 * one a reader with no basemap most wants — hiding it there makes it
 * unreachable on exactly the surface with no other route to it. The rule is now
 * "offered when it has anything to offer". */
const p2 = dom2.window.document.querySelector('.colmenu__panel');
ok('offered with no layer groups, because the view toggles remain',
   dom2.window.document.querySelector('[data-od-id="map-layers"]').hidden === false &&
   p2.querySelectorAll('[data-view-toggle]').length >= 1 &&
   p2.querySelectorAll('[data-layer-group]').length === 0);
ok('the legend toggle is among them',
   !!p2.querySelector('[data-view-toggle="legend"]'));


console.log('\n8. a view toggle drives the map, not just its own checkbox');
/* This section exists because a mutant survived without it: deleting the change
 * handler's apply() left every other check passing. A control whose checkbox
 * moves while the map does not is the dead control this project has shipped
 * more often than any other defect — and it was untested here. */
{
  const m = fakeMap();
  window.document.dispatchEvent(new window.CustomEvent('airbg:basemapchange',
    { detail: { state: 'tiles', map: m } }));
  const panelV = window.document.querySelector('.colmenu__panel');
  const bm = panelV.querySelector('[data-view-toggle="basemap"]');
  ok('the basemap toggle is offered once a map exists', !!bm);
  /* fakeMap records setLayoutProperty rather than implementing a getter, so the
   * assertion is on what the control ASKED the map to do — which is the right
   * question anyway: the defect being guarded against is a toggle that asks for
   * nothing. */
  const nLayers = m.getStyle().layers.length;
  m.calls.length = 0;
  bm.checked = false;
  bm.dispatchEvent(new window.Event('change', { bubbles: true }));
  const off = m.calls.filter(c => c[1] === 'visibility' && c[2] === 'none');
  ok('unticking it hides every tile layer', off.length === nLayers, `got ${off.length}/${nLayers}`);

  m.calls.length = 0;
  bm.checked = true;
  bm.dispatchEvent(new window.Event('change', { bubbles: true }));
  const on = m.calls.filter(c => c[1] === 'visibility' && c[2] === 'visible');
  ok('re-ticking restores them', on.length === nLayers, `got ${on.length}/${nLayers}`);
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
