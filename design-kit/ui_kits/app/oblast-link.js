/* Which province a detail page is about, carried between screens.
 *
 * The URL is the canonical carrier: `area-detail.html?oblast=<name_bg>` works
 * on the real server, is linkable, bookmarkable and shareable, and stays the
 * one thing this file reads first.
 *
 * But the kit is also opened in hosts that address files by PATH and have no
 * query layer at all — the OD preview resolves the whole string
 * `area-detail.html?oblast=Смолян` as a filename. There the link still opens
 * the page and the parameter is simply gone, so every province rendered as the
 * authored one: a control that appears to work and reports the wrong province,
 * which is the exact failure the parameter was added to fix.
 *
 * So the identity also rides in `sessionStorage`, written at the moment of the
 * click. That is a carrier, not a second source of truth: the query wins
 * whenever it exists, the stored value is read once and immediately cleared, and
 * a direct visit with neither leaves the page exactly as authored.
 */
(function () {
  var KEY = 'airbg:oblast';

  function known(name) {
    var k = window.AIRBG_OBLAST_EN;
    return !!(name && k && Object.prototype.hasOwnProperty.call(k, name));
  }

  // Is this document already the province detail screen?
  function onDetail() { return !!document.querySelector('[data-oblast]'); }

  /* Every screen: remember the province behind the link that was just clicked.
   * Delegated, so it covers the map's 28 SVG anchors, the table's 28 rows and
   * the finder's Отвори link without any of them knowing about this. */
  document.addEventListener('click', function (e) {
    var a = e.target.closest && e.target.closest('a[href*="area-detail.html"]');
    if (!a) return;
    // getAttribute, not .href: an SVG anchor's href is an SVGAnimatedString.
    var href = a.getAttribute('href') || '';
    var m = /[?&]oblast=([^&]+)/.exec(href);
    if (!m) return;
    var name;
    try { name = decodeURIComponent(m[1]); } catch (err) { return; }
    if (!known(name)) return;
    try { sessionStorage.setItem(KEY, name); } catch (err) { /* private mode */ }

    /* From a province page to ANOTHER province, the link points at the
     * document the reader is already in.
     *
     * That is what made this look broken. From the home map the target is a
     * different path, so the browser navigates and everything works. From a
     * province page the target path is `area-detail.html` — the current page —
     * and in a host that drops the query (§5.2, the OD preview addresses files
     * by path) the resolved URL is byte-identical to the one already loaded.
     * A link to the page you are on is not a navigation, so the browser did
     * nothing, and the carrier this handler had just written was never read.
     * The click looked dead while every part of it was working.
     *
     * Reloading would be wrong in the other direction: where the query DOES
     * survive, the old `?oblast=` is still in the address bar and would win
     * over the carrier, returning the reader to the province they were
     * leaving. So the page switches province IN PLACE instead — which is also
     * simply better, since nothing on the screen needs a round trip. */
    if (!onDetail()) return;                 // other screens: a real navigation
    if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;  // new tab is the reader's
    e.preventDefault();
    go(name, true);
  }, true);

  /* Switch the province this page is about, without leaving it.
   *
   * The identity lives in one place — `data-oblast` on the heading — and four
   * components read it (the map, the finder, the readouts, the label swap).
   * They do not need to know how it changed, so this writes it and fires one
   * event, exactly as the language and metric switchers do (§5.12). */
  function go(name, push) {
    var h1 = document.querySelector('[data-oblast]');
    if (!h1 || !known(name) || h1.getAttribute('data-oblast') === name) return;
    h1.setAttribute('data-oblast', name);
    h1.textContent = window.AIRBG_NAME ? window.AIRBG_NAME(name) : name;
    /* The address follows, so the page stays linkable and Back still works.
     * Query-only, same path: a host that drops the query on a request has no
     * quarrel with one written here. */
    if (push) {
      try {
        history.pushState({ oblast: name }, '',
          '?oblast=' + encodeURIComponent(name));
      } catch (err) { /* sandboxed history: the page is still correct */ }
    }
    document.dispatchEvent(new CustomEvent('airbg:oblastchange', { detail: name }));
  }
  window.AIRBG_SET_OBLAST = go;

  // Back and forward move between the provinces the reader has looked at.
  window.addEventListener('popstate', function (e) {
    var name = e.state && e.state.oblast;
    if (!name) {
      try { name = new URLSearchParams(window.location.search).get('oblast'); } catch (err) {}
    }
    if (name) go(name, false);
  });

  /* Navigating to a province in code, not by clicking a link. The finder does
   * this when a name is picked. It goes through here so the URL shape and the
   * carrier stay written down once — a second `?oblast=` built somewhere else
   * is a second thing that can drift from the reader it has to satisfy. */
  window.AIRBG_GO_OBLAST = function (name) {
    if (!known(name)) return false;
    try { sessionStorage.setItem(KEY, name); } catch (err) { /* private mode */ }
    window.location.href = 'area-detail.html?oblast=' + encodeURIComponent(name);
    return true;
  };

  /* The detail screen: resolve the identity before anything paints. */
  var h1 = document.querySelector('[data-oblast]');
  if (!h1) return;

  var name = null;
  try { name = new URLSearchParams(window.location.search).get('oblast'); } catch (e) { /* no URL API */ }

  var stored = null;
  try {
    stored = sessionStorage.getItem(KEY);
    // Read once. A stale pick must not decide a later direct visit.
    sessionStorage.removeItem(KEY);
  } catch (e) { /* private mode */ }

  if (!name) name = stored;
  if (!name) return;

  if (!known(name)) {
    console.warn('oblast-link: unknown province "' + name + '" — keeping the authored page');
    return;
  }

  h1.setAttribute('data-oblast', name);
  // The label follows the current language; the attribute above is the identity.
  h1.textContent = window.AIRBG_NAME ? window.AIRBG_NAME(name) : name;

  /* Stamp the province onto the entry the reader arrived on.
   *
   * Without this the first history entry carries no state, so after switching
   * province in place, Back had nothing to return to and the page stayed where
   * it was — a browser control that does nothing, which is the same defect as
   * a dead button. The entry is only replaced, never added: arriving is not a
   * navigation the reader made inside this page. */
  try {
    history.replaceState({ oblast: name }, '', location.search || ('?oblast=' + encodeURIComponent(name)));
  } catch (err) { /* sandboxed history: the page is still correct */ }
})();
