// Which data source a zoom level may read. This is the anti-scraping design
// expressed client-side: the map picks a TIER, never a viewport query, because
// no endpoint accepts a bounding box.
//
// Boundaries are `<`, not `<=`, and each one is pinned by its own assertion. At
// z=11 an off-by-one is the difference between one cached country aggregate and
// a per-area request that spends enumeration budget.
export function tierFor(zoom) {
  if (zoom < 9) return 'country'
  if (zoom < 11) return 'city'
  return 'sensors'
}
