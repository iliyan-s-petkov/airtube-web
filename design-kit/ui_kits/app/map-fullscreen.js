/* Full-screen map.
 *
 * The map is the primary surface (§5.2), so on a laptop the reader often wants
 * it to be the ONLY surface. The native Fullscreen API is the right mechanism:
 * the browser owns the exit affordance and Escape, which no in-page imitation
 * can match.
 *
 * It can still be refused — an iframe without allowfullscreen, an embedded
 * preview, Safari on iPhone — so a CSS fallback pins the frame over the
 * viewport instead. The control must work or say why; it must never sit there
 * doing nothing.
 */
(function () {
  var btn = document.querySelector('[data-od-id="map-fullscreen"]');
  if (!btn) return;                       // screens without a map simply have none
  var frame = btn.closest('.map');
  if (!frame) { console.error('map-fullscreen: button is not inside a .map frame.'); return; }

  function t(k) { return window.AIRBG_T ? window.AIRBG_T(k) : k; }

  function isFull() {
    return document.fullscreenElement === frame || frame.classList.contains('map--faux-full');
  }

  function paint() {
    var on = isFull();
    btn.setAttribute('aria-pressed', on ? 'true' : 'false');
    // The glyph is the affordance; the name is still required (§5.2a). Both are
    // written from the catalogue on every paint, so an icon-only control still
    // translates. The name states what the control will DO next, which is the
    // opposite of the state aria-pressed reports.
    var name = t(on ? 'map.exitFullscreen' : 'map.fullscreen');
    btn.setAttribute('aria-label', name);
    btn.setAttribute('title', name);
  }

  function enter() {
    if (frame.requestFullscreen) {
      frame.requestFullscreen().then(paint, faux);   // refused -> fall back, never nothing
    } else { faux(); }
  }
  function faux() { frame.classList.add('map--faux-full'); document.body.classList.add('has-faux-full'); paint(); }
  function exit() {
    if (document.fullscreenElement === frame && document.exitFullscreen) document.exitFullscreen();
    frame.classList.remove('map--faux-full');
    document.body.classList.remove('has-faux-full');
    paint();
  }

  btn.addEventListener('click', function () { isFull() ? exit() : enter(); });

  // The browser handles Escape in real fullscreen; the fallback needs its own.
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && frame.classList.contains('map--faux-full')) exit();
  });
  document.addEventListener('fullscreenchange', paint);
  document.addEventListener('airbg:languagechange', paint);
  paint();
})();
