# DESIGN.md — airbg.org

The design contract. Anything on screen that contradicts this file is a defect in one
of the two; decide which, then change that one. Do not settle a disagreement by
adding a third rule.

**Provenance.** Adapted by hand from the `ibm` design system bundled with Open Design
0.21.1 (`design-systems/ibm/DESIGN.md` + `tokens.css`), which describes IBM's Carbon.
Both source files are byte-stable across the 0.9.0 → 0.21.1 bump, so the port below
did not move under the update. That release also ships `design-tokens.json`, which is
the machine-readable form of the same values and the better source if this is ever
re-derived. It carries 18 translated variants; Bulgarian is not among them.
Nothing from Open Design is vendored, generated, or served: it supplied the starting
tokens and the prose below was written against airbg's own constraints. Its
`components.html` is a reference mockup and is not in this repository.

**Why Carbon.** Three reasons, none of them taste:

1. Carbon's single accent is a blue (`#0f62fe`). The map already spends green, teal
   and yellow on air quality, so an accent in any of those hues is ambiguous — a
   green link and a green "clean air" dot would mean two unrelated things.
2. IBM Plex ships full Cyrillic. The site is Bulgarian first; a display face without
   Cyrillic fails silently, mid-headline.
3. Carbon is built for dense public/institutional data, which is what a 28-oblast
   table and a sensor map are.

---

## 0. Product context

**airbg.org** is a Bulgarian-first public air-quality site. It shows PM readings from a
community sensor network on a country map, per-oblast aggregates, and time-series
charts. Readers are the general public checking whether the air where they live is
bad right now — not analysts. The institutional register is deliberate: this is data
about public health, and it must not look like a marketing page.

| Fact | Value | Design consequence |
|---|---|---|
| Primary language | Bulgarian (Cyrillic) | Display face must ship Cyrillic. Fit is checked in Bulgarian, never English. |
| Content shape | 28 oblasti, ~585 sensors, one country map, uPlot time-series | Data surfaces need a wider container than prose. |
| Delivery | Server-rendered Go app, `internal/web/static/theme.css` | Tokens ship as a plain stylesheet. No build step, no framework. |
| Security posture | CSP forbids inline styles; no third-party origins | No `style=""`, no font CDN, no icon font, no CSS framework. |
| Data authority | `/api/v1/scales` owns the air-quality ramp | The design system owns the UI palette only. |

### 0.1 What this design system covers

It covers the **interface**: masthead, map frame, legend, chart frame, oblast table,
footer, buttons, and the metric switcher. It does **not** cover the air-quality colour
ramp, which is served data (§2.1) and is intentionally absent from `tokens.css`.

### 0.2 Colour terminology

Prose below uses British *colour*; token names and CSS use `color`. Same thing.

## 1. Non-negotiables inherited from the app

These predate the design and outrank it. A design change that breaks one of these is
rejected, not negotiated.

| Constraint | Where it lives | Consequence for design |
|---|---|---|
| CSP allows no inline styles | server headers, `theme.css` comment | No `style="…"`, ever. Every value is a token in a stylesheet. |
| Air-quality colours are server-defined | `/api/v1/scales`, `web/src/lib/colour.js` | The ramp is data, not brand. Design never restates or overrides it. |
| Map tiers are an anti-enumeration control | Phase 1 §7.1, `tierFor()` | Design may explain the tiers. It may not widen what a tier returns. |
| Country fit has ~3.4 % latitude margin | measured at `data-zoom=7`, 926×382 | The fit is a **maximum aspect ratio of 926∶382**, not a pixel height. Wider than that crops the country top and bottom; **taller is always safe** — it simply shows more latitude around it. ~6 px of slack. |
| The kit is served by the app, under its CSP | `internal/designkit`, `/design-kit/` | Five allowlisted roots only — `ui_kits/ assets/ tokens.css colors_and_type.css components.css`. A sixth root is a code change, not a new file. References are relative (`../../tokens.css`); an absolute `/tokens.css` breaks under the mount point. Anything the repo does not carry — `context/`, `preview/`, `image*.png` — 404s on the host while working locally. |
| No new runtime dependency | project rule | No icon font, no CSS framework, no webfont CDN. **One exception, taken deliberately:** MapLibre GL + PMTiles, vendored under `assets/vendor/`, to draw the app's own vector basemap (§5.2b path A). Vendored, not hotlinked — the rule against a CDN is about third parties watching readers, and that reason is untouched. |

---

## 2. Color (colour)

### 2.1 The two palettes are separate, and that separation is the point

airbg has **two** colour systems, and mixing them is the single most likely design
defect:

- **Brand/UI palette** — below. Monochrome plus one blue. Describes *the interface*.
- **Air-quality ramp** — served by `/api/v1/scales`. Describes *the air*.

A hue in the ramp may not carry UI meaning, and a UI colour may not appear on a data
surface. Concretely: Carbon's own `--success` (`#24a148`) and `--warn` (`#f1c21b`)
are green and yellow, which is to say they are the ramp. **They are therefore
restricted to system messaging** — a failed request, a validation error — and are
forbidden on the map, in the legend, on a chart series, and in a value readout.

### 2.2 Tokens

```
--bg           #ffffff   page
--surface      #f4f4f4   card, tile, alternating band
--surface-2    #e0e0e0   panel inside a card
--surface-sel  #edf5ff   selected row (blue tint)
--fg           #161616   headings and body
--fg-2         #525252   secondary text, helper text
--muted        #6f6f6f   placeholder, disabled, EMPTY STATES
--meta         #8d8d8d   timestamps, attribution
--border       #c6c6c6   dividers, input underline
--border-soft  #e0e0e0   hairlines between tiles
--accent       #0f62fe   the only chromatic UI colour
--accent-on    #ffffff
--accent-hover #0043ce
--accent-active #002d9c
--danger       #da1e28   system errors only
--warn         #f1c21b   system warnings only — never on data
--success      #24a148   system success only — never on data
```

`--accent` replaces today's `#0b6`. That green is retired from the UI precisely
because it is a ramp hue.

### 2.3 Ruling: "insufficient data" is not a warning

`Недостатъчно данни за този район` currently renders in orange, eight times on the
areas page, and reads as eight failures. Nothing has failed — those oblasti have no
recent readings, which is an ordinary state of the world.

**It is `--muted`, at body size, with no icon and no colour.** The same applies to a
grey map dot: grey means "no recent data", and the legend must say so in those words
rather than leaving the reader to infer breakage.

---

## 3. Typography

**IBM Plex Sans**, self-hosted, Cyrillic + Latin subsets, weights **300 / 400 / 600**
only. Self-hosted because the CSP has no third-party allowance and because a font CDN
is a third party watching our readers. Fallback stack:
`"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif`.

Weight 700 does not exist in this system. Neither does italic display type.

| Role | Size | Weight | Line height | Tracking |
|---|---|---|---|---|
| Page title | 42px / 2.63rem | 300 | 1.19 | 0 |
| Section heading | 32px / 2rem | 400 | 1.25 | 0 |
| Sub-heading | 24px / 1.5rem | 400 | 1.33 | 0 |
| Card title | 20px / 1.25rem | 600 | 1.40 | 0 |
| Body | 16px / 1rem | 400 | 1.50 | 0 |
| Compact body, table cell | 14px | 400 | 1.29 | **0.16px** |
| Caption, timestamp, attribution | 12px | 400 | 1.33 | **0.32px** |
| Numeric readout (µg/m³) | 32px | 300 | 1.17 | 0, **tabular-nums** |

Two rules that are easy to get wrong:

- **Tracking only below 16px.** 0.16px at 14px, 0.32px at 12px, zero above. Display
  type gets none.
- **Measured values use `font-variant-numeric: tabular-nums`.** A PM figure that
  reflows its own width as it updates is the kind of detail that makes a data site
  feel untrustworthy.

Bulgarian sets the line length, not English. Cyrillic runs longer for the same
content; a heading that fits in English is not evidence.

---

## 4. Layout

### 4.1 Two containers, not one

Today everything sits in one 60rem column, so the map is confined to a prose measure
(the surviving half of F1) and the page has three different effective widths (D4).

- `--measure: 60rem` — prose, headings, the about page, footer. Reading measure.
- `--frame: 78rem` — map, charts, the oblast table. Data wants width.

Both centred, both with a 16px / 32px gutter. Nothing else gets its own width.

### 4.2 Spacing

8px grid. `4 · 8 · 12 · 16 · 24 · 32 · 48 · 96`. 2px and 4px exist for optical
nudges only. Component padding is 16px. Major section rhythm is 48px.

### 4.3 Geometry

**`border-radius: 0`.** Buttons, inputs, cards, tiles, the map frame, the chart
frame. The one exception is the legend tag, at `24px` (pill).

This makes the current `favicon.svg` wrong: it has `rx="7"`. Square it, and keep its
literal `#0f62fe` in step with `--accent` by hand — an `<img>`-referenced SVG never
sees `theme.css`.

### 4.4 Depth

There are no shadows on cards. Depth is background layering:
`#ffffff → #f4f4f4 → #e0e0e0`. A shadow (`0 2px 6px rgba(0,0,0,.12)`) means the
element genuinely floats: the chart hover readout, a dropdown. Nothing else.

---

## 5. Components

### 5.1 Header

Dark masthead, `--fg` background, 48px tall, full-bleed — it is the one element that
ignores both containers. Wordmark left in `--accent-on`. Nav links 14px/400 in
`#c6c6c6`, white on hover, white with a 2px bottom border when current.

**The nav carries destinations only.** Карта and Области are places; an oblast detail
view is not. It is reached from a name in the table or a dot on the map, and it has no
meaningful state of its own — a "Район" tab with no oblast chosen is a page about
nothing, which is exactly how it read. A detail view instead marks its *parent* section
current, so the reader keeps the "you are here" thread after drilling in.

**The current tab differs on two channels, not one.** It was a 2px white underline
while hover already turned the text white — near-identical states. It now takes a
lifted `#262626` background, a 3px rule and weight 600, so it is legible as "you are
here" at a glance rather than by comparison with its neighbours.

The language control is a **picker** (§5.12), not a pair of links. At 390px the
masthead stays one line; the wordmark shortens before it wraps.

### 5.2 Map

- Frame: `--frame` wide, `0` radius, `1px solid var(--border)`, no shadow.
- **The map is the primary surface of the site, and it is sized fluidly.** It is the
  second element allowed to ignore both containers (§4.1) — the masthead is the other.
  On the home page it runs edge to edge above the legend.
- **926∶382 is a floor on height, not a fixed height.** `height: max(100vw × 382/926,
  --map-cap)`. The first term is the fit; the second is how much of the viewport the
  map is allowed to claim. Whichever is larger wins, so the country can never crop and
  the map can always be taller. The superseded rule was a fixed `24rem`, which held
  the fit at exactly one width and cropped everywhere else on a fluid `--frame`.
- One knob: `--map-cap`, `78vh` on desktop, `52vh` under 1056px where a tall portrait
  viewport would otherwise swallow the legend. Raising it is always safe; lowering it
  past the fit floor simply has no effect, which is the correct failure direction.
- Floor is `22rem`. Re-measure the fit only if the **projection or zoom** changes —
  never for a resize. That is the whole point of encoding it as a ratio.
- **The kit draws a static preview; the app still owns the map.** Leaflet, tiles,
  panning and the zoom tiers belong to the app. What this system now renders is
  one frame of it: the real boundary, the 28 real centroids, the served colours.
  The superseded rule said the kit renders no map and printed *"Картата се показва
  тук в приложението"* — honest, but unverifiable. A sentence cannot be checked for
  dot size, overlap, legibility of the ramp against `--surface`, or whether the
  legend and the map agree; the moment there was live data to draw, the placeholder
  stopped being the honest option and became the untested one.
- **The preview is drawn at the 926∶382 fit**, so it crops exactly where the app
  crops. A preview on a different projection would validate a layout that never
  ships.
- **Boundary and readings are both localised assets.** `assets/bg-outline.json` is
  Natural Earth 1∶50m (public domain), fetched once and stored — the same rule that
  governs the flags (§5.12): acquire the real asset, never draw a look-alike. No
  third-party origin is contacted at runtime (§1).
- **Dot colour is read from the snapshot's copy of `/api/v1/scales`,** set as an
  SVG attribute rather than restated in CSS (§2.1). Grey is `--muted` and means
  absence, not the floor of the ramp (§2.3). Verified: every colour the preview
  paints is one of the six served bands.
- **If either file fails to load, the placeholder sentence stays.** A blank frame
  claims nothing; the old copy at least tells the reader where the map lives.
- **The mark is the province, not a dot on it.** A province average is not
  measured at a point, so a filled territory is the honest mark and a circle at
  the centroid was not. Boundaries are Natural Earth admin-1 (public domain),
  localised as `assets/bg-provinces.json` and keyed by the API's own `name_en`
  so the join cannot drift; all 28 match, with one alias — NE calls София-град
  *Grad Sofiya*.
- **The country is drawn in its surroundings, not on a blank field.** Bulgaria
  alone on flat grey reads as a cut-out: nothing distinguishes the Black Sea
  coast from the Serbian border, and the reader has no orientation. The frame
  is now filled with the real neighbours — Romania, Serbia, North Macedonia,
  Greece, Turkey and the rest inside a lon 19.5–31.5 / lat 38–46.5 window —
  at the same projection, from Natural Earth 1∶50m admin-0 (public domain),
  localised as `assets/bg-neighbours.json`. Same rule as the outline and the
  flags: acquire the real asset, never draw a look-alike.
- **The fit does not move to make room.** The neighbours run off the edges of
  the viewBox rather than shrinking Bulgaria inside it, so the 926∶382 fit and
  its ~3.4 % latitude margin are exactly as measured (§1). Widening the margin
  would be a second, undocumented framing that the app's own map does not use.
- **Water is painted, not left blank.** At one flat grey the reader could not
  tell the Black Sea from Romania, which is the defect the layer was added to
  fix. The sea is a rect under everything at `--surface-2`, 55 % — deliberately
  weaker than a no-data province's solid `--surface-2`, so absence of a reading
  never reads as sea — and foreign land is `--bg` with a half-strength hairline.
- **The context layer carries no reading and no ramp colour.** It is `pointer-
  events: none`, it is not linked, and it holds nothing measurable, so nothing
  in it can be mistaken for data (§2.1). Grey inside the national outline means
  no recent data; grey outside it means another country.
- **The neighbours are named, and the names came from Wikidata.** Natural
  Earth carries `NAME`/`NAME_EN` but no `NAME_BG`, and §5.12 forbids
  transliterating a proper noun onto the screen — so the first pass shipped the
  context unlabelled. That was the right call given the source, and the wrong
  place to stop: the constraint was a *sourcing* problem, exactly as it was for
  the Union Flag. Bulgarian and English labels for the five bordering countries,
  the Black Sea and the Danube are fetched from Wikidata (CC0) by QID and stored
  in the assets, so *Румъния · Сърбия · Северна Македония · Гърция · Турция*
  are the data's own spelling, not mine.
- **Only the five countries that share a border are named.** Ukraine, Hungary
  and the rest are drawn because they fill the frame, but naming everything a
  projection happens to clip would make the context compete with the data. The
  asset carries `borders_bg` per country, so the rule is in the data rather
  than in a list typed into the renderer.
- **Water is the one hue this system spends outside the accent and the ramp.**
  §2.1 keeps UI colour and air colour apart, and a basemap is neither: the sea
  is a real referent, not a state and not a reading. Grey water was also what
  made the Black Sea indistinguishable from Romania *and* from a silent
  province — three different things at one lightness. `--map-sea` and
  `--map-river` are derived in OKLCh at low chroma, far from the ramp's teal,
  and defined beside the palette with a Gray-100 counterpart so the theme owns
  them like every other colour. Measured: `--fg-2` at 5.48∶1 on the sea, the
  river at 3.7∶1 against it — clearing the 3∶1 UI floor — and 4.26∶1 in dark.
- **The Danube is drawn because it is the border.** The northern edge of the
  country *is* the river, so drawing it explains the shape of that boundary
  instead of leaving it as one more administrative stroke. Natural Earth 1∶10m
  river centrelines, the Danube alone, clipped to the same window.
- **A context name is placed by horizontal room, not by distance to an edge.**
  Only slivers of each neighbour are on screen, so a centroid is useless —
  Turkey's is hundreds of pixels outside the frame. The frame is sampled on a
  12px grid and the label takes the middle of the longest uninterrupted *run*
  of cells inside that country. Scoring by distance-to-nearest-edge instead
  printed СЪРБИЯ half off the top of the frame and cut the Я off СЕВЕРНА
  МАКЕДОНИЯ against the Bulgarian border: a cell deep inside a tall narrow
  strip has room in every direction except the one a word needs.
- **A name that does not fit is not drawn.** Half the run must hold half the
  word, and the outermost ring of grid cells is unusable so an ascender never
  clips on the frame edge. Same rule as a province's label: clipped type is
  worse than absent type. All seven names currently place, none clipped, none
  overlapping a reading.
- **The value is printed inside its own province**, in the served band's colour
  with ink chosen from that colour's luminance rather than fixed in CSS: the
  bands are data, so text on them cannot assume a background. Both candidates
  are palette tokens.
- **A label that will not fit is not drawn.** Under ~900px² the number would
  overlap a neighbour, and an unreadable label is worse than none — the tooltip
  still carries name, value and band. Currently 20 of 20 reporting provinces are
  labelled.
- **Painting order and labelling order answer different questions.** Provinces
  paint largest-first so an enclosed one is never buried; labels place
  smallest-first so the province with room to move is the one that moves.
  София-град keeps its centroid; Софийска's label walks outward until it is
  still inside its own shape and clear of everything already placed. They shared
  one loop at first and the two labels printed on top of each other.
- **The province names itself, under its reading.** A colour and a number
  answer *how bad*; only the name answers *where*, and a reader who has to
  hover 28 shapes to find their own province is using a map as a quiz. The
  name takes body weight under the value's emphasis weight, so the reading
  is still what the eye lands on first, and both take the same
  luminance-derived ink.
- **The reading and its name are one label, and it never comes apart.** Two
  earlier builds are superseded here. The first dropped the name whenever the
  pair would not fit at the centroid; the second let the name wander off alone
  inside the shape and, failing that, outside it on a leader line — ten
  hairlines, later two. Two is still two lines drawn across a map that has none
  anywhere else, and every one of them was a label admitting it had been
  split. The name now always sits directly under its own number.
- **What moves instead is the pair, and it may sit anywhere inside the
  province.** A label does not have to be at a centroid to belong to a shape;
  it has to be *inside* it, which the four-corner test already guarantees. The
  search runs the centroid walk at every size first — so the common case still
  reads as a centred label — then the dense walk at every size, then the same
  again with the name wrapped.
- **The ramp is 12 / 11 / 10 / 9, and 9px is the floor.** §3's scale is written
  for running copy; a map label is a different job. Below 9 the Cyrillic
  descenders close up at this weight, and an 8px notch was tried and rejected:
  it rescued nothing and only shrank *Велико Търново* further. Currently 12
  names sit at 12px, 4 at 11, 3 at 10 and 9 at 9.
- **The map is set in two scripts, so the width estimate is per character.**
  A single constant was wrong in both directions, and each correction broke the
  other language. 0.54 em is a Latin figure: it under-read Cyrillic —
  *Търговище* estimates 48px at 10px and sets nearer 62px — so the collision
  test cleared a gap the type then filled and **Велико** printed into
  **Търговище** while the automated check reported zero overlaps. Raising it to
  0.62 fixed Bulgarian and silently broke English, where every Latin name was
  then over-measured by ~15 % and dropped for want of room it did not need:
  *Targovishte*, the same province, disappeared from the English map. The
  factor now follows the glyph — 0.62 in the Cyrillic block, 0.54 outside it —
  and digits stay at 0.58 in either language, where tabular figures make that
  exact by construction. English went 25 → 27 named on that change alone.
