import { parseMetricList, splitAttr, byMetric } from './metrics.js'
import { LAYER_ORDER } from './maplayers.js'

// Whether the colour key is unrolled. Its own key, not part of the layers
// menu's state: the menu decides whether the key exists, this decides whether
// it is folded, and conflating them would make turning the key back on undo a
// fold the reader never touched.
export const LEGEND_FOLD_KEY = 'airbg:legend-open'

// Remembered, like the legend fold: a reader who needs the slow speed to
// follow a cell needs it every visit, not once.
export const PLAY_SPEED_KEY = 'airbg:play-speed'

// The camera's own floor, and the reason the zoom stack can be honest about it.
// MapLibre keeps TWO minimums: the setting getMinZoom() reports (0 by default)
// and the floor Transform._constrain silently enforces so the world still
// covers the container — around 0.2 on a hero-height map. Left at the default,
// getZoom() bottoms out at the constrained floor while getMinZoom() keeps
// saying 0, so installZoom's `z <= getMinZoom()` never fires and the minus
// button stays live over a camera that has stopped moving. Setting it makes the
// reported floor the reachable one. It used to be 5 — "a map of one country,
// nothing below it to zoom out to" — which stopped a reader from putting
// Bulgaria in its neighbourhood. 2 shows the continent and then some, and is
// kept off 0 because MapLibre's own constrained floor sits near 0.2 on a
// hero-height map, which would put the reported floor back out of reach.
export const MIN_ZOOM = 2

// MapLibre's own maxzoom default. setLayerZoomRange takes both ends, so a call
// that only means to move the floor still has to name a ceiling.
export const MAX_ZOOM_CEILING = 24

// 'street-names' -> 'tLayerStreetNames', the dataset spelling of
// data-t-layer-street-names. Exported for its own test: it is the one place the
// group keys and the template's attribute names have to agree, and they agree
// by rule rather than by two lists kept in step by hand.
//
// Moved here with readConfig, the only caller: it stays behind map.js's other
// exports but readConfig needs it to build the config object below.
export function layerLabelKey(group) {
  const camel = group.split('-').map((p) => p.charAt(0).toUpperCase() + p.slice(1)).join('')
  return `tLayer${camel}`
}

