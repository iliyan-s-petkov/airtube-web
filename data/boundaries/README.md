# Boundary data

This file documents what these files **are**. Two companions:

- `docs/boundary-regeneration.md` — how to **rebuild** them: the Overpass
  queries, the PostGIS cleaning pipeline, the slug rule as an executable test,
  and the acceptance checks. Read it before regenerating anything.
- `docs/known-limitations.md` — the consequences a **reader of the map** should
  know, chiefly the 13/14 city-proper vs. municipality split below.

## `bulgaria.geojson`

The national boundary. Importing it is a **hard prerequisite for ingesting
anything** — `collect` filters every incoming sensor against the `country`-kind
area with `ST_Covers`, and until one exists it fails closed and stores zero rows.

```bash
airbg import-areas data/boundaries/bulgaria.geojson country
```

Run this once, after `airbg migrate`, before the first `collect`.

**Source:** [Natural Earth](https://www.naturalearthdata.com/) 1:10m Admin 0 –
Countries, feature `ADM0_A3 = BGR`, geometry unmodified. Natural Earth is public
domain, so it is committed here rather than left as a deployment step.

**Extent:** longitude 22.3450–28.6035, latitude 41.2381–44.2284. 879 vertices,
single polygon, no holes.

Verified by point-in-polygon before committing: Sofia, Varna, Plovdiv, Musala,
and the far-eastern coastal towns Balchik and Shabla fall inside; London,
Bucharest, Thessaloniki, Istanbul, Skopje and Belgrade fall outside. The
neighbour checks matter — Bulgaria's bounding box overlaps Romania, Greece,
Turkey, Serbia and North Macedonia, so a box test would wrongly accept all five.

### Not to be confused with the test fixture

`internal/area/testdata/bulgaria.geojson` is a deliberately crude hand-authored
22-vertex polygon used only by tests. It is materially wrong along the eastern
border (cut at longitude 28.00, so it drops Balchik, Kavarna and Shabla) and is
never loaded at runtime. Tests that assert "just outside the boundary" depend on
knowing its exact shape, so it stays as it is. Do not substitute this file for
it, and do not import that one in production.

## `oblasti.geojson`, `cities.geojson`, `sofia-districts.geojson`

The three area tiers `/api/v1/overview` serves. Import order does not matter,
but all three must be imported before the API reports meaningful coverage:

```bash
airbg import-areas data/boundaries/oblasti.geojson oblast
airbg import-areas data/boundaries/cities.geojson city
airbg import-areas data/boundaries/sofia-districts.geojson neighbourhood
```

**Source:** OpenStreetMap, via the Overpass API
(`overpass-api.de`/`overpass.kumi.systems`). Administrative levels, per Phase 1
§5.4's intent, with the actual levels OSM uses for each tier in Bulgaria (which
are not uniform — see below): 4 for oblasti, 8 or 5 for cities depending on
which level a given city is mapped at, 6 for Sofia's districts. Retrieved
2026-08-10.

**Oblasti (28 features):** `boundary=administrative`, `admin_level=4`, inside
the `ISO3166-1=BG` area. One relation per oblast; matches exactly.

