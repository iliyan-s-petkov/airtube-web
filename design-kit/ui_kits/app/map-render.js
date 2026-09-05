/* Choropleth preview: real province boundaries, served colours, value in place.
 *
 * This does NOT replace MapLibre. The app mounts a real map with tiles, panning
 * and per-sensor zoom tiers; this draws one frame of it so the design can be
 * checked against actual data (§5.2).
 *
 * Why areas rather than dots. A dot is a point, and a province average is not
 * measured at a point — it belongs to the whole territory, so a filled area is
 * the honest mark for it. It also fixes a defect the dots had: София-град sits
 * inside Софийска, and as two circles they overlapped; as two polygons the
 * smaller simply sits inside the larger, which is what the geography does.
 *
 * Constraints, unchanged:
 *   §1   no third-party origin at runtime — boundaries and readings are local;
 *   §2.1 the ramp is data — fills come from the served scale, never a palette;
 *   §5.12 a real referent gets its real asset — Natural Earth admin-1, not a
 *        shape drawn by hand.
 */
(function () {
  var frames = document.querySelectorAll('[data-od-id="map"], [data-od-id="area-map"]');
  if (!frames.length) return;

  var SVGNS = 'http://www.w3.org/2000/svg';
  function el(n, a) {
    var e = document.createElementNS(SVGNS, n);
    for (var k in a) e.setAttribute(k, a[k]);
    return e;
  }
  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }
  function nameOf(bg) { return window.AIRBG_NAME ? window.AIRBG_NAME(bg) : bg; }
  /* Every label a province can ever carry, in both languages.
   *
   * Placement used to measure the string the reader is looking at, so the same
   * province sat in a different spot — and at a different size — on the
   * Bulgarian and the English map. Two maps of one country whose labels move
   * when the UI language changes read as two different maps. The geometry is a
   * property of the province, not of the current locale, so it is computed
   * from the WIDER of the two names and the drawing then puts whichever string
   * belongs to the current language into that fixed box. */
  function namesOf(bg) {
    var en = (window.AIRBG_OBLAST_EN || {})[bg];
    return en && en !== bg ? [bg, en] : [bg];
  }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }
  function num(v) {
    return new Intl.NumberFormat(lang(), { maximumFractionDigits: 1 }).format(v);
  }

  var W = 926, H = 382, PAD = 14;          // the measured fit (§1)

  /* Zoom is state, not geometry, so it lives outside the draw pass and
   * survives a repaint: a language switch or a metric switch must not throw
   * the reader back to the country. `mode` is what the frame is framed ON —
   * a province slug or the whole country — and `k` is what the reader has
   * done to it since. */
  var VIEWS = {};
  /* ZMAX was 8 when there was nothing below the province to look at, and it
   * silently became a ceiling under the new layers: Русе's own fit is 6,4 px/km,
   * so eight times it is 51 px/km — under the street-name gate, which therefore
   * could never fire on that page no matter how many times the reader pressed
   * plus. A gate above the ceiling is a feature that cannot happen, and it
   * looks exactly like one that is broken. 24 puts every province past 100
   * px/km; the floor is still the country fit. */
  var ZMIN = 1, ZMAX = 24, ZSTEP = 1.5;
  /* Published, because the pointer gestures need the same step the buttons
   * take. A second constant in map-pan.js would be a second answer to "how far
   * is one zoom", free to drift from the one the buttons use. */
  window.AIRBG_MAP_ZSTEP = ZSTEP;
  /* Where each basemap tier earns its place — in PIXELS PER KILOMETRE, not in
   * zoom steps.
   *
   * The first draft keyed both to `vk`, the scale relative to the country fit.
   * That is a ratio, not a distance, and it gets small provinces badly wrong:
   * Смолян's own fit is 6.6×, which cleared a `vk >= 6` gate and drew the whole
   * city street network into about eighteen pixels — a smudge that claimed to be
   * a street map. What decides whether a street can be seen is how many pixels a
   * kilometre gets, and that is the number to threshold on.
   *
   * 2.5 px/km: a national road is a line rather than a hair.
   * 25 px/km: a ~5 km city spans ~125 px, which is enough to read its shape. */
  var ROADS_PX_KM = 2.5, STREETS_PX_KM = 25;
  /* The national highways come in far earlier than the full network: at country
   * zoom (~1.05 px/km) they are the difference between a map and a diagram. */
  var MAJOR_PX_KM = 0.8;
  /* The minor network — tertiary, residential, unclassified, living street,
   * pedestrian — and the street names that come with it.
   *
   * 40 px/km: a 200 m residential block is 8 px, which is the point where a
   * side street is a line rather than a speck. 90 px/km: a street name at 10px
   * needs ~60 px of straight run to sit on, and below that scale almost no
   * segment offers one — so the names would thrash in and out as the reader
   * panned. Both are ground scale, not zoom steps, for the reason the street
   * gate already is. 60 rather than 90 because it has to be reachable on a
   * large province as well as a small one. */
  var MINOR_PX_KM = 40, STREETNAME_PX_KM = 60;
  /* Quarters sit between district names and street names: finer than a район,
   * coarser than a street. Set from what the surface reaches — a Sofia page
   * opens near 10 px/km and a reader looking for Бояна has zoomed well past
   * that — rather than from a number that sounds right. */
  var QUARTER_PX_KM = 45;
  /* 8 px/km: a Sofia rayon is ~4-8 km across, so its outline is 30-60 px wide —
   * enough to read as a division rather than as a scribble. It sits well below
   * the street gate on purpose: districts answer WHERE in a city you are, which
   * is the question that arrives first, and they are 35 shapes against ~4 800
   * street segments.
   *
   * The number was 12 and that was one notch too strict: the София-град page
   * opens at 9.7 px/km, so the districts appeared only after the reader zoomed
   * — on the one province where the whole province IS the city, and where the
   * divisions were asked for. A gate that hides a layer on its primary subject
   * is set wrong. Larger provinces open at 4-6 px/km and still show none. */
  var DISTRICTS_PX_KM = 8;
  function keyOf(frame) { return frame.getAttribute('data-od-id'); }
  /* Create-on-demand, and derivable from the frame id alone, because the zoom
   * controls read the view when they mount — which is BEFORE the first draw.
   * An earlier build only created it inside the draw pass, so the buttons
   * painted no accessible name and left every disabled state unset: three
   * controls that looked live and could not say what they would do. */
  function ensure(id) {
    /* `dx`/`dy` are the reader's pan, held in BASE projection units — the
     * country's own untransformed space — not in screen pixels. Screen pixels
     * would mean a different distance at every zoom, so a pan made at 4× would
     * jump when the reader zoomed out. In base units the offset is a place,
     * and it survives every scale change unchanged.
     *
     * `vk` is written back by the draw pass, because a pointer delta arrives
     * in screen pixels and only the renderer knows what one pixel is currently
     * worth. Keeping it here means the pointer code never re-derives the
     * scale and cannot disagree with what was drawn. */
    if (!VIEWS[id]) VIEWS[id] = {
      mode: id === 'area-map' ? 'province' : 'country', k: 1, dx: 0, dy: 0, vk: 1
    };
    return VIEWS[id];
  }
  function viewOf(frame) { return ensure(keyOf(frame)); }
  /* The floor is not 1 on a province view.
   *
   * `k` is relative to whatever the current framing fits, and a province fits
   * at 2,5–6,6× the country. Clamping k at 1 therefore made the province's own
   * fit the furthest you could get, so the first press of MINUS had nowhere to
   * go but straight out to the whole country — one step from a city to the
   * Balkans, which is not a zoom, it is a teleport.
   *
   * `fit` is what the province fit actually measured on the last draw, so
   * k may fall to 1/fit: the point at which the province view has scaled all
   * the way down to country scale and the two framings agree. Only there does
   * the mode change, and the picture does not jump when it does. */
  /* The country fit stopped being the floor when the basemap and the
   * cross-border readings arrived. It was the right floor while the map was a
   * Bulgaria-only choropleth: below it there was literally nothing drawn, so
   * out was correctly disabled at the default view and said so.
   *
   * Now the frame already carries the real neighbours, the tile basemap runs
   * past the border, and the hexes include readings outside Bulgaria — the
   * question "is it worse on the other side" has an answer on screen. A reader
   * who opens the home map and presses minus should get to see it.
   *
   * 0.55 is not a taste: the context window this map draws is lon 19,5–31,5,
   * 12° wide, and the country fit spans about 6,6° of it. 6,6/12 = 0,55, so
   * this is exactly the scale at which the drawn context fills the frame and
   * not one step further. Past it the map would be Europe with a
   * Bulgaria-shaped dataset on it, which says nothing. */
  var CMIN = 0.55;
  function minK(v) {
    return v.mode === 'province' && v.fit ? Math.min(CMIN, 1 / v.fit) : CMIN;
  }
  /* Published so the buttons state the limit they actually have. map-zoom.js
   * used to compare against a hardcoded 1, which is a second copy of the floor
   * — and it is what left minus disabled on the default view after the floor
   * moved. One owner. */
  window.AIRBG_MAP_MINK = minK;
  function clamp(z, v) { return Math.min(ZMAX, Math.max(v ? minK(v) : ZMIN, z)); }
  function repaint() {
    if (window.AIRBG_DATA) document.dispatchEvent(
      new CustomEvent('airbg:viewchange', { detail: VIEWS }));
  }
  /* The one way in for every control that moves a map. A second copy of this
   * arithmetic in the button handler is a second thing free to disagree with
   * what the renderer actually draws. */
  window.AIRBG_MAP_VIEW = function (id, action) {
    var v = ensure(id);
    if (action === 'in')      v.k = clamp(v.k * ZSTEP, v);
    else if (action === 'out') {
      /* Out is always one step, and the step is the same size everywhere.
       * The mode flips only once the province view has already scaled down to
       * country scale, so the frame the reader is looking at does not change
       * size at the moment the subject does. */
      var next = v.k / ZSTEP;
      if (v.mode === 'province' && next <= minK(v) + 1e-6) {
        v.mode = 'country'; v.k = 1; v.dx = v.dy = 0;
      } else v.k = clamp(next, v);
    }
    /* Returning to a framing resets the pan too. "Whole country" that left
     * the reader's old offset applied would return to a country they had
     * dragged half out of the frame — the way back has to actually be the way
     * back (§5.4). */
    else if (action === 'country') { v.mode = 'country'; v.k = 1; v.dx = v.dy = 0; }
    else if (action === 'province') { v.mode = 'province'; v.k = 1; v.dx = v.dy = 0; }
    repaint();
    return v;
  };
  window.AIRBG_MAP_STATE = function (id) { return ensure(id); };

  /* Pan by a screen-pixel delta. Converted to base units through the scale the
   * last draw actually used, so a drag moves the map exactly as far as the
   * pointer moved, at any zoom. */
  window.AIRBG_MAP_PAN = function (id, ddx, ddy, commit) {
    var v = ensure(id);
    v.dx -= ddx / (v.vk || 1);
    v.dy -= ddy / (v.vk || 1);
    if (commit !== false) repaint();
    return v;
  };
  /* Zoom about a point rather than about the centre, so the place under the
   * pointer stays under the pointer. Zooming to the centre while the reader
   * is looking at a corner walks the subject out from under them. */
  window.AIRBG_MAP_ZOOM_AT = function (id, factor, sx, sy) {
    var v = ensure(id), before = v.k;
    v.k = clamp(v.k * factor, v);
    var got = v.k / before;                     // what the clamp actually allowed
    if (got === 1) { repaint(); return v; }
    // The offset that keeps (sx, sy) fixed while the scale changes by `got`.
    v.dx += (sx / (v.vk || 1)) * (1 - 1 / got);
    v.dy += (sy / (v.vk || 1)) * (1 - 1 / got);
    repaint();
    return v;
  };

  /* Text on a coloured field has to stay legible on all six bands, and the
   * bands are served — so the ink is chosen from the fill's own luminance
   * rather than fixed in CSS. Both candidates are palette tokens (§2.2). */
  function inkFor(hex) {
    var c = hex.replace('#', '');
    var r = parseInt(c.substr(0, 2), 16) / 255,
        g = parseInt(c.substr(2, 2), 16) / 255,
        b = parseInt(c.substr(4, 2), 16) / 255;
    var f = function (u) { return u <= 0.03928 ? u / 12.92 : Math.pow((u + 0.055) / 1.055, 2.4); };
    var L = 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    return (L + 0.05) / 0.05 >= 4.5 ? '#161616' : '#ffffff';
  }

  function ready(outline, provinces, neighbours, rivers, data, roads, streets, districts, quarters, hexes) {
    var ring = outline.ring, i;
    var lat0 = 0;
    for (i = 0; i < ring.length; i++) lat0 += ring[i][1];
    var kx = Math.cos((lat0 / ring.length) * Math.PI / 180);
    var mnx = 1e9, mxx = -1e9, mny = 1e9, mxy = -1e9;
    for (i = 0; i < ring.length; i++) {
      var px = ring[i][0] * kx, py = ring[i][1];
      if (px < mnx) mnx = px; if (px > mxx) mxx = px;
      if (py < mny) mny = py; if (py > mxy) mxy = py;
    }
    var s = Math.min((W - 2 * PAD) / (mxx - mnx), (H - 2 * PAD) / (mxy - mny));
    var ox = (W - (mxx - mnx) * s) / 2, oy = (H - (mxy - mny) * s) / 2;
    /* One projection for the country, then a per-frame VIEW on top of it.
     *
     * The province page used to draw the whole country and mark one shape —
     * which answers "where is Смолян in Bulgaria" when the reader has already
     * chosen Смолян and is asking what its air is doing. A detail page should
     * open on its own subject. Rather than a second projection (which would
     * validate a framing the country map never uses, §5.2), the same fit is
     * scaled and re-centred: identical geometry, different window onto it.
     *
     * These three are set once per frame, immediately before that frame draws,
     * and every X/Y in the pass reads them — so labels, markers and hit areas
     * all land in the same space without any of them knowing a view exists. */
    var vk = 1, vcx = W / 2, vcy = H / 2, vh = H;
    function bx(lon) { return ox + (lon * kx - mnx) * s; }   // base, country fit
    function by(lat) { return H - oy - (lat - mny) * s; }
    /* THE PROJECTION SEAM (path A).
     *
     * When the vector basemap is mounted, MapLibre owns the camera and this
     * pass must land on top of it — so X/Y delegate to the tile map's own
     * projection instead of the country fit. Web Mercator is separable while
     * bearing and pitch are zero (map-tiles.js locks both), so screen x is a
     * function of longitude alone and screen y of latitude alone, and these
     * two signatures still hold. Every shape, label, marker and hit area in
     * the pass reads X/Y, so none of them needs to know which camera is
     * driving. */
    function X(lon) {
      var p = window.AIRBG_MAP_PROJECT;
      return p ? p.x(lon) : (bx(lon) - vcx) * vk + W / 2;
    }
    function Y(lat) {
      var p = window.AIRBG_MAP_PROJECT;
      return p ? p.y(lat) : (by(lat) - vcy) * vk + vh / 2;
    }
    /* True when the tiles are drawing the basemap for the frame being drawn
     * right now. `curFrame` is set at the top of the per-frame loop below:
     * X/Y and this helper live in the pass's shared scope, one level above
     * the loop, so they cannot close over the loop's own `frame` — the first
     * version did and threw `ReferenceError: frame is not defined` on every
     * tiled draw, which the catch reported as an empty message. */
    /* Decided once per pass, before the provinces paint: whether the hex
     * layer has anything to draw. Two layers each deciding this independently
     * is two layers free to disagree — the seam this file already uses for
     * the camera and the metric. */
    var hexesWillDraw = false;
    var curFrame = null;
    function tiled() {
      return !!(window.AIRBG_MAP_PROJECT &&
                window.AIRBG_MAP_PROJECT.frame === curFrame);
    }

    /* The metric decides both the value read and the scale it is banded
     * against — the two must move together, because the PM10 edges are not
     * the PM2.5 edges. */
    var metric = window.AIRBG_METRIC ? window.AIRBG_METRIC() : 'p2';
    var bands = (metric === 'p10' ? data.scale_p1_eaqi : data.scale_p2_eaqi) || data.scale_p2_eaqi;
    function valueOf(o) { return metric === 'p10' ? o.p10 : o.p2; }
    function band(v) {
      if (v == null) return null;
      for (var i = 0; i < bands.length; i++) {
        if (bands[i].upper == null || v <= bands[i].upper) return bands[i];
      }
      return bands[bands.length - 1];
    }
    /* ---- The progressive ramp ------------------------------------------
     * Requested: a continuous scale rather than six steps. The risk it carries
     * is the one this document warns about most — a smooth KEY over a banded
     * MAP shows colours nothing on screen uses, and the key looks right while
     * being wrong. So the interpolation lives here, in one function, and both
     * the fills and the legend gradient are built from it. They cannot disagree
     * because there is only one of them.
     *
     * The stops are still the SERVED colours (§2.1) — nothing is invented at
     * the ends. What is new is that a value between two breaks now lands
     * between their two colours instead of snapping to one.
     *
     * Position is by BAND INDEX, not by raw value: 0–5 and 50+ on one linear
     * axis makes the bands people actually sit in invisible. Equal segments
     * per band, interpolating inside each — which is also exactly how the
     * legend bar is laid out, so the two share a mapping as well as a palette. */
    function hex2rgb(h) {
      h = String(h).replace('#', '');
      if (h.length === 3) h = h[0]+h[0]+h[1]+h[1]+h[2]+h[2];
      var n = parseInt(h, 16);
      return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
    }
    /* Mixed in OKLab, not sRGB. A straight sRGB lerp between the two dark ends
     * (#960032 -> #7d2181) passes through a muddy grey that is in neither band;
     * OKLab keeps the path perceptually straight. */
    function srgb2lin(c) { c /= 255; return c <= 0.04045 ? c/12.92 : Math.pow((c+0.055)/1.055, 2.4); }
    function lin2srgb(c) { c = c <= 0.0031308 ? c*12.92 : 1.055*Math.pow(c, 1/2.4)-0.055;
      return Math.max(0, Math.min(255, Math.round(c*255))); }
    function rgb2oklab(rgb) {
      var r = srgb2lin(rgb[0]), g = srgb2lin(rgb[1]), b = srgb2lin(rgb[2]);
      var l = Math.cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b);
      var m = Math.cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b);
      var s2 = Math.cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b);
      return [0.2104542553*l + 0.7936177850*m - 0.0040720468*s2,
              1.9779984951*l - 2.4285922050*m + 0.4505937099*s2,
              0.0259040371*l + 0.7827717662*m - 0.8086757660*s2];
    }
    function oklab2rgb(L) {
      var l = Math.pow(L[0] + 0.3963377774*L[1] + 0.2158037573*L[2], 3);
      var m = Math.pow(L[0] - 0.1055613458*L[1] - 0.0638541728*L[2], 3);
      var s2 = Math.pow(L[0] - 0.0894841775*L[1] - 1.2914855480*L[2], 3);
      return [lin2srgb( 4.0767416621*l - 3.3077115913*m + 0.2309699292*s2),
              lin2srgb(-1.2684380046*l + 2.6097574011*m - 0.3413193965*s2),
              lin2srgb(-0.0041960863*l - 0.7034186147*m + 1.7076147010*s2)];
    }
    function mix(a, b, t) {
      var A = rgb2oklab(hex2rgb(a)), B = rgb2oklab(hex2rgb(b));
      var c = oklab2rgb([A[0]+(B[0]-A[0])*t, A[1]+(B[1]-A[1])*t, A[2]+(B[2]-A[2])*t]);
      return 'rgb(' + c[0] + ',' + c[1] + ',' + c[2] + ')';
    }
    /* ---- The reference ramp --------------------------------------------
     * Measured off the supplied reference (image-10.png) rather than guessed:
     * the bar was sampled every 5 % of its height and its axis read off its own
     * ticks. Two facts came out of it, and both matter.
     *
     * 1. The axis is PIECEWISE, not linear. 0–100 µg/m³ occupies the lower
     *    ~79 % of the bar and 100–500 the top ~21 %. That is what lets one key
     *    serve both an ordinary day and a wildfire without the useful range
     *    collapsing into a sliver.
     * 2. The colours run teal → green → amber → orange → red → magenta, with
     *    red HELD across roughly 63–88 before it turns.
     *
     * These stops are values in µg/m³, not band indices, so the colour a
     * reading gets no longer depends on how many bands the scale happens to
     * have. */
    var KNEE = 100, KNEE_AT = 0.794, TOP = 500;
    var RAMP = [
      [0, '#00796b'], [13, '#37835b'], [19, '#dfa32d'], [25, '#f4921c'],
      [32, '#ef7811'], [38, '#e95d05'], [44, '#e54b00'], [50, '#e13e00'],
      [57, '#de3300'], [63, '#dd2c00'], [88, '#dd2c00'], [95, '#c11d2f'],
      [112, '#8f027f'], [208, '#8c0084'], [500, '#8c0084']
    ];
    var RAMP_TICKS = [0, 25, 50, 75, 100, 500];

    /* Where a value sits on the bar, 0..1, on the reference's own axis. */
    function rampT(v) {
      if (v == null) return null;
      if (v <= 0) return 0;
      if (v >= TOP) return 1;
      return v <= KNEE ? (v / KNEE) * KNEE_AT
                       : KNEE_AT + ((v - KNEE) / (TOP - KNEE)) * (1 - KNEE_AT);
    }
    /* Interpolated BY VALUE between the two stops that bracket it. */
    function rampColour(v) {
      if (v == null) return null;
      if (v <= RAMP[0][0]) return RAMP[0][1];
      for (var i = 0; i < RAMP.length - 1; i++) {
        if (v <= RAMP[i + 1][0]) {
          var lo = RAMP[i][0], hi = RAMP[i + 1][0];
          return mix(RAMP[i][1], RAMP[i + 1][1], hi === lo ? 0 : (v - lo) / (hi - lo));
        }
      }
      return RAMP[RAMP.length - 1][1];
    }
    /* Published so the legend bar is built from the SAME stops the map paints
     * with. A second gradient written in CSS would be a second answer. */
    window.AIRBG_RAMP = function () {
      return {
        /* Each stop carries its own POSITION now. Handing the legend a bare
         * list of colours made it space them evenly, which on a piecewise axis
         * is a different gradient from the one the map paints — the exact
         * key-disagrees-with-map defect this seam exists to prevent. */
        stops: RAMP.map(function (s) {
          return { colour: s[1], pos: rampT(s[0]), value: s[0] };
        }),
        ticks: RAMP_TICKS.map(function (v) { return { value: v, pos: rampT(v) }; }),
        unit: 'µg/m³',
        colourFor: rampColour, positionFor: rampT
      };
    };
    /* And SAY that it exists. The legend paints from this, and it was painting
     * on airbg:datachange — which fires when the data lands, before the render
     * pass that defines the ramp has run. So the bar asked for a ramp that did
     * not exist yet, got nothing, and silently stayed on the six-block
     * fallback: the map interpolated and the key did not.
     *
     * Same shape as i18n.js loading after the component that reads it. The fix
     * is a seam, not a retry: whoever owns the value announces it. */
    document.dispatchEvent(new CustomEvent('airbg:rampchange'));

    function bandName(b) {
      if (!b) return t('legend.none');
      return lang() === 'en' ? b.label : b.label_bg;
    }

    // Ring in screen space, plus the area and centroid the label needs.
    function project(r) {
      var pts = r.map(function (c) { return [X(c[0]), Y(c[1])]; });
      var a = 0, cx = 0, cy = 0;
      for (var i = 0, n = pts.length; i < n; i++) {
        var p = pts[i], q = pts[(i + 1) % n];
        var f = p[0] * q[1] - q[0] * p[1];
        a += f; cx += (p[0] + q[0]) * f; cy += (p[1] + q[1]) * f;
      }
      a /= 2;
      return { pts: pts, area: Math.abs(a),
               c: Math.abs(a) < 1e-6 ? pts[0] : [cx / (6 * a), cy / (6 * a)] };
    }
    // Ray casting: is a candidate label point actually inside its own province?
    function inside(pt, pts) {
      var x = pt[0], y = pt[1], on = false;
      for (var i = 0, j = pts.length - 1; i < pts.length; j = i++) {
        var xi = pts[i][0], yi = pts[i][1], xj = pts[j][0], yj = pts[j][1];
        if (((yi > y) !== (yj > y)) && (x < (xj - xi) * (y - yi) / (yj - yi) + xi)) on = !on;
      }
      return on;
    }

    /* София-град sits inside Софийска, so both centroids land in almost the same
     * place and their labels collided — the enclosed province's value printed
     * over its neighbour's. The larger province yields: its label walks outward
     * in eight directions until it finds a spot that is still inside its own
     * shape and clear of every label already placed. Whoever encloses has room
     * to move; whoever is enclosed does not. */
    var GAP = 3;                        // clear space every label keeps around it
    function placeLabel(ring, taken, halfW, halfH, dense, spill) {
      /* `dense` doubles the directions and halves the step. The coarse walk is
       * right for a label that wants to sit near the centroid; a name that has
       * already given up that spot needs to search the whole shape, and eight
       * directions at a sixth of the width step straight over the gaps that
       * were left. Велико Търново, Стара Загора and София-град were all
       * findable — the search was simply too coarse to find them. */
      var step = Math.sqrt(ring.area) / (dense ? 12 : 6), dirs = dense ? 16 : 8;
      var rings = dense ? 12 : 6;
      for (var r = 0; r <= rings; r++) {
        for (var k = 0; k < (r ? dirs : 1); k++) {
          var a = (k / dirs) * Math.PI * 2;
          var p = [ring.c[0] + Math.cos(a) * step * r, ring.c[1] + Math.sin(a) * step * r];
          // Both ends of the widest line have to be inside, not just the anchor
          // point: a centred name whose tail hangs into the next province is
          // the overlap this test exists to prevent.
          if (!inside([p[0] - halfW, p[1] - halfH], ring.pts) ||
              !inside([p[0] + halfW, p[1] - halfH], ring.pts)) continue;
          /* `spill` is the last resort, and it relaxes exactly one thing: the
           * name may hang below the province while the reading stays on it.
           * София-град is the case it exists for — the smallest of the 28, and
           * no size in the ramp fits a two-line pair inside it. The pair stays
           * together and stays contiguous, so nothing about it reads as a
           * label belonging to the province underneath; what it gives up is
           * the guarantee that every glyph sits on its own territory, and that
           * is the lesser loss against a province with no name at all. It is
           * still bounded: inside the frame, and clear of every label already
           * placed. */
          if (!spill &&
              (!inside([p[0] - halfW, p[1] + halfH], ring.pts) ||
               !inside([p[0] + halfW, p[1] + halfH], ring.pts))) continue;
          if (spill && (p[0] - halfW < PAD || p[0] + halfW > W - PAD ||
                        p[1] - halfH < PAD || p[1] + halfH > vh - PAD)) continue;
          /* GAP, not zero. Two boxes that merely fail to intersect still print
           * as one word: *Veliko* ended and *Targovishte* began 2px later, and
           * the reader saw "VelikoTargovishte" on a map the overlap test had
           * passed. Non-overlapping is the arithmetic condition; legible is the
           * design one, and only the second is what a label is for. */
          var clash = taken.some(function (q) {
            return Math.abs(q[0] - p[0]) < q[2] + halfW + GAP &&
                   Math.abs(q[1] - p[1]) < q[3] + halfH + GAP;
          });
          if (!clash) return p;
        }
      }
      return null;                       // no honest spot: leave it to the tooltip
    }

    /* Text width without measuring it. `getComputedTextLength` needs the node
     * to be laid out, and the labels are placed before anything is in the
     * document — so this estimates from the glyph advance of IBM Plex Sans at
     * caption size and errs wide. Erring wide costs a name that could have
     * fitted; erring narrow costs an overlap, which is the defect. */
    var CAP = 12;                                   // caption size (§3)
    /* Province names get a small ramp below the caption size — 12 / 11 / 10 / 9.
     * §3's scale is written for running copy; a map label is a different job,
     * and the alternative to a 10px name here is no name at all or a leader
     * line dragged across three provinces. 9px is the floor: below that the
     * Cyrillic descenders close up at this weight. */
    var NAME_SIZES = [12, 11, 10, 9];

    /* A long name wraps rather than shrinking further or leaving the shape.
     * *Велико Търново* is 14 characters — 76px even at 10px — but split at its
     * own space it is two ~40px lines, which fits a province that was never
     * short of vertical room. Only a space or a hyphen may break, so the split
     * is always at a real word boundary; a name with neither stays one line. */
    // The box has to hold either language, so it is sized by the wider.
    function widestOf(strings, size) {
      return strings.reduce(function (m, t) {
        return Math.max(m, widthOf(t, false, size));
      }, 0);
    }
    function wrapName(name) {
      var at = name.lastIndexOf(' ');
      if (at > 0) return [name.slice(0, at), name.slice(at + 1)];
      at = name.indexOf('-');
      if (at > 0) return [name.slice(0, at + 1), name.slice(at + 1)];
      return null;
    }
    /* Width is per CHARACTER, not per string, because this map is set in two
     * scripts. A single constant was wrong in both directions: 0.54 em is a
     * Latin figure and under-read Cyrillic — Търговище estimates 48px at 10px
     * and sets nearer 62px, so Велико printed into it. Raising the constant to
     * 0.62 fixed Bulgarian and broke English, where every Latin name was then
     * over-measured by ~15% and dropped for want of room it did not need:
     * Targovishte, the same province, vanished from the English map.
     *
     * So the factor follows the glyph. Digits stay at 0.58 in either language;
     * tabular figures make that exact by construction. */
    function emOf(ch) {
      var c = ch.charCodeAt(0);
      return (c >= 0x0400 && c <= 0x04ff) ? 0.62 : 0.54;   // Cyrillic block
    }
    function widthOf(s, weight, size) {
      var em = size || CAP;
      if (weight) return s.length * em * 0.58;             // measured values
      var w = 0;
      for (var i = 0; i < s.length; i++) w += emOf(s[i]);
      return w * em;
    }

    // An open path: a river is a line, not a ring, so it never closes.
    function line(pts) {
      var s = '';
      for (var i = 0; i < pts.length; i++) {
        s += (i ? 'L' : 'M') + pts[i][0].toFixed(1) + ' ' + pts[i][1].toFixed(1);
      }
      return s;
    }

    function d(pts) {
      var s = '';
      for (var i = 0; i < pts.length; i++) {
        s += (i ? 'L' : 'M') + pts[i][0].toFixed(1) + ' ' + pts[i][1].toFixed(1);
      }
      return s + 'Z';
    }

    frames.forEach(function (frame) {
      curFrame = frame;                 // which frame `tiled()` is asking about
      var canvas = frame.querySelector('.map-canvas');
      if (!canvas) return;
      var focus = frame.getAttribute('data-focus-oblast');
      if (!focus && frame.getAttribute('data-od-id') === 'area-map') {
        var h1 = document.querySelector('[data-oblast]');
        focus = h1 && h1.getAttribute('data-oblast');
      }

      /* Set the view BEFORE anything projects. Everything downstream — shapes,
       * labels, markers, hit areas — reads vk/vcx/vcy through X/Y, so the
       * whole pass agrees about the window without being told about it. */
      var view = viewOf(frame);
      /* Written before the height is read, not after: the CSS aspect is keyed
       * off this attribute, so setting it at the end of the pass would measure
       * the old window and leave the first paint a frame behind. */
      frame.setAttribute('data-view', view.mode + '@' + view.k.toFixed(2));
      /* The window's height is whatever shape the frame ACTUALLY is.
       *
       * `--map-view-h` still sets the default box in CSS, and it is read first
       * so a frame that cannot be measured — jsdom, `display:none`, a detached
       * node — still draws at the intended shape. But once the reader drags the
       * frame taller, CSS no longer knows its height; only the box does. So the
       * measured aspect wins whenever there is one, and the drawing follows the
       * container instead of a number that has gone stale.
       *
       * Clamped at `H`, never below. VH is the viewBox height against a fixed
       * 926 width, so a shorter box means a WIDER aspect — and §1 is explicit
       * that wider than 926∶382 crops the country top and bottom while taller
       * is always safe. Below the floor the SVG letterboxes rather than losing
       * Bulgaria, which is the correct direction to fail in. */
      /* Set before anything paints: the province pass reads it, and the hex
       * pass below re-derives the same condition. Both must agree or the map
       * shows a choropleth under hexes, or neither. */
      /* Must stay identical to the draw pass's own condition below — the
       * comment above is not decoration, the two disagreeing is what puts a
       * choropleth underneath the hexes. tiled() is gone from BOTH. */
      hexesWillDraw = !!(hexes && hexes.hexes && hexes.hexes.length);

      var VH = H;
      try {
        var cssH = parseFloat(getComputedStyle(frame).getPropertyValue('--map-view-h'));
        if (cssH > 0) VH = cssH;
      } catch (err) { /* no computed style (jsdom, detached): the fit stands */ }
      try {
        var box = canvas.getBoundingClientRect();
        if (box.width > 0 && box.height > 0) {
          // Rounded: an unbounded float in a viewBox attribute is noise.
          VH = Math.round(Math.max(H, W * (box.height / box.width)) * 10) / 10;
        }
      } catch (err) { /* unmeasurable: the CSS default stands */ }
      vk = 1; vcx = W / 2; vcy = VH / 2; vh = VH;       // country, identity
      if (view.mode === 'province' && focus && provinces.provinces) {
        var fen = (window.AIRBG_OBLAST_EN || {})[focus] || focus;
        var pr = provinces.provinces[fen] ||
                 provinces.provinces[provinces._alias && provinces._alias[fen]];
        if (pr) {
          var x0 = 1e9, y0 = 1e9, x1 = -1e9, y1 = -1e9;
          /* The same box in lon/lat, kept alongside the projected one. Under
           * the tiles the fit below is computed and then thrown away — the
           * camera decides where every coordinate lands — so the framing has
           * to be handed to the camera in ITS units, or a province page opens
           * on the whole country with the subject somewhere inside it. */
          var lo0 = 1e9, la0 = 1e9, lo1 = -1e9, la1 = -1e9;
          pr.rings.forEach(function (r) {
            r.forEach(function (c) {
              var px = bx(c[0]), py = by(c[1]);
              if (px < x0) x0 = px; if (px > x1) x1 = px;
              if (py < y0) y0 = py; if (py > y1) y1 = py;
              if (c[0] < lo0) lo0 = c[0]; if (c[0] > lo1) lo1 = c[0];
              if (c[1] < la0) la0 = c[1]; if (c[1] > la1) la1 = c[1];
            });
          });
          /* Published, not applied: the renderer never moves the camera. The
           * key is the subject, so the camera re-fits when the province
           * changes and never fights a zoom the reader set themselves. */
          frame.__airbgWantBounds = [[lo0, la0], [lo1, la1]];
          frame.__airbgWantKey = 'province:' + fen;
          // Fit the province the same way the country is fitted, then apply
          // whatever the reader has zoomed since.
          var bw = Math.max(x1 - x0, 1), bh = Math.max(y1 - y0, 1);
          vk = Math.min((W - 2 * PAD * 2) / bw, (VH - 2 * PAD * 2) / bh);
          vcx = (x0 + x1) / 2; vcy = (y0 + y1) / 2;
        } else {
          view.mode = 'country';                        // unknown province: no invented framing
        }
      }
      if (view.mode !== 'province') {
        frame.__airbgWantBounds = null;
        frame.__airbgWantKey = 'country';
      }
      view.fit = vk;              // the framing's own scale, before the reader's zoom
      vk *= view.k;
      /* The pan is applied after the fit, in base units, so it is independent
       * of both the framing and the zoom. */
      vcx += (view.dx || 0); vcy += (view.dy || 0);

      /* A pan cannot strand the reader in empty space. The window may leave the
       * country, but not entirely: its centre stays inside the country's own
       * bounds grown by half a window, so at least the edge of Bulgaria is
       * always on screen. Dragging past that stops rather than rubber-banding —
       * a hard stop tells the reader they have reached the end; a spring
       * suggests there is more out there.
       *
       * The clamped value is written BACK to the view. Without that, the state
       * keeps accumulating a pan the drawing refuses to honour, and the map sits
       * still for the first second of the next drag back. */
      var half = (W / vk) / 2, halfV = (vh / vk) / 2;
      var cx0 = 1e9, cy0 = 1e9, cx1 = -1e9, cy1 = -1e9;
      for (i = 0; i < ring.length; i++) {
        var rx = bx(ring[i][0]), ry = by(ring[i][1]);
        if (rx < cx0) cx0 = rx; if (rx > cx1) cx1 = rx;
        if (ry < cy0) cy0 = ry; if (ry > cy1) cy1 = ry;
      }
      var wantX = vcx, wantY = vcy;
      vcx = Math.min(cx1 + half, Math.max(cx0 - half, vcx));
      vcy = Math.min(cy1 + halfV, Math.max(cy0 - halfV, vcy));
      view.dx += vcx - wantX; view.dy += vcy - wantY;

      view.vk = vk;                    // what one screen pixel is worth, for the pointer

      /* Under the tiles the SVG is an overlay in SCREEN pixels, not a fitted
       * country: MapLibre has already decided where every coordinate lands,
       * so a viewBox of anything but the real box would scale the data layer
       * away from the basemap under it. */
      var box = tiled() ? window.AIRBG_MAP_PROJECT.size : null;
      var svg = el('svg', {
        viewBox: box ? ('0 0 ' + box.w + ' ' + box.h) : ('0 0 ' + W + ' ' + vh),
        class: 'map-svg' + (box ? ' map-svg--overlay' : ''),
        preserveAspectRatio: box ? 'none' : 'xMidYMid meet', role: 'img'
      });

      /* Declared here, above the context layer that fills it. It was declared
       * beside the province counters lower down, and `var` hoisting turned
       * that into a silent `undefined.push` — the whole preview failed with
       * one line in the console and an empty frame. Declare where it is first
       * used, not where it is first convenient. */
      var ctxNames = [];

      /* The neighbours first, under everything.
       *
       * Bulgaria alone on an empty field reads as a cut-out rather than as a
       * place: a reader cannot tell coast from land border, or which way is
       * Greece. So the frame is filled with its actual surroundings — Romania,
       * Serbia, North Macedonia, Greece, Turkey and the rest — at the same
       * projection, and whatever is not land is the Black Sea.
       *
       * They are context, not data: no reading, no ramp colour, half opacity,
       * so nothing here can be mistaken for a measurement (§2.1). The country
       * fit is untouched (§1) — the neighbours simply run off the edges of the
       * viewBox, exactly as they do on the app's own map at this zoom. */
      /* The context layer — sea, foreign land, the Danube, their names — is
       * exactly what the archive's water/landcover/boundary/place layers
       * already draw, in the same projection and better. Drawing both would
       * be two basemaps arguing. */
      if (!tiled() && neighbours) {
        var ctx = el('g', { class: 'map-context' });
        // Water first, the whole frame. Everything that is not drawn as land
        // afterwards is sea, which is what a basemap means by the word.
        ctx.appendChild(el('rect', { class: 'map-context__sea', x: 0, y: 0, width: W, height: vh }));
        var drawn = 0, onscreen = [], allLand = [], riverPts = [];
        Object.keys(neighbours.countries).forEach(function (k) {
          var c = neighbours.countries[k];
          c.rings.forEach(function (r) {
            var pts = r.map(function (c) { return [X(c[0]), Y(c[1])]; });
            // Anything wholly off-frame is not drawn: it would cost DOM and
            // paint nothing.
            var mnx = 1e9, mxx = -1e9, mny = 1e9, mxy = -1e9;
            for (var i = 0; i < pts.length; i++) {
              if (pts[i][0] < mnx) mnx = pts[i][0];
              if (pts[i][0] > mxx) mxx = pts[i][0];
              if (pts[i][1] < mny) mny = pts[i][1];
              if (pts[i][1] > mxy) mxy = pts[i][1];
            }
            if (mxx < 0 || mnx > W || mxy < 0 || mny > vh) return;
            ctx.appendChild(el('path', { class: 'map-context__land', d: d(pts) }));
            allLand.push(pts);
            drawn++;
            if (c.borders_bg && c.name_bg) onscreen.push({ c: c, pts: pts });
          });
        });

        /* The Danube, over the land and under Bulgaria. It is the northern
         * border itself, so drawing it explains the shape of that edge — and a
         * blue line along a boundary is the one thing a reader already reads as
         * a river rather than as another administrative stroke. */
        var ctxNamed = 0;
        if (rivers) {
          rivers.rivers.forEach(function (r) {
            r.lines.forEach(function (seg) {
              var pts = seg.map(function (c) { return [X(c[0]), Y(c[1])]; });
              var vis = pts.filter(function (p) {
                return p[0] > -20 && p[0] < W + 20 && p[1] > -20 && p[1] < H + 20;
              });
              if (vis.length < 2) return;
              ctx.appendChild(el('path', { class: 'map-context__river', d: line(pts) }));
              riverPts = riverPts.concat(vis);
            });
          });
        }

        /* Where a context name goes.
         *
         * Only slivers of each neighbour are inside the frame, so a polygon
         * centroid is worthless here — Turkey's centroid is hundreds of pixels
         * off-screen. Instead the frame is sampled on a 12px grid, every cell
         * inside the country is marked, and the label takes the cell furthest
         * from any cell that is NOT the country. That is the widest visible
         * part of it, which is where an atlas would put the name too. */
        var STEP = 12, GW = Math.ceil(W / STEP), GH = Math.ceil(vh / STEP);

        /* Where a context name goes, and whether it goes at all.
         *
         * Only slivers of each neighbour are inside the frame, so a polygon
         * centroid is worthless here — Turkey's centroid is hundreds of pixels
         * off-screen. The frame is sampled on a 12px grid instead.
         *
         * The metric is the HORIZONTAL run of cells a candidate sits in, not
         * its distance to the nearest edge in any direction. A label is a
         * horizontal word: what it needs is room to its left and right, and a
         * cell deep inside a tall narrow strip has plenty of the second kind
         * of room and none of the first. Scoring by distance printed СЪРБИЯ
         * half off the top of the frame and cut the Я off СЕВЕРНА МАКЕДОНИЯ
         * against the Bulgarian border. */
        function placeContext(test, halfW) {
          var inside = new Uint8Array(GW * GH), x, y, i;
          for (y = 0; y < GH; y++) for (x = 0; x < GW; x++) {
            // The outermost ring of cells counts as unusable, so a name never
            // sits against the frame edge where its ascenders would clip.
            if (x === 0 || y === 0 || x === GW - 1 || y === GH - 1) continue;
            if (test((x + 0.5) * STEP, (y + 0.5) * STEP)) inside[y * GW + x] = 1;
          }
          var best = null, bestRun = 0;
          for (y = 1; y < GH - 1; y++) {
            x = 1;
            while (x < GW - 1) {
              if (!inside[y * GW + x]) { x++; continue; }
              var from = x;
              while (x < GW - 1 && inside[y * GW + x]) x++;
              var run = x - from;                       // cells, uninterrupted
              if (run > bestRun) { bestRun = run; best = [from + run / 2, y]; }
            }
          }
          // Half the run has to hold half the word, or the name is not drawn:
          // a clipped country name is worse than an unlabelled country.
          if (!best || (bestRun * STEP) / 2 < halfW) return null;
          return [best[0] * STEP, (best[1] + 0.5) * STEP];
        }

        onscreen.forEach(function (o) {
          var text = lang() === 'en' ? o.c.name_en : o.c.name_bg;
          // Uppercase with 0.6px tracking runs wider than the mixed-case
          // estimate used for province names, so it gets its own factor.
          var halfW = (text.length * (CAP * 0.62 + 0.6)) / 2;
          var at = placeContext(function (px, py) { return inside([px, py], o.pts); }, halfW);
          if (!at) return;
          var label = el('text', {
            class: 'map-context__name', x: at[0].toFixed(1), y: at[1].toFixed(1)
          });
          label.textContent = text;
          ctx.appendChild(label);
          ctxNames.push([at[0], at[1], halfW]);
          ctxNamed++;
        });

        // The sea is whatever is neither Bulgaria nor a neighbour, so its name
        // is placed by the same rule against the same grid.
        if (neighbours.sea) {
          var bgRing = project(ring).pts;
          var seaText = lang() === 'en' ? neighbours.sea.name_en : neighbours.sea.name_bg;
          var seaAt = placeContext(function (px, py) {
            if (inside([px, py], bgRing)) return false;
            for (var i = 0; i < allLand.length; i++) if (inside([px, py], allLand[i])) return false;
            return true;
          }, (seaText.length * CAP * 0.54) / 2);
          if (seaAt) {
            var sl = el('text', {
              class: 'map-context__name map-context__name--sea',
              x: seaAt[0].toFixed(1), y: seaAt[1].toFixed(1)
            });
            sl.textContent = seaText;
            ctx.appendChild(sl);
            ctxNames.push([seaAt[0], seaAt[1], (seaText.length * CAP * 0.54) / 2]);
            ctxNamed++;
          }
        }

        // The river names itself at its midpoint, just above the line.
        if (riverPts.length > 1 && rivers) {
          riverPts.sort(function (a, b) { return a[0] - b[0]; });
          var mid = riverPts[riverPts.length >> 1];
          var rl = el('text', {
            class: 'map-context__name map-context__name--river',
            x: mid[0].toFixed(1), y: (mid[1] - 6).toFixed(1)
          });
          rl.textContent = lang() === 'en' ? rivers.rivers[0].name_en : rivers.rivers[0].name_bg;
          ctx.appendChild(rl);
          ctxNames.push([mid[0], mid[1] - 6, (rl.textContent.length * CAP * 0.54) / 2]);
          ctxNamed++;
        }

        svg.appendChild(ctx);
        frame.setAttribute('data-context', drawn + ' rings, ' + ctxNamed + ' names');
      }

      /* Bulgaria's own landmass, filled, under everything domestic.
       *
       * It never needed one before: the province choropleth WAS the fill, so
       * the country was opaque by accident. Making provinces outline-only under
       * the hexes removed that without replacing it, and the sea backdrop
       * showed straight through — land and sea rendering the same blue, which
       * is exactly as wrong as it looks. Water and ground are the one
       * distinction a map cannot get away with blurring.
       *
       * Drawn from the same ring the national border uses, so the fill and the
       * outline can never disagree about where the country is. */
      svg.appendChild(el('path', { class: 'map-landmass', d: d(project(ring).pts) }));

      /* Draw the largest province first so a small one enclosed by it — София-град
       * inside Софийска — is never buried. Geography, not z-index guesswork. */
      var shapes = [];
      data.oblasti.forEach(function (o) {
        var p = provinces.provinces[o.name_en];
        if (!p) return;
        var rings = p.rings.map(project);
        shapes.push({ o: o, rings: rings, area: rings[0].area });
      });
      shapes.sort(function (a, b) { return b.area - a.area; });
      // Painted largest-first (above); labelled smallest-first, so an
      // enclosed province keeps its centroid and the encloser steps aside.
      var labelOrder = shapes.slice().sort(function (a, b) { return a.area - b.area; });

      var linked = true;   // every province links, except to the page you are on
      var reported = 0, labelled = 0, named = 0, silentLabelled = 0, taken = [];
      shapes.forEach(function (sh) {
        var b = band(valueOf(sh.o)), fill = rampColour(valueOf(sh.o));
        if (fill) reported++;
        sh.fill = fill;
        /* Over the tiles, a solid choropleth hides the streets the reader
         * zoomed in to see — and §2.1 forbids the obvious fix, dropping its
         * opacity, because that re-tints served data. So the fill is kept
         * where the choropleth IS the subject (the country view) and dropped
         * for an outline in the same band colour once the basemap is (city
         * scale). The reading does not leave the screen: the value label and
         * every marker still carry the served band. */
        /* Keyed on the FRAMING, not on a zoom number. A gate at zoom 9 was
         * set from what a reader reaches after zooming, and every province
         * page opens below it — Пловдив at 6.9, София-град at 8.0 — so the
         * archive was fetched, drawn, and then covered by 19 solid fills at
         * exactly the moment the page loaded. Third time this system has set a
         * threshold above what the surface can actually reach (the district
         * gate at 12, ZMAX at 8, this): set it from the state the page OPENS
         * in, not from the one it can be driven to. */
        /* The choropleth yields to the hexes wherever they draw. A province
         * fill and a hex layer are two answers to one question, and the
         * province is the weaker of the two: it paints 28 equal-weight
         * territories from evidence that ranges from 558 sensors to none.
         * The province keeps its outline, its name and its link — what it
         * gives up is the claim to a reading across ground nobody measured. */
        /* The hex clause no longer needs tiled() either: wherever the hexes
         * draw, the province must yield, basemap or not. The second clause
         * keeps it, because "the reader has zoomed in past the province" is a
         * statement about the tile camera and means nothing without one. */
        sh.outlineOnly = hexesWillDraw || tiled() &&
          (view.mode === 'province' || window.AIRBG_MAP_PROJECT.zoom >= 9);
        /* A province is a way into its own page from ANY map, so it is a real
         * `<a>` — focusable, middle-clickable, shown in the status bar on
         * hover, none of which a click handler on a `<path>` gives.
         *
         * The rule that a detail map "links nowhere" was too broad, and it is
         * narrowed here: what must not exist is a link from Пловдив to
         * Пловдив, which is the dead control. Every OTHER province on that
         * same map is a live destination — and once the reader can zoom out to
         * the country from a province page, refusing those links would strand
         * them on a map of 28 shapes where 27 do nothing. */
        var cls = 'map-area' + (sh.o.name_bg === focus ? ' map-area--focus' : '');
        var g;
        if (linked && sh.o.name_bg !== focus) {
          g = el('a', {
            class: cls + ' map-area--link',
            href: 'area-detail.html?oblast=' + encodeURIComponent(sh.o.name_bg)
          });
          g.setAttribute('aria-label', nameOf(sh.o.name_bg));
        } else {
          g = el('g', { class: cls });
        }
        sh.rings.forEach(function (r) {
          var path = el('path', {
            class: 'map-area__shape' + (sh.outlineOnly ? ' map-area__shape--outline' : ''),
            d: d(r.pts)
          });
          // Served colour, so it is an attribute here rather than CSS (§2.1).
          /* Outline mode moves the served colour from the fill to the stroke.
           * It is the same colour, at full strength, on a different property —
           * not a re-tint (§2.1). */
          if (fill && sh.outlineOnly) { path.setAttribute('stroke', fill); }
          else if (fill) { path.setAttribute('fill', fill); }
          else path.setAttribute('class', 'map-area__shape map-area__shape--none');
          g.appendChild(path);
        });
        var title = el('title');
        title.textContent = nameOf(sh.o.name_bg) + ' · ' +
          (valueOf(sh.o) == null ? t('legend.none') : num(valueOf(sh.o)) + ' µg/m³ · ' + bandName(b));
        g.appendChild(title);
        sh.g = g;
        svg.appendChild(g);
      });

      /* Labels are a second pass, smallest province first. Painting order is
       * about who covers whom; labelling order is about who has room to move —
       * they are different questions and had to stop sharing one loop. The
       * value goes inside its own province, or not at all: a number forced into
       * a sliver overlaps its neighbour, and the tooltip still has the reading. */
      /* Every label goes in one layer above every shape. They used to live
       * inside their own province's group, which meant a label was covered by
       * any province painted after it — Софийска's name disappeared under
       * София-град and Пловдив's under its eastern neighbour. Paint order is
       * settled for shapes by size (above); labels are not shapes and must not
       * take part in that ordering at all. */
      var labels = el('g', { class: 'map-labels' });
      /* ---- Hexagonal aggregates -------------------------------------
       * The province choropleth paints 28 equal-weight territories from
       * wildly unequal evidence: София-град carries 558 of the country's
       * 1055 sensors and seven provinces carry none at all, yet every one
       * of them is filled one served band. The colour implies a density of
       * measurement that does not exist.
       *
       * A hex is the honest mark at country scale: it covers only where
       * something was actually measured, and it is an AGGREGATE, so it
       * neither enumerates a sensor nor invents a reading for empty ground.
       * Drawn only over the real basemap — on the SVG fallback there is no
       * OSM underneath and a field of floating hexes explains nothing. */
      /* NOT gated on tiled() any more. The original rule was "hexes only over
       * the real basemap, because a field of floating hexes over nothing
       * explains nothing" — and that was wrong in the case that matters most:
       * wherever WebGL is unavailable, the reader lost the hexes AND the
       * basemap together and was left with the province choropleth, which is
       * the very mark the hexes exist to replace. A design preview pane is
       * exactly that case.
       *
       * The province outlines are enough context for a hex to mean something.
       * They are not OSM, but they carry the border, and a hex sitting inside a
       * named province still says "this much was measured here" — which is more
       * honest than a province painted one colour from two sensors. */
      if (hexes && hexes.hexes) {
        var hl = el('g', { class: 'map-hexes' });
        /* ---- Zoom-aware cell size --------------------------------------
         *
         * The served grid is one fixed resolution, so a cell is a fixed size
         * ON THE GROUND and therefore a shrinking size on screen as the reader
         * zooms out: at country zoom the cells collapse towards specks, and the
         * counts stop fitting.
         *
         * Merging fixes the zoomed-OUT half honestly. k neighbouring cells are
         * combined into one coarser cell whenever the served cell would draw
         * smaller than TARGET_PX, and the merged value is the count-weighted
         * mean of its parts — which is what an average over the larger area
         * actually is. Nothing is invented: a merged cell is a coarser
         * aggregate of aggregates, and it carries the SUM of the sensor counts,
         * so the number on it stays true.
         *
         * Zooming IN cannot be fixed here and this deliberately does not try.
         * Subdividing a 15 km bin would mean inventing where inside it the
         * readings came from. Finer cells have to be binned by the server from
         * data it has and we do not (see context/hex-zoom-proposal.md). */
        var TARGET_PX = 26;         /* a cell wide enough to hold two digits */
        var hk = window.AIRBG_MAP_PROJECT;
        /* One hex's radius in screen pixels, measured through the SAME
         * projection the marks use — never a constant. The bin is a distance
         * on the ground, so its size on screen is whatever the camera says. */
        var hcx = hexes.window ? (hexes.window.lon[0] + hexes.window.lon[1]) / 2 : 25.5;
        var kmDeg = 111.32 * Math.cos(hexes.lat_ref * Math.PI / 180);
        var pxPerKm = Math.abs(X(hcx + 1 / kmDeg) - X(hcx));
        /* Published so the FETCH can ask for a resolution suited to this
         * camera. Measured through the same projection the marks use, so the
         * request and the drawing can never disagree about what scale the
         * reader is at. */
        window.AIRBG_MAP_PXPERKM = function () { return pxPerKm; };
        window.AIRBG_MAP_BBOX = function () {
          var inv = window.AIRBG_MAP_UNPROJECT;
          if (!inv) return null;
          var a = inv(0, 0), b2 = inv(W, VH);
          if (!a || !b2) return null;
          return [Math.min(a[0], b2[0]), Math.min(a[1], b2[1]),
                  Math.max(a[0], b2[0]), Math.max(a[1], b2[1])];
        };
        /* CIRCUMRADIUS, derived from the centre spacing — not the bin size.
         *
         * `resolution_km` is the distance BETWEEN CENTRES (measured: median
         * nearest-neighbour spacing is 15.01 km on a 15 km grid). Drawing a
         * hexagon whose circumradius is that number makes it √3 × 15 = 25.98 km
         * flat-to-flat, so every cell overlapped its neighbours by 1.73× and
         * the map showed a pile of stacked hexagons with two sets of counts
         * showing through each other.
         *
         * For a hex grid, flat-to-flat = √3 · circumradius. Setting
         * r = spacing / √3 makes adjacent cells share an edge exactly: a
         * tessellation, which is the whole reason to use hexagons. */
        var hr = (hexes.bin_km * pxPerKm) / Math.sqrt(3);

        /* How many served cells wide the drawn cell has to be to reach
         * TARGET_PX. 1 means "draw the served grid as it came". */
        var merge = Math.max(1, Math.round(TARGET_PX / Math.max(hr, 0.001)));
        var cells = hexes.hexes;
        /* ALWAYS bin, even at merge = 1.
         *
         * The served centres are not a perfect lattice: nearest-neighbour
         * spacing runs 13.76–15.01 km on a grid declared as 15 km. Sizing every
         * cell from the declared spacing therefore overlaps the pairs that sit
         * closer than declared — measured 90 overlapping pairs at merge 1, up
         * to 5.1px of penetration, which is the pattern of dark rhombi at every
         * vertex the reader reported.
         *
         * Running the served grid through the same axial lattice snaps each
         * centre to a regular cell, so the drawn hexagons tessellate whatever
         * the upstream spacing did. Where two served cells fall in one lattice
         * cell they merge, counts sum, and the value stays count-weighted —
         * the same honest arithmetic as any other merge. */
        if (merge >= 1) {
          /* Bin the served centres onto a coarser grid whose spacing is
           * `merge` × the served spacing, keyed on the same axes the server
           * used, so a merged cell is always a whole number of served cells
           * and never straddles them. */
          /* A HEX lattice, not a square one.
           *
           * Rounding lon/lat to a rectangular grid and placing each merged cell
           * at its members' centroid produced centres that were neither evenly
           * spaced nor on any lattice, so the merged hexagons overlapped by up
           * to 3.08×. Hexagons do not tile a square grid, and a centroid is not
           * a lattice point.
           *
           * Axial coordinates for a FLAT-TOP hexagon — the orientation the draw
           * loop emits, vertices every 60° starting at 0°. Cube rounding picks
           * the nearest cell, and the centre is computed back FROM the lattice,
           * so merged cells tessellate by construction rather than by luck.
           * Work in kilometres so the maths is planar and isotropic. */
          var Rkm = (hexes.bin_km * merge) / Math.sqrt(3);   /* circumradius */
          function axial(lon, lat) {
            var x = lon * kmDeg, y = lat * 111.32;
            var q = (2 / 3 * x) / Rkm;
            var r = (-1 / 3 * x + Math.sqrt(3) / 3 * y) / Rkm;
            /* cube rounding: round all three, fix the one that moved most */
            var cx3 = q, cz3 = r, cy3 = -cx3 - cz3;
            var rx = Math.round(cx3), ry = Math.round(cy3), rz = Math.round(cz3);
            var dx = Math.abs(rx - cx3), dy = Math.abs(ry - cy3), dz = Math.abs(rz - cz3);
            if (dx > dy && dx > dz) rx = -ry - rz;
            else if (dy > dz) ry = -rx - rz;
            else rz = -rx - ry;
            return [rx, rz];
          }
          function centreOf(q, r) {
            var x = Rkm * 1.5 * q;
            var y = Rkm * Math.sqrt(3) * (r + q / 2);
            return { lon: x / kmDeg, lat: y / 111.32 };
          }
          var buckets = {};
          cells.forEach(function (h) {
            var ax = axial(h.lon, h.lat);
            var gx = ax[0], gy = ax[1];
            var key = gx + ':' + gy;
            var b = buckets[key];
            if (!b) {
              var c = centreOf(gx, gy);
              b = buckets[key] = { lon: c.lon, lat: c.lat, n: 0, wP1: 0, wP2: 0,
                                   nP1: 0, nP2: 0, bg: 0, parts: 0, country: h.country };
            }
            /* Count-weighted, because a cell holding 30 sensors should not be
             * averaged equally with one holding 1 — that would let a single
             * device outvote a city. */
            b.parts++; b.n += h.n;
            if (h.P1 != null) { b.wP1 += h.P1 * h.n; b.nP1 += h.n; }
            if (h.P2 != null) { b.wP2 += h.P2 * h.n; b.nP2 += h.n; }
            if (h.country === 'BG') b.bg += h.n;
          });
          cells = Object.keys(buckets).map(function (k) {
            var b = buckets[k];
            return {
              /* The LATTICE centre, not the members' centroid — a centroid
               * drifts off the grid and the tessellation stops closing. */
              lon: b.lon, lat: b.lat, n: b.n,
              P1: b.nP1 ? b.wP1 / b.nP1 : null,
              P2: b.nP2 ? b.wP2 / b.nP2 : null,
              /* A merged cell is thin only if the whole of it rests on one
               * sensor — merging must not launder a single reading into
               * something that looks corroborated. */
              thin: b.n === 1,
              country: b.bg * 2 >= b.n ? 'BG' : b.country
            };
          });
          /* The drawn radius is the LATTICE's circumradius in pixels, so the
           * mark and the grid it was snapped to are the same size by
           * construction rather than by a multiplication that has to be kept
           * in step. */
          hr = Rkm * pxPerKm;
        }
        /* ---- Zooming IN past the grid ----------------------------------
         *
         * A 15 km cell is 11px across at country zoom and 350px at z11: three
         * blobs filling the screen. Nothing overlaps — measured 0.986 — but the
         * mark stops being useful, and the reader zoomed in expecting MORE
         * detail and got less.
         *
         * We cannot subdivide: where inside a 15 km bin the readings came from
         * is exactly what the server did not tell us, and inventing it is the
         * one thing this layer must not do. So the cell is capped on screen and
         * SAYS it is capped — a hexagon at the cap is no longer claiming to
         * cover the ground it sits on, so it is drawn as an outline with its
         * centre marked, not as a filled territory. The reader sees "an
         * aggregate is centred here", which is true, instead of "this whole
         * neighbourhood reads 4.9", which is not.
         *
         * The real fix is finer bins from the server (context/
         * hex-zoom-proposal.md). This is the honest behaviour until then. */
        var MAX_PX = 64;
        var capped = hr > MAX_PX;
        if (capped) hr = MAX_PX;
        frame.setAttribute('data-hex-merge', merge + 'x');
        frame.setAttribute('data-hex-capped', capped ? 'yes' : 'no');

        var drawnHex = 0, thinHex = 0, foreignHex = 0;
        /* An optional layer must fail as an optional layer. A missing or
         * malformed lat_ref/bin_km makes hr NaN, and NaN passed to the
         * projection throws — which took the ENTIRE map down in production,
         * basemap and provinces with it, because one field was absent from one
         * envelope. isFinite here keeps that blast radius inside the layer that
         * caused it. */
        if (!isFinite(hr)) hr = 0;
        if (hr >= 3) {                       /* below this a hex is a speck */
          cells.forEach(function (h) {
            var v = (metric === 'p10') ? h.P1 : h.P2;
            if (v == null) return;
            var cx = X(h.lon), cy = Y(h.lat);
            if (cx < -hr || cy < -hr || cx > W + hr || cy > VH + hr) return;
            var pts = [];
            for (var i = 0; i < 6; i++) {
              var a = Math.PI / 180 * (60 * i);   /* flat-top, as binned */
              pts.push([cx + hr * Math.cos(a), cy + hr * Math.sin(a)]);
            }
            var b = band(v);
            var cls = 'map-hex' + (capped ? ' map-hex--capped' : '');
            /* A single sensor cannot be cross-checked, and 55 % of cells hold
             * exactly one. It still carries its reading — that reading is real
             * — but it says so, rather than looking as settled as a cell built
             * from thirty. Same rule as a silent province: state the limit
             * where the value is (§2.3), do not hide the cell. */
            if (h.thin) { cls += ' map-hex--thin'; thinHex++; }
            if (h.country !== 'BG') { cls += ' map-hex--foreign'; foreignHex++; }
            var poly = el('path', { class: cls, d: d(pts) });
            /* Served colour, so it is an attribute (§2.1) — and the class
             * rules below must never set `fill`, or the stylesheet wins and
             * every hex renders one grey. That defect has shipped twice. */
            /* `colour`, not `color`: the served band uses the British spelling
             * (§0.2), and `b.color` is silently undefined — which paints every
             * hex black while the node count and the tooltips all look right. */
            var rc = rampColour(v);
            if (rc) poly.setAttribute('fill', rc);
            else poly.setAttribute('class', cls + ' map-hex--none');
            var ti = el('title');
            ti.textContent = num(v) + ' µg/m³ · ' + (b ? bandName(b) + ' · ' : '') +
              t(h.thin ? 'hex.tierThin' : 'hex.tier', { n: h.n }) +
              (h.country !== 'BG' ? ' · ' + h.country : '');
            poly.appendChild(ti);
            hl.appendChild(poly);

            /* The count, ON the hex, once the cell is big enough to hold it.
             *
             * The reference map states it too ("you can see the amount of
             * sensor in the area and the median value") and it is the single
             * fact that answers "is each hexagon a sensor?" before a reader has
             * to ask. 54 of our cells hold one sensor and 6 hold more than
             * thirty; drawn identically, they claim identical authority. The
             * dashed edge on a single-sensor cell was carrying that alone and
             * was not read.
             *
             * Below ~13px there is no room for a glyph, and a number too small
             * to read is worse than none — the tooltip and the dashed edge
             * still carry it there. */
            /* 8, not 13. The threshold was chosen against the old radius, which
             * was √3 too large because it treated centre spacing as a
             * circumradius. Fixing the geometry shrank every cell to 9.13 px at
             * country zoom and silently took every count off the map — a
             * constant tuned against a bug, outliving it. */
            if (hr >= 8) {
              var nt = el('text', {
                class: 'map-hex__n', x: cx.toFixed(1), y: (cy + hr * 0.52).toFixed(1)
              });
              nt.textContent = h.n;
              hl.appendChild(nt);
            }
            drawnHex++;
          });
        }
        if (drawnHex) {
          svg.appendChild(hl);
          frame.setAttribute('data-hexes', drawnHex + '/' + thinHex + '/' + foreignHex);
        /* Which envelope drew this, and at what bin. A check that asserts on
         * hex counts alone passes identically against the snapshot and the
         * served data — that is exactly how the live path went unverified. */
        frame.setAttribute('data-hex-source', (hexes.live ? 'live' : 'snapshot') +
                                              '@' + hexes.bin_km + 'km');
        }
      }

      svg.appendChild(labels);

      labelOrder.forEach(function (sh) {
        var main = sh.rings[0];
        if (main.area <= 900) return;

        /* The name always sits under its own number, and the pair goes wherever
         * inside the province it fits.
         *
         * Two earlier builds are superseded here. One dropped the name when the
         * pair would not fit at the centroid; the next let the name wander off
         * on its own and, failing that, out of the shape on a leader line. Ten
         * leaders became two, and two is still two hairlines drawn across a map
         * that has none anywhere else. The reading and its name are one label —
         * splitting them cost more than it bought.
         *
         * What replaces both is cheaper: keep the pair together, let it move
         * off-centre, and let the name shrink one more notch. A label does not
         * have to sit at a province's centroid to belong to it; it has to sit
         * *inside* it, which the four-corner test already guarantees. */
        var silent = valueOf(sh.o) == null;
        var value = silent ? '—' : num(valueOf(sh.o));
        var label = nameOf(sh.o.name_bg);
        var alts  = namesOf(sh.o.name_bg);          // both languages
        var vw = widthOf(value, true) / 2;
        var parts = wrapName(label);
        /* A wrapped plan is only offered when BOTH names break — otherwise the
         * two maps would disagree about how many lines the label has, which is
         * the same drift the shared box exists to remove. */
        var altParts = alts.map(wrapName);
        var wrappable = altParts.every(function (w) { return !!w; });
        // Each wrapped line is sized by the wider language's version of it.
        var altLine = wrappable
          ? [altParts.map(function (w) { return w[0]; }),
             altParts.map(function (w) { return w[1]; })]
          : null;

        var at = null, nameSize = 0, wrapped = null;

        function boxHalf(twoLine, size) {
          var w = twoLine
            ? Math.max(widestOf(altLine[0], size), widestOf(altLine[1], size)) / 2
            : widestOf(alts, size) / 2;
          // The box is the value line plus however many name lines follow it.
          return [Math.max(vw, w), 7 + size * (twoLine ? 1.35 : 0.75)];
        }
        function tryPair(twoLine, size, dense, spill) {
          var b = boxHalf(twoLine, size);
          return placeLabel(main, taken, b[0], b[1], dense, spill);
        }

        /* Escalate in the order that costs the reader least: full size at the
         * centre, then full size anywhere, then smaller, then wrapped. The
         * centroid pass runs first at every size so the common case still
         * reads as a centred label rather than one shoved into a corner. */
        var plans = [];
        NAME_SIZES.forEach(function (sz) { plans.push({ two: false, size: sz, dense: false }); });
        NAME_SIZES.forEach(function (sz) { plans.push({ two: false, size: sz, dense: true }); });
        if (wrappable) NAME_SIZES.forEach(function (sz) { plans.push({ two: true, size: sz, dense: true }); });
        /* Last: let the name hang below the province rather than drop it.
         * Wrapped first, because a two-line name spills half as far. */
        if (wrappable) NAME_SIZES.forEach(function (sz) { plans.push({ two: true, size: sz, dense: true, spill: true }); });
        NAME_SIZES.forEach(function (sz) { plans.push({ two: false, size: sz, dense: true, spill: true }); });

        for (var i = 0; i < plans.length && !at; i++) {
          at = tryPair(plans[i].two, plans[i].size, plans[i].dense, plans[i].spill);
          if (at) {
            nameSize = plans[i].size;
            wrapped = plans[i].two ? (parts || [label]) : null;
          }
        }
        // Nothing holds the pair: the number alone, and the tooltip still
        // carries the name. This does not fire on the current 28.
        if (!at) {
          at = placeLabel(main, taken, vw, 7);
          if (!at) return;
          nameSize = 0;
        }

        var lines = wrapped || [label];
        // Reserve the shared box, not this language's — otherwise the English
        // map would leave gaps the Bulgarian one fills, and vice versa.
        var box = nameSize ? boxHalf(!!wrapped, nameSize) : [vw, 7];
        taken.push([at[0], at[1], box[0], box[1]]);

        /* Ink is computed from the served band (§2.1) — a silent province has
         * no band, so it takes --fg-2 through a class instead (§2.3). */
        var ink = silent ? null : inkFor(sh.fill);
        var top = nameSize ? at[1] - (wrapped ? nameSize * 0.55 : 0) - 1 : at[1] + 4;

        var v = el('text', {
          class: 'map-area__value' + (silent ? ' map-area__value--none' : ''),
          x: at[0].toFixed(1), y: top.toFixed(1)
        });
        if (ink) v.setAttribute('fill', ink);
        v.textContent = value;
        labels.appendChild(v);
        labelled++;
        if (silent) silentLabelled++;

        if (!nameSize) return;
        lines.forEach(function (part, li) {
          var n = el('text', {
            class: 'map-area__name' + (silent ? ' map-area__name--none' : ''),
            x: at[0].toFixed(1),
            y: (top + nameSize + 2 + li * (nameSize + 1)).toFixed(1)
          });
          // A presentation attribute, not a style attribute — the CSP rule is
          // about `style=""` (§1), and `fill` is set the same way.
          if (nameSize !== CAP) n.setAttribute('font-size', nameSize);
          if (ink) n.setAttribute('fill', ink);
          n.textContent = part;
          labels.appendChild(n);
        });
        named++;
      });

      // The national border on top, so province edges never break it.
      var border = el('path', { class: 'map-land', d: d(project(ring).pts) });
      svg.appendChild(border);

      /* The basemap: real road geometry, drawn ON TOP of the choropleth.
       *
       * A province was a flat slab of one served colour, which is the whole
       * reading and nothing else — true, and at any zoom past the country it
       * left the reader with no way to place what they were looking at.
       *
       * The obvious move is a translucent fill over a basemap, and it is the
       * wrong one here: the ramp is served data (§2.1) and dropping its opacity
       * re-tints it, which is the same error as adjusting the ramp for a theme.
       * So the fill keeps its exact served colour and the roads go over it,
       * carried by a casing rather than by transparency — the ordinary
       * cartographic answer, and the one that leaves the data alone.
       *
       * Two tiers, disclosed by scale, because granularity should match how
       * close the reader actually is. At the country fit neither is drawn: 813
       * lines over 28 provinces is a second dataset competing with the first,
       * and the country view exists to be read as a choropleth. */
      /* One degree of latitude is ~111 km, and `s * vk` is what a degree is
       * worth in pixels right now — so this is the real ground scale of what
       * is on screen, whatever framing produced it. */
      var pxPerKm = (s * vk) / 111;

      /* City districts, between the choropleth and the streets.
       *
       * A reader zoomed into София saw one flat band and a scatter of streets
       * with nothing to place them against: "how far am I from that sensor"
       * has no answer without the divisions people actually navigate by. The
       * райони are real administrative boundaries, fetched once from OSM and
       * localised like every other geometry here (§1 — no runtime origin).
       *
       * They carry NO fill. A tint over a served band would re-colour data
       * (§2.1), which is the same error the roads layer avoids by using a
       * casing; the boundary does the same — a page-coloured casing under a
       * thin dashed core, so it reads on the palest teal and the darkest
       * purple alike. Dashed, because an administrative line inside a province
       * must not be mistaken for the province border, which is solid.
       *
       * The names come from OSM, with English filled from Wikidata by QID
       * where OSM had no `name:en`. Three Plovdiv districts have neither, so
       * they keep their Bulgarian name on the English map — the data's own
       * spelling, never a transliteration (§5.12). */
      var drawnDistricts = 0, namedDistricts = 0, districtNames = [], districtLayer = null;
      if (!tiled() && pxPerKm >= DISTRICTS_PX_KM && districts && districts.districts) {
        var dg = el('g', { class: 'map-districts' });
        districts.districts.forEach(function (dst) {
          var xs = [], ys = [], paths = [];
          dst.rings.forEach(function (ring) {
            var pts = ring.map(function (c) { return [X(c[0]), Y(c[1])]; });
            pts.forEach(function (q) { xs.push(q[0]); ys.push(q[1]); });
            paths.push(line(pts));
          });
          if (!xs.length) return;
          var x0 = Math.min.apply(null, xs), x1 = Math.max.apply(null, xs);
          var y0 = Math.min.apply(null, ys), y1 = Math.max.apply(null, ys);
          // Wholly off-frame costs paint and says nothing.
          if (x1 < -40 || x0 > W + 40 || y1 < -40 || y0 > vh + 40) return;
          paths.forEach(function (dd) {
            dg.appendChild(el('path', { class: 'map-district__casing', d: dd }));
            dg.appendChild(el('path', { class: 'map-district', d: dd }));
          });
          drawnDistricts++;

          /* A name that will not fit is not drawn — the same rule the province
           * labels and the context names already follow. Clipped type is worse
           * than absent type, and the district outline still divides the city
           * without it.
           *
           * The name is only a CANDIDATE here. It cannot be placed until the
           * markers are known, because a reading outranks a district name: the
           * render showed Връбница, Надежда and Сердика printed straight
           * through the dots that carry the measurements. Placement happens
           * after the marker pass, below. */
          var nm = (lang() === 'en' && dst.name_en) ? dst.name_en : dst.name_bg;
          if (!nm) return;
          var cx = (x0 + x1) / 2, cy = (y0 + y1) / 2;
          if (cx < 30 || cx > W - 30 || cy < 20 || cy > vh - 12) return;
          /* Sized from the WIDER of the two names, never from the one on
           * screen. §5.2 settled this for the province labels: a map whose
           * labels move and change count when the UI language changes reads as
           * two different maps. The box is the district's, not the language's. */
          var nw = Math.max(widthOf(dst.name_bg || nm, false, 11),
                            widthOf(dst.name_en || dst.name_bg || nm, false, 11));
          if (nw + 8 > (x1 - x0)) return;
          districtNames.push({ s: nm, x: cx, y: cy, w: nw, h: 12,
                               bx0: x0, bx1: x1, by0: y0, by1: y1 });
        });
        svg.appendChild(dg);
        districtLayer = dg;
      }
      var lines = 0, streetNames = [], streetSeen = {}, streetLayer = null, streetCandidates = 0;
      /* MAJOR_PX_KM, below ROADS_PX_KM, so the country zoom is not bare.
       *
       * Country zoom measures ~1.05 px/km, under the 2.5 gate, so the schematic
       * map drew no roads at all — provinces and hexes floating on blank paper.
       * That is the "no OpenStreetMap background" the reader sees wherever the
       * tiles cannot load, and it was a threshold choice rather than missing
       * data: the asset carries 75 ways marked c:1, the national highways,
       * which is a real backdrop at a cost of 75 lines.
       *
       * These ARE OpenStreetMap geometry — the same source as the tiles,
       * bundled and simplified, credited in the same footer. The distinction
       * the reader cares about is streets-and-labels versus none, and this is
       * the honest middle. */
      if (!tiled() && pxPerKm >= MAJOR_PX_KM) {
        var majorOnly = pxPerKm < ROADS_PX_KM;
        var rl = el('g', { class: 'map-roads' });
        function lay(set, cls) {
          if (!set || !set.roads) return;
          set.roads.forEach(function (r) {
            /* Below the full-network gate only the highways draw. A secondary
             * road at 1px/km is a smudge, and 738 smudges is noise, not a map. */
            if (majorOnly && r.c !== 1) return;
            var pts = r.p.map(function (c) { return [X(c[0]), Y(c[1])]; });
            // Off-frame lines cost paint and say nothing.
            var on = pts.some(function (q) {
              return q[0] > -40 && q[0] < W + 40 && q[1] > -40 && q[1] < vh + 40;
            });
            if (!on) return;
            var dd = line(pts);
            // The tier class goes on both, so the casing scales with its line.
            var maj = r.c === 1 ? ' map-road--major' : '';
            rl.appendChild(el('path', { class: 'map-road__casing ' + cls + maj, d: dd }));
            rl.appendChild(el('path', { class: 'map-road ' + cls + maj, d: dd }));
            lines++;
          });
        }
        lay(roads, 'map-road--national');
        if (pxPerKm >= STREETS_PX_KM) lay(streets, 'map-road--street');

        /* The side streets, and the names on them.
         *
         * Below this the map showed one flat band, a district outline and the
         * two or three trunk roads that happen to cross it — which is what a
         * reader saw after zooming into Овча купел, and it answers nothing
         * about where they are. The named minor network is what a person
         * actually navigates by.
         *
         * The name is a CANDIDATE here for the same reason a district name is:
         * the markers carry the readings and are placed later, and a street
         * name printed under a value would be basemap over data. */
        if (pxPerKm >= MINOR_PX_KM) {
          var mset = minorFor(data, function (a) {
            return Math.hypot(X(a.lon) - W / 2, Y(a.lat) - vh / 2) / pxPerKm;
          }, function () {
            if (window.AIRBG_DATA) draw(window.AIRBG_DATA);
          });
          if (mset && mset.streets) {
            mset.streets.forEach(function (r) {
              var pts = r.p.map(function (c) { return [X(c[0]), Y(c[1])]; });
              var on = pts.some(function (q) {
                return q[0] > -20 && q[0] < W + 20 && q[1] > -20 && q[1] < vh + 20;
              });
              if (!on) return;
              var dd = line(pts);
              rl.appendChild(el('path', { class: 'map-road__casing map-road--minor', d: dd }));
              rl.appendChild(el('path', { class: 'map-road map-road--minor', d: dd }));
              lines++;
            });
          }

          /* The names come from the chains, not from the ways that drew the
           * lines: one label per street, on the whole street. */
          if (mset && mset.labels && pxPerKm >= STREETNAME_PX_KM) {
            mset.labels.forEach(function (L) {
              if (streetSeen[L.n]) return;
              var pts = L.p.map(function (c) { return [X(c[0]), Y(c[1])]; });
              var on = pts.some(function (q) {
                return q[0] > 0 && q[0] < W && q[1] > 0 && q[1] < vh;
              });
              if (!on) return;
              streetCandidates++;
              var wname = widthOf(L.n, false, 10), len = 0;
              for (var i = 0; i < pts.length - 1; i++) {
                len += Math.hypot(pts[i + 1][0] - pts[i][0], pts[i + 1][1] - pts[i][1]);
              }
              if (len < wname + 10) return;        // too short to carry its name
              /* Text on a path runs in the path's own direction, so a street
               * digitised east-to-west would set its name upside down. Reverse
               * the geometry rather than rotating the type. */
              var lab = pts[pts.length - 1][0] < pts[0][0] ? pts.slice().reverse() : pts;
              var mid = lab[Math.floor(lab.length / 2)];
              if (mid[0] < 0 || mid[0] > W || mid[1] < 0 || mid[1] > vh) return;
              streetSeen[L.n] = 1;
              streetNames.push({ s: L.n, d: line(lab), w: wname, h: 11,
                                 x: mid[0], y: mid[1] });
            });
          }
        }
        svg.appendChild(rl);
        streetLayer = rl;
      }
      frame.setAttribute('data-roads', String(lines));
      frame.setAttribute('data-scale', pxPerKm.toFixed(1) + 'px/km');

      /* The finer tiers, as points, and ONLY when the map is framed on one
       * province.
       *
       * These are the city (zoom 11) and neighbourhood (zoom 13) aggregates
       * the same `/api/v1/areas` endpoint serves — real places, real
       * coordinates, real readings. They are NOT sensors: the API exposes no
       * sensor tier at all, and §1 makes the tier ceiling a non-negotiable
       * the design may explain but not widen. So this layer says what it is
       * and stops there.
       *
       * A point is the honest mark HERE, unlike a province (§5.2, where the
       * area is the mark): the API gives these places a coordinate and no
       * boundary, so a filled territory would be a shape this system invented.
       * On the country view they are not drawn — 51 dots over 28 provinces is
       * a second dataset competing with the one the reader came for.
       *
       * NEIGHBOURS ARE DRAWN TOO, and the rule is what is ON SCREEN rather
       * than which province owns the point. Framed on a province the reader
       * saw one territory carrying readings inside a ring of flat colour: the
       * neighbours were painted their real served band, but with no mark, no
       * number and no name they read as background rather than as data, and
       * the first question a border raises — "is it worse on the other side?"
       * — had no answer on screen. The snapshot already holds an aggregate
       * for every province, so the answer was in the data and only this
       * filter was hiding it.
       *
       * Screen-bounded, not "bordering": a neighbour list would be a second
       * adjacency table to maintain, and it would still be wrong at the zoom
       * where the subject fills the frame. Testing the projected point is the
       * same question the reader is actually asking — is this place in view.
       *
       * They go once the reader drills in, at the scale the street layer
       * starts. Past that the subject fills the frame and what remains on
       * screen should be the place being examined, not its surroundings. This
       * supersedes the older rule scoping markers to one province: that was
       * written when a province was a solid slab, where a neighbour's dot
       * would have sat on undifferentiated colour. */
      var points = [], markerBoxes = [], markerNames = {};
      var NEIGHBOUR_MAX_PX_KM = STREETS_PX_KM;
      var showNeighbours = pxPerKm < NEIGHBOUR_MAX_PX_KM;
      var frameW = box ? box.w : W, frameH = box ? box.h : vh;
      function onScreen(lon, lat) {
        var px = X(lon), py = Y(lat);
        return px >= 0 && px <= frameW && py >= 0 && py <= frameH;
      }
      if (view.mode === 'province' && focus && data.sub_areas) {
        var pts = el('g', { class: 'map-points' });
        data.sub_areas.filter(function (a) {
          if (a.oblast === focus) return true;
          return showNeighbours && onScreen(a.lon, a.lat);
        })
          .sort(function (a, b) { return (b.p2 == null) - (a.p2 == null); })
          .forEach(function (a) {
            var av = metric === 'p10' ? a.p10 : a.p2;
            var ab = band(av), af = rampColour(av);
            var g = el('g', {
              class: 'map-point' + (av == null ? ' map-point--none' : ''),
              tabindex: '0', role: 'button', 'data-slug': a.slug, 'data-kind': a.kind
            });
            /* The marker carries its own reading, in its own band's colour.
             *
             * Every point used to be the same 7px dot. Two places 30 µg/m³
             * apart looked identical, so the layer said "there is a city here"
             * and nothing about the air — which is the one thing this map is
             * for. The dot is now big enough to hold the number, filled with
             * the served band (§2.1) and inked from that fill's own luminance,
             * exactly as a province's value is. Same rule, one tier down. */
            /* ...UNLESS the hexes are drawing, in which case it does not.
             *
             * A neighbourhood marker and a hex are two different aggregations
             * of the SAME readings — the marker averages an administrative
             * shape, the hex averages a 15 km bin — and drawn together, in the
             * same ramp, they are two coloured answers to one question sitting
             * on top of each other. A reader cannot tell which is the reading,
             * and reasonably concludes the small marks must be the sensors
             * themselves. They are not; nothing here is a sensor.
             *
             * So where hexes draw, the hex is the reading and the marker drops
             * to what it still uniquely does: naming a place and being the way
             * into it. No band fill, no number, no competing claim. */
            var cx = X(a.lon), cy = Y(a.lat);
            var navOnly = hexesWillDraw;
            var R = navOnly ? 4 : (av == null ? 9 : 14);
            var c = el('circle', {
              class: 'map-point__dot' + (navOnly ? ' map-point__dot--nav' : ''),
              cx: cx.toFixed(1), cy: cy.toFixed(1), r: R
            });
            if (af && !navOnly) c.setAttribute('fill', af);
            g.appendChild(c);

            if (!navOnly) {
              var vt = el('text', {
                class: 'map-point__value', x: cx.toFixed(1), y: (cy + 4).toFixed(1)
              });
              vt.textContent = av == null ? '—' : num(av);
              // Ink is computed from the band, never fixed: the fill is data.
              if (af) vt.setAttribute('fill', inkFor(af));
              g.appendChild(vt);
            }

            // The name sits under the mark now that the number is inside it.
            var lab = el('text', {
              class: 'map-point__name', x: cx.toFixed(1), y: (cy + R + 12).toFixed(1)
            });
            lab.textContent = lang() === 'en' ? a.name_en : a.name_bg;
            /* The name sits tight to a 4px navigation dot, not below a 14px
             * disc that is no longer there. */
            if (navOnly) lab.setAttribute('y', (cy + R + 11).toFixed(1));
            g.appendChild(lab);
            // The name is the accessible name; the reading and its tier follow,
            // so a screen reader never gets a bare number (§9.1).
            var ttl = el('title');
            ttl.textContent = (lang() === 'en' ? a.name_en : a.name_bg) + ' · ' +
              (av == null ? t('legend.none')
                          : num(av) + ' µg/m³ · ' + t('point.tier.' + a.kind));
            g.appendChild(ttl);
            g.setAttribute('aria-label', ttl.textContent);
            function pick() {
              document.dispatchEvent(new CustomEvent('airbg:areaselect', { detail: a }));
            }
            g.addEventListener('click', pick);
            g.addEventListener('keydown', function (e) {
              if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); pick(); }
            });
            pts.appendChild(g);
            points.push(a);
            /* What the marker occupies, so a district name never lands on it.
             * The mark and the name under it are one claim on the map. */
            var lw = widthOf(lab.textContent, false, 11);
            markerNames[lab.textContent] = 1;
            markerBoxes.push({ x0: cx - R, x1: cx + R, y0: cy - R, y1: cy + R });
            markerBoxes.push({ x0: cx - lw / 2, x1: cx + lw / 2,
                               y0: cy + R + 2, y1: cy + R + 16 });
          });
        svg.appendChild(pts);
      }
      frame.setAttribute('data-points', String(points.length));

      /* District names go on last, and they yield to everything already there.
       *
       * The first render placed them at their own centroids the moment the
       * outline was drawn, which put Връбница, Надежда and Сердика straight
       * through the markers that carry the readings — a basemap label sitting
       * on top of the data it is there to orient. A reading outranks a place
       * name, so the names are placed after the marker pass and any candidate
       * that collides with a marker, its own label, or a name already placed
       * is simply dropped. The outline still divides the city without it.
       *
       * GAP mirrors the province labels' rule: not overlapping is not the same
       * as being separable. */
      if (districtLayer) {
        var DGAP = 4, placedNames = markerBoxes.slice();
        function hits(b) {
          for (var i = 0; i < placedNames.length; i++) {
            var o = placedNames[i];
            if (b.x0 - DGAP < o.x1 && b.x1 + DGAP > o.x0 &&
                b.y0 - DGAP < o.y1 && b.y1 + DGAP > o.y0) return true;
          }
          return false;
        }
        // Widest first: a long name has the fewest places it can go.
        districtNames.sort(function (a, b) { return b.w - a.w; });
        districtNames.forEach(function (n) {
          /* A district whose marker is already on the map is not named twice.
           * София has both a район and a квартал called Витоша, and both
           * Кремиковци, and both Банкя — so the first render printed the same
           * word twice, 20px apart, which reads as a rendering fault rather
           * than as two real places. The marker carries a reading, so it is
           * the one that stays; the outline still shows the district. */
          if (markerNames[n.s]) return;
          /* The centroid first, then other places inside the district's own
           * box. A label does not have to be at a centroid to belong to a
           * shape — it has to be INSIDE it, which is the same call §5.2 makes
           * for a province's name. Refusing to move cost 23 of 24 names on the
           * София-град page, because 25 markers sit over the middle of the
           * city: a rule that drops a whole layer to avoid one collision has
           * priced the collision wrong. */
          var xs = [n.x, (n.bx0 + n.x) / 2, (n.bx1 + n.x) / 2];
          var ys = [n.y, n.by0 + 10, n.by1 - 6, (n.by0 + n.y) / 2, (n.by1 + n.y) / 2];
          var box = null;
          for (var yi = 0; yi < ys.length && !box; yi++) {
            for (var xi = 0; xi < xs.length; xi++) {
              var cand = { x0: xs[xi] - n.w / 2, x1: xs[xi] + n.w / 2,
                           y0: ys[yi] - n.h / 2, y1: ys[yi] + n.h / 2 };
              // Inside its own district's box, on the frame, and clear.
              if (cand.x0 < n.bx0 || cand.x1 > n.bx1) continue;
              if (cand.y0 < n.by0 || cand.y1 > n.by1) continue;
              if (cand.x0 < 4 || cand.x1 > W - 4 || cand.y0 < 4 || cand.y1 > vh - 4) continue;
              if (hits(cand)) continue;
              box = cand; break;
            }
          }
          if (!box) return;
          var nt = el('text', { class: 'map-district__name',
            x: ((box.x0 + box.x1) / 2).toFixed(1),
            y: ((box.y0 + box.y1) / 2).toFixed(1) });
          nt.textContent = n.s;
          districtLayer.appendChild(nt);
          placedNames.push(box);
          namedDistricts++;
        });
      }
      frame.setAttribute('data-districts', drawnDistricts + '/' + namedDistricts);

      /* QUARTERS — Бояна, Княжево, Горна баня and 183 more. They are what a
       * reader in Sofia actually navigates by, and no layer here had them: the
       * API stops at район and the district capture is admin_level=6, so the
       * names people use were missing from both the data and the basemap.
       *
       * Names only, no outline, and that is the honest form of this layer:
       * Overpass returned relation geometry empty on the capture, and a label
       * at a real centre states what is known while an invented boundary would
       * not. The asset says so in its own note.
       *
       * They carry NO value and never take a band colour (§2.1): nothing below
       * район is measured, so a quarter drawn like a reading would be a
       * fabricated one. This is basemap context, styled as such.
       *
       * Placed after the district names and tested against everything already
       * on the map, so a quarter never covers a reading it cannot explain. */
      var drawnQuarters = 0;
      if (quarters && quarters.quarters && pxPerKm >= QUARTER_PX_KM) {
        var ql = el('g', { class: 'map-quarters' });
        quarters.quarters.forEach(function (q) {
          var qx = X(q.lon), qy = Y(q.lat);
          if (qx < 4 || qx > W - 4 || qy < 4 || qy > vh - 4) return;
          var label = (lang() === 'en' && q.name_en) ? q.name_en : q.name_bg;
          var qw = widthOf(label, false, 10), qh = 12;
          var qbox = { x0: qx - qw / 2, x1: qx + qw / 2, y0: qy - qh / 2, y1: qy + qh / 2 };
          if (hits(qbox)) return;
          var qt = el('text', { class: 'map-quarter__name',
            x: qx.toFixed(1), y: qy.toFixed(1) });
          qt.textContent = label;
          ql.appendChild(qt);
          placedNames.push(qbox);
          drawnQuarters++;
        });
        if (drawnQuarters) svg.appendChild(ql);
      }
      frame.setAttribute('data-quarters', drawnQuarters);

      /* Street names go on last of all, under the same rule as every other
       * label here: a name that collides with something already placed is not
       * drawn. Readings first, then district names, then these — the order is
       * what the reader came for, then where they are, then which street.
       *
       * The box is tested unrotated. A rotated label's true box is a little
       * wider, so this errs tight rather than loose, which is the direction
       * that costs a name rather than producing an overlap. */
      if (streetLayer && streetNames.length) {
        var takenAll = (typeof placedNames !== 'undefined' && placedNames)
          ? placedNames : markerBoxes.slice();
        var SGAP = 3, drawnStreetNames = 0;
        streetNames.sort(function (a, b) { return b.w - a.w; });
        streetNames.forEach(function (n) {
          var box = { x0: n.x - n.w / 2, x1: n.x + n.w / 2,
                      y0: n.y - n.h / 2, y1: n.y + n.h / 2 };
          if (box.x0 < 2 || box.x1 > W - 2 || box.y0 < 2 || box.y1 > vh - 2) return;
          for (var i = 0; i < takenAll.length; i++) {
            var o = takenAll[i];
            if (box.x0 - SGAP < o.x1 && box.x1 + SGAP > o.x0 &&
                box.y0 - SGAP < o.y1 && box.y1 + SGAP > o.y0) return;
          }
          var pid = 'street-' + frame.getAttribute('data-od-id') + '-' + drawnStreetNames;
          var guide = el('path', { id: pid, d: n.d, fill: 'none', stroke: 'none' });
          streetLayer.appendChild(guide);
          var t2 = el('text', { class: 'map-street__name' });
          var tp = el('textPath', { startOffset: '50%' });
          // Both spellings: SVG 2 reads `href`, SVG 1.1 renderers read the
          // namespaced one, and this has to draw in whatever the reader has.
          tp.setAttribute('href', '#' + pid);
          tp.setAttributeNS('http://www.w3.org/1999/xlink', 'xlink:href', '#' + pid);
          tp.textContent = n.s;
          t2.appendChild(tp);
          streetLayer.appendChild(t2);
          takenAll.push(box);
          drawnStreetNames++;
        });
        // drawn/candidates, the same shape as data-labelled and data-named:
        // a count with no denominator cannot say whether a layer is thin
        // because the data is thin or because the placement is refusing.
        frame.setAttribute('data-street-names',
          drawnStreetNames + '/' + streetNames.length + '/' + streetCandidates);
      } else {
        frame.removeAttribute('data-street-names');
      }
      frame.setAttribute('data-view', view.mode + '@' + view.k.toFixed(2));

      /* SVG has no z-index: paint order is document order, so lifting the
       * hovered province above its neighbours means actually moving the node.
       * It stops below the label layer, so a lifted province never covers a
       * neighbour's reading, and the national outline stays topmost. The guard keeps the move idempotent, so
       * a pointer that stays put does not churn the DOM. */
      {
        /* The province rises above its NEIGHBOURS, and stops below the hexes.
         *
         * This used to insert before `labels`, which sits after the hex layer —
         * so the hovered province was lifted over the hexes as well. For a
         * province with a reading that only tinted them; for a province with
         * NO reading, whose fill is opaque, it painted them out completely.
         * Hovering Ямбол or Пазарджик erased every hex overlapping it, which
         * read as the hexes being sliced along that border.
         *
         * A hex overlapping a silent province is exactly the case the layer
         * exists for: the bin holds sensors from across the border, and that is
         * evidence about air the province itself cannot report. It must not be
         * hidden by the shape that has nothing to say. */
        function lift(a) {
          var ceiling = svg.querySelector('.map-hexes') || labels;
          if (a && a.nextSibling !== ceiling) svg.insertBefore(a, ceiling);
        }
        svg.addEventListener('pointerover', function (e) {
          var a = e.target.closest && e.target.closest('.map-area--link');
          if (a) lift(a);
        });
        svg.addEventListener('focusin', function (e) {
          var a = e.target.closest && e.target.closest('.map-area--link');
          if (a) lift(a);
        });
      }

      var aria = el('title');
      aria.textContent = t('map.previewAria', { n: reported, total: shapes.length });
      svg.insertBefore(aria, svg.firstChild);
      svg.setAttribute('aria-label', aria.textContent);

      canvas.textContent = '';
      canvas.appendChild(svg);
      canvas.removeAttribute('data-i18n');
      frame.setAttribute('data-preview', 'static');
      frame.setAttribute('data-labelled', labelled + '/' + reported);
      frame.setAttribute('data-named', named + '/' + labelled);
      frame.setAttribute('data-silent-labelled', silentLabelled);
    });
  }

  /* The minor network is loaded per city, on demand, and never on the home
   * page.
   *
   * София's side streets alone are 16 000 ways — about 1,1 MB — and every city
   * together would be an order of magnitude more. Shipping that with the
   * country map would make a reader who never zooms pay for detail they never
   * see, which is the opposite of what a basemap is for. So each city is its
   * own file under `assets/streets/`, fetched the first time the view is
   * actually inside that city and deep enough to draw it, then kept.
   *
   * A city with no file yet simply draws nothing: the capture is incomplete
   * (Overpass refused several boxes), and a missing file is a gap to fill
   * rather than a failure to report at the reader. */
  var minor = {};
  /* Which city the reader is looking at, measured in kilometres from the
   * middle of the frame — not in degrees. `vcx`/`vcy` are base projection
   * units, and a degree of longitude is not a degree of latitude, so a
   * degree-space radius would be an ellipse pretending to be a circle. The
   * capture boxes are ~4-8 km, so 8 km is "inside this city". */
  function minorFor(data, near, redraw) {
    if (!data || !data.sub_areas) return null;
    var best = null, bd = 1e9;
    data.sub_areas.forEach(function (a) {
      if (a.kind !== 'city') return;
      var d = near(a);
      if (d < bd) { bd = d; best = a; }
    });
    if (!best || bd > 8) return null;
    var rec = minor[best.slug];
    if (rec === undefined) {
      minor[best.slug] = null;                       // in flight: ask once
      fetch('../../assets/streets/' + best.slug + '.json')
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (j) {
          if (j) j.labels = chainByName(j.streets || []);
          minor[best.slug] = j || false;
          if (j) redraw();
        })
        .catch(function () { minor[best.slug] = false; });
      return null;
    }
    return rec || null;
  }

  /* OSM splits one street into many ways — at a junction, at a surface change,
   * wherever an editor cut it. So "ул. Борисова" is a dozen records of 150 m,
   * and a label long enough to read never fits on any single one of them: at
   * 72 px/km, 110 named ways on screen produced eight that could carry their
   * own name. The street the reader sees is the chain, not the fragment, so
   * the fragments are joined back together by name, once per file, at load.
   *
   * Greedy endpoint matching, and a chain that cannot be extended is kept as
   * it stands: a street in two disconnected parts yields two labels, which is
   * correct — they are two runs of road, and each will be tested for room on
   * its own. */
  function chainByName(ways) {
    var byName = {};
    ways.forEach(function (w) {
      if (!w.n || w.p.length < 2) return;
      (byName[w.n] = byName[w.n] || []).push(w.p);
    });
    var out = [];
    Object.keys(byName).forEach(function (name) {
      var parts = byName[name].slice(), used = [];
      var key = function (pt) { return pt[0].toFixed(5) + ',' + pt[1].toFixed(5); };
      while (parts.length) {
        var chain = parts.shift().slice(), grew = true;
        while (grew) {
          grew = false;
          for (var i = 0; i < parts.length; i++) {
            var q = parts[i];
            if (key(q[0]) === key(chain[chain.length - 1])) {
              chain = chain.concat(q.slice(1));
            } else if (key(q[q.length - 1]) === key(chain[chain.length - 1])) {
              chain = chain.concat(q.slice().reverse().slice(1));
            } else if (key(q[q.length - 1]) === key(chain[0])) {
              chain = q.slice(0, -1).concat(chain);
            } else if (key(q[0]) === key(chain[0])) {
              chain = q.slice().reverse().slice(0, -1).concat(chain);
            } else continue;
            parts.splice(i, 1); i--; grew = true;
          }
        }
        used.push(chain);
      }
      used.forEach(function (c) { out.push({ n: name, p: c }); });
    });
    return out;
  }

  var cache = {};

  /* ---- Re-ask for the hexes when the camera earns a finer grid -------------
   *
   * The asset bundle is fetched ONCE and memoised, which is right for the
   * outline, the provinces and the roads: they do not change with the camera.
   * The hexes now do. The server publishes 15/5/2/1 km and the kit derives the
   * one this scale can use — but the derivation happens on the first load,
   * before any camera exists, so it asked for nothing and then never asked
   * again. Tiers were live on the server and unreachable from the UI.
   *
   * So: after a zoom settles, if the wanted tier differs from the one already
   * on screen, drop the memo and redraw. Only the hex fetch is affected; every
   * other asset is served from the browser cache on the second pass.
   *
   * Guarded on the tier, not on the zoom, because zoom changes constantly and a
   * tier changes rarely — this must not become a request per wheel notch. */
  var servedTier = null;
  function retierIfNeeded() {
    var want = cache.wanted && cache.wanted();
    if (!want || want === servedTier) return;
    servedTier = want;
    cache.p = null;                       /* next assets() re-fetches the hexes */
    if (window.AIRBG_DATA) draw(window.AIRBG_DATA);
  }
  document.addEventListener('airbg:zoomsettled', retierIfNeeded);

  function assets() {
    if (cache.p) return cache.p;
    cache.p = Promise.all([
      fetch('../../assets/bg-outline.json').then(function (r) { return r.json(); }),
      fetch('../../assets/bg-provinces.json').then(function (r) { return r.json(); }),
      /* Context is optional. If the neighbours fail to load the map still draws —
       * Bulgaria on its own is incomplete, not wrong. */
      fetch('../../assets/bg-neighbours.json').then(function (r) { return r.json(); })
        .catch(function () { return null; }),
      fetch('../../assets/bg-rivers.json').then(function (r) { return r.json(); })
        .catch(function () { return null; }),
      /* Two road tiers, both optional and both localised (§1: no third-party
       * origin at runtime). They are basemap, so a map without them is quieter,
       * not wrong. */
      fetch('../../assets/bg-roads.json').then(function (r) { return r.json(); })
        .catch(function () { return null; }),
      fetch('../../assets/bg-streets.json').then(function (r) { return r.json(); })
        .catch(function () { return null; }),
      /* City districts (райони). Optional like every other basemap layer. */
      fetch('../../assets/bg-districts.json').then(function (r) { return r.json(); })
        .catch(function () { return null; }),
      /* Quarters (квартали) INSIDE those районы — Бояна, Княжево, Горна баня and
       * 183 more. They are the names people actually navigate Sofia by, and
       * neither layer above had them: the API publishes no aggregate below
       * район, and the district capture is admin_level=6. */
      fetch('../../assets/bg-quarters.json').then(function (r) { return r.json(); })
        .catch(function () { return null; }),
      /* Hexagonal aggregates of the sensor.community feed — the same ODbL
       * source every reading on this site already comes from. A hex is an
       * AGGREGATE: no sensor id or coordinate is published, and a 12 km bin is
       * coarser than the neighbourhood tier the API already serves, so this
       * does not widen the tier ceiling (§1). Optional like every other layer. */
      /* The SERVER owns the grid now. /api/v1/hexes publishes resolution_km in
       * its own envelope, so the kit reads the bin size rather than holding a
       * second copy of it — change the number server-side and the map follows
       * with no kit release. The bundled asset stays as the offline fallback
       * (§5.3a: the snapshot is half the design, not a bolt-on), and it carries
       * its own bin_km, so the two grids can never be mistaken for each other. */
      (function () {
        var api = (window.AIRBG_ORIGINS && window.AIRBG_ORIGINS.api) || '';
        /* The snapshot is a legitimate offline fallback (§5.3a) — but it is a
         * DIFFERENT ENVELOPE from the served one, at a different bin size, and
         * swapping to it silently is how a live-path defect shipped: off
         * loopback the cross-origin fetch always fails, so every local run
         * verified the fallback and never the code the reader actually gets.
         * Say which source is on screen, loudly, and record it on the frame so
         * a harness can assert on it. */
        /* What the current camera could use, in km, snapped to the tiers the
         * server is likely to publish. Null when there is no camera yet — the
         * first load asks for nothing and takes the default. */
        /* Published on the cache so the re-tier check can ask the same question
         * this fetch answers — one definition, so the request and the trigger
         * cannot drift apart. */
        cache.wanted = wantedResolutionKm;
        function wantedResolutionKm() {
          var st = window.AIRBG_MAP_STATE && window.AIRBG_MAP_STATE('map');
          var pk = window.AIRBG_MAP_PXPERKM && window.AIRBG_MAP_PXPERKM();
          if (!pk || !isFinite(pk) || pk <= 0) return null;
          var km = 26 / pk;                       /* a cell ≈ TARGET_PX across */
          /* The tiers the server actually publishes. 1 km is included because
           * it exists; whether it SHOULD be reachable is the open
           * anti-enumeration question, and that gate belongs on the server —
           * the kit asking for a tier it is not allowed is a refusal, not a
           * leak. */
          var TIERS = [15, 5, 2, 1];
          for (var i = 0; i < TIERS.length; i++) if (km >= TIERS[i]) return TIERS[i];
          return TIERS[TIERS.length - 1];
        }
        /* The lon/lat box the reader can currently see, rounded — a bbox that
         * changes on every pixel of pan would defeat any cache in front of it. */
        function visibleBBox() {
          var b = window.AIRBG_MAP_BBOX && window.AIRBG_MAP_BBOX();
          if (!b) return null;
          return b.map(function (v) { return Math.round(v * 10) / 10; });
        }
        function local(why) {
          if (why) console.warn('map-render: hexes fell back to the bundled ' +
                                'snapshot — ' + why + '. This is NOT the live ' +
                                'envelope; the served one carries no lat_ref ' +
                                'or window and a different bin size.');
          return fetch('../../assets/bg-hexes.json').then(function (r) { return r.json(); })
            .then(function (a) { a.live = false; return a; });
        }
        if (!api) return local('no API origin configured').catch(function () { return null; });
        /* Ask for the resolution this camera can actually use.
         *
         * The kit never assumes what it will get: `resolution_km` comes back in
         * the envelope and the draw pass sizes from that, so a server that
         * ignores the parameter — as ours does today — is served correctly and
         * nothing here has to change when it stops ignoring it.
         *
         * The wanted resolution is derived from the camera, not from a zoom
         * number: at a given scale a cell should land near TARGET_PX across, so
         * wanted_km ≈ TARGET_PX / pxPerKm. Sent as a hint, honoured or not.
         *
         * The bbox is sent for the same reason: a 2 km national grid is order
         * 10⁴ cells and nobody needs the ones off screen. Both are additive —
         * an older server sees query parameters it does not read. */
        var want = wantedResolutionKm();
        var q = [];
        if (want) q.push('resolution_km=' + want);
        var bb = visibleBBox();
        if (bb) q.push('bbox=' + bb.join(','));
        return fetch(api + 'hexes' + (q.length ? '?' + q.join('&') : '')).then(function (r) {
          if (!r.ok) throw new Error('HTTP ' + r.status);
          return r.json();
        }).then(function (d) {
          var rows = d.hexes || [];
          /* The served envelope carries generated_at, resolution_km and the
           * rows — and nothing else. The bundled snapshot also carries lat_ref
           * and window, which the draw pass needs to size a hex on screen, so
           * they have to be MEASURED from the rows here rather than read off an
           * envelope that never had them. Omitting them is not a missing
           * decoration: kmDeg goes NaN, the first X() throws on an
           * (NaN, 0) coordinate, and the whole map dies — not just the hexes. */
          var lons = rows.map(function (h) { return h.lon; });
          var lats = rows.map(function (h) { return h.lat; });
          var win = rows.length ? {
            lon: [Math.min.apply(null, lons), Math.max.apply(null, lons)],
            lat: [Math.min.apply(null, lats), Math.max.apply(null, lats)]
          } : null;
          return {
            live: true,
            bin_km: d.resolution_km,          /* served, never assumed */
            generated_at: d.generated_at,
            window: win,
            /* The latitude the bin's width in km was converted at. The middle
             * of the data's own span, for the same reason the snapshot picks
             * the middle of its window: a hex sized at 38° is visibly wrong at
             * 46°, and the error is worst at the edges either way. */
            lat_ref: win ? (win.lat[0] + win.lat[1]) / 2 : null,
            hexes: rows.map(function (h) {
              var v = h.values || {};
              return {
                lon: h.lon, lat: h.lat, n: h.n, country: h.country,
                /* A hex holding one sensor cannot be cross-checked. It keeps its
                 * real colour and says so (§2.3); it is not hidden. */
                thin: h.n === 1,
                P1: v.P1, P2: v.P2
              };
            })
            /* The device clamp, dropped on read until the server drops it at
             * source. 90 sensors worldwide report P2 at exactly 999.9 and 82
             * report P1 at exactly 1999.9, across countries at the same moment:
             * that is the ceiling, not the air. Without this the map paints the
             * worst band over a village because one device is stuck. Exact
             * equality only — a real pollution event must still come through.
             * Remove this when /api/v1/hexes filters it; the comment is here so
             * the next reader knows it is a duplicate and not a second opinion. */
            .filter(function (h) {
              return h.P1 !== 1999.9 && h.P2 !== 999.9;
            })
          };
        }).catch(function (e) {
          return local((e && e.message) || 'the live fetch failed');
        }).catch(function () { return null; })
      })()
    ]);
    return cache.p;
  }

  function draw(data) {
    if (!data || !data.oblasti || !data.scale_p2_eaqi) return;
    assets().then(function (v) { ready(v[0], v[1], v[2], v[3], data, v[4], v[5], v[6], v[7], v[8]); }).catch(function (e) {
      // The placeholder sentence stays: an honest "shown in the app" beats a
      // blank frame.
      /* The stack, not just the message: a DOMException from an SVG attribute
       * has an empty message, so the old line printed "preview unavailable —"
       * and nothing else. An error that names no location costs more time
       * than no error at all. */
      console.error('map-render: preview unavailable —', (e && (e.stack || e.message)) || e);
    });
  }

  /* One repaint entry point for whoever moves the camera. map-tiles.js calls
   * this on every animation frame of a drag; nothing else needs to know how
   * the pass is invoked. */
  window.AIRBG_MAP_DRAW = function () {
    if (window.AIRBG_DATA) draw(window.AIRBG_DATA);
  };

  // airbg-data.js owns the fetching; this only ever renders what it publishes.
  if (window.AIRBG_DATA) draw(window.AIRBG_DATA);
  document.addEventListener('airbg:datachange', function (e) { draw(e.detail); });
  document.addEventListener('airbg:languagechange', function () { draw(window.AIRBG_DATA); });
  // A metric change repaints from the same data: different value, different
  // scale, same everything else.
  document.addEventListener('airbg:metricchange', function () { draw(window.AIRBG_DATA); });
  /* Zooming re-runs the whole pass rather than scaling a viewBox. Scaling
   * would blow the type up with the geometry and leave every label where a
   * different scale had put it; re-running places labels for the scale the
   * reader is actually looking at. */
  document.addEventListener('airbg:viewchange', function () { draw(window.AIRBG_DATA); });

  /* Dragging the frame changes the window, so the pass runs again.
   *
   * Two guards, both necessary. The redraw itself changes the frame's contents,
   * which can fire the observer again — so a pass only happens when the aspect
   * has actually moved more than 1,5 %, which a re-render never does on its
   * own. And the work is deferred to the next frame, so a drag that fires
   * dozens of resize events repaints once per frame rather than once per
   * event. */
  if (window.ResizeObserver) {
    var seen = {}, pending = 0;
    var ro = new ResizeObserver(function (entries) {
      var need = false;
      entries.forEach(function (en) {
        var el2 = en.target, id = el2.getAttribute('data-od-id');
        var r = el2.getBoundingClientRect();
        if (!r.width || !r.height) return;
        var a = r.height / r.width;
        if (!seen[id] || Math.abs(a - seen[id]) / seen[id] > 0.015) { seen[id] = a; need = true; }
      });
      if (!need || pending) return;
      pending = requestAnimationFrame(function () {
        pending = 0;
        if (window.AIRBG_DATA) draw(window.AIRBG_DATA);
      });
    });
    frames.forEach(function (f) { ro.observe(f); });
  }
  /* A new province is a new subject, so the view returns to that subject's own
   * framing rather than keeping a zoom and a pan the reader set for a different
   * shape. Same argument as the reset button (§5.2b). */
  document.addEventListener('airbg:oblastchange', function () {
    var v = VIEWS['area-map'];
    if (v) { v.mode = 'province'; v.k = 1; v.dx = v.dy = 0; }
    draw(window.AIRBG_DATA);
  });
})();
