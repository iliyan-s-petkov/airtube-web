# Generating the basemap

The basemap is three static files, generated offline a few times a year and
served as-is by the tiles listener. Nothing here runs in CI: the inputs are
hundreds of megabytes and the cadence is seasonal.

| File | What it is |
|---|---|
| `bulgaria.pmtiles` | Protomaps basemap, Bulgaria extract, ~150–300 MB |
| `glyphs/{fontstack}/{range}.pbf` | Font atlases MapLibre needs to render labels |
| `style.json` | References the `pmtiles://` source, the glyphs, and the layer styling |

Self-hosting the glyphs is not polish. Fetching them from a public endpoint
would reintroduce exactly the third-party request from a visitor's browser that
this design exists to remove.

## Pinned tool versions

- `planetiler` 0.8.3 (`planetiler.jar`, from the GitHub release)
- Java 21 or newer
- `font-maker` (or `build-glyphs` from `fontnik`) for the glyph PBFs
- Noto Sans Regular and Noto Sans Medium, from the Google Fonts release

Pin these. A basemap regenerated with a different planetiler produces different
layer names, and `style.json` references layer names.

## 1. The extract

Download the Bulgaria extract from Geofabrik:

    https://download.geofabrik.de/europe/bulgaria-latest.osm.pbf

## 2. The archive

    java -Xmx8g -jar planetiler.jar \
      --osm-path=bulgaria-latest.osm.pbf \
      --output=bulgaria-YYYYMMDD.pmtiles \
      --force

The date suffix is what keeps `Cache-Control: immutable` honest: regeneration
changes the filename, so a cached copy can never be stale. Deploy by writing the
new file, updating `style.json`'s source URL, and only then removing the old one.

## 3. The glyphs

    font-maker Noto_Sans/NotoSans-Regular.ttf glyphs/NotoSans-Regular
    font-maker Noto_Sans/NotoSans-Medium.ttf  glyphs/NotoSans-Medium

Generate a fontstack for every `text-font` the style references, and no more.

## 4. The style

Start from a pinned Protomaps theme and set:

- `sources.protomaps.url` to `pmtiles://<tiles.public_url>/bulgaria-YYYYMMDD.pmtiles`
- `glyphs` to `<tiles.public_url>/glyphs/{fontstack}/{range}.pbf`
- every label layer's `text-field` to `["coalesce", ["get", "name:bg"], ["get", "name"]]`,
  so the basemap follows the interface language
- `attribution` to `© OpenStreetMap contributors, © Protomaps`

The attribution is a licence obligation, not presentation: OpenStreetMap data is
ODbL. The page footer must carry the same credit.

## 5. Install

Lay the three artefacts out under `tiles.dir`:

    /var/lib/airbg/tiles/
      style.json
      bulgaria.pmtiles
      glyphs/NotoSans-Regular/0-255.pbf
      ...

`bulgaria.pmtiles` is the name the handler serves; symlink the dated file to it,
or rename on install. The handler refuses to start if any of `style.json`,
`bulgaria.pmtiles` or `glyphs/` is missing, so a mis-set `tiles.dir` is a
startup failure rather than a blank map nobody notices.

## 6. The firewall rule

This is load-bearing, not advisory. Serving tiles from the origin means a
hostname that resolves to the origin IP, and the anti-scraping design depends on
that IP being unknown: `CF-Connecting-IP` is attacker-controlled on a direct
connection, and every rate limiter keys off it.

- The **application port** (`listen.addr`) accepts connections only from
  Cloudflare's published IP ranges, enforced by a packet filter.
  `listen.trusted_proxy_cidrs` is not sufficient on its own — it governs header
  parsing, not who may connect.
- The **tiles port** (`tiles.addr`) accepts the world, on a DNS-only hostname.

With the filter in place, discovering the origin IP yields tiles and nothing
else. Without it, self-hosting the tiles weakens the system.

## Open deployment questions

Two things are left to the deployment phase, not decided here — the
configuration supports either:

- Whether `tiles.dir` is baked into the image (~300 MB image, simpler) or
  mounted as a volume (smaller image, one more thing to provision).
- Whether `tiles.airbg.org` gets its own TLS certificate or a wildcard.
