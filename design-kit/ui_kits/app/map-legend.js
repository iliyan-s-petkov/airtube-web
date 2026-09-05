/* map-legend.js — paints the scale bar from the ramp the MAP uses.
 *
 * The bar used to be six blocks, each carrying its own served colour. Asked for
 * a progressive scale instead, which is one line of CSS to fake and a real
 * defect to ship: a smooth key over a banded map shows colours nothing on
 * screen uses, and it looks correct while being wrong. §5.2 records the
 * mirror-image of that ("recolouring a map without moving its legend").
 *
 * So the gradient is not written anywhere by hand. It is built here from
 * window.AIRBG_RAMP(), which is the same function map-render.js fills every hex
 * and every province with. One ramp, two surfaces — they cannot disagree,
 * because there is nothing to keep in step.
 *
 * The band EDGES stay as they were. EAQI's thresholds are data (§2.1): a reader
 * still needs to know that 25 is where Лошо starts, and a gradient with no
 * numbers on it says only "more is worse".
 */
(function () {
  'use strict';

  function bar(scale) {
    var ramp = window.AIRBG_RAMP && window.AIRBG_RAMP();
    if (!ramp || !ramp.stops || ramp.stops.length < 2) return false;

    /* Each stop states its own position, because the axis is piecewise: 0–100
     * over the lower ~79 % and 100–500 above it. Spacing them evenly here
     * would draw a gradient the map does not paint. */
    var stops = ramp.stops.map(function (s) {
      return s.colour + ' ' + (100 * s.pos).toFixed(2) + '%';
    });
    /* `to top`, because the bar reads upward: worst at the top, like the
     * numbers beside it. */
    scale.style.setProperty('--ramp', 'linear-gradient(to top, ' + stops.join(', ') + ')');

    /* The tick numbers come from the ramp too. They were authored into the
     * markup as EAQI edges (5/10/20/25/50), one per band row, which on this
     * axis is wrong twice over: wrong values, and positioned by row rather
     * than by where the value actually falls on the bar. */
    var host = scale.querySelector('.scale__bands');
    if (host && ramp.ticks) {
      var list = host.querySelector('.scale__ticks');
      if (!list) {
        list = document.createElement('ul');
        list.className = 'scale__ticks';
        list.setAttribute('aria-hidden', 'true');   /* the bands list is the accessible copy */
        host.appendChild(list);
      }
      list.textContent = '';
      ramp.ticks.forEach(function (t, i) {
        var li = document.createElement('li');
        li.className = 'scale__tick';
        li.style.insetBlockEnd = (100 * t.pos).toFixed(2) + '%';
        li.textContent = t.value + (i === 0 && ramp.unit ? ' ' + ramp.unit : '');
        list.appendChild(li);
      });
    }
    return true;
  }

  function paint() {
    var scale = document.querySelector('[data-od-id="map-legend"]');
    if (!scale) return;
    /* The fold is a bare triangle, so its accessible name has to come from
     * somewhere — and from the catalogue on every paint, not baked into the
     * markup, or it is untranslated by construction (§5.2a). */
    var toggle = scale.querySelector('.scale__toggle');
    if (toggle) {
      var name = (window.AIRBG_T ? window.AIRBG_T('legend.title') : 'Скала');
      toggle.setAttribute('aria-label', name);
      toggle.setAttribute('title', name);
    }
    /* The class goes on only when the ramp is actually available. Without it
     * the six-block bar stays exactly as it was — a scale that silently loses
     * its colours is worse than one that never changed. */
    if (bar(scale)) scale.classList.add('scale--progressive');
    else scale.classList.remove('scale--progressive');
  }

  /* The ramp is derived from the served scale, so it only exists once data has
   * landed — and it changes with the metric, because PM10's bands are not
   * PM2.5's (§5.2). Repaint on both, exactly like every other component that
   * composes from state. */
  /* airbg:rampchange is the one that matters: it fires from inside the render
   * pass, once the ramp is actually defined. datachange fires earlier, when the
   * data lands but before anything has been drawn from it. Listening only to
   * datachange is what left the bar on its six-block fallback while the map
   * interpolated — the key and the map disagreeing, which is the single thing
   * this component exists to prevent. */
  document.addEventListener('airbg:rampchange', paint);
  document.addEventListener('airbg:datachange', paint);
  document.addEventListener('airbg:metricchange', paint);
  document.addEventListener('airbg:languagechange', paint);
  if (document.readyState !== 'loading') paint();
  else document.addEventListener('DOMContentLoaded', paint);
}());
