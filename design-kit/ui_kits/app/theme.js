/* Light / dark, with "follow the system" as a real option.
 *
 * Three states, not two. A bare toggle can only say light-or-dark, so the
 * moment the reader touches it they lose the ability to follow the OS — and a
 * public site read at night on a phone that switches at sunset should not have
 * to be corrected twice a day. Auto is therefore the default and stays
 * reachable, exactly like Всички stays reachable in the table (§5.4).
 *
 * The choice writes data-theme on <html>; tokens.css does the rest. Nothing here
 * knows a colour — a theme script that names hex values is a second palette.
 */
(function () {
  var btn  = document.querySelector('[data-od-id="theme-btn"]');
  var list = document.querySelector('[data-od-id="theme-list"]');
  if (!btn || !list) return;
  var face = btn.querySelector('[data-theme-face]');
  var STORE = 'airbg.theme';
  var opts = Array.prototype.slice.call(list.querySelectorAll('[data-theme-opt]'));

  function t(k) { return window.AIRBG_T ? window.AIRBG_T(k) : k; }
  function label(v) { return t('theme.' + v); }

  function apply(value, persist) {
    if (value === 'auto') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', value);

    var chosen = null;
    opts.forEach(function (o) {
      var v = o.getAttribute('data-theme-opt');
      if (v === value) { o.setAttribute('aria-current', 'true'); chosen = o; }
      else o.removeAttribute('aria-current');
      /* The options carry icons, so the NAME lives on aria-label and title.
       * Both are set from the catalogue on every apply, which is what keeps
       * them translated — an icon-only control whose only name is a hardcoded
       * attribute is untranslated by construction. */
      o.setAttribute('aria-label', label(v));
      o.setAttribute('title', label(v));
    });

    /* The face shows the icon of the current state, not a word. It is a copy of
     * the chosen option's mark so the two can never drift apart. */
    if (face && chosen) {
      var mark = chosen.querySelector('.langpick__mark');
      if (mark) face.innerHTML = mark.innerHTML;
    }
    /* The button still has a name in words: an icon-only control with no
     * accessible name is a button that only sighted readers can use. */
    btn.setAttribute('aria-label', t('theme.label') + ': ' + label(value));
    btn.setAttribute('title', t('theme.label') + ': ' + label(value));

    if (persist) { try { localStorage.setItem(STORE, value); } catch (e) { /* private mode */ } }
  }

  function current() {
    var el = list.querySelector('[aria-current]');
    return el ? el.getAttribute('data-theme-opt') : 'auto';
  }
  function open()  { list.hidden = false; btn.setAttribute('aria-expanded', 'true'); }
  function close(focus) {
    list.hidden = true; btn.setAttribute('aria-expanded', 'false');
    if (focus) btn.focus();
  }

  btn.addEventListener('click', function () { list.hidden ? open() : close(false); });
  opts.forEach(function (o) {
    o.addEventListener('click', function (e) {
      e.preventDefault();
      apply(o.getAttribute('data-theme-opt'), true);
      close(true);
    });
  });
  btn.addEventListener('keydown', function (e) { if (e.key === 'Escape' && !list.hidden) close(true); });
  list.addEventListener('keydown', function (e) { if (e.key === 'Escape') { e.stopPropagation(); close(true); } });
  document.addEventListener('mousedown', function (e) {
    if (list.hidden) return;
    if (!list.contains(e.target) && e.target !== btn && !btn.contains(e.target)) close(false);
  });
  // Re-label when the language changes: the face and the options are copy.
  document.addEventListener('airbg:languagechange', function () { apply(current(), false); });

  var saved = null;
  try { saved = localStorage.getItem(STORE); } catch (e) { /* private mode */ }
  apply(saved === 'light' || saved === 'dark' ? saved : 'auto', false);
})();
