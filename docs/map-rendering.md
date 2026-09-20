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

## Map chunk size budget

`web/vite.config.js` fails the build if the `islands/map.js` chunk goes over
290 KB gzipped. MapLibre plus the wind and timelapse code is the bulk of what
the browser downloads for the map, and nothing else in the build watches it.

Two gzip numbers exist for the same file and they disagree. Vite/rolldown's
built-in reporter (`build.reportCompressedSize`) computes its figure natively
in Rust, with compression parameters no JS plugin can read or reproduce; the
guard measures with node:zlib's `gzipSync`. On the same bytes the reporter
said 288.89 KB and node:zlib said 278.68 KB. The reporter is turned off so the
log carries one number, and the guard prints a line for every emitted file —
assets and the manifest included — so nothing the reporter covered is lost.

The plan set the trigger at 300 KB against the reporter's 288.84 KB, i.e. about
11 KB of headroom. Re-based onto the guard's own measurement that is
278.68 + 11 ≈ 290 KB, which is the constant in the config.

Neither number is bytes on the wire: `internal/web` serves these assets
uncompressed and a CDN edge compresses at its own settings. The budget's job is
to be measured the same way every build so a crossing is detectable, not to
predict the wire.

The guard finds the chunk by `facadeModuleId`, not by its hashed filename. If
no chunk matches, that is a build failure too — a chunking change or a rename
would otherwise leave the budget unenforced with nobody told. It carries its
own message rather than reusing the over-budget one, so the two causes read
differently in CI output: one means the map got heavier, the other means the
guard stopped looking at the map.
