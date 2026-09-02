---
name: airbg-org-design-system
title: airbg.org design system
user-invocable: true
description: Use when building or extending any airbg.org surface, or any Bulgarian-first public-data interface in this visual language — Carbon-derived, monochrome plus one blue, square corners, Cyrillic-first, strict CSP, and a strict separation between UI colour and served air-quality data colour.
---

# airbg.org design system


## What is inside

| Path | What it gives you |
|---|---|
| `DESIGN.md` | The contract — §0 product context, §1 non-negotiables, §2 color, §3 type, §4 layout, §5 components, §8 motion, §9 voice, §10 anti-patterns |
| `tokens.css` | Spacing, radius, containers, elevation, motion, `--ramp-*` slots |
| `colors_and_type.css` | Color + typography foundations, contrast ratios, commented `@font-face` |
| `components.css` | Masthead, cards, map/chart frames, legend, buttons, metric switcher, oblast table, footer |
| `assets/` | `favicon.svg`, `wordmark.svg` |
| `preview/` | Nine focused review cards + `MANIFEST.md` |
| `ui_kits/app/` | Four applied screens + `app.css` + seven `components/` markup partials |
| `examples/theme.css` | The concatenated single file the Go app ships |
| `context/` | Source evidence and `provenance.md` |

## Source context

Adapted from **airbg.org** (source project `airbg-org`), a Bulgarian-first public
air-quality site: a country sensor map, 28 oblasti, uPlot time-series, server-rendered
Go, strict CSP. Its own design language was hand-adapted from IBM Carbon via the Open
Design `ibm` system. The two source files — `DESIGN.md` and `tokens.css` — are
preserved unmodified in `context/`. Full chain and gaps: `context/provenance.md`.

## When to use

Use it for any airbg.org surface, and for any Bulgarian-first or Cyrillic-first
public-data interface that wants this register: institutional, dense, square-cornered,
one accent, data-colour kept strictly apart from UI colour.

Do not use it for marketing pages, consumer apps that want warmth, or any product
where colour must carry both brand and data meaning — the whole system is built on
those two never overlapping.

## How to use

## Read order

1. `DESIGN.md` — the contract. §0 product context, §1 non-negotiables, §10 anti-patterns.
2. `tokens.css`, `colors_and_type.css`, `components.css` — the values.
3. `ui_kits/app/` — start from the screen closest to what you are building.

Never restate a value from these files in a new place. Bind the token.

## Before you write a line

```html
<link rel="stylesheet" href="tokens.css">
<link rel="stylesheet" href="colors_and_type.css">
<link rel="stylesheet" href="components.css">
```

Then pick a container. There are exactly two, and choosing wrong is the most common
structural mistake:

- `.measure` — 60rem. Prose, headings, about page, footer.
- `.frame` — 78rem. Map, charts, the oblast table. Data wants width.

Nothing else gets its own width.

## The rule that governs everything

**The UI palette and the air-quality ramp are separate systems.**

- UI colour: greys + `--accent: #0f62fe`. Never green, teal, or yellow.
- Air-quality colour: served by `/api/v1/scales`, exposed here as unset `--ramp-*`
  custom properties.

If you are about to write a hex value for an air-quality band, stop — you are
inventing data. Set the slot at runtime instead. If you are about to use `--success`
or `--warn` anywhere except a system message, stop — those hues belong to the air.

## Hard rules

