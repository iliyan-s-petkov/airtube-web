/* Drag to move the map; wheel and pinch to scale it.
 *
 * The buttons (map-zoom.js) are the discoverable way in and stay the way in
 * for keyboard and screen-reader readers. This file adds the direct one: a
 * reader who sees a map expects to be able to grab it.
 *
 * It owns no arithmetic. Every gesture ends in AIRBG_MAP_PAN or
 * AIRBG_MAP_ZOOM_AT in map-render.js, which holds the one copy of the view
 * state — the same rule the buttons follow (§5.2b).
 *
 * Two rules that shaped the rest of it:
 *
 * 1. A drag must not follow a link. Every province is a real <a> and every
 *    marker is clickable, so a 60px drag that happens to start on Пловдив
 *    would otherwise navigate on release. Movement past a small threshold arms
 *    a one-shot capture-phase click suppressor.
 *
 * 2. The page must keep scrolling. A full-bleed map that eats the wheel traps
 *    the reader on the home screen, so a plain wheel scrolls the page as it
 *    always did and Ctrl/⌘ + wheel zooms — the convention embedded maps
 *    settled on. Because that is undiscoverable, a plain wheel over the map
 *    says so once, briefly, instead of silently doing nothing.
 */
(function () {
  var frames = document.querySelectorAll('[data-od-id="map"], [data-od-id="area-map"]');
  if (!frames.length) return;
  if (!window.AIRBG_T) console.error('map-pan: i18n.js must load first.');
  if (!window.AIRBG_MAP_PAN) { console.error('map-pan: map-render.js must load first.'); return; }

  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }

  var MOVED = 4;          // px before a press counts as a drag, not a click
  var WHEEL_STEP = 1.0015; // per wheel unit; deltas vary wildly between devices

  frames.forEach(function (frame) {
    var id = frame.getAttribute('data-od-id');
    var canvas = frame.querySelector('.map-canvas');
    if (!canvas) return;

    var pointers = {};       // live pointers, for pinch
    var last = null;         // last single-pointer position
    var moved = 0;
    var pinch = 0;           // distance between two pointers at the last event
    var captured = null;     // pointerId we hold capture for, once a drag begins

    frame.classList.add('map--grabbable');

    /* Suppress only the click that ends a drag, and only inside this map.
     *
     * The first version armed a one-shot capture listener on `document`, which
     * swallowed the next click ANYWHERE on the page — the zoom buttons, the
     * masthead, a table row. It presented as "whole country does not reset the
     * pan", which sent me looking at the reset. A guard has to be as narrow as
     * the thing it guards, or its blast radius becomes someone else's bug.
     *
     * It is also self-clearing: if no click follows the release — a drag that
     * ends outside the map, a cancelled pointer — the guard must not sit there
     * waiting to eat an unrelated click later. */
    var guard = null;
    function armClickGuard() {
      disarm();
      guard = function (e) { e.stopPropagation(); e.preventDefault(); disarm(); };
      canvas.addEventListener('click', guard, true);
      setTimeout(disarm, 300);
    }
    function disarm() {
      if (!guard) return;
      canvas.removeEventListener('click', guard, true);
      guard = null;
    }

    function pos(e) { return { x: e.clientX, y: e.clientY }; }
    function spread() {
      var k = Object.keys(pointers);
      if (k.length < 2) return 0;
      var a = pointers[k[0]], b = pointers[k[1]];
      return Math.hypot(a.x - b.x, a.y - b.y);
    }
    function mid() {
      var k = Object.keys(pointers), a = pointers[k[0]], b = pointers[k[1]];
      var r = canvas.getBoundingClientRect();
      return { x: (a.x + b.x) / 2 - r.left - r.width / 2,
               y: (a.y + b.y) / 2 - r.top - r.height / 2 };
    }

    canvas.addEventListener('pointerdown', function (e) {
      if (e.button != null && e.button !== 0) return;   // left / touch / pen only
      pointers[e.pointerId] = pos(e);
      moved = 0;
      if (Object.keys(pointers).length === 2) { pinch = spread(); last = null; }
      else { last = pos(e); }
    });

    /* Capture is taken when a drag STARTS, not when a press does.
     *
     * Capturing on `pointerdown` keeps the moves coming, but it also retargets
     * the `click` that follows to the capturing element — so an ordinary click
     * on a province would be delivered to the canvas instead of to the <a>,
     * and every link inside the map would go quiet. Nothing needs capture until
     * the pointer is actually dragging, and by then there is no click to lose. */
    function grab(e) {
      if (captured || !canvas.setPointerCapture) return;
      try { canvas.setPointerCapture(e.pointerId); captured = e.pointerId; } catch (err) {}
    }
    function ungrab() {
      if (captured == null || !canvas.releasePointerCapture) { captured = null; return; }
      try { canvas.releasePointerCapture(captured); } catch (err) {}
      captured = null;
    }

    canvas.addEventListener('pointermove', function (e) {
      if (!pointers[e.pointerId]) return;
      pointers[e.pointerId] = pos(e);
      var n = Object.keys(pointers).length;

      if (n >= 2) {
        var d = spread();
        if (pinch && d) {
          var m = mid();
          window.AIRBG_MAP_ZOOM_AT(id, d / pinch, m.x, m.y);
          moved = MOVED + 1;                 // a pinch is never a click
        }
        pinch = d;
        e.preventDefault();
        return;
      }

      if (!last) return;
      var dx = e.clientX - last.x, dy = e.clientY - last.y;
      moved += Math.abs(dx) + Math.abs(dy);
      last = pos(e);
      if (!dx && !dy) return;
      window.AIRBG_MAP_PAN(id, dx, dy);
      // Only take the gesture off the page once it is genuinely a drag, so a
      // tap-then-scroll on touch still scrolls.
      if (moved > MOVED) { grab(e); e.preventDefault(); }
    });

    function release(e) {
      if (pointers[e.pointerId]) delete pointers[e.pointerId];
      if (Object.keys(pointers).length < 2) pinch = 0;
      if (!Object.keys(pointers).length) {
        if (moved > MOVED) armClickGuard();
        ungrab();
        last = null; moved = 0;
      }
    }
    canvas.addEventListener('pointerup', release);
    canvas.addEventListener('pointercancel', release);

    /* Double-click zooms in one step, about the point clicked.
     *
     * It is the oldest map gesture there is, and it costs nothing to honour:
     * ZSTEP is the buttons' own step, read off map-render so the two controls
     * can never disagree about how far one press goes. Shift + double-click
     * zooms out, which is the same convention.
     *
     * It does NOT fire on a province or a marker. Those are links and buttons,
     * and the browser delivers their `click` first — so a double-click there
     * would navigate on the way to zooming, and the reader would land on
     * another page having asked for a closer look. Where the pointer is on
     * something clickable the gesture is simply not offered; everywhere else
     * on the canvas it is.
     *
     * A double-click also selects text under the pointer in most browsers, so
     * the default is suppressed once we have decided to act on it. */
    canvas.addEventListener('dblclick', function (e) {
      if (e.target.closest && e.target.closest('a, .map-point')) return;
      e.preventDefault();
      var r = canvas.getBoundingClientRect();
      var step = window.AIRBG_MAP_ZSTEP || 1.5;
      window.AIRBG_MAP_ZOOM_AT(id, e.shiftKey ? 1 / step : step,
        e.clientX - r.left - r.width / 2, e.clientY - r.top - r.height / 2);
    });

    /* Ctrl/⌘ + wheel zooms about the pointer; a plain wheel is left to the
     * page. `passive: false` is required to be allowed to preventDefault. */
    var hintTimer = null;
    canvas.addEventListener('wheel', function (e) {
      if (!(e.ctrlKey || e.metaKey)) { showHint(); return; }
      e.preventDefault();
      var r = canvas.getBoundingClientRect();
      window.AIRBG_MAP_ZOOM_AT(id, Math.pow(WHEEL_STEP, -e.deltaY),
        e.clientX - r.left - r.width / 2, e.clientY - r.top - r.height / 2);
    }, { passive: false });

    /* The hint is a fact about the control, so it is written from the
     * catalogue and it disappears on its own. It is `aria-hidden`: a keyboard
     * reader is not being denied anything here — the buttons and the arrow
     * keys do the same job — so announcing a mouse-only tip would be noise. */
    var hint = document.createElement('p');
    hint.className = 'map-hint';
    hint.setAttribute('aria-hidden', 'true');
    hint.hidden = true;
    frame.appendChild(hint);
    function showHint() {
      hint.textContent = t('map.wheelHint');
      hint.hidden = false;
      clearTimeout(hintTimer);
      hintTimer = setTimeout(function () { hint.hidden = true; }, 1800);
    }
    document.addEventListener('airbg:languagechange', function () {
      if (!hint.hidden) hint.textContent = t('map.wheelHint');
    });

    /* Keyboard parity. Drag and wheel are pointer-only, so the same two
     * operations are on the arrow keys and +/− once the map itself has focus.
     * Without this the map would be a surface only a mouse can move, which is
     * the §6/§7 floor this system holds everywhere else. */
    canvas.setAttribute('tabindex', '0');
    canvas.setAttribute('role', 'application');
    function paintCanvasName() { canvas.setAttribute('aria-label', t('map.panHint')); }
    paintCanvasName();
    document.addEventListener('airbg:languagechange', paintCanvasName);

    canvas.addEventListener('keydown', function (e) {
      var STEP = e.shiftKey ? 120 : 40;
      var handled = true;
      switch (e.key) {
        case 'ArrowLeft':  window.AIRBG_MAP_PAN(id,  STEP, 0); break;
        case 'ArrowRight': window.AIRBG_MAP_PAN(id, -STEP, 0); break;
        case 'ArrowUp':    window.AIRBG_MAP_PAN(id, 0,  STEP); break;
        case 'ArrowDown':  window.AIRBG_MAP_PAN(id, 0, -STEP); break;
        case '+': case '=': window.AIRBG_MAP_VIEW(id, 'in'); break;
        case '-': case '_': window.AIRBG_MAP_VIEW(id, 'out'); break;
        case '0': window.AIRBG_MAP_VIEW(id, id === 'area-map' ? 'province' : 'country'); break;
        default: handled = false;
      }
      if (handled) e.preventDefault();
    });
  });
})();
