# Map rendering fixes — investigation and plan

Six reported issues. Each below states the reproduction, the verified root cause,
and the fix.

## 1. Zoomed out: hexagons collapse into dots

**Reported:** at continental zoom the grid is dots, not cells.

**Verified.** `HexTiersKM` (internal/snapshot/hexes.go:54) tops out at 15 km. The
client asks for `resolutionForZoom(z) = 5742.1 / 2**z` km and the server snaps
that onto the nearest published tier, so every zoom below ~8.6 is answered with
the same 15 km bins. `hexFeatures` then draws an aggregate cell at the SERVER's
resolution — correctly, because the cell must cover the ground its count
describes. 15 km is ~20 px at z7, ~5 px at z5 and sub-pixel at z2.

There is no honest way to draw a 15 km bin bigger than 15 km. So the cells stop
being cells below the zoom where the coarsest tier is legible.

**Fix:** give the three hex layers a `minzoom` derived from the coarsest
published tier — the zoom at which a `HEX_TIER_MAX_KM` cell first draws at
`TARGET_HEX_PX`. Below it the grid hides and the area markers, which are the
right representation at national scale, carry the reading alone. Derived from
the same two constants `POINT_TIER_MIN_ZOOM` is derived from, never a literal.

## 2. Default zoom: the Danube is cut, the ground north of it changes

**Verified.** The self-hosted archive is a Bulgaria extract with a north edge at
latitude 44.21777. Silistra sits at 44.117 — the extract's boundary runs through
the Danube a few kilometres north of the town. The style's `background`,
`landcover`, `landuse`, `park` and `water` fills paint an opaque rectangle over
the world raster underneath, so the river, the coastline and every land colour
stop dead on that line.

The same mismatch is why the map looks flat at street level: at z16 over Ловеч
the vector style paints pale beige with white roads and no place labels, while
the OSM raster underneath it carries full cartography at every zoom to 19.

**Fix:** decided with the operator — see "Basemap decision" below.

## 3. Dots drawn on top of hexagons, and dots with no hexagon

**Verified, two separate defects.**

(a) **Overlap band.** The hex tier is chosen from `Math.round(zoom)`
(hexes.js:89) while `LAYER_ID`/`LABEL_LAYER_ID` carry `maxzoom:
POINT_TIER_MIN_ZOOM`, which MapLibre applies to the true fractional zoom. In
[14.5, 15) the grid is already at the point tier while the markers are still
drawn — the same reading, twice, from two sources.

(b) **The grid and the markers are both drawn at every zoom below the
handover.** That is what the Sofia screenshot shows: 15 km cells with city
aggregate dots and their labels on top. The dots also appear where no cell does
(Перник, Банкя), because those areas have no bins in the grid — hence "sensor
dots without hexagons".

**Fix:** one handover zoom, applied to the rounded zoom on both sides, and the
aggregate markers hidden wherever the grid is drawn. The markers stay live below
the grid's new `minzoom` (issue 1), which is where they are the only reading —
so area selection, and the enumeration budget that depends on it, is unchanged.

## 4. No reading printed inside a hexagon at high zoom

**Not a layer bug.** `HEX_LABEL_LAYER_ID` is correct: `minzoom` 15, polygon-only,
non-null value. The point-tier payload does carry values
(`/api/v1/hexes?resolution_km=0&bbox=...` returns `sensor_id` and `values`).

What was actually observed: zooming in from an area page's centre walks the bbox
away from the sensors. Ловеч's five sensors sit at 24.706/24.717 while zoom 16
from the page's centre requests `bbox=24.75,43.15,24.8,43.2` — the sensors are
outside it, so there is nothing to label. Compounding it, issue 3(a) means the
last zoom step before 15 draws point cells with markers over them.

**Fix:** covered by 3(a). Re-verify at z16 centred on a sensor once 3 is in.

## 5. The wind arrows are invisible

**Verified.** `arrowPaint` (islands/wind.js:76) paints the arrows
`cfg.markerStrokeColour`, which the server renders as `#ffffff`, at
`text-opacity: 0.75`, with no halo. White arrows on a pale basemap. The data is
fine — `/api/v1/wind` returns a full `vectors` array — and the disclosure the
operator saw is the layer correctly reporting itself on.

`markerStrokeColour` is the right value for a HALO and the wrong one for a fill;
the arrows need the label colour and the stroke colour as a halo, exactly the
pairing `labelPaint` already uses.

**Fix:** paint the arrows `cfg.labelColour` with a `markerStrokeColour` halo,
and raise the opacity — the halo is what keeps them off the readings, not the
transparency.

## 6. The provinces table is duplicated on the map page

The home page prints the province table under the map; `/provinces` is that same
table as its own page. Remove the section from the home page.

