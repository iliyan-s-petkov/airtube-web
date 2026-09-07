# Map rendering

Why the map looks the way it does. Constants live in `web/src/lib/hexes.js`,
`web/src/islands/map.js` and `airbg.yaml`; this is the reasoning behind them.

## Cell size — `TARGET_HEX_PX = 32`

The tiered grid exists so a cell stays about the same size on screen at every
zoom: `resolutionForZoom` asks the server for whatever ground distance draws at
`TARGET_HEX_PX`, and the server snaps that to its nearest published tier.

The number is bounded on both sides. Below ~24 px a cell cannot hold the two-
or three-character reading printed inside it. Above ~36 px the grid stops being
an overlay: at 50 px the Balkans view drew about nine cells across Bulgaria, a
sheet of colour with the ground barely visible between the outlines. 32 is the
middle, and `hexes.test.js` pins both limits.

Lowering it costs bandwidth — a finer tier means more bins in the response.

## Where the grid starts and stops

`GRID_MIN_ZOOM` is the first zoom at which a cell of the **coarsest published
tier** is still at least `MIN_HEX_PX` (8 px) wide. It is deliberately not "the
zoom at which the coarsest tier is as big as this zoom would ideally like" —
answering that question turned the grid off at the very zoom the country fits
on screen.

`TARGET_HEX_PX` cancels out of that derivation; only the coarsest tier and
`MIN_HEX_PX` move it.

`POINT_TIER_MIN_ZOOM` is the other end: the zoom at which the wanted resolution
passes the finest published tier (0.25 km), past which the map asks for one
feature per device instead of bins.

Both handovers are used by two layers each — the one appearing and the one
disappearing. `hexesURL` picks a tier from `Math.round(zoom)` while MapLibre
applies `minzoom`/`maxzoom` to the true fractional zoom, so the layer ranges
use the `*_FRACTIONAL` constants, half a level down, where the rounding flips.

## Basemap

The OpenStreetMap raster is the ground. Over it the vector archive's
**non-fill, non-background** layers are drawn for detail and POIs.

Fills and the background are why the archive cannot be the basemap on its own:
they are clipped opaque polygons, so outside the Bulgaria extract they paint a
country-shaped rectangle over the world, sever the Danube and blank the sea.
Lines, symbols and circles cover only what they trace, so outside the extract
they draw nothing and the raster shows through.

A raster-only style must still declare `glyphs`, derived from the basemap URL
by string surgery — `new URL()` percent-encodes the `{fontstack}` braces and
MapLibre 404s. Without glyphs every symbol layer silently stops drawing:
marker labels, cell values and wind arrows all disappear at once.

## Cell fill and outline

`hex_opacity` (`airbg.yaml`) is 0.75 and the outline is the label ink at
1.2 px / 0.7 opacity. White outlines were invisible over a pale raster.