- **Labels keep a 3px gap, because not overlapping is not the same as being
  legible.** With the gap at zero, *Veliko* ended and *Targovishte* began two
  pixels later: no intersection, so every overlap test passed, and the reader
  saw **VelikoTargovishte** on the rendered map. Non-overlap is the arithmetic
  condition; separable is the design one, and only the second is what a label
  is for. The measured minimum between any two labels is now 5,4px in
  Bulgarian and 3,4px in English, and the gap cost no name in either.
- **The label's geometry belongs to the province, not to the current
  language.** Placement measured whichever string the reader was looking at, so
  the same province sat at a different spot *and* a different size on the
  Bulgarian and the English map — two maps of one country whose labels move
  when the UI language changes read as two different maps. The box is now sized
  from the **wider of the two names** and the drawing puts whichever string
  belongs to the current language into that fixed box. Verified: value anchors
  and name sizes compare identical, BG against EN, on one page load.
- **A wrapped plan is offered only when both names break.** *София-град* breaks
  at its hyphen and *Sofia-City* at its own; if only one of them broke, the two
  maps would disagree about how many lines the label has — the same drift the
  shared box exists to remove, one level down.
- **The shared box costs a notch, and that is the trade.** Sizing every label
  for the wider language means a few names sit one step smaller in the language
  where they would have fitted larger. Two maps that agree is worth more than
  one map at its best.
- **The last resort lets the name hang below the province, and the reading
  stays on it.** *София-град* is the case it exists for — the smallest of the
  28, and no size in the ramp fits a two-line pair inside it. This is a
  deliberate exception to the rule that every glyph sits on its own territory,
  taken because the alternative was an unnamed province and the leader line is
  gone. The pair stays together and contiguous, so it still reads as one label
  rather than as a name belonging to the province underneath, and it is still
  bounded: inside the frame, and clear of every label already placed. With it,
  both maps name **28 of 28**; *Перник* came back on the same change.
- **Coverage is checked in both languages, on the same page.** The label pass
  runs again on `airbg:languagechange`, so a map verified in Bulgarian says
  nothing about the English one. Every count above is measured per language.
- **The number never leaves the shape.** Only the name travels. A reading is a
  measurement of that territory and belongs on it; a name is an identifier and
  can point from a distance. Splitting them this way is what keeps the callout
  from turning into a second, floating readout (§5.10 makes the same call about
  the combobox).
- **A silent province is labelled too, and its value line is an em dash.**
  The label pass used to skip anything with no reading, which left eight
  unexplained grey shapes: nothing on the map distinguished a province with no
  sensors from one the renderer had failed to draw, and the reader had to hover
  to find out which grey shape was theirs. Absence is now stated in the same
  place a reading would be — *—* over the province's own name. The dash is the
  ordinary typographic statement of "no value here" and needs no catalogue
  entry in either language; the tooltip and the legend carry the words.
- **It is `--fg-2`, not `--muted`, and that is a contrast finding.** §2.3 makes
  `--muted` the empty-state colour, but these labels sit at 12px on
  `--surface-2`, where `--muted` measures **3.81∶1** — under the 4.5 floor.
  `--fg-2` clears it at **5.92∶1** and is still visibly quieter than the
  near-black ink a measured value takes. A contrast floor outranks a colour
  convention; the convention was written for body copy on `--surface`.
- **The dash's ink is not computed, because there is nothing to compute from.**
  A reading takes ink derived from its served band (§2.1); a silent province
  has no band, so it takes a class instead of an attribute. All 8 silent
  provinces are labelled, 3 of them with room for the name as well.
- **`var` hoisting turned one misplaced declaration into a blank map.** The
  callout grid needs to know where the context names landed, and that array was
  declared beside the province counters — *after* the context layer that fills
  it. Hoisting made it `undefined` rather than a `ReferenceError`, so the whole
  preview died on `undefined.push` with one console line and an empty frame.
  Declare a collection where it is first used, not where it is first
  convenient; a `let` would have thrown at the point of the mistake.
- **Fit is tested at the corners of the text box, not at its anchor.** A
  centred name is placed by its middle, so testing only that point passes a
  label whose tail hangs into the next province. All four corners of the box
  must be inside the shape.
- **Width is estimated, not measured.** `getComputedTextLength` needs a laid-out
  node and the placement runs before anything is in the document, so the
  estimate comes from IBM Plex Sans's glyph advance at caption size and errs
  wide. Erring wide costs a name that would have fitted; erring narrow costs an
  overlap, and only one of those is a defect.
- **Labels are one layer above every shape.** They used to sit inside their own
  province's group, which put them in the shapes' size-ordered paint order:
  Софийска's name was covered by София-град and Пловдив's by its eastern
  neighbour, both printing as fragments. A label is not a shape and must not
  take part in shape ordering at all. The layer is `pointer-events: none`, so
  the province underneath stays the thing you click.
- **A province on the country map is a link, and it is a real `<a>`.** The
  reader's next question after "how bad is it here" is "what is happening
  here", and the answer is that province's own page. An `<a>` inside the SVG
  is focusable, middle-clickable and previewed in the status bar; a click
  handler on a `<path>` is none of those. **The area screen's own map links
  nowhere** — a link from Пловдив to Пловдив is a dead control.
- **Which province the page is about travels in the URL.** The kit has one
  `area-detail.html`, so every link into it carried the authored province and
  a reader clicking Смолян arrived at Пловдив — a control that appears to work
  and reports the wrong province, worse than one that plainly does nothing.
  `?oblast=` carries the Bulgarian *identity* (§5.12), is validated against
  `AIRBG_OBLAST_EN` before use, and an unknown value leaves the page as
  authored rather than inventing a heading. The table's 28 name links and the
  finder's *Отвори* link now carry it too.
- **The URL is the carrier, and it is not the only one.** `?oblast=` is
  correct and stays first: linkable, bookmarkable, shareable, and what the real
  server reads. But the kit is also opened in hosts that address files by
  **path with no query layer at all** — the OD preview resolves the whole
  string `area-detail.html?oblast=Смолян` as a filename. There the link opened
  the page and the parameter vanished, so every province rendered as the
  authored one and *every click looked like it went to Пловдив*. The parameter
  was right; the assumption that a query survives was not.
- **So the identity also rides in `sessionStorage`, written at the click.** One
  delegated capture handler covers the map's 28 SVG anchors, the table's 28 rows
  and the finder's link, so none of them has to know about it. This is a carrier,
  not a second source of truth: the query wins whenever it exists, the stored
  value is **read once and immediately cleared**, and a direct visit with neither
  leaves the page exactly as authored. Verified in all four states — click writes
  it, query-less visit resolves from it, a query beats a stale stored pick, and
  the next direct visit falls back to the authored province.
- **It reads `getAttribute('href')`, not `.href`.** An SVG anchor's `href` is an
  `SVGAnimatedString`, not a string; the regex would have matched nothing on
  exactly the 28 links that mattered most.
- **Hover lifts the province off the sheet.** It is one of the sanctioned
  shadow cases (§4.4) because the shape genuinely floats: it scales 4.5 % from
  its own centre and takes a drop shadow, so a reader can see which of 28
  irregular shapes the pointer is on without reading a tooltip. Only
  `transform` and `filter` animate (§8) — the frame never reflows and the
  country fit keeps its slack (§1). Under `prefers-reduced-motion` the
  transform is removed outright, not merely made instant, and the edge carries
  the state instead.
- **SVG has no z-index, so lifting means moving the node.** The hovered
  province is re-inserted before the label layer — above every other shape, and
  still under every reading and under the national border. The move is guarded
  so a pointer that stays put does not churn the DOM.
- **Keyboard focus is stated on the edge.** An SVG child cannot carry the
  Carbon double ring, so its equivalent is a 2.5px `--accent` stroke on the
  shape the reader is on, and the same lift applies to `:focus-visible`.
- **The metric switcher was markup with nothing bound to it.** Clicking ФПЧ10
  moved the radio and changed nothing — not the map, not the legend, not the
  readouts. It is the dead control this document warns about more than any
  other defect, shipped on the primary surface. One file now owns the metric
  (`map-metric.js`); the choropleth, the readout bar and the legend re-render
  from `airbg:metricchange`, and nothing else reads the radios.
- **`/api/v1/scales` serves six scales, not one.** EAQI, EU limit and WHO,
  each for P1 and P2. The kit had captured only `eaqi/P2`, which is why the
  question "what would PM10 even be coloured against?" had no answer in the
  data. The snapshot now carries `scale_p1_eaqi` beside `scale_p2_eaqi`.
- **The PM10 bands are not the PM2.5 bands, and that is the whole risk.**
  PM2.5 breaks at 5 / 10 / 20 / 25 / 50; PM10 at 20 / 40 / 50 / 100 / 150 —
  and the six **colours are identical**. So a switch that recoloured the map
  and left the key alone would look completely plausible while stating
  thresholds the map was not using: confidently wrong, which is worse than
  visibly inert. Metric and scale move together or neither moves.
- **The legend's ranges are written from the served bands.** They were six
  hard-coded strings in markup — correct for PM2.5 and silently wrong for
  anything else. The caption lost its `data-i18n` key at the same time and is
  composed from `legend.unitOf` + the metric's own short name, because the
  script now owns that string (§5.12).
- **The readouts follow the metric too.** *Най-висока стойност* and *Медиана
  по области* are readings of whichever particulate is selected; a card still
  reporting PM2.5 under a ФПЧ10 map is two surfaces disagreeing about one
  question (§9.3). Verified: 18,16 → 22,69 and 3,18 → 8,04 on the switch.
- **`data-metric` on the frame carries the state** for whoever mounts Leaflet
  — the same seam as `data-focus-oblast` and `data-sensor-filter`.
- **There is no time-range control, because the API has no time range.**
  `/api/v1/areas` was probed with `window`, `period`, `hours`, `avg` and `range`
  at 1h / 12h / 24h: every response was byte-identical, same values and same
  `generated_at`. A selector over that would be the dead control this system has
  hit more often than any other defect. The tier line states the period in words
  instead — *"текущата си средна стойност — последното отчитане от сензорите в
  нея, не осреднено за час или ден"* — which answers the reader's question
  honestly while the control waits for an endpoint that can serve it.
- **Two provinces share one point, and the preview shows it.** София-град sits
  inside Софийска, so their centroids collide and the dots overlap — a real
  property of centroid plotting that a placeholder hid. The app's Leaflet layer
  offsets or clusters them; the design system's job here is to have surfaced the
  question rather than to fake a tidy answer. Drawing the larger value first at
  least keeps the quieter neighbour from being fully covered.
- **The map can take the whole viewport.** The map is the primary surface, so on
  a laptop the reader often wants it to be the *only* surface. The control uses
  the native Fullscreen API — the browser then owns Escape and the exit
  affordance, which no in-page imitation matches. The API can be refused (an
  iframe without `allowfullscreen`, an embedded preview, iOS Safari), so a
  fallback pins the frame over the viewport and handles Escape itself. A control
  that can silently do nothing is the failure this system keeps hitting; this one
  either enters fullscreen or falls back, never neither. Verified with the API
  forced to reject.
- **The fullscreen control is an icon, and it still has a name.** Corner
  brackets pointing out to enter, pointing in to leave — inline SVG in
  `currentColor`, no icon font and no second asset (§1). *Цял екран* as a word
  was a 14px label parked over the map's top-right corner, competing with the
  data underneath it; a 40px square button does not. The name moves to
  `aria-label` and `title`, written from the catalogue on every paint like the
  theme picker's (§5.2a) — an icon-only control whose only name is hardcoded
  markup is untranslated by construction. Verified reading *Exit full screen*
  after a language switch.
- **`aria-pressed` selects the glyph.** The attribute already carries the state
  for a screen reader, so CSS reads the same attribute to show one of the two
  SVGs. A separate class toggled by JS would be a second source of truth for one
  state, and therefore a second thing to get out of step.
- **On the area screen the map fills its column, and the legend spans the frame
  below the split.** The legend used to sit under the map inside the left column,
  so that column was map + legend while the card beside it stretched to their
  combined height — the two frames could never end level, and the map always read
  as the shorter box. Moved out, the legend is a caption for the map at full
  width and the map is free to match the card exactly. The fit still governs the
  floor: `min-block-size: max(22rem, 100cqi × 382/926)`, so a short card can
  stretch the map but never crop it.
- **The ramp is a horizontal scale under the map, drawn from the served bands.**
  The area screen previously carried chips reading *0–10 / 10–20 / 20–25 / 25–50*
  with **empty swatches** — wrong on both counts. The generic `.chip__swatch` is
  deliberately unset (§2.1) and nothing bound it on that screen, and those band
  edges are not the served ones. The scale now uses `/api/v1/scales` as captured
  in `airbg-snapshot.json`: 0–5 · 5–10 · 10–20 · 20–25 · 25–50 · 50+.
- **The bands are hard stops, not a blend.** A smooth gradient looks better and
  is wrong: EAQI is banded, so 12 µg/m³ is squarely *Умерено*, not a mix of its
  neighbours. Blending would invent colours the scale does not define — the same
  error as re-tinting served data for a theme. Render the data; do not improve it.
- **The home legend is the same scale, plus the names.** It was a wrapping row
  of pill chips — seven boxes in no particular order, so the ramp's *sequence*,
  the thing a colour scale exists to show, had to be inferred from the reading
  order of a paragraph. It is now one continuous bar in band order, each band
  named under its own colour with its own range.
- **Built from adjacent blocks, not a gradient, because it carries names.** The
  area scale can be one `linear-gradient`; this one needs six labelled cells, so
  the blocks touch at zero gap and read as one bar. The swatch reuses
  `.chip__swatch--eaqi[data-band]`, so the ramp is still written down once
  (§2.1) — a second gradient here would have been a third copy of the hexes.
- **Each band prints its own range, so there is no tick row.** Ticks under a
  named bar would state the same six edges twice, in two places that can drift.
- **On a phone the bar stands up.** Six Bulgarian band names cannot share a
  343px line — *Изключително лошо* alone is wider than a sixth of it. Under
  672px the same markup becomes rows: colour on the leading edge, name and range
  beside it, order preserved. The alternative was hiding the names on the screen
  where most readers are, which drops the half of the legend that says what the
  colour *means*.
