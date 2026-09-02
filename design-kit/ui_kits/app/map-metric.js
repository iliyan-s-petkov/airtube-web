/* Which particulate the map is showing.
 *
 * The switcher was markup with nothing bound to it: clicking ФПЧ10 moved the
 * radio and changed nothing on the map, the legend or the readouts — the dead
 * control this design system has hit more often than any other defect.
 *
 * One owner. This file holds the metric, and everything that depends on it
 * re-renders from `airbg:metricchange`: the choropleth, the readout bar and
 * the legend. Nothing else reads the radios.
 *
 * THE LEGEND HAS TO MOVE WITH IT. `/api/v1/scales` serves an EAQI scale per
 * metric and their band edges are NOT the same — PM2.5 breaks at 5/10/20/25/50
 * and PM10 at 20/40/50/100/150, while the six colours are identical. So a
 * switch that recoloured the map and left the key alone would state thresholds
 * the map is not using, which is worse than the dead control: it would be
 * confidently wrong instead of visibly inert (§5.2).
 */
(function () {
  var frame = document.querySelector('[data-od-id="map"]');
  var set = document.querySelector('[data-od-id="metric-switcher"]');
  if (!frame || !set) return;

  var metric = 'p2';                       // matches the checked radio in markup
  window.AIRBG_METRIC = function () { return metric; };

  function t(k, v) { return window.AIRBG_T ? window.AIRBG_T(k, v) : k; }
  function lang() { return window.AIRBG_LANG ? window.AIRBG_LANG() : 'bg'; }
  function num(v) {
    return new Intl.NumberFormat(lang(), { maximumFractionDigits: 0 }).format(v);
  }

  // The scale for the metric in hand, as served (§2.1).
  window.AIRBG_SCALE = function () {
    var d = window.AIRBG_DATA;
    if (!d) return null;
    return metric === 'p10' ? d.scale_p1_eaqi : d.scale_p2_eaqi;
  };

  /* The legend's ranges are written from the served bands, not from markup.
   * They used to be six hard-coded strings — correct for PM2.5 and silently
   * wrong for anything else. Whatever composes a string at runtime owns it
   * (§5.12), so the caption lost its data-i18n key too. */
  function paintLegend() {
    var bands = window.AIRBG_SCALE();
    if (!bands) return;

    var caption = document.querySelector('[data-od-id="legend-unit"]');
    if (caption) {
      caption.textContent = t('legend.unitOf', {
        metric: t(metric === 'p10' ? 'col.pm10Short' : 'col.pm25Short')
      });
    }

    var cells = document.querySelectorAll('[data-od-id="map-legend"] .scale__band-range');
    for (var i = 0; i < cells.length && i < bands.length; i++) {
      var lower = i ? bands[i - 1].upper : 0;
      cells[i].textContent = bands[i].upper == null
        ? num(lower) + '+'
        : num(lower) + '–' + num(bands[i].upper);
    }
  }

  function apply() {
    // The frame carries the state for whoever mounts Leaflet — the same seam
    // as data-focus-oblast and data-sensor-filter.
    frame.setAttribute('data-metric', metric);
    paintLegend();
    document.dispatchEvent(new CustomEvent('airbg:metricchange', { detail: metric }));
  }

  set.addEventListener('change', function (e) {
    var r = e.target;
    if (!r || r.name !== 'metric' || !r.checked) return;
    metric = r.value === 'pm10' ? 'p10' : 'p2';
    apply();
  });

  // The markup ships one checked radio; start from it rather than assuming.
  var checked = set.querySelector('input[name="metric"]:checked');
  if (checked) metric = checked.value === 'pm10' ? 'p10' : 'p2';
  frame.setAttribute('data-metric', metric);

  // The scale arrives with the data, so the legend is painted then and on
  // every language change (the caption and the numbers are both localised).
  document.addEventListener('airbg:datachange', paintLegend);
  document.addEventListener('airbg:languagechange', paintLegend);
  if (window.AIRBG_DATA) paintLegend();
})();
