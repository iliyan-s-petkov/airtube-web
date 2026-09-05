// Which data source a zoom level may read. This is the anti-scraping design
// expressed client-side: the map picks a TIER, and the tier endpoints answer
// whole-country or whole-area, never "everything inside this rectangle".
//
// /api/v1/hexes is the one endpoint that does take a bbox, and it is not a tier
// (see lib/hexes.js). It serves pre-binned aggregates whose finest cell is
// published country-wide anyway, so clipping it to a viewport reveals nothing a
// caller could not already download in one request.
//
// Boundaries are `<`, not `<=`, and each one is pinned by its own assertion. At
// the sensor boundary an off-by-one is the difference between one cached country
// aggregate and a per-area request that spends enumeration budget. The
// thresholds are configuration and arrive as data-* attributes.
export function tierFor(zoom, zoomCity, zoomSensor) {
  if (zoom < zoomCity) return 'country'
  if (zoom < zoomSensor) return 'city'
  return 'sensors'
}
