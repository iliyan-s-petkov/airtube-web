/* Area time-series: metric + window are the reader's, not the page's.
 *
 * The section used to be a fixed "Последните 24 часа" with an unlabelled
 * series. Two things were unanswerable from the screen: WHAT is plotted
 * (PM2.5 or PM10 — the axis said only µg/m³) and WHAT PERIOD, since the window
 * could not be changed. Both are now controls, and the heading restates the
 * answer in words: metric · window · aggregation tier (§9.1).
 *
 * The shape is derived
 * deterministically from (metric, hours) so that changing either control
 * visibly changes the chart — a control that redraws nothing is the defect this
 * design system has hit most often.
 */
(function () {
  var root = document.querySelector('[data-od-id="area-chart-section"]');
  if (!root) { console.error('area-chart: no [data-od-id="area-chart-section"] on the page.'); return; }
  if (!window.AIRBG_T) console.error('area-chart: i18n.js must load first.');

  var svg      = root.querySelector('[data-od-id="area-chart"] svg');
  var heading  = root.querySelector('[data-od-id="area-chart-title"]');
  var custom   = root.querySelector('[data-od-id="chart-custom"]');
  var fromInp  = root.querySelector('#chart-from');
  var toInp    = root.querySelector('#chart-to');

  // The kit has no clock of its own: "now" is the snapshot the page reports as
  // its update time, so every label on the screen agrees.
  var NOW = new Date('2026-09-01T12:10');
  var MAX_HOURS = 42;                       // the furthest back the reader may go

  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }

  var state = { metrics: ['pm25'], hours: 24, from: null, to: null };

  var SVGNS = 'http://www.w3.org/2000/svg';
  function el(name, attrs) {
    var n = document.createElementNS(SVGNS, name);
    for (var k in attrs) n.setAttribute(k, attrs[k]);
    return n;
  }

  /* A gridline the reader can price is a gridline on a round number. Steps come
   * from 1 / 2 / 2.5 / 5 × 10^n, so the axis reads 0 · 1 · 2 · 3 rather than
   * 0 · 0,87 · 1,74. The top line is the scale's ceiling, not the data's peak:
   * ending the axis on the maximum reading puts a gridline through the highest
   * point and makes it unreadable. */
  function metricName(m) { return t(m === 'pm10' ? 'col.pm10Short' : 'col.pm25Short'); }

  var NICE = [1, 2, 2.5, 5];
  function chooseStep(peak, maxLines) {
    // The smallest round step that still keeps the grid under maxLines. Deriving
    // the step from peak/4 and rounding up overshot badly at some peaks — 10,2
    // took a step of 5 and a ceiling of 15, half the panel empty above the data.
    for (var e = -3; e <= 6; e++) {
      for (var i = 0; i < NICE.length; i++) {
        var step = NICE[i] * Math.pow(10, e);
        if (Math.ceil(peak / step) <= maxLines) return step;
      }
    }
    return peak;
  }

  /* Hour steps a reader already thinks in. 7 hourly lines across 6 hours is
   * legible; 42 is a hatch pattern. */
  var HOUR_STEPS = [1, 2, 3, 6, 12, 24];
  function hourStep(hours) {
    for (var i = 0; i < HOUR_STEPS.length; i++) {
      if (hours / HOUR_STEPS[i] <= 8) return HOUR_STEPS[i];
    }
    return 24;
  }

  function num(v) {
    // Bulgarian writes a decimal comma; the axis is measured data like any
    // other value on the page (§3), so the locale decides, not the code.
    return new Intl.NumberFormat(lang(), { maximumFractionDigits: 2 }).format(v);
  }


  function pad(n) { return (n < 10 ? '0' : '') + n; }
  function hhmm(d) { return pad(d.getHours()) + ':' + pad(d.getMinutes()); }
  /* A window that crosses midnight cannot be stated in clock time alone: 24
   * hours ending now runs 14:20 → 14:20, which reads as no window at all. The
   * day comes from the locale, never from a month list typed here (§5.12). */
  function dayMon(d) {
    return new Intl.DateTimeFormat(lang(), { day: 'numeric', month: 'short' }).format(d);
  }
  function stamp(d, withDay) { return hhmm(d) + (withDay ? ', ' + dayMon(d) : ''); }
  function isoLocal(d) {
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) +
           'T' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  function range() {
    if (state.from && state.to) return { from: state.from, to: state.to };
    return { from: new Date(NOW.getTime() - state.hours * 3600e3), to: NOW };
  }

  /* A repeatable walk, so the same window always draws the same line. PM10
   * runs higher than PM2.5 because the coarse fraction contains the fine one
   * (§5.4) — the sample must not contradict the rule the table enforces. */
  function series(metric, n) {
    var base = metric === 'pm10' ? 5.6 : 2.5;
    var seed = metric === 'pm10' ? 91 : 17;
    var out = [], v = base;
    for (var i = 0; i < n; i++) {
      seed = (seed * 1103515245 + 12345) % 2147483648;
      var wobble = ((seed / 2147483648) - 0.45) * base * 0.55;
      var diurnal = Math.sin((i / n) * Math.PI * 2 - 1.1) * base * 0.42;
      v = Math.max(base * 0.25, v * 0.55 + (base + diurnal + wobble) * 0.45);
      out.push(v);
    }
    return out;
  }

  function draw() {
    var r = range();
    var hours = Math.max(1, Math.round((r.to - r.from) / 3600e3));
    var n = Math.min(Math.max(hours, 6), 84);
    var plotted = state.metrics.map(function (m) { return { metric: m, pts: series(m, n) }; });
    var pts = plotted[0].pts;                     // the x scale is shared
    var W = 780, H = 260, L = 46, R = 40, T = 30, B = 40;
    // One scale for both series, so the reader compares heights, not axes.
    var peak = Math.max.apply(null, plotted.map(function (p) { return Math.max.apply(null, p.pts); }));
    var step = chooseStep(peak, 6);
    var max  = step * Math.ceil(peak / step);
    // Only when the peak lands exactly on the top line does it need one more:
    // otherwise the ceiling already sits above the data by construction.
    if (max <= peak) max += step;
    var x = function (i) { return L + (i / (pts.length - 1)) * (W - L - R); };
    var y = function (v) { return H - B - (v / max) * (H - T - B); };
    var xAt = function (time) {
      return L + ((time - r.from) / (r.to - r.from)) * (W - L - R);
    };

    /* A second series cannot take a second colour: the palette has exactly one
     * accent (§2.2) and every other hue on this site belongs to the air-quality
     * ramp (§2.1). So PM10 is the same blue, dashed. The fill goes when two are
     * plotted — two areas over one another read as a third value that is not
     * there. */
    var host = svg.querySelector('[data-series]');
    host.textContent = '';
    plotted.forEach(function (p) {
      var d = p.pts.map(function (v, i) {
        return (i ? 'L' : 'M') + x(i).toFixed(1) + ' ' + y(v).toFixed(1);
      }).join(' ');
      if (plotted.length === 1) {
        host.appendChild(el('path', { class: 'series-area',
          d: d + ' L' + x(p.pts.length - 1).toFixed(1) + ' ' + (H - B) + ' L' + L + ' ' + (H - B) + ' Z' }));
      }
      host.appendChild(el('path', { class: 'series-line series-line--' + p.metric, d: d }));
    });


    /* The axes are rebuilt on every draw: how many lines belong on the chart is
     * a function of the window and the peak, so they cannot be markup. Three
     * clock labels and no y values at all was the defect — the reader could see
     * the shape and price nothing on it. */
    var grid = svg.querySelector('[data-grid]');
    var yAxis = svg.querySelector('[data-yaxis]');
    var xAxis = svg.querySelector('[data-xaxis]');
    grid.textContent = ''; yAxis.textContent = ''; xAxis.textContent = '';

    for (var v = 0; v <= max + 1e-9; v += step) {
      var gy = y(v);
      // The zero line is the axis itself, already drawn; no second rule on it.
      if (v > 0) grid.appendChild(el('line', { x1: L, y1: gy.toFixed(1), x2: W - R, y2: gy.toFixed(1) }));
      var label = el('text', { class: 'axis-text axis-text--y', x: L - 8, y: (gy + 4).toFixed(1) });
      label.textContent = num(v);
      yAxis.appendChild(label);
    }

    // Clock labels land on whole hours, so they read 12:00, not 12:37.
    var stepH = hourStep(hours);
    var first = new Date(r.from);
    first.setMinutes(0, 0, 0);
    if (first < r.from) first = new Date(first.getTime() + 3600e3);
    while (first.getHours() % stepH !== 0) first = new Date(first.getTime() + 3600e3);
    for (var tms = first.getTime(); tms <= r.to.getTime(); tms += stepH * 3600e3) {
      var tx = xAt(tms);
      if (tx < L - 0.5 || tx > W - R + 0.5) continue;
      /* An hour gets a tick on the axis, not a rule through the panel. The
       * horizontal lines are what a value is read against; verticals only align
       * a moment to its label, and the label is already under it. */
      xAxis.appendChild(el('line', {
        class: 'chart-tick', x1: tx.toFixed(1), y1: H - B, x2: tx.toFixed(1), y2: H - B + 4
      }));
      var xl = el('text', { class: 'axis-text axis-text--x', x: tx.toFixed(1), y: H - B + 20 });
      xl.textContent = hhmm(new Date(tms));
      xAxis.appendChild(xl);
    }
    /* The axis caption is the UNIT alone. The heading above the panel already
     * names the metric — printing "ФПЧ2.5" twice inside one component makes the
     * reader check whether the two are saying different things. The unit is not
     * a duplicate: the heading carries no unit, and §9.4 requires one. */
    var unit = svg.querySelector('[data-unit]');
    if (unit) unit.textContent = t('chart.axisUnit');

    /* The key names the lines only when there is more than one. With a single
     * series the heading already names it, and a key would be the duplicate
     * this panel just had removed. */
    var key = svg.querySelector('[data-key]');
    key.textContent = '';
    if (plotted.length > 1) {
      plotted.forEach(function (p, i) {
        var kx = W - R - 160 + i * 82;
        key.appendChild(el('line', { class: 'series-line series-line--' + p.metric,
          x1: kx, y1: 16, x2: kx + 18, y2: 16 }));
        var kt = el('text', { class: 'axis-text', x: kx + 24, y: 20 });
        kt.textContent = metricName(p.metric);
        key.appendChild(kt);
      });
    }

    var names = state.metrics.map(metricName).join(t('chart.metricJoin'));
    var sameDay = r.from.toDateString() === r.to.toDateString();
    var windowText = (state.from && state.to)
      ? stamp(r.from, !sameDay) + ' – ' + stamp(r.to, !sameDay)
      : t('chart.lastHours', { n: hours });

    // The heading answers both questions the old one left open.
    heading.textContent = names + ' · ' + windowText + ' · ' + t('area.tierOblast');
    svg.setAttribute('aria-label', t('chart.ariaLabel', {
      metric: names, window: windowText,
      min: num(+Math.min.apply(null, plotted.map(function (p) { return Math.min.apply(null, p.pts); })).toFixed(1)),
      max: num(+peak.toFixed(1))
    }));
  }

  /* Never zero series. The last one checked goes disabled, which states the
   * limit; a click that silently does nothing does not (§5.11). Run on load as
   * well as on change, or the first paint ships an unlocked lone checkbox. */
  function readMetrics() {
    var boxes = [].slice.call(root.querySelectorAll('input[name="chart-metric"]'));
    var on = boxes.filter(function (b) { return b.checked; });
    state.metrics = on.map(function (b) { return b.value; });
    boxes.forEach(function (b) { b.disabled = (on.length === 1 && b.checked); });
  }

  root.addEventListener('change', function (e) {
    var el = e.target;
    if (el.name === 'chart-metric') { readMetrics(); draw(); return; }
    if (el.name === 'chart-window') {
      if (el.value === 'custom') {
        custom.hidden = false;
        // Seed the custom fields with the window already on screen, so opening
        // the control never blanks the chart.
        var r = range();
        fromInp.value = isoLocal(r.from); toInp.value = isoLocal(r.to);
        state.from = r.from; state.to = r.to;
      } else {
        custom.hidden = true;
        state.from = state.to = null;
        state.hours = parseInt(el.value, 10);
      }
      draw(); return;
    }
    if (el === fromInp || el === toInp) {
      var f = new Date(fromInp.value), tt = new Date(toInp.value);
      if (isNaN(f) || isNaN(tt) || f >= tt) return;   // ignore, never redraw to nothing
      state.from = f; state.to = tt; draw();
    }
  });

  // The floor is fixed by the retention the app exposes, not by taste.
  var floor = new Date(NOW.getTime() - MAX_HOURS * 3600e3);
  [fromInp, toInp].forEach(function (i) {
    i.min = isoLocal(floor); i.max = isoLocal(NOW);
  });

  readMetrics();
  document.addEventListener('airbg:languagechange', draw);
  draw();
})();
