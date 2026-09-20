// The map-driving seam between the map and boundaries.js (pure geometry:
// boundaryFillPaint, boundsOf, findBoundary, the source/layer ids). Kept
// separate, same as mapwind.js/wind.js, so the pure module stays importable
// on its own. Named to make the pairing with boundaries.js obvious.
import { getJSON } from './api.js'
import { nearestArea } from './nearest.js'
import {
  BOUNDARY_SOURCE_ID, BOUNDARY_SELECTED_LAYER_ID, BOUNDARY_LAYER_IDS, selectedFilter,
} from './boundaries.js'

// Padding in pixels around a province fitted into the frame. Enough that the
// outline the reader just selected is not flush against the edge of the map,
// where the highlight it was given would be half a line wide.
export const BOUNDARY_FIT_PADDING = 24

// queryRenderedFeatures over whichever of the named layers the map actually
// carries. MapLibre throws on a layer id it does not know, and every caller
// here runs on a map whose layers were added in an async 'load' handler that
// may not have reached them yet.
export function hit(map, point, layers) {
  const present = layers.filter((id) => map.getLayer?.(id))
  if (!present.length) return []
  return map.queryRenderedFeatures(point, { layers: present }) ?? []
}

// boundaryChoice is the whole decision behind a click on open ground: the
// province under the pointer, or nothing.
//
// Nothing on a map already scoped to one area — /area/{slug} is one province,
// ever, and there is nothing to drill into — which is the same rule cellArea
// applies to the cells, for the same reason. Separated from the handler because
// the handler needs a real MapLibre instance the "no jsdom" rule puts out of
// reach.
export function boundaryChoice(state, feature) {
  if (state.slug) return null
  return feature?.properties?.slug || null
}

// highlightBoundary is the selection, expressed as a filter on the heavy
// outline layer. One write, no geometry: the selected province is already in
// the source.
export function highlightBoundary(map, slug) {
  if (!map.getLayer?.(BOUNDARY_SELECTED_LAYER_ID)) return
  map.setFilter(BOUNDARY_SELECTED_LAYER_ID, selectedFilter(slug))
}

// setBoundaries is the outline control: fetch once, then show or hide.
//
// The same shape as setWind, and for the same reasons — it returns the state
// actually reached so a failed fetch corrects the checkbox rather than leaving
// it ticked over a map with no outlines on it, and it takes the state asked for
// rather than flipping the one it finds.
//
// A failed fetch leaves the outlines off and raises no banner: the readings are
// what the page is for, and they are all still there.
export async function setBoundaries(map, state, bstate, on, fetchJSON = getJSON) {
  if (!on) {
    bstate.on = false
    setBoundaryVisibility(map, 'none')
    return false
  }
  if (bstate.loading) return bstate.on
  if (!bstate.body) {
    bstate.loading = true
    try {
      bstate.body = await fetchJSON('/api/v1/boundaries')
    } catch {
      setBoundaryVisibility(map, 'none')
      return false
    } finally {
      bstate.loading = false
    }
  }
  map.getSource?.(BOUNDARY_SOURCE_ID)?.setData(bstate.body)
  // On /area/{slug} the province is already chosen, so the outlines arrive with
  // that one already picked out.
  highlightBoundary(map, state.slug)
  setBoundaryVisibility(map, 'visible')
  bstate.on = true
  return true
}

function setBoundaryVisibility(map, visibility) {
  for (const id of BOUNDARY_LAYER_IDS) {
    if (map.getLayer?.(id)) map.setLayoutProperty(id, 'visibility', visibility)
  }
}

// cellArea decides which area an aggregate cell click selects: the one whose
// centroid the click falls nearest.
//
// Returns null rather than a slug in the three cases where selecting anything
// would be wrong — a click MapLibre reported no position for, a map already
// scoped to one area (there is nothing to drill into), and a map that has not
// yet loaded the area list. Separated from the handler because that is the
// whole decision, and the handler around it needs a real MapLibre instance the
// "no jsdom" rule puts out of reach.
export function cellArea(state, lngLat) {
  if (!lngLat || state.slug) return null
  return nearestArea([lngLat.lng, lngLat.lat], state.areas)?.slug ?? null
}