- **The legend is inside the map's viewport budget, not below it.** `--map-cap:
  78vh` let the map take three quarters of the screen while the masthead, page
  head and toolbar took another 272px, so the scale that explains the colours
  landed under the fold — the reader saw a coloured map and no key until they
  scrolled. The height is now `min(--map-cap, 100svh − --map-reserve)`, and the
  reserve is what sits above the map plus the legend itself: measured off a
  render at 272px and 170px, plus slack, so `30rem` on desktop and `26rem`
  where the toolbar and legend stack.
- **A short viewport is paid for in width, never in height.** The fit is a
  ratio, so trimming height alone crops the country (§1) — but a *narrower* map
  is taller than the fit, which is always safe. So the budget caps
  `max-inline-size` and the height follows `aspect-ratio`. On a normal-height
  desktop the budget does not bind and the band stays full-bleed; only a short
  window pulls the edges in. That is the honest trade: the map gives up some
  width so its key stays on screen.
- **Grey sits outside the ramp**, on its own line: *"няма скорошни данни"* is the
  absence of a reading, not the bottom of the scale (§2.3).
- **The home map's toolbar finds a province and points the map at it.** It
  carried *Виж всички области* — a second door to a place the masthead already
  links to (§5.1), answering the wrong question. A reader on the map screen
  wants *their* province, not a list of all 28. The button is now the §5.10
  combobox, and picking a name moves the map.
- **The province list is not written down twice.** It is the 28 keys of
  `AIRBG_OBLAST_EN`, the identity/label pairing §5.12 already defines. A second
  copy would be a second thing free to disagree with the table (§9.3), and the
  search matches the *label* the reader can see, so "Plovdiv" works on an
  English page and the options collate per language.
- **Picking a name goes to that province's page.** The finder used to set
  `data-focus-oblast`, print *"Картата е центрирана върху област Варна."* and
  offer two controls beneath it — *Отвори страницата на областта* and *Цялата
  страна*. Three sentences and two buttons stood between the reader and the
  thing they had just named. A reader who types a province is asking to see
  that province; they do not need to be told what the map did and then offered
  a link to where they were already going. The field is now the whole control.
- **The seam survives the change.** `data-focus-oblast` is still written on the
  frame before the navigation, so whoever mounts Leaflet on a page that does
  *not* navigate reads exactly what it always read. Removing the status line
  removed a caption, not an interface contract.
- **Enter goes only on an unambiguous match** — a name typed in full, or a
  query narrowed to one option. *"С"* matches ten provinces and does nothing;
  *"Смол"* matches one and goes. A field that navigates on a half-typed query
  would take the reader somewhere they did not choose.
- **One owner for the URL and the carrier.** The finder does not build
  `?oblast=` itself; it calls `AIRBG_GO_OBLAST(name)` in `oblast-link.js`,
  which validates against `AIRBG_OBLAST_EN`, writes the `sessionStorage`
  carrier and navigates. A second copy of that URL shape is a second thing free
  to drift from the page that has to read it.
- **Deleting the line deleted its CSS and its strings.** `.map-focus` in
  `components.css` and the three catalogue keys (`province.focused`,
  `province.open`, `province.reset`) went with the markup. A rule with no
  subject and a string with no caller are both dead weight that the next
  reader has to prove is dead.
- **A live region concatenates its parts.** `role="status"` announces the whole
  line, so the sentence, the link and the reset needed real separators in the
  text — the visible gaps are layout. Same defect as *Коматевонеактивен*, found
  the same way: by reading `textContent`, not the screen.
- **The query belongs to the field.** Re-labelling the input on a language
  switch without clearing the query left the field reading *Plovdiv* while the
  filter still held an abandoned search that matched nothing — the list said *no
  province with that name* about a province plainly named in the box. Whatever
  writes the input owns the query derived from it.
- **The sensor bar sits above the map: one finder, one filter, one set.** The
  finder is the §5.10 combobox over sensor name and district; the filter is a
  §5.6 fieldset — Всички / Активни / Неактивни. They share a list, so filtering
  narrows what the finder can offer. A finder that returns a sensor the map is
  hiding is two controls disagreeing about the same set.
- **The area screen opens on *Активни*, not *Всички*.** A reader on a province
  page is asking what the air is doing now, and a sensor with no recent reading
  answers nothing — the same argument that opens the table on *С данни* (§5.4).
- **Which makes it a filter, and a filter owes three things.** The state shows on
  the switcher, the count line names what is hidden and why — *"Показани 7 от 10
  сензора в извадката. Останалите 3 нямат скорошни данни; изберете „Всички“, за
  да ги видите."* — and one click restores all ten. Drop any of the three and it
  is truncation wearing a radio button.
- **The reason changes with the filter, so the sentence does too.** Under
  *Неактивни* the hidden sensors are the ones *reporting now*; printing "нямат
  скорошни данни" there would be false. Two strings, each true of what it hides.
- **The markup ships the same default the script holds.** `data-sensor-filter`
  on the frame starts at `active`, matching the checked radio. Markup that says
  `all` under a checked *Активни* contradicts itself before the reader touches
  anything, and whoever mounts the map reads the attribute, not the radio.
- **The aside is gone and the map takes the frame.** *Сензори наблизо* held one
  empty-state line and spent a third of the width saying nothing; the questions it
  implied — which sensor, which sensors — are now controls above the map, where
  they act on it.
- **Inactive is a state, not an error.** It reads *неактивен* in `--muted` with no
  colour and no icon, and the separator is part of the string so a screen reader
  does not hear *"Коматевонеактивен"*. Visual spacing is layout; a reader hears
  content.
- **A legend is required**, and it is the fix for D2 and for F6. It states two things,
  because the reader cannot infer either:
  1. the ramp, as tag chips with their µg/m³ bands, including grey as
     *"няма скорошни данни"* — not as an error;
  2. **what a dot means at this zoom** — oblast average, city aggregate, or single
     sensor. The area page opens at zoom 9 with sensors at 11, so its ~25 dots are
     city aggregates while the page prints "585 сензора". Two honest numbers that
     contradict each other until the legend reconciles them.
- The "zoom in" hint currently only appears when the sensor tier is refused for want
  of a slug, which is never true on an area page. The legend carries the tier line
  unconditionally instead.

### 5.2b The map moves, and what it may show when it does

**A detail page opens on its subject.** The province map drew the whole country
and outlined one shape — which answers "where is Смолян in Bulgaria" to a
reader who has already chosen Смолян and is asking what its air is doing. The
map is now framed on the province.

**One projection, a view on top of it.** A second projection for the province
would validate a framing the country map never uses (§5.2), so the same fit is
scaled and re-centred: `vk`, `vcx`, `vcy` are set once per frame immediately
before that frame draws, and every `X`/`Y` in the pass reads them. Shapes,
labels, markers and hit areas therefore land in one space without any of them
knowing a view exists.

**926∶382 is the country's aspect, not a province's — and that is why a
correct zoom still looked wrong.** Пловдив is 84×118 in the country's own
space. Fitting it into a wide letterbox is limited by *height*, so the
computed scale was right and the map still showed a third of the country
across the empty width. A province view gets a taller window (`--map-view-h`,
560), and taller is always safe (§1); width is the axis that crops. The number
lives in CSS and the renderer reads it off the frame, so the viewBox and the
box it is drawn into cannot disagree — and the attribute the CSS keys on is
written *before* the height is read, or the first paint measures the old
window and lands a frame behind.

**Zoom is state, not geometry.** It lives outside the draw pass, so a language
switch or a metric switch repaints without throwing the reader back to the
country. Zooming re-runs the whole pass rather than scaling a viewBox: scaling
would blow the type up with the geometry and leave every label where a
different scale had put it.

**Out always does something.** Zooming out past a province's own fit continues
into the country instead of stopping at a clamp — the clamped version left the
button enabled and inert at `province@1`, which is the dead control this system
has hit more than any other defect. Only the country fit is a floor, and there
the button is `disabled`, which states the limit.

**Every province links, except to the page you are on.** The old rule — a
detail map "links nowhere" — was too broad. What must not exist is a link from
Пловдив to Пловдив; the other 27 are live destinations, and once the reader can
zoom out to the country from a province page, refusing them would strand them
on a map where 27 shapes do nothing.

**The tier ceiling is the design, not an obstacle to route around.** The
request was markers for the *sensors* in a province, selectable, with their
reading history. `/api/v1/areas` serves exactly three kinds — oblast (zoom 9),
city (zoom 11), neighbourhood (zoom 13) — and nothing below; every
sensor-shaped route probed returns 404. §1 lists the map tiers as an
anti-enumeration control and says the design may explain what a tier returns
but **may not widen it**. Enumerating sensors is the thing the API is built not
to do, so the honest answer is to draw the finest tier that exists and say what
it is. There is also no history endpoint at any tier, which is why the chart
series is still sample data (§5.3) — that has not changed and is not hidden.

**So the markers are city and neighbourhood aggregates, and they say so.** Real
places, real coordinates, real readings, with the tier named in the tooltip, in
the finder option and in the readout — never a bare number (§9.1). A point is
the honest mark *here*, unlike a province where the territory is the mark
(§5.2): the API gives these places a coordinate and no boundary, so a filled
shape would be geometry this system invented. They are drawn only on a province
view; 51 points over 28 provinces is a second dataset competing with the one
the reader came for.

**A province is not an island, so its neighbours carry their readings too.**
Framed on Смолян the reader saw one territory with numbers on it inside a ring
of flat colour. The neighbours were painted their real served band, but with no
mark, no number and no name they read as background rather than as data — and
the first question a border raises, *is it worse on the other side?*, had no
answer on screen. The snapshot already holds an aggregate for every province,
so the answer was in the data and only the filter was hiding it.

**The rule is what is on screen, not which province owns the point.** A list of
bordering provinces would be a second adjacency table to maintain, and it would
still be wrong at the zoom where the subject fills the frame. The projected
point is tested against the drawn box instead, which is the same question the
reader is actually asking: is this place in view. Measured: Смолян draws 2
markers at its own framing, София-град 26.

**They go when the reader drills in, at the scale the street layer starts
(25 px/km).** Past that the subject fills the frame, a neighbour's single city
dot is usually outside it anyway, and what remains on screen should be the
place being examined rather than its surroundings. Measured: 26 → 25 for
София-град and 2 → 1 for Смолян at 7,6×.

**This supersedes the scoping rule above, and the reason it was written has
gone.** Markers were confined to one province when a province was a solid slab
of served colour, where a neighbour's dot would have sat on undifferentiated
paint with nothing to relate it to. The province now carries its own outline,
its districts and its name, so a dot outside it is legible as belonging
somewhere else.

**The parent province came from the boundaries, not from the name.** The feed
publishes no parent, so each sub-area was joined by point-in-polygon against
the same Natural Earth provinces the map draws, once, at capture time — 50 of
51 fell inside a polygon and Варна fell 1,3 km outside its own coastline, which
the nearest-boundary fallback resolves. That is still geometry. A slug the
snapshot has never seen gets no parent and appears on no province page: a new
place is missing until the snapshot is recaptured, rather than being attached
to a province a guess picked.

**The finder is scoped to one province**, because a finder on a province page
that offers places in other provinces answers a question the reader did not
ask. Picking a place and clicking its marker are the same act and go through
one path, ending in one `airbg:areaselect` — a finder that highlighted the map
while the map did nothing back would be two controls disagreeing about one
selection.

**Three emptinesses, three sentences.** The province has no finer tier at all;
the filter is hiding what there is; or the query matches nothing. One message
for all three is wrong about two of them — and the first is the common case,
since only **7 of the 28** provinces publish any reading below their own level.

**Two fabrications were removed, not relabelled.** The finder held ten
hand-written sensors with invented readings — real Plovdiv district names
against numbers nobody measured — offered identically on all 28 pages, so a
reader on Смолян was shown Пловдив's districts. The peak card held
*"96,0 µg/m³ · отделен сензор, кв. Тракия"*, frozen into the markup and equally
untrue on every page; it outlived the sensor list by one edit. The card now
reports the highest of the province's own finer-tier places, and where there is
none it says so.

**The map is grabbable, and the buttons stay.** Dragging is what a reader
expects of a map, but it is pointer-only, so it is an addition to the zoom
cluster rather than a replacement for it. The same two operations are on the
arrow keys and +/− once the canvas has focus, and `0` returns the view — a
surface only a mouse can move fails the floor this system holds everywhere
else. The canvas is `tabindex="0"` with `role="application"` and a name from
the catalogue.

**The pan is held in base projection units, not screen pixels.** A pixel is
worth a different distance at every zoom, so an offset stored in pixels would
jump the moment the reader zoomed. In the country's own untransformed space the
offset is a *place*, and it survives every scale change unchanged. The renderer
writes back the scale it last drew at (`vk`), so the pointer code converts its
delta through the number the drawing actually used and can never disagree with
it.

**Zoom follows the pointer, not the centre.** Scaling about the middle walks
the subject out from under a reader who is looking at a corner. The wheel and
the pinch both keep the point under the pointer fixed; the buttons, which have
no pointer position, still scale about the centre.

**A pan stops; it does not rubber-band.** The window may leave the country but
not entirely — its centre stays inside the country's bounds grown by half a
window, so an edge of Bulgaria is always on screen. The clamped value is
written *back* to the view state: without that, the state accumulates a pan the
drawing refuses to honour, and the map sits still for the first second of the
next drag back.

**Returning to a framing resets the pan.** "Цялата страна" that kept the
reader's old offset would return to a country they had already dragged half out
of frame. The way back has to actually be the way back (§5.4).

**A drag must not follow a link.** Every province is a real `<a>` and every
marker is clickable, so a drag that starts on Пловдив would navigate on
release. Movement past 4px arms a one-shot click suppressor — **on the canvas,
in capture phase, self-clearing after 300ms.**

The first version armed it on `document`, which swallowed the next click
*anywhere on the page*: the zoom buttons, the masthead, a table row. It
presented as "whole country does not reset the pan", and sent me reading the
reset — which was correct. **A guard has to be as narrow as the thing it
guards**, or its blast radius becomes a bug somewhere else entirely, wearing a
disguise.

**The page keeps its wheel.** A full-bleed map that eats scroll traps the
reader on the home screen, so a plain wheel scrolls the page and Ctrl/⌘ + wheel
zooms — the convention embedded maps settled on. Because that is
undiscoverable, a plain wheel says so once, briefly, rather than silently doing
nothing; the hint is `aria-hidden`, since a keyboard reader is not being denied
anything the arrow keys do not already give them.

**Touch gives up the axis the page needs.** `touch-action: pan-y`, so vertical
drags scroll the page and horizontal drags pan the map. In full screen there is
no page left to scroll, so there the map takes the whole gesture and pinch
works. That is a real trade and it is stated rather than defaulted into: on a
phone, outside full screen, pinch belongs to the browser.

**A link to the page you are already on is not a navigation.** From the home
map a province link changes path, so the browser navigates and everything
works. From a province page the target path is `area-detail.html` — the current
document — and in a host that drops the query (§5.2) the resolved URL is
byte-identical to the one already loaded. The browser did nothing; the
`sessionStorage` carrier the click handler had just written was never read. The
control looked dead while every part of it was working.

**Reloading would have been wrong in the other direction.** Where the query
*does* survive, the old `?oblast=` is still in the address bar and would beat
the carrier, returning the reader to the province they were leaving. One host
would be fixed and the other quietly broken — so the page switches province
**in place** instead, which needs no round trip on either.

**The identity has one owner and one event.** `data-oblast` on the heading is
the province this page is about; four components read it — the map, the finder,
the readouts and the label swap — and none of them needs to know how it
changed. `oblast-link.js` writes it and fires `airbg:oblastchange`, exactly as
the language and metric switchers do (§5.12). The map re-frames on the new
subject and drops the zoom and pan the reader set for the old one; the finder
clears a selection that belonged to a province that has left.

**The address follows, and so does Back.** `pushState` writes a query-only,
same-path URL, so the page stays linkable in a host that keeps queries and
harmless in one that does not. The entry the reader *arrived* on is stamped
with `replaceState` — without it the first entry carried no province, Back had
nothing to return to, and the browser's own control did nothing. A modifier
click is still the reader's: Ctrl, ⌘, Shift and Alt are never intercepted.

**Capture is taken when a drag starts, not when a press does.** `setPointerCapture`
on `pointerdown` keeps the moves coming and also retargets the following `click`
to the capturing element — which would deliver every province click to the
canvas instead of to the `<a>`, silencing every link inside the map. Nothing
needs capture until the pointer is actually dragging, and by then there is no
click left to lose.

**A province is no longer a flat slab.** One served colour is the whole
reading and nothing else — true, and at any scale past the country it left the
reader with no way to place what they were looking at. There is now a basemap
under the data: the real national road network, and real streets inside the
cities.

**CORRECTION — tiles were the obvious answer and they exist.** The paragraph
that stood here said a tile server is a third-party origin that §1 forbids. That
was wrong on the facts, and it is the premise every localised-geometry decision
below was built on, so it is corrected rather than quietly edited.

airbg serves its **own** basemap: `internal/tiles/tiles.go` on its own listener,
a planetiler build of the OSM Bulgaria extract. Verified live, not from memory:
`https://tiles.airbg.org/style.json` returns an OpenMapTiles-schema style with
**19 layers** — `building` (fill), four `transportation` lines,
`transportation_name` (symbol), `place`, `water`, `waterway`, `landcover`,
`landuse`, `park`, `boundary`, `water_name`, `mountain_peak`. Its label layer
already carries `["coalesce",["get","name:bg"],["get","name"]]` and
`["Noto Sans Regular"]`, and the source carries
`attribution: © OpenStreetMap contributors`. The archive is
`bulgaria-20260827.pmtiles`, **217 197 432 bytes**, `PMTiles` magic, HTTP range
requests honoured. The app's own CSP already reads
`connect-src 'self' https://tiles.airbg.org`.

So the origin is **first-party**, §1 never applied to it, and the app has had
street names and building footprints at every zoom the whole time.

**The kit still draws localised geometry, for a different and real reason.**
`Access-Control-Allow-Origin` on that listener is `https://airbg.org` — not
`*`. This kit runs from `file://` or a preview host, so it cannot read the
archive, the style or the glyphs at runtime. That is the same posture as the
JSON API (§5.3a), and it has the same consequence: the bundled copy is not a
fallback, it is how the kit renders at all. Rendering vector tiles would also
mean MapLibre GL plus the PMTiles protocol — a new runtime dependency §1 does
forbid, and a large one.

**Two things follow, and both are debts, not features.** First, the kit's
basemap is a **stand-in for a basemap that already ships**, which strains §5.2's
rule that the preview is drawn as the app draws it: road weights, street-name
density and building fills validated here are not the ones a reader sees.
Second, everything captured for those layers — `bg-roads.json`,
`bg-streets.json`, `assets/streets/*`, and the per-city recapture that Overpass
kept refusing — is **duplicate geometry with a second owner**, and the archive's
copy is better: whole country, every zoom, already labelled in Bulgarian. The
buildings finding above (~32 MB per city, "not shippable") is superseded for the
app: the archive carries buildings country-wide inside 207 MB.

**The superseded reasoning is kept above deliberately.** "It is a third-party
origin" was a plausible-sounding rule applied without checking whether the thing
it forbade was actually third-party — the same error as recording "no data" on a
429. Check what the constraint is about before designing around it.

**What the kit localises, and why it is still worth having.** Fetched once and
stored, exactly as the boundaries, the rivers and the flags are: `assets/bg-roads.json` is Natural Earth
1∶10m Roads (public domain, 813 lines, 197 KB); `assets/bg-streets.json` is
OpenStreetMap via Overpass (ODbL 1.0, ~4 800 ways, ~260 KB), motorway through
secondary within ~5 km of each city in the API's own city tier, simplified to
~25 m with Douglas–Peucker.

**The roads go over the fill, not under it.** The instinct is a translucent
choropleth above a basemap, and it is the wrong instinct here: the ramp is
served data (§2.1), and dropping its opacity re-tints it — the same error as
adjusting the ramp for a theme. The fill keeps its exact served colour and the
roads sit on top, carried by a **casing** — a wider stroke in the page colour
under a thin dark core. That is the ordinary cartographic device, it reads on
every band from the palest teal to the darkest purple, and it alters no pixel
of the value underneath. Verified: every fill is still one of the six served
colours, and no fill carries an opacity.

**Hierarchy is weight, never a second colour.** A motorway is a thicker line
than a side street. A second hue here would be either a second accent (§2.2) or
a ramp hue on a data surface (§2.1), so the layer spends none.

**Both tiers are keyed to ground scale, not to zoom steps.** The first draft
gated them on `vk`, the scale relative to the country fit — a ratio, not a
distance, and it gets small provinces badly wrong: Смолян's own fit is 6,6×,
which cleared a `vk >= 6` gate and drew that city's entire street network into
about **eighteen pixels**. A smudge that claims to be a street map is worse
than no street map. The gates are now **pixels per kilometre** — 2,5 px/km for
the national network, 25 px/km for streets — computed from the projection
itself, because what decides whether a street can be seen is how many pixels a
kilometre gets.

That also keeps the country view a choropleth: at the country fit the scale is
1,1 px/km and neither tier draws. 813 lines over 28 provinces is a second
dataset competing with the one the reader came for.

**Compound selectors, because one element carries two classes.** The tier class
sits on the casing and on the core alike, so a bare `.map-road--street {
stroke-width }` also hits the casing and collapses it to the width of the line
it exists to carry. Every rule in that block names tier *and* role.

**Four cities have no streets yet, and the asset says so.** Overpass returned
504 and 429 for Варна, Видин, Враца and Ямбол; those boxes carry no geometry
and simply draw nothing. `missing_cities` records which, so the gap is a
recapture rather than a mystery — the same rule as a slug the snapshot has
never seen.

**Out is one step everywhere, including out of a province.** `k` is relative
to whatever the current framing fits, and a province fits at 2,5–6,6× the
country — so clamping `k` at 1 made the province's own fit the furthest the
reader could get, and the first press of minus had nowhere to go but the whole
country. One press from a city to the Balkans is not a zoom, it is a teleport.
The floor is now `1/fit`, the point at which the province view has scaled down
to country scale and the two framings agree; only there does the mode change.
Measured, one press at a time: 4,5 → 3,0 → 2,0 → 1,3 → 1,1 px/km.

**A marker carries its own reading.** Every point was the same 7px dot, so two
places 30 µg/m³ apart looked identical: the layer said *there is a city here*
and nothing about the air, which is the one thing this map is for. The mark is
now large enough to hold its number, filled with the served band and inked from
that fill's own luminance — the same rule a province's value already follows
(§2.1), one tier down. A place with no reading keeps a smaller dot and an em
dash, because absence is stated where the value would be (§2.3).

**There IS a history endpoint, and the earlier note that there was none was
wrong.** `/api/v1/area/{slug}/series?metric={P1|P2}&period={24h|7d|30d|1y}`
returns parallel `t`/`v` arrays for oblast, city and neighbourhood slugs. It was
missed because every probe had been shaped like `/areas/...`; this route is
`/area/...`, singular, and it answered **400 with a message naming the valid
parameters** rather than 404. A 400 is a route that exists and a request that
was wrong — worth reading, where a 404 is worth abandoning.

**So the detail card is measured, not sampled.** Clicking a marker used to
produce one line — a caption, not an answer. The card under the map now carries
both particulates with their served bands, the sensor count behind the
aggregate, how the place compares with the province around it, and a real
history chart. `assets/bg-series.json` holds the capture, because the API sends
no CORS header and a browser cannot read it from this kit at runtime (§5.3a).

**The card opens on the province, and a marker swaps the subject.** It first
appeared only after a marker click, which left the chart unreachable: the API
refused the finer-tier series during capture, so no city or neighbourhood has
one, and a province is not a marker. A panel reachable only by a path that
produces nothing is the dead control again — and the province is the better
default regardless, since it is what the reader opened the page to see.

**11 provinces have captured history; the rest say so.** The API rate-limited
the capture partway through and then refused it entirely, so 0 of 30 cities and
neighbourhoods have a series yet. Where none exists the card states that the
*capture* is missing rather than implying the place has never been measured, and
draws nothing. A chart of invented numbers under a real reading would be the
fabrication this system has already removed twice.

**A class rule beats a presentation attribute, and it clipped a label.**
`.place-chart__tick` sets `text-anchor: end` for the value axis, and that rule
also matched the clock labels — so the right-hand one was centred on the frame
edge and printed as *21:3*. The anchor is now a class per position. Same family
as the `[hidden]` reset: when markup and stylesheet both have an opinion, the
stylesheet wins, so put the intent where the winner can see it.

