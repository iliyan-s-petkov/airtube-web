# Boundary data

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
