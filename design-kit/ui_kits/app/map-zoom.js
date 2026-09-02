/* Zoom controls for both maps.
 *
 * The map is the primary surface (§5.2) and until now it was a fixed picture:
 * the reader could see that Смолян was dark and could not get closer to it.
 * These are the controls that move it.
 *
 * Three buttons, not two. Plus and minus are obvious; the third is the way
 * back — "Цялата страна" on a province page, and on the country map the same
 * button returns the fit after zooming. Same argument as *Всички* in the table
 * (§5.4) and *Автоматична* in the theme picker (§5.2a): the way back is part
 * of the control, not something the reader should have to reconstruct by
 * clicking minus the right number of times.
 *
 * The buttons own no arithmetic. They call AIRBG_MAP_VIEW(id, action) in
 * map-render.js, which holds the one copy of the zoom state and repaints; a
 * second copy here would be a second thing free to disagree with what is
 * actually drawn (§5.12 makes the same call about the province URL).
 *
 * Icons are inline SVG in currentColor — no icon font, no second asset (§1) —
 * and each carries a name from the catalogue, written on every paint, because
 * an icon-only control whose only name is hardcoded markup is untranslated by
 * construction (§5.2a).
 */
(function () {
  var frames = document.querySelectorAll('[data-od-id="map"], [data-od-id="area-map"]');
  if (!frames.length) return;
  if (!window.AIRBG_T) console.error('map-zoom: i18n.js must load first.');

  var SVGNS = 'http://www.w3.org/2000/svg';
  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }

  function ico(paths) {
    var svg = document.createElementNS(SVGNS, 'svg');
    svg.setAttribute('viewBox', '0 0 16 16');
    svg.setAttribute('width', '16'); svg.setAttribute('height', '16');
    svg.setAttribute('aria-hidden', 'true'); svg.setAttribute('focusable', 'false');
    svg.setAttribute('class', 'map-zoom__ico');
    paths.forEach(function (dd) {
      var p = document.createElementNS(SVGNS, 'path');
      p.setAttribute('d', dd);
      p.setAttribute('fill', 'none');
      p.setAttribute('stroke', 'currentColor');
      p.setAttribute('stroke-width', '1.5');
      p.setAttribute('stroke-linecap', 'square');
      svg.appendChild(p);
    });
    return svg;
  }

  var GLYPH = {
    in:      [['M8 3.25v9.5', 'M3.25 8h9.5']],
    out:     [['M3.25 8h9.5']],
    /* A divided territory, not a frame.
     *
     * The first draft was corner brackets around a dot — which is the
     * full-screen control's own glyph, sitting 8px above it in the same 40px
     * square. Two different actions with near-identical marks, adjacent: the
     * render made it obvious in a way the DOM check could not. This one is a
     * bordered area split into regions, which is what the button returns to. */
    country: [['M1.75 3.25h12.5v9.5H1.75z', 'M8 3.25v9.5', 'M1.75 8h12.5']]
  };

  frames.forEach(function (frame) {
    var id = frame.getAttribute('data-od-id');
    var bar = document.createElement('div');
    bar.className = 'map-zoom';
    bar.setAttribute('data-od-id', id + '-zoom');

    var btns = {};
    ['in', 'out', 'country'].forEach(function (act) {
      var b = document.createElement('button');
      b.type = 'button';
      b.className = 'map-zoom__btn';
      b.setAttribute('data-act', act);
      b.appendChild(ico(GLYPH[act][0]));
      b.addEventListener('click', function () {
        /* When the vector basemap is mounted MapLibre owns the camera, so the
         * same three buttons drive it instead. One control, two engines — the
         * alternative was a second zoom cluster, which is the duplicated
         * control this system keeps removing. */
        var tm = frame.__airbgTileMap;
        if (tm) {
          if (act === 'in') tm.zoomIn();
          else if (act === 'out') tm.zoomOut();
          else tm.flyTo({ center: [25.4858, 42.7339], zoom: 6.2 });
          return;
        }
        window.AIRBG_MAP_VIEW(id, act);
        paint();
      });
      bar.appendChild(b);
      btns[act] = b;
    });
    frame.appendChild(bar);

    /* The reset button says which view it returns to, and on a province page
     * that is a different sentence from the one on the country map. A single
     * generic label would be true of neither. */
    function paint() {
      var v = window.AIRBG_MAP_STATE(id);
      if (!v) return;
      var back = (id === 'area-map' && v.mode === 'province') || v.k > 1;
      btns.in.setAttribute('aria-label', t('map.zoomIn'));
      btns.in.setAttribute('title', t('map.zoomIn'));
      btns.out.setAttribute('aria-label', t('map.zoomOut'));
      btns.out.setAttribute('title', t('map.zoomOut'));
      var backName = id === 'area-map' ? t('map.wholeCountry') : t('map.resetView');
      btns.country.setAttribute('aria-label', backName);
      btns.country.setAttribute('title', backName);
      // Disabled states the limit; a click that silently does nothing does not
      // (§5.11). At the country fit there is nothing to zoom out to.
      // Out always does something now: it either scales down or widens the
      // subject to the country. Only the country fit is the true floor.
      btns.out.disabled = v.mode === 'country' && v.k <= 1;
      btns.in.disabled = v.k >= 8;
      btns.country.disabled = !back;
    }

    document.addEventListener('airbg:languagechange', paint);
    document.addEventListener('airbg:viewchange', paint);
    paint();
  });

  /* A province page can be sent back to its own province from elsewhere — the
   * finder does this when the reader picks a place inside it. */
  document.addEventListener('airbg:areaselect', function () {
    var v = window.AIRBG_MAP_STATE('area-map');
    if (v && v.mode !== 'province') window.AIRBG_MAP_VIEW('area-map', 'province');
  });
})();
