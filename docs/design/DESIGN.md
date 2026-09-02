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

## 1. Non-negotiables inherited from the app

These predate the design and outrank it. A design change that breaks one of these is
rejected, not negotiated.

| Constraint | Where it lives | Consequence for design |
|---|---|---|
| CSP allows no inline styles | server headers, `theme.css` comment | No `style="…"`, ever. Every value is a token in a stylesheet. |
| Air-quality colours are server-defined | `/api/v1/scales`, `web/src/lib/colour.js` | The ramp is data, not brand. Design never restates or overrides it. |
| Map tiers are an anti-enumeration control | Phase 1 §7.1, `tierFor()` | Design may explain the tiers. It may not widen what a tier returns. |
| Country fit has ~3.4 % latitude margin | measured at `data-zoom=7`, 926×382 | **Any change to map height re-checks the fit.** ~6 px of slack top and bottom. |
| No new runtime dependency | project rule | No icon font, no CSS framework, no webfont CDN. |

---

## 2. Colour

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

The language switcher is two links, styled identically, the current one marked with
`aria-current` and a bottom border — not bare text beside a link (D5). At 390px the
masthead stays one line; the wordmark shortens before it wraps.

### 5.2 Map

- Frame: `--frame` wide, `0` radius, `1px solid var(--border)`, no shadow.
- **Height stays 24rem until the country fit is re-measured.** See §1.
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

### 5.3 Charts

- uPlot series stroke is `--accent`. One series, one colour; no second accent.
- The hover readout floats, so it is the legitimate shadow case (§4.4).
- Frame matches the map: `--frame`, square, hairline border.
- **The x-axis label is still uPlot's English `Time`.** It needs a catalogue string.
  A Bulgarian page with an English axis label is a defect, not a default.

### 5.4 Oblast list — a table, not a directory

28 rows of name-plus-count is a directory (D3). Under this contract it is a table
at `--frame`:

| column | treatment |
|---|---|
| Oblast | 16px/400, the link, `--accent` |
| Current PM2.5 | 14px tabular-nums, right-aligned, with a ramp chip |
| Sensors | 14px tabular-nums, right-aligned, `--fg-2` |
| No data | `--muted`, spanning the value columns, no colour |

Zebra by `--surface`, hairline `--border-soft` between rows, hover to `#e8e8e8`.
Sorted by value descending, with the no-data rows last — a reader looking for bad air
should not have to scroll past eight blanks.

### 5.5 Footer

Four stacked body-weight paragraphs currently weigh as much as a content section
(D6). Demote: 12px, `--meta`, on `--surface`, at `--measure`. The disclaimer keeps
body size and `--fg-2` — it is the one line that must be read.

### 5.6 Buttons and the metric switcher

48px tall, `0` radius, `--accent` / `--accent-on` for primary; ghost is transparent
with `--accent` text. Focus is the Carbon double ring:
`0 0 0 2px #fff, 0 0 0 4px var(--accent)`.

The metric switcher is currently a native `fieldset`/`legend`. Keep the fieldset —
it is the correct grouping semantics for a radio set, and it is what a screen reader
needs. Style the legend as a 12px `--fg-2` label and the options as a segmented
control of square 40px buttons. Do not replace it with divs.

---

## 6. Responsive

| Breakpoint | Behaviour |
|---|---|
| < 672px | Single column. `--frame` collapses to full width minus 16px gutter. Table drops the sensor count. Footer stacks. |
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
- Don't change the map's height without re-measuring the country fit.
- Don't put a value on screen without saying what tier it aggregates.
