/* One fetch on load, one more per press of Обнови. Nothing on a timer.
 *
 * Every screen reads the same object and the same event, so a province's
 * figures cannot drift from the table's (§9.3): whoever renders listens for
 * `airbg:datachange` and re-reads `window.AIRBG_DATA`. No component fetches on
 * its own.
 *
 * THE API SENDS NO CORS HEADER. Checked: /api/v1/areas returns no
 * `access-control-allow-origin`, so a browser may read it only from airbg.org
 * itself. Opened from file:// or any other host the request fails — which is
 * the normal case for this kit. That is why the bundled snapshot is not a
 * fallback bolted on afterwards but half the design: the screens always have
 * data, and the status line says which of the two the reader is looking at.
 * Showing a stale figure while implying it is live would be the §9 defect.
 */
(function () {
  /* Same one owner as the tile origin (origins.js). */
  var API = (window.AIRBG_ORIGINS && window.AIRBG_ORIGINS.api) || 'https://airbg.org/api/v1/';
  var SNAPSHOT = 'airbg-snapshot.json';

  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }
  function num(v, d) {
    return new Intl.NumberFormat(lang(), { maximumFractionDigits: d == null ? 2 : d }).format(v);
  }

  /* The API's own stamp, in Sofia time, in the reader's language. Absolute and
   * local — never "3 минути назад", which goes stale in a cached page (§9.5). */
  function stamp(iso) {
    var d = new Date(iso);
    if (isNaN(d)) return '';
    var time = new Intl.DateTimeFormat(lang(), {
      hour: '2-digit', minute: '2-digit', timeZone: 'Europe/Sofia'
    }).format(d);
    var day = new Intl.DateTimeFormat(lang(), {
      day: 'numeric', month: 'long', timeZone: 'Europe/Sofia'
    }).format(d);
    return time + ', ' + day;
  }

  /* The parent province of a city or neighbourhood, learned once from the
   * bundled snapshot and applied to whatever the API serves later.
   *
   * `/api/v1/areas` publishes no parent for the finer tiers, so the join was
   * computed from the real boundaries (point-in-polygon against Natural Earth)
   * when the snapshot was captured, and stored on each sub-area. Slugs are
   * stable, so the live feed can be joined by slug rather than by recomputing
   * geometry in the browser on every load.
   *
   * A slug the snapshot has never seen gets no parent and therefore appears on
   * no province page. That is the honest failure: a new place is missing until
   * the snapshot is recaptured, rather than being attached to a province that
   * a guess picked for it. */
  var PARENT = {};
  function learnParents(list) {
    (list || []).forEach(function (a) { if (a.oblast) PARENT[a.slug] = a.oblast; });
  }

  // Both sources are reshaped into one form, so nothing downstream has to know
  // which one it got.
  function fromApi(areas, meta) {
    var obl = areas.areas.filter(function (a) { return a.kind === 'oblast'; });
    return {
      live: true,
      generated_at: areas.generated_at,
      attribution: meta && meta.attribution,
      boundary_attribution: meta && meta.boundary_attribution,
      oblasti: obl.map(function (o) {
        return {
          name_bg: o.name_bg, name_en: o.name_en, slug: o.slug,
          sensor_count: o.sensor_count, lon: o.lon, lat: o.lat,
          p2: o.values && o.values.P2 != null ? o.values.P2 : null,
          p10: o.values && o.values.P1 != null ? o.values.P1 : null
        };
      }),
      // The city and neighbourhood tiers, in the same shape as the snapshot's.
      sub_areas: areas.areas.filter(function (a) { return a.kind !== 'oblast'; })
        .map(function (a) {
          return {
            slug: a.slug, kind: a.kind, name_bg: a.name_bg, name_en: a.name_en,
            lon: a.lon, lat: a.lat, zoom: a.zoom,
            sensor_count: a.sensor_count, covered: a.covered,
            oblast: PARENT[a.slug] || null,
            p2: a.values && a.values.P2 != null ? a.values.P2 : null,
            p10: a.values && a.values.P1 != null ? a.values.P1 : null
          };
        }).filter(function (a) { return a.oblast; })
    };
  }
  function fromSnapshot(s) {
    return {
      live: false, generated_at: s.generated_at,
      attribution: s.attribution, boundary_attribution: s.boundary_attribution,
      scale_p2_eaqi: s.scale_p2_eaqi, scale_p1_eaqi: s.scale_p1_eaqi, oblasti: s.oblasti,
      sub_areas: s.sub_areas || []
    };
  }

  // A non-2xx is a failure, not data: fetch only rejects on network errors.
  function j(r) { if (!r.ok) throw new Error('HTTP ' + r.status); return r.json(); }

  var scale = null, scale10 = null;       // the ramps, kept from whichever source

  function paintReadouts(data) {
    // The home readouts summarise whichever particulate the switcher is on;
    // a card that keeps reporting PM2.5 under a ФПЧ10 map is two surfaces
    // disagreeing about one question (§9.3).
    var metric = window.AIRBG_METRIC ? window.AIRBG_METRIC() : 'p2';
    var val = function (o) { return metric === 'p10' ? o.p10 : o.p2; };
    var withData = data.oblasti.filter(function (o) { return val(o) != null; });
    var silent = data.oblasti.length - withData.length;
    var sensors = data.oblasti.reduce(function (s, o) { return s + o.sensor_count; }, 0);

    var home = document.querySelector('[data-od-id="country-readouts"]');
    if (home && withData.length) {
      var top = withData.slice().sort(function (a, b) { return val(b) - val(a); })[0];
      var vals = withData.map(val).sort(function (a, b) { return a - b; });
      var m = vals.length >> 1;
      var median = vals.length % 2 ? vals[m] : (vals[m - 1] + vals[m]) / 2;
      var cells = home.querySelectorAll('.readout__value');
      setValue(cells[0], num(val(top)), true);
      setValue(cells[1], num(Math.round(median * 100) / 100), true);
      setValue(cells[2], num(sensors, 0), false);
      setValue(cells[3], num(silent, 0), false);
      var tier = home.querySelectorAll('.readout__tier')[0];
      if (tier) {
        tier.textContent = (window.AIRBG_NAME ? window.AIRBG_NAME(top.name_bg) : top.name_bg) +
          ' · ' + t('area.tierOblast');
      }
      var covered = home.querySelectorAll('.readout__tier')[1];
      if (covered) covered.textContent = t('home.tierCovered', { n: withData.length });
    }

    // The area screen is one province, named by its own title (§5.12: the name
    // is the identity, the label is resolved from it).
    var area = document.querySelector('[data-od-id="area-readouts"]');
    var title = document.querySelector('[data-oblast]');
    if (area && title) {
      var key = title.getAttribute('data-oblast');
      var o = data.oblasti.filter(function (x) { return x.name_bg === key; })[0];
      if (o) {
        var ac = area.querySelectorAll('.readout__value');
        setValue(ac[0], o.p2 == null ? '—' : num(o.p2), o.p2 != null);
        setValue(ac[1], o.p10 == null ? '—' : num(o.p10), o.p10 != null);
        setValue(ac[3], num(o.sensor_count, 0), false);
        // The script owns this sentence, so the markup does not also tag it
        // with a data-i18n key — the two fought and the catalogue's frozen
        // "111 сензора" won on every language pass (§5.12).
        var intro = document.querySelector('[data-od-id="area-intro"]');
        if (intro) intro.textContent = t('area.intro', { n: o.sensor_count });
      }
    }
  }

  // A readout is value + unit; rewriting the whole cell would delete the unit
  // span, and §9.4 requires the unit beside every measured value.
  function setValue(cell, text, hasUnit) {
    if (!cell) return;
    var unit = cell.querySelector('.readout__unit');
    cell.textContent = text + (hasUnit && unit ? ' ' : '');
    if (hasUnit && unit) cell.appendChild(unit);
  }

  function paintStamp(data) {
    var when = stamp(data.generated_at);
    document.querySelectorAll('[data-od-id="refresh-status"]').forEach(function (el) {
      el.textContent = t(data.live ? 'refresh.live' : 'refresh.local', { when: when });
    });
    document.querySelectorAll('[data-i18n="foot.updated"]').forEach(function (el) {
      el.textContent = t('foot.updatedAt', { when: when });
    });
    // ODbL requires attribution wherever the data is shown. It is served by
    // /api/v1/meta rather than typed here, so it cannot drift from the licence.
    document.querySelectorAll('[data-od-id="attribution"]').forEach(function (el) {
      var parts = [data.attribution, data.boundary_attribution].filter(Boolean);
      el.textContent = parts.join(' · ');
      el.hidden = !parts.length;
    });
  }

  function publish(data) {
    // The snapshot teaches the parent join; the API inherits it (see PARENT).
    learnParents(data.sub_areas);
    if (data.sub_areas && !data.sub_areas.length && window.AIRBG_DATA &&
        window.AIRBG_DATA.sub_areas) data.sub_areas = window.AIRBG_DATA.sub_areas;
    if (data.scale_p2_eaqi) scale = data.scale_p2_eaqi;
    if (!data.scale_p2_eaqi && scale) data.scale_p2_eaqi = scale;
    if (data.scale_p1_eaqi) scale10 = data.scale_p1_eaqi;
    if (!data.scale_p1_eaqi && scale10) data.scale_p1_eaqi = scale10;
    window.AIRBG_DATA = data;
    paintReadouts(data);
    paintStamp(data);
    document.dispatchEvent(new CustomEvent('airbg:datachange', { detail: data }));
  }

  function busy(on) {
    document.querySelectorAll('[data-od-id="refresh"]').forEach(function (b) {
      b.disabled = on;
      b.setAttribute('aria-busy', on ? 'true' : 'false');
    });
    if (on) {
      // §8: a loading state is a muted line of text saying what is loading —
      // no spinner, no shimmer.
      document.querySelectorAll('[data-od-id="refresh-status"]').forEach(function (el) {
        el.textContent = t('refresh.busy');
      });
    }
  }

  function snapshot() {
    return fetch(SNAPSHOT).then(function (r) { return r.json(); }).then(fromSnapshot);
  }

  function load(manual) {
    busy(true);
    // A manual press must not be answered from cache (the API sets max-age=150).
    var bust = manual ? '?_=' + Date.now() : '';
    return Promise.all([
      fetch(API + 'areas' + bust, { cache: manual ? 'reload' : 'default' }).then(j),
      fetch(API + 'meta' + bust, { cache: manual ? 'reload' : 'default' }).then(j).catch(function () { return null; })
    ]).then(function (v) {
      publish(fromApi(v[0], v[1]));
    }).catch(function () {
      // No CORS header, offline, or opened from file:// — say so and show the
      // bundled copy rather than an empty screen.
      return snapshot().then(function (d) {
        publish(d);
        document.querySelectorAll('[data-od-id="refresh-status"]').forEach(function (el) {
          el.textContent = t('refresh.local', { when: stamp(d.generated_at) });
        });
      });
    }).then(function () { busy(false); });
  }

  document.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('[data-od-id="refresh"]');
    if (b) { e.preventDefault(); load(true); }
  });
  // Re-render the composed strings in the new language, without re-fetching.
  document.addEventListener('airbg:languagechange', function () {
    if (window.AIRBG_DATA) { paintReadouts(window.AIRBG_DATA); paintStamp(window.AIRBG_DATA); }
  });
  document.addEventListener('airbg:oblastchange', function () {
    if (window.AIRBG_DATA) { paintReadouts(window.AIRBG_DATA); paintStamp(window.AIRBG_DATA); }
  });
  document.addEventListener('airbg:metricchange', function () {
    if (window.AIRBG_DATA) paintReadouts(window.AIRBG_DATA);
  });

  // Snapshot first so the screen is never empty, then upgrade if the API answers.
  snapshot().then(publish).catch(function () {}).then(function () { load(false); });
})();
