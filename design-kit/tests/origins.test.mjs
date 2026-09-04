/* origins.js — where the backends live, and who is allowed to say so.
 *
 * The interesting check is #4: a `?tiles=` on the PRODUCTION origin must be
 * ignored. Ungated, that parameter is a link that repoints a reader's map at
 * someone else's server. The production CSP would refuse the connection anyway,
 * but a guard that relies on a different system to be correct is not a guard.
 *
 *   node design-kit/tests/origins.test.mjs [path-to-mutated-origins.js]
 */
import { JSDOM } from '../../web/node_modules/jsdom/lib/api.js';
import { readFileSync } from 'node:fs';

const SRC = process.argv[2]
  || new URL('../ui_kits/app/origins.js', import.meta.url);
const src = readFileSync(SRC, 'utf8');

const PROD_TILES = 'https://tiles.airbg.org';
const PROD_API = 'https://airbg.org/api/v1/';

function run(url, bodyAttrs = '') {
  const dom = new JSDOM(`<!doctype html><body ${bodyAttrs}></body>`, { url, runScripts: 'outside-only' });
  dom.window.eval(src);
  return dom.window.AIRBG_ORIGINS;
}

let fail = 0;
const ok = (name, cond, extra = '') => {
  console.log((cond ? '  PASS ' : '  FAIL ') + name + (cond ? '' : ' — ' + extra));
  if (!cond) fail++;
};

console.log('\n1. production is the default, unchanged');
let o = run('https://airbg.org/design-kit/ui_kits/app/map-home.html');
ok('tiles', o.tiles === PROD_TILES, o.tiles);
ok('api', o.api === PROD_API, o.api);
ok('reports itself as not overridden', o.overridden() === false);

console.log('\n2. authored markup wins');
o = run('https://airbg.org/x.html', 'data-tiles-base="https://tiles.example/" data-api-base="https://api.example/v1"');
ok('tiles from data-tiles-base, trailing slash trimmed', o.tiles === 'https://tiles.example', o.tiles);
ok('api from data-api-base, exactly one trailing slash', o.api === 'https://api.example/v1/', o.api);
ok('reports itself as overridden', o.overridden() === true);

console.log('\n3. the query param works on loopback');
for (const host of ['http://localhost:8099/k.html', 'http://127.0.0.1:8099/k.html']) {
  o = run(host + '?tiles=http://127.0.0.1:8092&api=http://127.0.0.1:8080/api/v1/');
  ok(`${new URL(host).hostname}: tiles`, o.tiles === 'http://127.0.0.1:8092', o.tiles);
  ok(`${new URL(host).hostname}: api`, o.api === 'http://127.0.0.1:8080/api/v1/', o.api);
}

console.log('\n4. the query param is INERT on production — the security property');
o = run('https://airbg.org/x.html?tiles=https://evil.example&api=https://evil.example/v1/');
ok('a crafted ?tiles= cannot repoint the map', o.tiles === PROD_TILES, o.tiles);
ok('a crafted ?api= cannot repoint the data', o.api === PROD_API, o.api);

console.log('\n5. only http(s) is accepted, from either channel');
o = run('http://localhost/x.html?tiles=javascript:alert(1)');
ok('javascript: from a query is refused', o.tiles === PROD_TILES, o.tiles);
o = run('https://airbg.org/x.html', 'data-tiles-base="data:text/html,x"');
ok('data: from markup is refused', o.tiles === PROD_TILES, o.tiles);

console.log('\n6. markup outranks the query, so a served page is never steerable by URL');
o = run('http://localhost/x.html?tiles=http://127.0.0.1:9999', 'data-tiles-base="http://127.0.0.1:8092"');
ok('data-tiles-base wins over ?tiles=', o.tiles === 'http://127.0.0.1:8092', o.tiles);

console.log('\n7. the two bases are different shapes, and a bare ?api= says so');
// The tiles base is an origin; the API base is an origin PLUS /api/v1, because
// requests are built as `<base>areas`. Getting that wrong 404s everything, and
// the parameter names do not hint at the difference — so it warns on loopback.
{
  const warn = [];
  const dom = new JSDOM('<!doctype html><body></body>', {
    url: 'http://localhost/x.html?api=http://127.0.0.1:8080', runScripts: 'outside-only',
  });
  dom.window.console.warn = (m) => warn.push(m);
  dom.window.eval(src);
  ok('warns when the API base carries no path', warn.some(m => /needs its path/.test(m)), warn.join('|'));

  const quiet = [];
  const good = new JSDOM('<!doctype html><body></body>', {
    url: 'http://localhost/x.html?api=http://127.0.0.1:8080/api/v1', runScripts: 'outside-only',
  });
  good.window.console.warn = (m) => quiet.push(m);
  good.window.eval(src);
  ok('stays quiet when it carries one', quiet.length === 0, quiet.join('|'));

  /* There is deliberately no "never warns on production" check. Production
   * always resolves to PROD_API, which has a path, so the branch is unreachable
   * there whatever the code does — a check would pass without testing anything,
   * which a mutant proved by deleting the guard and staying green. Check 4
   * already asserts the property that matters: a crafted ?api= cannot take
   * effect off loopback. */
}

console.log(fail ? `\n${fail} failed` : '\nall checks passed');
process.exit(fail ? 1 : 0);
