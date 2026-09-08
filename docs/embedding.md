# Embedding the map

`/embed` serves the map on its own, for an `<iframe>` on another site. It is the
same map, the same islands and the same API as the home page — only the site's
chrome is gone.

```html
<iframe src="https://airbg.org/embed"
        title="Качество на въздуха — airbg.org"
        width="100%" height="480" loading="lazy"
        style="border:0"></iframe>
```

Give the frame a height. The map fills whatever height the host page allots and
does not impose the site's own 926:382 aspect ratio, because inside a frame the
height has already been decided by the page around it.

## What the frame contains

The map, its layers menu, its zoom and fullscreen controls, its colour key, the
metric switcher as an overlay in the top-left row, and a link back to
airbg.org in the bottom-left corner. Clicking a sensor opens the reading panel
over the lower half of the frame. There is no masthead, no language picker, no
province table and no footer — those belong to the host page's own design.

The "find me" button is hidden: geolocation is refused by the site's
Permissions-Policy, and a cross-origin frame would need its own `allow=` besides.

## Parameters

| Parameter | Values | Effect |
|---|---|---|
| `metric` | `P1`, `P2`, `temperature`, `humidity`, `pressure`, `noise_LAeq`, `noise_LA_max` | the metric the map paints on load |
| `area` | a province slug, e.g. `sofiya-grad-oblast` (the slug in its /area/ URL) | centres and zooms the map on that province |

Both are checked against what the server already knows — the configured metric
list and the snapshot's own slugs. Anything else is ignored, and the frame
renders exactly as it would with no parameters at all. There is deliberately no
bounding-box, coordinate or list parameter: the embed reads through the same
tiered public API as the site, under the same rate limits.

```html
<iframe src="https://airbg.org/embed?area=plovdiv-oblast&metric=P1" …></iframe>
```

## Language

`/embed` renders in the site's default language (Bulgarian); `/{lang}/embed` —
`/en/embed` today, and any language later added to `internal/i18n` — renders in
that one. A frame has no language picker, so the host page chooses by URL.

## Headers

`/embed` is the only route that may be framed. It answers with
`frame-ancestors https:` and no `X-Frame-Options`, set on its own response;
every other route on the site keeps `frame-ancestors 'none'` and
`X-Frame-Options: DENY`. Any https origin may frame it — the page carries no
cookie, no session and no form, so there is nothing for a clickjacker to aim at.

The page also sends `X-Robots-Tag: noindex` and a canonical link to the map page
it mirrors, so a crawler that finds the frame indexes the site rather than the
frame.