**The frame is draggable, and the browser's own handle does it.** `resize:
vertical` on the frame, `overflow: hidden` so it applies at all. No JavaScript
writes a height — which matters twice over: `style=""` is forbidden outright by
the CSP (§1), so a hand-built grip would have had nowhere to put the number,
and the native handle already carries the cursor, the hit area and the pointer
behaviour that a custom one would have to re-implement.

**Vertical only.** The frame's width belongs to the page's containers (§4.1),
and width is also the axis that crops the country (§1). There is nothing to
gain by letting the reader narrow it and something real to lose.

**`--map-view-h` now sets the DEFAULT shape, and the box sets the drawn one.**
The token moved from the canvas to the frame, because the frame is what the
reader drags: an explicit height written by the resize handle can only beat the
default if both live on the same element. The renderer reads the token first —
so a frame that cannot be measured (jsdom, `display:none`, detached) still
draws at the intended shape — and then lets the **measured aspect win whenever
there is one**. Once the reader has dragged, CSS no longer knows the height;
only the box does.

**Dragging taller shows more latitude, never a letterbox.** The pass re-runs at
the new shape rather than scaling what was already drawn, so the extra height
is filled with map. Dragging *shorter* is clamped at `VH = 382`: the viewBox
width is fixed at 926, so a shorter box is a *wider* aspect, and §1 is explicit
that wider than 926∶382 crops the country while taller is always safe. Below
the floor the SVG letterboxes instead of losing Bulgaria — the correct
direction to fail in. Verified: a 900×200 box draws `0 0 926 382`, not a
cropped country.

**Two guards on the redraw, both load-bearing.** A repaint changes the frame's
contents, which can fire the observer again, so a pass only runs when the
aspect has moved more than 1,5 % — which a re-render never does on its own. And
the work is deferred to the next animation frame, so a drag that fires dozens
of resize events repaints once per frame rather than once per event. Verified
with a stubbed observer: a no-op resize leaves the viewBox untouched, a real
drag redraws once, and repeated events at the same size stay stable.

**Three limits worth stating rather than discovering.** The native handle is
**not keyboard-operable** in any browser — the keyboard path to a bigger map is
the full-screen control, which is why `resize` is switched off in full screen
(a handle there would offer to shrink the thing the reader just asked to fill
the screen). The size is **not persisted**: restoring it on the next load would
mean writing a height from script, which the CSP forbids. And `resize` is
**unsupported on iOS Safari**, where the frame simply keeps its default shape —
a missing convenience, not a broken control.

**A marker's colour was a class rule beating its own attribute.** Every point
had the right `fill` on it — the served band, computed exactly as a province's
value is — and every point rendered the same grey, because
`.map-point__dot { fill: var(--surface-2) }` sat in the stylesheet above it. A
presentation attribute loses to any class rule, so the layer *looked* correct in
the DOM, in the attribute, and in every check that read either one, while the
screen showed one flat colour for readings 30 µg/m³ apart. Same family as the
clipped `21:3` tick, and the second time this exact defect has shipped: when
markup and stylesheet both have an opinion, the stylesheet wins. The default for
a marker with no reading now lives on `.map-point--none`, where it is the only
rule that applies and nothing has to lose a fight to get there.

**A city is divided, and the map now says how.** Zoomed into София the reader had
one served band and a scatter of streets with nothing to place them against —
"how far am I from that sensor" had no answer without the divisions people
actually navigate by. The райони are real administrative boundaries:
`assets/bg-districts.json`, OSM `admin_level=6` via Overpass (ODbL 1.0), 35
districts across София (24), Пловдив (6) and Варна (5), simplified to ~25 m and
localised like every other geometry here (§1). Бургас, Русе and Стара Загора
returned nothing at that level because they have no districts, which is an answer
rather than a gap.

**Below район there are квартали, and they were missing from everything.**
*Бояна*, *Княжево*, *Горна баня* — the names people actually navigate Sofia by
— appeared in no layer: the API publishes no aggregate below район, and the
district capture is `admin_level=6`. A reader looking for their own квартал
found the map silent about it. `assets/bg-quarters.json` is 186 of them,
OpenStreetMap via Overpass (ODbL 1.0), `place=suburb|quarter|neighbourhood`.

**Names only, and that is the honest form.** Overpass returned relation
geometry empty on this capture, so there are no outlines — a label at a real
centre states what is known, where an invented boundary would not. The asset
records that in its own note rather than leaving the next reader to wonder.

**They carry no reading and never take a band colour (§2.1).** Nothing below
район is measured, so a quarter drawn like a reading would be a fabricated one.
The layer is basemap context and is styled as such: 10px, `--fg-2`, under the
district names in the visual order.

**Gated at 45 px/km, between the district names and the street names**, and
placed after both — a quarter never covers a reading it cannot explain.
Measured on София-град: 0 at the province framing, 51 at 5,1×, 25 at 11,4× as
the frame narrows. On-screen only, like the neighbour markers.

**English names came from Wikidata where OSM had none.** Ten districts carried no
`name:en`; eight carried a `wikidata` QID, and Wikidata's own English label filled
seven of them. Три Пловдивски района — Южен, Тракия, Източен — have neither, so
they keep their Bulgarian name on the English map. That is the data's own
spelling; transliterating it would be the error §5.12 forbids.

**No fill, ever.** A tint over a served band re-colours data (§2.1) — the same
error the roads layer avoids with a casing — so the boundary carries itself: a
page-coloured casing under a thin dashed core. Dashed, so an administrative line
*inside* a province is never mistaken for the solid province border. Hierarchy is
weight and pattern; the layer spends no hue.

**The gate is 8 px/km, and 12 was one notch too strict.** At 12 the София-град
page opened at 9,7 px/km and showed no districts until the reader zoomed — on the
one province where the province *is* the city, and where the divisions were
asked for. A gate that hides a layer on its primary subject is set wrong. Larger
provinces open at 4–6 px/km and still show none, and the country view sits at
1,1 px/km where the map is a choropleth and nothing else.

**A district name is placed after the markers, and yields to them.** Placed at
its own centroid the moment the outline was drawn, it printed Връбница, Надежда
and Сердика straight through the dots carrying the readings — a basemap label on
top of the data it exists to orient. A reading outranks a place name, so names go
on last, against a list of boxes the markers have already claimed, with the same
4px gap the province labels use because not overlapping is not the same as being
separable.

**But refusing to move cost the whole layer.** Held to its centroid, exactly one
name of twenty-four survived — twenty-five markers sit over the middle of София.
A rule that drops a layer to avoid one collision has priced the collision wrong,
so a name now tries fifteen positions inside its own district's box before it is
given up. Same principle as a province label: a name has to be *inside* its
shape, not at its centre.

**The box is sized from the wider of the two names,** so the same district holds
the same anchor and the same count in Bulgarian and in English. Verified: the
anchors compare identical, BG against EN, on one page load.

**A district a marker already names is not named twice.** София has a район and a
квартал called Витоша, and both Кремиковци, and both Банкя — and the API's finer
tier for София-град turns out to be *the districts themselves*: 24 of its 25
places carry district names. So the first render printed the same word twice,
20px apart, which reads as a rendering fault rather than as two real places. The
marker stays, because it carries a reading; the outline still divides the city.
On Пловдив and Варна, whose only marker is the city, the district names are new
information and all of them are drawn as room allows.

**History is captured for 11 provinces and no finer place, because the endpoint
rate-limits by distinct area.** `/api/v1/area/{slug}/series` answers
`429 {"error":"rate_limited","message":"Too many different areas requested."}`
after a few dozen slugs — the same anti-enumeration posture as the map tiers
(§1), applied to history. A slug missing from `assets/bg-series.json` therefore
means *not captured yet*, never *a place without history*, and the file says so
in those words. An earlier pass wrote six slugs into a `no_series` list on the
strength of a 429; that list was a false negative dressed as a finding, and it is
gone. Capture resumes; it does not conclude.

**Double-click zooms in, about the point clicked.** It is the oldest map
gesture there is and it costs nothing to honour. Shift + double-click zooms out,
the same convention. The step is `AIRBG_MAP_ZSTEP`, published by the renderer
and read by the pointer code, so the gesture and the buttons cannot disagree
about how far one press goes — a second constant here would be a second answer
to one question.

**It is not offered on a province or a marker, and that is the honest scope.**
Those are links and buttons, and the browser delivers their `click` before any
`dblclick`: a double-click on Пловдив would navigate on the way to zooming, and
the reader would arrive on another page having asked for a closer look. So the
handler stands down whenever the pointer is on something clickable — the rest of
the canvas, which is most of it, zooms. Measured: 1,00 → 1,50 on the canvas,
back to 1,00 with Shift, and unchanged on a province link.

**The basemap had no side streets and no names, and that is what "blank"
was.** Zoomed into Овча купел the reader saw one served band, a district
outline and two trunk roads — because `assets/bg-streets.json` holds
motorway→secondary only, and the capture dropped the `name` tag entirely. The
layer was working exactly as built; what it was built from was too coarse to
navigate by.

**The minor network is per city, lazy, and never on the home page.**
`assets/streets/<slug>.json` — tertiary, residential, unclassified, living
street and pedestrian, with names, ODbL 1.0 from Overpass. София alone is
~16 000 ways and ~1,1 MB, so shipping every city with the country map would
make a reader who never zooms pay for detail they never see. The file is
fetched the first time the view is both deep enough to draw streets and within
8 km of that city, then kept.

**Which city, in kilometres — not in degrees.** The view centre is held in base
projection units, and a degree of longitude is not a degree of latitude, so a
degree-space radius is an ellipse pretending to be a circle. The test converts
through `pxPerKm`, the number the drawing is already using.

**`ZMAX` was a ceiling under the new gates, and that made a feature
unreachable.** Русе's own fit is 6,4 px/km, so eight times it is 51 px/km — the
street-name gate at 90 could never fire on that page however many times the
reader pressed plus. A gate above the ceiling is indistinguishable from a
broken one. The ceiling is now 24×, which puts every province past 100 px/km,
and the name gate is 60 so it is reachable on a large province as well as a
small one. Same lesson as the district gate, one turn later: **set a threshold
against what the surface can actually reach.**

**A street name rides its street, because three placement rules failed
before it.** Measured on a single segment, one name of eighty-three survived —
the geometry is simplified to ~13 m, so a segment is a few pixels. Measured on
the longest straight *run*, three of fourteen — a city street that holds one
bearing for 130 px is 870 m of unbent road, and streets bend. Both were asking
a curve to behave like a ruler. `textPath` is what maps have always done: the
type follows the line, and the only remaining question is whether the line is
long enough to carry the word, which is a length test and an honest one.

**And the street is the chain, not the fragment.** OSM splits one street into
many ways — at a junction, at a surface change, wherever an editor cut it — so
*ул. Борисова* is a dozen records of 150 m and no single one can hold its own
name. Same-named ways are joined back into chains once per file at load, by
endpoint. A street in two disconnected parts yields two labels, which is
correct: they are two runs of road, and each is tested for room on its own.

**Names go on last and yield to everything.** Readings first, then district
names, then streets — the order the reader needs them in. `data-street-names`
carries *drawn / passed-room / candidates*, because a bare count cannot say
whether a thin layer is thin data or a refusing placement; that distinction is
what turned the segment rule into the chain rule.

**Reversed rather than rotated.** Text on a path runs in the path's own
direction, so a street digitised east-to-west would set its name upside down.
The geometry is reversed instead, and both `href` and the namespaced
`xlink:href` are written, because the kit has to draw in whatever renderer the
reader has.

**Seven cities are captured and София is not, which is a gap, not a finding.**
Overpass answered 500/502 on every mirror for most of this pass; one Sofia
quadrant came back with 1 410 ways and the run timed out before it could be
stored, which is why the capture now writes each quadrant the moment it lands
and resumes from what is already there. One mirror answers **200 with zero
elements** because it serves a Switzerland-only extract — a success status
carrying no data, which an unguarded capture records as "this city has no
streets". Status is not evidence; the payload is.

**Buildings are measured and deliberately not shipped.** A 2,2 × 2,2 km box of
central София holds 6 106 footprints and 871 KB, which extrapolates to roughly
**32 MB** for the city — an order of magnitude past what a localised asset can
be here, and there is no tile server to fall back on (§1). A city-centre box is
feasible at ~0,9 MB per 2 km² if the value is worth that; inventing simplified
blocks to stand in for buildings would be the fabrication this system has
already removed twice.

**PATH A: the kit now draws the real basemap, and falls back when it cannot.**
The correction above left a fork — adopt the archive, or keep the stand-in.
Adopting it is the choice, because a preview drawn on geometry the reader
never sees validates nothing (§5.2).

**What mounts.** `map-tiles.js` registers the PMTiles protocol, mounts a
MapLibre map inside the frame under the data SVG, and locks bearing and pitch
to zero. That lock is load-bearing, not tidiness: the overlay projects
longitude through `X()` and latitude through `Y()` separately, which is exact
in Web Mercator only while the camera is north-up and flat. Allow rotation and
the readings shear off the streets they belong to.

**The projection is a seam, not a rewrite.** `X`/`Y` delegate to
`map.project` when `AIRBG_MAP_PROJECT` is published, and every shape, label,
marker and hit area in the pass already reads them — so the data layer lands on
the tile camera without a single call site knowing a second camera exists. The
same trick §5.2b used for the province view, one level up. Verified: a marker
draws at the exact pixel the tile camera projects its coordinate to.

**And the gate for that was set from the wrong state.** It read `zoom >= 9`,
a number taken from where a reader ends up after zooming in. Measured on the
deployed kit, every province page opens *below* it — Пловдив at 6,94,
София-град at 7,99 — so the archive was fetched, drawn, and immediately
covered by 19 solid fills at the one moment that matters, the load. The
condition is now the **framing**: a province view outlines, the country view
fills, and the zoom number only still applies to a reader who has zoomed the
country map in. Third time this system has set a threshold above what the
surface can reach (the district gate at 12, `ZMAX` at 8, this one) — **set a
threshold from the state the page opens in, not from the one it can be driven
to.**

**The choropleth yields once the basemap is the subject.** A solid fill hides
the streets the reader zoomed in for, and the obvious fix — dropping its
opacity — is the re-tint §2.1 forbids. So the served colour moves from `fill`
to `stroke` at zoom ≥ 9: same colour, full strength, different property.
Measured: at zoom 7, 20 provinces filled and none stroked; at 11.5, none
filled and 20 stroked, with all 28 value labels, all 28 names and the markers
still drawn. The reading never leaves the screen.

**The hand-built basemap stands down rather than being deleted.** Under the
tiles the context layer, the roads, the streets and the district boundaries do
not draw at all — the archive has all four, in the same projection and better.
They stay in the repository because they are still what renders when the tiles
are unreachable, which today is every host except airbg.org itself.

**CORS was the last blocker, and it was solved by moving rather than by
widening.** `tiles.airbg.org` answers `Access-Control-Allow-Origin:
https://airbg.org`, so the kit could not read the style, the glyphs or the
archive from `file://` or a preview host — and a whole prompt was written
asking the app to add a second origin to an allowlist. None of it was needed.
The kit now lives in the app repository and is served **by the app**, at
`https://airbg.org/design-kit/ui_kits/app/`. Its origin is therefore the origin
that was already allowed. **The narrower fix was to move the reader onto the
allowed origin, not to allow one more reader**; nothing about who may read the
tiles changed.

**Verified in a real browser, on the host, which no jsdom pass could do.**
`data-basemap="tiles"`, a MapLibre canvas mounted, **151 tile features
rendered** from the PMTiles archive, `© OpenStreetMap contributors` in the
attribution control, and the hand-built basemap correctly stood down — zero
roads, zero district outlines, zero street names in the SVG. Over it the data
layer drew 28 provinces and 57 label nodes. Zero console errors.

**And the same load exposed a defect that was invisible in the code.** A
province page opens on its subject (§5.2b) — but the tile camera was mounted
at a hard-coded country centre and zoom, and under the tiles it is the camera,
not the renderer, that decides where every coordinate lands. So the renderer
computed София-град's fit exactly as it always had, and then had it discarded:
the page opened on the whole country with 25 city markers piled into it. Every
line of the framing code was correct and none of it reached the screen.

**So the renderer publishes the framing and the camera applies it.** The
province's lon/lat box is written to the frame as the pass runs; `map-tiles.js`
fits the camera to it **once per subject**, keyed by province name — so a
reader who has zoomed keeps their view, and only `airbg:oblastchange` re-frames.
The renderer still never moves the camera and the camera still never computes a
fit: the same one-owner seam as `X`/`Y`, one level up.

**The kit's own stand-down still exists and is still the normal case
elsewhere.** Opened from `file://`, from a preview daemon on loopback, or from
any other host, the origin does not match, the style read fails, and the SVG
basemap draws as it always did. That is why the hand-built geometry stays in
the repository rather than being deleted the moment the tiles worked once.

**Fall back visibly, never silently.** Three separate paths stand the module
down — libraries absent, WebGL refused, style unreachable — and a fourth
catches a tile error *after* mount, removes the canvas and hands the frame back
to the SVG. The app's own `docs/tiles.md` documents the failure this guards
against: a style whose layers match nothing renders a blank map with no error.
A blank map is the worst thing this system can ship, so every route out of the
tile path ends in a drawn map.

**An error with an empty message cost more than no error would have.** The
first tiled draw threw `ReferenceError: frame is not defined` — `tiled()` was
defined in the pass's shared scope and closed over a variable that only exists
inside the per-frame loop — and the renderer's catch printed
`e.message` alone, which for that throw was empty. The catch now logs the
stack. **A rescue that hides where it rescued from is not a rescue.**

**The render caught what the DOM could not.** The zoom cluster's "whole
country" button was corner brackets around a dot — the full-screen control's
own glyph, in an identical 40px square 8px below it. Two different actions,
near-identical marks, adjacent. It is now a bordered area divided into regions,
which is what the button returns to.

**The POIs were never missing. The layers that draw them were.** The reader
asked for streets, rivers, schools, kindergartens and shops and could see none
of them, and the reference map they sent — a sensor.community view of София —
was drawn from **the same source we use**: OpenStreetMap. That is what settled
it. Our archive is a planetiler build of the Bulgaria extract with **no
`--profile` flag**, so it carries the default OpenMapTiles layer set, and the
app's own `docs/tiles.md` line 136 lists what that set contains: *water,
waterway, landcover, landuse, park, building, place, **poi**, housenumber,
aeroway, aerodrome_label*. `tools/basemap/style.json` drew **19** layers and
`poi` was not among them. 217 MB of schools and shops shipped to every reader
and nothing asked for them.

**A style is a selection, not an inventory, and the capability was declared
absent from the wrong document.** Every previous pass read the *style* to learn
what the basemap had — which answers what we chose to draw, never what exists.
This is the same shape as the third-party-origin correction above, one level
down: a constraint was inferred from an artefact that could not carry the
answer. **Read the schema, not the stylesheet, before concluding the data is
not there** — and before commissioning an Overpass capture or a rebuild to
replace it. The alternatives being weighed were a per-city POI capture and a
planetiler re-run; the actual fix is ten layers in a 10 KB JSON file and no
rebuild at all.

**Ten layers, four named categories and a catch-all.** POIs are grouped by
OpenMapTiles' own `class` — education, health, shops and eating places,
transport — because a single "POI" switch over a thousand unrelated things is
not a category a reader thinks in. The fifth group is a `!in` filter rather
than a fifth list: a class nobody enumerated still draws under *Други обекти*
instead of vanishing, the same rule as a slug the snapshot has never seen.

**They are grey, and that is §2.1 doing its job.** A POI is neither a state nor
a reading, so a hue here would be either a second accent (§2.2) or a ramp
colour on a data surface. Dot `#8a8378`, ink `#6b6459`, white halo — the
basemap's own neutrals. `text-optional` is set on every label, so a POI name
that will not fit is dropped rather than pushing anything else off the tile: a
reading outranks a place name everywhere on this map, and a POI outranks
nothing.

**Gated by zoom, not by taste.** Education, health and transport draw from z14
because they are the anchors people navigate by; shops from z15; everything
else from z16, with names one zoom later than their dots in every case. At the
zoom a province page opens on, none of them draw at all — the map is still a
choropleth, and 40 000 shops over 28 provinces would be the second dataset
§5.2b keeps refusing.

**The fontstack is the one the style already ships.** `Noto Sans Regular`,
because the glyphs are self-hosted at `tiles.airbg.org/glyphs/{fontstack}/` and
a name the archive does not carry renders **nothing at all, with no error** —
the blank-map failure `docs/tiles.md` warns about, arriving through a typo.

**Once they draw, they have to be switchable, so the categories are a
control.** The map is the primary surface and the readings are what the reader
came for; a city at z16 carrying every shop in it competes with them. The
control is §5.11's column menu **unchanged** — one disclosure owning
checkboxes, Escape closing and returning focus, a click outside closing without
stealing it. Two scoped rules were added and nothing else: it does not stretch
in the toolbar, and twelve categories are capped at `min(26rem, 60svh)` and
scrolled. Shortening the list to fit would hide a layer with no other way to
reach it.

**The options are read off the style, never listed in the control.** Every
layer carries `metadata["airbg:group"]`, and `map-layers.js` collects the
groups from the mounted style. A list of layer ids in the kit would be a second
thing free to drift from the style it describes — the same argument that put
`borders_bg` in the neighbours asset rather than in the renderer (§5.2). Add a
layer to `style.json` tomorrow and the control offers it with no kit change; a
group with no catalogue string prints its own key, which is a visible gap
rather than a silent omission (§5.12).

