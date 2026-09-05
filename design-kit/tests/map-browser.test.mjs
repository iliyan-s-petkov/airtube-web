/* map-browser.test.mjs — the checks that only a browser can make.
 *
 * Every defect this file pins was found by hand tonight and would have shipped
 * again: they are all invisible to a DOM-free unit test and several are
 * invisible to a screenshot too.
 *
 * It serves the kit itself, with NO Content-Security-Policy and NO tile proxy,
 * because that is the shape of a design-preview host — and running against a
 * harness that differs from the real surface is how the lat_ref defect reached
 * production behind a green local check.
 *
 * WebGL is disabled on purpose. That is the SVG-fallback path, which is what
 * the preview and any locked-down renderer get, and it is the path where the
 * hex layer, the legend toggle and the landmass fill were each broken at some
 * point tonight while the tiled path looked fine.
 *
 *   node design-kit/tests/map-browser.test.mjs
 */
import http from 'node:http';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';

const here = path.dirname(fileURLToPath(import.meta.url));
const KIT = path.join(here, '..');
const require = createRequire(path.join(KIT, '../web/node_modules/noop.js'));

let chromium;
try { ({ chromium } = require('playwright')); }
catch {
  console.log('playwright not installed — skipping browser checks');
  process.exit(0);
}

const TYPES = {
  '.html': 'text/html', '.js': 'text/javascript', '.mjs': 'text/javascript',
  '.css': 'text/css', '.json': 'application/json', '.png': 'image/png',
  '.svg': 'image/svg+xml', '.woff2': 'font/woff2'
};
const server = http.createServer(async (req, res) => {
  const rel = decodeURIComponent(req.url.split('?')[0]);
  try {
    const buf = await readFile(path.join(KIT, rel));
    res.writeHead(200, { 'content-type': TYPES[path.extname(rel)] || 'application/octet-stream' });
    res.end(buf);
  } catch { res.writeHead(404); res.end('not found'); }
});
await new Promise(r => server.listen(0, '127.0.0.1', r));
const BASE = 'http://127.0.0.1:' + server.address().port;

let pass = 0, fail = 0;
function ok(name, cond, detail) {
  if (cond) { pass++; console.log(`  PASS ${name}`); }
  else { fail++; console.log(`  FAIL ${name}${detail !== undefined ? ' — ' + detail : ''}`); }
}

const browser = await chromium.launch({ headless: true, args: ['--disable-gpu'] });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
/* Genuinely no WebGL, not merely no GPU: the SVG fallback is the surface. */
await ctx.addInitScript(() => {
  const real = HTMLCanvasElement.prototype.getContext;
  HTMLCanvasElement.prototype.getContext = function (t) {
    if (/webgl/.test(t)) return null;
    return real.apply(this, arguments);
  };
});

async function open(file, query = '') {
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message.slice(0, 90)));
  page.on('console', m => { if (m.type() === 'error') errors.push(m.text().slice(0, 90)); });
  await page.goto(`${BASE}/ui_kits/app/${file}?${query}&cb=${Date.now()}`,
                  { waitUntil: 'networkidle', timeout: 60000 });
  await page.waitForTimeout(5000);
  return { page, errors };
}

