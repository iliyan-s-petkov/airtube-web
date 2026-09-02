/* The detail card for a selected place, under the map.
 *
 * Clicking a marker used to produce one line — *"Lovech · 6,9 µg/m³ PM2.5 ·
 * city average, 5 sensors in the aggregate"* — which is a caption, not an
 * answer. A reader who has just picked a place wants both particulates, what
 * band each one is in, how it compares with the province around it, and what
 * it has been doing.
 *
 * Every field here is measured. The readings and the sensor count come from
 * `/api/v1/areas`; the history comes from `/api/v1/area/{slug}/series`, which
 * this kit captures into `assets/bg-series.json` because the API sends no CORS
 * header and a browser cannot read it from here at runtime (§5.3a).
 *
 * Where a series was not captured the card says so and draws nothing. A chart
 * of invented numbers under a real reading would be the fabrication this
 * system removed twice already (§5.2b).
 */
(function () {
  var host = document.querySelector('[data-od-id="place-card"]');
  if (!host) return;
  if (!window.AIRBG_T) console.error('area-place: i18n.js must load first.');

  var SVGNS = 'http://www.w3.org/2000/svg';
  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }
  function metric() { return window.AIRBG_METRIC ? window.AIRBG_METRIC() : 'p2'; }
  function num(v, d) {
    return new Intl.NumberFormat(lang(), { maximumFractionDigits: d == null ? 1 : d }).format(v);
  }
  function el(n, a) {
    var e = document.createElementNS(SVGNS, n);
    for (var k in a) e.setAttribute(k, a[k]);
    return e;
  }
  function tag(n, cls, txt) {
    var e = document.createElement(n);
    if (cls) e.className = cls;
    if (txt != null) e.textContent = txt;
    return e;
  }

  var SERIES = null;
  fetch('../../assets/bg-series.json').then(function (r) { return r.json(); })
    .then(function (d) { SERIES = d.series || {}; if (state.place) render(); })
    .catch(function () { SERIES = {}; });

  var state = { place: null, period: '24h' };

  function bands(m) {
    var d = window.AIRBG_DATA || {};
    return (m === 'p10' ? d.scale_p1_eaqi : d.scale_p2_eaqi) || d.scale_p2_eaqi || [];
  }
  function bandOf(v, m) {
    if (v == null) return null;
    var b = bands(m);
    for (var i = 0; i < b.length; i++) if (b[i].upper == null || v <= b[i].upper) return b[i];
    return b[b.length - 1];
  }
  function bandName(b) { return !b ? t('legend.none') : (lang() === 'en' ? b.label : b.label_bg); }
  function label(a) { return lang() === 'en' ? a.name_en : a.name_bg; }

  /* The province this place sits in, so the card can say how the two compare.
   * A number with nothing to compare it against is a number the reader has to
   * price on their own (§9.1 makes the same argument about naming the tier). */
  function parent() {
    var d = window.AIRBG_DATA;
    if (!d || !state.place) return null;
    return (d.oblasti || []).filter(function (o) { return o.name_bg === state.place.oblast; })[0] || null;
  }

  function seriesFor(slug, m, period) {
    if (!SERIES) return null;
    var s = SERIES[slug];
    return s && s[(m === 'p10' ? 'P1_' : 'P2_') + period] || null;
  }
  function periodsFor(slug, m) {
    return ['24h', '7d', '30d'].filter(function (p) { return !!seriesFor(slug, m, p); });
  }

  /* The chart follows §5.3's rules: anchored at zero, round gridlines, filled
   * encoding, tabular figures, and the heading names metric, window and tier
   * so no value stands on its own. */
  function chart(ser, m) {
    var W = 640, H = 180, PAD_L = 34, PAD_B = 20, PAD_T = 10, PAD_R = 6;
    var svg = el('svg', {
      viewBox: '0 0 ' + W + ' ' + H, class: 'place-chart', preserveAspectRatio: 'none',
      role: 'img'
    });
    var v = ser.v, n = v.length;
    var peak = Math.max.apply(null, v);
    // Smallest round step that keeps the grid under six lines (§5.3).
    var step = null, CAND = [1, 2, 2.5, 5];
    for (var e = -2; e <= 4 && step == null; e++) {
      for (var i = 0; i < CAND.length; i++) {
        var cand = CAND[i] * Math.pow(10, e);
        if (peak / cand <= 5) { step = cand; break; }
      }
    }
    step = step || 1;
    var top = Math.ceil(peak / step) * step || step;
    if (top === peak) top += step;
    var x = function (i) { return PAD_L + (i / Math.max(1, n - 1)) * (W - PAD_L - PAD_R); };
    var y = function (val) { return PAD_T + (1 - val / top) * (H - PAD_T - PAD_B); };

    for (var g = 0; g <= top + 1e-9; g += step) {
      svg.appendChild(el('line', { class: 'place-chart__grid',
        x1: PAD_L, x2: W - PAD_R, y1: y(g).toFixed(1), y2: y(g).toFixed(1) }));
      var lb = el('text', { class: 'place-chart__tick', x: PAD_L - 6, y: (y(g) + 3.5).toFixed(1) });
      lb.textContent = num(g, g < 10 ? 1 : 0);
      svg.appendChild(lb);
    }
    var d = '', area = '';
    for (var j = 0; j < n; j++) {
      d += (j ? 'L' : 'M') + x(j).toFixed(1) + ' ' + y(v[j]).toFixed(1);
    }
    area = d + 'L' + x(n - 1).toFixed(1) + ' ' + y(0).toFixed(1) +
               'L' + x(0).toFixed(1) + ' ' + y(0).toFixed(1) + 'Z';
    svg.appendChild(el('path', { class: 'place-chart__area', d: area }));
    svg.appendChild(el('path', { class: 'place-chart__line', d: d }));

    // Clock labels on the ends and the middle: enough to place the shape in
    // time without a tick row competing with the series (§5.3).
    [0, Math.floor(n / 2), n - 1].forEach(function (i, k) {
      var dt = new Date(ser.t[i]);
      var s = new Intl.DateTimeFormat(lang(), { hour: '2-digit', minute: '2-digit' }).format(dt);
      var e2 = el('text', {
        // The anchor is a class, not an attribute: a class rule on
        // `.place-chart__tick` would beat the attribute and re-centre it.
        class: 'place-chart__tick place-chart__tick--x' +
               (k === 0 ? ' place-chart__tick--start' : k === 2 ? ' place-chart__tick--end' : ''),
        x: x(i).toFixed(1), y: H - 6
      });
      e2.textContent = s;
      svg.appendChild(e2);
    });
    var lo = Math.min.apply(null, v);
    svg.setAttribute('aria-label', t('place.chartAria', {
      metric: t(m === 'p10' ? 'metric.p10' : 'metric.p2'),
      min: num(lo), max: num(peak), n: n
    }));
    return svg;
  }

  function render() {
    host.textContent = '';
    var a = state.place;
    if (!a) { host.hidden = true; return; }
    host.hidden = false;

    var m = metric();
    var val = m === 'p10' ? a.p10 : a.p2;
    var b = bandOf(val, m);

    var card = tag('div', 'place');
    // ---- heading -----------------------------------------------------------
    var head = tag('div', 'place__head');
    head.appendChild(tag('h2', 'place__name', label(a)));
    head.appendChild(tag('p', 'place__tier', t('point.tier.' + a.kind)));
    card.appendChild(head);

    // ---- both particulates, each with its band ------------------------------
    var grid = tag('div', 'place__grid');
    [['p2', a.p2], ['p10', a.p10]].forEach(function (pair) {
      var mm = pair[0], vv = pair[1], bb = bandOf(vv, mm);
      var cell = tag('div', 'place__cell');
      cell.appendChild(tag('span', 'place__label',
        t(mm === 'p10' ? 'metric.p10' : 'metric.p2')));
      var line = tag('span', 'place__value');
      line.textContent = vv == null ? '—' : num(vv);
      if (vv != null) {
        var u = tag('span', 'place__unit', ' µg/m³');
        line.appendChild(u);
      }
      cell.appendChild(line);
      var band = tag('span', 'place__band', bandName(bb));
      /* The swatch is a tiny SVG, not a styled span.
       *
       * The served colour has to reach the element somehow, and `style=""` is
       * forbidden outright by the CSP (§1) — no exception for one custom
       * property. An SVG `fill` is a presentation ATTRIBUTE, not a style
       * attribute, which is the same seam every band colour on the map already
       * uses (§2.1). */
      if (bb && bb.colour) {
        var sw = el('svg', { class: 'place__swatch', width: 12, height: 12,
                             viewBox: '0 0 12 12', 'aria-hidden': 'true' });
        sw.appendChild(el('rect', { width: 12, height: 12, fill: bb.colour }));
        band.insertBefore(sw, band.firstChild);
      }
      cell.appendChild(band);
      grid.appendChild(cell);
    });

    // sensors behind the aggregate
    var sc = tag('div', 'place__cell');
    sc.appendChild(tag('span', 'place__label', t('place.sensors')));
    sc.appendChild(tag('span', 'place__value', num(a.sensor_count, 0)));
    sc.appendChild(tag('span', 'place__band', t('place.sensorsTier')));
    grid.appendChild(sc);

    /* How it sits against the province around it — but only when the subject
     * is inside a province. A province compared with itself is a cell that
     * always reads ±0, which is a fact about arithmetic, not about air. */
    var par = a.isProvince ? null : parent();
    var pv = par && (m === 'p10' ? par.p10 : par.p2);
    var cmp = tag('div', 'place__cell');
    cmp.appendChild(tag('span', 'place__label', t('place.vsProvince')));
    if (val != null && pv != null) {
      var diff = val - pv;
      cmp.appendChild(tag('span', 'place__value',
        (diff > 0 ? '+' : diff < 0 ? '−' : '±') + num(Math.abs(diff))));
      cmp.appendChild(tag('span', 'place__band',
        t('place.vsProvinceTier', { name: window.AIRBG_NAME ? window.AIRBG_NAME(a.oblast) : a.oblast,
                                    value: num(pv) })));
    } else {
      cmp.appendChild(tag('span', 'place__value', '—'));
      cmp.appendChild(tag('span', 'place__band', t('place.vsProvinceNone')));
    }
    if (!a.isProvince) grid.appendChild(cmp);
    card.appendChild(grid);

    // ---- history -----------------------------------------------------------
    var hist = tag('div', 'place__history');
    var have = periodsFor(a.slug, m);
    var h3 = tag('h3', 'place__subhead');
    h3.textContent = t('place.historyHead', {
      metric: t(m === 'p10' ? 'metric.p10' : 'metric.p2'),
      tier: t('point.tier.' + a.kind)
    });
    hist.appendChild(h3);

    if (!SERIES) {
      hist.appendChild(tag('p', 'place__note', t('place.historyLoading')));
    } else if (!have.length) {
      /* Absence, stated where the chart would be (§2.3). The capture is what
       * is missing, not the measurement — so the sentence says that rather
       * than implying the place has never been measured. */
      hist.appendChild(tag('p', 'place__note', t('place.historyNone')));
    } else {
      if (have.indexOf(state.period) === -1) state.period = have[0];
      var bar = tag('div', 'place__periods');
      have.forEach(function (p) {
        var btn = tag('button', 'place__period', t('place.period.' + p));
        btn.type = 'button';
        btn.setAttribute('aria-pressed', p === state.period ? 'true' : 'false');
        btn.addEventListener('click', function () { state.period = p; render(); });
        bar.appendChild(btn);
      });
      hist.appendChild(bar);
      hist.appendChild(chart(seriesFor(a.slug, m, state.period), m));
      hist.appendChild(tag('p', 'place__note', t('place.historyStamp')));
    }
    card.appendChild(hist);
    host.appendChild(card);
  }

  /* The card opens on the province the page is about, and a marker click
   * swaps the subject.
   *
   * It first appeared only after a marker was clicked, which left the history
   * chart unreachable: the API refused the finer-tier series during capture, so
   * no city or neighbourhood has one, and a province is not a marker. A panel
   * that can only ever be reached through a path that produces nothing is the
   * dead control this system keeps hitting — and the province is the better
   * default anyway, because it is what the reader opened the page to see. */
  function provinceAsPlace() {
    var d = window.AIRBG_DATA;
    var h1 = document.querySelector('[data-oblast]');
    var key = h1 && h1.getAttribute('data-oblast');
    if (!d || !key) return null;
    var o = (d.oblasti || []).filter(function (x) { return x.name_bg === key; })[0];
    if (!o) return null;
    return {
      slug: o.slug, kind: 'oblast', name_bg: o.name_bg, name_en: o.name_en,
      lon: o.lon, lat: o.lat, p2: o.p2, p10: o.p10,
      sensor_count: o.sensor_count, oblast: o.name_bg, isProvince: true
    };
  }
  function showDefault() { state.place = provinceAsPlace(); render(); }

  document.addEventListener('airbg:areaselect', function (e) {
    if (!e.detail) return;
    state.place = e.detail;
    render();
  });
  // A different province is a different subject, so the card follows it.
  document.addEventListener('airbg:oblastchange', showDefault);
  document.addEventListener('airbg:metricchange', render);
  document.addEventListener('airbg:languagechange', render);
  document.addEventListener('airbg:datachange', function () {
    if (state.place) render(); else showDefault();
  });

  if (window.AIRBG_DATA) showDefault(); else host.hidden = true;
})();