**It is not offered when there is nothing to switch.** From `file://`, from a
preview host, with WebGL refused, or after a tile error, the SVG basemap draws
and there are no tile layers at all. The whole disclosure stays `hidden` until
the camera exists. **And it does not probe for one:** `map-tiles.js` fires
`airbg:basemapchange` with the state and the map on all three of its paths —
mounted, stood down, failed — because two components each deciding whether the
tiles are live is two components free to disagree about it. Same seam as
`airbg:metricchange` and `airbg:oblastchange`.

**The квартали are in the archive too, and the hand-captured asset is now the
duplicate.** `place-village` already filters `suburb` and `neighbourhood` at
z≥11 out of the `place` layer — which is *Горна баня*, *Княжево* and *Бояна*,
the four names the reader listed as missing, from OSM, with `name:bg`, at every
zoom. `assets/bg-quarters.json` was captured to fill a gap that the basemap did
not have; it stays only as part of the SVG fallback, and it is recorded here as
a debt like `bg-roads.json` and `bg-streets.json` beside it, not as a feature.
The reason the reader saw no квартали is the gate, not the data: the province
page opens near z8 and that layer starts at z11.

**The control is exercised, not asserted — and the suite was made to fail
first.** The tiles themselves need a browser on the host, but every rule the
layer control holds is decidable without a GPU: which options exist, where they
come from, what one toggle drives, what survives a language switch, and when
the control refuses to appear. `design-kit/tests/map-layers.test.mjs` runs it
under jsdom — already in `web/node_modules` for the app's own suite, so no new
dependency (§1) — with MapLibre stubbed. 18 checks. Then five mutants were run
against it and all five died: `apply()` driving only a group's first layer, the
control shown while the SVG basemap draws, one option per layer instead of per
group, no re-render on a language change, and checkboxes defaulting to off. **A
green suite that has never been shown to fail is decoration**, and this system
has shipped enough controls that looked correct in the DOM while doing nothing
on screen to owe that step. `tests/` is not an allowlisted root, so it 404s on
the host — correct for a check.

**And because it is a hand deploy, the style checks itself.**
`tools/basemap/style.test.mjs` runs MapLibre's own validator over `style.json`
— the spec package is already in `web/node_modules` under the vendored
renderer, so again no new dependency — and then asserts three things the
categories depend on: every `text-font` names a stack the glyph endpoint
actually serves, every layer carries an `airbg:group`, and **every POI class
lands in exactly one group**, evaluated through MapLibre's own `featureFilter`
rather than by reading the JSON. A class in two groups is a POI two checkboxes
fight over; a class in none is a POI with no switch at all, which is what the
`!in` catch-all exists to prevent and what this proves. Five mutants, all
killed: a class enumerated twice, the catch-all narrowed to an `in`, an
unserved fontstack, a malformed filter, and a layer stripped of its group.

The check earns its place because of *how* a style fails: an unknown font, a
malformed expression or a layer matching nothing renders a **blank map with no
error**. There is nothing on screen to debug, and no build stands between this
file and the server.

**The archive stops at z14, and everything above it is overzoom.** Read off
the PMTiles header on the live server rather than assumed: max zoom **14**, and
`poi` is present at **z12–14** carrying a `name:bg` field. So the POI gates at
z14 / z15 / z16 do draw — MapLibre keeps using the z14 tile and scales it — but
a gate above 14 buys *placement and decluttering, never more detail*. Someone
setting z18 expecting finer POIs would get exactly what z14 holds. Worth
stating because it is invisible from the style: nothing in `style.json` says
where the data runs out.

**Verified against the deployed server, not the repository.** The style really
is serving 29 layers with all ten POI layers and every `airbg:group`; the
archive name has not moved; `Noto Sans Regular` answers on both the Latin
(76 021 B) and Cyrillic (125 058 B) ranges, and a stack nobody serves 404s —
which is what makes the first two results mean anything rather than being a
probe that says yes to everything.

**What is still unverified, and it is the last step: a human seeing a dot.**
`airbg.org` answers `ERR_BAD_SSL_CLIENT_AUTH_CERT` — the origin requires a
client certificate (`client_auth`, `require_and_verify`) while the tile host is
DNS-only and deliberately does not. Every input to the render is confirmed
correct; the render itself is not. **Confirming the inputs is not confirming
the output**, and this document has recorded enough controls that were correct
in the DOM and dead on screen to keep those two apart.

**Loopback does not substitute, and finding out why was worth the probe.** Both
review servers already serve the kit — `:8080` and `:8091` answer 200 on
`/design-kit/ui_kits/app/map-home.html` — but that route's CSP is
`connect-src 'self'`, with no tile host. So on loopback the tile request is
refused by the CSP *before* CORS is consulted, and the kit stands down to the
SVG basemap exactly as designed. Two independent blocks, not one: the local CSP
and the origin. A local review therefore exercises the fallback and says
nothing about the tiled path, which is the opposite of what it looks like it is
doing.

**A constant three files away decided what could be tested, and that is the
defect.** `map-tiles.js` hardcoded the style URL and `airbg-data.js` hardcoded
the API host. Both correct in production, and together they made the kit
reviewable **only** in production: on loopback the style fetch is refused — the
local CSP omits the tile host and the listener's ACAO names the app origin
exactly — so the kit falls back to the SVG basemap every time, exactly as
designed. Correct for the basemap, fatal for anything reachable only over the
tiled path. That is how the layer control came to be verified in jsdom, verified
in the style, and never once seen operating a real map.

**`origins.js` owns both, and nothing else decides.** Precedence: authored
`<body data-tiles-base>` / `data-api-base`, then `?tiles=` / `?api=` **on a
loopback host only**, then the production constants — byte-identical to what
shipped before, so a reader on airbg.org is unaffected.

**The query parameter is gated, and the gate is the point.** Ungated,
`?tiles=https://evil.example` is a link that repoints a reader's map at someone
else's server. The production CSP would refuse the connection anyway, but **a
guard that relies on a different system to be correct is not a guard** — so off
loopback the parameter does not exist. Only `http`/`https` is accepted from
either channel, and authored markup outranks the query so a served page is
never steerable by URL. Sixteen checks, and four mutants killed: the loopback
gate removed, any protocol accepted, the query outranking markup, and the
default moved.

**The stand-down did not latch, and it rebuilt the dead control out of its own
recovery path.** MapLibre can emit `load` *after* an error has already torn the
canvas out. The first version let that second event re-announce `tiles`: the
frame ended up marked `data-basemap="tiles"`, the layer control re-appeared with
every category ticked, and **there was no map in the document for any of it to
act on**. Reproduced exactly — host absent, canvas absent, control visible,
attribute still claiming tiles. A recovery path that can be undone by a late
event is not a recovery path; `downed` now latches and the `load` handler
returns early.

**And the one line explaining the whole screen was `console.info`.** A reviewer
looking at a stood-down kit reported *zero console errors* — accurately, because
the stand-down logged at info level, which most consoles filter and every bug
report omits. It is `console.warn` now. **A message nobody sees is not a
message**, and this one is the difference between "the basemap is broken" and
"the basemap was handed back on purpose, here is why."

**The camera is reachable from a console, on the loopback gate.** Diagnosing a
blank map through screenshots because `getStyle()` cannot be called is six
round trips where one `evaluate` would do. `window.AIRBG_TILEMAP` is published
only where `origins.js` already permits an override — never on airbg.org — so
production gains no new global.

**Verified by breaking it first.** The failure was triggered with a deliberately
malformed PMTiles archive (190 bytes, hand-built, invalid varint in its root
directory), which is a cheaper and more reliable way to reach the error path
than waiting for a real tile server to misbehave. Before the fix:
`data-basemap="tiles"`, control visible, zero canvas. After: `local`, control
hidden, and the SVG fallback drawing 28 provinces and 105 labels — which is the
promise the fallback exists to keep.

**CORRECTION — a screenshot of a WebGL canvas is not evidence, and one was
treated as evidence here.** The report that "no basemap paints on the kit map"
was **false**. The MapLibre context runs `preserveDrawingBuffer: false`, so a
screenshot of that canvas comes back blank whatever the map is doing; at the
moment of the blank screenshots the map was rendering **247 road features and
201 POI features** with all 29 style layers loaded. Tiles, style, `origins.js`
and the kit were all working. **The instrument was the broken part.**

`queryRenderedFeatures()` is the instrument for what a map is drawing.
Screenshots are fine for the DOM *around* the map and worthless for the canvas
itself — and a blank canvas screenshot is indistinguishable from a broken map,
which is the most expensive kind of false negative this project has hit.

**What survives the correction, and what does not.** The latch fix stands: a
stand-down that a late `load` can undo is a real defect, reproduced
deterministically, and `console.info` is the wrong level for the one line that
explains an empty frame. What does **not** stand is any claim that it explains
the reported blank map — that map was never blank. The hedge written at the
time ("this does not prove that run failed this way") was the correct reading,
and it is the reason the fix was not oversold.

**Q4 is closed, measured through the right instrument.** At Младост, z15.4:
201 POI features with every category ticked; unticking *Магазини и заведения*
drops to **134**, with `poi-shop` and `poi-shop-name` both flipped to
`visibility: none`; re-ticking restores 201. Other categories are untouched —
transport 92, health 12, education 5 — and the choice survives a reload. The
control drives the map.

**The diagnosis handle must not outlive the map it points at.** After a
stand-down `window.AIRBG_TILEMAP` was still set, so anyone debugging a blank
screen would read `getStyle()` off a torn-down map and conclude the layers were
fine. That is exactly the "state claims live when it is not" defect the latch
had just fixed, reintroduced by the tool added to catch it. It is nulled in
`stand_down` now.

**An offline fixture was attempted and abandoned, which is worth recording so
nobody repeats it.** A hand-built PMTiles archive carrying one z14 tile of
synthetic POIs would make the tiled path reviewable with no production access
and no 217 MB download. Two attempts failed the same way — `Expected varint not
more than 10 bytes` — because the v3 root-directory encoding is not obvious from
the outside. Worth doing properly with the `pmtiles` library rather than by
hand; not worth hand-rolling under time pressure, and a broken fixture in the
repository would be worse than none.

**Two sessions sharing one browser will corrupt each other's measurements.** A
page navigated itself mid-run because another session drove the same Playwright
instance. Whoever holds the browser should say so on the ticket before either
side trusts what it reports.

**The style is a deploy, not a build.** `tools/basemap/style.json` is served
from `/var/lib/airbg/tiles/style.json`; changing it is a copy and a reload. The
archive is untouched, so none of the 217 MB is regenerated and `tiles.archive`
does not move. **Until that copy happens the layers exist in the repository and
not on the reader's screen** — which is exactly the stale-asset failure §5.3a
describes, and it is stated here so the next reader does not debug correct code.

### 5.2a Theme — light, dark, and following the system

**Three states, not two.** *Автоматична* is the default and stays reachable: a
bare toggle can only say light-or-dark, so the moment the reader touches it they
lose the ability to follow the OS — and a site read at night on a phone that
switches at sunset should not need correcting twice a day. Same argument as
*Всички* in the table (§5.4): the way back is part of the control.

**The picker is icons, not words** — a sun, a crescent, and a half-filled
circle for *follow the system*. They are inline SVG in `currentColor`: no icon
font and no second asset (§1), and they inherit the masthead's colour on the
face and the menu's on each option. They sit in the same 20×12 mark slot as the
language flags, so the two pickers align on one grid (§5.12).

**An icon-only control still has a name.** Each option carries `aria-label` and
`title`, and the button carries both too — *"Тема: Тъмна"*. Both are written from
the catalogue on every apply, not baked into the markup: an icon-only control
whose only name is a hardcoded attribute is untranslated by construction, and
this one is verified to read *Automatic / Light / Dark* after a language switch.

**The face is a copy of the chosen option's mark**, not a second drawing of the
same idea. Two hand-maintained glyphs for one state drift apart the first time
one is edited.

The half-circle for *Автоматична* is the weak link: a sun and a moon are
unambiguous, "follows the system" is not a thing with a settled glyph. It has a
name on hover and to a screen reader, but if it reads as a puzzle on screen the
honest fix is to put that one word back rather than to keep hunting for a
cleverer symbol.

**The dark values are Carbon's Gray 100 theme, not darkened light ones.** The
light palette is a hand port of Carbon's White theme, and its dark counterpart
already exists and is contrast-tested; inventing one would be the same error as
drawing a flag from memory (§5.12). One deliberate substitution: `--muted` and
`--meta` take Gray 40 `#a8a8a8` rather than Gray 50 `#8d8d8d`. Gray 50 does pass
on `--surface` (4.56∶1) but with almost no margin at 12px, and captions are
where the margin matters.

**Measured, not asserted.** Sixteen pairings were computed before shipping —
body, secondary, muted and meta on all three surfaces, links, the solid button
and its hover, the danger colour, borders at the 3∶1 UI floor, and text on the
selected row. All clear their floors; the solid button's hover *raises* contrast
rather than lowering it (§7).

**The dark rules live in the same file as the palette, at the end of it.** They
were in `tokens.css`, which loads *before* `colors_and_type.css` — and that file
declares the whole light palette on `:root`. `:root` and `[data-theme="dark"]`
have **identical specificity** (0,1,0), so source order alone decides: the later
light `:root` re-declared every colour and the explicit dark choice lost. The
picker set the attribute correctly and the page never changed — a control that
looks broken while doing exactly what it was told.

Two consequences worth keeping. **A theme override belongs beside the palette it
overrides, after it**; split across files, the winner is decided by link order in
every page that happens to include both. And **the same defect existed twice** —
`examples/theme.css` had its dark block above a later light `:root` as well.
Fixing one copy of a cascade bug fixes one copy.

**Verified by resolving the cascade, not by reading it.** All three states were
computed from the concatenated sheets in document order with specificity applied:
unset → `#ffffff`, `dark` → `#161616`, `light` → `#ffffff`, in both the app sheets
and the standalone one.

**Cascade order is the feature.** `:root` light → `@media (prefers-color-scheme:
dark) :root:not([data-theme="light"])` → `[data-theme="dark"]`. That order is
what lets an explicit choice beat the system while an unset choice still follows
it. Reverse the last two and the picker stops working at night.

**The focus ring's inner band is the page colour, not white.** `0 0 0 2px #fff`
on a `#161616` page is a bright rectangle, not a focus indicator.

**The air-quality ramp is not re-tinted.** It is served (§2.1). A design system
that adjusts served data for its own theme is restating it.

### 5.3 Charts

- uPlot series stroke is `--accent`. One series, one colour; no second accent.
- The hover readout floats, so it is the legitimate shadow case (§4.4).
- Frame matches the map: `--frame`, square, hairline border.
- **The x-axis label is still uPlot's English `Time`.** It needs a catalogue string.
  A Bulgarian page with an English axis label is a defect, not a default.

**A chart states what it plots, in words, above itself.** The area screen carried
*"Последните 24 часа"* over an unlabelled series: the axis said `µg/m³`, which is
true of both particulates, so the reader could not tell PM2.5 from PM10 — and the
period was fixed, so the question "24 hours of what, ending when?" had no control
to answer it. The heading is now composed from the current state and reads
*metric · window · tier* — **ФПЧ2.5 · последните 24 ч · средно за областта** —
which satisfies §9.1 on the chart as well as on a readout.

**Metric and window are the reader's.** A metric switcher (§5.6) and a window
control sit beside the heading: 6 / 12 / 24 / 42 hours, plus *Избран* disclosing
`from`/`to` fields. Both redraw the series, both rewrite the heading and the
`aria-label`, and the axis ticks print the real clock times of the window chosen.
A control that leaves the picture unchanged is the failure this system has hit
most often; these were tested by asserting the path data differs after each.

**Both particulates can be on the panel at once, so the control is
checkboxes.** The two were a radio pair, which is a claim that the states are
mutually exclusive — and they are not: the PM2.5∶PM10 *ratio* is itself
informative (§5.4 says so about the table), and reading it meant flipping
between two pictures and remembering the first. The `fieldset` stays; the inputs
become checkboxes. This narrows §5.6: the fieldset is the grouping semantics for
either, and radio-versus-checkbox follows from whether the states combine.

**The second series is dashed, not coloured.** The palette has exactly one
chromatic UI colour (§2.2) and every other hue on this site belongs to the
air-quality ramp (§2.1), so a second stroke colour would be either a second
accent or a ramp hue reused to mean something other than air. PM10 takes the
same blue with `stroke-dasharray`. Pattern also survives greyscale printing and
colour blindness, which a second hue does not — the constraint produced the more
robust answer.

**The fill goes when two series are plotted.** Two areas over one another read
as a third value that is not there. A single series keeps its fill.

**One scale, not two.** Both series share the y axis, so the reader compares
heights directly; the ceiling is computed from the higher peak. Twin axes make
PM2.5 and PM10 look equal when one is triple the other — and PM10 contains
PM2.5 (§5.4), so their relationship is the whole point of showing them together.

**The key appears only when there is more than one line.** With one series the
heading names it and a key would be exactly the duplication removed from this
panel a moment ago; with two, nothing else says which line is which.

**Zero series is not a state.** The last checked box goes `disabled`, which
states the limit — the same call as the last visible data column in the column
menu (§5.11). It is set on load as well as on change, or the first paint ships
an unlocked lone checkbox that can empty the panel.

**The panel names the unit; the heading names the metric.** Both printed
*ФПЧ2.5* — the heading as *ФПЧ2.5 · последните 24 ч · средно за областта* and
the axis caption as *ФПЧ2.5, µg/m³* — so one component said the same thing twice
within 30px. Repetition inside a single component is not reinforcement: the
reader stops to check whether the two labels differ, and they never do. The axis
caption is now `µg/m³` alone.

**The unit is not the duplicate, and it stays.** The heading carries no unit, and
§9.4 requires one beside every measured value, so deleting the whole caption to
kill the repeat would have removed the only statement of what the numbers are.
Cut the half that repeats, keep the half that does not.

**A series without a scale is a picture, not a reading.** The chart carried
three clock labels and *no y values at all* — the reader could see the shape and
price nothing on it. It now has a grid: horizontal lines at the y ticks, vertical
lines on whole hours, and both axes labelled.

**Gridlines land on round numbers, or they are decoration.** Steps come from
1 / 2 / 2.5 / 5 × 10ⁿ, so the axis reads *0 · 1 · 2 · 3* rather than
*0 · 0,87 · 1,74*.

**The step is chosen, not derived from a target count.** The first rule computed
`niceStep(peak / 4)` and rounded the ceiling up — which overshot badly at some
peaks: a peak of 10,2 took a step of 5 and a ceiling of 15, leaving a third of
the panel empty above the data and only three lines in it. The step is now the
**smallest** round value that keeps the grid under six lines, and the ceiling is
the first multiple at or above the peak. A peak of 8 draws 0 · 2 · 4 · 6 · 8 · 10;
a peak of 3,8 draws 0 · 1 · 2 · 3 · 4. The extra step is added only when the peak
lands exactly on the top line, so the series never runs along the frame.

**The axis is anchored at zero and stays there.** A zoomed baseline would fill
the panel with the variation the reader is squinting at, and that is exactly why
it is wrong here: PM is a concentration measured against health thresholds, so
the distance from zero *is* the information. A chart that starts at 1,4 makes a
quiet day look like a spike. The honest way to make small variation legible is a
tighter ceiling, which the step rule above now gives.

**The hour step is one a reader already thinks in** — 1, 2, 3, 6, 12 or 24 hours,
whichever first gets the window under eight labels. Labels sit on whole hours, so
they read *12:00*, never *12:37*.

**Horizontal rules only; hours get a tick on the axis.** The first grid drew a
rule at every y step *and* every hour — thirteen lines across a panel holding one
series, which is a hatch pattern the data has to compete with. Verticals earn
their place only when a moment must be aligned to something, and here the label
already sits directly under the point. Four horizontal rules and a 4px tick per
hour say the same thing and leave the series alone.

**The grid is chrome, so it takes `--border-soft` at 55 % opacity** — the palette
has no lighter step than the hairline token, and at full strength even four rules
read as structure rather than as background. Opacity is the right lever: it dims
the grid in both themes without inventing a colour outside §2.2. The grid is
drawn before the series so the line never sits under a rule.

