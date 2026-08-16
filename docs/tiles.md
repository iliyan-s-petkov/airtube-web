# Generating the basemap

The basemap is three static files, generated offline a few times a year and
served as-is by the tiles listener. Nothing here runs in CI: the inputs are
hundreds of megabytes and the cadence is seasonal.

| File | What it is |
|---|---|
| `bulgaria-YYYYMMDD.pmtiles` | Protomaps basemap, Bulgaria extract, ~150–300 MB. The date is part of the name, and the name goes in `tiles.archive`. |
| `glyphs/{fontstack}/{range}.pbf` | Font atlases MapLibre needs to render labels |
| `style.json` | References the `pmtiles://` source, the glyphs, and the layer styling |

Self-hosting the glyphs is not polish. Fetching them from a public endpoint
would reintroduce exactly the third-party request from a visitor's browser that
this design exists to remove.

## Pinned tool versions

- `planetiler` 0.8.3 (`planetiler.jar`, from the GitHub release)
- Java 21 or newer
- `font-maker` git tag `v0.0.1` (commit `46fac6c`, `maplibre/font-maker`) — this
  is the tool's only tagged release; it has no npm package or semver line, so
  the pin is the git tag, built from a `git clone --recursive` checkout via
  `cmake . && make` per the repository's `CONTRIBUTING.md`
- Noto Sans, release tag `NotoSans-v2.015` from `notofonts/latin-greek-cyrillic`
  — the Latin/Greek/Cyrillic split covers both the interface's English and the
  Cyrillic `name:bg` labels; a plain "Google Fonts" download is not a pin,
  because Google Fonts does not expose a version history to point at

Pin these. A basemap regenerated with a different planetiler produces different
layer names, and `style.json` references layer names. The same logic applies to
the glyph half of the procedure: a different `font-maker` build or a different
Noto Sans release can shift which codepoints exist or how they're shaped,
which surfaces as labels rendering wrong or not at all — silently, the same
failure class the startup couplings exist to prevent.

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

    font-maker --name "NotoSans-Regular" glyph-out-regular Noto_Sans/NotoSans-Regular.ttf
    font-maker --name "NotoSans-Medium"  glyph-out-medium  Noto_Sans/NotoSans-Medium.ttf
    mkdir -p glyphs
    mv glyph-out-regular/NotoSans-Regular glyphs/
    mv glyph-out-medium/NotoSans-Medium   glyphs/
    rm -rf glyph-out-regular glyph-out-medium

`font-maker`'s real signature, verified against `main.cpp` in the pinned
checkout (`CONTRIBUTING.md` only shows one example line, so the source is the
authority here) is:

    font-maker [--name FONTSTACK] <OUTPUT_DIR> <FONT.ttf> [FONT2.ttf ...]

`OUTPUT_DIR` must **not** already exist — the tool exits with an error
("output directory X exists") rather than merging into it — and it writes
`OUTPUT_DIR/<FONTSTACK>/<start>-<end>.pbf` for every 256-codepoint range the
input font(s) cover. `<FONTSTACK>` is the `--name` value copied verbatim, with
no sanitising (no space-to-dash, no case change) — it is exactly the on-disk
directory name and exactly what `style.json`'s `text-font` must name,
character for character. Passing several font files to one invocation merges
them into a **single** fontstack as fallback faces, which is not what two
separate weights need, so Regular and Medium are two separate invocations.
Because `OUTPUT_DIR` can't already exist, the two runs can't both target
`glyphs` directly; each goes to its own throwaway staging directory and is
then moved into the shared `glyphs/` tree that `internal/tiles` serves —
`glyphs/{fontstack}/{range}.pbf`, exactly that depth, or the handler's
allowlist 404s it.

Generate a fontstack for every `text-font` the style references, and no more.
If a style layer's `text-font` doesn't match one of these `--name` values
exactly, the glyph fetch 404s and the label silently disappears rather than
erroring — there is no visible failure to debug from.

## 4. The style

Start from a pinned Protomaps theme and set:

- `sources.protomaps.url` to `pmtiles://<tiles.public_url>/<tiles.archive>` — the
  same dated filename you generated in §1, e.g.
  `pmtiles://https://tiles.airbg.org/bulgaria-20260815.pmtiles`
- `glyphs` to `<tiles.public_url>/glyphs/{fontstack}/{range}.pbf`
- every label layer's `text-field` to `["coalesce", ["get", "name:bg"], ["get", "name"]]`,
  so the basemap follows the interface language
- `attribution` to `© OpenStreetMap contributors, © Protomaps`

The attribution is a licence obligation, not presentation: OpenStreetMap data is
ODbL. The page footer must carry the same credit.

## 5. Install

Lay the three artefacts out under `tiles.dir`, keeping the dated archive name:

    /var/lib/airbg/tiles/
      style.json
      bulgaria-20260815.pmtiles
      glyphs/NotoSans-Regular/0-255.pbf
      ...

Then set `tiles.archive` to that filename:

```yaml
tiles:
  addr: "127.0.0.1:8082"
  dir: "/var/lib/airbg/tiles"
  public_url: "https://tiles.airbg.org"
  archive: "bulgaria-20260815.pmtiles"   # regeneration changes this
```

Do **not** rename or symlink the archive to a fixed name. The handler serves
exactly one archive name — the configured one — and responses carry
`Cache-Control: public, max-age=31536000, immutable`. That header is only
truthful because regeneration produces a new filename and therefore a new URL:
reuse the name and every returning visitor keeps serving themselves the old
basemap for up to a year, with no way to invalidate it.

Three names must agree: the file on disk, `tiles.archive`, and the
`pmtiles://` URL inside `style.json` (§4). The first two are checked at
startup — the handler refuses to start if `style.json`, the configured archive
or `glyphs/` is missing, so a mis-set `tiles.dir` or `tiles.archive` is a
startup failure rather than a blank map nobody notices. The third is not
checkable from the server, because `style.json` is an opaque generated
artefact: if it points at a name the handler does not serve, the basemap is
blank and only the browser's network panel says so.

## 5a. Regenerating

Every regeneration is: build a new `bulgaria-YYYYMMDD.pmtiles`, point
`style.json` at the new name, update `tiles.archive`, restart. The old archive
can stay on disk for as long as you like — the handler will not serve it once
`tiles.archive` names the new one — and should be deleted once no cached page
still references it.

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

## 7. Sizing the tiles host

Nothing fronts `tiles.addr` — that is the point of the DNS-only hostname, and it
means every byte is served from the origin's own bandwidth. Size for it before
deploying, because the ceiling is not obvious:

`listen.max_conns` caps concurrent connections on the tiles listener as well as
the public one, and a single unranged `GET /<tiles.archive>` transfers the whole
archive. Worst-case concurrent egress is therefore `listen.max_conns` × the
archive size — with the shipped cap and a ~300 MB Bulgaria extract, that is
substantial.

No real client does this: MapLibre reads the archive through the `pmtiles`
protocol, which issues ranged requests for the few megabytes a viewport needs.
The unranged GET is a `curl` away, though, so treat it as a bandwidth
consideration when choosing the host, not as an attack that has been closed.

## Open deployment questions

Two things are left to the deployment phase, not decided here — the
configuration supports either:

- Whether `tiles.dir` is baked into the image (~300 MB image, simpler) or
  mounted as a volume (smaller image, one more thing to provision).
- Whether `tiles.airbg.org` gets its own TLS certificate or a wildcard.