for (const [label, file, query, stateId] of [
  ['home map', 'map-home.html', '', 'map'],
  ['province map', 'area-detail.html', 'oblast=' + encodeURIComponent('София-град'), 'area-map']
]) {
  console.log(`\n== ${label} ==`);
  const { page, errors } = await open(file, query);

  const m = await page.evaluate(() => {
    const q = s => document.querySelector(s);
    const all = s => [...document.querySelectorAll(s)];
    const scale = q('[data-od-id="map-legend"]');
    const shell = q('.map-shell');
    const layers = q('[data-od-id="map-layers"]');
    const landmass = q('.map-landmass');
    const sea = q('.map-context__sea');
    const fill = e => e ? getComputedStyle(e).fill : null;
    const label0 = q('.map-area__value');
    const ls = label0 ? getComputedStyle(label0) : null;
    return {
      hexes: all('.map-hex').length,
      hexPoints: [...new Set(all('.map-hex').map(h => (h.getAttribute('d').match(/[ML]/g) || []).length))],
      hexCounts: all('.map-hex__n').length,
      landFill: fill(landmass),
      seaFill: fill(sea),
      scaleOnMap: !!scale && scale.classList.contains('scale--onmap'),
      scaleProgressive: !!scale && scale.classList.contains('scale--progressive'),
      ticks: scale ? all('.scale__tick').map(t => t.textContent.trim()) : [],
      layersOnMap: !!layers && !!shell && shell.contains(layers),
      layersOffered: !!layers && !layers.hidden,
      layerOpts: layers ? all('.colmenu__opt').length : 0,
      markersWithBandFill: all('.map-point__dot').filter(d => d.getAttribute('fill')).length,
      markerValues: all('.map-point__value').length,
      haloRatio: ls ? +(parseFloat(ls.strokeWidth) / parseFloat(ls.fontSize)).toFixed(3) : null,
      basemapReason: q('[data-basemap]') && q('[data-basemap]').getAttribute('data-basemap-reason'),
      hexSource: q('[data-hexes]') && q('[data-hexes]').getAttribute('data-hex-source')
    };
  });

  ok('no console or page errors', errors.length === 0, errors[0]);
  ok('hexes are drawn without a tiled basemap', m.hexes > 0, `${m.hexes}`);
  ok('every hex is a hexagon', m.hexPoints.length === 1 && m.hexPoints[0] === 6,
     JSON.stringify(m.hexPoints));
  ok('every hex states its sensor count', m.hexCounts === m.hexes,
     `${m.hexCounts}/${m.hexes}`);
  ok('land and sea are different colours', !!m.landFill && m.landFill !== m.seaFill,
     `${m.landFill} vs ${m.seaFill}`);
  ok('the scale is on the map', m.scaleOnMap);
  ok('the scale is the progressive ramp', m.scaleProgressive);
  ok('the scale ticks come from the ramp', m.ticks.length === 6 && /500/.test(m.ticks.join()),
     JSON.stringify(m.ticks));
  ok('the layer switcher is on the map', m.layersOnMap);
  /* Regression: map-tiles.js announces its stand-down before map-layers.js
   * exists, so a control that only builds on that event never appears. */
  ok('the layer switcher is offered without a camera', m.layersOffered && m.layerOpts > 0,
     `offered=${m.layersOffered} opts=${m.layerOpts}`);
  ok('markers carry no band fill where hexes draw', m.markersWithBandFill === 0,
     `${m.markersWithBandFill}`);
  ok('markers carry no reading where hexes draw', m.markerValues === 0, `${m.markerValues}`);
  /* A stroke straddles the outline: above ~0.15em it deforms the glyph. */
  ok('label halo does not deform the type', m.haloRatio !== null && m.haloRatio <= 0.16,
     `${m.haloRatio}em`);
  ok('a stood-down basemap says why', !!m.basemapReason, m.basemapReason);
  ok('the hex source is named', !!m.hexSource, m.hexSource);

  /* Hover must not move the map or repaint over the hexes. Both were real:
   * a transform:scale lift, and a DOM reorder that put the province above
   * the hex layer — opaque for a province with no reading. */
  const hover = await page.evaluate(async () => {
    const wait = ms => new Promise(r => setTimeout(r, ms));
    const a = [...document.querySelectorAll('a.map-area--link')]
      .find(x => x.getBoundingClientRect().width > 60);
    const svg = document.querySelector('.map-hex').ownerSVGElement;
    const hexIdx = () => [...svg.children].findIndex(c => /map-hexes/.test(c.getAttribute('class') || ''));
    const before = { hexes: document.querySelectorAll('.map-hex').length, idx: hexIdx() };
    a.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }));
    await wait(300);
    const cs = getComputedStyle(a);
    return {
      transform: cs.transform, filter: cs.filter,
      hexesBefore: before.hexes,
      hexesAfter: document.querySelectorAll('.map-hex').length,
      provinceAboveHexes: [...svg.children].indexOf(a) > hexIdx()
    };
  });
  ok('hover does not move the province', hover.transform === 'none' && hover.filter === 'none',
     `${hover.transform} / ${hover.filter}`);
  ok('hover does not remove hexes', hover.hexesAfter === hover.hexesBefore,
     `${hover.hexesBefore} → ${hover.hexesAfter}`);
  ok('a hovered province stays below the hexes', hover.provinceAboveHexes === false);

  /* Every province must be reachable: an outline-only shape has no painted
   * fill, so without pointer-events:all it is clickable on its border only. */
  /* Sample the centre of the part of each province that is ON SCREEN, not the
   * centre of its full box. The province map is pre-zoomed, so a province can
   * be plainly visible while its box centre sits outside the viewport, where
   * elementFromPoint returns null — the viewport answering honestly, not the
   * link being unreachable. Taking the box centre left this check with nothing
   * to sample at all on that map, which is a vacuous pass away from useless. */
  const hits = await page.evaluate(() => {
    /* Clip to the map's own visible rect, not the window's: the map fills part
     * of the page, so a point can be inside the window and still land on the
     * masthead or the title, where the answer says nothing about the link. */
    const svg = document.querySelector('.map-hex').ownerSVGElement;
    const v = svg.getBoundingClientRect();
    const vx0 = Math.max(v.left, 0), vx1 = Math.min(v.right, window.innerWidth);
    const vy0 = Math.max(v.top, 0), vy1 = Math.min(v.bottom, window.innerHeight);
    return [...document.querySelectorAll('a.map-area--link')]
      .map(a => {
        const b = a.getBoundingClientRect();
        const x0 = Math.max(b.left, vx0), x1 = Math.min(b.right, vx1);
        const y0 = Math.max(b.top, vy0), y1 = Math.min(b.bottom, vy1);
        /* 4px, not 0: a province clipped to a sub-pixel sliver is not on screen
         * in any sense a pointer can act on, and probing it asks the renderer a
         * question it has no honest answer to. */
        return { a, x0, x1, y0, y1, on: x1 - x0 >= 4 && y1 - y0 >= 4 };
      })
      .filter(p => p.on)
      .slice(0, 8)
      .map(p => {
        /* A grid, not the box centre. A province is a shape, not a rectangle:
         * Софийска is a ring whose box centre is София-град, so a centre-only
         * probe reports it unreachable while every point of it is clickable.
         * Reachable means SOME point of it answers with its own link. */
        const N = 6;
        for (let i = 1; i < N; i++) {
          for (let j = 1; j < N; j++) {
            const x = p.x0 + (p.x1 - p.x0) * i / N;
            const y = p.y0 + (p.y1 - p.y0) * j / N;
            const top = document.elementFromPoint(x, y);
            if (top && top.closest('a.map-area--link') === p.a) return true;
          }
        }
        return false;
      });
  });
  /* On the province map the camera is inside ONE province, and that province is
   * deliberately not a link to itself — so there may be no linked province
   * centre on screen at all. Zero candidates is not zero passes: it means this
   * check has nothing to say here, and reporting it as a failure would be the
   * harness lying about the page. */
  if (hits.length === 0) console.log('  SKIP province centres hit their own link — none on screen');
  else ok('province centres hit their own link', hits.every(Boolean),
          `${hits.filter(Boolean).length}/${hits.length}`);

  /* Auto-refresh must not throw the reader back to the country view. */
  const zoom = await page.evaluate(async (id) => {
    const wait = ms => new Promise(r => setTimeout(r, ms));
    const inBtn = document.querySelector('[data-od-id="map-zoom"] [data-act="in"]');
    for (let i = 0; i < 3 && inBtn; i++) { inBtn.click(); await wait(250); }
    const st = () => window.AIRBG_MAP_STATE(id);
    const before = { k: +st().k.toFixed(3), dx: Math.round(st().dx) };
    document.dispatchEvent(new CustomEvent('airbg:datachange'));
    await wait(1200);
    return { before, after: { k: +st().k.toFixed(3), dx: Math.round(st().dx) } };
  }, stateId);
  ok('a data refresh keeps the reader where they were',
     zoom.before.k === zoom.after.k && zoom.before.dx === zoom.after.dx,
     `${JSON.stringify(zoom.before)} → ${JSON.stringify(zoom.after)}`);

  await page.close();
}

await browser.close();
server.close();
console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
