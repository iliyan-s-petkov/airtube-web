// Which data source a zoom level may read. This is the anti-scraping design
// expressed client-side: the map picks a TIER, never a viewport query, because
// no endpoint accepts a bounding box.
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
