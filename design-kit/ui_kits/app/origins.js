/* Where the kit's two backends live — one owner, one rule.
 *
 * WHY THIS EXISTS
 * ---------------
 * `map-tiles.js` hardcoded `https://tiles.airbg.org/style.json` and
 * `airbg-data.js` hardcoded `https://airbg.org/api/v1/`. Both are correct in
 * production and both made the kit **untestable anywhere else**: on loopback the
 * style fetch is refused (the local CSP omits the tile host, and the tile
 * listener's ACAO names the app origin exactly), so the kit falls back to the
 * SVG basemap — every time, by design. Which is fine for the basemap and fatal
 * for anything that can only be exercised over the tiled path.
 *
 * That is how the layer control (§5.2b) came to ship verified in jsdom, verified
 * in the style, and never once seen operating a real MapLibre map: reviewing it
 * required production, and production is closed. **A constant three files away
 * decided what could be tested.**
 *
 * THE RULE, IN PRECEDENCE ORDER
 * -----------------------------
 *   1. `<body data-tiles-base>` / `data-api-base` — authored markup.
 *   2. `?tiles=` / `?api=` — ONLY on a loopback host.
 *   3. The production constants. Byte-identical to what shipped before.
 *
 * WHY THE QUERY PARAM IS GATED TO LOOPBACK
 * ----------------------------------------
 * Ungated, `?tiles=https://evil.example` would be a link that repoints a
 * reader's map at someone else's server. The production CSP would refuse the
 * connection anyway — `connect-src 'self' https://tiles.airbg.org` — but that
 * is a second system catching this file's mistake, and a guard that relies on
 * something else to be correct is not a guard. So the parameter simply does not
 * exist off loopback.
 *
 * Only http/https are accepted, so `javascript:` and `data:` cannot arrive
 * through either channel.
 */
(function () {
  var PROD_TILES = 'https://tiles.airbg.org';
  var PROD_API = 'https://airbg.org/api/v1/';

  /* file:// has hostname '', and a preview daemon serves from loopback. Both
   * are "somewhere that is not production", which is the whole test. */
  function local() {
    var h = location.hostname;
    return h === 'localhost' || h === '127.0.0.1' || h === '[::1]' || h === '::1' || h === '';
  }

  function safe(url) {
    if (!url) return null;
    try {
      var u = new URL(url, location.href);
      return (u.protocol === 'http:' || u.protocol === 'https:') ? u.href.replace(/\/+$/, '') : null;
    } catch (e) { return null; }
  }

  function resolve(attr, param, fallback) {
    var body = document.body;
    var authored = body && body.dataset ? body.dataset[attr] : null;
    var fromMarkup = safe(authored);
    if (fromMarkup) return fromMarkup;

    if (local()) {
      var q = null;
      try { q = new URLSearchParams(location.search).get(param); } catch (e) { q = null; }
      var fromQuery = safe(q);
      if (fromQuery) return fromQuery;
    }
    return fallback.replace(/\/+$/, '');
  }

  var api = resolve('apiBase', 'api', PROD_API) + '/';

  /* THE TWO BASES ARE NOT THE SAME SHAPE, AND THAT COST SOMEONE AN HOUR.
   * The tile base is an ORIGIN — the style is fetched from `<base>/style.json`.
   * The API base is an ORIGIN PLUS A PATH — requests go to `<base>meta` and
   * `<base>areas`, so it has to end in `/api/v1`. Passing a bare origin sends
   * every request to `/meta` and `/areas` and 404s them.
   *
   * That asymmetry is invisible from the parameter names, so instead of only
   * writing it down, say so at the moment it is got wrong.
   *
   * No loopback gate here, and the absence is deliberate: off loopback `api` is
   * always PROD_API — a crafted ?api= is refused upstream — and PROD_API has a
   * path, so this branch is unreachable on airbg.org by construction. A gate
   * would be dead code implying a risk that does not exist. It was written with
   * one, and a mutant that deleted the gate still passed every check: the test
   * covering it could not distinguish, because production never reaches here
   * either way. A check that passes for the wrong reason is decoration. */
  if (!/\/[^/]/.test(new URL(api).pathname)) {
    if (window.console) {
      console.warn('origins: ?api= looks like a bare origin (' + api + '). ' +
                   'The API base needs its path — e.g. http://127.0.0.1:8080/api/v1 — ' +
                   'or requests go to /meta and /areas. The tiles base takes no path.');
    }
  }

  window.AIRBG_ORIGINS = {
    tiles: resolve('tilesBase', 'tiles', PROD_TILES),
    api: api,
    /* Stated so a reader of the console can tell a redirected kit from a
     * production one without reading three files. */
    overridden: function () {
      return this.tiles !== PROD_TILES || this.api !== PROD_API;
    }
  };
})();
