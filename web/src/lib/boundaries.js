// The province outlines: where one oblast ends and the next begins.
//
// The map already answers "which province is this reading in" by nearest
// centroid — a rule that is right almost everywhere and visibly wrong along
// every border, because a centroid has no edges. Drawing the real edges makes
// the answer checkable, and makes the border itself something a reader can aim
// at: click inside an outline and that province is the one selected.
//
// The geometry comes from /api/v1/boundaries, which carries no bounds of its
// own — boundsOf below derives them from the rings, because flying to a
// selected province means fitting its extent, not its centre.

export const BOUNDARY_SOURCE_ID = 'airbg-boundaries'
// Three layers over one source. The fill is the hit target and paints nothing:
// a line is a few pixels wide and a reader aiming at a province aims at the
// province, not at its edge. The outline is what they see. The highlight is the
// same outline drawn heavier, filtered to the selected slug, so a selection is
// one filter write rather than a second copy of the geometry.
export const BOUNDARY_FILL_LAYER_ID = 'airbg-boundary-hit'
export const BOUNDARY_LINE_LAYER_ID = 'airbg-boundary-line'
export const BOUNDARY_SELECTED_LAYER_ID = 'airbg-boundary-selected'

export const BOUNDARY_LAYER_IDS = [
  BOUNDARY_FILL_LAYER_ID, BOUNDARY_LINE_LAYER_ID, BOUNDARY_SELECTED_LAYER_ID,
]

// fill-opacity 0, not visibility 'none': MapLibre hit-tests a layer it is
// asked to draw whatever the paint makes of it, and an invisible fill is the
// standard way to give a line a body worth clicking. The colour is irrelevant
// at zero opacity and is the label ink only because a paint property has to
// name something.
export function boundaryFillPaint(cfg) {
  return { 'fill-color': cfg.labelColour, 'fill-opacity': 0 }
}

// Thinner and fainter than the hex outline, which is deliberate: the grid is
// the measurement and this is the frame around it. An administrative line that
// competed with the readings would be the map arguing with itself.
export function boundaryLinePaint(cfg) {
  return { 'line-color': cfg.labelColour, 'line-width': 1, 'line-opacity': 0.45 }
}

// The selected province, drawn over its own faint outline in the same ink at
// full weight. Weight and opacity carry the selection rather than a second
// colour, so it reads the same on a pale raster and a dark one — and a reader
// who cannot separate the two colours still sees which outline thickened.
export function boundarySelectedPaint(cfg) {
  return { 'line-color': cfg.labelColour, 'line-width': 3, 'line-opacity': 1 }
}

// The filter for the highlight layer. A slug of null matches nothing, which is
// how "no province selected" is expressed — MapLibre has no "draw none of it"
// short of hiding the layer, and hiding it would fight the reader's own toggle.
export function selectedFilter(slug) {
  return ['==', ['get', 'slug'], slug ?? '']
}

// boundsOf reduces a feature's rings to [[minLon, minLat], [maxLon, maxLat]],
// the pair fitBounds takes. Polygon and MultiPolygon differ only in how deep
// the positions are nested, so the walk is depth-agnostic rather than two
// branches that could disagree.
//
// null for anything with no position in it: a feature the server sent with an
// empty geometry must not become a camera move to [0, 0].
export function boundsOf(feature) {
  let minLon = Infinity, minLat = Infinity, maxLon = -Infinity, maxLat = -Infinity
  const walk = (node) => {
    if (!Array.isArray(node)) return
    if (typeof node[0] === 'number' && typeof node[1] === 'number') {
      if (node[0] < minLon) minLon = node[0]
      if (node[0] > maxLon) maxLon = node[0]
      if (node[1] < minLat) minLat = node[1]
      if (node[1] > maxLat) maxLat = node[1]
      return
    }
    for (const child of node) walk(child)
  }
  walk(feature?.geometry?.coordinates)
  if (minLon === Infinity) return null
  return [[minLon, minLat], [maxLon, maxLat]]
}

// findBoundary picks one province out of the collection by slug, so the click
// handler can fit the shape it selected without asking the map to re-query.
export function findBoundary(body, slug) {
  if (!slug) return null
  return (body?.features ?? []).find((f) => f?.properties?.slug === slug) ?? null
}
