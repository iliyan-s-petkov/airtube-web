// @vitest-environment jsdom
//
// readConfig falls back to the global `document` when the object it is
// handed has no ownerDocument (every test here passes a plain object).
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect } from 'vitest'
import { readConfig, layerLabelKey } from '../mapconfig.js'
import { LAYER_ORDER } from '../maplayers.js'

// readConfig reads the server-rendered data-* attributes. Passed a plain
// {dataset} object rather than a real DOM element: readConfig only ever
// touches el.dataset, so this is exactly as pure as any other object-in,
// object-out function here, and stays inside the "no jsdom" rule.
describe('readConfig', () => {
  it('reads the opening view from the server-rendered attributes', () => {
    const cfg = readConfig({ dataset: { zoom: '7', lon: '25.4858', lat: '42.7339' } })
    expect(cfg).toMatchObject({ slug: null, zoom: 7, lon: 25.4858, lat: 42.7339, basemap: '' })
  })

  // The language prefix is server-supplied for any language, not just the two
  // that happen to be embedded — and '' is the default language's real value,
  // not a missing one.
  it('reads the language prefix the server rendered', () => {
    expect(readConfig({ dataset: { langPrefix: '/de' } }).langPrefix).toBe('/de')
    expect(readConfig({ dataset: {} }).langPrefix).toBe('')
  })

  // The opening view is configuration too (frontend.default_zoom/_lon/_lat, or
  // the area's own centre). A JS-side 7/25.4858/42.7339 would agree with
  // today's airbg.yaml by coincidence while hiding a server that stopped
  // rendering the attributes — the same rule data-metric follows below.
  it('has no JS-side default for the opening view', () => {
    const cfg = readConfig({ dataset: {} })
    expect(cfg.zoom).toBeNaN()
    expect(cfg.lon).toBeNaN()
    expect(cfg.lat).toBeNaN()
  })

  // series.default_metric is configuration, not a JS default: a missing
  // data-metric attribute must surface as undefined, never as a silent 'P2'.
  // A hardcoded fallback here would (a) mask a server bug that stops
  // rendering the attribute and (b) be exactly the duplicated constant this
  // phase exists to delete — 'P2' would keep working today by coincidence
  // even if airbg.yaml's series.default_metric changed to something else.
  it('reads metric from data-metric with no JS-side default', () => {
    expect(readConfig({ dataset: {} }).metric).toBeUndefined()
    expect(readConfig({ dataset: { metric: 'P1' } }).metric).toBe('P1')
  })

  it('treats a blank data-basemap as "no basemap configured", not as a broken URL', () => {
    const cfg = readConfig({ dataset: { basemap: '' } })
    expect(cfg.basemap).toBe('')
  })

  // The attribute names the server actually renders on the map island, read
  // off the template rather than kept as a list here: index.gohtml's open tag
  // with its {{template}} partials (mapLayerLabels) spliced in, which is the
  // attribute set the browser hands readConfig.
  function mapIslandAttributes() {
    // import.meta.dirname, not cwd: vitest is run from web/, but this rule is
    // about a file four levels up and must not move when the cwd does.
    const dir = join(import.meta.dirname, '../../../../internal/web/templates')
    const base = readFileSync(join(dir, 'base.gohtml'), 'utf8')
    const page = readFileSync(join(dir, 'index.gohtml'), 'utf8')
    const marker = page.indexOf('data-island="map"')
    expect(marker).toBeGreaterThan(-1)
    const start = page.lastIndexOf('<', marker)
    // Quote-aware: stop at the '>' that closes the open tag, never at one
    // inside a translated string.
    let end = start
    for (let quoted = false; end < page.length; end++) {
      if (page[end] === '"') quoted = !quoted
      else if (page[end] === '>' && !quoted) break
    }
    const tag = page.slice(start, end).replace(/\{\{template\s+"([^"]+)"[^}]*\}\}/g, (_, name) => {
      const define = base.match(new RegExp(`\\{\\{define "${name}"\\}\\}([\\s\\S]*?)\\{\\{end\\}\\}`))
      expect(define, `{{define "${name}"}} in base.gohtml`).not.toBeNull()
      return define[1]
    })
    // The leading (?<![\w-]) is what keeps this off `my-data-t-x`; requiring a
    // trailing [a-z0-9] before the '=' keeps it off a bare `data-t-`.
    return new Set([...tag.matchAll(/(?<![\w-])data-t-([a-z0-9-]*[a-z0-9])=/g)].map((m) => m[1]))
  }

  // The DOM's own attribute-to-dataset rule: a hyphen before an ASCII lowercase
  // letter uppercases it, a hyphen before a digit stays (data-t-window-24h is
  // tWindow-24h, which is why that one is positional instead).
  function datasetKey(attr) {
    const camel = attr.replace(/-([a-z])/g, (_, c) => c.toUpperCase())
    return 't' + camel[0].toUpperCase() + camel.slice(1)
  }

  // Which dataset properties readConfig actually touches, recorded rather than
  // restated: the alias (communitySensors) and the two nested shapes (tier,
  // layers) mean cfg.t's key names are not the attribute names, so the
  // comparison has to happen on the dataset side of readConfig, not after it.
  function datasetKeysReadConfigReads() {
    const seen = new Set()
    const dataset = new Proxy({}, {
      get(_, prop) {
        if (typeof prop === 'string') seen.add(prop)
        return ''
      },
    })
    readConfig({ dataset })
    return seen
  }

  const sorted = (set) => [...set].sort()

  // A rule, not a snapshot. The exact-object toEqual this replaced passed only
  // for one frozen list: it proved someone had edited two literals in step, not
  // that the template and the reader agree. Set equality both ways fails the
  // moment either side gains or loses a string.
  it('reads exactly the data-t-* attributes the template renders on the map island', () => {
    const rendered = new Set([...mapIslandAttributes()].map(datasetKey))
    // t followed by a non-lowercase char: the dataset spelling of data-t-*,
    // and never 'then'/'toString'/'title' that a Proxy also sees.
    const read = new Set(sorted(datasetKeysReadConfigReads()).filter((k) => /^t[^a-z]/.test(k)))
    expect(rendered.size).toBeGreaterThan(40)
    expect(sorted(rendered)).toEqual(sorted(read))
  })

  // The three places where one attribute is not one flat key, which the set
  // comparison above deliberately cannot see.
  it('nests and aliases the translations the lookups index by', () => {
    const cfg = readConfig({
      dataset: {
        tLayerBase: 'Terrain and parks', tLayerStreetNames: 'Street names',
        tTierCountry: 'Each dot is an oblast average',
        tViewCommunitySensors: 'Citizen sensors',
        tViewOfficialStations: 'Official stations',
      },
    })
    // One entry per LAYER_ORDER group, always: the menu looks a label up by the
    // group the STYLE reports, so an absent key is a group rendering under its
    // own slug the day the style starts carrying it.
    expect(Object.keys(cfg.t.layers)).toEqual(LAYER_ORDER)
    expect(cfg.t.layers.base).toBe('Terrain and parks')
    expect(cfg.t.layers['street-names']).toBe('Street names')
    expect(cfg.t.layers.water).toBe('')
    // Keyed by the names tierFor returns, so showLegend indexes rather than branches.
    expect(cfg.t.tier).toEqual({ country: 'Each dot is an oblast average', city: '', sensors: '' })
    // setSourceViewAvailability keys the checkbox label by view id, so the two
    // source labels are read a second time under a second name.
    expect(cfg.t.communitySensors).toBe('Citizen sensors')
    expect(cfg.t.officialStations).toBe('Official stations')
  })

  // One comma-separated attribute, positional against WINDOW_CHOICES, because
  // the per-window alternative would have to spell "24h" as a dataset key and
  // data-t-window-24h converts to tWindow-24h — not reachable with a dot.
  it('reads the window labels positionally from one attribute', () => {
    const cfg = readConfig({ dataset: { tWindows: 'Now,Last 24 hours,Last 48 hours,Last week' } })
    expect(cfg.windowLabels).toEqual(['Now', 'Last 24 hours', 'Last 48 hours', 'Last week'])
  })

  it('reads no window labels when the attribute is absent', () => {
    expect(readConfig({ dataset: {} }).windowLabels).toEqual([])
  })

  // The one place the style's group names and the template's attribute names
  // have to agree. They agree by rule, so the rule is what gets tested: a
  // second hand-kept list is exactly what this function exists to avoid.
  it('turns a style group into the dataset spelling of its label attribute', () => {
    expect(layerLabelKey('water')).toBe('tLayerWater')
    // The hyphenated ones are the whole point: data-t-layer-street-names and
    // data-t-layer-poi-education are where a hand-written mapping would slip.
    expect(layerLabelKey('street-names')).toBe('tLayerStreetNames')
    expect(layerLabelKey('poi-education')).toBe('tLayerPoiEducation')
  })

  // data-metrics is the same attribute (and same parseMetricList) the switcher
  // island reads — getViewState needs the full metric list to validate a
  // metric read from the hash before adopting it.
  it('reads the metric list from data-metrics with parseMetricList\'s own blank-input rule', () => {
    expect(readConfig({ dataset: { metrics: 'P1,P2,temperature' } }).metrics).toEqual(['P1', 'P2', 'temperature'])
    expect(readConfig({ dataset: {} }).metrics).toEqual([])
  })

  // Keyed by metric, not positional: the key looks its caption up by name.
  it('keys the metric names and units by metric', () => {
    const cfg = readConfig({
      dataset: {
        metrics: 'P1,P2,temperature',
        metricLabels: 'PM10,PM2.5,Temperature',
        metricUnits: 'µg/m³,µg/m³,°C',
      },
    })
    expect(cfg.metricLabels).toEqual({ P1: 'PM10', P2: 'PM2.5', temperature: 'Temperature' })
    expect(cfg.metricUnits.temperature).toBe('°C')
  })

  // A metric the server has no unit for is an empty slot, not a missing one:
  // the list stays positional, so every later unit keeps its own metric.
  it('keeps a metric with no unit aligned with the ones after it', () => {
    const cfg = readConfig({
      dataset: { metrics: 'P1,pressure,temperature', metricUnits: 'µg/m³,,°C' },
    })
    expect(cfg.metricUnits).toEqual({ P1: 'µg/m³', pressure: '', temperature: '°C' })
  })

  // Absent attributes: every metric gets '', never undefined, because the
  // caption prints what it is given and "undefined" is a word.
  it('gives every metric an empty string when the attributes are missing', () => {
    const cfg = readConfig({ dataset: { metrics: 'P1,P2' } })
    expect(cfg.metricLabels).toEqual({ P1: '', P2: '' })
    expect(cfg.metricUnits).toEqual({ P1: '', P2: '' })
  })

  it('reads numeric attributes as numbers, not strings', () => {
    const cfg = readConfig({ dataset: { zoom: '12', lon: '25.1', lat: '42.2' } })
    expect(cfg.zoom).toBe(12)
    expect(cfg.lon).toBe(25.1)
    expect(cfg.lat).toBe(42.2)
  })

  // The paint values and zoom thresholds are configuration, not a JS default:
  // readConfig must read the exact server-rendered attribute, not a name that
  // happens to look similar.
  it('reads the frontend paint values and zoom thresholds from their data-* attributes', () => {
    const cfg = readConfig({
      dataset: {
        noDataColour: '#9ca3af',
        unscaledColour: '#94a3b8',
        markerStrokeColour: '#ffffff',
        emptyBasemapColour: '#eef2f5',
        zoomCity: '9',
        zoomSensor: '11',
      },
    })
    expect(cfg.noDataColour).toBe('#9ca3af')
    expect(cfg.unscaledColour).toBe('#94a3b8')
    expect(cfg.markerStrokeColour).toBe('#ffffff')
    expect(cfg.emptyBasemapColour).toBe('#eef2f5')
    expect(cfg.zoomCity).toBe(9)
    expect(cfg.zoomSensor).toBe(11)
  })
})