**Axis numbers are measured values and follow their rules (§3):** tabular
figures, so the axis does not reflow as the window changes, and the locale's
decimal separator. The `aria-label` composes its own min and max, and it used
`toFixed` — printing *3.7* to a screen reader while the axis showed *3,7*. One
formatter, or two surfaces disagree about the same number.

**Both axes are rebuilt on every draw.** How many lines belong on the chart is a
function of the window and the peak, so they cannot be markup; the previous three
`<text>` ticks were a fixed answer to a question whose answer moves.

**A custom window states its days when it crosses one.** *По избор* over 24
hours ending now composed *14:20–14:20* — two identical clock times, which reads
as a window of zero length rather than of a full day. The heading now prints the
date beside each end whenever the two fall on different days, and the day name
comes from `Intl.DateTimeFormat` in the current language rather than a month list
typed into the catalogue (§5.12). Same rule as §9.5: a timestamp that a reader
can misread is not a timestamp.

**The option is *По избор*, not *Избран* and not *Избери*.** The legend reads
*Период* and the siblings are lengths — 6 ч, 12 ч, 24 ч, 42 ч — so the label has
to complete *"Период: …"*. *Избран* is an adjective with its noun missing;
*Избери* is an imperative standing in a list of nouns, and it describes the
control as a command when the radio itself is the action. *По избор* is the
ordinary Bulgarian for a custom value and is grammatically parallel to its
siblings.

**The archive floor is a fact, not a preference.** 42 hours is what the app
retains, so the `datetime-local` fields carry `min`/`max` from it and the note
says so plainly. An out-of-order range (`from` ≥ `to`) is ignored rather than
drawn — a chart of nothing is worse than the chart you already had.

**The chart's own copy says the series is sample data**, and names the endpoint
that will replace it *in the design system*, never on screen (§5.5). The
superseded caption — *"Една серия, щрих `--accent`, запълнена площ"* — described
the CSS to a reader who wanted to know about air.

### 5.3a Loading and refreshing

**One fetch on load, one per press. Nothing on a timer.** A public-health page
that re-renders under the reader is the §8 defect — motion is confirmation, not
performance — and a reader comparing two provinces should not have the figures
move while they look. Freshness is the reader's to ask for.

**One fetcher, one event.** `airbg-data.js` owns every request; the map, the
table and the readouts listen for `airbg:datachange` and re-read
`window.AIRBG_DATA`. If each component fetched for itself they would answer at
different moments and the screens would disagree about one province — the §9.3
defect, arriving through the network instead of through a typo.

**The API sends no CORS header**, so a browser can read it only from airbg.org
itself; from `file://` or any other host the request fails. That is the kit's
normal case, so the bundled snapshot is not a fallback bolted on afterwards —
it is half the design. The screens render from it first, then upgrade if the
API answers.

**The control sits under the legend, not above the data.** It first ran across
the top of each screen, where it read as a page-level toolbar and pushed the
data down. The key and the timestamp are captions on the same thing — one says
what the values *mean*, the other how old they are — so they belong together,
directly under the scale. On the table, which has no legend, the equivalent
anchor is the ordering note that explains the rows.

**Button first, status after.** The button is the action; the status is its
result. Leading with a sentence that changes length would shove the control
sideways every time the data reloaded.

**Icon and word, not one or the other.** A circular arrow is the one refresh
glyph readers already know, so it earns recognition at a glance — but alone it
would be a puzzle beside a timestamp, and §5.2a's rule stands: the glyph is the
affordance, the name is still required. Inline SVG in `currentColor`, no icon
font (§1).

**The status line says which of the two it is showing.** *"Данни от 12:10, 1
септември · локално копие"* against *"Данни от 18:45, 1 септември"*. A refresh
button beside a figure with no statement of freshness answers half the question
it raises, and a stale number presented as live is the plainest §9 breach there
is.

**Refreshing preserves the reader's state.** The table's rows are the same DOM
nodes with new values, so sort, filter, search and page all survive. Rebuilding
them would silently reset every one — changing the view under the cursor (§5.4).

**Loading is a muted line of text, not a spinner** (§8), and the button is
`disabled` with `aria-busy` while in flight, so a second press cannot race the
first.

**The licence is displayed because the data is displayed.** Readings are
sensor.community under ODbL 1.0 and boundaries are OpenStreetMap under ODbL
1.0; both strings come from `/api/v1/meta` rather than being typed here, so
they cannot drift from the licence they satisfy. This is an obligation, not
chrome — it appears the moment live data does, and the footer carries it on
every screen.

**One version query for every asset, bumped together.** Each script and
stylesheet is referenced as `file.js?v=N`, and the number was maintained by
hand — so it drifted three ways at once. `map-render.js` sat at `?v=1` through
every change to it, and the reader's browser went on serving the copy it had
cached: the map on screen was several fixes behind the map in the repository,
and the labels reported missing were present in the DOM the whole time. Worse,
the *same file* carried different numbers on different pages — `theme.js` at
`?v=4`, `?v=5` and `?v=10`, `i18n.js` likewise — so one screen served a fresh
copy while its neighbour served a stale one, and the two disagreed about
behaviour no one had changed.

Two rules come out of it. **The version is per deployment, not per file**: all
50 references now read `?v=11`, and the next change moves every one of them.
And **a stale asset is indistinguishable from a bug** — it presents as a fix
that did not take, which sends the next hour into the code that is already
correct. Before debugging a change that "did not work" on screen, confirm the
browser is running the file you edited.

### 5.4 Oblast list — a table, not a directory

28 rows of name-plus-count is a directory (D3). Under this contract it is a table
at `--frame`:

| column | treatment |
|---|---|
| Oblast | 16px/400, the link, `--accent` |
| Current PM2.5 | 14px tabular-nums, right-aligned, with a ramp chip |
| Current PM10 | same as PM2.5. Sortable independently |
| Sensors | 14px tabular-nums, right-aligned, `--fg-2`. Those contributing to the oblast aggregate |
| No data | `--muted`, spanning the two PM columns only, no colour |

**A sensor count must say what it counts.** "Сензори" alone has two plausible
readings — reporting now, or installed — and a reader will pick whichever suits them.
`/api/v1/areas` returns one figure, `sensor_count`, so the table ships one column and
the caption defines it: sensors contributing to the oblast aggregate. An earlier draft
carried a second *Регистрирани* column with invented totals; the live API has no such
field, so it is gone. Define the column you have rather than inventing the one that
would make the pair look complete.

**A silent oblast still shows its sensor count.** The PM columns collapse to
*"Няма скорошни данни"*, but the row still prints the count — usually `0`, and for
Силистра `2`, which is the more interesting case: hardware present, nothing
aggregating. Absence
stated *and* accounted for is still not an error (§2.3) — it is just less mysterious.
This is also why the silent rows sort normally on either sensor column: they have
real values there, unlike the PM columns.

**The country total is the sum of the column.** 1072 sensors across 28 oblasti, and
the home page prints that same figure. Two surfaces disagreeing on the network's size
is the §9.3 defect again.

**The table shows both PM metrics; the map switches between them.** That is not an
inconsistency, it is the medium. A map dot can carry one colour, so the map needs the
metric switcher (§5.6). A table row has as many columns as it needs, and the PM2.5∶PM10
ratio is itself informative — coarse dust and combustion do not produce the same
ratio. Putting a switcher on the table would hide half the data to save nothing.

**PM10 is never below PM2.5 for the same oblast**, since the coarse fraction contains
the fine one. A row that violates this is a data defect, and the table should not be
made to render it as if it were ordinary.

**Any oblast's figures must agree across screens.** Пловдив reads 2,48 / 5,62 with
110 sensors on the area page, so it reads exactly that here. (The figures quoted
here were themselves stale — the contract claimed 52,1 / 78,4 and 57 of 61 while
both screens showed the snapshot's numbers. The area page also said *61 сензора*
in its intro beside *110* in its own readout: one screen contradicting itself,
which is the same defect at closer range.) Two screens
disagreeing about one oblast is the defect §9.3 exists to prevent, and it is the
easiest one to introduce by editing a number on one surface only.

Zebra by `--surface`, hairline `--border-soft` between rows, hover to `#e8e8e8`.
Default order is value descending.

**All 28 oblasti are always reachable.** A truncated list is the defect this
section was written against: a reader whose oblast is missing cannot tell whether it
has no sensors, has no recent data, or was simply cut off. There is no "show more".

**The table opens on *С данни*, not *Всички*.** The reader comes to find out whether
the air is bad, and eight rows that say *Няма скорошни данни* answer nothing — they
are the least useful rows on the page in the first five seconds. So the default view
is the 20 oblasti that are actually reporting.

This is a filter, not truncation, and the difference is what makes it allowed:

- the control is **visible above the table** with its state showing, so the reader can
  see a filter is on without deducing it;
- the count line **accounts for the hidden rows and says why** — *"Показани 20 от 28
  области. Останалите 8 нямат скорошни данни; изберете „Всички“, за да ги видите."*
  It names the number, the reason, and the way back in one sentence;
- **one click restores all 28**, and the denominator was 28 the whole time.

Truncation is silent, unexplained and one-way. Strip any of the three properties above
and this default becomes the very defect the paragraph before it forbids.

**The note about ordering sits after the table, at the full frame width.** It
explains that the default sort is PM2.5 descending and that silent provinces stay
last — a statement about what you have just read, so it belongs below the table
rather than in front of it. Above the table it was a paragraph inside the page
head, where a `60ch` cap held it to roughly a third of a 78rem surface: a long
sentence stranded in the top corner with the rest of the width empty beside it.

It takes compact size and `--fg-2`, not body weight. At the foot of the page a
body-size paragraph would outweigh the count line above it and compete with the
footer disclaimer, which is the one line that must be read (§5.5). A hairline
rule separates it from the count without turning it into a card.

**Search, filter and sort are part of the component, not an enhancement.** 28 rows is
past the point where scanning works:

- **Search** — a **combobox** (§5.10) over the oblast name. Case-insensitive,
  substring, `toLocaleLowerCase('bg')`. It does three separable things as you type:
  filters the table live, lists the matching names so you can pick instead of type,
  and inline-completes the input to the first *prefix* match. Zero hits is `--muted`
  body copy, no colour and no icon (§2.3). Picking a name is always exactly
  equivalent to having typed it — no hidden third state.
- **Filter** — the same `fieldset`/`legend` segmented control as the metric switcher
  (§5.6): Всички / С данни / Без данни. A radio group, because the states are
  mutually exclusive.
- **Sort** — every column header is a `<button>` inside its `<th>`, and the `<th>`
  carries `aria-sort`. Name sorts through `Intl.Collator('bg')`, never by code point.
  First click sorts names ascending and measurements descending, which is what each
  column is actually read for.

**Two invariants that outrank the sort the reader picked:**

1. **No-data rows sink to the bottom in every order, ascending included.** A reader
   looking for bad air should not scroll past eight blanks. Among themselves they
   stay alphabetical rather than arbitrary.
2. **The count line's denominator is always 28.** *"Показани 6 от 28 области"* — a
   filtered view must never be mistakable for the whole country (§9.3). A paged view
   states its range too: *"Показани 11–20 от 28 избрани области, от общо 28."*

**Paging defaults to off**, at **Всички**: 28 rows fit on one screen, and four clicks
to read a country is worse than one scroll. The control exists because search results,
the sensor lists on an area page, and any future longer table need it — not because
this table does.

**The page sizes are derived from the rows currently visible, not from the table's
total.** Under *Без данни* there are eight rows, and offering 21 or 14 there offers
nothing — both show all eight. The set is rebuilt whenever the visible count changes:

| showing | offers |
|---|---|
| 28 (unfiltered) | Всички · 14 · 7 · 4 |
| 20 (С данни) | Всички · 10 · 5 · 4 |
| 8 (Без данни) | Всички · 4 |
| 5 or fewer | Всички only — the control hides |

Only **true divisors** qualify, so every page is exactly full and no split ends in a
stub. A page must hold at least three rows: dividing eight into fours is useful, into
twos is not. At most three numeric options, largest first — the numbers stay bare,
because a reader seeing 14/7/4 against 28 rows can infer the arithmetic.

A correction worth recording: an earlier set was **28 · 21 · 14 · 7**, described here
as "the divisors of 28". **21 is not a divisor of 28** — it splits the table into
21 + 7, which is precisely the stub this rule exists to prevent. The claim and the
worked example contradicted each other in the same paragraph.

**Rebuilding is not the same as removing.** The list is rewritten only when the
visible count actually changes — a deliberate act by the reader, filtering or
searching — never mid-render and never while they are inside the control. What the
anti-pattern below forbids is options vanishing or greying *under the cursor* during
one interaction.

**When no size qualifies, the whole control hides.** At five rows or fewer there is
nothing to page, and a select holding one option is a control that cannot do
anything. This refines the §5.9 rule that the select never hides: that rule protects
the reader's way *back* to more rows, which the filter and search still provide.

**"Всички" resolves to a finite page size — never `Infinity`.** It is the whole set,
so the natural sentinel is the row count, not an infinite one. `Infinity` looks
harmless and is not: the offset is `(page − 1) × size`, `0 × Infinity` is `NaN`, and
`slice(NaN, NaN)` coerces to `slice(0, 0)` — an empty table on the default setting.
A sentinel that poisons the arithmetic around it is not a sentinel.

**"Всички" is a named option, not the literal 28.** The two look interchangeable and
are not: `28` is a claim about how many rows exist, while *Всички* is a claim about
wanting all of them. Once a filter narrows the set to 20 or 8, the literal is simply
wrong, and it collides with the redundancy rule below — an option equal to the row
count tests as redundant against itself. Name the intent, not the current count.

**Page sizes are never disabled, and never removed.** An earlier rule greyed out any
size at or above the filtered count, reasoning that it duplicated Всички. It read as
breakage: on the *С данни* filter — 20 rows — the option **21** went dead, a one-row
difference the reader cannot see or explain. A size larger than the current set is
harmless: it shows every row, the pager hides itself at one page (§5.9), and nothing
on screen misleads. The rule guarded a non-problem and manufactured a real one.

The general lesson, and this design system has now hit it four times: **a rule that is
correct in the middle of its range can still be wrong at the boundary.** Disabling,
hiding and auto-correcting all deserve the same test — *what does this look like to
someone who cannot see the reasoning?*

**Paging is applied last, after filter and after sort.** Both invariants above
therefore survive it: page 3 of an ascending sort still cannot put a no-data row above
a measured one. Any change to the set — search, filter, sort, page size — returns to
page 1; leaving the reader on page 3 of a result set they have not seen the start of
is disorienting, not helpful.

The pager hides itself entirely when there is only one page. A lone disabled page
control implies content the table does not have.

### 5.5 Footer

Four stacked body-weight paragraphs currently weigh as much as a content section
(D6). Demote: 12px, `--meta`, on `--surface`, at `--measure`. The disclaimer keeps
body size and `--fg-2` — it is the one line that must be read.

**The footer addresses the reader, not the developer.** It carries three things: the
disclaimer, how the readings are aggregated, and when they were updated. It does not
carry implementation facts — the repository's licence, the framework, or the endpoint
the ramp is served from. *"Скалите се сервират от `/api/v1/scales`"* was true and
irrelevant: a reader checking whether their air is bad has no use for a route, and a
public-health page that footnotes its own API reads as a project about software rather
than about air. That the ramp is server-owned is a rule for whoever builds the site
(§2.1); it belongs in this document, not on the page.

### 5.6 Buttons and the metric switcher

48px tall, `0` radius, `--accent` / `--accent-on` for primary; ghost is transparent
with `--accent` text. Focus is the Carbon double ring:
`0 0 0 2px #fff, 0 0 0 4px var(--accent)`.

The metric switcher is currently a native `fieldset`/`legend`. Keep the fieldset —
it is the correct grouping semantics for a radio set, and it is what a screen reader
needs. Style the legend as a 12px `--fg-2` label and the options as a segmented
control of square 40px buttons. Do not replace it with divs.

### 5.7 Text input

Carbon's filled field: `--surface` fill, square, 48px tall, and a single
`1px solid var(--border)` bottom rule. There is no box outline, so the bottom rule is
the entire affordance — which means focus cannot be signalled by changing a border
colour. It gets the same double ring as every other control
(`0 0 0 2px #fff, 0 0 0 4px var(--accent)`).

Hover moves the fill one step down the layering scale to `--surface-hover`; the text
colour never moves toward the background (§7). Placeholder is `--muted`. Every input
carries a visible `.field__label` — a placeholder is not a label, it disappears on
first keystroke. Native `::-webkit-search-cancel-button` is reset: it is browser
chrome, not this system's.

### 5.8 Select

The same filled field as §5.7 at the compact 40px height. The caret uses the
border-triangle technique, not a `background-image` data URI — it inherits `--fg`
and needs no second asset. It is drawn by a **pseudo-element**, not a `<span>`:
a purely decorative node standing next to a translatable label is something the
language swap can write into by mistake, and that is exactly what happened to the
sort caret (§5.12). Decoration that carries no content gets no element.

### 5.9 Pagination

Square 40px ghost buttons in two groups — **‹‹ first, ‹ prev** | status | **› next,
›› last** — with the page status between them. Rules:

- **The ends are disabled at the boundaries, never hidden.** Disabled is the only
  state allowed to drop contrast (§7), and it is the right one here: hiding a button
  reflows the row, so Next moves out from under the cursor mid-click.
- **The status uses `tabular-nums`.** *"Страница 9 от 10"* → *"10 от 10"* must not
  change width, for the same reason a PM reading must not (§3).
- **No numbered page links.** A reader of a 28-row table navigates by adjacency, not
  by jumping to page 7; numbered links would be seven controls doing the work of two.
- **Rows-per-page and page navigation share one bar under the table**, select on the
  left, page controls on the right. Both answer the same question — *how much of this
  am I seeing?* — so the reader should never have to look in two places. (This
  supersedes an earlier rule that put the select with the filters above the table.
  Grouping by what a control *answers* beat grouping by what it technically *is*.)
- **Only the page controls hide at one page. The select never does.** Since the
  default is Всички, one page is the normal state — hiding the whole bar would hide
  the control that creates pages, and the reader would have no way back. A single
  disabled page control is noise; a missing page-size control is a dead end.

### 5.10 Combobox

A text input that owns a listbox — ARIA 1.2, not a `<div>` with a click handler.

- **Focus stays in the input.** The active option is pointed at with
  `aria-activedescendant`, so a screen reader announces it without the caret
  leaving the field. This is why options are `<li role="option">`, not buttons.
- **The list floats, so it gets the shadow** — one of the two sanctioned cases
  (§4.4), the chart hover readout being the other.
- **Inline completion only runs while the value is growing.** Completing during a
  backspace re-inserts the character just deleted and the field becomes impossible
  to clear. Only a genuine *prefix* match completes; completing a mid-string match
  would rewrite what the reader typed into something else.
- **The completed remainder is selected**, so the next keystroke replaces it.
- **Hover and keyboard-active share one background**, plus a 2px `--accent` inset
  rule on the keyboard cursor. Two competing highlights make the reader work out
  which one Enter will take.
- **Escape is two-stage:** first closes the list, then clears the field. Escape
  should not destroy a query the reader may still want.
- **Options listen on `mousedown`.** `click` fires after the input's `blur`, which
  has already closed the list.
- The matched substring is weight 600. Weight 700 does not exist here (§3).
- **An option is the name and nothing else.** An earlier rule put the reading beside
  each name, on the argument that an option hiding whether it has data makes the
  reader pick blind. That over-read the control's job: this list finds a *row*, and
  the reading is in the table one line below it. Carrying the value made the dropdown
  a second, narrower table — the reader compared numbers in a floating list instead of
  in the column built for comparing them, and every option grew a right-hand element
  the matching-substring highlight had to compete with.
  The reader is not blind either way: picking a name filters the table, and the row
  states its own reading — or *"Няма скорошни данни"* — immediately. Deferred by one
  step is not hidden.

### 5.11 Column menu

A disclosure button owning a panel of checkboxes, one per optional column. Focus
**does** move into this panel — unlike the combobox (§5.10), these are real controls
the reader toggles several of, not one value they pick. Escape closes and returns
focus to the button; a click outside closes without stealing it.

Three rules, all about not stranding the reader:

