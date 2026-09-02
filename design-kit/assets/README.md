nd # assets/

| File | What it is | Provenance |
|---|---|---|
| `favicon.svg` | Square app mark, literal `#0f62fe` | Reconstructed from `DESIGN.md` §4.3, which names the source `favicon.svg`, its `rx="7"` defect, and the required correction. The binary was not copied into this workspace. |
| `wordmark.svg` | Masthead wordmark, `--accent-on` on `--fg` | Built to the §5.1 masthead spec (weight 600 + 400, `#c6c6c6` for the TLD). |

**Not present, because the source project did not supply them:** the original
`favicon.svg` binary, IBM Plex Sans `.woff2` subsets (`fonts/`), and any runtime icon
set (`build/`). The `@font-face` block in `colors_and_type.css` is written and
commented out, ready for the woff2 files.

`#0f62fe` is hardcoded in both SVGs on purpose: an `<img>`-referenced SVG never sees
`theme.css`, so it cannot read `--accent`. Keep the two in step by hand.

## flag-bg.svg

The flag of Bulgaria, authored to the official construction: ratio 3∶5, three equal
horizontal bands, white `#FFFFFF` / green `#00966E` / red `#D62612`. It is a real
referent rendered exactly, not an approximation.

It is used only in the language picker (§5.12), and only for Bulgarian. A flag names
a nation rather than a language, so English carries its language code in the same
20×12 slot instead of borrowing another country's flag. Any language added later gets
the same test: a flag only where the mapping to one nation is unambiguous.

## flag-gb.svg

The Union Flag, 3∶5 land variant, fetched from Wikimedia Commons
(`File:Flag_of_the_United_Kingdom_(3-5).svg`) — **public domain**, no attribution
required, but recorded here because provenance is the point.

It is a real asset, not a redrawing. The `clipPath` is what counterchanges the red
saltire correctly, and that offset is precisely the detail a hand-drawn Union Jack
gets wrong. Same 3∶5 ratio as `flag-bg.svg`, so both fill the picker's 20×12 mark
slot identically.


## bg-neighbours.json

Natural Earth 1∶50m Admin 0 Countries (**public domain**, naturalearthdata.com,
via `nvkelso/natural-earth-vector`) — the countries around Bulgaria, drawn as
faded context behind the choropleth so the map reads as a zoomed view of a real
place rather than a cut-out (DESIGN.md §5.2).

Twelve countries, 24 rings, ~44 KB. Rings whose bounding box falls outside a
lon 19.5–31.5 / lat 38–46.5 window are dropped, and coordinates are rounded to
2 dp (~1 km) — far below one screen pixel at this scale. The build carries
`NAME_EN` but no `NAME_BG`, which is why the neighbours are drawn unlabelled.

## bg-rivers.json

Natural Earth 1∶10m Rivers and Lake Centerlines (**public domain**) — the Danube
only, clipped to the map window, three segments / 547 points. It is drawn because
Bulgaria's northern border *is* the river (DESIGN.md §5.2).

## Bulgarian names in bg-neighbours.json and bg-rivers.json

Natural Earth carries `NAME_EN` but no `NAME_BG`. The Bulgarian and English
labels for the five bordering countries, the Black Sea and the Danube come from
**Wikidata** (CC0), fetched by QID — Romania Q218, Serbia Q403, North Macedonia
Q221, Greece Q41, Turkey Q43, Black Sea Q166, Danube Q1653. Each country carries
its `wikidata` id and a `borders_bg` flag, so which names appear is a property of
the data rather than a list typed into the renderer.
