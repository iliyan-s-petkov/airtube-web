// Nearest area, computed IN THE BROWSER. The precise-GPS path must never send
// a coordinate to the server: /api/v1/locate exists for coarse placement and
// takes no body, and there is no endpoint that accepts a point — by design,
// since one would be a bounding-box query wearing a hat.
//
// Equirectangular approximation, not haversine: over Bulgaria's ~500 km extent
// the error is far below the distance between two oblast centroids, and it is
// one line instead of five.
export function nearestArea([lon, lat], areas) {
  if (!areas || areas.length === 0) return null
  const k = Math.cos((lat * Math.PI) / 180) // longitude degrees shrink with latitude
  let best = null
  let bestD = Infinity
  for (const a of areas) {
    const dx = (a.lon - lon) * k
    const dy = a.lat - lat
    const d = dx * dx + dy * dy // squared: monotonic, so no sqrt needed
    if (d < bestD) { bestD = d; best = a }
  }
  return best
}
