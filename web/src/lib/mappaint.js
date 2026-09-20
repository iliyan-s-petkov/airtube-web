import { rampValueStops } from './ramp.js'
import { DIAMOND_RADIUS_PX } from './markericon.js'
import { OFFICIAL_SOURCE } from './sourcefilter.svelte.js'
import {
  GRID_MIN_ZOOM_FRACTIONAL, POINT_TIER_MIN_ZOOM_FRACTIONAL,
} from './hexes.js'
import { OFFICIAL_IMAGE_ID } from './mapids.js'

// How far a held reading is faded. Low enough to read as held, high enough to
// stay legible over every band.
export const CARRIED_OPACITY = 0.55

// The two steps a newly arrived cell climbs before it is drawn like any other.
// Both sit above CARRIED_OPACITY so a fading-in reading never reads as a held
// one, which is a different fact about the same cell.
export const FRESH_OPACITY = 0.6
export const SETTLING_OPACITY = 0.8

// markerMaxZoom: the zoom at which the dots hand over, for the tier the dots
// currently ARE.
//
// The two tiers hand over to different things. Province and city markers are
// aggregates, and so is the hex grid — one reading per bin instead of one per
// province, but the same kind of claim about the same ground. Drawing both left
// the map with 15 km cells and labelled aggregate dots on top of them, and with
// dots in places (Перник, Банкя) where the grid had no bin at all: two answers
// to one question, disagreeing. So an aggregate marker steps aside the moment
// the grid appears.
//
// A sensor marker does not: it is a device at its own coordinate, which the
// grid does not draw until the point tier, and it is the thing a reader clicks
// to open a panel. It runs to the point-tier handover as it always did.
export function markerMaxZoom(tier) {
  return tier === 'sensors' ? POINT_TIER_MIN_ZOOM_FRACTIONAL : GRID_MIN_ZOOM_FRACTIONAL
}

// hexOutlinePaint: the border between one cell and the next.
//
// Drawn in the label ink — a dark colour — and NOT in either of the two that
// have already been tried and failed. The cell's own `colour` outlines a fill
// in the fill's own colour, which is by definition invisible. The marker stroke
// is white, which is what lifts a dot off a dark map but disappears completely
// into a pale one: over the OSM raster it left the cells edgeless, floating.
//
// A cell has to win against a busy street map, because the reading is what the
// page is for. Full width and most of the way opaque; the streets stay legible
// around the cell and through its fill.
export function hexOutlinePaint(cfg) {
  return {
    'line-color': cfg.labelColour,
    'line-width': 1.2,
    'line-opacity': 0.7,
  }
}

// bandsFor picks the scale table for one metric. The scales endpoint returns an
// array of tables; matching on `metric` rather than on array position means a
// reordered response cannot silently recolour the map.
//
// The scale's ceiling rides on its top band. The ceiling belongs to the scale,
// not to any one band, but the only band it can change is the open one at the
// top — and every consumer downstream (the ramp, the key) is handed bands, not
// scales. Carrying it here rather than widening four signatures keeps the
// ceiling one hop from the band whose width it sets.
export function bandsFor(scales, metric) {
  if (!Array.isArray(scales)) return []
  const scale = scales.find((s) => s.metric === metric)
  const bands = scale?.bands ?? []
  if (bands.length === 0 || scale?.ceiling == null) return bands
  return bands.map((band, i) =>
    i === bands.length - 1 ? { ...band, ceiling: scale.ceiling } : band,
  )
}

export function hexLabelPaint(cfg) {
  return {
    ...labelPaint(cfg),
    // carried is tested first so the mute wins outright rather than by
    // evaluation luck: a held reading has a previous value by definition, so it
    // can never also be an arrival.
    'text-opacity': [
      'case',
      ['==', ['get', 'carried'], true], CARRIED_OPACITY,
      ['==', ['get', 'fresh'], 0], FRESH_OPACITY,
      ['==', ['get', 'fresh'], 1], SETTLING_OPACITY,
      1,
    ],
  }
}

//
// Named layerPaint, not markerPaint (its name before this task): 'circle-
// color' here is only ever a placeholder — the source is empty at addLayer
// time (see emptyCollection), and by the time features exist, onMetricChange
// has already replaced 'circle-color' via setPaintProperty with the real,
// metric-aware expression from markerPaint below. Two functions named
// markerPaint, one returning a full paint object and one returning a single
// paint VALUE, would have been the same kind of silent ambiguity this file's
// other comments warn about elsewhere.
export function layerPaint(cfg) {
  return {
    'circle-color': ['get', 'colour'],
    'circle-radius': MARKER_RADIUS,
    'circle-stroke-width': 1,
    'circle-stroke-color': cfg.markerStrokeColour,
  }
}

// The circle layer draws every marker the diamond layer does not. An area
// marker carries no source at all, and ['get'] on an absent property is null,
// which is not the official one — so the areas stay where they were.
export const NOT_OFFICIAL = ['!=', ['get', 'source'], OFFICIAL_SOURCE]

const MARKER_RADIUS = ['interpolate', ['linear'], ['zoom'], 5, 5, 12, 9]

// The diamond raster is drawn at twice its nominal size, as the wind arrow is,
// so it stays sharp on a retina screen and when icon-size scales it past 1.
export const MARKER_PIXEL_RATIO = 2