1. **The oblast name is never hideable.** It is the row's identity, and a table of
   anonymous numbers is not a table. It has no checkbox at all rather than a disabled
   one — it is not a choice being denied, it is not a choice. Its absence from the
   list is the whole statement; the panel does not also say so in prose.
2. **The last visible data column cannot be switched off.** Its checkbox goes
   `disabled`, which states the limit; a silently ignored click does not. Disabled is
   the only state allowed to lose contrast (§7) and this is the right use of it.
3. **Hiding the sorted column re-sorts.** An order justified by a column the reader
   can no longer see is an unexplained order. Sort falls back to the first visible
   column, descending.

**Choice hides columns; it never forces one back.** The responsive rules (§6) are a
floor: a column the viewport has dropped stays dropped even when ticked. Anything else
reintroduces the horizontal scroll the breakpoints exist to prevent. This is a rule for
whoever builds the table, not a caption for the reader: on a narrow screen the column
is simply not there, which needs no explaining.

**The panel is checkboxes and nothing else.** No footnote. Both facts a note would
carry — that the name cannot be hidden, that a phone drops columns anyway — are
already visible in the control or on the screen. A menu that has to explain itself is
describing a design problem rather than solving one (§5.4 makes the same call about
page-size labels).

**Structural cells follow the columns.** The "no data" cell spans the PM columns, so
its `colspan` is however many are visible — not a constant. A hard-coded `colspan`
silently mis-spans the moment a column is hidden.

The choice persists in `localStorage`, and a stored state with nothing left in it is
discarded on load, so a stale value cannot present an empty table.

### 5.12 Language picker

A disclosure on the masthead: current language on the face, alternatives on demand.
Two links can show which languages exist but not which one is active without the
reader comparing them; a picker states it.

- **Every option carries a mark in a fixed 20×12 slot**, so the list stays aligned
  whether the mark is a flag or a code.
- **A flag must be the real flag, fetched — never drawn from memory.** Both marks are
  authentic assets at 3∶5, so they fill the slot identically: the Bulgarian tricolour
  with its official bands and colours, and the Union Flag from Wikimedia Commons
  (public domain). The earlier draft gave English a bare language code, on the
  reasoning that a flag names a nation and English has no single one. That reasoning
  is sound in general and was the wrong call here: it was really a *sourcing* problem
  dressed as a principle — an eyeballed Union Jack gets the counterchange offset wrong,
  so the honest fix is to fetch the real one, not to substitute a code for it.
  The rule that survives: **acquire the real asset, or use the code — never draw a
  look-alike.**
- The current option is marked with `aria-current` **and** `--surface-sel` plus weight
  600 — stated, not implied by position.
- Options are 48px, the full touch target (§6), because this list is reachable on a
  phone where the masthead is the only chrome.
- The list floats, so it takes the sanctioned shadow (§4.4). Escape closes and returns
  focus to the button; arrow keys walk the options.
- **Choosing an option must change something, and the button must say so.** Picking a
  language moves `aria-current`, rewrites the button's own mark and code, sets
  `document.documentElement.lang`, closes the list and returns focus. The first build
  shipped bare `<a href="#">` options with no handler at all: the list opened, a click
  jumped to the fragment, and nothing moved — a picker that could not be picked from.
- **In the app each option is a real link to the translated route** and the server
  renders that language. The kit has no server, so it carries a string catalogue
  (`i18n.js`): every user-visible string has a `data-i18n` key, and picking a language
  swaps all of them. §9.6 forbade English fallthrough; a key that is missing from a
  catalogue is a visible gap rather than a Bulgarian word leaking into an English page.
- **A string the script BUILDS cannot be reached by `data-i18n`.** The pager
  status, the count line and the combobox options are created after the swap has
  already walked the DOM, so they resolve through `AIRBG_T(key, vars)` at render
  time instead. Picking a language dispatches `airbg:languagechange`, and every
  component that composes its own copy re-renders on it. This is the seam between
  independent scripts: without it the masthead translates what exists and the
  table quietly rebuilds it in Bulgarian a moment later.
- **`i18n.js` loads before anything that reads it.** The script order on the
  table screen was `oblast-table.js → i18n.js`, so every lookup on the first
  paint fell through to its own key and the rows-per-page select literally read
  *runtime.allOption*. Returning the key is the right fallback for a missing
  *key* and the wrong one for a missing *catalogue*, so the component now says
  so in the console rather than rendering identifiers at the reader.
- **A `data-i18n` never nests inside another.** The ancestor already owns that
  string; tagging a child too writes it twice — *SensorsSensors*. Where a
  sentence is broken across inline children, each Cyrillic fragment gets its own
  span and its own key, and the parent gets none.
- **Coverage is measured, not asserted.** A count of `data-i18n` attributes says
  nothing about what is missing. The check that matters walks the rendered DOM
  after the swap and looks for Cyrillic text nodes; run against all five screens
  it found 64 untranslated strings that an attribute count had reported as
  complete.
- **An oblast name is data even in a heading.** The area title and the sample
  rows on the states screen carry `data-oblast`, not a catalogue key, so they
  resolve through the API's `name_en` like every other oblast name.
- **Re-applying the current language must change nothing.** The leak check only
  looks for source-language text after switching *away*, so it is blind to a
  swap that corrupts a string in the language it is already in. The test that
  catches it is idempotence: apply Bulgarian to a Bulgarian page and compare.
  Run twenty times it must be a no-op — that is what proves the swap is a
  replacement and not an append.
- **One string, one owner — and the area intro was breaking it in production.**
  `area.intro` carried a `data-i18n` key on the markup *and* a count the data
  script composes, and the catalogue's copy of the sentence had the number
  **frozen into it** — `"111 сензора в областта."`, no placeholder. So every
  language pass overwrote the real figure with Пловдив's, and a Смолян page
  read *111 сензора* beside a readout of *11*: one screen contradicting itself,
  the §9.3 defect. The catalogue string now takes `{n}`, the markup carries
  `data-od-id="area-intro"` and no key, and the script owns it alone.
- **One string, one owner.** The count line carried both a `data-i18n` key and a
  value the table script computes from the current filter, so the two fought and
  the markup's generic sentence won on load. Whatever composes a string at
  runtime owns it; the markup does not also tag it.
- **A translatable label lives in its own element, not as loose text.** The
  header button holds `<span data-i18n="col.pm25">` and no bare text node of its
  own. That is not decoration: it makes the markup correct under *any* writing
  strategy — the current text-node search and the superseded `lastChild` one
  both land on the same span. A page can be served fresh while a browser still
  runs a cached script, so markup that only works with the newest algorithm is
  markup that breaks on someone else's cache. Verified by running the old buggy
  algorithm against the current markup: no duplication.
- **A decoration that sits beside a label is not a node.** The sort caret was a
  real `<span>` — a text-node sibling of the header label, and therefore
  something a swap can mistake for the label. It is now `.th-sort::after`, the
  same border-triangle drawn by a pseudo-element. A node that only paints cannot
  be corrupted if it does not exist.
- **The swap targets the element's text node, not a position in it.** An
  earlier build wrote to `lastChild`, which is not the label — it is a guess
  about child order. A legend chip is `<swatch><text>`, so the guess held; a
  column header is `<text><caret>`, so it did not. The English went into the
  caret span while the Bulgarian text node stayed, printing *ФПЧ2.5, µg/m³PM2.5,
  µg/m³* — both languages at once — and stuffing copy into a span that is a CSS
  border triangle. Find the node by what it is, never by where it sits.
- **A name is an identity or a label, never both.** `data-name="Смолян"` is the
  row's key — stable, Bulgarian, the way into the catalogue. The label is
  resolved from it per language. Reading the key as a label left the dropdown in
  Cyrillic on an English page, and it broke *search*: the filter compared what
  the reader typed against a name they could no longer see, so "Sofia" matched
  nothing. Sorting has the same dependency — the collator follows the displayed
  language, because Latin names do not fall where Cyrillic ones did.
- **A translated UI still names each language in its own language.** *Български*
  stays Cyrillic inside the English catalogue. It is the one string a
  Cyrillic-leak check must not flag, because a reader who cannot read the current
  UI language finds their own by recognising it.
- **Translated names come from the data, not from transliteration.** Oblast names use
  the API's own `name_en`, and the air-quality bands use its `label` — *Софийска* is
  *Sofia*, not *Sofiyska*. Guessing a proper noun's spelling is the same class of error
  as drawing a flag from memory.

---

## 6. Responsive

| Breakpoint | Behaviour |
|---|---|
| < 480px | Table drops PM10 and the active count. Oblast + PM2.5 is the irreducible pair; below that it stops being a table. |
| < 672px | Single column. `--frame` collapses to full width minus 16px gutter. Table drops **Регистрирани** — the network's shape matters less on a phone than the reading does. Footer stacks. |
| 672–1056px | 24px gutter. Table full. |
| > 1056px | `--measure` and `--frame` both centred and capped. |

Touch targets are 48px; 40px is the floor for a compact control.

---

## 7. Do / Don't

**Do**
- Keep the brand palette and the air-quality ramp strictly apart.
- Square corners everywhere except the legend tag.
- Layer backgrounds for depth; reserve shadow for things that float.
- Use tabular numerals for every measured value.
- Set copy in Bulgarian first and check the fit there.

**Don't**
- Don't add a second accent hue. One blue.
- Don't use green or yellow for UI state — those hues belong to the air.
- Don't colour an empty state. Absence of data is not an error.
- Don't use weight 700.
- Don't add tracking above 16px.
- Don't lock the map's height and width at once, or set a height that contradicts the 926∶382 fit.
- Don't put a value on screen without saying what tier it aggregates.

---

## 8. Motion

Motion here is confirmation, not personality. A public data site that animates its
own numbers looks like it is performing rather than reporting.

| Token | Value | Used for |
|---|---|---|
| `--dur-fast` | 70ms | Focus ring appearing, chip press |
| `--dur-base` | 110ms | Button/row/nav hover background |
| `--dur-slow` | 240ms | Dropdown or hover readout entering |
| `--ease-standard` | `cubic-bezier(0.2, 0, 0.38, 0.9)` | Everything that enters or changes state |
| `--ease-exit` | `cubic-bezier(0.2, 0, 1, 0.9)` | Everything that leaves |

Rules:

- **Only `background-color`, `border-color`, `box-shadow`, `opacity`, and `transform`
  animate.** Never `width`, `height`, `top`, or `left` — they reflow the map frame,
  and the country fit has ~6px of slack (§1).
- **No transition on a measured value.** A PM figure updates instantly. Counting or
  fading a number implies the reading is still settling; it is not.
- **No entrance animation on page load.** Content that fades in delays the one thing
  the reader came for.
- **No map-dot pulsing, no scroll-triggered reveal, no parallax, no skeleton
  shimmer.** A loading state is a `--muted` line of text saying what is loading.
- Focus rings appear with no transition on the ring itself; they must be instant to
  be trustworthy for keyboard users.
- `@media (prefers-reduced-motion: reduce)` sets every duration to `0.01ms` and
  disables `transform`. This is required, not optional.

---

## 9. Voice

Bulgarian first, plain, and specific. The site's credibility is that it says exactly
what it knows and exactly what it does not.

**Register.** Neutral institutional. Not chatty, not alarmed, not reassuring. No
exclamation marks. No second-person coaxing (*"Виж какъв е въздухът при теб!"*).

**Rules that came out of real defects:**

1. **Name the tier with the number.** Never print a value without saying what it
   aggregates — oblast average, city aggregate, or single sensor (§5.2). *"Средно за
   областта"*, not a bare figure.
2. **Absence is stated, not apologised for.** *"Няма скорошни данни"* — present tense,
   no colour, no icon, no "за съжаление". Nothing failed (§2.3).
3. **Two honest numbers that appear to disagree get reconciled in copy,** not hidden.
   "585 сензора" beside ~25 map dots needs the legend line, not a smaller font.
4. **Units always.** `µg/m³` follows every measured value, spaced.
5. **Timestamps are absolute and local.** *"14:20, 31 август"* — not "преди 3 минути",
   which goes stale in a cached page.
6. **No English fallthrough.** uPlot's default `Time` axis label is a defect (§5.3).
   Every user-visible string goes through the catalogue.
7. **Disclaimer keeps body size.** It is the one footer line that must be read (§5.5).

**Word choices:** *област* not *регион*; *сензор* not *станция* (a station implies
official calibration the network does not have); *ФПЧ2.5 / PM2.5* — both, PM in
parentheses on first use per page.

**In English an *област* is a province.** Not "oblast": transliterating the
Bulgarian word leaves an English reader with a term they must look up, on a page
whose whole job is to be understood quickly. *Province* is the ordinary English
word for the administrative unit and needs no gloss. The plural is *provinces*,
never *oblasti*, and the article moves with the noun — *a province*, not
*an oblast*.

This applies to prose, not to identifiers: catalogue keys (`col.oblast`),
`data-oblast`, and the filenames `oblast-table.html` / `oblast-table.js` are
code, and renaming them would churn every reference to buy nothing a reader
sees. The rule is about what appears on screen.

---

## 10. Anti-patterns

Each of these was observed in the source project or is a failure mode the constraints
in §1 make likely. Committing one is a defect, not a preference.

**Colour**

- ❌ Green as a UI accent (the retired `#0b6`). Green is a ramp hue and reads as
  "clean air" on a page full of air data.
- ❌ `--warn` orange on *"Недостатъчно данни за този район"* — eight non-failures
  rendered as eight failures.
- ❌ Any second chromatic accent. One blue.
- ❌ Restating or hardcoding the air-quality ramp in CSS. It is served.

**Typography**

- ❌ Weight 700 anywhere.
- ❌ Letter-spacing above 16px.
- ❌ Proportional figures on a measured value — the number twitches as it updates.
- ❌ Checking a headline's fit in English and shipping it in Bulgarian.
- ❌ A webfont CDN. Self-host or fall back.

**Layout**

- ❌ One 60rem column for everything, which squeezes the map into a prose measure.
- ❌ Rounded corners outside the legend pill — including the `rx="7"` favicon.
- ❌ Card shadows. Depth is `#ffffff → #f4f4f4 → #e0e0e0`.
- ❌ A fixed map height inside a fluid container. The fit is a ratio; a pixel height
  holds it at exactly one viewport width and crops the country at every other.
- ❌ Treating the map as one card among several. It is the primary surface — a
  public air-quality site whose map is a 24rem tile buried under a hero paragraph
  has buried the one thing the reader came for.
- ❌ 28 rows of name-plus-count presented as a directory instead of a sortable table.
- ❌ No-data rows sorted among the values, so the reader scrolls past eight blanks —
  including when the reader sorts ascending. The sink is an invariant, not a default.
- ❌ A truncated oblast list. All 28 ship, or the reader cannot tell "no sensors"
  from "cut off".
- ❌ A default filter state in the script that the markup's own attributes do
  not match. The first paint is then a disagreement no click caused.
- ❌ One count sentence reused across filters, so it states the wrong reason for
  the rows the current filter hides.
- ❌ A default filter whose count line does not account for what it hides. Hiding
  rows is legitimate when the control is visible, the omission is counted with its
  reason, and one click brings them back; drop any of those and it is truncation
  wearing a radio button.
- ❌ A count line whose denominator is the filtered set. It is always *of 28*.
- ❌ 28 sortable rows with no search box.
- ❌ Prose capped to a reading measure inside a data frame, so a long sentence
  sits in one corner while the rest of the width stays empty. Pick the container
  the content belongs to; do not cap inside the wide one.
- ❌ A table that shows one PM metric when the system defines two, so a metric the
  design system supports is unreachable on that surface.
- ❌ A PM10 figure below the PM2.5 figure for the same oblast — the coarse fraction
  contains the fine one.
- ❌ The same oblast reading differently on the table and the area page.
- ❌ A bare "Сензори" column. Say active or registered; the two differ by 55.
- ❌ An active count above the registered count for the same oblast.
- ❌ Per-oblast sensor counts that do not sum to the 585 / 640 the home page prints.
- ❌ Blanking a silent oblast's sensor columns. It has hardware; saying `0 / 3`
  explains the silence instead of deepening it.
- ❌ Paging applied before sorting, which lets a no-data row outrank a measured one
  on a later page.
- ❌ Page controls rendered with a single page of results, or end buttons that vanish
  instead of disabling and so reflow the row under the cursor.
- ❌ Hiding the rows-per-page control along with the page controls. At one page it is
  the only way to create more, and one page is the default state.
- ❌ A stock 10/25/50 page-size list pasted onto a table whose row count makes half of
  it meaningless. Use the divisors of the actual count.
- ❌ A page size that leaves a stub final page of two or three rows.
- ❌ Explanatory suffixes on page-size options. If the set needs prose to be
  understood, the numbers are wrong.
- ❌ A footnote inside a menu restating what its controls already show. If the panel
  needs prose, the controls are wrong.
- ❌ Spelling the "show everything" option as the literal row count. It is a claim
  about the data that goes stale the moment a filter runs, and it makes the option
  test as redundant against its own total.
- ❌ A redundancy rule that can disable the "show everything" option. It is the way
  back; it stays enabled in every state.
- ❌ `Infinity` as a page-size sentinel. `0 × Infinity` is `NaN` and every slice
  built on it silently returns nothing. Use the row count.
- ❌ Disabling page-size options, or rewriting the list mid-interaction. Rebuilding
  the set when the reader changes a filter is correct; changing it under their cursor
  during one interaction is not.
- ❌ Offering a page size larger than the visible set. It duplicates *Всички* and
  tells the reader nothing.
- ❌ Calling a number a divisor without checking. 21 against 28 rows leaves a 7-row
  stub — the exact defect the divisor rule exists to prevent.
- ❌ Keeping the reader on page 3 after a search changes the result set.
- ❌ A count line that reports only the current page. It always names the range and
  the total.
- ❌ **Two elements sharing one `data-od-id`.** `querySelector` returns the ancestor,
  the script throws on its first property access, and every control on the page dies
  silently. Scope the selector to the element type and fail loudly when it misses.
- ❌ A `<style>` block kept for "standalone review". Under `style-src 'self'`
  with no nonce the browser refuses it and says so in the console, so the page
  is styled everywhere except the one place readers see it. Put the rules in a
  served stylesheet; the review harness gets them too.
- ❌ A detail gate keyed on a zoom number when the page's framing already
  states the answer. Every province opens below the number, so the layer it
  guards is hidden at load and appears only if the reader happens to zoom.
- ❌ Mounting a second camera without telling it what the page is about. The
  framing the renderer computed is discarded, every line of it still correct,
  and a detail page opens on the whole country.
- ❌ Asking for a permission when moving to the side that already has it would
  do. An allowlist entry for a preview origin was drafted and unnecessary: the
  kit moved onto the allowed origin instead, and nothing about who may read the
  tiles changed.
- ❌ A component that sets `display` without a `[hidden]` reset above it. A class
  rule beats the UA's `[hidden] { display: none }`, so JS that hides an element
  silently does nothing. `[hidden] { display: none !important }` is in the reset.

**Components and interaction**

- ❌ Replacing the metric-switcher or table-filter `fieldset`/`legend` with divs.
- ❌ A radio group for states that can legitimately be held at once. The reader
  then flips between pictures and compares from memory.
- ❌ A second series in a second colour on a one-accent palette. Dash it; the
  pattern also survives greyscale and colour blindness.
- ❌ Two filled areas stacked on one panel. The overlap reads as a value that
  does not exist.
- ❌ Twin y axes for two series of the same unit. It makes unequal things look
  equal.
- ❌ A key drawn beside a single series whose heading already names it.
- ❌ A multi-select that can reach zero selected. Disable the last one.
- ❌ A sortable header that is a `<div>` with a click handler, or one that paints a
  caret without setting `aria-sort` on the `<th>`.
- ❌ A placeholder used as the only label on an input.
- ❌ An autocomplete that inline-completes on backspace, so the field cannot be
  cleared.
- ❌ Inline-completing a mid-string match, which rewrites what the reader typed.
- ❌ A combobox that moves focus into the list instead of using
  `aria-activedescendant`, or whose options are `<div>`s with click handlers.
- ❌ Options bound to `click` rather than `mousedown`, so `blur` closes the list
  before the selection lands.
