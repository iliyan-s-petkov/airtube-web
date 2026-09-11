// The official-station marker, generated rather than shipped: an SDF glyph so
// one image serves every colour the scale produces and every outline width,
// which a PNG per band could not. Same no-canvas, no-dependency technique as
// islands/wind.js's arrowImage — see that file for why a raster is built by
// hand here.
//
// SDF encoding is MapLibre's (TinySDF's): alpha = 255 - 255 * (d/SPREAD + 0.25)
// with d the signed distance in image pixels, so the shape's edge lands at
// alpha 192 — the buffer MapLibre's SDF shader cuts at.

export const DIAMOND_PX = 32
const SPREAD = 8
// Half-diagonal. The rest of the image is margin, which is what the halo draws
// into: a shape flush with its own bitmap has nowhere to put an outline.
export const DIAMOND_RADIUS_PX = 12
const RADIUS = DIAMOND_RADIUS_PX

// Signed distance to the diamond |x| + |y| = RADIUS, negative inside. The
// L1 ball's exact distance is the L1 residual over sqrt(2); at the points the
// nearest edge is a vertex this reads slightly large, which only softens the
// corners by a fraction of a pixel.
function distance(x, y, radius) {
  return (Math.abs(x) + Math.abs(y) - radius) / Math.SQRT2
}

export function diamondImage(size = DIAMOND_PX) {
  const data = new Uint8Array(size * size * 4)
  // Every length is a fraction of the nominal image, so a larger size is the
  // same glyph at a higher resolution and not a fatter one.
  const scale = size / DIAMOND_PX
  const half = size / 2
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const d = distance(x + 0.5 - half, y + 0.5 - half, RADIUS * scale)
      const a = 255 - 255 * (d / (SPREAD * scale) + 0.25)
      const i = (y * size + x) * 4
      data[i + 3] = Math.max(0, Math.min(255, Math.round(a)))
    }
  }
  return { width: size, height: size, data }
}
