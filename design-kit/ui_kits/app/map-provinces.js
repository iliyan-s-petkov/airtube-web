/* Find a province, and point the map at it.
 *
 * This replaces the "Виж всички области" button. That button was a second door
 * to a place the masthead already links to (§5.1), and it answered the wrong
 * question: a reader on the map screen wants THIS province, not a list of all
 * of them. The control that belongs beside a map is one that moves the map.
 *
 * The list is not written down here. The 28 provinces already exist as the keys
 * of AIRBG_OBLAST_EN — the identity/label pairing from §5.12 — so this reads
 * that. A second copy of the province list is a second thing that can disagree
 * with the table (§9.3).
 *
 * Picking a name GOES to that province's page. The earlier build set
 * data-focus-oblast on the frame and printed a status line — *"Картата е
 * центрирана върху област Варна. Отвори страницата на областта. Цялата
 * страна."* — three sentences and two controls standing between the reader and
 * the thing they had just named. A reader who types a province is asking to
 * see that province, not to be told what the map did and offered a link to
 * where they were already going. The finder is now a way in, and the search
 * field is the whole control.
 *
 * data-focus-oblast is still set before the navigation, so whoever mounts
 * Leaflet on a page that stays put keeps the same seam it always read.
 */
(function () {
  var root = document.querySelector('[data-od-id="province-find"]');
  if (!root) return;
  if (!window.AIRBG_T) console.error('map-provinces: i18n.js must load first.');

  var input   = root.querySelector('#province-search');
  var listbox = root.querySelector('#province-listbox');
  var frame   = document.querySelector('[data-od-id="map"]');

  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }
  /* The Bulgarian name is the identity; the label is resolved per language, and
   * the reader searches the label they can actually see (§5.12). */
  function label(bg) { return window.AIRBG_NAME ? window.AIRBG_NAME(bg) : bg; }

  var NAMES = Object.keys(window.AIRBG_OBLAST_EN || {});
  var state = { q: '', picked: null };

  function visible() {
    var out = NAMES.filter(function (bg) {
      return !state.q || label(bg).toLocaleLowerCase(lang()).indexOf(state.q) !== -1;
    });
    // Cyrillic and Latin do not fall in the same order, so the collator follows
    // the displayed language rather than code points.
    return out.sort(function (a, b) {
      return new Intl.Collator(lang()).compare(label(a), label(b));
    });
  }

  function buildList() {
    listbox.textContent = '';
    var rows = visible();
    if (!rows.length) {
      var none = document.createElement('li');
      none.className = 'combobox__empty';
      none.textContent = t('province.noMatch');       // absence, stated plainly (§2.3)
      listbox.appendChild(none);
      return;
    }
    rows.forEach(function (bg, i) {
      var li = document.createElement('li');
      li.className = 'combobox__opt';
      li.id = 'province-opt-' + i;
      li.setAttribute('role', 'option');
      li.setAttribute('aria-selected', bg === state.picked ? 'true' : 'false');
      li.textContent = label(bg);
      // mousedown, not click: click fires after blur has closed the list (§5.10).
      li.addEventListener('mousedown', function (e) { e.preventDefault(); choose(bg); });
      listbox.appendChild(li);
    });
  }

  function choose(bg) {
    state.picked = bg;
    state.q = '';
    input.value = label(bg);
    close();
    // The seam for whoever mounts the real map, set before we leave.
    if (frame) frame.setAttribute('data-focus-oblast', bg);
    // One owner for the URL and the carrier (oblast-link.js). If the province
    // is somehow unknown the field keeps the text and nothing navigates —
    // better than a page about nothing.
    if (window.AIRBG_GO_OBLAST) window.AIRBG_GO_OBLAST(bg);
  }

  function reset() {
    state.picked = null;
    state.q = '';
    input.value = '';
    if (frame) frame.removeAttribute('data-focus-oblast');
    input.focus();
  }

  function paintChrome() {
    input.setAttribute('placeholder', t('province.hint'));
  }

  function open()  { buildList(); listbox.hidden = false; input.setAttribute('aria-expanded', 'true'); }
  function close() { listbox.hidden = true; input.setAttribute('aria-expanded', 'false'); }

  input.addEventListener('input', function () {
    state.q = input.value.trim().toLocaleLowerCase(lang());
    open();
  });
  input.addEventListener('focus', open);
  input.addEventListener('blur', function () { setTimeout(close, 0); });
  input.addEventListener('keydown', function (e) {
    // Enter goes, because the field's whole job is now to take the reader to a
    // province. It only fires on an unambiguous match — one visible option, or
    // a name typed in full — so a half-typed query never navigates.
    if (e.key === 'Enter') {
      var rows = visible();
      var exact = rows.filter(function (bg) {
        return label(bg).toLocaleLowerCase(lang()) === state.q;
      });
      var pick = exact.length === 1 ? exact[0] : (rows.length === 1 ? rows[0] : null);
      if (pick) { e.preventDefault(); choose(pick); }
      return;
    }
    // Escape is two-stage: close the list, then clear the query (§5.10).
    if (e.key !== 'Escape') return;
    if (!listbox.hidden) { close(); return; }
    if (input.value) { reset(); }
  });

  document.addEventListener('airbg:languagechange', function () {
    paintChrome();
    if (state.picked) { input.value = label(state.picked); state.q = ''; }  // pick survives; label moves
    if (!listbox.hidden) buildList();
  });

  paintChrome();
})();