// officialLayout/officialPaint: the diamond drawn at the radius the circles
// use, so the two networks read as one population at one size and differ only
// in shape. icon-size 1 puts the glyph's point at DIAMOND_RADIUS_PX image
// pixels, which pixelRatio 2 halves into CSS px — so the stops are the circle
// radii over that, and a change to either end stays a change to one number.
export function officialLayout() {
  const unit = DIAMOND_RADIUS_PX / MARKER_PIXEL_RATIO
  return {
    'icon-image': OFFICIAL_IMAGE_ID,
    'icon-size': ['interpolate', ['linear'], ['zoom'], 5, 5 / unit, 12, 9 / unit],
    // Markers may overlap; dropping one would silently hide a station rather
    // than a label, which is not a trade the label layer's rule was making.
    'icon-allow-overlap': true,
    'icon-ignore-placement': true,
  }
}

export function officialPaint(cfg) {
  return {
    'icon-color': ['get', 'colour'],
    'icon-halo-color': cfg.markerStrokeColour,
    'icon-halo-width': 1,
  }
}

// labelLayout and labelPaint are the symbol layer that prints each area's
// reading beside its dot — the non-colour channel for the air-quality scale.
//
// text-allow-overlap stays FALSE, which is the whole crowding strategy: rather
// than inventing a zoom threshold to guess when labels start colliding,
// MapLibre drops the ones that would overlap and keeps the rest. The map thins
// itself out as areas converge, and no number is ever drawn on top of another.
//
// The fontstack is the one the basemap style already ships (the tiles are an
// OpenMapTiles build whose own label layers use Noto Sans Regular), so this
// adds no font asset and no new origin.
export function labelLayout(cfg) {
  return {
    // One decimal, matching the province list and the sensor panel: three
    // surfaces showing the same reading to different precision would be three
    // surfaces disagreeing. number-format localises the separator, so this
    // reads 12,4 in Bulgarian and 12.4 in English without a second formatter.
    'text-field': [
      'number-format',
      ['get', 'value'],
      { locale: cfg.lang || 'bg', 'min-fraction-digits': 1, 'max-fraction-digits': 1 },
    ],
    'text-font': ['Noto Sans Regular'],
    'text-size': 11,
    // Beside the dot, not on it: the circle is 5-9px, so a centred number
    // would sit on its own stroke and lose contrast against every band colour.
    'text-offset': [0.9, 0],
    'text-anchor': 'left',
    'text-allow-overlap': false,
    'text-ignore-placement': false,
    'text-optional': true,
  }
}

// hexLabelLayout is the same number, printed in the middle of a cell instead
// of beside a dot. It reuses labelLayout's formatting — one decimal, the
// basemap's own fontstack, overlap thinning — and differs only in placement:
// there is no dot to clear, so the number is centred on the cell it describes.
export function hexLabelLayout(cfg) {
  const { 'text-offset': _offset, 'text-anchor': _anchor, ...shared } = labelLayout(cfg)
  return { ...shared, 'text-anchor': 'center' }
}

export function labelPaint(cfg) {
  return {
    'text-color': cfg.labelColour,
    // The halo is what makes the number legible on every band from the palest
    // teal to the darkest purple, and over the basemap's own streets, without
    // re-tinting the served colour underneath it.
    'text-halo-color': cfg.markerStrokeColour,
    'text-halo-width': 1.4,
  }
}

// markerPaint is the circle layer's 'circle-color' paint VALUE for one
// metric — recomputed on every metric switch and applied via
// map.setPaintProperty, never at layer-creation time (see layerPaint above).
//
// Three different facts must not share one colour: "no reading" (grey,
// noDataColour), "this metric has no band table" (unscaledColour), and a
// real band value. An unscaled metric still distinguishes the first two —
// only the third collapses, because there is no per-value meaning left to
// draw once there are no bands. A flat unscaledColour return for the whole
// !scaled branch was tried and rejected: it paints "no reading" and "has a
// reading" identically, so on a metric most sensors don't report (e.g.
// temperature), the map reads as full coverage when it is not — the same
// class of defect this file's rampColour/noDataColour split exists to
// prevent for scaled metrics. ['has', 'value'] (the shape this task's brief
// originally suggested) is ALSO wrong here, for a reason worth stating
// loudly: areaFeatures and sensorFeatures always set the `value` key, even
// when its content is null (`value: a.covered ? ... : null` /
// `column[i] ?? null`), so `has` is true unconditionally and that branch
// would be dead code, always taking the "has a reading" side.
export function markerPaint(bands, { noDataColour, unscaledColour, scaled }) {
  if (!scaled) return ['case', ['==', ['get', 'value'], null], noDataColour, unscaledColour]

  // `interpolate`, not `step`: the markers are on the same scale as the hexes
  // and must not be the one thing on the map still painted in categories.
  //
  // The stops come from rampValueStops, which puts one at every point where the
  // ramp's slope changes, so a linear blend between them is the same colour
  // rampColour computes for that value — a dot and the cell under it agree.
  // Computed here rather than per feature for the reason onMetricChange gives:
  // switching metric must not re-walk every feature.
  const stops = rampValueStops(bands)
  if (stops.length < 2) return ['case', ['==', ['get', 'value'], null], noDataColour, unscaledColour]
  return [
    'case',
    ['==', ['get', 'value'], null], noDataColour,
    ['interpolate', ['linear'], ['get', 'value'], ...stops.flatMap((s) => [s.value, s.colour])],
  ]
}
