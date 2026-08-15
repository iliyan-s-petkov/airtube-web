// colourFor picks the first band whose inclusive upper bound is at or above the
// value. bands come verbatim from /api/v1/scales, ascending, with the top band's
// upper === null meaning "open ended".
//
// noDataColour is passed in, not defined here: it arrives from the server as a
// data-* attribute, because this project keeps no defaults in code. Band colours
// still come only from /api/v1/scales, so a legislative change stays a one-file
// server edit rather than a frontend release.
export function colourFor(value, bands, noDataColour) {
  if (value === null || value === undefined || Number.isNaN(value)) return noDataColour
  if (!bands || bands.length === 0) return noDataColour
  for (const band of bands) {
    // upper is INCLUSIVE: a value exactly on a boundary belongs to the lower
    // band. `upper == null` catches both null and undefined and is the open top.
    if (band.upper == null || value <= band.upper) return band.colour
  }
  // Reachable only if the scale has no open top band, which would be a server
  // bug. The no-data colour rather than the last band's: better to show
  // "unknown" than to assert a band the scale does not actually claim.
  return noDataColour
}
