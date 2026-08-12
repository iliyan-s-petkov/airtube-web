// The colour for "we have no usable reading". Neutral grey, deliberately not a
// band colour: an area below the coverage threshold, or one with no data at all,
// must not be paintable as clean air.
//
// This and chrome colours are the only literal colours permitted in web/src —
// every band colour comes from /api/v1/scales, so a legislative change is a
// one-file server edit rather than a frontend release.
export const NO_DATA_COLOUR = '#9ca3af'

// colourFor picks the first band whose inclusive upper bound is at or above the
// value. bands come verbatim from /api/v1/scales, ascending, with the top band's
// upper === null meaning "open ended".
export function colourFor(value, bands) {
  if (value === null || value === undefined || Number.isNaN(value)) return NO_DATA_COLOUR
  if (!bands || bands.length === 0) return NO_DATA_COLOUR
  for (const band of bands) {
    // upper is INCLUSIVE: a value exactly on a boundary belongs to the lower
    // band. `upper == null` catches both null and undefined and is the open top.
    if (band.upper == null || value <= band.upper) return band.colour
  }
  // Reachable only if the scale has no open top band, which would be a server
  // bug. Grey rather than the last band's colour: better to show "unknown" than
  // to assert a band the scale does not actually claim.
  return NO_DATA_COLOUR
}
