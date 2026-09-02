/* Find a place inside THIS province, and choose which places the map shows.
 *
 * What this control finds, and why it is not sensors
 * -------------------------------------------------
 * It used to hold ten hand-written sensors with invented readings — real
 * Plovdiv district names against numbers nobody measured — and it offered the
 * same ten on every province page. That is the placeholder this system forbids
 * keeping once real data exists for it (§5.2), and the province page made it
 * worse: a reader on Смолян was being shown Пловдив's districts.
 *
 * There is no sensor tier to replace it with. `/api/v1/areas` serves exactly
 * three kinds — oblast (zoom 9), city (zoom 11) and neighbourhood (zoom 13) —
 * and nothing below. That is not an oversight to route around: §1 lists the
 * map tiers as an anti-enumeration control, and says the design may explain
 * what a tier returns but may not widen it. Enumerating individual sensors is
 * the thing the API is built not to do.
 *
 * So the finder finds what the data actually has: the real cities and
 * neighbourhoods inside the province the page is about, with their own real
 * readings and their own sensor counts. Every one is a place that exists at a
 * coordinate the API published. Deferring to the tier is the honest answer;
 * inventing a sensor list to satisfy the shape of the old control would not be.
 *
 * The list is scoped to one province. A finder on a province page that offers
 * places in other provinces is answering a question the reader did not ask.
 */
