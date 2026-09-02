# ui_kits/app — airbg.org applied interface kit

Real screens from the source product, assembled only from this package's tokens and
components. `app.css` is an arrangement layer: it introduces no new colour, radius,
weight, or spacing value. If you find a literal here that is not a token, that is a
bug in this kit, not a licence to add one.

## Files

| File | Screen | What it demonstrates |
|---|---|---|
| `index.html` | Kit launcher | Entry point; links every screen |
| `map-home.html` | Country map | Masthead, metric switcher `fieldset`, full-bleed hero map (`.map--hero`, `max(fit, 78vh)`), legend with the tier line, readout strip |
| `oblast-table.html` | 28 oblasti | Table (not a directory): ФПЧ2.5 **and** ФПЧ10 columns, each independently sortable; active vs registered sensor counts; a Колони menu that toggles column visibility; combobox search with live filtering, inline completion and a selectable dropdown, data filter, sortable headers, one pager bar under the table — rows-per-page select left, first/prev/next/last right (defaults to Всички), tabular numerals, zebra, no-data rows last in every order |
| `area-detail.html` | One oblast (detail view — reached from the table or a map dot, not a nav tab) | Readouts that name their tier, ratio-sized zoom-9 map, filled 24h area chart |
| `states.html` | State reference | Empty vs. loading vs. real error, tier limit, selected row |
| `app.css` | Applied styles | The only file here with CSS; everything else is markup |

## Load order

```html
<link rel="stylesheet" href="../../tokens.css">           <!-- spacing, radius, containers, motion -->
<link rel="stylesheet" href="../../colors_and_type.css">  <!-- colour + type foundations -->
<link rel="stylesheet" href="../../components.css">       <!-- masthead, table, legend, buttons -->
<link rel="stylesheet" href="app.css">                    <!-- screen arrangement -->
```

## Two things this kit deliberately does not do

1. **It does not colour the air-quality ramp.** The legend chips carry an unset
   `--chip-ramp` slot with a dashed outline. Those values are served by
   `/api/v1/scales`; a design system that hardcodes them is restating data as brand.
   Set `--ramp-*` at runtime in the consuming app.
2. **It does not render a real map.** `.map-canvas` is a labelled stand-in inside the
   real frame. What this system owns is the frame, the 926∶382 ratio, and the legend —
   the Leaflet canvas belongs to the app. `926 / 382` is the measured country fit
   (~3.4% latitude margin, ~6px of slack at `data-zoom=7`) and acts as a **floor on
   height**: wider crops the country, taller never does. `.map--hero` takes
   `max(fit, --map-cap)`. Re-measure only if projection or zoom changes.

**The language picker translates the screens.** Every user-visible string carries a
`data-i18n` key resolved from `i18n.js` (59 keys per language); oblast names and
air-quality band labels come from the API's `name_en` and `label` fields rather than
hand transliteration. In the real app the server renders each language — the catalogue
exists because the kit is five static files.

All figures in the screens are sample values and are labelled as such in the page
copy. No metric here is presented as a live reading.

## Reusing this in a new project

Copy `tokens.css`, `colors_and_type.css`, `components.css`, and `assets/`, then start
from the screen closest to yours. `oblast-table.html` is the best starting point for
any ranked-region view; `states.html` is the reference for what is and is not an error.

## Structure

```
ui_kits/app/
  index.html            Launcher — links every screen
  map-home.html         Country map home
  oblast-table.html     All 28 oblasti — search / filter / sort
  oblast-table.js       Behaviour for that table (external: CSP has no inline allowance)
  area-detail.html      One oblast, with a 24h chart
  states.html           Empty / loading / error / tier-limit / selected
  app.css               The only stylesheet here; arrangement only
  components/           Copy-paste markup partials (below)
  README.md             This file
```

## Component files

`components/` holds the copy-paste markup partials the screens are assembled from.
They are HTML fragments, not modules: this product is a server-rendered Go app with no
build step and no framework (`DESIGN.md` §1, no new runtime dependency), so there is
nothing here to `import`. Paste a fragment into a page that already loads the four
stylesheets.

| File | Component | Spec |
|---|---|---|
| `components/app-shell.html` | **App shell** — masthead, page container, footer | §5.1, §5.5, §4.1 |
| `components/input-bar.html` | **InputBar** — control row: metric switcher `fieldset` plus at most one solid button | §5.6 |
| `components/sidebar.html` | **Sidebar** — the detail-page aside in the narrow `.split--2` column | §4.1, §4.4 |
| `components/readouts.html` | Readout strip — every value states its tier, tabular numerals | §3, §9 |
| `components/map-legend.html` | Map frame + legend with ramp slots and the tier line | §5.2 |
| `components/oblast-table.html` | Ranked-region table, no-data rows last | §5.4 |
| `components/chart.html` | Chart frame with a filled single series | §5.3 |

Naming note: this package's audit expects a chat-app component vocabulary
(App, Sidebar, Composer, PreviewCard). Only **App shell**, **InputBar** and
**Sidebar** map onto anything real in a public-data site; the rest are named for what
they actually are.

The CSS behind these partials lives once in `../../components.css` (masthead, card, `.data-frame`, `.map`,
`.chart`, `.legend`/`.chip`, `.btn`, `.switcher`, `.table`, `.footer`). Each screen
here is an assembly of those classes. `app.css` adds only screen-level arrangement —
`.page`, `.toolbar`, `.readouts`, `.split`, `.map-canvas`, the chart series fills, and
the kit index. Adding a component means editing `components.css` with a `DESIGN.md`
§-reference in the comment, not adding a local rule here.

## Usage workflow

1. Open `index.html` and pick the screen closest to what you are building.
2. Copy it. Replace content; do not add CSS.
3. If a component is genuinely missing, add it to `../../components.css` with its
   `DESIGN.md` §-reference, then use it here.
4. Regenerate the shipping bundle:
   `cat tokens.css colors_and_type.css components.css > examples/theme.css`
5. Check in Bulgarian at 390px, 672px and 1056px, then Tab through the screen and
   confirm the double focus ring never thins.

## Design notes

- **Two containers.** `.frame` (78rem) for map, table and chart; `.measure` (60rem)
  for `states.html` and the footer. Choosing wrong is the most common structural bug.
- **Every readout names its tier.** `52,1 µg/m³` is meaningless until the line under it
  says *средно за областта*. `area-detail.html` shows the pattern.
- **`states.html` is the reference for what is and is not an error.** No-data is
  `--muted` at body size; `--danger` appears only on a genuinely failed request.
- **The chart is filled,** never an outline: `.series-area` at 12% `--accent` plus a
  2px `.series-line`. One series, one colour.
- **No inline styles in the screens.** The consuming app's CSP forbids them. Only
  `index.html` carries a scoped `<style>`, for standalone review rendering.

## Source basis

Written from the airbg.org `DESIGN.md` specification (§5 components, §6 responsive),
preserved unmodified at `../../context/source-DESIGN.md`. The source project handed
over no HTML, so these screens are an implementation of the written contract rather
than a copy of a running page — see `../../context/provenance.md`. Copy, oblast names,
sensor counts and the 585-vs-25 legend reconciliation come from facts stated in that
specification; the numeric values are sample data and are labelled as such on screen.
