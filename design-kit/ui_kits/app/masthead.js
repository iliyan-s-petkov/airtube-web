/* masthead.js — the language picker (§5.12).
 *
 * External file: the app's CSP has no inline-script allowance (§1), and this
 * runs on every screen, so it must not be duplicated per page.
 *
 * The picker replaces a pair of BG/EN links. Two links made the reader compare
 * them to find the active one; a picker states the current language on its face
 * and lists the alternatives only on demand.
 */
(function () {
  'use strict';

  var btn = document.querySelector('[data-od-id="lang-btn"]');
  var list = document.querySelector('[data-od-id="lang-list"]');
  if (!btn || !list) return;   // screen without a masthead picker

  function open() { list.hidden = false; btn.setAttribute('aria-expanded', 'true'); }
  function close(refocus) {
    list.hidden = true;
    btn.setAttribute('aria-expanded', 'false');
    if (refocus) btn.focus();
  }

  btn.addEventListener('click', function () {
    if (list.hidden) open(); else close(false);
  });

  // Escape closes and returns focus to the button; a click elsewhere just closes.
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && !list.hidden) close(true);
  });
  document.addEventListener('mousedown', function (e) {
    if (list.hidden) return;
    if (!list.contains(e.target) && !btn.contains(e.target)) close(false);
  });

  /* Selecting a language. The options had no handler at all — they were bare
   * `<a href="#">`, so a click jumped to the fragment, the list stayed open and
   * nothing moved. A picker that cannot be picked from is the dead-control
   * defect this system keeps catching (DESIGN.md §10).
   *
   * In the app each option is a real link to the translated route, and the
   * server renders that language. In this kit the screens are Bulgarian-only,
   * so the control does the part it legitimately owns: it records the choice,
   * moves aria-current, updates its own face, and sets the document language.
   */
  var STORE = 'airbg.lang';

  /* Swap every tagged string. A `data-i18n` element takes the catalogue value
   * outright; `data-i18n-suffix` means the element also carries trailing text
   * that is NOT translatable — the legend chips keep their µg/m³ range, which
   * is data. Oblast names come from `data-oblast` and the API's own name_en,
   * never a hand transliteration. */
  /* Write the label into the element's own TEXT NODE, wherever it sits.
   *
   * This used to write to el.lastChild, which was not "the label" — it was a
   * guess about node order. A legend chip is <swatch><text>, so the last child
   * really is the text and it worked. A column header is the reverse,
   * <text><caret>: the English went INTO the caret span while the Bulgarian
   * text node stayed put, so the header showed both languages at once and the
   * sort caret — a CSS border triangle — had copy stuffed inside it.
   *
   * Position is not identity. Find the text node by what it is.
   */
  function setLabel(el, value) {
    for (var i = 0; i < el.childNodes.length; i++) {
      var n = el.childNodes[i];
      if (n.nodeType === 3 && n.nodeValue.trim() !== '') { n.nodeValue = value; return; }
    }
    // No text node yet (icon-only element): add one ahead of the children
    // rather than overwrite one of them.
    el.insertBefore(document.createTextNode(value), el.firstChild);
  }

  function applyLanguage(code) {
    var dict = (window.AIRBG_I18N || {})[code];
    if (!dict) return;

    Array.prototype.forEach.call(document.querySelectorAll('[data-i18n]'), function (el) {
      var v = dict[el.getAttribute('data-i18n')];
      if (v === undefined) return;   // a missing key stays visible, not blank
      // The suffix is the µg/m³ range on a legend chip: data, not copy, so the
      // label is replaced and the range is carried across untouched.
      if (el.hasAttribute('data-i18n-suffix')) {
        // The separator's leading space lives in the label's text node, so
        // rebuilding the label without it printed "Добро· 0–5".
        setLabel(el, v + ' ' + el.textContent.replace(/^[^·]*/, '').trim());
      } else {
        setLabel(el, v);
      }
    });

    var names = code === 'en' ? (window.AIRBG_OBLAST_EN || {}) : null;
    Array.prototype.forEach.call(document.querySelectorAll('[data-oblast]'), function (el) {
      var bg = el.getAttribute('data-oblast');
      el.textContent = names && names[bg] ? names[bg] : bg;
    });

    var ph = document.getElementById('oblast-search');
    if (ph && dict['table.searchHint']) ph.setAttribute('placeholder', dict['table.searchHint']);
    ['first','prev','next','last'].forEach(function (k) {
      var b = document.querySelector('[data-page="' + k + '"]');
      if (b && dict['pager.' + k]) b.setAttribute('aria-label', dict['pager.' + k]);
    });
  }

  function select(opt, persist) {
    var code = opt.getAttribute('lang');
    Array.prototype.forEach.call(list.querySelectorAll('.langpick__opt'), function (o) {
      if (o === opt) o.setAttribute('aria-current', 'true');
      else o.removeAttribute('aria-current');
    });

    // The button face must state the language that is now current, or the
    // picker reports one thing and the list another.
    var mark = opt.querySelector('.langpick__mark');
    var faceMark = btn.querySelector('.langpick__mark');
    if (mark && faceMark) faceMark.innerHTML = mark.innerHTML;
    if (mark && faceMark) faceMark.className = mark.className;
    var faceCode = btn.querySelector('[data-lang-code]');
    if (faceCode) faceCode.textContent = code.toUpperCase();
    var langWord = window.AIRBG_T ? window.AIRBG_T('runtime.langLabel') : 'Език';

    document.documentElement.setAttribute('lang', code);
    applyLanguage(code);
    btn.setAttribute('aria-label', langWord + ': ' + opt.textContent.trim());

    // The seam between components. Anything that BUILDS strings at render time
    // — the table's pager, count line and combobox — cannot be reached by
    // walking [data-i18n], because those nodes do not exist yet. It listens
    // for this instead and re-renders itself.
    document.dispatchEvent(new CustomEvent('airbg:languagechange', {
      detail: { lang: code }
    }));
    if (persist) { try { localStorage.setItem(STORE, code); } catch (e) { /* private mode */ } }
    close(true);
  }

  Array.prototype.forEach.call(list.querySelectorAll('.langpick__opt'), function (opt) {
    opt.addEventListener('click', function (e) {
      e.preventDefault();   // '#' in the kit; a real route in the app
      select(opt, true);
    });
  });

  // Restore the stored choice so the control does not forget between screens.
  try {
    var saved = localStorage.getItem(STORE);
    if (saved) {
      var match = list.querySelector('.langpick__opt[lang="' + saved + '"]');
      if (match) select(match, false);
    }
  } catch (e) { /* private mode */ }

  // Arrow keys walk the options; the list is short, so this is the whole model.
  list.addEventListener('keydown', function (e) {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    e.preventDefault();
    var opts = Array.prototype.slice.call(list.querySelectorAll('.langpick__opt'));
    var i = opts.indexOf(document.activeElement);
    var next = e.key === 'ArrowDown' ? i + 1 : i - 1;
    if (next >= opts.length) next = 0;
    if (next < 0) next = opts.length - 1;
    if (opts[next]) opts[next].focus();
  });
})();