**Colour**
- One accent, at most twice per screen. No second chromatic hue.
- Empty state is `--muted` at body size, no icon, no colour. Absence of data is not an error.
- `--meta` (#8d8d8d, 3.5:1) is for 12px captions only. Never body copy.
- Hover moves the **background**; it never lightens the foreground toward it.
  Define foreground and background as a pair. Disabled is the only state allowed to
  reduce contrast.

**Type**
- IBM Plex Sans, weights 300/400/600. **Weight 700 does not exist.** No italic display type.
- Tracking only below 16px: 0.16px at 14px, 0.32px at 12px, zero above.
- Every measured value gets `font-variant-numeric: tabular-nums` (`.num` / `.t-readout`).
- Write and fit copy in **Bulgarian**. Cyrillic runs longer; an English fit proves nothing.

**Layout**
- `border-radius: 0` everywhere. The legend chip at 24px is the only exception.
- No card shadows. Depth is `#ffffff → #f4f4f4 → #e0e0e0`. `--elev-float` means the
  element genuinely floats: a hover readout, a dropdown.
- 8px grid. Component padding 16px, section rhythm 48px.
- Touch targets 48px; 40px is the floor for a compact control.
- **926∶382 is the map's height floor, not its height.** That aspect is the measured
  country fit (~3.4% latitude margin, ~6px of slack at `data-zoom=7`). A frame wider
  than that ratio crops the country; a taller one never does. Re-measure only if the
  projection or zoom changes.
- **Sensor counts are two columns, never one.** Active (reported this hour) and
  registered (installed) differ by 55 nationally; a bare "Сензори" lets the reader
  pick the flattering reading. Active never exceeds registered, and the columns sum
  to the 585 / 640 the home page prints. A silent oblast still shows `0 / N`.
- **The table carries both PM metrics as columns; only the map switches between
  them.** A dot holds one colour, a row holds as many columns as it needs. Never put
  a metric switcher on a table. PM10 is never below PM2.5 for the same oblast, and an
  oblast's figures must match on every screen that prints them.
- **The oblast table ships all 28 rows, with search, a data filter and sortable
  headers.** Two invariants: no-data rows sink to the bottom in *every* sort order,
  and the count line's denominator is always 28. Sorting names uses
  `Intl.Collator('bg')`; `aria-sort` lives on the `<th>`, the control is a `<button>`.
- **Paging is applied last and defaults to off** (`Всички`). It slices the sorted,
  filtered set, so it cannot reorder anything. End buttons disable rather than hide;
  any change to the set resets to page 1.
- **Rows-per-page and page navigation live in one bar under the table.** Only the
  page controls hide at a single page — the select always stays, or the reader loses
  the only way to create pages.
- **"Всички" resolves to the row count, never `Infinity`** — `0 × Infinity` is `NaN`
  and `slice(NaN, NaN)` empties the table.
- **Page sizes derive from the VISIBLE row count, rebuilt whenever it changes.** Only
  true divisors (so every page is full), at least 3 rows per page, at most 3 options,
  largest first. 28 → 14/7/4; 20 → 10/5/4; 8 → 4; ≤5 → the control hides. Never spell
  "all" as a literal count, and never disable an option — rebuild the set instead.
- **`data-od-id` must be unique.** A region and the element inside it sharing one
  value makes `querySelector` return the wrapper; scope selectors by element type and
  fail loudly.
- **`.select` (§5.8) and `.pager` (§5.9)** are the components for this.
- **The footer speaks to the reader**: disclaimer, aggregation note, timestamp. No
  endpoints, licences or framework names — implementation facts live in DESIGN.md.
- **Nav carries destinations only.** A detail view is reached from a row or a map dot
  and marks its parent section current; it is never a top-level tab.
- **The current tab differs on two channels** — lifted background plus a 3px rule —
  because hover already claims the white text.
- **A picker's options must be bound.** Choosing one moves `aria-current`, updates
  the button face, sets `document.documentElement.lang`, closes and refocuses.
- **Strings live in a catalogue** (`i18n.js`) keyed by `data-i18n`; the picker swaps
  them. Proper nouns and band labels come from the API's `name_en` / `label`, never
  hand-transliterated.
- **Language is a `.langpick` (§5.12).** Flags name nations: use one only where the
  language maps to a single country, otherwise the language code in the same slot.
- **Column visibility is a `.colmenu` (§5.11).** Checkboxes only, no footnote — the
  identity column's absence from the list *is* the statement. The identity column has
  no checkbox;
  the last data column's checkbox disables; hiding the sorted column re-sorts;
  `colspan` on spanning cells is computed, not constant; breakpoints outrank the
  reader's choice. Persists in `localStorage`, with empty states discarded.
- **Search is a `.combobox` (§5.10)**: ARIA 1.2, focus stays in the input via
  `aria-activedescendant`, options are `<li role="option">` bound on `mousedown`,
  inline completion runs only while the value grows and only on a prefix match,
  Escape closes then clears. The floating list is a sanctioned shadow case.
- **Inputs use `.field` + `.input`** (§5.7): filled `--surface`, square, 48px, single
  bottom rule, double focus ring, always a visible label.
- **The map is the primary surface**, not a tile. Use `.map--hero` outside `.frame`
  on a map-led page; tune with `--map-cap` (`78vh` desktop / `52vh` under 1056px).
  Raise it freely; it can only ever add height above the fit floor.

**Components**
- Keep the metric switcher a native `fieldset`/`legend`. Style it; do not replace it with divs.
- A map without a legend is incomplete. The legend states the ramp **and** what a dot
  means at this zoom — oblast average, city aggregate, or single sensor.
- Ranked regions are a **table**, not a directory: tabular numerals right-aligned,
  zebra, no-data rows sorted last.
- Focus is the Carbon double ring `0 0 0 2px #fff, 0 0 0 4px var(--accent)`, with no
  transition on the ring itself.

**Motion**
- Only `background-color`, `border-color`, `box-shadow`, `opacity`, `transform` animate.
  Never `width`/`height`/`top`/`left` — they reflow the map frame.
- No transition on a measured value. No entrance animation. No pulsing dots, no
  skeleton shimmer. A loading state is a `--muted` line of text.
- Honour `prefers-reduced-motion`.

**Voice**
- Bulgarian first, neutral institutional, no exclamation marks.
- **Never print a value without saying what it aggregates.**
- Units always (`µg/m³`). Timestamps absolute and local, never "преди 3 минути".
- No English fallthrough — the uPlot `Time` axis label is a defect, not a default.

**Environment**
- The CSP forbids inline styles: no `style="…"` attribute, ever, including in SVG you
  embed. Add a class and a rule.
- No new runtime dependency: no icon font, no CSS framework, no webfont CDN.

## Building a new screen

1. Copy the nearest screen from `ui_kits/app/`, or assemble one from the partials in
   `ui_kits/app/components/` (app shell, input bar, sidebar, readouts, map + legend,
   oblast table, chart).
2. Replace content; do not add CSS. If a component is missing, add it to
   `components.css` with a `DESIGN.md` §-reference in the comment, then use it.
3. Regenerate the shipping bundle:
   `cat tokens.css colors_and_type.css components.css > examples/theme.css`
4. Check in Bulgarian at 390px, 672px, and 1056px.
5. Tab through it. Every focusable element shows the double ring.

## Self-check before you call it done

- [ ] No hex value outside `tokens.css` / `colors_and_type.css` (the two SVGs in
      `assets/` are the documented exception — an `<img>` SVG cannot read `theme.css`).
- [ ] No `style="…"` anywhere.
- [ ] No green, teal, or yellow in the interface.
- [ ] No weight 700, no radius except the legend chip, no card shadow.
- [ ] Every measured value has tabular numerals, a unit, and a stated tier.
- [ ] Every empty state is `--muted` and uncoloured.
- [ ] Hover raises contrast; focus rings are intact.
- [ ] Copy is Bulgarian and fits at 390px.

## Design-system highlights

- **Two palettes, never mixed.** Greys + one blue for the interface; the air-quality
  ramp is served data and stays out of the design system.
- **Square by rule.** `--radius: 0` everywhere; the legend chip is the sole pill.
- **Depth by layering,** not shadow: `#ffffff → #f4f4f4 → #e0e0e0`.
- **Two containers:** 60rem for prose, 78rem for data.
- **Cyrillic-first type:** IBM Plex Sans 300/400/600, tracking only below 16px,
  tabular numerals on every measured value.
- **Motion is confirmation, not personality** — and never touches a number.
- **Voice rule with teeth:** never print a value without saying what it aggregates.
