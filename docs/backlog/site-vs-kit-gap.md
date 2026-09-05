# The site vs. the kit's reference pages

Written 2026-09-06, after `open-design` reconnected and the kit's own page
mockups became readable.

## What changed in my understanding

Stages 1–3 of the design-kit adoption treated `design-kit/` as three
stylesheets — `tokens.css`, `colors_and_type.css`, `components.css` — and
adoption as "make the site's markup carry the kit's class names". That is
what commits `8a8496f`, `b31acb6` and `54515bb` did.

`design-kit/ui_kits/app/` is the part that was never read. It is not a token
file. It is a complete reference implementation of the same three pages the
site serves — `map-home.html`, `oblast-table.html`, `area-detail.html` — with
its own `app.css` (332 lines) and sixteen JS modules. It is the "od" the site
was supposed to look like.

The site does not look like it. The difference is structural, not colour.

## The gap, on the home page

| | Kit `map-home.html` | Live airbg.org |
|---|---|---|
| Header | `.masthead`: brand lockup with favicon, `airbg` + muted `.org`, nav tabs with an active pill, a theme picker (auto/light/dark), a flag+code language dropdown | plain text link row, no theme control, `Български`/`English` as bare links |
| H1 | `Качеството на въздуха в България` | `Мръсен въздух` |
| Metric control | `.switcher` — two segmented radios, ФПЧ2.5 / ФПЧ10 only | seven chips in a fieldset, including humidity, noise, pressure, temperature |
| Toolbar | province search combobox + `Обнови` button, right-aligned | none |
| Map | `.map--hero`, full-bleed edge to edge | boxed, centred, ~1184px |
| Map controls | fullscreen top-right, zoom rail, layer switcher on-map bottom-left | none |
| Legend | `.scale--vertical.scale--onmap` — a continuous ticked bar on the map, bottom-left, collapsible via `<details>`, edge numbers between segments | boxed overlay top-left, discrete swatch rows with range text |
| Below the map | `.readouts` — a four-cell grid: highest value, median across provinces, sensors in network, provinces with no data | province table straight away |
| Refresh | `.data-refresh` status line + auto-refresh checkbox | none |
| Footer | three-line disclaimer block in a `.measure` | shorter |

`oblast-table.html` and `area-detail.html` have not been compared yet.

## Consequences for work already done

- The legend built in `b31acb6` matches the kit's *class names* but not its
  *form*. The kit's legend is a vertical continuous bar; the site's is a row
  list. Adopting `legend__row`/`legend__label` was correct as far as it went
  and is not wasted, but the shape still has to change.
- Dark mode is correct after all. The kit's own pages render dark under a
  dark OS, exactly as the site now does. The contrast bug `54515bb` fixed was
  real; the theme itself was never the problem. The kit additionally ships a
  three-way theme picker the site does not have, so a reader can override it.
- The seven-metric chip row is a site feature with no kit counterpart. The
  kit shows two. This is a product question, not a styling one.

## Open decisions

1. How far to go: the header alone, the whole home page, or all three pages.
2. Whether to keep seven metrics or narrow to the kit's two.
3. Whether `ui_kits/app/*.js` is a reference to read or code to port. The
   site's islands are Svelte-adjacent ES modules under `web/src/islands/`;
   the kit's are plain scripts. They cannot be dropped in.