(function () {
  var root = document.querySelector('[data-od-id="sensor-bar"]');
  if (!root) return;
  if (!window.AIRBG_T) console.error('area-sensors: i18n.js must load first.');

  var input   = root.querySelector('#sensor-search');
  var listbox = root.querySelector('#sensor-listbox');
  var count   = document.querySelector('[data-od-id="sensor-count"]');
  var picked  = document.querySelector('[data-od-id="sensor-picked"]');
  var frame   = document.querySelector('[data-od-id="area-map"]');

  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }
  function metric() { return window.AIRBG_METRIC ? window.AIRBG_METRIC() : 'p2'; }
  function num(v) {
    return new Intl.NumberFormat(lang(), { maximumFractionDigits: 1 }).format(v);
  }

  /* Which province this page is about. One owner already answers that — the
   * heading carries the identity oblast-link.js resolved — so this reads it
   * rather than parsing the URL a second time (§5.2). */
  function province() {
    var h1 = document.querySelector('[data-oblast]');
    return h1 && h1.getAttribute('data-oblast');
  }

  function places() {
    var d = window.AIRBG_DATA, p = province();
    if (!d || !d.sub_areas || !p) return [];
    return d.sub_areas.filter(function (a) { return a.oblast === p; });
  }
  function valueOf(a) { return metric() === 'p10' ? a.p10 : a.p2; }
  function reporting(a) { return valueOf(a) != null; }
  function label(a) { return lang() === 'en' ? a.name_en : a.name_bg; }
  function name(a) { return label(a) + ' · ' + t('point.tier.' + a.kind); }

  var state = { status: 'active', q: '' };

  function visible() {
    return places().filter(function (a) {
      if (state.status === 'active'   && !reporting(a)) return false;
      if (state.status === 'inactive' &&  reporting(a)) return false;
      if (!state.q) return true;
      return name(a).toLocaleLowerCase(lang()).indexOf(state.q) !== -1;
    }).sort(function (x, y) {
      return new Intl.Collator(lang()).compare(label(x), label(y));
    });
  }

  /* The count line owes the reader three things whenever a filter is on: how
   * many are hidden, why, and the way back (§5.4). The denominator is always
   * the whole province, never the filtered set (§9.3).
   *
   * It also has to survive the case this data actually produces. Only seven of
   * the 28 provinces have any finer-tier reading at all, so on most pages the
   * honest sentence is "this province publishes no readings below its own
   * level" — not a count of zero dressed up as a filter result. */
  function paintCount() {
    if (!count) return;
    var all = places(), on = all.filter(reporting).length, shown = visible().length;
    if (!all.length) { count.textContent = t('place.countNone'); return; }
    if (state.status === 'all') {
      count.textContent = t('place.count', {
        shown: shown, total: all.length, active: on, off: all.length - on
      });
      return;
    }
    count.textContent = t(state.status === 'active' ? 'place.countActive' : 'place.countInactive',
      { shown: shown, total: all.length, hidden: all.length - shown });
  }

  function buildList() {
    listbox.textContent = '';
    var rows = visible();
    if (!rows.length) {
      var none = document.createElement('li');
      none.className = 'combobox__empty';
      /* Three different emptinesses, three different sentences. The province
       * has no finer tier at all; the filter is hiding what there is; or the
       * query matches nothing. One message for all three would be wrong about
       * two of them. */
      none.textContent = !places().length ? t('place.none')
        : state.q ? t('place.noMatch') : t('place.noneShown');
      listbox.appendChild(none);
      return;
    }
    rows.forEach(function (a, i) {
      var li = document.createElement('li');
      li.className = 'combobox__opt';
      li.id = 'sensor-opt-' + i;
      li.setAttribute('role', 'option');
      li.setAttribute('aria-selected', 'false');
      li.textContent = name(a);
      if (!reporting(a)) {
        // Absence is a state, not an error: muted, no colour, no icon (§2.3).
        var off = document.createElement('span');
        off.className = 'combobox__opt-off';
        off.textContent = ' · ' + t('place.noReading');
        li.appendChild(off);
      }
      li.addEventListener('mousedown', function (e) { e.preventDefault(); choose(a); });
      listbox.appendChild(li);
    });
  }

  /* Picking a place and clicking its marker are the same act, so both go
   * through here and both end in one event. A finder that highlighted the map
   * while the map did nothing back would be two controls disagreeing about one
   * selection (§5.2). */
  function choose(a) {
    input.value = label(a);
    state.q = '';
    close();
    document.dispatchEvent(new CustomEvent('airbg:areaselect', { detail: a }));
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
    if (e.key === 'Enter') {
      var rows = visible();
      if (rows.length === 1) { e.preventDefault(); choose(rows[0]); }
      return;
    }
    if (e.key !== 'Escape') return;
    if (!listbox.hidden) { close(); return; }
    if (input.value) { input.value = ''; state.q = ''; }
  });

  root.addEventListener('change', function (e) {
    if (e.target.name !== 'sensor-status') return;
    state.status = e.target.value;
    if (frame) frame.setAttribute('data-sensor-filter', state.status);
    paintCount();
    if (!listbox.hidden) buildList();
  });

  /* A marker click fills the field, so the two controls always state the same
   * selection. Guarded against the event this file just dispatched. */
  document.addEventListener('airbg:areaselect', function (e) {
    if (!e.detail) return;
    if (input.value !== label(e.detail)) input.value = label(e.detail);
    paintPicked(e.detail);
  });

  /* The readout for the selected place: its name, its tier, its reading, and
   * how many sensors the API says feed it. Never a bare number (§9.1). */
  function paintPicked(a) {
    if (!picked) return;
    var v = valueOf(a);
    picked.hidden = false;
    picked.textContent = t(v == null ? 'place.pickedNone' : 'place.picked', {
      name: label(a),
      tier: t('point.tier.' + a.kind),
      value: v == null ? '—' : num(v),
      metric: t(metric() === 'p10' ? 'metric.p10' : 'metric.p2'),
      n: a.sensor_count
    });
  }

  /* The peak card used to read "96,0 µg/m³ · отделен сензор, кв. Тракия" —
   * a per-sensor figure nobody measured, frozen into the markup and identical
   * on all 28 province pages. It is the same fabrication as the ten sample
   * sensors, and it outlived them by one edit.
   *
   * It now reports the highest of the province's own finer-tier places, which
   * is a real reading of a real place. Where the province has no such reading
   * — and that is most of them — it says so rather than borrowing a number
   * from somewhere else (§2.3). */
  function paintPeak() {
    var label = document.querySelector('[data-od-id="area-peak-label"]');
    var value = document.querySelector('[data-od-id="area-peak-value"]');
    var tier  = document.querySelector('[data-od-id="area-peak-tier"]');
    if (!label || !value || !tier) return;
    var m = t(metric() === 'p10' ? 'metric.p10' : 'metric.p2');
    label.textContent = t('area.peakLabel', { metric: m });
    var top = places().filter(reporting).sort(function (a, b) {
      return valueOf(b) - valueOf(a);
    })[0];
    if (!top) {
      value.textContent = '—';
      tier.textContent = t('area.peakNone');
      return;
    }
    value.textContent = num(valueOf(top)) + ' ';
    var u = document.createElement('span');
    u.className = 'readout__unit';
    u.textContent = 'µg/m³';
    value.appendChild(u);
    tier.textContent = t('area.peakTier', {
      name: label2(top), tier: t('point.tier.' + top.kind)
    });
  }
  function label2(a) { return lang() === 'en' ? a.name_en : a.name_bg; }

  var last = null;
  document.addEventListener('airbg:areaselect', function (e) { last = e.detail; });
  function repaint() {
    paintCount();
    paintPeak();
    if (last) paintPicked(last);
    if (!listbox.hidden) buildList();
  }
  document.addEventListener('airbg:languagechange', function () {
    if (last) input.value = label(last);
    state.q = '';                       // the query belongs to the field (§5.2)
    repaint();
  });
  document.addEventListener('airbg:metricchange', repaint);
  /* A different province means a different set of places, so the field, the
   * selection and the readout all belong to the province that just left. */
  document.addEventListener('airbg:oblastchange', function () {
    last = null;
    input.value = '';
    state.q = '';
    if (picked) { picked.hidden = true; picked.textContent = ''; }
    close();
    repaint();
  });
  document.addEventListener('airbg:datachange', repaint);

  paintCount();
  paintPeak();
})();
