# airbg.org — design system

The interface language of [airbg.org](https://airbg.org), a Bulgarian-first public
air-quality site. Carbon-derived, monochrome plus one blue, square-cornered,
Cyrillic-first, and built for a server-rendered Go app with a strict CSP.

`DESIGN.md` is the contract. This file is the map.

## Product Overview

**airbg.org** is a Bulgarian-first public air-quality site. It shows PM readings from a
community sensor network on a country map, per-oblast aggregates, and time-series
charts. Its readers are the general public checking whether the air where they live is
bad right now — not analysts. The register is deliberately institutional: this is data
about public health, and it must not look like a marketing page.

The product provides one job done well: answer "is the air bad here, right now?"
without making the reader interpret a chart. It is built server-rendered, ships static
CSS, and includes no client framework.

**Primary surfaces:** the country map home, the 28-oblast table, an oblast/area detail
page with a 24-hour chart, and an about page. **Core capabilities:** live sensor
aggregation, zoom-tiered map dots (oblast → city → sensor), a metric switcher
(PM2.5 / PM10), and per-oblast history. **Environment:** server-rendered Go, static
CSS at `internal/web/static/theme.css`, strict CSP, no build step, no runtime
dependencies.

**Source references:** `context/source-DESIGN.md` and `context/source-tokens.css` are
the unmodified evidence this package was derived from; `context/provenance.md` records
the chain (IBM Carbon → Open Design `ibm` → airbg.org → this package) and every gap.

## The one idea to hold onto

**Two colour systems live in this product and must never mix.**

- The **UI palette** — in `colors_and_type.css`. Greys plus `--accent: #0f62fe`.
  It describes *the interface*.
- The **air-quality ramp** — served by `/api/v1/scales`. It describes *the air*.

That is why the accent is blue and not the site's old `#0b6` green: green, teal, and
yellow are ramp hues, and a green link beside a green "clean air" dot means two
unrelated things. It is also why `--success` and `--warn` are restricted to system
messaging and are forbidden on the map, in the legend, on a chart series, and in any
value readout. The ramp is **absent from this package on purpose**; the legend chips
carry unset `--ramp-*` slots for the app to fill at runtime.

## Files

```
DESIGN.md              The contract: context, colour, type, layout, components, motion,
                       voice, anti-patterns. Read this first.
README.md              This file.
SKILL.md               Agent-facing instructions for building with the system.

tokens.css             Spacing, radius, containers, elevation, motion, ramp slots.
colors_and_type.css    Colour and typography foundations, with contrast ratios.
components.css         Masthead, cards, map/chart frames, legend, buttons, metric
                       switcher, oblast table, footer.

assets/
  favicon.svg          Square app mark (source shipped rx="7"; corrected here)
  wordmark.svg         Masthead wordmark
  README.md            Asset provenance and what is missing

preview/               Nine focused review cards + preview.css + MANIFEST.md
  index.html           Links all nine
  colors-primary.html            colors-state-and-ramp.html
  typography-specimens.html      spacing-tokens.html
  radius-shadow.html             components-buttons.html
  components-data.html           brand-assets.html
  applied-surfaces.html

ui_kits/app/           Applied interface kit: four screens, seven partials, app.css
  index.html  map-home.html  oblast-table.html  area-detail.html  states.html
  components/  app-shell.html  input-bar.html  sidebar.html  readouts.html
               map-legend.html  oblast-table.html  chart.html

examples/
  theme.css            tokens + colours/type + components concatenated, the form the
                       Go app ships at internal/web/static/theme.css
  README.md

context/
  source-context.md    Handoff note from the copy step
  source-DESIGN.md     Source evidence, unmodified
  source-tokens.css    Source evidence, unmodified
  provenance.md        Chain, what is evidence vs. generated, what could not be sourced
```

## Usage

```html
<link rel="stylesheet" href="tokens.css">
<link rel="stylesheet" href="colors_and_type.css">
<link rel="stylesheet" href="components.css">
```

In the Go app, ship `examples/theme.css` instead — one file, because there is no build
step and the CSP forbids inline styles.

Set the served ramp at runtime, on `:root`:

```js
scales.bands.forEach((b, i) => root.style.setProperty(`--ramp-${i + 1}`, b.colour));
```

## Constraints that outrank taste

These predate the design. A change that breaks one is rejected, not negotiated.

| Constraint | Consequence |
|---|---|
| CSP allows no inline styles | No `style="…"`, ever. Every value is a token in a stylesheet. |
| The ramp is server-defined | Design never restates or overrides it. |
| Map tiers are an anti-enumeration control | Design may explain the tiers; it may not widen what a tier returns. |
| Country fit has ~3.4% latitude margin | The fit is a **maximum** aspect of 926∶382 — a height floor, not a fixed height. Taller is safe, shorter crops. Re-measure only if projection or zoom changes. |
| No new runtime dependency | No icon font, no CSS framework, no webfont CDN. |
| Bulgarian is the primary language | Fit is checked in Cyrillic. An English headline that fits proves nothing. |

## Preview manifest

Nine focused cards in `preview/`, each loading the real package files rather than a
copy. `preview/MANIFEST.md` carries the same list plus what a reviewer should try to
break.

| Card | Reviews |
|---|---|
| `preview/index.html` | Launcher, links all nine |
| `preview/colors-primary.html` | Surfaces, text, accent, lines, with measured contrast |
| `preview/colors-state-and-ramp.html` | UI palette vs. served ramp; unset `--ramp-*` slots; empty state |
| `preview/typography-specimens.html` | Full scale in Bulgarian, tracking, `tabular-nums` readout |
| `preview/spacing-tokens.html` | 8px grid, the two containers, breakpoints |
| `preview/radius-shadow.html` | `--radius: 0` + pill exception, depth layering, the one shadow, focus ring |
| `preview/components-buttons.html` | Button hierarchy, state pairs, `fieldset` metric switcher |
| `preview/components-data.html` | Oblast table, map frame, legend with tier line, chart frame |
| `preview/brand-assets.html` | `assets/favicon.svg` + `assets/wordmark.svg` as shipped; live masthead |
| `preview/applied-surfaces.html` | The four `ui_kits/app/` screens embedded as real files |

## Preserved assets, fonts and build artifacts

| Kind | Status |
|---|---|
| Brand assets | `assets/favicon.svg` (square, corrects the source `rx="7"`), `assets/wordmark.svg`. Provenance in `assets/README.md`. |
| Fonts | **No `fonts/`** — the IBM Plex Sans Cyrillic + Latin `.woff2` subsets were not supplied. A written `@font-face` block waits, commented, in `colors_and_type.css`. |
| Build/runtime icons | **No `build/`** — the source project ships no icon set and forbids icon fonts by rule (§1, no new runtime dependency). |
| Source examples | `context/source-DESIGN.md`, `context/source-tokens.css` (unmodified); `examples/theme.css` is the shipping concatenation. |

## Start here

1. `preview/index.html` — all nine cards.
2. `preview/colors-state-and-ramp.html` — the separation the whole system turns on.
3. `preview/applied-surfaces.html` — the real screens, embedded as files.

## Known gaps

- **No `fonts/`.** The IBM Plex Sans Cyrillic + Latin `.woff2` subsets were not
  supplied. `colors_and_type.css` has a written, commented-out `@font-face` block;
  uncomment it once the files land, then re-check Bulgarian line fit, because the
  fallback stack has different metrics.
- **No `build/`.** The source project has no runtime icon set to preserve — it uses no
  icon font by rule.
- **Ramp swatches render empty.** Deliberate. See above and `context/provenance.md`.