- ❌ Two different highlight styles for hover and keyboard-active in one list.
- ❌ Repeating a row's data inside the control that finds the row. A search list
  returns names; the table beside it is where values are compared.
- ❌ An icon-only control with no accessible name, or with a name hardcoded in
  markup so it never translates. The glyph is the affordance; the name is still
  required.
- ❌ A JS-toggled class duplicating a state an ARIA attribute already carries.
  Style off the attribute; two records of one state drift.
- ❌ Narrating what a control just did when the reader could simply be taken
  there. A status line plus a link to the destination is two steps where one
  was asked for.
- ❌ A text field that navigates before the query is unambiguous.
- ❌ Leaving a rule or a catalogue string behind when its markup is deleted.
- ❌ A control that duplicates a masthead destination while the surface it sits
  on needs a different question answered.
- ❌ Writing an input's value without clearing the query derived from it. The
  field and the filter then disagree, and the list contradicts the box.
- ❌ Relying on visual gaps inside a live region. `role="status"` announces its
  parts as one string; separators are content.
- ❌ A picker whose options carry no handler — `href="#"` with nothing bound. The
  list opens, the click goes nowhere, and the control looks broken because it is.
- ❌ A column toggle that can hide the row's identity column, or that lets the reader
  switch off every data column and face an empty table.
- ❌ A column toggle that silently ignores the click on the last remaining column
  instead of disabling it.
- ❌ Leaving a table sorted by a column that is no longer visible.
- ❌ A hard-coded `colspan` on a row that spans toggleable columns.
- ❌ A column preference that overrides a responsive breakpoint and brings back the
  horizontal scroll.
- ❌ A plotted series with no value axis. The reader can see the shape and
  price nothing on it.
- ❌ Gridlines on arbitrary numbers. A line at 0,87 explains nothing; the step
  is 1 / 2 / 2.5 / 5 × 10ⁿ or the grid is decoration.
- ❌ An axis whose top is the data's peak, so a gridline crosses the highest
  point — or a ceiling so far above it that a third of the panel is empty.
  Round *and* tight, not round at any cost.
- ❌ A non-zero baseline on a concentration measured against health thresholds.
  The distance from zero is the information; zooming it turns a quiet day into
  a spike.
- ❌ A rule at every tick on both axes. Thirteen lines under one series is a
  hatch pattern; the verticals go, the hours keep a tick.
- ❌ Axis tick elements hard-coded in markup. Their count depends on the window
  and the peak, which markup cannot know.
- ❌ Two formatters for one number — a locale-aware axis beside a `toFixed`
  aria-label. The screen and the screen reader then disagree.
- ❌ One component stating the same fact twice — a metric named in the heading
  and again in the axis caption 30px below it. The reader stops to compare two
  labels that never differ.
- ❌ Deleting a duplicated label wholesale when only part of it repeats. The
  unit beside it was the one thing nothing else said.
- ❌ A chart whose title names a period but not a metric, over an axis labelled
  only with a unit two metrics share. The reader cannot tell what the line is.
- ❌ A range printed in clock time alone when it crosses midnight. 14:20–14:20
  is a day, and it reads as nothing.
- ❌ An option label that does not complete its own legend. Under *Период*, the
  choices are periods; an adjective missing its noun or a bare imperative is
  neither.
- ❌ A fixed time window on a time series the reader has questions about. If the
  data exists for 42 hours, the window is a control.
- ❌ A legend whose swatches are empty because the ramp binding is missing on
  that screen. Uncoloured colour chips read as a rendering failure.
- ❌ Legend bands that are not the served bands. The thresholds are data; typing
  plausible ones is inventing the scale.
- ❌ Blending a banded scale into a smooth gradient. It shows colours the scale
  does not define.
- ❌ A ramp legend as a wrapping row of chips. A scale's order is its content;
  a paragraph of boxes makes the reader reconstruct it.
- ❌ Numbers printed twice on one scale — per-band ranges and a tick row. Two
  statements of one fact, free to disagree.
- ❌ A viewport allowance that covers the map but not the legend. A key below
  the fold is a key the reader does not have while looking at the colours.
- ❌ Trimming a ratio-fitted map's height to make room. Narrow it instead —
  height is the axis that crops.
- ❌ A placeholder kept past the point where real data exists for it. It cannot
  be checked, so it hides every question the real thing would raise.
- ❌ A preview drawn on a different projection or ratio from the shipping map.
  It validates a layout nobody sees.
- ❌ A hand-drawn country outline. Fetch the real boundary and localise it.
- ❌ A point mark for a value measured across a territory. The area is the mark.
- ❌ A switcher rendered as markup with no handler bound. The state moves,
  the surfaces do not, and the control looks broken because it is.
- ❌ Recolouring a map for a new metric without moving its legend. Identical
  band colours across two scales make the stale key look right.
- ❌ Capturing one scale from an endpoint that serves several, then treating
  the missing ones as evidence the feature cannot exist.
- ❌ A control whose parameter the API ignores. Probe it first; a selector that
  cannot change the answer is worse than the missing feature it imitates.
- ❌ Text on a served colour with a fixed ink. The band is data; the contrast
  has to be computed from it.
- ❌ Forcing a label into a shape too small to hold it.
- ❌ A country drawn on a blank field. Without its surroundings the reader
  cannot tell a coast from a land border, and the map reads as a cut-out.
- ❌ Shrinking the subject inside its frame to make room for context. The fit
  is measured; the context runs off the edges instead.
- ❌ Leaving water unpainted, so sea and foreign land are the same grey — or
  painting it a grey that also matches a province with no reading. Three
  different things at one lightness is not a palette, it is a collision.
- ❌ Placing a horizontal label by its distance to the nearest edge. That
  metric is blind to the one axis the word occupies, and it clips names inside
  tall narrow shapes.
- ❌ Naming every country a projection happens to clip. Name the ones the
  subject actually borders; the rest are there to fill the frame.
- ❌ Leaving a factual name off the map because one source lacks it. That is a
  sourcing problem — fetch it from a source that has it (Wikidata by QID), and
  still never transliterate.
- ❌ Context drawn at the same strength as an absent reading. One means another
  country, the other means no data, and they must not look alike.
- ❌ A map label inside its own shape's group, so the size-ordered paint of
  the shapes decides which readings survive. Labels are their own layer.
- ❌ Skipping the label on a shape with no value. It becomes an unexplained
  grey area, indistinguishable from a rendering failure — state the absence
  where the reading would have been.
- ❌ Applying an empty-state colour without checking it against the surface it
  actually lands on. `--muted` passes on `--surface` and fails on `--surface-2`
  at caption size.
- ❌ A choropleth whose areas carry a number and no name. The colour says how
  bad, the number says how much; only the name says where.
- ❌ A `fill` (or any presentation attribute) on an element whose class the
  stylesheet also paints. The stylesheet wins, the attribute reads as though it
  did nothing, and every marker shows one colour for every reading.
- ❌ A basemap label placed before the data it orients. The name lands on the
  reading, and the layer that was added for context obscures the content.
- ❌ A label rule so strict that a whole layer disappears. One collision avoided
  is not worth twenty-three names dropped; let the label move inside its shape.
- ❌ Naming a shape that a marker beside it already names. The reader sees the
  same word twice and reads it as a fault, not as two real places.
- ❌ A detail gate tuned on a large subject and shipped on a small one. Set it
  from the page where the layer matters most, or it is invisible exactly there.
- ❌ Recording "no data" on the strength of a rate-limit response. A 429 is the
  server declining to answer, not an answer.
- ❌ A pointer gesture with its own copy of the zoom step. It drifts from the
  buttons, and one press then means two different distances.
- ❌ A double-click gesture on top of a link. The click lands first, so the
  reader navigates away from the thing they were trying to look at.
- ❌ A detail gate set above what the zoom ceiling can reach. The layer then
  never appears, and the reader cannot tell that from a broken one.
- ❌ Shipping a basemap tier without the tag that makes it legible. Geometry
  with no `name` is a street network you cannot navigate by.
- ❌ Eagerly loading city-scale geometry on a country map. The reader who never
  zooms pays for detail they never see.
- ❌ Treating an HTTP 200 as evidence of data. A mirror serving a different
  region answers 200 with zero elements, and an unguarded capture writes that
  down as "this place has none".
- ❌ A long-running capture that only writes at the end. A timeout then costs
  every request it already paid for.
- ❌ Placing a curved feature's label as though it were straight. Segment and
  run tests both fail on ordinary streets; put the type on the path.
- ❌ Labelling an OSM way rather than the street. One street is many ways, and
  none of the fragments is long enough to carry the name.
- ❌ Designing around a constraint without checking that it applies. "A tile
  server is a third-party origin" was true of tile servers in general and false
  of this project's own listener, and a whole basemap was hand-captured on the
  strength of it.
- ❌ Reading a style to find out what a basemap contains. The style is the
  selection; the schema is the inventory. `poi` shipped in the archive from the
  first build and was declared missing because it was absent from `style.json`.
- ❌ Commissioning a capture or a rebuild to supply data the backend already
  serves, without first checking the schema it was built to.
- ❌ A basemap layer whose `text-font` names a stack the glyph endpoint does not
  serve. It renders nothing, with no error — the blank map, through a typo.
- ❌ A layer control holding its own list of layer ids. The style is the source;
  a copy of it in the control drifts the first time a layer is added.
- ❌ Offering a layer control while the fallback basemap is drawing. There are no
  tile layers to switch, so every checkbox is inert.
- ❌ A POI drawn in a ramp hue, or in the accent. It is neither a reading nor a
  state; the basemap's neutrals are what it gets.
- ❌ Shortening a category list so its panel fits the viewport. Cap and scroll —
  a dropped category is a layer with no way to reach it.
- ❌ Treating a first-party service on a second hostname as an external one.
  Read the CSP the app already ships; it names what is allowed.
- ❌ Duplicating geometry the backend already serves. Two owners for one
  basemap, and the hand-built copy is the thinner of the two.
- ❌ A basemap swap with no fallback. Libraries, WebGL and a cross-origin
  style can each fail independently; every one of those routes must end in a
  drawn map, or the reader gets the blank the app's own docs warn about.
- ❌ A `catch` that logs `e.message` alone. A DOMException and a
  ReferenceError from a nested scope both arrive with nothing useful in it.
- ❌ Letting the overlay eat the gestures that move the basemap underneath.
  Transparent to the pointer except on the marks that are controls.
- ❌ Allowing rotation or pitch under a separable lon/lat projection. The data
  layer shears off the basemap and nothing in the code says why.
- ❌ Building a drag handle in script when the platform has `resize`. It costs a
  height the CSP will not let you write, plus a cursor, a hit area and a
  pointer state the browser already implements.
- ❌ Letting a resizable box get wider than the fit. Width is the axis that
  crops; clamp the drawn window and letterbox instead.
- ❌ A resize observer with no change threshold and no frame batching. The
  repaint it triggers re-fires it, and a single drag becomes dozens of passes.
- ❌ Keeping a size token on a child of the element the reader can drag. The
  explicit height lands on the parent and the token never loses, so the two
  disagree about the same shape.
- ❌ Clamping a relative zoom at 1 when the framing it is relative to is not the
  country. The first step out then skips every intermediate scale.
- ❌ One dot size and one dot colour for values that differ. The mark then says
  a place exists and nothing about what was measured there.
- ❌ Abandoning a probe on a 400. A 404 is a route that is not there; a 400 is a
  route that is, answering that the request was shaped wrong — and its message
  usually names the parameters.
- ❌ A panel reachable only through a path that currently produces nothing. It
  is a dead control even when every line of it is correct.
- ❌ Setting a presentation attribute that a class rule already governs. The
  stylesheet wins and the attribute reads as though it did nothing.
- ❌ A runtime tile server, or any third-party origin, to get basemap detail.
  §1 forbids it and the CSP enforces it; localise the geometry as every other
  real asset here already is.
- ❌ Dropping a served fill's opacity so a basemap shows through. The ramp is
  data — a translucent choropleth is a re-tinted one. Put the basemap on top
  with a casing instead.
- ❌ Gating detail on a zoom ratio rather than on ground scale. A small
  province's own fit clears a high ratio while still crushing a city into
  eighteen pixels.
- ❌ A single-class selector for an element that carries a tier class and a role
  class. It matches both roles and silently resizes the one you did not mean.
- ❌ Relying on the browser to navigate to the document it is already showing.
  Same-path links are a no-op, and the failure looks like a dead control rather
  than like a routing decision.
- ❌ Taking pointer capture on `pointerdown`. It retargets the click that
  follows, so every link under the capturing element goes quiet.
- ❌ An in-page state change with no history entry — and no state on the entry
  the reader arrived on. Back then does nothing, which is the browser's own
  control behaving like a dead one.
- ❌ A guard scoped wider than the thing it guards. A one-shot click suppressor
  on `document` eats the next click anywhere on the page, and the symptom
  surfaces in an unrelated control that looks broken on its own.
- ❌ Storing a pan in screen pixels. It means a different distance at every
  zoom, so the offset jumps the moment the scale changes.
- ❌ A clamp that is not written back to the state it clamps. The state keeps
  accumulating a value the drawing refuses, and the control goes dead for the
  first part of the gesture back.
- ❌ Zooming about the centre while the reader is pointing at a corner. The
  place they are looking at slides away under the gesture.
- ❌ A full-bleed map that swallows the wheel. The reader cannot scroll past it;
  leave the plain wheel to the page and say what zooms.
- ❌ A gesture with no keyboard equivalent. Drag and wheel are pointer-only; the
  same operations belong on the arrow keys and +/−.
- ❌ Widening what an API tier returns because a request implies finer marks.
  The tier ceiling is an anti-enumeration control (§1); draw the finest tier
  that exists and name it, rather than inventing the one below it.
- ❌ Fitting a subject into a container whose aspect belongs to a different
  subject. The scale comes out arithmetically right and the result still reads
  as unzoomed, because the constrained axis wastes the free one.
- ❌ Reading a CSS-owned value before the attribute it is keyed on is written.
  The first paint measures the old value and lands a frame behind.
- ❌ A zoom-out that clamps instead of widening the subject. The button stays
  enabled and does nothing, which is the dead control in its purest form.
- ❌ Two adjacent controls with near-identical glyphs. A DOM check sees two
  different buttons; a reader sees one button twice.
- ❌ A per-sensor figure on a page that has no per-sensor data — especially one
  frozen into markup, where it is identical and wrong on all 28 pages.
- ❌ A finder on a detail page that offers records from outside the record the
  page is about.
- ❌ A leader line on this map at all. The reading and its name are one label;
  if the pair does not fit, the number stands alone and the tooltip carries the
  name.
- ❌ One glyph-width constant on a map set in two scripts. Whichever script it
  was measured from, it is wrong for the other — and it fails in opposite
  directions: too narrow collides, too wide drops names that would have fitted.
  Measure per character.
- ❌ A collision test with no gap. Two boxes that merely fail to intersect print
  as one word, and every overlap check passes while the map reads
  *VelikoTargovishte*.
- ❌ Placing a label by the width of the string in the current language. The
  same province then moves, and resizes, when the UI language changes.
- ❌ A hand-maintained `?v=` cache-buster. It drifts — the same file ends up at
  three different versions on three pages, and an unbumped one serves a stale
  script that looks exactly like a fix that did not work.
- ❌ Verifying a bilingual map in one language only. The other language has
  different strings, different widths and therefore different placements.
- ❌ Reaching for a leader line as the second option. Shrink one step, then
  let the name leave its reading and search the shape on its own; a leader is
  what remains when both have failed.
- ❌ An uncapped leader. Past ~50–80px it stops pointing and starts crossing;
  a name that cannot be placed near its shape is a placement failure, not a
  longer line.
- ❌ Breaking a name anywhere but at a space or a hyphen to make it fit.
- ❌ Leaving a shape unnamed because its own area cannot hold the name. Move
  the name out and draw the leader; unnamed shapes make the reader hunt.
- ❌ A callout resting inside the subject, on a neighbour that it does not
  name. The leader line is what prevents misattribution — do not undo it by
  parking the label on the wrong territory.
- ❌ Moving the measured value off its own shape. The name can point from a
  distance; the reading is a property of the territory it sits on.
- ❌ Shrinking label type until the longest name fits. One province's fit is
  bought with every other province's legibility; drop the name instead.
- ❌ Testing a centred label's fit at its anchor point alone. The tail is what
  crosses the border.
- ❌ A shape that is clickable through a handler rather than being a link. It
  cannot be focused, middle-clicked, or previewed before the click.
- ❌ A link into a single detail page that does not say which record it is
  about. Every row and every shape then opens the same one, confidently.
- ❌ Reading a URL parameter into a page without checking it against the known
  set. An unknown value renders a heading for something that does not exist.
- ❌ Animating a hover lift with anything but transform and filter, or leaving
  the transform in place under `prefers-reduced-motion`.
- ❌ One loop deciding both paint order and label order. Who covers whom and who
  has room to move are different questions.
- ❌ Data polled on a timer on a page a reader is trying to read.
- ❌ Each component fetching for itself. They answer at different moments and
  the screens disagree about one province.
- ❌ A refresh control with no statement of how fresh the data already is.
- ❌ Presenting a bundled copy as though it were live, or an empty screen when
  the API cannot be reached. Show the copy and label it.
- ❌ Rebuilding rows on refresh, discarding the reader's sort, filter and page.
- ❌ Showing ODbL-licensed data without its attribution. The licence term is
  not a design preference.
- ❌ A map without a legend, or a legend that omits the tier line.
- ❌ A "zoom in" hint that only fires in a state the page never reaches.
- ❌ Hover that lightens text toward the background. Move the background instead.
- ❌ A focus style thinner than the Carbon double ring.
- ❌ Footer paragraphs at body weight, outweighing real content.
- ❌ Implementation detail in the footer — endpoints, licences, frameworks. The
  footer speaks to the reader; build facts belong in the design system.
- ❌ Translating only the DOM that exists at swap time. Script-built copy —
  pager status, count lines, listbox options — needs a runtime lookup and a
  re-render signal, or it reverts to the source language on the next render.
- ❌ A theme override in a different file from the palette it overrides. `:root`
  and `[data-theme="…"]` tie on specificity, so whichever file the page links
  last wins — and the picker appears broken while behaving correctly.
- ❌ A component that reads the string catalogue but is loaded before it. The
  page renders raw keys, and nothing throws.
- ❌ Leaving a translatable label as a bare text node among element children.
  Wrap it; loose text is what makes the element's shape part of the contract.
- ❌ A decorative `<span>` as a sibling of a translatable label. Draw it with a
  pseudo-element; an empty node beside copy is a target for text-writing code.
- ❌ A string with two owners — a `data-i18n` key on markup that a script also
  composes at runtime. They disagree, and load order decides the winner.
- ❌ A catalogue string with a sample number baked into it instead of a
  placeholder. It looks translated and silently overwrites the real value.
- ❌ Assuming a query string survives. Some hosts address files by path and
  drop it; a parameter-only design then reports the same record for every link.
- ❌ Reading `.href` on an SVG anchor as if it were a string.
- ❌ Testing a language swap only in the language you switched *to*. A swap that
  appends instead of replacing looks correct until you re-apply the source
  language to itself.
- ❌ Nested `data-i18n`. The string is written twice, once by the ancestor and
  once by the child.
- ❌ Claiming translation coverage from a count of tagged elements. Walk the
  rendered DOM in the target language and look for leftover source-language text.
- ❌ Addressing a translatable string by node position — `firstChild`,
  `lastChild`, `childNodes[0]`. It survives exactly as long as no one adds an
  icon or a caret beside the text, and it fails by *duplicating* the string
  rather than by throwing.
- ❌ Using a display label as a sort or search key. The reader searches what they
  can see; matching against a hidden language makes the field silently dead.
- ❌ Debug strings as placeholder copy — zoom levels, ratios, node counts. A
  deliberate gap labelled in developer shorthand is indistinguishable from a
  rendering failure.

**Generic gloss that does not belong here**

- ❌ Gradient washes, glassmorphism, emoji as icons, hero illustration, a second CTA
  for the same action, invented metrics, or placeholder copy.