export function readConfig(el) {
  const d = el.dataset
  return {
    slug: d.slug || null,
    // No fallbacks: the opening view is configuration
    // (frontend.default_zoom/default_lon/default_lat, or the area's own
    // centre), and the server renders all three on every map island. A
    // hardcoded 7/25.4858/42.7339 here would numerically agree with today's
    // airbg.yaml while masking a server that stopped rendering them.
    zoom: Number(d.zoom),
    lon: Number(d.lon),
    lat: Number(d.lat),
    // No fallback: series.default_metric is configuration. A hardcoded 'P2'
    // here would silently mask a missing data-metric attribute AND would be
    // the exact duplicated constant this phase removes — the server always
    // renders data-metric now (see internal/web/render.go), so a missing
    // attribute must surface as undefined, not a quiet default.
    metric: d.metric,
    // The full metric list (upstream.CanonicalMetrics, server-rendered) that
    // getViewState needs to validate a metric before adopting it — same
    // attribute, same parseMetricList, as the switcher island reads. No
    // fallback beyond what parseMetricList itself already gives a blank/
    // missing attribute ([]): a second default list here would be the
    // duplicated-constant problem series.default_metric's comment above is
    // about, one metric list instead of one metric.
    metrics: parseMetricList(d.metrics),
    // What the key calls the metric it is a key to, and what that metric is
    // measured in. Zipped into lookups here rather than kept as two positional
    // arrays, because the legend asks by metric name and never by index — and
    // an index that has to be looked up first is the off-by-one zipLabels
    // exists to prevent. Both fall back per-metric inside legendTitle.
    metricLabels: byMetric(parseMetricList(d.metrics), splitAttr(d.metricLabels)),
    metricUnits: byMetric(parseMetricList(d.metrics), splitAttr(d.metricUnits)),
    basemap: d.basemap || '',
    // Server-rendered: the language set is data, so no expression here could
    // tell a language segment from a page segment. "" is the default language.
    langPrefix: d.langPrefix || '',
    // Which of the band table's two shipped label languages to show. Read off
    // <html lang>, which base.gohtml already renders, rather than derived from
    // langPrefix — the default language has an empty prefix.
    // ownerDocument ?? document because readConfig is duck-typed on `dataset`
    // and its tests pass a plain object rather than a mounted element.
    lang: (el.ownerDocument ?? document).documentElement.lang,
    // Paint values and zoom thresholds: configuration, arriving as data-*
    // attributes, no fallback here — a hardcoded fallback that numerically
    // agrees with today's airbg.yaml is exactly the duplicated constant this
    // phase removes.
    noDataColour: d.noDataColour,
    unscaledColour: d.unscaledColour,
    markerStrokeColour: d.markerStrokeColour,
    // A WebGL paint value is configuration, not CSS: no rule can reach a
    // canvas layer, which is why every colour this island paints with arrives
    // as a data-* attribute. An earlier version read --fg through
    // getComputedStyle with a hex fallback; literals.test.js caught the
    // fallback, and it was right to — the fallback was the tell that the value
    // was coming from the wrong place.
    labelColour: d.markerLabelColour,
    emptyBasemapColour: d.emptyBasemapColour,
    // How solidly the hex grid paints. A paint value like the colours above,
    // and server-rendered for the same reason: no CSS rule reaches a WebGL
    // layer, and a fallback here that agreed with today's airbg.yaml would hide
    // a server that stopped rendering the attribute.
    hexOpacity: Number(d.hexOpacity),
    // One positional list, in WINDOW_CHOICES order — the same idiom as
    // data-metric-labels, and for the same reason a per-window attribute cannot
    // work: data-t-window-24h arrives in the dataset as tWindow-24h.
    windowLabels: splitAttr(d.tWindows),
    zoomCity: Number(d.zoomCity),
    zoomSensor: Number(d.zoomSensor),
    // Strings come from the server, not from a JS catalogue: Go owns the
    // catalogue, and a second copy here would drift on the first edit.
    t: {
      legend: d.tLegend || '',
      // The name of the fold, not of the key: the summary is icon-only, and an
      // icon-only control still has to be announced as something.
      legendToggle: d.tLegendToggle || '',
      legendNoData: d.tLegendNoData || '',
      // The (i) beside the key and what its dialog says: the name of the
      // button, the name of the outbound link, and the standing indicative-data
      // disclaimer, which belongs anywhere the bands are explained.
      legendAbout: d.tLegendAbout || '',
      legendSource: d.tLegendSource || '',
      disclaimer: d.tDisclaimer || '',
      close: d.tClose || '',
      // Keyed by the tier names tierFor returns, so the lookup in showLegend is
      // a direct index rather than a branch that could drift from tier.js.
      tier: {
        country: d.tTierCountry || '',
        city: d.tTierCity || '',
        sensors: d.tTierSensors || '',
      },
      // Two names for one button: what it will do next, not what state it is
      // in — aria-pressed already reports the state.
      fullscreen: d.tFullscreen || '',
      fullscreenExit: d.tFullscreenExit || '',
      zoomIn: d.tZoomIn || '',
      zoomOut: d.tZoomOut || '',
      zoomReset: d.tZoomReset || '',
      windowLabel: d.tWindowLabel || '',
      playLabel: d.tPlayLabel || '',
      speedLabel: d.tSpeedLabel || '',
      pauseLabel: d.tPauseLabel || '',
      timeLabel: d.tTimeLabel || '',
      exitLabel: d.tExitLabel || '',
      // The coverage guard's two sentences: one hour thinner than the rest of
      // the animation, and a measurement with no history to animate at all.
      replayThin: d.tReplayThin || '',
      replayNoHistory: d.tReplayNoHistory || '',
      layersButton: d.tLayersButton || '',
      layersCaption: d.tLayersCaption || '',
      viewLegend: d.tViewLegend || '',
      viewBasemap: d.tViewBasemap || '',
      viewCellValues: d.tViewCellValues || '',
      viewInactiveSensors: d.tViewInactiveSensors || '',
      // Its own string, not map.layer.boundaries: that one names the basemap's
      // administrative lines, which are a different set of lines from a
      // different source and switch independently.
      viewBoundaries: d.tViewBoundaries || '',
      viewCommunitySensors: d.tViewCommunitySensors || '',
      viewOfficialStations: d.tViewOfficialStations || '',
      notMeasured: d.tNotMeasured || '',
      communitySensors: d.tViewCommunitySensors || '',
      officialStations: d.tViewOfficialStations || '',
      // One label per style group, keyed by the group's own name so the menu
      // can look up whatever the style turns out to carry. Derived from
      // LAYER_ORDER rather than written out, because the attribute name is a
      // mechanical transform of the key — data-t-layer-street-names becomes
      // d.tLayerStreetNames — and writing both would be two spellings of one
      // fact. A group with no string falls back to its key at render time.
      layers: Object.fromEntries(LAYER_ORDER.map((g) => [g, d[layerLabelKey(g)] || ''])),
      hint: d.tHint || '',
      noSources: d.tNoSources || '',
      rateLimited: d.tRateLimited || '',
      unavailable: d.tUnavailable || '',
      unscaled: d.tUnscaled || '',
      locateButton: d.tLocateButton || '',
      locateDenied: d.tLocateDenied || '',
      locateFailed: d.tLocateFailed || '',
      windToggle: d.tWindToggle || '',
      windAbout: d.tWindAbout || '',
      windNote: d.tWindNote || '',
      windAttribution: d.tWindAttribution || '',
    },
  }
}