## Basemap decision

Issues 1 and 2 both come from mixing two basemaps whose coverage and cartography
disagree. The operator's call, recorded here before implementation.

**Decision: OSM raster only.** Drop the self-hosted vector style from the client
entirely and mount `tile.openstreetmap.org` z0–19 as the whole basemap.

The archive is a Bulgaria extract. Its land and water fills are opaque polygons
clipped to the extract's rectangle, so they painted a box over the world raster
underneath: inside the box the Danube ended at Silistra, the ground changed
colour at the border, and the Black Sea carried no name because its label sits
outside the extract. Rebuilding from a Europe-wide extract was the alternative;
this was chosen instead.

Two consequences to state to the operator, both already true of the raster
underlay and now true of the only basemap:

- every visitor's viewport is disclosed to `tile.openstreetmap.org`, and the
  OSMF tile usage policy asks that busy sites not use it. Swapping to a keyed
  provider is `RASTER_BASEMAP` plus the CSP origin in `airbg.yaml`, `env.j2`
  and `deploy/.env.example`.
- the layers menu's place categories are derived from the style's
  `airbg:group` metadata, which a raster style has none of, so the menu is now
  the two view toggles alone. "Hide the basemap" hides the raster layer by id.

The style must still declare `glyphs`, derived from the configured basemap URL
by string surgery (`new URL` percent-encodes the `{fontstack}` braces). Glyphs
are where MapLibre gets letter shapes for every symbol layer: without them the
marker labels, the cell values and the wind arrows all silently stop drawing.

## 7. The key states no top of range

**Verified.** `rampSpans` gave the open top band its neighbour's width, so
PM2.5's band starting at 50 was drawn 50..75 and every winter reading above 75
painted the same colour and printed no number.

**Fix.** `Scale` gains a `Ceiling *float64` — a SCALE-level field, not a band
bound. The top band of every table here is genuinely open-ended in law, and
giving it an `Upper` would misstate the legislation; the ceiling only affects
where the drawn ramp stops. Set to 500 µg/m³ for every particulate scale, the
same ceiling maps.sensor.community draws to. `bandsFor` carries it onto the top
band so `rampSpans` and `legendRows` both see it without four signature changes.

---

# Follow-up round: four defects reported after the first seven shipped

## 8. No hexagons at the default zoom

`GRID_MIN_ZOOM` was derived from the wrong question — "at which zoom is the
coarsest tier as small as this zoom ideally wants" (z9) rather than "at which
zoom is the coarsest tier still legible" (z4). The grid switched off at every
zoom a visitor uses.

**Fix.** Two halves. The server publishes three coarser tiers — `HexTiersKM`
gains 100, 50 and 25 km — because the national view asks for a ~45 km bin and
15 km was a third of that, rendering as a field of specks. The client derives
`GRID_MIN_ZOOM` from `MIN_HEX_PX = 8`: the first zoom at which a cell of the
coarsest tier is at least 8 screen pixels. That is now z4.

**Consequence, deliberate.** `markerMaxZoom` hands over to the grid at
`GRID_MIN_ZOOM_FRACTIONAL` (3.5), so the province and city dots are hidden at
every real zoom. The drill-down click target moved onto the cells: `cellArea`
resolves an aggregate cell to the nearest area slug, and the tier captions were
rewritten from "each dot" to "each cell".

## 9. Hiding the basemap left an empty canvas

Issue 6's raster-only style removed every layer carrying `airbg:group`
metadata, which is what the POI categories menu reads. Switching OSM off left
nothing at all.

**Fix, superseding Issue 6's decision.** The OSM raster stays the ground; the
vector archive's style is fetched and its **non-fill, non-background** layers
are added over it (`overlayLayers` / `addBasemapOverlay`). Fills and the
background were the whole cause of the Bulgaria-shaped box — they are clipped
opaque polygons. Lines, symbols and circles cover only what they trace, so
outside the extract they draw nothing and the world raster shows through. The
POI menu comes back with no rectangle, no severed Danube, no missing sea label.
The overlay is not awaited: it is detail the map does not need to be a map, and
it slots under the grid by id when it arrives.

## 10. Hexagons not contrast enough against the basemap

White outlines on a pale raster are invisible. `hexOutlinePaint` now draws
`cfg.labelColour` at 1.2 px / 0.7 opacity, and `hex_opacity` went 0.55 → 0.75.

## 11. "Still not sure what the wind button does"

Not a rendering bug — the arrows draw, and `/api/v1/wind` returns 296 vectors.
The control was unexplained: a one-word button and an attribution line naming a
model. New `wind.note` string says what the arrows mean and why wind matters to
a pollution map. It leads the disclosure and doubles as the button's `title`,
so the answer is available before the layer is turned on.