**Cities (27 features, not 28):** Bulgaria has 28 oblasti but only 27 distinct
oblast-capital cities — Sofia is the administrative seat of both Sofia-grad
(admin_level 4, an oblast that *is* the city) and Sofia Oblast (admin_level 4,
a separate oblast whose `admin_centre` node sits at the same coordinates as
central Sofia, confirmed against this file's own Overpass response). A
28th "city" feature would mean committing Sofia's polygon twice under two
slugs — the same physical place claiming two rows — which is worse than a
file that honestly has 27. OSM's `admin_level=8` ("city proper") boundary,
tagged `place=city` or `place=town`, exists for only 13 of the 27 capitals
(Sofia, Ruse, Dobrich, Razgrad, Targovishte, Silistra, Shumen, Varna, Gabrovo,
Veliko Tarnovo, Kyustendil, Burgas, Vidin); the other 14 capitals
(Blagoevgrad, Vratsa, Kardzhali, Lovech, Montana, Pazardzhik, Pernik, Pleven,
Plovdiv, Sliven, Smolyan, Haskovo, Stara Zagora, Yambol) have no separate
settlement-level boundary in OSM at all, so their `admin_level=5`
(`border_type=municipality`) boundary is used instead — that covers the whole
municipality, not just the built-up city, which is an acceptable
approximation for point-in-polygon area aggregation but not a survey-grade
city outline.

**Sofia districts (24 features):** the 24 `raiони` are `boundary=administrative`
relations at `admin_level=6` inside the area named `Столична` (`admin_level=5`,
the "Stolichna" municipality — the brief's assumed name `Столична община` and
assumed `admin_level=9` do not match current OSM tagging; `admin_level=9`
under `Столична` is a finer subdivision, not the district level). Includes
`Лозенец` (Lozenets), which the importer test in
`internal/area/committed_boundaries_test.go` uses as its cross-tier point.

**Licence:** ODbL 1.0. Attribution — "© OpenStreetMap contributors" — must
appear in the site footer alongside sensor.community's. This is a licence
obligation, not a courtesy.

**Geometry:** fetched with `out geom;` (full outer-ring coordinates inline, no
separate `out body`/`out skel` pass needed), then cleaned and simplified with
`ST_MakeValid`, `ST_SimplifyPreserveTopology(geom, 0.002)` (roughly 200 m), and
`ST_CollectionExtract(..., 3)` to drop stray linestrings. `ST_MakeValid` is
needed here — several relations contain a spurious near-zero-length "outer"
member way (an OSM digitisation artefact) that, treated as its own ring,
collapses into a degenerate line under simplification and turns the result
into a `GeometryCollection` that `area.Import`'s `validateGeometry` would
reject; `ST_MakeValid` before simplifying repairs that without hand-editing the
source rings. `ST_SimplifyPreserveTopology` rather than `ST_Simplify` because
plain simplification can also emit self-intersecting rings on its own.
Vertex counts dropped by roughly 20–25x (oblasti: 369,993 → 14,051; cities:
109,412 → 5,635; sofia-districts: 26,457 → 1,394) while staying far more
precise than point-in-polygon on a sensor coordinate requires.

**Slugs** are transliterated from `name_bg` to ASCII. They appear in URLs
(`/oblast/{slug}`), so they must be stable: changing a slug breaks every
inbound link and every search-engine result for that page. Treat them as
permanent once shipped.

### Slugs must be unique across every `kind`, not just within one file

`area.slug` is the table's `PRIMARY KEY`, shared globally across all `kind`
values, and `area.Import` upserts on it (`ON CONFLICT (slug) DO UPDATE SET
kind = EXCLUDED.kind, ...`). A Bulgarian oblast and its capital city share the
same name and therefore the same transliteration — "Варна" oblast and "Варна"
city both slugify to `varna` — so importing `oblasti.geojson` then
`cities.geojson`, exactly the order this file documents above, silently
rewrote 26 of 28 oblast rows into city rows: same slug, so the second import's
`kind`/`name_bg`/`name_en`/`geom` overwrote the first import's row rather than
inserting a new one. `SELECT count(*) FROM area WHERE kind = 'oblast'` dropped
from 28 to 2. This is caught by
`internal/area/committed_boundaries_test.go`'s
`TestAllFourFilesImportWithoutRowLoss`, which asserts per-kind row counts in
the database after all four files are imported together — not just each
file's parsed feature count, which stays correct regardless of what happens
in the table.

**Fix:** every oblast slug carries a uniform `-oblast` suffix
(`varna-oblast`, `plovdiv-oblast`, `sofiya-grad-oblast`, `sofiyska-oblast`, all
28, including the two Sofia oblasti whose slugs never collided with a city in
the first place). The suffix is uniform rather than applied only where a
collision exists today, because a collision-driven rule breaks again the
first time a new city is added whose name happens to match an oblast that
currently has no colliding city. Cities keep their bare slug (`varna`,
`plovdiv`) because a city is what a reader searches for, and the slug is what
appears in its URL (`/area/varna`). Sofia district slugs are untouched.

Checked and confirmed collision-free (28+27+24+1 = 80 distinct slugs, `bulgaria` included):
oblast vs. city (the only pair that collided, now fixed by the suffix), city
vs. Sofia district, oblast vs. Sofia district, and every one of the three
files against `bulgaria.geojson`'s own `bulgaria` slug. Any future fourth or
fifth tier must run the same cross-file slug check before being committed —
the primary key does not care which file a row came from.
