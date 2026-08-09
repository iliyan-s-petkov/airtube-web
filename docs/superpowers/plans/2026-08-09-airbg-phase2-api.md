# airbg.org Phase 2 — API and server-rendered pages: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve the collected air-quality data over a tiered, rate-limited JSON API and server-rendered bilingual area pages, from the same single Go binary that already collects it.

**Architecture:** One process, two subsystems sharing an immutable snapshot. At the end of every ingest cycle the collector builds all memory-backed API responses — serialised, gzipped, and ETagged once — and publishes them with a single `atomic.Pointer.Store`. HTTP handlers load that pointer and write precomputed bytes, taking no lock and touching no database. Only the two unbounded endpoints (`/sensor/{id}/series`, `/area/{slug}?period=`) query Postgres, through narrow indexed scans. Every request first passes a middleware chain that recovers panics, derives a *verified* client IP, and applies token-bucket and enumeration limits keyed on that verified value.

**Tech Stack:** Go 1.26 stdlib only — `net/http` pattern routing, `html/template`, `encoding/json`, `compress/gzip`, `crypto/sha256`, `embed`, `log/slog`, `sync/atomic`. Existing deps: `pgx/v5`, `goose/v3`, `testcontainers-go`. PostgreSQL 18 + PostGIS + TimescaleDB.

**Spec:** `docs/superpowers/specs/2026-08-09-airbg-phase2-api-design.md`, which extends `docs/superpowers/specs/2026-08-07-airbg-phase1-design.md` §§7–9.

## Global Constraints

- Module path is exactly `airbg.org`. Import paths are `airbg.org/internal/...`.
- **No new third-party dependency may be added.** `go.mod`'s direct `require` block must be unchanged at the end of this plan. This is why metrics are hand-rolled and why routing uses `net/http` patterns.
- **No SQL may be assembled by string concatenation, anywhere, including in tests.** Every query uses `pgx` bind parameters. The legacy PHP app's InfluxQL injection hole is a stated reason this rewrite exists.
- **No secrets in the repo.** All configuration comes from `AIRBG_*` environment variables via `internal/config`. No config files, nothing compiled in.
- `www-root/` is the legacy PHP application. **Do not modify anything under it.**
- PostGIS `geography` is **(longitude, latitude)** — the inverse of the legacy `[lat, long]` order. Bulgaria spans lon 22–29, lat 41–45.
- Canonical metrics are exactly `P1`, `P2`, `temperature`, `humidity`, `pressure`, `noise_LAeq`, `noise_LA_max`. `P1` = PM10, `P2` = PM2.5.
- `quality_flag` enum values are exactly `ok`, `out_of_range`, `stuck`, `spatial_outlier`, `no_neighbours`. Published aggregates filter `quality IN ('ok', 'no_neighbours')` — `no_neighbours` means the spatial check could not run, not that the reading failed it.
- **Coverage threshold is 3.** An area publishes an aggregate only when at least 3 distinct sensors with usable readings fall inside it. Below that it reports an insufficient-coverage state and no number.
- **Every test must fail if its fix is removed.** An assertion downstream of the filter it is testing is a tautology. Before committing any test, ask whether it would still pass with the implementation reverted.
- Tests that need a database use `testsupport.NewPostgres(t)` plus `db.Migrate`, following the existing `migrated(t)` helper pattern in `internal/area/area_test.go:15`.
- Commit messages: conventional-commit prefix, imperative mood, no attribution trailer of any kind.
- Never stage `CLAUDE.md` (gitignored deliberately) or `.claude/`.
- `git log` in this repo performs an online signature lookup per commit. Always pass `--no-show-signature` in scripts, or they hang.

---

## File Structure

**New packages:**

| Path | Responsibility |
|---|---|
| `internal/snapshot/snapshot.go` | `Snapshot` type, `atomic.Pointer` holder, `Load`/`Store` |
| `internal/snapshot/build.go` | Builds a `Snapshot` from the database: aggregates, sensors, serialisation, gzip, ETag |
| `internal/metrics/metrics.go` | Atomic counters and gauges; Prometheus text exposition |
| `internal/httpx/recover.go` | Panic recovery middleware |
| `internal/httpx/clientip.go` | Verified client-IP derivation; Cloudflare range trust |
| `internal/httpx/headers.go` | CSP, HSTS, nosniff, frame-ancestors, body cap |
| `internal/httpx/chain.go` | Middleware composition order |
| `internal/ratelimit/bucket.go` | Sharded token buckets, TTL eviction, IPv6 `/64` keying |
| `internal/ratelimit/enumerate.go` | Distinct-slug and distinct-sensor breadth counters |
| `internal/api/router.go` | Route table, error responses, ETag/304 helper |
| `internal/api/overview.go` | `/overview`, `/areas`, `/meta` |
| `internal/api/sensors.go` | `/area/{slug}/sensors` |
| `internal/api/series.go` | `/sensor/{id}/series`, `/area/{slug}?period=` |
| `internal/api/locate.go` | `/locate` |
| `internal/api/scales.go` | EAQI / EU / WHO scale tables as Go data |
| `internal/i18n/i18n.go` | Catalogue loading, `T`, missing-key fallback |
| `internal/i18n/bg.json`, `en.json` | Message catalogues |
| `internal/web/render.go` | Template set, funcmap, page data types |
| `internal/web/templates/*.gohtml` | Page shell, area page, sensor page, error page |
| `internal/server/server.go` | Public and private listeners, timeouts, graceful shutdown |

**Modified:**

| Path | Change |
|---|---|
| `internal/db/migrations/00007_area_presentation.sql` | New: `area.centroid`, `area.default_zoom` |
| `internal/store/aggregate.go` | New file in existing package: area aggregate and series queries |
| `internal/ingest/ingest.go` | `Ingester` gains an optional snapshot publisher, invoked at cycle end |
| `internal/config/config.go` | New `AIRBG_LISTEN_ADDR`, `AIRBG_METRICS_ADDR`, `AIRBG_TRUSTED_PROXY_CIDRS`, `AIRBG_BASE_URL` |
| `cmd/airbg/main.go` | New `serve` subcommand |
| `data/boundaries/` | Oblast, city and Sofia-district GeoJSON plus README |
| `README.md` | Serving instructions, Cloudflare prerequisites |

`internal/httpx` must not import `internal/api`. It operates on `http.Handler` alone, so the middleware chain can be tested with a stub handler and no database.

---

## Task 1: Area presentation columns

The `area` table has `slug`, `kind`, `name_bg`, `name_en`, `geom` and nothing else. `/locate` must snap a visitor to "that area's centroid and natural zoom" (Phase 1 §9.4), and area pages need a map centre. Computing a centroid per request is a `ST_Centroid` over a MultiPolygon on every call; computing it once at import is free.

**Files:**
- Create: `internal/db/migrations/00007_area_presentation.sql`
- Create: `internal/db/area_columns_test.go`
- Modify: `internal/area/import.go`

**Interfaces:**
- Consumes: `area.Import(ctx, pool, path, kind string) (int, error)` — existing.
- Produces: `area` rows carry non-null `centroid geography(Point, 4326)` and `default_zoom smallint`. Later tasks read both.

- [ ] **Step 1: Write the failing test**

Create `internal/db/area_columns_test.go`:

```go
package db_test

import "testing"

// TestAreaHasPresentationColumns asserts the columns exist AND are NOT NULL.
// Nullable columns would let a future import path insert an area with no
// centroid, which /locate would then resolve to a nil point — an error at the
// far end of the system from its cause. The database is the right place to
// make that impossible.
func TestAreaHasPresentationColumns(t *testing.T) {
	ctx, pool := migrated(t)

	rows, err := pool.Query(ctx,
		`SELECT column_name, is_nullable, data_type
		   FROM information_schema.columns
		  WHERE table_name = 'area'
		    AND column_name IN ('centroid', 'default_zoom')`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var name, nullable, dataType string
		if err := rows.Scan(&name, &nullable, &dataType); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = nullable
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, col := range []string{"centroid", "default_zoom"} {
		nullable, ok := got[col]
		if !ok {
			t.Errorf("area.%s does not exist", col)
			continue
		}
		if nullable != "NO" {
			t.Errorf("area.%s is nullable; it must be NOT NULL", col)
		}
	}
}

// TestDefaultZoomByKind pins the per-kind zoom levels. They are not arbitrary:
// Phase 1 §7.1 swaps the map from choropleth to individual sensors at z >= 11,
// so an oblast must resolve BELOW that threshold (or /locate would drop a
// visitor straight into the per-sensor tier for a region far too large to
// render) and a neighbourhood must resolve at or above it.
func TestDefaultZoomByKind(t *testing.T) {
	ctx, pool := migrated(t)

	// A minimal polygon somewhere inside Bulgaria; geometry is irrelevant here,
	// only the trigger-computed columns are under test.
	const poly = `MULTIPOLYGON(((23.0 42.0, 23.1 42.0, 23.1 42.1, 23.0 42.1, 23.0 42.0)))`

	for _, tc := range []struct{ kind string; wantZoom int16 }{
		{"country", 7},
		{"oblast", 9},
		{"city", 11},
		{"neighbourhood", 13},
	} {
		var zoom int16
		err := pool.QueryRow(ctx,
			`INSERT INTO area (slug, kind, name_bg, name_en, geom)
			 VALUES ($1, $2, 'x', 'x', ST_GeomFromText($3, 4326)::geography)
			 RETURNING default_zoom`,
			"zoomtest-"+tc.kind, tc.kind, poly).Scan(&zoom)
		if err != nil {
			t.Fatalf("insert %s: %v", tc.kind, err)
		}
		if zoom != tc.wantZoom {
			t.Errorf("kind %q default_zoom = %d, want %d", tc.kind, zoom, tc.wantZoom)
		}
	}
}

// TestCentroidComputedOnInsert asserts the centroid is derived from geom
// rather than left to the caller. A caller-supplied centroid can disagree with
// its own polygon; a derived one cannot.
func TestCentroidComputedOnInsert(t *testing.T) {
	ctx, pool := migrated(t)

	const poly = `MULTIPOLYGON(((23.0 42.0, 23.2 42.0, 23.2 42.2, 23.0 42.2, 23.0 42.0)))`
	var lon, lat float64
	err := pool.QueryRow(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('centroid-test', 'city', 'x', 'x', ST_GeomFromText($1, 4326)::geography)
		 RETURNING ST_X(centroid::geometry), ST_Y(centroid::geometry)`,
		poly).Scan(&lon, &lat)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Centre of that square is (23.1, 42.1). Asserting lon and lat separately,
	// against non-overlapping tolerances, is what makes this test detect a
	// (lat, lon) swap — if the columns were transposed, lon would read 42.1.
	if lon < 23.05 || lon > 23.15 {
		t.Errorf("centroid longitude = %v, want ~23.1 (a value near 42 means lon/lat are swapped)", lon)
	}
	if lat < 42.05 || lat > 42.15 {
		t.Errorf("centroid latitude = %v, want ~42.1 (a value near 23 means lon/lat are swapped)", lat)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/db/ -run 'TestAreaHasPresentationColumns|TestDefaultZoomByKind|TestCentroidComputedOnInsert' -v
```

Expected: FAIL — `column "centroid" does not exist`.

- [ ] **Step 3: Write the migration**

Create `internal/db/migrations/00007_area_presentation.sql`:

```sql
-- +goose Up

-- Two presentation columns, both derived rather than supplied.
--
-- centroid: /api/v1/locate snaps a visitor to the containing area's centre
-- (Phase 1 §9.4), and every area page centres its map. Deriving it from geom in
-- a trigger rather than accepting it from the caller removes a whole class of
-- bug: a centroid that disagrees with its own polygon. There is no code path
-- that can produce one.
--
-- default_zoom: the zoom a map opens at for this area's kind. The values
-- straddle 11 deliberately — Phase 1 §7.1 switches from the choropleth tier to
-- the individual-sensor tier at z >= 11, so an oblast resolves to 9 (still a
-- choropleth; a region that size cannot usefully render 300 markers) and a
-- neighbourhood to 13 (individual sensors, which is the whole point of looking
-- at a neighbourhood).

ALTER TABLE area ADD COLUMN centroid     geography(Point, 4326);
ALTER TABLE area ADD COLUMN default_zoom smallint;

-- +goose StatementBegin
CREATE FUNCTION area_derive_presentation() RETURNS trigger AS $$
BEGIN
    NEW.centroid := ST_Centroid(NEW.geom);
    NEW.default_zoom := CASE NEW.kind
        WHEN 'country'       THEN 7
        WHEN 'oblast'        THEN 9
        WHEN 'city'          THEN 11
        WHEN 'neighbourhood' THEN 13
    END;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER area_presentation
    BEFORE INSERT OR UPDATE OF geom, kind ON area
    FOR EACH ROW EXECUTE FUNCTION area_derive_presentation();

-- Backfill any rows imported before this migration, then enforce NOT NULL. The
-- order matters: adding NOT NULL first would fail on existing rows.
UPDATE area SET geom = geom;

ALTER TABLE area ALTER COLUMN centroid     SET NOT NULL;
ALTER TABLE area ALTER COLUMN default_zoom SET NOT NULL;

-- +goose Down
DROP TRIGGER area_presentation ON area;
DROP FUNCTION area_derive_presentation();
ALTER TABLE area DROP COLUMN default_zoom;
ALTER TABLE area DROP COLUMN centroid;
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/db/ -run 'TestAreaHasPresentationColumns|TestDefaultZoomByKind|TestCentroidComputedOnInsert' -v
```

Expected: PASS, all three.

- [ ] **Step 5: Verify the existing suite still passes**

```bash
go test ./internal/db/ ./internal/area/ -count=1
```

Expected: PASS. `internal/area`'s import tests exercise the new trigger implicitly.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/00007_area_presentation.sql internal/db/area_columns_test.go
git commit -m "feat(db): derive area centroid and default zoom in the schema"
```

---

## Task 2: Oblast, city and Sofia-district boundaries

`data/boundaries/` currently holds only `bulgaria.geojson` (`kind = 'country'`). Phase 1 §7.1 specifies `/overview` as "28 oblasti; one aggregate value and coverage state each" — with no oblast rows, that endpoint returns an empty list and reports success. That is the exact failure class Phase 1's review kept surfacing: a bug that looks like a clean result.

Phase 1 §5.4 already fixes the source: OpenStreetMap, admin level 4 for oblasti, 8 for cities, 9/10 for Sofia districts, ODbL licensed.

**Files:**
- Create: `data/boundaries/oblasti.geojson`
- Create: `data/boundaries/cities.geojson`
- Create: `data/boundaries/sofia-districts.geojson`
- Modify: `data/boundaries/README.md`
- Create: `internal/area/committed_boundaries_test.go`

**Interfaces:**
- Consumes: `area.Import(ctx, pool, path, kind string) (int, error)`; `area.AssignSensors(ctx, pool) (assigned, revoked int64, err error)`.
- Produces: three committed GeoJSON FeatureCollections whose features each carry `slug`, `name_bg`, `name_en`, `source` properties, importable by the existing `area.Import`.

- [ ] **Step 1: Write the failing test**

Create `internal/area/committed_boundaries_test.go`:

```go
package area_test

import (
	"testing"

	"airbg.org/internal/area"
)

// TestCommittedBoundariesImport imports the actual committed files, not a
// fixture. Phase 1 shipped a bulgaria.geojson that Import silently accepted as
// zero features, because every boundary test used a hand-written fixture and
// nothing ever read the real file. These assertions exist so that cannot recur.
func TestCommittedBoundariesImport(t *testing.T) {
	ctx, pool := migrated(t)

	for _, tc := range []struct {
		path     string
		kind     string
		wantMin  int
		wantMax  int
	}{
		// Bulgaria has 28 oblasti. The range is exact because the count is a
		// fact about the country, not an implementation detail.
		{"../../data/boundaries/oblasti.geojson", "oblast", 28, 28},
		// One city per oblast capital. Sofia city proper is included, so 28.
		{"../../data/boundaries/cities.geojson", "city", 28, 28},
		// Sofia has 24 raiони (districts).
		{"../../data/boundaries/sofia-districts.geojson", "neighbourhood", 24, 24},
	} {
		n, err := area.Import(ctx, pool, tc.path, tc.kind)
		if err != nil {
			t.Fatalf("Import(%s, %s): %v", tc.path, tc.kind, err)
		}
		if n < tc.wantMin || n > tc.wantMax {
			t.Errorf("Import(%s) = %d features, want %d..%d", tc.path, n, tc.wantMin, tc.wantMax)
		}
	}
}

// TestSofiaSensorResolvesThroughAllTiers asserts a single point lands in an
// oblast, a city AND a district. Importing three files that each parse is not
// the requirement; the requirement is that the three tiers nest, because
// /overview?tier=city and /area/{slug}/sensors both depend on a sensor being
// reachable at more than one zoom.
func TestSofiaSensorResolvesThroughAllTiers(t *testing.T) {
	ctx, pool := migrated(t)

	for _, f := range []struct{ path, kind string }{
		{"../../data/boundaries/oblasti.geojson", "oblast"},
		{"../../data/boundaries/cities.geojson", "city"},
		{"../../data/boundaries/sofia-districts.geojson", "neighbourhood"},
	} {
		if _, err := area.Import(ctx, pool, f.path, f.kind); err != nil {
			t.Fatalf("Import(%s): %v", f.path, err)
		}
	}

	// Sofia, Lozenets: lon 23.3219, lat 42.6977. Longitude first — PostGIS
	// geography is (lon, lat), the inverse of the legacy [lat, long] order.
	const lon, lat = 23.3219, 42.6977

	rows, err := pool.Query(ctx,
		`SELECT kind FROM area
		  WHERE ST_Covers(geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography)
		  ORDER BY kind`,
		lon, lat)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	kinds := map[string]bool{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan: %v", err)
		}
		kinds[kind] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for _, want := range []string{"oblast", "city", "neighbourhood"} {
		if !kinds[want] {
			t.Errorf("Sofia point (%v, %v) is not covered by any %s area", lon, lat, want)
		}
	}
}

// TestBoundariesDoNotSwapCoordinates is the swap detector for this data. A
// GeoJSON file written with [lat, lon] instead of [lon, lat] still parses, still
// imports, and still produces valid polygons — they simply sit in the Indian
// Ocean. Asserting the bbox catches it; asserting "the import succeeded" does
// not.
func TestBoundariesDoNotSwapCoordinates(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "../../data/boundaries/oblasti.geojson", "oblast"); err != nil {
		t.Fatalf("Import: %v", err)
	}

	var minLon, minLat, maxLon, maxLat float64
	err := pool.QueryRow(ctx,
		`SELECT ST_XMin(e), ST_YMin(e), ST_XMax(e), ST_YMax(e)
		   FROM (SELECT ST_Extent(geom::geometry) AS e FROM area WHERE kind = 'oblast') s`,
	).Scan(&minLon, &minLat, &maxLon, &maxLat)
	if err != nil {
		t.Fatalf("extent: %v", err)
	}

	// Bulgaria: lon 22.3..28.7, lat 41.2..44.3. The two ranges do not overlap,
	// which is exactly what makes a swap detectable.
	if minLon < 22.0 || maxLon > 29.0 {
		t.Errorf("oblast longitude extent %v..%v outside Bulgaria's 22..29 (values in 41..45 mean lat/lon are swapped)", minLon, maxLon)
	}
	if minLat < 41.0 || maxLat > 45.0 {
		t.Errorf("oblast latitude extent %v..%v outside Bulgaria's 41..45", minLat, maxLat)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/area/ -run 'TestCommittedBoundaries|TestSofiaSensorResolves|TestBoundariesDoNotSwap' -v
```

Expected: FAIL — `no such file or directory: ../../data/boundaries/oblasti.geojson`.

- [ ] **Step 3: Fetch the boundaries from OpenStreetMap**

Run this in the sandbox (Overpass is an HTTP call, so it goes through `ctx_execute`, never `curl` in Bash). Write the script to a file and run it:

```bash
mkdir -p "$(git rev-parse --show-toplevel)/tmp-boundaries"
```

The Overpass query for oblasti (`admin_level=4`, inside Bulgaria):

```
[out:json][timeout:180];
area["ISO3166-1"="BG"][admin_level=2]->.bg;
relation(area.bg)["boundary"="administrative"]["admin_level"="4"];
out geom;
```

For oblast capitals (`admin_level=8`, place=city):

```
[out:json][timeout:180];
area["ISO3166-1"="BG"][admin_level=2]->.bg;
relation(area.bg)["boundary"="administrative"]["admin_level"="8"]["place"="city"];
out geom;
```

For Sofia districts (`admin_level=9`, inside Sofia city):

```
[out:json][timeout:180];
area["name"="Столична община"]->.sofia;
relation(area.sofia)["boundary"="administrative"]["admin_level"="9"];
out geom;
```

Post each to `https://overpass-api.de/api/interpreter` as the `data` form field.

- [ ] **Step 4: Convert to the FeatureCollection shape `area.Import` expects**

`area.Import` requires a top-level `FeatureCollection` whose features carry `slug`, `name_bg`, `name_en` and `source` properties, and a Polygon or MultiPolygon geometry. Convert each Overpass response with this Python, run in the sandbox:

```python
import json, re, unicodedata

# Cyrillic-to-Latin transliteration for slugs. Slugs appear in URLs
# (/oblast/{slug}), so they must be ASCII, stable, and human-readable — a
# percent-encoded Cyrillic slug is neither shareable nor indexable.
CYR = {
    'а':'a','б':'b','в':'v','г':'g','д':'d','е':'e','ж':'zh','з':'z','и':'i',
    'й':'y','к':'k','л':'l','м':'m','н':'n','о':'o','п':'p','р':'r','с':'s',
    'т':'t','у':'u','ф':'f','х':'h','ц':'ts','ч':'ch','ш':'sh','щ':'sht',
    'ъ':'a','ь':'','ю':'yu','я':'ya',
}

def slugify(name_bg):
    s = ''.join(CYR.get(ch, ch) for ch in name_bg.lower())
    s = unicodedata.normalize('NFKD', s).encode('ascii', 'ignore').decode()
    s = re.sub(r'[^a-z0-9]+', '-', s).strip('-')
    return s

SOURCE = "OpenStreetMap contributors, ODbL 1.0"

def convert(overpass_path, out_path):
    raw = json.load(open(overpass_path))
    features = []
    seen = set()
    for el in raw['elements']:
        tags = el.get('tags', {})
        name_bg = tags.get('name')
        if not name_bg:
            continue
        slug = slugify(name_bg)
        if not slug or slug in seen:
            continue
        seen.add(slug)
        rings = [
            [[pt['lon'], pt['lat']] for pt in m['geometry']]
            for m in el.get('members', [])
            if m.get('role') == 'outer' and m.get('geometry')
        ]
        if not rings:
            continue
        # Close each ring; GeoJSON requires first point == last point.
        for r in rings:
            if r[0] != r[-1]:
                r.append(r[0])
        features.append({
            "type": "Feature",
            "properties": {
                "slug": slug,
                "name_bg": name_bg,
                "name_en": tags.get('name:en', name_bg),
                "source": SOURCE,
            },
            "geometry": {"type": "MultiPolygon", "coordinates": [[r] for r in rings]},
        })
    json.dump({"type": "FeatureCollection", "features": features},
              open(out_path, 'w'), ensure_ascii=False, separators=(',', ':'))
    print(out_path, len(features), "features")
```

Then reduce vertex count so the committed files stay reviewable. Load each file into the running Postgres and simplify with PostGIS rather than reimplementing Douglas–Peucker:

```sql
SELECT ST_AsGeoJSON(ST_SimplifyPreserveTopology(ST_GeomFromGeoJSON($1), 0.002))
```

`0.002` degrees is roughly 200 m at this latitude — far below the precision at which an oblast boundary matters for point-in-polygon on a sensor, and it typically cuts vertex count by an order of magnitude. `ST_SimplifyPreserveTopology` is required rather than `ST_Simplify`: plain simplification can produce self-intersecting rings, which `area.Import`'s existing `validateGeometry` rejects.

- [ ] **Step 5: Verify the files before committing them**

```bash
python3 - <<'PY'
import json
for p, want in [("data/boundaries/oblasti.geojson", 28),
                ("data/boundaries/cities.geojson", 28),
                ("data/boundaries/sofia-districts.geojson", 24)]:
    d = json.load(open(p))
    assert d["type"] == "FeatureCollection", (p, d["type"])
    n = len(d["features"])
    lons, lats = [], []
    for f in d["features"]:
        for k in ("slug", "name_bg", "name_en", "source"):
            assert f["properties"].get(k), (p, f["properties"].get("slug"), k)
        for poly in f["geometry"]["coordinates"]:
            for ring in poly:
                for lon, lat in ring:
                    lons.append(lon); lats.append(lat)
    print(p, n, "features", "lon", min(lons), max(lons), "lat", min(lats), max(lats))
    assert n == want, (p, n, want)
    assert 22 <= min(lons) and max(lons) <= 29, (p, "longitude out of Bulgaria")
    assert 41 <= min(lats) and max(lats) <= 45, (p, "latitude out of Bulgaria")
PY
```

Expected: three lines, no assertion error. If longitudes come out in 41–45, the coordinate order is swapped — fix the converter, do not adjust the assertion.

- [ ] **Step 6: Document the provenance**

Append to `data/boundaries/README.md`:

```markdown
## `oblasti.geojson`, `cities.geojson`, `sofia-districts.geojson`

The three area tiers `/api/v1/overview` serves. Import order does not matter,
but all three must be imported before the API reports meaningful coverage:

```bash
airbg import-areas data/boundaries/oblasti.geojson oblast
airbg import-areas data/boundaries/cities.geojson city
airbg import-areas data/boundaries/sofia-districts.geojson neighbourhood
```

**Source:** OpenStreetMap, via the Overpass API. Administrative levels per
Phase 1 §5.4: 4 for oblasti, 8 for oblast-capital cities, 9 for Sofia's
districts. Retrieved 2026-08-09.

**Licence:** ODbL 1.0. Attribution — "© OpenStreetMap contributors" — must
appear in the site footer alongside sensor.community's. This is a licence
obligation, not a courtesy.

**Geometry:** simplified with `ST_SimplifyPreserveTopology(geom, 0.002)`,
roughly 200 m. `ST_SimplifyPreserveTopology` rather than `ST_Simplify` because
plain simplification can emit self-intersecting rings, which `area.Import`'s
`validateGeometry` rejects. Precision far exceeds what point-in-polygon on a
sensor coordinate requires.

**Slugs** are transliterated from `name_bg` to ASCII. They appear in URLs
(`/oblast/{slug}`), so they must be stable: changing a slug breaks every
inbound link and every search-engine result for that page. Treat them as
permanent once shipped.
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
go test ./internal/area/ -run 'TestCommittedBoundaries|TestSofiaSensorResolves|TestBoundariesDoNotSwap' -v -count=1
```

Expected: PASS, all three.

- [ ] **Step 8: Commit**

```bash
git add data/boundaries/ internal/area/committed_boundaries_test.go
git commit -m "feat(data): add oblast, city and Sofia district boundaries"
```

---

## Task 3: Area aggregate and series queries

The only database work the API does. Two shapes: an aggregate per area for the choropleth tiers, and a time series for one sensor or one area.

**Files:**
- Create: `internal/store/aggregate.go`
- Create: `internal/store/aggregate_test.go`

**Interfaces:**
- Consumes: `store.New(pool *pgxpool.Pool) *Store` — existing.
- Produces:
  - `const CoverageThreshold = 3`
  - `type AreaAggregate struct { Slug, Kind, NameBG, NameEN string; CentroidLon, CentroidLat float64; DefaultZoom int; SensorCount int; Values map[string]float64; Covered bool }`
  - `func (s *Store) AreaAggregates(ctx context.Context, kinds []string) ([]AreaAggregate, error)`
  - `type SensorReading struct { SensorID int64; SensorType string; Lon, Lat float64; AreaSlugs []string; Quality string; Values map[string]float64 }`
  - `func (s *Store) LatestSensors(ctx context.Context) ([]SensorReading, error)`
  - `type Point struct { Time time.Time; Value float64 }`
  - `func (s *Store) SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool) ([]Point, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/store/aggregate_test.go`:

```go
package store_test

import (
	"testing"
	"time"

	"airbg.org/internal/store"
)

// seedArea inserts one area with a square polygon around the given centre.
// Parameterised, like every query in this project — no string concatenation,
// including in test helpers.
func seedArea(t *testing.T, ctx contextT, pool poolT, slug, kind string, lon, lat float64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ($1, $2, $1, $1,
		         ST_Buffer(ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, 5000)::geography)`,
		slug, kind, lon, lat)
	if err != nil {
		t.Fatalf("seed area %s: %v", slug, err)
	}
}

// TestAreaAggregatesRespectsCoverageThreshold is the central test of this task.
// Two sensors must NOT produce a published average, three must. Phase 1 §5.7:
// below the threshold, deeper tiers manufacture confident-looking averages from
// single sensors.
func TestAreaAggregatesRespectsCoverageThreshold(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	seedArea(t, ctx, pool, "two-sensors", "oblast", 23.0, 42.0)
	seedArea(t, ctx, pool, "three-sensors", "oblast", 25.0, 43.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 1, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 2, 23.001, 42.001, "P2", 20, "ok", now)

	seedSensorReading(t, ctx, pool, 3, 25.0, 43.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 4, 25.001, 43.001, "P2", 20, "ok", now)
	seedSensorReading(t, ctx, pool, 5, 25.002, 43.002, "P2", 30, "ok", now)

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	byslug := map[string]store.AreaAggregate{}
	for _, a := range aggs {
		byslug[a.Slug] = a
	}

	two, ok := byslug["two-sensors"]
	if !ok {
		t.Fatal("two-sensors area missing from aggregates; an under-covered area must still be listed, so the map can render its insufficient-coverage state")
	}
	if two.Covered {
		t.Errorf("two-sensors Covered = true with 2 sensors; CoverageThreshold is %d", store.CoverageThreshold)
	}
	if len(two.Values) != 0 {
		t.Errorf("two-sensors published values %v; an uncovered area must publish no number at all", two.Values)
	}

	three, ok := byslug["three-sensors"]
	if !ok {
		t.Fatal("three-sensors area missing from aggregates")
	}
	if !three.Covered {
		t.Error("three-sensors Covered = false with 3 sensors; the threshold is a minimum, not an exclusive bound")
	}
	if got := three.Values["P2"]; got < 19.9 || got > 20.1 {
		t.Errorf("three-sensors P2 = %v, want 20 (mean of 10, 20, 30)", got)
	}
}

// TestAreaAggregatesExcludesFlaggedReadings asserts the quality filter. Written
// so it fails if the filter is dropped: the flagged value is far enough from
// the good ones that including it moves the mean well outside tolerance.
func TestAreaAggregatesExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	seedArea(t, ctx, pool, "flagged", "oblast", 23.0, 42.0)

	now := time.Now().UTC().Truncate(time.Minute)
	seedSensorReading(t, ctx, pool, 10, 23.0, 42.0, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 11, 23.001, 42.001, "P2", 10, "ok", now)
	seedSensorReading(t, ctx, pool, 12, 23.002, 42.002, "P2", 10, "no_neighbours", now)
	// A stuck sensor reporting 1000: if the filter is missing, the mean jumps
	// from 10 to ~257 and the assertion below fails loudly.
	seedSensorReading(t, ctx, pool, 13, 23.003, 42.003, "P2", 1000, "stuck", now)

	assignAreas(t, ctx, pool)

	aggs, err := s.AreaAggregates(ctx, []string{"oblast"})
	if err != nil {
		t.Fatalf("AreaAggregates: %v", err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1", len(aggs))
	}
	// 'ok' and 'no_neighbours' are usable, 'stuck' is not — three usable
	// sensors, all reading 10.
	if got := aggs[0].Values["P2"]; got < 9.9 || got > 10.1 {
		t.Errorf("P2 = %v, want 10; a value near 257 means the quality filter is missing", got)
	}
	if aggs[0].SensorCount != 3 {
		t.Errorf("SensorCount = %d, want 3 (the stuck sensor must not count toward coverage either)", aggs[0].SensorCount)
	}
}

// TestSensorSeriesUsesRawBelowThirtyDays and its hourly counterpart pin the
// table-selection rule from Phase 1 §7.2. Getting it backwards is silent: both
// tables answer the query, one just returns nothing useful.
func TestSensorSeriesUsesRawBelowThirtyDays(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	now := time.Now().UTC().Truncate(time.Hour)
	seedSensorReading(t, ctx, pool, 20, 23.0, 42.0, "P2", 12, "ok", now.Add(-2*time.Hour))

	// A DIFFERENT value in reading_hourly for the same sensor and hour. If
	// SensorSeries reads the wrong table, it returns 99 and this fails.
	_, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES ($1, 20, 'P2', 99, 99, 99, 1)`,
		now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("seed hourly: %v", err)
	}

	pts, err := s.SensorSeries(ctx, 20, "P2", now.Add(-24*time.Hour), false)
	if err != nil {
		t.Fatalf("SensorSeries: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	if pts[0].Value != 12 {
		t.Errorf("value = %v, want 12; 99 means it read reading_hourly instead of reading", pts[0].Value)
	}
}

func TestSensorSeriesUsesHourlyAboveThirtyDays(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	now := time.Now().UTC().Truncate(time.Hour)
	bucket := now.Add(-60 * 24 * time.Hour)

	seedSensor(t, ctx, pool, 21, 23.0, 42.0)
	_, err := pool.Exec(ctx,
		`INSERT INTO reading_hourly (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 VALUES ($1, 21, 'P2', 42, 40, 44, 6)`,
		bucket)
	if err != nil {
		t.Fatalf("seed hourly: %v", err)
	}

	pts, err := s.SensorSeries(ctx, 21, "P2", bucket.Add(-time.Hour), true)
	if err != nil {
		t.Fatalf("SensorSeries: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("got %d points, want 1", len(pts))
	}
	if pts[0].Value != 42 {
		t.Errorf("value = %v, want 42", pts[0].Value)
	}
}

// TestLatestSensorsReturnsOneRowPerSensor guards against the classic
// join-fanout bug: seven metrics per sensor must produce one SensorReading with
// seven values, not seven SensorReadings.
func TestLatestSensorsReturnsOneRowPerSensor(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	now := time.Now().UTC().Truncate(time.Minute)
	for _, m := range []struct{ name string; v float64 }{
		{"P1", 30}, {"P2", 18}, {"temperature", 21}, {"humidity", 55},
	} {
		seedSensorReading(t, ctx, pool, 30, 23.0, 42.0, m.name, m.v, "ok", now)
	}

	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		t.Fatalf("LatestSensors: %v", err)
	}
	if len(sensors) != 1 {
		t.Fatalf("got %d sensors, want 1 (4 metrics on one sensor must not fan out into 4 rows)", len(sensors))
	}
	if len(sensors[0].Values) != 4 {
		t.Errorf("got %d values, want 4: %v", len(sensors[0].Values), sensors[0].Values)
	}
	if got := sensors[0].Values["P2"]; got != 18 {
		t.Errorf("P2 = %v, want 18", got)
	}
}
```

Add these helpers to the same file:

```go
// The two type aliases keep the helper signatures short; contextT and poolT are
// declared once here rather than repeating the full types in every helper.
type contextT = context.Context
type poolT = *pgxpool.Pool

func seedSensor(t *testing.T, ctx contextT, pool poolT, id int64, lon, lat float64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, 'TEST', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)
		 ON CONFLICT (sensor_id) DO NOTHING`,
		id, lon, lat)
	if err != nil {
		t.Fatalf("seed sensor %d: %v", id, err)
	}
}

func seedSensorReading(t *testing.T, ctx contextT, pool poolT, id int64, lon, lat float64, metric string, value float64, quality string, at time.Time) {
	t.Helper()
	seedSensor(t, ctx, pool, id, lon, lat)
	_, err := pool.Exec(ctx,
		`INSERT INTO reading (time, sensor_id, metric, value, quality)
		 VALUES ($1, $2, $3, $4, $5::quality_flag)
		 ON CONFLICT (sensor_id, metric, time) DO UPDATE
		   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
		at, id, metric, value, quality)
	if err != nil {
		t.Fatalf("seed reading %d/%s: %v", id, metric, err)
	}
}

func assignAreas(t *testing.T, ctx contextT, pool poolT) {
	t.Helper()
	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}
}

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool
}
```

Required imports for the test file: `context`, `testing`, `time`, `github.com/jackc/pgx/v5/pgxpool`, `airbg.org/internal/area`, `airbg.org/internal/db`, `airbg.org/internal/store`, `airbg.org/internal/testsupport`.

Note: `internal/store` already has a `store_test.go` and `rollup_test.go`. If either already declares `migrated`, reuse the existing one and omit it here — duplicate declarations in one package will not compile.

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/store/ -run 'TestAreaAggregates|TestSensorSeries|TestLatestSensors' -v
```

Expected: FAIL to compile — `undefined: store.AreaAggregates`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/aggregate.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"
)

// CoverageThreshold is the minimum number of distinct sensors with usable
// readings an area needs before it publishes an aggregate number (Phase 1
// §5.7). Below it the area still appears — the map must be able to render an
// insufficient-coverage state — but carries no value.
//
// Three, not one, because a "regional average" derived from a single sensor is
// not an average. It is one sensor's reading with a region's name on it, and it
// looks exactly as authoritative as a real one.
const CoverageThreshold = 3

// usableQuality is the quality filter every published aggregate applies.
// 'no_neighbours' is usable: it records that the spatial-outlier check had
// nothing to compare against, not that the reading failed it. Excluding it
// would silently drop every rural sensor.
var usableQuality = []string{"ok", "no_neighbours"}

// freshnessWindow bounds how old a reading may be and still count toward a
// "current" aggregate. Two hours tolerates a missed poll or two without
// letting a sensor that died last week keep contributing to the number on the
// map — which is the more dangerous failure, because a stale value looks
// current.
const freshnessWindow = 2 * time.Hour

type AreaAggregate struct {
	Slug        string
	Kind        string
	NameBG      string
	NameEN      string
	CentroidLon float64
	CentroidLat float64
	DefaultZoom int
	// SensorCount counts distinct sensors with a usable, fresh reading — the
	// number the coverage threshold is applied to, not the total inside the
	// polygon.
	SensorCount int
	Values      map[string]float64
	Covered     bool
}

const areaAggregateSQL = `
WITH latest AS (
    SELECT DISTINCT ON (r.sensor_id, r.metric)
           r.sensor_id, r.metric, r.value
      FROM reading r
     WHERE r.time >= $1
       AND r.quality = ANY($2::quality_flag[])
     ORDER BY r.sensor_id, r.metric, r.time DESC
),
per_area AS (
    SELECT a.slug, l.metric,
           avg(l.value)               AS avg_value,
           count(DISTINCT l.sensor_id) AS sensors
      FROM area a
      JOIN area_sensor asx ON asx.area_slug = a.slug
      JOIN latest l        ON l.sensor_id = asx.sensor_id
     WHERE a.kind = ANY($3::text[])
     GROUP BY a.slug, l.metric
),
coverage AS (
    SELECT a.slug, count(DISTINCT asx.sensor_id) AS sensors
      FROM area a
      JOIN area_sensor asx ON asx.area_slug = a.slug
      JOIN latest l        ON l.sensor_id = asx.sensor_id
     WHERE a.kind = ANY($3::text[])
     GROUP BY a.slug
)
SELECT a.slug, a.kind, a.name_bg, a.name_en,
       ST_X(a.centroid::geometry), ST_Y(a.centroid::geometry), a.default_zoom,
       COALESCE(c.sensors, 0),
       COALESCE(
           (SELECT jsonb_object_agg(p.metric, round(p.avg_value::numeric, 2))
              FROM per_area p WHERE p.slug = a.slug),
           '{}'::jsonb)
  FROM area a
  LEFT JOIN coverage c ON c.slug = a.slug
 WHERE a.kind = ANY($3::text[])
 ORDER BY a.slug`

// AreaAggregates returns one row per area of the requested kinds, including
// areas with no sensors at all. Areas below CoverageThreshold come back with
// Covered false and an empty Values map — the filtering happens here, once, so
// no handler can forget it.
//
// kinds is passed as a bound text[] parameter, never interpolated. A slug or
// kind reaching SQL as text is the legacy application's injection bug.
func (s *Store) AreaAggregates(ctx context.Context, kinds []string) ([]AreaAggregate, error) {
	since := time.Now().UTC().Add(-freshnessWindow)

	rows, err := s.pool.Query(ctx, areaAggregateSQL, since, usableQuality, kinds)
	if err != nil {
		return nil, fmt.Errorf("store: area aggregates: %w", err)
	}
	defer rows.Close()

	var out []AreaAggregate
	for rows.Next() {
		var a AreaAggregate
		var values map[string]float64
		if err := rows.Scan(&a.Slug, &a.Kind, &a.NameBG, &a.NameEN,
			&a.CentroidLon, &a.CentroidLat, &a.DefaultZoom,
			&a.SensorCount, &values); err != nil {
			return nil, fmt.Errorf("store: scan area aggregate: %w", err)
		}
		a.Covered = a.SensorCount >= CoverageThreshold
		if a.Covered {
			a.Values = values
		} else {
			// Explicitly empty, not the scanned map. An uncovered area must
			// carry no number anywhere downstream — a handler that checked
			// Covered but serialised Values anyway would leak it.
			a.Values = map[string]float64{}
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: area aggregates rows: %w", err)
	}
	return out, nil
}

type SensorReading struct {
	SensorID   int64
	SensorType string
	Lon        float64
	Lat        float64
	AreaSlugs  []string
	Quality    string
	Values     map[string]float64
}

const latestSensorsSQL = `
WITH latest AS (
    SELECT DISTINCT ON (r.sensor_id, r.metric)
           r.sensor_id, r.metric, r.value, r.quality
      FROM reading r
     WHERE r.time >= $1
     ORDER BY r.sensor_id, r.metric, r.time DESC
)
SELECT s.sensor_id, s.sensor_type,
       ST_X(s.location::geometry), ST_Y(s.location::geometry),
       COALESCE(
           (SELECT array_agg(asx.area_slug ORDER BY asx.area_slug)
              FROM area_sensor asx WHERE asx.sensor_id = s.sensor_id),
           ARRAY[]::text[]),
       -- The worst flag on any of this sensor's metrics, so one bad metric
       -- marks the sensor rather than being averaged away. 'ok' sorts first,
       -- so max() over the text form picks any non-ok flag.
       COALESCE(max(l.quality::text) FILTER (WHERE l.quality <> 'ok'), 'ok'),
       jsonb_object_agg(l.metric, round(l.value::numeric, 2))
           FILTER (WHERE l.quality = ANY($2::quality_flag[]))
  FROM sensor s
  JOIN latest l ON l.sensor_id = s.sensor_id
 GROUP BY s.sensor_id, s.sensor_type, s.location
 ORDER BY s.sensor_id`

// LatestSensors returns one row per sensor with a fresh reading, carrying every
// usable metric value. Grouping happens in SQL: the naive join returns one row
// per sensor-metric pair, and a caller assembling those in Go is one forgotten
// map lookup away from emitting seven markers where one belongs.
func (s *Store) LatestSensors(ctx context.Context) ([]SensorReading, error) {
	since := time.Now().UTC().Add(-freshnessWindow)

	rows, err := s.pool.Query(ctx, latestSensorsSQL, since, usableQuality)
	if err != nil {
		return nil, fmt.Errorf("store: latest sensors: %w", err)
	}
	defer rows.Close()

	var out []SensorReading
	for rows.Next() {
		var sr SensorReading
		var values map[string]float64
		if err := rows.Scan(&sr.SensorID, &sr.SensorType, &sr.Lon, &sr.Lat,
			&sr.AreaSlugs, &sr.Quality, &values); err != nil {
			return nil, fmt.Errorf("store: scan sensor: %w", err)
		}
		if values == nil {
			values = map[string]float64{}
		}
		sr.Values = values
		out = append(out, sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: latest sensors rows: %w", err)
	}
	return out, nil
}

type Point struct {
	Time  time.Time
	Value float64
}

const rawSeriesSQL = `
SELECT time, value FROM reading
 WHERE sensor_id = $1 AND metric = $2 AND time >= $3
   AND quality = ANY($4::quality_flag[])
 ORDER BY time`

const hourlySeriesSQL = `
SELECT bucket, avg_value FROM reading_hourly
 WHERE sensor_id = $1 AND metric = $2 AND bucket >= $3
 ORDER BY bucket`

// SensorSeries returns a time series for one sensor and metric. hourly selects
// reading_hourly instead of reading.
//
// The caller decides which table, because the rule is a property of the
// requested period, not of the data: raw readings are retained 30 days
// (migration 00003), so any window reaching further back must come from
// reading_hourly or it silently returns a truncated series that looks complete.
func (s *Store) SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool) ([]Point, error) {
	var rows interface {
		Next() bool
		Scan(...any) error
		Err() error
		Close()
	}
	var err error

	if hourly {
		rows, err = s.pool.Query(ctx, hourlySeriesSQL, sensorID, metric, since)
	} else {
		rows, err = s.pool.Query(ctx, rawSeriesSQL, sensorID, metric, since, usableQuality)
	}
	if err != nil {
		return nil, fmt.Errorf("store: sensor series: %w", err)
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, fmt.Errorf("store: scan point: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: sensor series rows: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/store/ -run 'TestAreaAggregates|TestSensorSeries|TestLatestSensors' -v -count=1
```

Expected: PASS, all five.

- [ ] **Step 5: Prove the coverage-threshold test is not a tautology**

Temporarily change `a.Covered = a.SensorCount >= CoverageThreshold` to `a.Covered = true`, re-run, confirm `TestAreaAggregatesRespectsCoverageThreshold` FAILS, then revert. Do the same for the quality filter: remove `AND r.quality = ANY($2::quality_flag[])` from `areaAggregateSQL` and confirm `TestAreaAggregatesExcludesFlaggedReadings` fails on the mean. Revert both.

- [ ] **Step 6: Commit**

```bash
git add internal/store/aggregate.go internal/store/aggregate_test.go
git commit -m "feat(store): add area aggregate, latest-sensor and series queries"
```

---

## Task 4: Snapshot package

The immutable value every memory-backed endpoint is served from. Built once per ingest cycle, published with one atomic store, never mutated afterwards.

**Files:**
- Create: `internal/snapshot/snapshot.go`
- Create: `internal/snapshot/build.go`
- Create: `internal/snapshot/snapshot_test.go`
- Create: `internal/snapshot/build_test.go`

**Interfaces:**
- Consumes: `store.AreaAggregates(ctx, kinds []string) ([]store.AreaAggregate, error)`, `store.LatestSensors(ctx) ([]store.SensorReading, error)`, `store.CoverageThreshold`.
- Produces:
  - `type Body struct { JSON []byte; Gzip []byte; ETag string }`
  - `type Snapshot struct { GeneratedAt time.Time; Overview Body; OverviewCity Body; Areas Body; AreaSensors map[string]Body; KnownSlugs map[string]AreaMeta }`
  - `type AreaMeta struct { Slug, Kind, NameBG, NameEN string; CentroidLon, CentroidLat float64; DefaultZoom int; Covered bool; SensorCount int }`
  - `type Holder struct { ... }`, `func NewHolder() *Holder`, `func (h *Holder) Load() *Snapshot`, `func (h *Holder) Store(s *Snapshot)`
  - `func Build(ctx context.Context, s *store.Store, now time.Time) (*Snapshot, error)`

- [ ] **Step 1: Write the failing test for the holder**

Create `internal/snapshot/snapshot_test.go`:

```go
package snapshot_test

import (
	"sync"
	"testing"
	"time"

	"airbg.org/internal/snapshot"
)

// TestHolderReturnsNilBeforeFirstStore pins the 503 precondition. A holder that
// returned an empty &Snapshot{} instead of nil would let handlers serve an empty
// country as though it had been measured — the "reports success while storing
// nothing" failure this project keeps guarding against.
func TestHolderReturnsNilBeforeFirstStore(t *testing.T) {
	h := snapshot.NewHolder()
	if got := h.Load(); got != nil {
		t.Fatalf("Load() = %+v before any Store, want nil", got)
	}
}

// TestHolderIsRaceFree is run under -race. Concurrent readers during a publish
// is the actual production pattern: the ingest goroutine stores while every
// in-flight request loads.
func TestHolderIsRaceFree(t *testing.T) {
	h := snapshot.NewHolder()
	h.Store(&snapshot.Snapshot{GeneratedAt: time.Unix(1, 0)})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if s := h.Load(); s == nil {
					t.Error("Load() returned nil after a Store")
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 500; j++ {
			h.Store(&snapshot.Snapshot{GeneratedAt: time.Unix(int64(j+2), 0)})
		}
	}()
	wg.Wait()
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/snapshot/ -race -v
```

Expected: FAIL to build — no such package.

- [ ] **Step 3: Write the holder**

Create `internal/snapshot/snapshot.go`:

```go
// Package snapshot holds the precomputed API responses that the collector
// rebuilds once per ingest cycle.
//
// The type is immutable by convention: Build constructs a Snapshot, Holder
// publishes it, and nothing mutates one afterwards. That is what makes the read
// path lock-free — a reader holding a *Snapshot cannot observe a torn or
// half-updated value, because the pointer swap is atomic and the pointee never
// changes.
package snapshot

import (
	"sync/atomic"
	"time"
)

// Body is one fully prepared HTTP response body: the JSON, its gzip encoding,
// and its ETag. All three are computed once at build time.
//
// Gzipping at build time rather than per request matters more than it looks:
// /overview is requested by every visitor and its content changes at most once
// every five minutes, so compressing it per request would burn CPU recomputing
// an identical result thousands of times.
type Body struct {
	JSON []byte
	Gzip []byte
	ETag string
}

// AreaMeta is the non-payload metadata a handler needs about an area: enough to
// validate a slug, resolve /locate, and render a page header, without going to
// the database.
type AreaMeta struct {
	Slug        string
	Kind        string
	NameBG      string
	NameEN      string
	CentroidLon float64
	CentroidLat float64
	DefaultZoom int
	Covered     bool
	SensorCount int
}

type Snapshot struct {
	GeneratedAt time.Time

	// Overview is the country tier (oblast aggregates). OverviewCity is the
	// regional tier (city and neighbourhood aggregates).
	Overview     Body
	OverviewCity Body
	Areas        Body

	// AreaSensors is keyed by area slug. Present for every known slug, even
	// one with no sensors — a missing key must mean "no such area" (404) and
	// never "this area happens to be empty" (200 with an empty list).
	AreaSensors map[string]Body

	// KnownSlugs is the validation set for {slug} path parameters. Validating
	// against it means no caller-supplied slug ever reaches a query.
	KnownSlugs map[string]AreaMeta
}

// Holder publishes snapshots to concurrent readers.
type Holder struct {
	ptr atomic.Pointer[Snapshot]
}

func NewHolder() *Holder { return &Holder{} }

// Load returns the current snapshot, or nil if none has been built yet.
// Callers must treat nil as "not ready" and answer 503 — never as an empty
// dataset.
func (h *Holder) Load() *Snapshot { return h.ptr.Load() }

func (h *Holder) Store(s *Snapshot) { h.ptr.Store(s) }
```

- [ ] **Step 4: Run the holder tests**

```bash
go test ./internal/snapshot/ -race -v
```

Expected: PASS.

- [ ] **Step 5: Write the failing test for Build**

Create `internal/snapshot/build_test.go`:

```go
package snapshot_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/db"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
)

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return ctx, pool
}

// seed inserts one oblast with three usable sensors, which is exactly
// CoverageThreshold — enough to publish.
func seed(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('sofia', 'oblast', 'София', 'Sofia',
		         ST_Buffer(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 20000)::geography)`,
		23.3219, 42.6977)
	if err != nil {
		t.Fatalf("seed area: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	for i, v := range []float64{10, 20, 30} {
		id := int64(100 + i)
		_, err := pool.Exec(ctx,
			`INSERT INTO sensor (sensor_id, sensor_type, location)
			 VALUES ($1, 'SDS011', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)`,
			id, 23.3219+float64(i)*0.001, 42.6977)
		if err != nil {
			t.Fatalf("seed sensor: %v", err)
		}
		_, err = pool.Exec(ctx,
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, 'P2', $3, 'ok')`,
			now, id, v)
		if err != nil {
			t.Fatalf("seed reading: %v", err)
		}
	}
	if _, _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}
}

func TestBuildProducesValidJSONAndMatchingGzip(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	snap, err := snapshot.Build(ctx, store.New(pool), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// The gzip body must decompress to exactly the JSON body. Two independently
	// produced representations of the same response is precisely the kind of
	// thing that drifts, and a client sent mismatched bytes under an ETag that
	// claims they are the same resource has no way to detect it.
	for name, b := range map[string]snapshot.Body{
		"overview":      snap.Overview,
		"overview_city": snap.OverviewCity,
		"areas":         snap.Areas,
	} {
		if !json.Valid(b.JSON) {
			t.Errorf("%s: JSON is not valid: %s", name, b.JSON)
		}
		zr, err := gzip.NewReader(bytes.NewReader(b.Gzip))
		if err != nil {
			t.Fatalf("%s: gzip reader: %v", name, err)
		}
		got, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("%s: gzip read: %v", name, err)
		}
		if !bytes.Equal(got, b.JSON) {
			t.Errorf("%s: gzip body does not decompress to the JSON body", name)
		}
		if b.ETag == "" {
			t.Errorf("%s: ETag is empty", name)
		}
	}
}

// TestBuildETagsDifferPerBody is the bug this design deliberately avoids:
// deriving ETags from GeneratedAt would give every tier built in the same cycle
// the same ETag, so a client that had fetched /overview would get a spurious 304
// for /overview?tier=city and render the wrong tier's data.
func TestBuildETagsDifferPerBody(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	snap, err := snapshot.Build(ctx, store.New(pool), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.Overview.ETag == snap.OverviewCity.ETag {
		t.Error("Overview and OverviewCity share an ETag; they must be hashed per body, not from GeneratedAt")
	}
	if snap.Overview.ETag == snap.Areas.ETag {
		t.Error("Overview and Areas share an ETag")
	}
}

// TestBuildETagIsStableForIdenticalData asserts the other half: identical data
// must yield an identical ETag, or every cycle invalidates every cache even when
// nothing changed, and the edge cache becomes useless.
func TestBuildETagIsStableForIdenticalData(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	s := store.New(pool)
	a, err := snapshot.Build(ctx, s, time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build a: %v", err)
	}
	// A DIFFERENT build time, same data. GeneratedAt is excluded from the hash
	// for exactly this reason.
	b, err := snapshot.Build(ctx, s, time.Unix(1_800_000_300, 0).UTC())
	if err != nil {
		t.Fatalf("Build b: %v", err)
	}
	if a.Overview.ETag != b.Overview.ETag {
		t.Errorf("ETag changed between builds of identical data (%s vs %s); GeneratedAt must not be hashed", a.Overview.ETag, b.Overview.ETag)
	}
}

// TestBuildIncludesEmptyAreasInAreaSensors: a known area with no sensors must
// have an AreaSensors entry, so the handler can distinguish 404 (no such area)
// from 200-with-nothing-in-it. Collapsing those two is how "this region has no
// data" gets served as "this region does not exist".
func TestBuildIncludesEmptyAreasInAreaSensors(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ('empty-oblast', 'oblast', 'Празна', 'Empty',
		         ST_Buffer(ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 5000)::geography)`,
		26.0, 43.5)
	if err != nil {
		t.Fatalf("seed empty area: %v", err)
	}

	snap, err := snapshot.Build(ctx, store.New(pool), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, ok := snap.AreaSensors["empty-oblast"]; !ok {
		t.Error("AreaSensors has no entry for an existing but sensor-less area; the handler cannot then tell 404 from an empty 200")
	}
	if _, ok := snap.KnownSlugs["empty-oblast"]; !ok {
		t.Error("KnownSlugs is missing an existing area")
	}
	if meta := snap.KnownSlugs["empty-oblast"]; meta.Covered {
		t.Error("an area with no sensors reports Covered = true")
	}
}

// TestBuildSensorPayloadIsColumnar pins the wire format from Phase 1 §7.3.
// Phase 3's MapLibre layer consumes typed arrays; a silent switch to
// row-per-sensor would break it at runtime, not at compile time.
func TestBuildSensorPayloadIsColumnar(t *testing.T) {
	ctx, pool := migrated(t)
	seed(t, ctx, pool)

	snap, err := snapshot.Build(ctx, store.New(pool), time.Unix(1_800_000_000, 0).UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	body, ok := snap.AreaSensors["sofia"]
	if !ok {
		t.Fatal("no AreaSensors entry for sofia")
	}

	var got struct {
		GeneratedAt string `json:"generated_at"`
		Sensors     struct {
			ID      []int64   `json:"id"`
			Type    []string  `json:"type"`
			Lon     []float64 `json:"lon"`
			Lat     []float64 `json:"lat"`
			Quality []string  `json:"quality"`
			P2      []*float64 `json:"P2"`
		} `json:"sensors"`
	}
	if err := json.Unmarshal(body.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, body.JSON)
	}
	if n := len(got.Sensors.ID); n != 3 {
		t.Fatalf("got %d sensor ids, want 3", n)
	}
	for name, n := range map[string]int{
		"type": len(got.Sensors.Type), "lon": len(got.Sensors.Lon),
		"lat": len(got.Sensors.Lat), "quality": len(got.Sensors.Quality),
		"P2": len(got.Sensors.P2),
	} {
		if n != 3 {
			t.Errorf("column %q has length %d, want 3 — every column must be the same length or the arrays do not line up", name, n)
		}
	}
	// Longitude near 23 and latitude near 42. Asserted separately, because
	// equal-length columns would happily hold swapped values.
	if got.Sensors.Lon[0] < 23 || got.Sensors.Lon[0] > 24 {
		t.Errorf("lon[0] = %v, want ~23.3 (a value near 42 means lon/lat are swapped)", got.Sensors.Lon[0])
	}
	if got.Sensors.Lat[0] < 42 || got.Sensors.Lat[0] > 43 {
		t.Errorf("lat[0] = %v, want ~42.7", got.Sensors.Lat[0])
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

```bash
go test ./internal/snapshot/ -run TestBuild -v
```

Expected: FAIL to compile — `undefined: snapshot.Build`.

- [ ] **Step 7: Write Build**

Create `internal/snapshot/build.go`:

```go
package snapshot

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// countryKinds and cityKinds define the two choropleth tiers from Phase 1 §7.1.
// The country tier is oblasti only — 28 shapes, ~4 KB. The regional tier adds
// cities and Sofia's districts.
var (
	countryKinds = []string{"oblast"}
	cityKinds    = []string{"city", "neighbourhood"}
)

// areaPayload is the choropleth wire format: one entry per area, aggregate
// values only, and — deliberately — no sensor coordinates. That omission is the
// anti-extraction property from Phase 1 §7.1: the low-zoom response that every
// visitor fetches cannot be assembled into a sensor list.
type areaPayload struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Areas       []areaPayloadEntry `json:"areas"`
}

type areaPayloadEntry struct {
	Slug        string             `json:"slug"`
	Kind        string             `json:"kind"`
	NameBG      string             `json:"name_bg"`
	NameEN      string             `json:"name_en"`
	Lon         float64            `json:"lon"`
	Lat         float64            `json:"lat"`
	Zoom        int                `json:"zoom"`
	SensorCount int                `json:"sensor_count"`
	Covered     bool               `json:"covered"`
	Values      map[string]float64 `json:"values"`
}

// sensorPayload is columnar (Phase 1 §7.3): each field named once, values in
// parallel arrays. Roughly 40 % smaller than row-per-sensor before compression,
// gzips better because same-typed values are adjacent, and it is the shape
// MapLibre's typed arrays want.
type sensorPayload struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Sensors     sensorColumns  `json:"sensors"`
}

type sensorColumns struct {
	ID      []int64    `json:"id"`
	Type    []string   `json:"type"`
	Lon     []float64  `json:"lon"`
	Lat     []float64  `json:"lat"`
	Quality []string   `json:"quality"`
	// Metrics holds one column per canonical metric, each the same length as
	// ID. A nil entry means that sensor does not report that metric — which is
	// distinct from reporting zero, and must stay distinct: 0 µg/m³ is a
	// reading, absence is not.
	Metrics map[string][]*float64 `json:"-"`
}

// MarshalJSON flattens Metrics into sibling keys of the fixed columns, so the
// wire format is {"id":[…],"lon":[…],"P1":[…],"P2":[…]} rather than nesting the
// metrics under another object. Phase 1 §7.3's example payload has them as
// siblings, and Phase 3 reads them that way.
func (c sensorColumns) MarshalJSON() ([]byte, error) {
	out := map[string]any{
		"id":      c.ID,
		"type":    c.Type,
		"lon":     c.Lon,
		"lat":     c.Lat,
		"quality": c.Quality,
	}
	for metric, col := range c.Metrics {
		out[metric] = col
	}
	return json.Marshal(out)
}

// Build reads everything the memory-backed endpoints need and prepares each
// response completely: JSON, gzip, ETag.
//
// now is passed in rather than read from the clock so a test can build twice
// with different timestamps and assert the ETag did not move.
func Build(ctx context.Context, s *store.Store, now time.Time) (*Snapshot, error) {
	countryAggs, err := s.AreaAggregates(ctx, countryKinds)
	if err != nil {
		return nil, fmt.Errorf("snapshot: country tier: %w", err)
	}
	cityAggs, err := s.AreaAggregates(ctx, cityKinds)
	if err != nil {
		return nil, fmt.Errorf("snapshot: city tier: %w", err)
	}
	sensors, err := s.LatestSensors(ctx)
	if err != nil {
		return nil, fmt.Errorf("snapshot: sensors: %w", err)
	}

	all := make([]store.AreaAggregate, 0, len(countryAggs)+len(cityAggs))
	all = append(all, countryAggs...)
	all = append(all, cityAggs...)

	snap := &Snapshot{
		GeneratedAt: now,
		AreaSensors: make(map[string]Body, len(all)),
		KnownSlugs:  make(map[string]AreaMeta, len(all)),
	}

	for _, a := range all {
		snap.KnownSlugs[a.Slug] = AreaMeta{
			Slug: a.Slug, Kind: a.Kind, NameBG: a.NameBG, NameEN: a.NameEN,
			CentroidLon: a.CentroidLon, CentroidLat: a.CentroidLat,
			DefaultZoom: a.DefaultZoom, Covered: a.Covered, SensorCount: a.SensorCount,
		}
	}

	if snap.Overview, err = encode(areaPayloadFrom(now, countryAggs)); err != nil {
		return nil, fmt.Errorf("snapshot: encode overview: %w", err)
	}
	if snap.OverviewCity, err = encode(areaPayloadFrom(now, cityAggs)); err != nil {
		return nil, fmt.Errorf("snapshot: encode city overview: %w", err)
	}
	if snap.Areas, err = encode(areaPayloadFrom(now, all)); err != nil {
		return nil, fmt.Errorf("snapshot: encode areas: %w", err)
	}

	// Group sensors by area. A sensor in three nested areas appears in three
	// entries; that is correct, since each is a separate response.
	bySlug := make(map[string][]store.SensorReading, len(all))
	for _, sr := range sensors {
		for _, slug := range sr.AreaSlugs {
			bySlug[slug] = append(bySlug[slug], sr)
		}
	}
	// Iterate the known areas, not bySlug, so every existing area gets an
	// entry — including empty ones. See TestBuildIncludesEmptyAreasInAreaSensors.
	for slug := range snap.KnownSlugs {
		body, err := encode(sensorPayloadFrom(now, bySlug[slug]))
		if err != nil {
			return nil, fmt.Errorf("snapshot: encode sensors for %q: %w", slug, err)
		}
		snap.AreaSensors[slug] = body
	}

	return snap, nil
}

func areaPayloadFrom(now time.Time, aggs []store.AreaAggregate) areaPayload {
	p := areaPayload{GeneratedAt: now, Areas: make([]areaPayloadEntry, 0, len(aggs))}
	for _, a := range aggs {
		values := a.Values
		if values == nil {
			values = map[string]float64{}
		}
		p.Areas = append(p.Areas, areaPayloadEntry{
			Slug: a.Slug, Kind: a.Kind, NameBG: a.NameBG, NameEN: a.NameEN,
			Lon: a.CentroidLon, Lat: a.CentroidLat, Zoom: a.DefaultZoom,
			SensorCount: a.SensorCount, Covered: a.Covered, Values: values,
		})
	}
	return p
}

func sensorPayloadFrom(now time.Time, sensors []store.SensorReading) sensorPayload {
	n := len(sensors)
	cols := sensorColumns{
		ID:      make([]int64, 0, n),
		Type:    make([]string, 0, n),
		Lon:     make([]float64, 0, n),
		Lat:     make([]float64, 0, n),
		Quality: make([]string, 0, n),
		Metrics: make(map[string][]*float64),
	}
	// Every canonical metric gets a column of exactly n entries, present or
	// not. A ragged payload — where P2 has 40 entries and pressure has 3 — has
	// no way to say which sensor a value belongs to.
	metrics := upstream.CanonicalMetrics()
	for _, m := range metrics {
		cols.Metrics[m] = make([]*float64, 0, n)
	}

	for _, sr := range sensors {
		cols.ID = append(cols.ID, sr.SensorID)
		cols.Type = append(cols.Type, sr.SensorType)
		cols.Lon = append(cols.Lon, sr.Lon)
		cols.Lat = append(cols.Lat, sr.Lat)
		cols.Quality = append(cols.Quality, sr.Quality)
		for _, m := range metrics {
			if v, ok := sr.Values[m]; ok {
				value := v
				cols.Metrics[m] = append(cols.Metrics[m], &value)
			} else {
				cols.Metrics[m] = append(cols.Metrics[m], nil)
			}
		}
	}
	return sensorPayload{GeneratedAt: now, Sensors: cols}
}

// encode serialises, gzips, and hashes one payload.
//
// The ETag is the SHA-256 of the JSON body with GeneratedAt zeroed out first.
// Hashing the timestamped body would change the ETag every cycle even when no
// value moved, invalidating every cached copy five minutes after it was stored
// — which defeats the edge cache entirely on a dataset that changes slowly.
func encode(payload any) (Body, error) {
	withTime, err := json.Marshal(payload)
	if err != nil {
		return Body{}, err
	}

	etagSource, err := json.Marshal(zeroGeneratedAt(payload))
	if err != nil {
		return Body{}, err
	}
	sum := sha256.Sum256(etagSource)

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return Body{}, err
	}
	if _, err := zw.Write(withTime); err != nil {
		return Body{}, err
	}
	if err := zw.Close(); err != nil {
		return Body{}, err
	}

	return Body{
		JSON: withTime,
		Gzip: buf.Bytes(),
		// Quoted, as RFC 9110 requires. A bare hex string is not a valid
		// entity-tag and intermediaries are free to ignore it.
		ETag: `"` + hex.EncodeToString(sum[:]) + `"`,
	}, nil
}

// zeroGeneratedAt returns a copy of the payload with its timestamp cleared, for
// hashing only. Handled per concrete type rather than by reflection: there are
// exactly two payload types, and a reflective version would silently stop
// working the moment a third is added without a matching case.
func zeroGeneratedAt(payload any) any {
	switch p := payload.(type) {
	case areaPayload:
		p.GeneratedAt = time.Time{}
		return p
	case sensorPayload:
		p.GeneratedAt = time.Time{}
		return p
	default:
		// Unknown payload type: hash it as-is rather than silently returning
		// something that is not the payload. A caller adding a third type gets
		// per-cycle ETag churn, which is visible in cache metrics, rather than
		// a wrong hash.
		return payload
	}
}
```

- [ ] **Step 8: Run the Build tests**

```bash
go test ./internal/snapshot/ -race -v -count=1
```

Expected: PASS, all six.

- [ ] **Step 9: Prove the ETag tests are not tautologies**

Temporarily change `encode` to hash `withTime` instead of `etagSource`, re-run, and confirm `TestBuildETagIsStableForIdenticalData` FAILS. Revert. Then temporarily make `encode` return a constant ETag and confirm `TestBuildETagsDifferPerBody` FAILS. Revert.

- [ ] **Step 10: Commit**

```bash
git add internal/snapshot/
git commit -m "feat(snapshot): precompute, gzip and ETag every memory-backed response"
```

---

## Task 5: Metrics package

Hand-rolled because no third-party dependency may be added. Prometheus text exposition is line-based and small; this is roughly 120 lines.

**Files:**
- Create: `internal/metrics/metrics.go`
- Create: `internal/metrics/metrics_test.go`

**Interfaces:**
- Produces:
  - `func Counter(name, help string) *Count`, `func (c *Count) Inc()`, `func (c *Count) Add(n int64)`, `func (c *Count) Value() int64`
  - `func CounterVec(name, help, label string) *Vec`, `func (v *Vec) With(value string) *Count`
  - `func Gauge(name, help string) *Level` — the constructor is `Gauge`, so the type is named `Level`; `func (g *Level) Set(v float64)`, `func (g *Level) Value() float64`
  - `func Handler() http.Handler`

- [ ] **Step 1: Write the failing test**

Create `internal/metrics/metrics_test.go`:

```go
package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"airbg.org/internal/metrics"
)

func TestCounterExposition(t *testing.T) {
	c := metrics.Counter("airbg_test_total", "A test counter.")
	c.Inc()
	c.Add(4)

	body := scrape(t)

	// Prometheus requires the HELP and TYPE lines before the sample. A scraper
	// tolerates their absence, but the metric then has no documentation and no
	// declared type, which changes how it is aggregated.
	for _, want := range []string{
		"# HELP airbg_test_total A test counter.",
		"# TYPE airbg_test_total counter",
		"airbg_test_total 5",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition missing %q\ngot:\n%s", want, body)
		}
	}
}

func TestCounterVecEscapesLabelValues(t *testing.T) {
	v := metrics.CounterVec("airbg_test_labelled_total", "Labelled.", "route")
	// A label value containing a quote, a backslash and a newline. All three
	// must be escaped or the exposition becomes unparseable — and route labels
	// come from request paths, which are attacker-controlled.
	v.With("/a\"b\\c\nd").Inc()

	body := scrape(t)
	if strings.Contains(body, "\nd") && strings.Contains(body, "airbg_test_labelled_total{route=\"/a\"b") {
		t.Errorf("label value was not escaped:\n%s", body)
	}
	if !strings.Contains(body, `airbg_test_labelled_total{route="/a\"b\\c\nd"} 1`) {
		t.Errorf("expected escaped label line, got:\n%s", body)
	}
}

func TestGaugeSet(t *testing.T) {
	g := metrics.Gauge("airbg_test_gauge", "A gauge.")
	g.Set(42.5)

	body := scrape(t)
	if !strings.Contains(body, "# TYPE airbg_test_gauge gauge") {
		t.Errorf("gauge type line missing:\n%s", body)
	}
	if !strings.Contains(body, "airbg_test_gauge 42.5") {
		t.Errorf("gauge value missing:\n%s", body)
	}
}

// TestConcurrentIncIsRaceFree is run under -race. Counters are incremented from
// every request goroutine, so this is the normal case, not an edge case.
func TestConcurrentIncIsRaceFree(t *testing.T) {
	c := metrics.Counter("airbg_test_concurrent_total", "Concurrent.")
	v := metrics.CounterVec("airbg_test_concurrent_labelled_total", "Concurrent labelled.", "k")

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.Inc()
				// Distinct label values from concurrent goroutines exercise the
				// map-write path, not just the atomic increment.
				v.With(string(rune('a' + i%4))).Inc()
			}
		}(i)
	}
	wg.Wait()

	if got := c.Value(); got != 3200 {
		t.Errorf("counter = %d, want 3200", got)
	}
}

func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/metrics/ -race -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the implementation**

Create `internal/metrics/metrics.go`. Note the exported gauge type is `Level`, not `Gauge` — `Gauge` is the constructor function, so the type needs a different name:

```go
// Package metrics exposes counters and gauges in Prometheus text format.
//
// Hand-rolled deliberately. The project adds no third-party dependency, and the
// exposition format is a handful of lines per metric — importing
// prometheus/client_golang to emit them would pull in protobuf, procfs and
// common for something this file does in 120 lines.
//
// Registration is process-global and happens at package-variable initialisation
// in the packages that own each metric. That mirrors how the standard library
// treats expvar, and it means a metric cannot be forgotten at wiring time: if
// the var exists, it is exposed.
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type kind string

const (
	kindCounter kind = "counter"
	kindGauge   kind = "gauge"
)

type entry struct {
	name  string
	help  string
	kind  kind
	label string // empty for unlabelled metrics

	simple *Count             // unlabelled counter
	level  *Level             // gauge
	vecMu  sync.RWMutex       // guards vec
	vec    map[string]*Count  // labelled counter, keyed by label value
}

var (
	registryMu sync.RWMutex
	registry   []*entry
)

func register(e *entry) *entry {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, e)
	return e
}

// Count is a monotonically increasing counter.
type Count struct{ n atomic.Int64 }

func (c *Count) Inc()          { c.n.Add(1) }
func (c *Count) Add(n int64)   { c.n.Add(n) }
func (c *Count) Value() int64  { return c.n.Load() }

// Level is a gauge: a value that goes up and down.
//
// Stored as the IEEE-754 bits in an atomic uint64 rather than behind a mutex,
// because gauges are set from the ingest goroutine while the scrape goroutine
// reads them, and a torn float64 read on a 32-bit platform is a real (if rare)
// possibility that math.Float64bits + atomic removes for free.
type Level struct{ bits atomic.Uint64 }

func (g *Level) Set(v float64) { g.bits.Store(float64bits(v)) }
func (g *Level) Value() float64 { return float64frombits(g.bits.Load()) }

// Vec is a counter with one label dimension.
type Vec struct{ e *entry }

// With returns the counter for one label value, creating it on first use.
//
// The caller MUST pass a bounded set of values. Label cardinality is the
// classic way a metrics endpoint becomes a memory leak: labelling by raw
// request path or by client IP grows the map without limit, and an attacker can
// then exhaust memory by varying the label. Route labels must be the route
// PATTERN ("/api/v1/area/{slug}/sensors"), never the concrete path.
func (v *Vec) With(value string) *Count {
	v.e.vecMu.RLock()
	c, ok := v.e.vec[value]
	v.e.vecMu.RUnlock()
	if ok {
		return c
	}

	v.e.vecMu.Lock()
	defer v.e.vecMu.Unlock()
	if c, ok := v.e.vec[value]; ok {
		return c
	}
	c = &Count{}
	v.e.vec[value] = c
	return c
}

func Counter(name, help string) *Count {
	c := &Count{}
	register(&entry{name: name, help: help, kind: kindCounter, simple: c})
	return c
}

func CounterVec(name, help, label string) *Vec {
	e := register(&entry{
		name: name, help: help, kind: kindCounter, label: label,
		vec: map[string]*Count{},
	})
	return &Vec{e: e}
}

func Gauge(name, help string) *Level {
	g := &Level{}
	register(&entry{name: name, help: help, kind: kindGauge, level: g})
	return g
}

// Handler serves the exposition. It must be mounted on the PRIVATE listener
// only: metric names and counts describe internal behaviour and request volume,
// which is reconnaissance material for anyone probing the rate limits.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registryMu.RLock()
		snapshot := make([]*entry, len(registry))
		copy(snapshot, registry)
		registryMu.RUnlock()

		var b strings.Builder
		for _, e := range snapshot {
			fmt.Fprintf(&b, "# HELP %s %s\n", e.name, e.help)
			fmt.Fprintf(&b, "# TYPE %s %s\n", e.name, e.kind)

			switch {
			case e.simple != nil:
				fmt.Fprintf(&b, "%s %d\n", e.name, e.simple.Value())
			case e.level != nil:
				fmt.Fprintf(&b, "%s %s\n", e.name,
					strconv.FormatFloat(e.level.Value(), 'g', -1, 64))
			default:
				e.vecMu.RLock()
				keys := make([]string, 0, len(e.vec))
				for k := range e.vec {
					keys = append(keys, k)
				}
				// Sorted so successive scrapes produce identical output for
				// identical state. Map order would make the exposition differ
				// run to run, which makes diffing a scrape useless.
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&b, "%s{%s=\"%s\"} %d\n",
						e.name, e.label, escapeLabelValue(k), e.vec[k].Value())
				}
				e.vecMu.RUnlock()
			}
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	})
}

// escapeLabelValue escapes the three characters the exposition format reserves.
// Label values derive from request data, so an unescaped quote or newline lets a
// caller inject synthetic metric lines into the scrape — corrupting the very
// dashboard an operator would use to notice the abuse.
func escapeLabelValue(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
```

Add the float bit helpers at the bottom of the same file, so `math` stays the only extra import:

```go
// Thin wrappers so the atomic gauge reads clearly at the call site.
func float64bits(f float64) uint64     { return math.Float64bits(f) }
func float64frombits(b uint64) float64 { return math.Float64frombits(b) }
```

Add `"math"` to the import block.

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/metrics/ -race -v -count=1
```

Expected: PASS, all four.

- [ ] **Step 5: Prove the escaping test is not a tautology**

Temporarily change `escapeLabelValue` to `return s`, re-run, confirm `TestCounterVecEscapesLabelValues` FAILS. Revert.

- [ ] **Step 6: Commit**

```bash
git add internal/metrics/
git commit -m "feat(metrics): add hand-rolled Prometheus exposition"
```

---

## Task 6: Verified client IP

The single most load-bearing piece of security code in Phase 2. Every rate limit and every enumeration counter keys on its output, so a mistake here silently disables all of them.

**Files:**
- Create: `internal/httpx/clientip.go`
- Create: `internal/httpx/cloudflare_ranges.go`
- Create: `internal/httpx/clientip_test.go`

**Interfaces:**
- Produces:
  - `type IPResolver struct { ... }`
  - `func NewIPResolver(trustedCIDRs []string) (*IPResolver, error)`
  - `func (r *IPResolver) ClientIP(req *http.Request) netip.Addr`
  - `func (r *IPResolver) BucketKey(req *http.Request) string`
  - `func (r *IPResolver) TrustsPeer(req *http.Request) bool`
  - `func DefaultCloudflareCIDRs() []string`
  - `type ctxKey`, `func WithClientIP(next http.Handler, r *IPResolver) http.Handler`, `func ClientIPFrom(ctx context.Context) netip.Addr`, `func BucketKeyFrom(ctx context.Context) string`, `func PeerTrustedFrom(ctx context.Context) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/httpx/clientip_test.go`:

```go
package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"airbg.org/internal/httpx"
)

func resolver(t *testing.T) *httpx.IPResolver {
	t.Helper()
	r, err := httpx.NewIPResolver(httpx.DefaultCloudflareCIDRs())
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	return r
}

// TestTrustedPeerHeaderIsHonoured — the happy path.
func TestTrustedPeerHeaderIsHonoured(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// 173.245.48.0/20 is a published Cloudflare range.
	req.RemoteAddr = "173.245.48.1:41000"
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")

	if got := resolver(t).ClientIP(req).String(); got != "198.51.100.7" {
		t.Errorf("ClientIP = %s, want 198.51.100.7", got)
	}
}

// TestUntrustedPeerHeaderIsIgnored is the test that matters. Without it, an
// implementation that trusts the header unconditionally passes every other test
// in this file — and every rate limit in the system becomes a no-op, because a
// scraper simply sets a fresh CF-Connecting-IP per request.
func TestUntrustedPeerHeaderIsIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:41000" // not Cloudflare
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")

	got := resolver(t).ClientIP(req).String()
	if got == "198.51.100.7" {
		t.Fatal("CF-Connecting-IP from a non-Cloudflare peer was trusted; every rate limit is now spoofable")
	}
	if got != "203.0.113.9" {
		t.Errorf("ClientIP = %s, want the socket address 203.0.113.9", got)
	}
}

// TestSpoofedHeaderChainIsIgnored: multiple comma-separated values, the classic
// X-Forwarded-For prepend attack shape, arriving from an untrusted peer.
func TestSpoofedHeaderChainIsIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.9:41000"
	req.Header.Set("CF-Connecting-IP", "1.2.3.4, 5.6.7.8")
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	if got := resolver(t).ClientIP(req).String(); got != "203.0.113.9" {
		t.Errorf("ClientIP = %s, want 203.0.113.9; no forwarded header may be read from an untrusted peer", got)
	}
}

// TestMalformedHeaderFromTrustedPeerFallsBack: a trusted peer sending garbage
// must fall back to the socket address, not produce a zero Addr. A zero Addr
// stringifies to "invalid IP" and every such request would share one bucket —
// so one malformed header would let an attacker pool all their traffic into a
// single key, or grief every other client sharing it.
func TestMalformedHeaderFromTrustedPeerFallsBack(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "173.245.48.1:41000"
	req.Header.Set("CF-Connecting-IP", "not-an-ip")

	got := resolver(t).ClientIP(req)
	if !got.IsValid() {
		t.Fatal("ClientIP returned an invalid Addr; a malformed header must fall back to the socket address")
	}
	if got.String() != "173.245.48.1" {
		t.Errorf("ClientIP = %s, want 173.245.48.1", got)
	}
}

// TestBucketKeyGroupsIPv6By64 is the IPv6 defeat, asserted deliberately.
//
// A single IPv6 host is routinely allocated a /64 — 2^64 addresses. Keying a
// rate limit on the full address against such a client is not rate limiting at
// all: it rotates source addresses at zero cost and never hits the same bucket
// twice. The failure is invisible when testing over IPv4, which is why this test
// exists rather than being left to integration.
func TestBucketKeyGroupsIPv6By64(t *testing.T) {
	res := resolver(t)

	keys := map[string]bool{}
	for _, addr := range []string{
		"[2001:db8:abcd:0012::1]:41000",
		"[2001:db8:abcd:0012::2]:41000",
		"[2001:db8:abcd:0012:ffff:ffff:ffff:ffff]:41000",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		keys[res.BucketKey(req)] = true
	}
	if len(keys) != 1 {
		t.Errorf("three addresses in one /64 produced %d bucket keys, want 1: %v", len(keys), keys)
	}

	// A different /64 must be a different bucket, or the limiter would lump
	// unrelated clients together and one abuser would 429 the neighbourhood.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[2001:db8:abcd:0013::1]:41000"
	if k := res.BucketKey(req); keys[k] {
		t.Error("a different /64 shares a bucket key with the first")
	}
}

// TestBucketKeyUsesFullIPv4Address: /64 grouping must not leak into IPv4, where
// a /24 would sweep up 256 unrelated customers.
func TestBucketKeyUsesFullIPv4Address(t *testing.T) {
	res := resolver(t)

	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = "203.0.113.1:41000"
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "203.0.113.2:41000"

	if res.BucketKey(req1) == res.BucketKey(req2) {
		t.Error("two distinct IPv4 addresses share a bucket key")
	}
}

// TestIPv4MappedIPv6IsNormalised: a dual-stack listener reports IPv4 peers as
// ::ffff:203.0.113.1. Left unnormalised, the same client gets one bucket over
// IPv4 and a second, /64-grouped one over the mapped form — and the mapped /64
// covers the entire IPv4 space, so every IPv4 client would share one bucket.
func TestIPv4MappedIPv6IsNormalised(t *testing.T) {
	res := resolver(t)

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	plain.RemoteAddr = "203.0.113.1:41000"
	mapped := httptest.NewRequest(http.MethodGet, "/", nil)
	mapped.RemoteAddr = "[::ffff:203.0.113.1]:41000"

	if a, b := res.BucketKey(plain), res.BucketKey(mapped); a != b {
		t.Errorf("bucket keys differ for the same client: %q vs %q", a, b)
	}
}

// TestEmptyTrustedListTrustsNothing: the no-proxy deployment mode. With no
// trusted CIDRs, no header may ever be honoured.
func TestEmptyTrustedListTrustsNothing(t *testing.T) {
	res, err := httpx.NewIPResolver(nil)
	if err != nil {
		t.Fatalf("NewIPResolver(nil): %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "173.245.48.1:41000" // a Cloudflare address
	req.Header.Set("CF-Connecting-IP", "198.51.100.7")

	if got := res.ClientIP(req).String(); got != "173.245.48.1" {
		t.Errorf("ClientIP = %s, want 173.245.48.1; an empty trusted list must trust no header, not fall back to the defaults", got)
	}
}

func TestNewIPResolverRejectsMalformedCIDR(t *testing.T) {
	if _, err := httpx.NewIPResolver([]string{"not-a-cidr"}); err == nil {
		t.Fatal("NewIPResolver accepted a malformed CIDR; a typo in AIRBG_TRUSTED_PROXY_CIDRS must fail at startup, not silently trust nothing at runtime")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/httpx/ -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the Cloudflare ranges**

Create `internal/httpx/cloudflare_ranges.go`:

```go
package httpx

// Cloudflare's published proxy ranges, from https://www.cloudflare.com/ips/
// (retrieved 2026-08-09).
//
// Embedded rather than fetched at startup on purpose. Fetching would make the
// binary's security posture depend on a network call succeeding at boot: a
// failed fetch either fails closed (the service will not start) or fails open
// (nothing is trusted, so every request is attributed to Cloudflare's own
// address and one bucket rate-limits the entire internet). Neither is
// acceptable, and Cloudflare changes these ranges rarely.
//
// AIRBG_TRUSTED_PROXY_CIDRS overrides this list, so a range change is a config
// edit and a restart, not a rebuild.
func DefaultCloudflareCIDRs() []string {
	return []string{
		// IPv4
		"173.245.48.0/20",
		"103.21.244.0/22",
		"103.22.200.0/22",
		"103.31.4.0/22",
		"141.101.64.0/18",
		"108.162.192.0/18",
		"190.93.240.0/20",
		"188.114.96.0/20",
		"197.234.240.0/22",
		"198.41.128.0/17",
		"162.158.0.0/15",
		"104.16.0.0/13",
		"104.24.0.0/14",
		"172.64.0.0/13",
		"131.0.72.0/22",
		// IPv6
		"2400:cb00::/32",
		"2606:4700::/32",
		"2803:f800::/32",
		"2405:b500::/32",
		"2405:8100::/32",
		"2a06:98c0::/29",
		"2c0f:f248::/32",
	}
}
```

- [ ] **Step 4: Write the resolver**

Create `internal/httpx/clientip.go`:

```go
// Package httpx holds the middleware every request passes through. It knows
// nothing about handlers or the database — it operates on http.Handler alone,
// so the whole chain is testable with a stub and no container.
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// IPResolver derives the client IP that rate limiting keys on.
type IPResolver struct {
	trusted []netip.Prefix
}

// NewIPResolver parses the trusted proxy CIDRs. An empty or nil list means no
// forwarded header is ever honoured — the correct behaviour for a
// directly-exposed origin.
//
// A malformed CIDR is a startup error, not a warning. The alternative is a
// typo in AIRBG_TRUSTED_PROXY_CIDRS silently emptying the trusted list, at
// which point every request behind Cloudflare is attributed to a Cloudflare
// edge address — a handful of buckets shared by the entire internet, which
// rate-limits all legitimate visitors and no attacker.
func NewIPResolver(trustedCIDRs []string) (*IPResolver, error) {
	r := &IPResolver{}
	for _, c := range trustedCIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("httpx: trusted proxy CIDR %q is not valid: %w", c, err)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

// peerAddr returns the normalised socket peer address.
//
// Unmap is essential: a dual-stack listener reports an IPv4 peer as
// ::ffff:203.0.113.1. Left mapped, BucketKey would take its /64 — and the
// v4-mapped /64 contains the entire IPv4 address space, so every IPv4 client on
// earth would share one bucket.
func peerAddr(req *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

// TrustsPeer reports whether the socket peer is inside a trusted proxy range.
func (r *IPResolver) TrustsPeer(req *http.Request) bool {
	addr := peerAddr(req)
	if !addr.IsValid() {
		return false
	}
	for _, p := range r.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP returns the address to attribute this request to.
//
// CF-Connecting-IP is read ONLY when the socket peer is itself a trusted proxy.
// From anyone else the header is ignored entirely — it is caller-supplied data,
// and a limiter keyed on caller-supplied data is not a limiter. This is the same
// rule the whole project applies: a safety mechanism must not sit downstream of
// the failure it guards.
//
// Cloudflare sends exactly one address in CF-Connecting-IP, never a list. A
// comma therefore means the value did not come from Cloudflare, so it is
// rejected rather than split — splitting would accept the shape of the classic
// X-Forwarded-For prepend attack.
func (r *IPResolver) ClientIP(req *http.Request) netip.Addr {
	peer := peerAddr(req)

	if r.TrustsPeer(req) {
		if v := strings.TrimSpace(req.Header.Get("CF-Connecting-IP")); v != "" && !strings.Contains(v, ",") {
			if addr, err := netip.ParseAddr(v); err == nil {
				return addr.Unmap()
			}
		}
	}
	return peer
}

// BucketKey returns the rate-limiting key for this request.
//
// IPv6 is keyed on the /64 prefix, not the address. A single IPv6 host is
// routinely allocated a /64 — 2^64 addresses — so per-address limiting against
// an IPv6 client is not rate limiting: the client rotates source addresses for
// free and never touches the same bucket twice. IPv4 keeps the full address,
// because an IPv4 prefix would sweep up unrelated customers, and Bulgarian
// mobile networks use CGNAT where one address already fronts thousands of
// legitimate users.
func (r *IPResolver) BucketKey(req *http.Request) string {
	addr := r.ClientIP(req)
	if !addr.IsValid() {
		// Unparseable peer: one shared key. Rare, and preferable to an empty
		// key that would silently exempt the request from every limit.
		return "invalid"
	}
	if addr.Is4() {
		return addr.String()
	}
	p, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return p.String()
}

type ctxKey int

const (
	ctxClientIP ctxKey = iota
	ctxBucketKey
	ctxPeerTrusted
)

// WithClientIP resolves the client IP once and puts it, its bucket key, and the
// peer-trust verdict in the request context.
//
// Resolving once matters: downstream middleware and handlers must all agree on
// the attribution, and re-deriving it per consumer is how a limiter and its log
// line end up naming different clients.
func WithClientIP(next http.Handler, r *IPResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		ctx = context.WithValue(ctx, ctxClientIP, r.ClientIP(req))
		ctx = context.WithValue(ctx, ctxBucketKey, r.BucketKey(req))
		ctx = context.WithValue(ctx, ctxPeerTrusted, r.TrustsPeer(req))
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func ClientIPFrom(ctx context.Context) netip.Addr {
	addr, _ := ctx.Value(ctxClientIP).(netip.Addr)
	return addr
}

func BucketKeyFrom(ctx context.Context) string {
	key, _ := ctx.Value(ctxBucketKey).(string)
	if key == "" {
		// A handler reached without WithClientIP in front of it. Returning a
		// shared key rather than "" keeps such a request limited rather than
		// exempt — fail closed.
		return "unattributed"
	}
	return key
}

// PeerTrustedFrom reports whether the request arrived through a trusted proxy.
// /locate uses it to decide whether Cloudflare's visitor-location headers may
// be read.
func PeerTrustedFrom(ctx context.Context) bool {
	trusted, _ := ctx.Value(ctxPeerTrusted).(bool)
	return trusted
}
```

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/httpx/ -race -v -count=1
```

Expected: PASS, all nine.

- [ ] **Step 6: Prove the trust tests are not tautologies**

Temporarily remove the `if r.TrustsPeer(req)` guard from `ClientIP` so the header is always read; re-run and confirm `TestUntrustedPeerHeaderIsIgnored`, `TestSpoofedHeaderChainIsIgnored` and `TestEmptyTrustedListTrustsNothing` all FAIL. Revert. Then change `BucketKey` to always `return addr.String()` and confirm `TestBucketKeyGroupsIPv6By64` FAILS. Revert.

- [ ] **Step 7: Commit**

```bash
git add internal/httpx/clientip.go internal/httpx/cloudflare_ranges.go internal/httpx/clientip_test.go
git commit -m "feat(httpx): derive the client IP from a verified peer only"
```

---

## Task 7: Token buckets

Per-client rate limiting at the origin. Sharded so the lock is not a global choke point, and TTL-evicted so idle keys cannot grow memory without bound.

**Files:**
- Create: `internal/ratelimit/bucket.go`
- Create: `internal/ratelimit/bucket_test.go`

**Interfaces:**
- Consumes: `httpx.BucketKeyFrom(ctx) string`, `metrics.CounterVec`.
- Produces:
  - `type Rate struct { PerSecond float64; Burst float64 }`
  - `type Limiter struct { ... }`
  - `func New(rate Rate, ttl time.Duration) *Limiter`
  - `func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration)`
  - `func (l *Limiter) SetClockForTesting(now func() time.Time)`
  - `func (l *Limiter) Evict()`, `func (l *Limiter) Len() int`
  - `func (l *Limiter) StartEvicting(ctx context.Context, every time.Duration)`

- [ ] **Step 1: Write the failing test**

Create `internal/ratelimit/bucket_test.go`:

```go
package ratelimit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"airbg.org/internal/ratelimit"
)

// clock is a manual clock. Rate limiting is defined in terms of elapsed time, so
// a test that used the real clock would either sleep (slow) or race (flaky).
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_800_000_000, 0).UTC()} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func limiter(t *testing.T, r ratelimit.Rate, ttl time.Duration) (*ratelimit.Limiter, *clock) {
	t.Helper()
	c := newClock()
	l := ratelimit.New(r, ttl)
	l.SetClockForTesting(c.now)
	return l, c
}

func TestBurstIsAllowedThenExhausted(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 5}, time.Hour)

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("request %d of the burst was denied", i+1)
		}
	}
	ok, retryAfter := l.Allow("k")
	if ok {
		t.Fatal("the 6th request was allowed with a burst of 5")
	}
	if retryAfter <= 0 {
		t.Errorf("retryAfter = %v, want > 0; a 429 without a usable Retry-After tells a well-behaved client nothing", retryAfter)
	}
}

func TestTokensRefillOverTime(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 2, Burst: 2}, time.Hour)

	l.Allow("k")
	l.Allow("k")
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("bucket was not exhausted")
	}

	c.advance(500 * time.Millisecond) // 2/s → exactly one token
	if ok, _ := l.Allow("k"); !ok {
		t.Error("no token after the refill interval elapsed")
	}
}

// TestRefillDoesNotExceedBurst: a bucket that accumulated tokens over an idle
// hour would let a client fire thousands of requests at once — the limiter would
// average correctly and still fail to protect the origin from the spike.
func TestRefillDoesNotExceedBurst(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 10, Burst: 3}, time.Hour)

	c.advance(time.Hour)

	allowed := 0
	for i := 0; i < 100; i++ {
		if ok, _ := l.Allow("k"); ok {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("allowed %d requests after an idle hour, want 3 (Burst); tokens must saturate at Burst", allowed)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, time.Hour)

	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("first request for key a denied")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Error("key b was denied because key a had spent its token; one client would then 429 everybody else")
	}
}

// TestEvictRemovesIdleKeys is the memory bound. Without eviction the map grows
// one entry per distinct key forever — and the keys are client-controlled, so an
// attacker rotating source addresses turns the rate limiter itself into the
// denial-of-service vector it was added to prevent.
func TestEvictRemovesIdleKeys(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, 10*time.Minute)

	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	if got := l.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}

	c.advance(11 * time.Minute)
	l.Evict()
	if got := l.Len(); got != 0 {
		t.Errorf("Len() = %d after the TTL elapsed, want 0", got)
	}
}

// TestEvictKeepsActiveKeys: eviction must not drop a key that is still being
// used, or an active abuser gets a fresh full bucket on every sweep — the
// limiter would then be strictly weaker against heavy traffic than light.
func TestEvictKeepsActiveKeys(t *testing.T) {
	l, c := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, 10*time.Minute)

	l.Allow("busy")
	c.advance(11 * time.Minute)
	l.Allow("busy") // touches the key just before the sweep
	l.Evict()

	if got := l.Len(); got != 1 {
		t.Errorf("Len() = %d, want 1; an active key was evicted", got)
	}
}

// TestConcurrentAllowIsRaceFree is run under -race and also checks the total:
// a limiter that lost increments under contention would let a distributed client
// exceed the limit by simply being concurrent, which is the normal case.
func TestConcurrentAllowIsRaceFree(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 0, Burst: 100}, time.Hour)

	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if ok, _ := l.Allow("shared"); ok {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// PerSecond is 0 and the clock never advances, so exactly Burst may pass.
	if allowed != 100 {
		t.Errorf("allowed = %d, want exactly 100 (Burst) — a lost update let extra requests through", allowed)
	}
}

func TestStartEvictingStopsWithContext(t *testing.T) {
	l, _ := limiter(t, ratelimit.Rate{PerSecond: 1, Burst: 1}, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	l.StartEvicting(ctx, time.Millisecond)
	cancel()
	// No assertion beyond "returns and does not panic"; the goroutine leak this
	// guards against is caught by -race plus the test binary exiting cleanly.
	time.Sleep(5 * time.Millisecond)
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/ratelimit/ -race -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the implementation**

Create `internal/ratelimit/bucket.go`:

```go
// Package ratelimit implements the origin-side token buckets and the
// enumeration-breadth counters from Phase 1 §8.2 and §8.3.
//
// The edge (Cloudflare) is the first line and absorbs volumetric floods. These
// buckets are the second: they still work when the edge is bypassed or a rule is
// mis-scoped, and they are the only control that sees the shape of a request —
// which area, which sensor — rather than just its rate.
package ratelimit

import (
	"context"
	"hash/maphash"
	"math"
	"sync"
	"time"
)

// Rate is a refill rate and a maximum burst, both in tokens.
type Rate struct {
	PerSecond float64
	Burst     float64
}

type bucket struct {
	tokens   float64
	lastFill time.Time
	lastSeen time.Time
}

// shardCount is a power of two so the mask below is valid. 32 shards keeps
// lock contention low without making Evict expensive.
//
// A single mutex would serialise every request in the process on one lock —
// which turns the rate limiter into the throughput ceiling, the opposite of
// what it is for.
const shardCount = 32

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type Limiter struct {
	rate Rate
	ttl  time.Duration

	shards [shardCount]shard
	seed   maphash.Seed

	// now is swappable so tests drive time explicitly. A rate limiter tested
	// against the wall clock either sleeps or flakes.
	nowMu sync.RWMutex
	now   func() time.Time
}

func New(rate Rate, ttl time.Duration) *Limiter {
	l := &Limiter{rate: rate, ttl: ttl, seed: maphash.MakeSeed(), now: time.Now}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*bucket)
	}
	return l
}

func (l *Limiter) SetClockForTesting(now func() time.Time) {
	l.nowMu.Lock()
	defer l.nowMu.Unlock()
	l.now = now
}

func (l *Limiter) clock() time.Time {
	l.nowMu.RLock()
	defer l.nowMu.RUnlock()
	return l.now()
}

func (l *Limiter) shardFor(key string) *shard {
	h := maphash.String(l.seed, key)
	return &l.shards[h&(shardCount-1)]
}

// Allow spends one token for key.
//
// When denied it returns how long until the next token, so the handler can send
// a truthful Retry-After. A 429 with no Retry-After — or a guessed one — pushes
// well-behaved clients into blind retry loops, which adds load precisely when
// the origin is already refusing work.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.clock()
	sh := l.shardFor(key)

	sh.mu.Lock()
	defer sh.mu.Unlock()

	b, ok := sh.buckets[key]
	if !ok {
		b = &bucket{tokens: l.rate.Burst, lastFill: now}
		sh.buckets[key] = b
	}

	// Refill for the elapsed time, saturating at Burst. The cap is the point:
	// without it an idle client accumulates unbounded credit and can discharge
	// an arbitrarily large spike, which averages out on a graph while still
	// overwhelming the origin in the moment.
	if elapsed := now.Sub(b.lastFill); elapsed > 0 {
		b.tokens = math.Min(l.rate.Burst, b.tokens+elapsed.Seconds()*l.rate.PerSecond)
		b.lastFill = now
	}

	// lastSeen is updated on every call, allowed or not. Tracking only allowed
	// requests would let a client being actively throttled fall idle by the
	// eviction sweep's reckoning, get its bucket dropped, and come back with a
	// full one.
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	if l.rate.PerSecond <= 0 {
		// No refill configured: nothing will ever free a token. Report the TTL
		// rather than an infinite or zero wait.
		return false, l.ttl
	}
	need := 1 - b.tokens
	return false, time.Duration(need / l.rate.PerSecond * float64(time.Second)).Round(time.Second)
}

// Evict drops buckets untouched for longer than the TTL.
func (l *Limiter) Evict() {
	cutoff := l.clock().Add(-l.ttl)
	for i := range l.shards {
		sh := &l.shards[i]
		sh.mu.Lock()
		for k, b := range sh.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(sh.buckets, k)
			}
		}
		sh.mu.Unlock()
	}
}

func (l *Limiter) Len() int {
	n := 0
	for i := range l.shards {
		sh := &l.shards[i]
		sh.mu.Lock()
		n += len(sh.buckets)
		sh.mu.Unlock()
	}
	return n
}

// StartEvicting runs Evict on a ticker until ctx is cancelled.
func (l *Limiter) StartEvicting(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				l.Evict()
			}
		}
	}()
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/ratelimit/ -race -v -count=1
```

Expected: PASS, all eight.

- [ ] **Step 5: Prove the saturation and eviction tests are not tautologies**

Remove the `math.Min` cap (just `b.tokens += …`) and confirm `TestRefillDoesNotExceedBurst` FAILS. Revert. Move the `b.lastSeen = now` line inside the `if b.tokens >= 1` block and confirm `TestEvictKeepsActiveKeys` still passes but a denied-then-swept key would be reset — then instead change `Evict` to `delete` unconditionally and confirm `TestEvictKeepsActiveKeys` FAILS. Revert.

- [ ] **Step 6: Commit**

```bash
git add internal/ratelimit/bucket.go internal/ratelimit/bucket_test.go
git commit -m "feat(ratelimit): add sharded, TTL-evicted token buckets"
```

---

## Task 8: Enumeration detection

Volume limits do not catch a patient scraper. This counts **breadth** — how many distinct areas and sensors one client prefix touches per hour — which is the signal that separates browsing from harvesting.

**Files:**
- Create: `internal/ratelimit/enumerate.go`
- Create: `internal/ratelimit/enumerate_test.go`

**Interfaces:**
- Produces:
  - `const DistinctAreaLimit = 12`, `const DistinctSensorLimit = 40`, `const EnumerationWindow = time.Hour`
  - `type Breadth struct { ... }`
  - `func NewBreadth(areaLimit, sensorLimit int, window time.Duration) *Breadth`
  - `func (b *Breadth) ObserveArea(key, slug string) bool`
  - `func (b *Breadth) ObserveSensor(key string, sensorID int64) bool`
  - `func (b *Breadth) SetClockForTesting(now func() time.Time)`
  - `func (b *Breadth) Evict()`, `func (b *Breadth) Len() int`, `func (b *Breadth) StartEvicting(ctx, every)`

- [ ] **Step 1: Write the failing test**

Create `internal/ratelimit/enumerate_test.go`:

```go
package ratelimit_test

import (
	"testing"
	"time"

	"airbg.org/internal/ratelimit"
)

func breadth(t *testing.T, areaLimit, sensorLimit int) (*ratelimit.Breadth, *clock) {
	t.Helper()
	c := newClock()
	b := ratelimit.NewBreadth(areaLimit, sensorLimit, time.Hour)
	b.SetClockForTesting(c.now)
	return b, c
}

// TestRepeatedSameAreaIsNotEnumeration is the false-positive guard, and the
// reason this counts DISTINCT slugs rather than requests. Someone watching one
// city's page all afternoon — a resident checking the air before a run — must
// never be flagged. A volume counter cannot tell them from a scraper; a breadth
// counter can.
func TestRepeatedSameAreaIsNotEnumeration(t *testing.T) {
	b, _ := breadth(t, 3, 10)

	for i := 0; i < 500; i++ {
		if !b.ObserveArea("client", "sofia") {
			t.Fatalf("flagged at request %d for repeatedly viewing ONE area", i+1)
		}
	}
}

func TestDistinctAreasTripTheLimit(t *testing.T) {
	b, _ := breadth(t, 3, 10)

	for _, slug := range []string{"sofia", "plovdiv", "varna"} {
		if !b.ObserveArea("client", slug) {
			t.Fatalf("flagged at or below the limit on %q", slug)
		}
	}
	if b.ObserveArea("client", "burgas") {
		t.Error("the 4th distinct area was allowed with a limit of 3")
	}
}

// TestTrippedClientStaysTrippedForKnownAreas: once over the limit, a client must
// be refused even for a slug it already visited. Otherwise a scraper walks the
// country, trips at the end, and then replays its whole visited set freely —
// which is exactly the extraction the check exists to stop.
func TestTrippedClientStaysTrippedForKnownAreas(t *testing.T) {
	b, _ := breadth(t, 2, 10)

	b.ObserveArea("client", "a")
	b.ObserveArea("client", "b")
	b.ObserveArea("client", "c") // trips

	if b.ObserveArea("client", "a") {
		t.Error("a tripped client was allowed to re-request an already-seen area")
	}
}

func TestDistinctSensorsTripSeparately(t *testing.T) {
	b, _ := breadth(t, 100, 3)

	for _, id := range []int64{1, 2, 3} {
		if !b.ObserveSensor("client", id) {
			t.Fatalf("flagged at or below the sensor limit on %d", id)
		}
	}
	if b.ObserveSensor("client", 4) {
		t.Error("the 4th distinct sensor was allowed with a limit of 3")
	}
	// The area budget must be untouched — the two dimensions are independent,
	// or a sensor-heavy session would lock a user out of the map.
	if !b.ObserveArea("client", "sofia") {
		t.Error("tripping the sensor limit also blocked areas")
	}
}

func TestKeysAreIndependentForBreadth(t *testing.T) {
	b, _ := breadth(t, 1, 1)

	b.ObserveArea("a", "sofia")
	b.ObserveArea("a", "varna") // a trips

	if !b.ObserveArea("b", "sofia") {
		t.Error("client b was blocked by client a's enumeration; on CGNAT that would lock out a whole mobile network at once")
	}
}

// TestWindowResets: the window must roll, or a client that once tripped is
// blocked forever. On CGNAT one abuser shares an address with thousands of
// legitimate users, so a permanent block is collateral damage measured in
// neighbourhoods.
func TestWindowResets(t *testing.T) {
	b, c := breadth(t, 2, 10)

	b.ObserveArea("client", "a")
	b.ObserveArea("client", "b")
	if b.ObserveArea("client", "c") {
		t.Fatal("did not trip")
	}

	c.advance(61 * time.Minute)
	if !b.ObserveArea("client", "d") {
		t.Error("still blocked after the window elapsed")
	}
}

// TestEvictRemovesIdleBreadthKeys — same unbounded-growth hazard as the token
// buckets, with a bigger footprint: each entry holds two sets, not one counter.
func TestEvictRemovesIdleBreadthKeys(t *testing.T) {
	b, c := breadth(t, 5, 5)

	b.ObserveArea("a", "x")
	b.ObserveArea("b", "y")
	if got := b.Len(); got != 2 {
		t.Fatalf("Len() = %d, want 2", got)
	}

	c.advance(3 * time.Hour)
	b.Evict()
	if got := b.Len(); got != 0 {
		t.Errorf("Len() = %d after eviction, want 0", got)
	}
}

// TestSetSizeIsBoundedWhenTripped: after tripping, the sets must stop growing.
// A tripped client that kept inserting every new slug it asked for would let an
// attacker who has ALREADY been flagged keep allocating memory — turning a
// refused request into a slow leak.
func TestSetSizeIsBoundedWhenTripped(t *testing.T) {
	b, _ := breadth(t, 2, 2)

	for i := 0; i < 10_000; i++ {
		b.ObserveArea("client", string(rune(i%0x4000+0x100)))
	}
	if got := b.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 key", got)
	}
	if got := b.SlugSetSizeForTesting("client"); got > 3 {
		t.Errorf("slug set grew to %d entries after tripping; a refused client must stop consuming memory", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/ratelimit/ -run Breadth -v
go test ./internal/ratelimit/ -run 'Enumerat|Distinct|Window|SetSize' -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the implementation**

Create `internal/ratelimit/enumerate.go`:

```go
package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// Limits from Phase 1 §8.3.
//
// 12 distinct areas per hour: Bulgaria has 28 oblasti and 28 cities. A curious
// visitor compares their own city with a handful of others; nobody legitimately
// opens half the country in an hour. 40 distinct sensors: a dense city page
// shows well under that, and a user clicking individual markers gets bored long
// before reaching it.
//
// Both are breadth, not volume, on purpose: volume limits punish enthusiasm and
// miss a patient scraper pacing itself under the rate limit.
const (
	DistinctAreaLimit   = 12
	DistinctSensorLimit = 40
	EnumerationWindow   = time.Hour
)

type breadthEntry struct {
	windowStart time.Time
	lastSeen    time.Time
	slugs       map[string]struct{}
	sensors     map[string]struct{}
	// tripped is sticky for the rest of the window. Without it a client that
	// walked the country could replay its visited set freely after tripping.
	areaTripped   bool
	sensorTripped bool
}

// Breadth counts how many distinct areas and sensors each client key touches
// within a rolling window.
type Breadth struct {
	areaLimit   int
	sensorLimit int
	window      time.Duration

	mu      sync.Mutex
	entries map[string]*breadthEntry

	nowMu sync.RWMutex
	now   func() time.Time
}

func NewBreadth(areaLimit, sensorLimit int, window time.Duration) *Breadth {
	return &Breadth{
		areaLimit: areaLimit, sensorLimit: sensorLimit, window: window,
		entries: make(map[string]*breadthEntry), now: time.Now,
	}
}

func (b *Breadth) SetClockForTesting(now func() time.Time) {
	b.nowMu.Lock()
	defer b.nowMu.Unlock()
	b.now = now
}

func (b *Breadth) clock() time.Time {
	b.nowMu.RLock()
	defer b.nowMu.RUnlock()
	return b.now()
}

// ObserveArea records that key requested slug and reports whether to serve it.
//
// A single mutex here rather than the sharded design the token buckets use:
// this is called once per area request, not per request, and it mutates two maps
// per call — sharding it would add complexity for a lock that is not hot.
func (b *Breadth) ObserveArea(key, slug string) bool {
	return b.observe(key, slug, false)
}

func (b *Breadth) ObserveSensor(key string, sensorID int64) bool {
	return b.observe(key, strconv.FormatInt(sensorID, 10), true)
}

func (b *Breadth) observe(key, value string, sensor bool) bool {
	now := b.clock()

	b.mu.Lock()
	defer b.mu.Unlock()

	e, ok := b.entries[key]
	if !ok {
		e = &breadthEntry{
			windowStart: now,
			slugs:       make(map[string]struct{}),
			sensors:     make(map[string]struct{}),
		}
		b.entries[key] = e
	}

	// Roll the window. A fixed window rather than a sliding one: sliding would
	// need per-observation timestamps, and the worst case a fixed window allows
	// — 2×limit across a boundary — is well inside the tolerance of a check
	// whose limits are already set generously to avoid false positives.
	if now.Sub(e.windowStart) >= b.window {
		e.windowStart = now
		e.slugs = make(map[string]struct{})
		e.sensors = make(map[string]struct{})
		e.areaTripped = false
		e.sensorTripped = false
	}
	e.lastSeen = now

	set, limit, tripped := e.slugs, b.areaLimit, &e.areaTripped
	if sensor {
		set, limit, tripped = e.sensors, b.sensorLimit, &e.sensorTripped
	}

	if *tripped {
		// Already over: refuse without recording. Recording would let a client
		// we have already refused keep growing our memory.
		return false
	}

	if _, seen := set[value]; seen {
		// Revisiting something already counted is free. This is what makes
		// "reads one city's page all day" indistinguishable from one request.
		return true
	}

	if len(set) >= limit {
		*tripped = true
		return false
	}
	set[value] = struct{}{}
	return true
}

func (b *Breadth) Evict() {
	// Entries are dropped after two windows of silence rather than one, so a
	// client whose window is still open is not handed a clean slate early.
	cutoff := b.clock().Add(-2 * b.window)

	b.mu.Lock()
	defer b.mu.Unlock()
	for k, e := range b.entries {
		if e.lastSeen.Before(cutoff) {
			delete(b.entries, k)
		}
	}
}

func (b *Breadth) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// SlugSetSizeForTesting exposes one entry's slug-set size so a test can assert
// that a tripped client stops consuming memory.
func (b *Breadth) SlugSetSizeForTesting(key string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		return 0
	}
	return len(e.slugs)
}

func (b *Breadth) StartEvicting(ctx context.Context, every time.Duration) {
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				b.Evict()
			}
		}
	}()
}
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/ratelimit/ -race -v -count=1
```

Expected: PASS, all sixteen (Task 7's eight plus these eight).

- [ ] **Step 5: Prove the sticky-trip and no-record tests are not tautologies**

Delete the `if *tripped { return false }` block and confirm `TestTrippedClientStaysTrippedForKnownAreas` FAILS. Revert. Move `set[value] = struct{}{}` above the `len(set) >= limit` check and confirm `TestSetSizeIsBoundedWhenTripped` FAILS. Revert.

- [ ] **Step 6: Commit**

```bash
git add internal/ratelimit/enumerate.go internal/ratelimit/enumerate_test.go
git commit -m "feat(ratelimit): detect enumeration by breadth, not volume"
```

---

## Task 9: Middleware chain

Panic recovery, security headers, body cap, and the composition order. `internal/httpx` must not import `internal/api` — it operates on `http.Handler` alone, which is what makes the whole chain testable against a stub handler with no database.

**Files:**
- Create: `internal/httpx/recover.go`
- Create: `internal/httpx/headers.go`
- Create: `internal/httpx/chain.go`
- Create: `internal/httpx/chain_test.go`

**Interfaces:**
- Consumes: `IPResolver`, `ratelimit.Limiter`, `ratelimit.Breadth`, `metrics`.
- Produces:
  - `func Recover(next http.Handler) http.Handler`
  - `func SecurityHeaders(next http.Handler) http.Handler`
  - `func LimitBody(next http.Handler, maxBytes int64) http.Handler`
  - `func RateLimit(next http.Handler, l *ratelimit.Limiter) http.Handler`
  - `type Chain struct { Resolver *IPResolver; Limiter *ratelimit.Limiter; MaxBodyBytes int64 }`
  - `func (c Chain) Wrap(h http.Handler) http.Handler`
  - `const CSPValue`

- [ ] **Step 1: Write the failing test**

Create `internal/httpx/chain_test.go`:

```go
package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/httpx"
	"airbg.org/internal/ratelimit"
)

func TestRecoverTurnsPanicInto500(t *testing.T) {
	h := httpx.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	// If Recover did not recover, this call panics and the test fails loudly —
	// which is the assertion. One handler bug must not take the process down and
	// with it every other in-flight request.
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("the panic value leaked into the response body; panic text routinely contains paths, SQL and internal state")
	}
	if strings.Contains(rec.Body.String(), "goroutine") {
		t.Error("a stack trace leaked into the response body")
	}
}

// TestRecoverDoesNotWriteAfterHeaders: if the handler already wrote a status,
// Recover must not try to write another. net/http logs "superfluous
// WriteHeader" and the client gets a truncated body under a 200 — a corrupt
// response that looks successful.
func TestRecoverDoesNotWriteAfterHeaders(t *testing.T) {
	h := httpx.Recover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the already-written 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("the panic value was appended to the partial body")
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := httpx.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"X-Frame-Options":        "DENY",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy is absent")
	}
	// frame-ancestors, not just X-Frame-Options: modern browsers honour the CSP
	// directive and ignore the legacy header when both are present.
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP is missing %q: %s", want, csp)
		}
	}
	// unsafe-inline would defeat the point of having a CSP at all.
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP contains an unsafe-* directive: %s", csp)
	}
}

// TestLimitBodyRejectsOversizeRequest: without a cap, a single request with an
// enormous body makes the origin allocate until it dies — a one-line
// denial-of-service that no rate limiter catches, because it is one request.
func TestLimitBodyRejectsOversizeRequest(t *testing.T) {
	var readErr error
	h := httpx.LimitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<16)
		for {
			_, err := r.Body.Read(buf)
			if err != nil {
				readErr = err
				break
			}
		}
		w.WriteHeader(http.StatusOK)
	}), 1024)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 8192)))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if readErr == nil {
		t.Error("reading an 8 KiB body under a 1 KiB cap produced no error")
	}
}

func TestRateLimitReturns429WithRetryAfter(t *testing.T) {
	l := ratelimit.New(ratelimit.Rate{PerSecond: 1, Burst: 1}, time.Hour)
	res := resolver(t)

	h := httpx.WithClientIP(httpx.RateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), l), res)

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
		r.RemoteAddr = "203.0.113.50:41000"
		return r
	}

	first := httptest.NewRecorder()
	h.ServeHTTP(first, req())
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	h.ServeHTTP(second, req())
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	ra := second.Header().Get("Retry-After")
	if ra == "" {
		t.Fatal("429 has no Retry-After")
	}
	if n, err := strconv.Atoi(ra); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive integer number of seconds", ra)
	}
}

// TestChainRateLimitsBeforeReachingTheHandler is the ordering test, and the one
// that catches the mistake that matters. If the limiter sits AFTER the handler
// — or after anything expensive — then the work a flood is meant to be denied
// has already been done by the time it is denied. A safety mechanism must not
// sit downstream of the failure it guards.
func TestChainRateLimitsBeforeReachingTheHandler(t *testing.T) {
	reached := 0
	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      ratelimit.New(ratelimit.Rate{PerSecond: 0, Burst: 1}, time.Hour),
		MaxBodyBytes: 4096,
	}
	h := chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
		r.RemoteAddr = "203.0.113.51:41000"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	if reached != 1 {
		t.Errorf("handler ran %d times under a burst of 1; the limiter is downstream of the work it protects", reached)
	}
}

// TestChainRecoversAndStillSetsHeaders: a panicking handler must still produce a
// response carrying the security headers. Recover has to be OUTSIDE
// SecurityHeaders for that, and getting the nesting backwards yields a bare 500
// with no CSP — on an HTML error page that is an XSS surface.
func TestChainRecoversAndStillSetsHeaders(t *testing.T) {
	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      ratelimit.New(ratelimit.Rate{PerSecond: 100, Burst: 100}, time.Hour),
		MaxBodyBytes: 4096,
	}
	h := chain.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	r.RemoteAddr = "203.0.113.52:41000"
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Error("the 500 produced by Recover carries no CSP; SecurityHeaders must run inside Recover so its headers are already set when the panic unwinds")
	}
}

// TestChainProvidesClientIPToTheHandler: the handler must see the resolved IP.
// If WithClientIP is missing from the chain, BucketKeyFrom falls back to
// "unattributed" and every client shares one bucket.
func TestChainProvidesClientIPToTheHandler(t *testing.T) {
	chain := httpx.Chain{
		Resolver:     resolver(t),
		Limiter:      ratelimit.New(ratelimit.Rate{PerSecond: 100, Burst: 100}, time.Hour),
		MaxBodyBytes: 4096,
	}

	var got string
	h := chain.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = httpx.BucketKeyFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	r.RemoteAddr = "203.0.113.53:41000"
	h.ServeHTTP(httptest.NewRecorder(), r)

	if got != "203.0.113.53" {
		t.Errorf("BucketKeyFrom = %q, want %q", got, "203.0.113.53")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/httpx/ -race -v
```

Expected: FAIL to build — `undefined: httpx.Recover` and the rest.

- [ ] **Step 3: Write Recover**

Create `internal/httpx/recover.go`:

```go
package httpx

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"airbg.org/internal/metrics"
)

var panicsRecovered = metrics.Counter(
	"airbg_http_panics_recovered_total",
	"Handler panics caught by the recovery middleware.")

// statusRecorder tracks whether a status has been written, so Recover knows
// whether it may still write one.
type statusRecorder struct {
	http.ResponseWriter
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// An implicit 200 counts as written; net/http sends one on the first Write.
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Recover converts a handler panic into a 500.
//
// The panic value and stack go to the log, never to the client. Panic text
// routinely contains file paths, SQL fragments and internal state — returning it
// hands an attacker a free reconnaissance channel, and this project's whole API
// posture is about not volunteering internals.
//
// http.ErrAbortHandler is re-panicked rather than swallowed: net/http uses it as
// the deliberate "drop this connection silently" signal, and turning it into a
// 500 would both log noise and defeat the abort.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}

		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v)
			}

			panicsRecovered.Inc()
			slog.Error("handler panic",
				"panic", v,
				"method", r.Method,
				// The route pattern, not the raw path: logging attacker-supplied
				// paths verbatim invites log injection, and r.Pattern is set by
				// ServeMux from the route table.
				"pattern", r.Pattern,
				"stack", string(debug.Stack()))

			// Only write if nothing has been written yet. Writing a second
			// status produces net/http's "superfluous WriteHeader" and leaves
			// the client with a truncated body under a success status.
			if !rec.wroteHeader {
				rec.ResponseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
				rec.ResponseWriter.WriteHeader(http.StatusInternalServerError)
				_, _ = rec.ResponseWriter.Write([]byte(`{"error":"internal","message":"Internal server error."}`))
			}
		}()

		next.ServeHTTP(rec, r)
	})
}
```

- [ ] **Step 4: Write the security headers and body cap**

Create `internal/httpx/headers.go`:

```go
package httpx

import "net/http"

// CSPValue is the policy from Phase 1 §9.7.
//
// No 'unsafe-inline' and no 'unsafe-eval': Phase 3's islands ship as external
// modules and its map styles as external JSON, so nothing needs them. Adding
// either would make the CSP decorative — an inline-script allowance is the
// single most common way a CSP stops mitigating XSS.
//
// connect-src 'self' keeps the API calls same-origin. img-src allows data: for
// MapLibre's canvas-generated sprites and blob: for its worker-produced tiles;
// worker-src blob: is required by MapLibre GL JS, which constructs its workers
// from blobs.
const CSPValue = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data: blob:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"worker-src 'self' blob:; " +
	"object-src 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// SecurityHeaders sets the response headers that do not depend on the handler.
//
// Set BEFORE calling next, so they are already on the ResponseWriter if the
// handler panics — a 500 rendered as HTML with no CSP is an XSS surface.
//
// HSTS is deliberately absent here and set by Cloudflare instead: sending it
// from the origin would also apply to a local `serve` over plain HTTP, pinning
// a developer's browser to HTTPS for localhost.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", CSPValue)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Legacy, for browsers that predate frame-ancestors. Harmless where
		// both are understood; the CSP directive wins there.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// LimitBody caps how much of a request body a handler can read.
//
// Every Phase 2 endpoint is a GET with no body, so the cap is small. It exists
// because without it one request with an enormous body can make the origin
// allocate until it dies — a denial of service that no rate limiter sees, since
// it is a single request.
func LimitBody(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 5: Write the rate-limit middleware and the chain**

Create `internal/httpx/chain.go`:

```go
package httpx

import (
	"net/http"
	"strconv"

	"airbg.org/internal/metrics"
	"airbg.org/internal/ratelimit"
)

var (
	requestsTotal = metrics.CounterVec(
		"airbg_http_requests_total",
		"Requests served, by route pattern.",
		"pattern")

	rateLimited = metrics.CounterVec(
		"airbg_http_rate_limited_total",
		"Requests refused by the origin token buckets, by route pattern.",
		"pattern")
)

// RateLimit refuses a request when its client's bucket is empty.
//
// It requires WithClientIP upstream of it; BucketKeyFrom returns
// "unattributed" otherwise, which would pool every client into one bucket. Chain
// guarantees the ordering.
func RateLimit(next http.Handler, l *ratelimit.Limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := BucketKeyFrom(r.Context())
		ok, retryAfter := l.Allow(key)
		if !ok {
			// Label by the route PATTERN, never the concrete path: the path is
			// caller-controlled and would give the metric unbounded label
			// cardinality — an attacker could exhaust memory through the
			// counters that exist to report the attack.
			rateLimited.With(patternLabel(r)).Inc()

			secs := int(retryAfter.Seconds())
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limited","message":"Too many requests. Please slow down."}`))
			return
		}
		requestsTotal.With(patternLabel(r)).Inc()
		next.ServeHTTP(w, r)
	})
}

// patternLabel returns the matched route pattern, or "unmatched" when the
// request never reached the mux (which is the case for middleware running
// outside it). Bounded by the route table, so label cardinality is bounded too.
func patternLabel(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return "unmatched"
}

// Chain composes the middleware every public request passes through.
type Chain struct {
	Resolver     *IPResolver
	Limiter      *ratelimit.Limiter
	MaxBodyBytes int64
}

// Wrap builds the handler. Order, outermost first:
//
//  1. Recover      — must be outermost, or a panic in any other middleware
//                    kills the connection with no response and no metric.
//  2. SecurityHeaders — inside Recover so its headers are already set when a
//                    panic unwinds and Recover writes its 500.
//  3. WithClientIP — must precede RateLimit, which keys on its output.
//  4. RateLimit    — as early as possible: everything downstream of it is work
//                    a refused request must not cost us. This is the ordering
//                    property TestChainRateLimitsBeforeReachingTheHandler pins.
//  5. LimitBody    — cheap, and only relevant to a request that got this far.
//  6. the handler.
//
// Enumeration detection is NOT here. It needs the parsed {slug} and {id} path
// parameters, which only exist after the mux has matched, so it lives in the
// api package's per-route handlers.
func (c Chain) Wrap(h http.Handler) http.Handler {
	h = LimitBody(h, c.MaxBodyBytes)
	h = RateLimit(h, c.Limiter)
	h = WithClientIP(h, c.Resolver)
	h = SecurityHeaders(h)
	h = Recover(h)
	return h
}
```

- [ ] **Step 6: Run the tests**

```bash
go test ./internal/httpx/ -race -v -count=1
```

Expected: PASS — Task 6's nine plus these eight.

- [ ] **Step 7: Prove the ordering tests are not tautologies**

In `Chain.Wrap`, swap the `RateLimit` and `LimitBody` lines so the limiter is innermost — confirm `TestChainRateLimitsBeforeReachingTheHandler` still passes (it only asserts the handler, which is still downstream). Then move `RateLimit` to wrap the OUTSIDE of `Recover` and confirm `TestChainRecoversAndStillSetsHeaders` FAILS. Revert. Delete the `WithClientIP` line and confirm `TestChainProvidesClientIPToTheHandler` FAILS. Revert. Finally move the header writes in `SecurityHeaders` to AFTER `next.ServeHTTP` and confirm `TestChainRecoversAndStillSetsHeaders` FAILS. Revert.

- [ ] **Step 8: Commit**

```bash
git add internal/httpx/recover.go internal/httpx/headers.go internal/httpx/chain.go internal/httpx/chain_test.go
git commit -m "feat(httpx): compose recovery, security headers, body cap and rate limiting"
```

---

## Task 10: Router, error shape and scale tables

The route table, the one error envelope every failure uses, the conditional-request helper, and the air-quality scale data the frontend needs to colour anything.

**Files:**
- Create: `internal/api/router.go`
- Create: `internal/api/scales.go`
- Create: `internal/api/router_test.go`
- Create: `internal/api/scales_test.go`

**Interfaces:**
- Consumes: `snapshot.Holder`, `snapshot.Body`, `httpx.Chain`, `ratelimit.Breadth`, `metrics`.
- Produces:
  - `type Deps struct { Snapshots *snapshot.Holder; Breadth *ratelimit.Breadth; Store DataSource; BaseURL string }`
  - ```go
    type DataSource interface {
        AreaAtPoint(ctx context.Context, lon, lat float64) (string, error)
        SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool) ([]store.Point, error)
        AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool) ([]store.Point, error)
    }
    ```
    All three are implemented by `*store.Store`; the interface exists so the handler tests need no container.
  - `func NewRouter(d Deps) *http.ServeMux`
  - `func writeError(w http.ResponseWriter, status int, code, message string)`
  - `func serveBody(w http.ResponseWriter, r *http.Request, b snapshot.Body, maxAge int) `
  - `func Scales() []Scale`, `type Scale struct { ... }`, `type Band struct { ... }`

- [ ] **Step 1: Write the failing test for the error shape and conditional requests**

Create `internal/api/router_test.go`:

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
)

// stubSource satisfies api.DataSource without a database. The api package's
// tests must not need a container: they are about HTTP semantics, and a
// container per test would make them slow enough to be skipped.
type stubSource struct {
	slug   string
	points []store.Point
	err    error
}

func (s stubSource) AreaAtPoint(_ context.Context, _, _ float64) (string, error) {
	return s.slug, s.err
}

func (s stubSource) SensorSeries(_ context.Context, _ int64, _ string, _ time.Time, _ bool) ([]store.Point, error) {
	return s.points, s.err
}

func (s stubSource) AreaSeries(_ context.Context, _, _ string, _ time.Time, _ bool) ([]store.Point, error) {
	return s.points, s.err
}

func deps(t *testing.T, snap *snapshot.Snapshot) api.Deps {
	t.Helper()
	h := snapshot.NewHolder()
	if snap != nil {
		h.Store(snap)
	}
	return api.Deps{
		Snapshots: h,
		Breadth:   ratelimit.NewBreadth(ratelimit.DistinctAreaLimit, ratelimit.DistinctSensorLimit, time.Hour),
		Store:     stubSource{slug: "sofia"},
		BaseURL:   "https://airbg.org",
	}
}

// fixture builds a minimal but complete snapshot: one known area with a body.
func fixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	body := func(s string) snapshot.Body {
		return snapshot.Body{JSON: []byte(s), Gzip: []byte("gzipped-" + s), ETag: `"` + s + `"`}
	}
	return &snapshot.Snapshot{
		GeneratedAt:  time.Unix(1_800_000_000, 0).UTC(),
		Overview:     body(`{"areas":[{"slug":"sofia"}]}`),
		OverviewCity: body(`{"areas":[{"slug":"sofia-center"}]}`),
		Areas:        body(`{"areas":[{"slug":"sofia"}]}`),
		AreaSensors: map[string]snapshot.Body{
			"sofia": body(`{"sensors":{"id":[1]}}`),
		},
		KnownSlugs: map[string]snapshot.AreaMeta{
			"sofia": {Slug: "sofia", Kind: "oblast", NameBG: "София", NameEN: "Sofia",
				CentroidLon: 23.32, CentroidLat: 42.69, DefaultZoom: 9, Covered: true, SensorCount: 5},
		},
	}
}

// TestErrorResponsesShareOneShape. A client cannot handle failures it cannot
// parse. More importantly, an envelope that sometimes carries a Go error string
// leaks internals — so message is always a fixed human sentence and code is
// always a fixed machine token.
func TestErrorResponsesShareOneShape(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	cases := []struct {
		path   string
		status int
		code   string
	}{
		{"/api/v1/area/nope/sensors", http.StatusNotFound, "not_found"},
		{"/api/v1/sensor/abc/series", http.StatusBadRequest, "bad_request"},
		{"/api/partner/v1/anything", http.StatusNotImplemented, "not_implemented"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))

		if rec.Code != c.status {
			t.Errorf("%s: status = %d, want %d (body: %s)", c.path, rec.Code, c.status, rec.Body.String())
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: Content-Type = %q, want application/json", c.path, ct)
		}

		var got struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Errorf("%s: body is not JSON: %v (%s)", c.path, err, rec.Body.String())
			continue
		}
		if got.Error != c.code {
			t.Errorf("%s: error = %q, want %q", c.path, got.Error, c.code)
		}
		if got.Message == "" {
			t.Errorf("%s: message is empty", c.path)
		}
	}
}

// TestUnknownSlugIsNotFoundNotEmpty: an unknown slug must be 404, never a 200
// with an empty list. Serving 200-with-nothing for a typo is the same class of
// bug as reporting success while storing nothing — the caller cannot tell "no
// such place" from "nothing measured here".
func TestUnknownSlugIsNotFoundNotEmpty(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/area/atlantis/sensors", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for an unknown slug", rec.Code)
	}
}

func TestETagProduces304(t *testing.T) {
	snap := fixture(t)
	mux := api.NewRouter(deps(t, snap))

	first := httptest.NewRecorder()
	mux.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first response")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	mux.ServeHTTP(second, req)

	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carries a %d-byte body; RFC 9110 forbids one", second.Body.Len())
	}
	if second.Header().Get("ETag") != etag {
		t.Error("the 304 does not repeat the ETag; a client cannot then revalidate again")
	}
}

// TestStaleETagIsIgnored: a client holding an old ETag must get fresh data, not
// a 304. A helper that answered 304 for any If-None-Match at all would pin every
// returning visitor to whatever they first saw — permanently stale, and
// invisible in tests that only ever send a matching ETag.
func TestStaleETagIsIgnored(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.Header.Set("If-None-Match", `"something-else"`)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a non-matching If-None-Match", rec.Code)
	}
}

// TestGzipIsServedOnlyWhenAccepted. Sending gzip to a client that did not
// advertise it produces unreadable bytes under a 200 — a corrupt success.
func TestGzipIsServedOnlyWhenAccepted(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	plain := httptest.NewRecorder()
	mux.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))
	if enc := plain.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q with no Accept-Encoding, want empty", enc)
	}
	if plain.Body.String() != `{"areas":[{"slug":"sofia"}]}` {
		t.Errorf("plain body = %q", plain.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	zipped := httptest.NewRecorder()
	mux.ServeHTTP(zipped, req)
	if enc := zipped.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Errorf("Content-Encoding = %q with Accept-Encoding: gzip, want gzip", enc)
	}
	if got := zipped.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Error("Vary does not list Accept-Encoding; a shared cache would then serve gzip to a client that cannot decode it")
	}
}

// TestNoSnapshotIs503: before the first ingest cycle the service has no data.
// It must say so, not serve an empty country as though it had been measured.
func TestNoSnapshotIs503(t *testing.T) {
	mux := api.NewRouter(deps(t, nil))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 before the first snapshot", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("the 503 has no Retry-After")
	}
}

// TestPostIsMethodNotAllowed. Go 1.22+ ServeMux gives this for free from a
// method-qualified pattern; the test pins that the patterns ARE method-qualified.
func TestPostIsMethodNotAllowed(t *testing.T) {
	mux := api.NewRouter(deps(t, fixture(t)))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/overview", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}
```

Add `"context"` and `"airbg.org/internal/store"` to the import block — `stubSource` needs both.

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/api/ -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the router**

Create `internal/api/router.go`:

```go
// Package api serves the JSON endpoints from Phase 1 §7.
//
// Every response except /locate comes from the in-memory snapshot, so a request
// costs a pointer load and a byte-slice write. That is the whole
// denial-of-service posture: there is no per-request query to overwhelm.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

// DataSource is the whole database surface this package uses. Narrowed to three
// methods so the handlers can be tested against a stub instead of a container —
// and so it is obvious at a glance which endpoints touch the database at all
// (only /locate and the two series endpoints; everything else is served from
// the snapshot).
type DataSource interface {
	AreaAtPoint(ctx context.Context, lon, lat float64) (string, error)
	SensorSeries(ctx context.Context, sensorID int64, metric string, since time.Time, hourly bool) ([]store.Point, error)
	AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool) ([]store.Point, error)
}

type Deps struct {
	Snapshots *snapshot.Holder
	Breadth   *ratelimit.Breadth
	Store     DataSource
	BaseURL   string
}

// Cache lifetimes, in seconds.
//
// 150 s — half the 300 s poll interval — is deliberate. A max-age equal to the
// poll interval means a copy cached one second after a rebuild is served until
// one second after the NEXT rebuild: nearly two full cycles of staleness. Half
// the interval bounds worst-case staleness at one cycle.
//
// scalesMaxAge is long because the EAQI bands are legislation, not measurements.
const (
	dataMaxAge   = 150
	scalesMaxAge = 86400
)

func NewRouter(d Deps) *http.ServeMux {
	mux := http.NewServeMux()

	// Method-qualified patterns, so ServeMux answers 405 for anything else
	// without a per-handler check.
	mux.HandleFunc("GET /api/v1/overview", d.handleOverview)
	mux.HandleFunc("GET /api/v1/areas", d.handleAreas)
	mux.HandleFunc("GET /api/v1/meta", d.handleMeta)
	mux.HandleFunc("GET /api/v1/scales", d.handleScales)
	mux.HandleFunc("GET /api/v1/area/{slug}/sensors", d.handleAreaSensors)
	mux.HandleFunc("GET /api/v1/area/{slug}/series", d.handleAreaSeries)
	mux.HandleFunc("GET /api/v1/sensor/{id}/series", d.handleSensorSeries)
	mux.HandleFunc("GET /api/v1/locate", d.handleLocate)

	// Phase 1 §7.4's partner API is deferred to Phase 4. The path is reserved
	// now so the version namespace cannot be taken by anything else, and it
	// answers a truthful 501 rather than a 404 that would suggest the design
	// never existed.
	mux.HandleFunc("/api/partner/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotImplemented, "not_implemented",
			"The partner API is not available yet.")
	})

	return mux
}

// errorBody is the single failure envelope. Fixed code, fixed sentence — never a
// Go error string, which would leak table names, file paths and driver
// internals to anyone who can provoke a failure.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Never let an error response be cached: a transient 503 cached for even a
	// few minutes turns a blip into an outage for everyone behind that cache.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: code, Message: message})
}

func writeUnavailable(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "30")
	writeError(w, http.StatusServiceUnavailable, "unavailable",
		"Data is not ready yet. Please try again shortly.")
}

// serveBody writes one prepared snapshot body, handling revalidation and
// content coding.
func serveBody(w http.ResponseWriter, r *http.Request, b snapshot.Body, maxAge int) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("ETag", b.ETag)
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(maxAge))
	// Vary is mandatory once the body varies by Accept-Encoding: without it a
	// shared cache may hand a gzip body to a client that never asked for one.
	h.Set("Vary", "Accept-Encoding")

	// Compare against every tag the client offers, and honour "*". Substring
	// matching would be wrong in the other direction too — a stale tag that
	// happens to contain the current one would produce a spurious 304.
	if matchesETag(r.Header.Get("If-None-Match"), b.ETag) {
		// 304 must carry no body; the headers above are the whole response.
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if acceptsGzip(r.Header.Get("Accept-Encoding")) && len(b.Gzip) > 0 {
		h.Set("Content-Encoding", "gzip")
		h.Set("Content-Length", strconv.Itoa(len(b.Gzip)))
		_, _ = w.Write(b.Gzip)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(b.JSON)))
	_, _ = w.Write(b.JSON)
}

func matchesETag(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		// A weak validator (W/"…") compares equal to the strong tag for the
		// weak comparison that If-None-Match uses.
		candidate = strings.TrimPrefix(candidate, "W/")
		if candidate == etag {
			return true
		}
	}
	return false
}

// acceptsGzip parses Accept-Encoding just far enough to honour an explicit
// q=0 refusal. "gzip;q=0" means "do not send me gzip", and a naive
// strings.Contains check reads it as consent.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding != "gzip" && coding != "*" {
			continue
		}
		for _, param := range fields[1:] {
			param = strings.ReplaceAll(strings.ToLower(param), " ", "")
			if strings.HasPrefix(param, "q=") {
				if q, err := strconv.ParseFloat(strings.TrimPrefix(param, "q="), 64); err == nil && q == 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}
```

- [ ] **Step 4: Write the scale tables**

Create `internal/api/scales.go`:

```go
package api

// The scale tables the frontend colours by. Data, not logic, and served from a
// static endpoint so a legislative change is a one-file edit rather than a
// frontend release.
//
// Sources:
//   - EAQI: European Environment Agency, European Air Quality Index bands for
//     PM10 and PM2.5 (24-hour running mean).
//   - EU limit values: Directive 2008/50/EC — PM10 50 µg/m³ daily,
//     PM2.5 25 µg/m³ annual.
//   - WHO: 2021 Global Air Quality Guidelines — PM10 45 µg/m³ 24-hour,
//     PM2.5 15 µg/m³ 24-hour.
//
// A sensor.community reading is a ~2.5-minute mean from a low-cost nephelometer,
// not a 24-hour reference-method measurement. Bands are therefore INDICATIVE and
// every consumer must say so — Phase 1 §9.2 requires the disclaimer on the page.

type Band struct {
	Label   string   `json:"label"`
	LabelBG string   `json:"label_bg"`
	// Upper is the inclusive top of the band, or nil for the open-ended top
	// band. A sentinel like 9999 would be a real number a caller could plot.
	Upper   *float64 `json:"upper"`
	Colour  string   `json:"colour"`
}

type Scale struct {
	Name    string           `json:"name"`
	Metric  string           `json:"metric"`
	Unit    string           `json:"unit"`
	Bands   []Band           `json:"bands"`
	Notes   string           `json:"notes"`
	NotesBG string           `json:"notes_bg"`
}

func upper(v float64) *float64 { return &v }

// Scales returns every scale table. Recomputed per call rather than shared as a
// package var, because the Band values contain pointers: a shared slice would
// let a caller mutate the table other callers read.
func Scales() []Scale {
	eaqiPM25 := []Band{
		{Label: "Good", LabelBG: "Добро", Upper: upper(5), Colour: "#50f0e6"},
		{Label: "Fair", LabelBG: "Задоволително", Upper: upper(10), Colour: "#50ccaa"},
		{Label: "Moderate", LabelBG: "Умерено", Upper: upper(20), Colour: "#f0e641"},
		{Label: "Poor", LabelBG: "Лошо", Upper: upper(25), Colour: "#ff5050"},
		{Label: "Very poor", LabelBG: "Много лошо", Upper: upper(50), Colour: "#960032"},
		{Label: "Extremely poor", LabelBG: "Изключително лошо", Upper: nil, Colour: "#7d2181"},
	}
	eaqiPM10 := []Band{
		{Label: "Good", LabelBG: "Добро", Upper: upper(20), Colour: "#50f0e6"},
		{Label: "Fair", LabelBG: "Задоволително", Upper: upper(40), Colour: "#50ccaa"},
		{Label: "Moderate", LabelBG: "Умерено", Upper: upper(50), Colour: "#f0e641"},
		{Label: "Poor", LabelBG: "Лошо", Upper: upper(100), Colour: "#ff5050"},
		{Label: "Very poor", LabelBG: "Много лошо", Upper: upper(150), Colour: "#960032"},
		{Label: "Extremely poor", LabelBG: "Изключително лошо", Upper: nil, Colour: "#7d2181"},
	}

	const indicative = "Low-cost sensor readings are indicative and are not " +
		"reference-method measurements."
	const indicativeBG = "Данните от нискобюджетни сензори са индикативни и не " +
		"са измервания по референтен метод."

	return []Scale{
		{Name: "eaqi", Metric: "P2", Unit: "µg/m³", Bands: eaqiPM25,
			Notes: "European Air Quality Index bands for PM2.5. " + indicative,
			NotesBG: "Класове на Европейския индекс за качество на въздуха за ПМ2.5. " + indicativeBG},
		{Name: "eaqi", Metric: "P1", Unit: "µg/m³", Bands: eaqiPM10,
			Notes: "European Air Quality Index bands for PM10. " + indicative,
			NotesBG: "Класове на Европейския индекс за качество на въздуха за ПМ10. " + indicativeBG},
		{Name: "eu_limit", Metric: "P1", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the EU daily limit", LabelBG: "В рамките на дневната норма на ЕС", Upper: upper(50), Colour: "#50ccaa"},
				{Label: "Above the EU daily limit", LabelBG: "Над дневната норма на ЕС", Upper: nil, Colour: "#ff5050"},
			},
			Notes: "Directive 2008/50/EC: PM10 daily limit 50 µg/m³. " + indicative,
			NotesBG: "Директива 2008/50/ЕО: дневна норма за ПМ10 50 µg/m³. " + indicativeBG},
		{Name: "eu_limit", Metric: "P2", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the EU annual limit", LabelBG: "В рамките на годишната норма на ЕС", Upper: upper(25), Colour: "#50ccaa"},
				{Label: "Above the EU annual limit", LabelBG: "Над годишната норма на ЕС", Upper: nil, Colour: "#ff5050"},
			},
			Notes: "Directive 2008/50/EC: PM2.5 annual limit 25 µg/m³. " + indicative,
			NotesBG: "Директива 2008/50/ЕО: годишна норма за ПМ2.5 25 µg/m³. " + indicativeBG},
		{Name: "who", Metric: "P1", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the WHO 24-hour guideline", LabelBG: "В рамките на 24-часовата насока на СЗО", Upper: upper(45), Colour: "#50ccaa"},
				{Label: "Above the WHO 24-hour guideline", LabelBG: "Над 24-часовата насока на СЗО", Upper: nil, Colour: "#ff5050"},
			},
			Notes: "WHO 2021 guidelines: PM10 24-hour 45 µg/m³. " + indicative,
			NotesBG: "Насоки на СЗО 2021: ПМ10 за 24 часа 45 µg/m³. " + indicativeBG},
		{Name: "who", Metric: "P2", Unit: "µg/m³",
			Bands: []Band{
				{Label: "Within the WHO 24-hour guideline", LabelBG: "В рамките на 24-часовата насока на СЗО", Upper: upper(15), Colour: "#50ccaa"},
				{Label: "Above the WHO 24-hour guideline", LabelBG: "Над 24-часовата насока на СЗО", Upper: nil, Colour: "#ff5050"},
			},
			Notes: "WHO 2021 guidelines: PM2.5 24-hour 15 µg/m³. " + indicative,
			NotesBG: "Насоки на СЗО 2021: ПМ2.5 за 24 часа 15 µg/m³. " + indicativeBG},
	}
}
```

- [ ] **Step 5: Write the scales test**

Create `internal/api/scales_test.go`:

```go
package api_test

import (
	"testing"

	"airbg.org/internal/api"
)

// TestScaleBandsAreMonotonic. Bands out of order, or with a repeated upper
// bound, would silently mis-colour readings: a lookup walking the slice returns
// the first match, so a low band placed after a high one is never reached.
func TestScaleBandsAreMonotonic(t *testing.T) {
	for _, s := range api.Scales() {
		if len(s.Bands) < 2 {
			t.Errorf("%s/%s: %d bands, want at least 2", s.Name, s.Metric, len(s.Bands))
			continue
		}
		prev := -1.0
		for i, b := range s.Bands {
			if b.Upper == nil {
				if i != len(s.Bands)-1 {
					t.Errorf("%s/%s: band %d is open-ended but is not last; every band after it is unreachable", s.Name, s.Metric, i)
				}
				continue
			}
			if *b.Upper <= prev {
				t.Errorf("%s/%s: band %d upper %v is not above the previous %v", s.Name, s.Metric, i, *b.Upper, prev)
			}
			prev = *b.Upper
		}
		if last := s.Bands[len(s.Bands)-1]; last.Upper != nil {
			t.Errorf("%s/%s: the last band has an upper bound of %v; a reading above it would fall into no band at all", s.Name, s.Metric, *last.Upper)
		}
	}
}

// TestScalesAreBilingualAndCarryTheDisclaimer. Phase 1 §9.2 requires the
// indicative-data disclaimer wherever a value is shown; shipping it with the
// scale means a consumer cannot render bands without also having the caveat.
func TestScalesAreBilingualAndCarryTheDisclaimer(t *testing.T) {
	for _, s := range api.Scales() {
		if s.Notes == "" || s.NotesBG == "" {
			t.Errorf("%s/%s: notes missing (en=%q bg=%q)", s.Name, s.Metric, s.Notes, s.NotesBG)
		}
		if s.Unit == "" {
			t.Errorf("%s/%s: unit is empty", s.Name, s.Metric)
		}
		for i, b := range s.Bands {
			if b.Label == "" || b.LabelBG == "" {
				t.Errorf("%s/%s band %d: a label is empty (en=%q bg=%q)", s.Name, s.Metric, i, b.Label, b.LabelBG)
			}
			if len(b.Colour) != 7 || b.Colour[0] != '#' {
				t.Errorf("%s/%s band %d: colour %q is not a #rrggbb hex string", s.Name, s.Metric, i, b.Colour)
			}
		}
	}
}

// TestScalesReturnsIndependentCopies: the Upper fields are pointers, so a shared
// package-level slice would let one caller's mutation change what every other
// caller reads — including the JSON the API has already promised.
func TestScalesReturnsIndependentCopies(t *testing.T) {
	a, b := api.Scales(), api.Scales()
	if a[0].Bands[0].Upper == b[0].Bands[0].Upper {
		t.Error("two calls returned the same *float64; Scales must not share mutable state")
	}
}

// TestScalesCoverBothParticulateMetrics.
func TestScalesCoverBothParticulateMetrics(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range api.Scales() {
		seen[s.Name+"/"+s.Metric] = true
	}
	for _, want := range []string{"eaqi/P1", "eaqi/P2", "eu_limit/P1", "eu_limit/P2", "who/P1", "who/P2"} {
		if !seen[want] {
			t.Errorf("missing scale %s", want)
		}
	}
}
```

- [ ] **Step 6: Run the router and scales tests**

The router tests will still fail — the handlers land in Tasks 11–13. Run the scales tests now:

```bash
go test ./internal/api/ -run TestScale -v
```

Expected: FAIL to build until Task 11 supplies the handler methods. That is intended: Tasks 10–13 form one compile unit, so **Task 10 has no green checkpoint of its own** and commits its files without a passing run. Note this in the commit message so a reviewer is not surprised.

- [ ] **Step 7: Commit**

```bash
git add internal/api/router.go internal/api/scales.go internal/api/router_test.go internal/api/scales_test.go
git commit -m "feat(api): add the route table, error envelope and scale tables

The package does not compile until the handlers land in the next tasks;
routes and handlers are split for reviewability, not to be run in between."
```

---

## Task 11: Overview, areas, meta, scales and sensor handlers

**Files:**
- Create: `internal/api/overview.go`
- Create: `internal/api/sensors.go`
- Create: `internal/api/handlers_test.go`

**Interfaces:**
- Consumes: `Deps`, `serveBody`, `writeError`, `writeUnavailable`, `Scales()`, `snapshot.Snapshot`, `ratelimit.Breadth`, `httpx.BucketKeyFrom`.
- Produces: methods on `Deps` — `handleOverview`, `handleAreas`, `handleMeta`, `handleScales`, `handleAreaSensors`.

- [ ] **Step 1: Write the failing test**

Create `internal/api/handlers_test.go`:

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/httpx"
	"airbg.org/internal/ratelimit"
)

// serve wraps the mux in WithClientIP so BucketKeyFrom resolves, which is how
// the enumeration counters key. Without it every test client would share the
// "unattributed" key and the breadth tests would interfere with each other.
func serve(t *testing.T, d api.Deps, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	res, err := httpx.NewIPResolver(nil)
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	h := httpx.WithClientIP(api.NewRouter(d), res)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(path, clientIP string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = clientIP + ":41000"
	return r
}

func TestOverviewServesTheCountryTierByDefault(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/overview", "203.0.113.1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != `{"areas":[{"slug":"sofia"}]}` {
		t.Errorf("body = %q, want the country tier", got)
	}
}

func TestOverviewTierCityServesTheRegionalTier(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/overview?tier=city", "203.0.113.2"))

	if got := rec.Body.String(); got != `{"areas":[{"slug":"sofia-center"}]}` {
		t.Errorf("body = %q, want the city tier", got)
	}
}

// TestOverviewRejectsUnknownTier: an unrecognised tier must be a 400, not a
// silent fall back to the country tier. Silently substituting a different answer
// than the one asked for is how a frontend bug becomes invisible.
func TestOverviewRejectsUnknownTier(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/overview?tier=street", "203.0.113.3"))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for tier=street", rec.Code)
	}
}

// TestOverviewTakesNoBoundingBox is the anti-extraction invariant from Phase 1
// §7.1, asserted rather than assumed. A bbox parameter would let a caller walk
// the country in a loop, which is the single change that would undo the entire
// tiering design — so it gets a test that fails the moment someone adds one.
func TestOverviewTakesNoBoundingBox(t *testing.T) {
	fix := fixture(t)
	plain := serve(t, deps(t, fix), get("/api/v1/overview", "203.0.113.4"))
	withBBox := serve(t, deps(t, fix), get("/api/v1/overview?bbox=22,41,29,45", "203.0.113.5"))

	if plain.Body.String() != withBBox.Body.String() {
		t.Error("a bbox parameter changed the response; /overview must never accept spatial filtering")
	}
}

func TestMetaReportsGeneratedAtAndCoverage(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/meta", "203.0.113.6"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		GeneratedAt        time.Time `json:"generated_at"`
		CoverageThreshold  int       `json:"coverage_threshold"`
		Attribution        string    `json:"attribution"`
		BoundaryAttribution string   `json:"boundary_attribution"`
		Metrics            []string  `json:"metrics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if got.GeneratedAt.IsZero() {
		t.Error("generated_at is zero; a client cannot tell how stale the data is")
	}
	if got.CoverageThreshold != 3 {
		t.Errorf("coverage_threshold = %d, want 3", got.CoverageThreshold)
	}
	// Both attributions are licence obligations, not decoration: sensor.community
	// data is ODbL and the OSM boundaries are ODbL. Omitting either is a licence
	// breach, so it is asserted rather than left to the template.
	if got.Attribution == "" {
		t.Error("attribution is empty")
	}
	if got.BoundaryAttribution == "" {
		t.Error("boundary_attribution is empty")
	}
	if len(got.Metrics) != 7 {
		t.Errorf("metrics has %d entries, want the 7 canonical metrics", len(got.Metrics))
	}
}

func TestAreasListsEveryKnownArea(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/areas", "203.0.113.7"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag on /areas")
	}
}

func TestScalesEndpointServesTheTables(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/scales", "203.0.113.8"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []api.Scale
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != len(api.Scales()) {
		t.Errorf("got %d scales, want %d", len(got), len(api.Scales()))
	}
	// Long-lived cache: the bands are legislation, not measurements.
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Error("no Cache-Control on /scales")
	}
}

func TestAreaSensorsServesTheColumnarBody(t *testing.T) {
	rec := serve(t, deps(t, fixture(t)), get("/api/v1/area/sofia/sensors", "203.0.113.9"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"sensors":{"id":[1]}}` {
		t.Errorf("body = %q", got)
	}
}

// TestAreaSensorsEnumerationTrips: after DistinctAreaLimit distinct slugs from
// one client, further distinct slugs must be refused. This is the breadth check
// wired into a real request path, not just the counter in isolation.
func TestAreaSensorsEnumerationTrips(t *testing.T) {
	fix := fixture(t)
	// Populate enough known areas that the limit is reachable.
	for i := 0; i < ratelimit.DistinctAreaLimit+2; i++ {
		slug := "area-" + string(rune('a'+i))
		fix.KnownSlugs[slug] = fix.KnownSlugs["sofia"]
		fix.AreaSensors[slug] = fix.AreaSensors["sofia"]
	}
	d := deps(t, fix)

	allowed, refused := 0, 0
	for i := 0; i < ratelimit.DistinctAreaLimit+2; i++ {
		slug := "area-" + string(rune('a'+i))
		rec := serve(t, d, get("/api/v1/area/"+slug+"/sensors", "203.0.113.10"))
		switch rec.Code {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			refused++
			if rec.Header().Get("Retry-After") == "" {
				t.Error("the enumeration 429 has no Retry-After")
			}
		default:
			t.Fatalf("%s: status = %d", slug, rec.Code)
		}
	}
	if allowed != ratelimit.DistinctAreaLimit {
		t.Errorf("allowed %d distinct areas, want %d", allowed, ratelimit.DistinctAreaLimit)
	}
	if refused != 2 {
		t.Errorf("refused %d requests, want 2", refused)
	}
}

// TestEnumerationCheckRunsBeforeTheBodyIsWritten: the refusal must not send the
// data first. A check that answers 429 after already writing the payload has
// leaked exactly what it was there to withhold.
func TestEnumerationCheckRunsBeforeTheBodyIsWritten(t *testing.T) {
	fix := fixture(t)
	d := api.Deps{
		Snapshots: deps(t, fix).Snapshots,
		// A limit of zero trips on the very first request.
		Breadth: ratelimit.NewBreadth(0, 0, time.Hour),
		Store:   stubSource{slug: "sofia"},
		BaseURL: "https://airbg.org",
	}

	rec := serve(t, d, get("/api/v1/area/sofia/sensors", "203.0.113.11"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if body := rec.Body.String(); body == `{"sensors":{"id":[1]}}` {
		t.Fatal("the 429 response carried the sensor payload")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/api/ -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the overview, areas, meta and scales handlers**

Create `internal/api/overview.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// Attribution strings. Both are licence obligations under ODbL, not niceties.
const (
	DataAttribution     = "Data from sensor.community contributors, ODbL 1.0"
	BoundaryAttribution = "Boundaries © OpenStreetMap contributors, ODbL 1.0"
)

// handleOverview serves one choropleth tier.
//
// There is deliberately no bounding-box parameter. The tier is the ONLY spatial
// control a caller has, and that is the whole anti-extraction design from Phase
// 1 §7.1: a bbox would let a scraper walk the country in a loop, and no rate
// limit distinguishes that from normal panning.
func (d Deps) handleOverview(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	switch r.URL.Query().Get("tier") {
	case "", "country":
		serveBody(w, r, snap.Overview, dataMaxAge)
	case "city":
		serveBody(w, r, snap.OverviewCity, dataMaxAge)
	default:
		// Explicit 400 rather than falling back to the country tier: quietly
		// answering a different question than the one asked hides frontend bugs
		// and makes the API's contract untestable.
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "tier" parameter must be "country" or "city".`)
	}
}

func (d Deps) handleAreas(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}
	serveBody(w, r, snap.Areas, dataMaxAge)
}

type metaBody struct {
	GeneratedAt         time.Time `json:"generated_at"`
	CoverageThreshold   int       `json:"coverage_threshold"`
	Metrics             []string  `json:"metrics"`
	AreaCount           int       `json:"area_count"`
	CoveredAreaCount    int       `json:"covered_area_count"`
	Attribution         string    `json:"attribution"`
	BoundaryAttribution string    `json:"boundary_attribution"`
	Disclaimer          string    `json:"disclaimer"`
}

// handleMeta tells a client how to interpret everything else: when the data was
// built, what the coverage rule is, which metrics exist, and who to credit.
//
// covered_area_count next to area_count is the honest pair. Reporting only the
// total would let a UI imply the whole country is measured when most oblasti sit
// below the 3-sensor threshold.
func (d Deps) handleMeta(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	covered := 0
	for _, m := range snap.KnownSlugs {
		if m.Covered {
			covered++
		}
	}

	body, err := json.Marshal(metaBody{
		GeneratedAt:         snap.GeneratedAt,
		CoverageThreshold:   store.CoverageThreshold,
		Metrics:             upstream.CanonicalMetrics(),
		AreaCount:           len(snap.KnownSlugs),
		CoveredAreaCount:    covered,
		Attribution:         DataAttribution,
		BoundaryAttribution: BoundaryAttribution,
		Disclaimer: "Low-cost sensor readings are indicative and are not " +
			"reference-method measurements.",
	})
	if err != nil {
		// Marshalling fixed-shape structs cannot realistically fail, but
		// swallowing the error would send a 200 with an empty body.
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(dataMaxAge))
	_, _ = w.Write(body)
}

func (d Deps) handleScales(w http.ResponseWriter, r *http.Request) {
	body, err := json.Marshal(Scales())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(scalesMaxAge))
	_, _ = w.Write(body)
}
```

- [ ] **Step 4: Write the sensors handler**

Create `internal/api/sensors.go`:

```go
package api

import (
	"net/http"

	"airbg.org/internal/httpx"
	"airbg.org/internal/metrics"
)

var enumerationTrips = metrics.CounterVec(
	"airbg_enumeration_trips_total",
	"Requests refused by the enumeration-breadth check, by dimension.",
	"dimension")

// handleAreaSensors serves the sensor detail for one area.
//
// This is the only endpoint that returns sensor coordinates, which makes it the
// one worth extracting — so it carries the breadth check.
func (d Deps) handleAreaSensors(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	slug := r.PathValue("slug")

	// Validate against the snapshot's known slugs BEFORE observing. Counting an
	// unknown slug would let a caller exhaust their own area budget with
	// garbage, and — worse — would make the breadth counter trivially
	// pollutable by anyone wanting to trip a shared CGNAT address on purpose.
	body, ok := snap.AreaSensors[slug]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such area.")
		return
	}

	// The check runs before anything is written. A refusal that has already
	// sent the payload has leaked precisely what it was withholding.
	if !d.Breadth.ObserveArea(httpx.BucketKeyFrom(r.Context()), slug) {
		enumerationTrips.With("area").Inc()
		writeTooManyAreas(w)
		return
	}

	serveBody(w, r, body, dataMaxAge)
}

// writeTooManyAreas answers an enumeration trip.
//
// The message says what happened without naming the threshold: publishing the
// exact limit tells a scraper precisely how to pace itself just under it.
// Retry-After is generous because the window is an hour and the alternative —
// a tight retry — invites a client to hammer the refusal.
func writeTooManyAreas(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "900")
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		"Too many different areas requested. Please slow down.")
}

func writeTooManySensors(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "900")
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		"Too many different sensors requested. Please slow down.")
}
```

- [ ] **Step 5: Run the tests**

The package still will not compile until Task 12 adds `handleAreaSeries`, `handleSensorSeries` and Task 13 adds `handleLocate` — all three are referenced by `NewRouter`. To get a green checkpoint here, add a temporary file `internal/api/todo_stubs.go` containing only:

```go
package api

import "net/http"

// Temporary. Task 12 replaces handleAreaSeries and handleSensorSeries; Task 13
// replaces handleLocate. Delete this file in Task 13.
func (d Deps) handleAreaSeries(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented.")
}

func (d Deps) handleSensorSeries(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented.")
}

func (d Deps) handleLocate(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "Not implemented.")
}
```

Then:

```bash
go test ./internal/api/ -race -v -count=1
```

Expected: PASS for every test except `TestErrorResponsesShareOneShape`'s `/api/v1/sensor/abc/series` case, which expects 400 and gets 501 from the stub. Comment that one case out with the marker `// TASK-12: restore` and restore it in Task 12 Step 4.

- [ ] **Step 6: Prove the anti-extraction and ordering tests are not tautologies**

Add a `bbox` query parameter that filters `snap.Overview` and confirm `TestOverviewTakesNoBoundingBox` FAILS. Revert. Move the `d.Breadth.ObserveArea` check to AFTER `serveBody` and confirm `TestEnumerationCheckRunsBeforeTheBodyIsWritten` FAILS. Revert. Move the `ObserveArea` call ABOVE the `snap.AreaSensors[slug]` lookup and confirm `TestAreaSensorsEnumerationTrips` still passes but `TestUnknownSlugIsNotFoundNotEmpty` now consumes budget — verify by asserting the count, then revert.

- [ ] **Step 7: Commit**

```bash
git add internal/api/overview.go internal/api/sensors.go internal/api/handlers_test.go internal/api/todo_stubs.go
git commit -m "feat(api): serve the overview, meta, scales and area-sensor endpoints"
```

---

## Task 12: Time-series endpoints

The only endpoints that query the database per request — a series is per-sensor and unbounded, so it cannot live in the snapshot. That makes them the ones worth bounding hardest: a fixed period vocabulary, a validated metric, and the breadth counter on sensor IDs.

**Files:**
- Create: `internal/api/series.go`
- Create: `internal/api/series_test.go`
- Modify: `internal/store/aggregate.go` (add `AreaSeries`)
- Modify: `internal/store/aggregate_test.go` (add `TestAreaSeriesAveragesAcrossSensors`)
- Delete: the `handleAreaSeries` and `handleSensorSeries` stubs from `internal/api/todo_stubs.go`

**Interfaces:**
- Consumes: `DataSource.SensorSeries`, `DataSource.AreaSeries`, `store.Point`, `ratelimit.Breadth.ObserveSensor`, `upstream.IsCanonicalMetric`.
- Produces:
  - `func (s *Store) AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool) ([]Point, error)`
  - methods on `Deps` — `handleSensorSeries`, `handleAreaSeries`
  - `func parsePeriod(v string) (window time.Duration, hourly bool, ok bool)`

- [ ] **Step 1: Write the failing store test**

Append to `internal/store/aggregate_test.go`:

```go
// TestAreaSeriesAveragesAcrossSensors: the area series is the mean of the
// sensors in the area at each instant, not a concatenation of their readings.
// Concatenating would produce a sawtooth that looks like violent air-quality
// swings but is really just sensors disagreeing — the most misleading possible
// chart to publish under a public-health banner.
func TestAreaSeriesAveragesAcrossSensors(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	for i, v := range []float64{10, 30} {
		id := int64(700 + i)
		seedSensor(t, ctx, pool, id, 23.3219+float64(i)*0.001, 42.6977)
		seedSensorReading(t, ctx, pool, id, "P2", v, base, "ok")
	}
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 (two sensors at one instant must average into one point)", len(points))
	}
	if points[0].Value != 20 {
		t.Errorf("value = %v, want 20 (the mean of 10 and 30)", points[0].Value)
	}
}

// TestAreaSeriesExcludesFlaggedReadings: the same quality filter the aggregates
// use must apply here, or a chart shows values the map refuses to.
func TestAreaSeriesExcludesFlaggedReadings(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	seedArea(t, ctx, pool, "sofia", "oblast", 23.3219, 42.6977)
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)

	seedSensor(t, ctx, pool, 800, 23.3219, 42.6977)
	seedSensorReading(t, ctx, pool, 800, "P2", 10, base, "ok")
	seedSensor(t, ctx, pool, 801, 23.3229, 42.6977)
	// A stuck sensor pegged at 1000. If the filter is dropped the mean becomes
	// 505 rather than 10 — a 50× error, impossible to miss.
	seedSensorReading(t, ctx, pool, 801, "P2", 1000, base, "stuck")
	assignAreas(t, ctx, pool)

	points, err := s.AreaSeries(ctx, "sofia", "P2", base.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 10 {
		t.Errorf("value = %v, want 10; a flagged reading was included", points[0].Value)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/store/ -run TestAreaSeries -v
```

Expected: FAIL to compile — `s.AreaSeries undefined`.

- [ ] **Step 3: Implement AreaSeries**

Append to `internal/store/aggregate.go`:

```go
// areaRawSeriesSQL averages across the area's sensors at each timestamp.
//
// Grouped by time so two sensors reporting at the same instant produce ONE
// point. Without the grouping the result is every sensor's reading in
// timestamp order, which renders as a sawtooth that a reader would interpret as
// rapid air-quality swings rather than as sensors disagreeing.
const areaRawSeriesSQL = `
SELECT r.time, avg(r.value)
  FROM reading r
  JOIN area_sensor asn ON asn.sensor_id = r.sensor_id
  JOIN area a          ON a.id = asn.area_id
 WHERE a.slug   = $1
   AND r.metric = $2
   AND r.time  >= $3
   AND r.quality = ANY($4::quality_flag[])
 GROUP BY r.time
 ORDER BY r.time`

// areaHourlySeriesSQL is the same over the rollup. reading_hourly carries no
// quality column — the rollup is built from readings that already passed the
// filter, so re-filtering here would be impossible AND unnecessary.
const areaHourlySeriesSQL = `
SELECT h.bucket, avg(h.avg_value)
  FROM reading_hourly h
  JOIN area_sensor asn ON asn.sensor_id = h.sensor_id
  JOIN area a          ON a.id = asn.area_id
 WHERE a.slug   = $1
   AND h.metric = $2
   AND h.bucket >= $3
 GROUP BY h.bucket
 ORDER BY h.bucket`

// AreaSeries returns the area-mean time series for one metric.
//
// hourly selects the rollup. The caller decides, because only the caller knows
// the requested window — and raw readings are retained for 30 days, so a longer
// window queried against `reading` returns a silently truncated series rather
// than an error.
func (s *Store) AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool) ([]Point, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if hourly {
		rows, err = s.pool.Query(ctx, areaHourlySeriesSQL, slug, metric, since)
	} else {
		rows, err = s.pool.Query(ctx, areaRawSeriesSQL, slug, metric, since, usableQuality)
	}
	if err != nil {
		return nil, fmt.Errorf("store: area series for %q: %w", slug, err)
	}
	defer rows.Close()

	var points []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Time, &p.Value); err != nil {
			return nil, fmt.Errorf("store: scan area series: %w", err)
		}
		points = append(points, p)
	}
	return points, rows.Err()
}
```

- [ ] **Step 4: Run the store tests**

```bash
go test ./internal/store/ -run TestAreaSeries -v -count=1
```

Expected: PASS, both.

- [ ] **Step 5: Write the failing API test**

Create `internal/api/series_test.go`:

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/store"
)

func withPoints(t *testing.T, points []store.Point) api.Deps {
	t.Helper()
	d := deps(t, fixture(t))
	d.Store = stubSource{slug: "sofia", points: points}
	return d
}

func samplePoints() []store.Point {
	base := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	return []store.Point{
		{Time: base, Value: 12.5},
		{Time: base.Add(time.Hour), Value: 14},
	}
}

func TestSensorSeriesReturnsPoints(t *testing.T) {
	rec := serve(t, withPoints(t, samplePoints()),
		get("/api/v1/sensor/42/series?metric=P2&period=24h", "203.0.113.20"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		SensorID int64     `json:"sensor_id"`
		Metric   string    `json:"metric"`
		Period   string    `json:"period"`
		Times    []string  `json:"t"`
		Values   []float64 `json:"v"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
	}
	if got.SensorID != 42 {
		t.Errorf("sensor_id = %d, want 42", got.SensorID)
	}
	// Columnar, same as the sensor payload — and the two columns must be the
	// same length or the points do not line up.
	if len(got.Times) != 2 || len(got.Values) != 2 {
		t.Fatalf("t has %d entries and v has %d, want 2 each", len(got.Times), len(got.Values))
	}
	if got.Values[0] != 12.5 {
		t.Errorf("v[0] = %v, want 12.5", got.Values[0])
	}
}

// TestSeriesRejectsUnknownMetric: the metric reaches a WHERE clause. It is
// validated against the canonical set, so no caller-supplied string is ever
// interpolated — and an unrecognised one is a 400, not an empty 200 that would
// read as "this sensor measures nothing".
func TestSeriesRejectsUnknownMetric(t *testing.T) {
	for _, metric := range []string{"", "durP1", "P2; DROP TABLE reading", "../P2"} {
		rec := serve(t, withPoints(t, samplePoints()),
			get("/api/v1/sensor/42/series?metric="+metric+"&period=24h", "203.0.113.21"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("metric=%q: status = %d, want 400", metric, rec.Code)
		}
	}
}

// TestSeriesRejectsUnknownPeriod: a fixed vocabulary, not a free-form duration.
// An arbitrary window lets a caller request ten years of raw readings and make
// the database do unbounded work — one request, no rate limit triggered.
func TestSeriesRejectsUnknownPeriod(t *testing.T) {
	for _, period := range []string{"", "99y", "1s", "forever", "-24h"} {
		rec := serve(t, withPoints(t, samplePoints()),
			get("/api/v1/sensor/42/series?metric=P2&period="+period, "203.0.113.22"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("period=%q: status = %d, want 400", period, rec.Code)
		}
	}
}

func TestSeriesRejectsNonNumericSensorID(t *testing.T) {
	rec := serve(t, withPoints(t, samplePoints()),
		get("/api/v1/sensor/abc/series?metric=P2&period=24h", "203.0.113.23"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestLongPeriodsUseTheRollup. Raw readings are retained 30 days; a 1-year
// window against `reading` would return the last 30 days and label it a year —
// a chart that is wrong without being empty, which is the hardest kind to catch.
func TestLongPeriodsUseTheRollup(t *testing.T) {
	cases := map[string]bool{"24h": false, "7d": false, "30d": false, "1y": true}
	for period, wantHourly := range cases {
		_, hourly, ok := api.ParsePeriodForTesting(period)
		if !ok {
			t.Errorf("period %q was rejected", period)
			continue
		}
		if hourly != wantHourly {
			t.Errorf("period %q: hourly = %v, want %v", period, hourly, wantHourly)
		}
	}
}

// TestEmptySeriesIsTwoEmptyArraysNotNull: `null` and `[]` are different values
// to every JSON consumer, and a chart library handed null throws rather than
// drawing an empty axis.
func TestEmptySeriesIsTwoEmptyArraysNotNull(t *testing.T) {
	rec := serve(t, withPoints(t, nil),
		get("/api/v1/sensor/42/series?metric=P2&period=24h", "203.0.113.24"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"t", "v"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
}

// TestSensorSeriesEnumerationTrips: the sensor dimension of the breadth check.
func TestSensorSeriesEnumerationTrips(t *testing.T) {
	d := withPoints(t, samplePoints())
	d.Breadth = ratelimit.NewBreadth(100, 3, time.Hour)

	allowed := 0
	for id := 1; id <= 5; id++ {
		rec := serve(t, d, get("/api/v1/sensor/"+itoa(id)+"/series?metric=P2&period=24h", "203.0.113.25"))
		if rec.Code == http.StatusOK {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("allowed %d distinct sensors, want 3", allowed)
	}
}

// TestSeriesDatabaseErrorIsNotLeaked: the message must be a fixed sentence. A
// pgx error carries the SQL and the table names.
func TestSeriesDatabaseErrorIsNotLeaked(t *testing.T) {
	d := deps(t, fixture(t))
	d.Store = stubSource{err: errBoom}

	rec := serve(t, d, get("/api/v1/sensor/42/series?metric=P2&period=24h", "203.0.113.26"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "boom") || strings.Contains(body, "reading") {
		t.Errorf("the database error leaked into the response: %s", body)
	}
}

func TestAreaSeriesRequiresAKnownSlug(t *testing.T) {
	rec := serve(t, withPoints(t, samplePoints()),
		get("/api/v1/area/atlantis/series?metric=P2&period=24h", "203.0.113.27"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
```

Add to the top of the file: `"errors"`, `"strconv"`, `"strings"`, plus

```go
var errBoom = errors.New("boom: pq: relation \"reading\" does not exist")

func itoa(i int) string { return strconv.Itoa(i) }
```

- [ ] **Step 6: Run it to verify it fails**

```bash
go test ./internal/api/ -run 'Series|Period' -v
```

Expected: FAIL to build — `api.ParsePeriodForTesting` undefined, and the stubs return 501.

- [ ] **Step 7: Write the series handlers**

Create `internal/api/series.go`:

```go
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"airbg.org/internal/httpx"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

// The period vocabulary. Fixed rather than free-form on purpose: an arbitrary
// duration lets one request ask for ten years of raw readings, which is
// unbounded database work that no rate limiter catches, because it is a single
// request.
//
// The hourly flag is not a performance hint — it is a correctness requirement.
// Raw readings are retained for 30 days (ingest.RawRetentionHours), so a 1-year
// window against `reading` silently returns the last 30 days under a "1 year"
// label: a chart that is wrong without being empty.
var periods = map[string]struct {
	window time.Duration
	hourly bool
}{
	"24h": {24 * time.Hour, false},
	"7d":  {7 * 24 * time.Hour, false},
	"30d": {30 * 24 * time.Hour, false},
	"1y":  {365 * 24 * time.Hour, true},
}

func parsePeriod(v string) (time.Duration, bool, bool) {
	p, ok := periods[v]
	return p.window, p.hourly, ok
}

// ParsePeriodForTesting exposes parsePeriod so the raw/hourly cut-over can be
// asserted directly. Testing it only through the handler would leave the
// hourly flag verified by nothing — the stub returns the same points either way.
func ParsePeriodForTesting(v string) (time.Duration, bool, bool) { return parsePeriod(v) }

// seriesBody is columnar for the same reasons as the sensor payload: uPlot
// (Phase 3) consumes parallel arrays directly, and same-typed adjacent values
// compress well.
type seriesBody struct {
	SensorID *int64      `json:"sensor_id,omitempty"`
	Slug     string      `json:"slug,omitempty"`
	Metric   string      `json:"metric"`
	Period   string      `json:"period"`
	Hourly   bool        `json:"hourly"`
	Times    []time.Time `json:"t"`
	Values   []float64   `json:"v"`
}

// seriesRequest validates everything a series endpoint takes from the caller.
// Returning ok=false means a response has already been written.
func seriesRequest(w http.ResponseWriter, r *http.Request) (metric, period string, since time.Time, hourly, ok bool) {
	metric = r.URL.Query().Get("metric")
	// Validated against the canonical set. The value reaches a WHERE clause, so
	// this is also what guarantees no caller string is ever interpolated —
	// belt and braces alongside the parameterised query.
	if !upstream.IsCanonicalMetric(metric) {
		writeError(w, http.StatusBadRequest, "bad_request",
			"Unknown metric. Valid metrics are: "+joinComma(upstream.CanonicalMetrics())+".")
		return "", "", time.Time{}, false, false
	}

	period = r.URL.Query().Get("period")
	window, hourly, valid := parsePeriod(period)
	if !valid {
		writeError(w, http.StatusBadRequest, "bad_request",
			`The "period" parameter must be one of: 24h, 7d, 30d, 1y.`)
		return "", "", time.Time{}, false, false
	}

	return metric, period, time.Now().UTC().Add(-window), hourly, true
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func (d Deps) handleSensorSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "The sensor id must be a positive integer.")
		return
	}

	metric, period, since, hourly, ok := seriesRequest(w, r)
	if !ok {
		return
	}

	// Breadth check before the query, not after: a refused request must not
	// have cost a database round trip, or the refusal is the expensive path.
	if !d.Breadth.ObserveSensor(httpx.BucketKeyFrom(r.Context()), id) {
		enumerationTrips.With("sensor").Inc()
		writeTooManySensors(w)
		return
	}

	points, err := d.Store.SensorSeries(r.Context(), id, metric, since, hourly)
	if err != nil {
		// Logged with the detail, answered without it. A pgx error carries the
		// SQL text and table names.
		slog.Error("sensor series query failed", "sensor_id", id, "metric", metric, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	writeSeries(w, seriesBody{
		SensorID: &id, Metric: metric, Period: period, Hourly: hourly,
	}, points)
}

func (d Deps) handleAreaSeries(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	slug := r.PathValue("slug")
	if _, known := snap.KnownSlugs[slug]; !known {
		writeError(w, http.StatusNotFound, "not_found", "No such area.")
		return
	}

	metric, period, since, hourly, ok := seriesRequest(w, r)
	if !ok {
		return
	}

	if !d.Breadth.ObserveArea(httpx.BucketKeyFrom(r.Context()), slug) {
		enumerationTrips.With("area").Inc()
		writeTooManyAreas(w)
		return
	}

	points, err := d.Store.AreaSeries(r.Context(), slug, metric, since, hourly)
	if err != nil {
		slog.Error("area series query failed", "slug", slug, "metric", metric, "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	writeSeries(w, seriesBody{Slug: slug, Metric: metric, Period: period, Hourly: hourly}, points)
}

func writeSeries(w http.ResponseWriter, body seriesBody, points []store.Point) {
	// Allocated with make, not left nil: a nil slice marshals to `null`, and a
	// charting library handed null throws instead of drawing an empty axis.
	body.Times = make([]time.Time, 0, len(points))
	body.Values = make([]float64, 0, len(points))
	for _, p := range points {
		body.Times = append(body.Times, p.Time)
		body.Values = append(body.Values, p.Value)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(dataMaxAge))
	_, _ = w.Write(encoded)
}
```

- [ ] **Step 8: Remove the stubs and restore the commented-out case**

Delete `handleAreaSeries` and `handleSensorSeries` from `internal/api/todo_stubs.go`, leaving only `handleLocate`. Restore the `// TASK-12: restore` case in `router_test.go`.

- [ ] **Step 9: Run the tests**

```bash
go test ./internal/api/ ./internal/store/ -race -count=1
```

Expected: PASS.

- [ ] **Step 10: Prove the period and averaging tests are not tautologies**

Set `"1y"`'s `hourly` to `false` and confirm `TestLongPeriodsUseTheRollup` FAILS. Revert. Remove `GROUP BY r.time` and the `avg()` from `areaRawSeriesSQL` (select `r.value` directly) and confirm `TestAreaSeriesAveragesAcrossSensors` FAILS with 2 points instead of 1. Revert. Drop the `quality = ANY(...)` clause and confirm `TestAreaSeriesExcludesFlaggedReadings` FAILS with 505. Revert.

- [ ] **Step 11: Commit**

```bash
git add internal/api/series.go internal/api/series_test.go internal/api/todo_stubs.go internal/api/router_test.go internal/store/aggregate.go internal/store/aggregate_test.go
git commit -m "feat(api,store): serve sensor and area time series over a fixed period vocabulary"
```

---

## Task 13: /locate and the end of the stubs

Turns a visitor into a starting map view without asking for permission or storing anything.

**Files:**
- Create: `internal/api/locate.go`
- Create: `internal/api/locate_test.go`
- Create: `internal/store/locate.go`
- Create: `internal/store/locate_test.go`
- Delete: `internal/api/todo_stubs.go`

**Interfaces:**
- Consumes: `httpx.PeerTrustedFrom`, `snapshot.AreaMeta`, `DataSource.AreaAtPoint`.
- Produces:
  - `func (s *Store) AreaAtPoint(ctx context.Context, lon, lat float64) (string, error)`
  - `func (d Deps) handleLocate(w http.ResponseWriter, r *http.Request)`

- [ ] **Step 1: Write the failing store test**

Create `internal/store/locate_test.go`:

```go
package store_test

import (
	"testing"

	"airbg.org/internal/store"
)

// TestAreaAtPointPrefersTheSmallestArea: a point in Sofia falls inside the
// oblast, the city AND a district. Returning the oblast would drop a visitor
// into a country-scale view when a neighbourhood view was available.
func TestAreaAtPointPrefersTheSmallestArea(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	// Concentric buffers around the same point: 40 km, 15 km, 2 km.
	seedAreaWithRadius(t, ctx, pool, "sofia-oblast", "oblast", 23.3219, 42.6977, 40000)
	seedAreaWithRadius(t, ctx, pool, "sofia-city", "city", 23.3219, 42.6977, 15000)
	seedAreaWithRadius(t, ctx, pool, "lozenets", "neighbourhood", 23.3219, 42.6977, 2000)

	got, err := s.AreaAtPoint(ctx, 23.3219, 42.6977)
	if err != nil {
		t.Fatalf("AreaAtPoint: %v", err)
	}
	if got != "lozenets" {
		t.Errorf("AreaAtPoint = %q, want %q (the smallest containing area)", got, "lozenets")
	}
}

// TestAreaAtPointOutsideBulgariaReturnsEmpty: no area, no error. A visitor
// abroad is a normal case, not a failure, and must produce the default national
// view rather than a 500.
func TestAreaAtPointOutsideBulgariaReturnsEmpty(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	seedAreaWithRadius(t, ctx, pool, "sofia-oblast", "oblast", 23.3219, 42.6977, 40000)

	// Berlin.
	got, err := s.AreaAtPoint(ctx, 13.4050, 52.5200)
	if err != nil {
		t.Fatalf("AreaAtPoint: %v", err)
	}
	if got != "" {
		t.Errorf("AreaAtPoint = %q for a point outside every area, want \"\"", got)
	}
}

// TestAreaAtPointRejectsSwappedCoordinates. (23.3, 42.7) is Sofia;
// (42.7, 23.3) is in Somalia. PostGIS geography takes (lon, lat) — the reverse
// of the legacy PHP app's [lat, long] — so a swap here silently sends every
// Bulgarian visitor to the default view and nothing errors.
func TestAreaAtPointRejectsSwappedCoordinates(t *testing.T) {
	ctx, pool := migrated(t)
	s := store.New(pool)

	seedAreaWithRadius(t, ctx, pool, "sofia-oblast", "oblast", 23.3219, 42.6977, 40000)

	if got, err := s.AreaAtPoint(ctx, 23.3219, 42.6977); err != nil || got != "sofia-oblast" {
		t.Fatalf("AreaAtPoint(lon, lat) = %q, %v; want sofia-oblast", got, err)
	}
	if got, _ := s.AreaAtPoint(ctx, 42.6977, 23.3219); got == "sofia-oblast" {
		t.Error("AreaAtPoint(lat, lon) also matched Sofia; the argument order is not being honoured")
	}
}
```

Add the helper to the same file:

```go
func seedAreaWithRadius(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, kind string, lon, lat float64, metres int) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO area (slug, kind, name_bg, name_en, geom)
		 VALUES ($1, $2, $1, $1,
		         ST_Buffer(ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5)::geography)`,
		slug, kind, lon, lat, metres)
	if err != nil {
		t.Fatalf("seed area %s: %v", slug, err)
	}
}
```

with imports `"context"` and `"github.com/jackc/pgx/v5/pgxpool"`.

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/store/ -run TestAreaAtPoint -v
```

Expected: FAIL to compile.

- [ ] **Step 3: Implement AreaAtPoint**

Create `internal/store/locate.go`:

```go
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// areaAtPointSQL finds the smallest area containing a point.
//
// ORDER BY ST_Area(geom) picks the tightest fit, so a Sofia visitor lands on a
// district rather than the oblast. ST_Covers rather than ST_Within: Covers
// treats a point exactly on the boundary as inside, and a visitor standing on a
// municipal line should get a map, not the national default.
//
// LIMIT 1 after the ordering, not instead of it — without the ORDER BY, which
// row comes back is whatever the planner produces.
const areaAtPointSQL = `
SELECT a.slug
  FROM area a
 WHERE ST_Covers(a.geom, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography)
 ORDER BY ST_Area(a.geom)
 LIMIT 1`

// AreaAtPoint returns the slug of the smallest area containing (lon, lat), or
// "" when the point is outside every area.
//
// Argument order is longitude first, matching PostGIS geography and the rest of
// this codebase — and the inverse of the legacy PHP app's [lat, long]. A swap
// produces a valid coordinate somewhere off the Somali coast, so it fails
// silently by returning the default view rather than erroring.
//
// An empty result is not an error. A visitor abroad is a normal case.
func (s *Store) AreaAtPoint(ctx context.Context, lon, lat float64) (string, error) {
	var slug string
	err := s.pool.QueryRow(ctx, areaAtPointSQL, lon, lat).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: area at point: %w", err)
	}
	return slug, nil
}
```

- [ ] **Step 4: Run the store tests**

```bash
go test ./internal/store/ -run TestAreaAtPoint -v -count=1
```

Expected: PASS, all three.

- [ ] **Step 5: Write the failing /locate test**

Create `internal/api/locate_test.go`:

```go
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"airbg.org/internal/api"
	"airbg.org/internal/httpx"
)

// locateVia serves a request through a resolver that trusts the given CIDRs, so
// a test can control whether the Cloudflare headers are honoured.
func locateVia(t *testing.T, d api.Deps, trusted []string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	res, err := httpx.NewIPResolver(trusted)
	if err != nil {
		t.Fatalf("NewIPResolver: %v", err)
	}
	h := httpx.WithClientIP(api.NewRouter(d), res)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type locateResponse struct {
	Slug   string  `json:"slug"`
	Name   string  `json:"name"`
	Lon    float64 `json:"lon"`
	Lat    float64 `json:"lat"`
	Zoom   int     `json:"zoom"`
	Source string  `json:"source"`
}

func TestLocateUsesCloudflareHeadersFromATrustedPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "173.245.48.1:41000"
	req.Header.Set("CF-IPLatitude", "42.6977")
	req.Header.Set("CF-IPLongitude", "23.3219")

	rec := locateVia(t, deps(t, fixture(t)), httpx.DefaultCloudflareCIDRs(), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got locateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Slug != "sofia" {
		t.Errorf("slug = %q, want sofia", got.Slug)
	}
	if got.Source != "geoip" {
		t.Errorf("source = %q, want geoip", got.Source)
	}
	if got.Zoom == 0 {
		t.Error("zoom is 0; the client has no initial view")
	}
}

// TestLocateIgnoresHeadersFromAnUntrustedPeer. Otherwise anyone can claim any
// location — harmless for a map view on its own, but it is the same header-trust
// bug as the client IP, and letting it stand here means the codebase contains a
// worked example of trusting an unverified header.
func TestLocateIgnoresHeadersFromAnUntrustedPeer(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "203.0.113.9:41000"
	req.Header.Set("CF-IPLatitude", "42.6977")
	req.Header.Set("CF-IPLongitude", "23.3219")

	rec := locateVia(t, deps(t, fixture(t)), httpx.DefaultCloudflareCIDRs(), req)

	var got locateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "default" {
		t.Errorf("source = %q, want default; the headers came from an untrusted peer", got.Source)
	}
}

// TestLocateFallsBackToTheNationalView: no headers at all — the local
// development case, and any deployment without Cloudflare. It must still return
// a usable view, because the frontend has nothing else to open with.
func TestLocateFallsBackToTheNationalView(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "203.0.113.9:41000"

	rec := locateVia(t, deps(t, fixture(t)), nil, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got locateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "default" {
		t.Errorf("source = %q, want default", got.Source)
	}
	// Bulgaria's centre, roughly. Asserted as separate ranges so a lon/lat swap
	// is visible: (25, 42) is Bulgaria, (42, 25) is Saudi Arabia.
	if got.Lon < 22 || got.Lon > 29 {
		t.Errorf("lon = %v, want a Bulgarian longitude (22–29)", got.Lon)
	}
	if got.Lat < 41 || got.Lat > 45 {
		t.Errorf("lat = %v, want a Bulgarian latitude (41–45)", got.Lat)
	}
}

// TestLocateRejectsOutOfRangeHeaderValues: a trusted peer can still send
// nonsense. Latitude 999 fed to ST_MakePoint is not an error in PostGIS — it is
// a point nothing contains — so validating here is what keeps the fallback
// honest instead of silently querying garbage.
func TestLocateRejectsOutOfRangeHeaderValues(t *testing.T) {
	for _, c := range []struct{ lat, lon string }{
		{"999", "23.3"}, {"42.7", "999"}, {"nan", "23.3"}, {"", "23.3"}, {"42.7", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
		req.RemoteAddr = "173.245.48.1:41000"
		req.Header.Set("CF-IPLatitude", c.lat)
		req.Header.Set("CF-IPLongitude", c.lon)

		rec := locateVia(t, deps(t, fixture(t)), httpx.DefaultCloudflareCIDRs(), req)
		if rec.Code != http.StatusOK {
			t.Errorf("lat=%q lon=%q: status = %d, want 200 with the default view", c.lat, c.lon, rec.Code)
			continue
		}
		var got locateResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		if got.Source != "default" {
			t.Errorf("lat=%q lon=%q: source = %q, want default", c.lat, c.lon, got.Source)
		}
	}
}

// TestLocateIsNeverCachedPublicly. The response depends on the caller's IP; a
// shared cache storing it would hand one visitor's city to everyone behind the
// same edge node.
func TestLocateIsNeverCachedPublicly(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/locate", nil)
	req.RemoteAddr = "203.0.113.9:41000"

	rec := locateVia(t, deps(t, fixture(t)), nil, req)
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "private") && !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q; a per-IP response must not be publicly cacheable", cc)
	}
}
```

Add `"strings"` to the imports.

- [ ] **Step 6: Run it to verify it fails**

```bash
go test ./internal/api/ -run TestLocate -v
```

Expected: FAIL — the stub returns 501.

- [ ] **Step 7: Write the handler**

Create `internal/api/locate.go`:

```go
package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"airbg.org/internal/httpx"
)

// The national fallback view: roughly Bulgaria's centre, at a zoom that fits the
// country. Used for a visitor abroad, a visitor whose location cannot be
// determined, and every request in a deployment without Cloudflare.
const (
	defaultLon  = 25.4858
	defaultLat  = 42.7339
	defaultZoom = 7
)

type locateBody struct {
	Slug string  `json:"slug"`
	Name string  `json:"name"`
	Lon  float64 `json:"lon"`
	Lat  float64 `json:"lat"`
	Zoom int     `json:"zoom"`
	// Source is "geoip" or "default", so the frontend can decide whether to
	// show a "showing all of Bulgaria" hint. Without it a default view is
	// indistinguishable from a confident but wrong one.
	Source string `json:"source"`
}

// handleLocate resolves an approximate starting view from Cloudflare's visitor
// location headers.
//
// No browser geolocation prompt, no IP stored, no cookie. The coordinates are
// used for one ST_Covers lookup and discarded — the response carries an area
// slug, never the caller's own position.
func (d Deps) handleLocate(w http.ResponseWriter, r *http.Request) {
	snap := d.Snapshots.Load()
	if snap == nil {
		writeUnavailable(w)
		return
	}

	body := locateBody{
		Lon: defaultLon, Lat: defaultLat, Zoom: defaultZoom, Source: "default",
	}

	// The headers are honoured ONLY from a trusted peer, exactly as
	// CF-Connecting-IP is. Anything else is caller-supplied data.
	if httpx.PeerTrustedFrom(r.Context()) {
		if lon, lat, ok := headerCoords(r); ok {
			slug, err := d.Store.AreaAtPoint(r.Context(), lon, lat)
			if err != nil {
				// A failed lookup degrades to the national view rather than
				// failing the request: the caller wanted a map to open, and a
				// wider map is a worse answer but still an answer.
				slog.Warn("locate lookup failed", "error", err)
			} else if meta, known := snap.KnownSlugs[slug]; known {
				body = locateBody{
					Slug: meta.Slug, Name: meta.NameBG,
					Lon: meta.CentroidLon, Lat: meta.CentroidLat,
					Zoom: meta.DefaultZoom, Source: "geoip",
				}
			}
		}
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "Internal server error.")
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// private: the response varies by caller IP. A shared cache storing it
	// would hand one visitor's city to everyone behind the same edge node.
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Vary", "CF-IPLatitude, CF-IPLongitude")
	_, _ = w.Write(encoded)
}

// headerCoords parses and range-checks Cloudflare's visitor-location headers.
//
// Range-checking matters because PostGIS does not object to latitude 999: it
// builds a point nothing contains and returns no rows, so a garbage header
// would look identical to a visitor abroad. Validating here keeps the "default"
// source meaningful.
func headerCoords(r *http.Request) (lon, lat float64, ok bool) {
	latStr := r.Header.Get("CF-IPLatitude")
	lonStr := r.Header.Get("CF-IPLongitude")
	if latStr == "" || lonStr == "" {
		return 0, 0, false
	}

	lat, errLat := strconv.ParseFloat(latStr, 64)
	lon, errLon := strconv.ParseFloat(lonStr, 64)
	if errLat != nil || errLon != nil {
		return 0, 0, false
	}
	// NaN and ±Inf parse successfully from "nan"/"inf" and pass a naive range
	// check, because every comparison against NaN is false.
	if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lon, lat, true
}
```

- [ ] **Step 8: Delete the stub file and run everything**

```bash
rm internal/api/todo_stubs.go
go test ./internal/api/ ./internal/store/ -race -count=1
```

Expected: PASS. If `todo_stubs.go` still declared `handleLocate`, the build now fails on a duplicate method — which is the check that the stub is really gone.

- [ ] **Step 9: Prove the trust and validation tests are not tautologies**

Make one change at a time and revert it before the next.

1. Remove the `httpx.PeerTrustedFrom` guard. `TestLocateIgnoresHeadersFromAnUntrustedPeer` must FAIL. Revert.
2. Remove the `lon < -180 || lon > 180 || lat < -90 || lat > 90` check from `headerCoords`. `TestLocateRejectsOutOfRangeHeaderValues` must FAIL on the `999` case — the response now carries 999 instead of the default centre. Revert.
3. Remove the `math.IsNaN`/`math.IsInf` check. The same test must FAIL on the `nan` case: `strconv.ParseFloat("nan", 64)` succeeds, and NaN passes every `<`/`>` comparison, so the range check alone does not catch it. Revert. Change `ORDER BY ST_Area(a.geom)` to `ORDER BY ST_Area(a.geom) DESC` in `areaAtPointSQL` and confirm `TestAreaAtPointPrefersTheSmallestArea` FAILS. Revert.

- [ ] **Step 10: Commit**

```bash
git add internal/api/locate.go internal/api/locate_test.go internal/store/locate.go internal/store/locate_test.go
git rm internal/api/todo_stubs.go
git commit -m "feat(api,store): resolve an initial map view from verified edge headers"
```

---

## Task 14: Internationalisation

Bulgarian is the default; English lives under `/en/` (Phase 1 §9.5). Catalogues are JSON compiled into the binary.

**Files:**
- Create: `internal/i18n/i18n.go`
- Create: `internal/i18n/bg.json`
- Create: `internal/i18n/en.json`
- Create: `internal/i18n/i18n_test.go`

**Interfaces:**
- Produces:
  - `const DefaultLang = "bg"`, `var Languages = []string{"bg", "en"}`
  - `func Load() (*Catalogue, error)`
  - `func (c *Catalogue) T(lang, key string) string`
  - `func (c *Catalogue) Has(lang, key string) bool`
  - `func LangFromPath(path string) (lang, rest string)`
  - `func (c *Catalogue) Keys() []string`

- [ ] **Step 1: Write the failing test**

Create `internal/i18n/i18n_test.go`:

```go
package i18n_test

import (
	"strings"
	"testing"

	"airbg.org/internal/i18n"
)

func loaded(t *testing.T) *i18n.Catalogue {
	t.Helper()
	c, err := i18n.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestTranslatesBothLanguages(t *testing.T) {
	c := loaded(t)

	bg := c.T("bg", "site.title")
	en := c.T("en", "site.title")

	if bg == "" || en == "" {
		t.Fatalf("site.title is empty (bg=%q en=%q)", bg, en)
	}
	if bg == en {
		t.Errorf("bg and en are identical (%q); one of the catalogues is untranslated", bg)
	}
	// Bulgarian must actually be Cyrillic — a catalogue accidentally filled with
	// the English strings would pass every other assertion here.
	if !strings.ContainsAny(bg, "абвгдежзийклмнопрстуфхцчшщъьюяАБВГДЕЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЮЯ") {
		t.Errorf("bg site.title = %q contains no Cyrillic", bg)
	}
}

// TestCataloguesHaveIdenticalKeys is the test that keeps translations honest.
// A key present in bg.json and missing from en.json renders as a fallback on
// every English page — visible to users, invisible in tests that only check the
// keys they happen to name.
func TestCataloguesHaveIdenticalKeys(t *testing.T) {
	c := loaded(t)

	for _, key := range c.Keys() {
		for _, lang := range i18n.Languages {
			if !c.Has(lang, key) {
				t.Errorf("key %q is missing from the %q catalogue", key, lang)
			}
		}
	}
}

// TestMissingKeyFallsBackVisibly: an unknown key must not render as an empty
// string. An empty string produces a page with a blank where a label belongs and
// nothing in the logs — the failure mode is a silently broken UI.
func TestMissingKeyFallsBackVisibly(t *testing.T) {
	c := loaded(t)

	got := c.T("en", "no.such.key")
	if got == "" {
		t.Fatal("a missing key rendered as an empty string")
	}
	if !strings.Contains(got, "no.such.key") {
		t.Errorf("the fallback %q does not name the missing key, so nobody can find it", got)
	}
}

// TestUnknownLanguageFallsBackToBulgarian rather than to an empty catalogue.
func TestUnknownLanguageFallsBackToBulgarian(t *testing.T) {
	c := loaded(t)

	if got, want := c.T("de", "site.title"), c.T("bg", "site.title"); got != want {
		t.Errorf("T(\"de\", …) = %q, want the Bulgarian %q", got, want)
	}
}

func TestLangFromPath(t *testing.T) {
	cases := []struct{ path, lang, rest string }{
		{"/", "bg", "/"},
		{"/area/sofia", "bg", "/area/sofia"},
		{"/en/", "en", "/"},
		{"/en/area/sofia", "en", "/area/sofia"},
		// "/en" with no trailing slash is still the English root.
		{"/en", "en", "/"},
		// A path that merely starts with the letters "en" is not English.
		{"/energy", "bg", "/energy"},
		// An unsupported prefix is part of the path, not a language.
		{"/de/area/sofia", "bg", "/de/area/sofia"},
	}
	for _, c := range cases {
		lang, rest := i18n.LangFromPath(c.path)
		if lang != c.lang || rest != c.rest {
			t.Errorf("LangFromPath(%q) = (%q, %q), want (%q, %q)", c.path, lang, rest, c.lang, c.rest)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/i18n/ -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the catalogues**

Create `internal/i18n/bg.json`:

```json
{
  "site.title": "Мръсен въздух",
  "site.tagline": "Качество на въздуха в България в реално време",
  "nav.map": "Карта",
  "nav.areas": "Райони",
  "nav.about": "За проекта",
  "area.sensors": "сензора",
  "area.no_coverage": "Недостатъчно данни за този район",
  "area.no_coverage_detail": "Публикуваме средни стойности само когато поне три сензора отчитат надеждни данни.",
  "area.updated": "Обновено",
  "area.back_to_map": "Обратно към картата",
  "metric.P1": "ПМ10",
  "metric.P2": "ПМ2.5",
  "metric.temperature": "Температура",
  "metric.humidity": "Влажност",
  "metric.pressure": "Атмосферно налягане",
  "metric.noise_LAeq": "Шум (LAeq)",
  "metric.noise_LA_max": "Шум (макс.)",
  "sensor.title": "Сензор",
  "sensor.quality.ok": "Изправен",
  "sensor.quality.out_of_range": "Стойност извън обхват",
  "sensor.quality.stuck": "Неподвижна стойност",
  "sensor.quality.spatial_outlier": "Отклонение спрямо съседните сензори",
  "sensor.quality.no_neighbours": "Няма съседни сензори за сравнение",
  "period.24h": "24 часа",
  "period.7d": "7 дни",
  "period.30d": "30 дни",
  "period.1y": "1 година",
  "error.not_found.title": "Страницата не е намерена",
  "error.not_found.body": "Няма такава страница.",
  "error.unavailable.title": "Данните още не са готови",
  "error.unavailable.body": "Опитайте отново след минута.",
  "error.internal.title": "Възникна грешка",
  "error.internal.body": "Нещо се обърка. Опитайте отново по-късно.",
  "disclaimer": "Данните от нискобюджетни сензори са индикативни и не са измервания по референтен метод.",
  "footer.data": "Данни от сътрудниците на sensor.community, ODbL 1.0",
  "footer.boundaries": "Граници © сътрудниците на OpenStreetMap, ODbL 1.0",
  "lang.switch": "English"
}
```

Create `internal/i18n/en.json`:

```json
{
  "site.title": "Dusty Air",
  "site.tagline": "Real-time air quality in Bulgaria",
  "nav.map": "Map",
  "nav.areas": "Areas",
  "nav.about": "About",
  "area.sensors": "sensors",
  "area.no_coverage": "Not enough data for this area",
  "area.no_coverage_detail": "We publish an average only when at least three sensors are reporting usable readings.",
  "area.updated": "Updated",
  "area.back_to_map": "Back to the map",
  "metric.P1": "PM10",
  "metric.P2": "PM2.5",
  "metric.temperature": "Temperature",
  "metric.humidity": "Humidity",
  "metric.pressure": "Pressure",
  "metric.noise_LAeq": "Noise (LAeq)",
  "metric.noise_LA_max": "Noise (max)",
  "sensor.title": "Sensor",
  "sensor.quality.ok": "Healthy",
  "sensor.quality.out_of_range": "Value out of range",
  "sensor.quality.stuck": "Value not changing",
  "sensor.quality.spatial_outlier": "Disagrees with nearby sensors",
  "sensor.quality.no_neighbours": "No nearby sensors to compare with",
  "period.24h": "24 hours",
  "period.7d": "7 days",
  "period.30d": "30 days",
  "period.1y": "1 year",
  "error.not_found.title": "Page not found",
  "error.not_found.body": "There is no such page.",
  "error.unavailable.title": "Data is not ready yet",
  "error.unavailable.body": "Please try again in a minute.",
  "error.internal.title": "Something went wrong",
  "error.internal.body": "Something went wrong. Please try again later.",
  "disclaimer": "Low-cost sensor readings are indicative and are not reference-method measurements.",
  "footer.data": "Data from sensor.community contributors, ODbL 1.0",
  "footer.boundaries": "Boundaries © OpenStreetMap contributors, ODbL 1.0",
  "lang.switch": "Български"
}
```

- [ ] **Step 4: Write the implementation**

Create `internal/i18n/i18n.go`:

```go
// Package i18n serves the UI message catalogues.
//
// Bulgarian is the default and English lives under /en/ (Phase 1 §9.5). The
// catalogues are embedded, so a missing file is a build error rather than a
// deployment that starts fine and renders blank labels.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed bg.json en.json
var catalogueFS embed.FS

const DefaultLang = "bg"

// Languages is the supported set, in display order.
var Languages = []string{"bg", "en"}

type Catalogue struct {
	messages map[string]map[string]string // lang → key → text
}

func Load() (*Catalogue, error) {
	c := &Catalogue{messages: make(map[string]map[string]string, len(Languages))}
	for _, lang := range Languages {
		raw, err := catalogueFS.ReadFile(lang + ".json")
		if err != nil {
			return nil, fmt.Errorf("i18n: reading %s.json: %w", lang, err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("i18n: parsing %s.json: %w", lang, err)
		}
		if len(m) == 0 {
			// An empty catalogue would render every label as a fallback marker
			// on a live page. Fail at startup instead.
			return nil, fmt.Errorf("i18n: %s.json contains no messages", lang)
		}
		c.messages[lang] = m
	}
	return c, nil
}

// T returns the message for key in lang.
//
// Fallback order: the requested language, then Bulgarian, then a visible marker
// naming the key. Returning "" for a missing key would render a blank where a
// label belongs and leave nothing to search for — a silently broken page.
func (c *Catalogue) T(lang, key string) string {
	if m, ok := c.messages[lang]; ok {
		if text, ok := m[key]; ok {
			return text
		}
	}
	if text, ok := c.messages[DefaultLang][key]; ok {
		return text
	}
	return "!" + key + "!"
}

func (c *Catalogue) Has(lang, key string) bool {
	m, ok := c.messages[lang]
	if !ok {
		return false
	}
	_, ok = m[key]
	return ok
}

// Keys returns the union of every catalogue's keys, sorted. Used by the
// consistency test — the union rather than one language's keys, so a key present
// only in English is caught too.
func (c *Catalogue) Keys() []string {
	seen := map[string]struct{}{}
	for _, m := range c.messages {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LangFromPath splits a request path into its language and the remainder.
//
// Matching on the full first segment, not a prefix: strings.HasPrefix(path,
// "/en") would classify "/energy" as English and then serve "ergy" as the path.
func LangFromPath(path string) (string, string) {
	for _, lang := range Languages {
		if lang == DefaultLang {
			continue
		}
		if path == "/"+lang || path == "/"+lang+"/" {
			return lang, "/"
		}
		if strings.HasPrefix(path, "/"+lang+"/") {
			return lang, strings.TrimPrefix(path, "/"+lang)
		}
	}
	return DefaultLang, path
}
```

- [ ] **Step 5: Run the tests**

```bash
go test ./internal/i18n/ -v -count=1
```

Expected: PASS, all five.

- [ ] **Step 6: Prove the key-parity test is not a tautology**

Delete one key from `en.json`, re-run, confirm `TestCataloguesHaveIdenticalKeys` FAILS naming that key. Restore it. Change `T`'s final `return` to `return ""` and confirm `TestMissingKeyFallsBackVisibly` FAILS. Revert.

- [ ] **Step 7: Commit**

```bash
git add internal/i18n/
git commit -m "feat(i18n): embed the Bulgarian and English message catalogues"
```

---

## Task 15: Server-rendered pages

The HTML shell, the area page, the sensor page and the error page. Server-rendered so the site works with JavaScript disabled and is indexable; Phase 3 hydrates islands into the same markup.

**Files:**
- Create: `internal/web/render.go`
- Create: `internal/web/pages.go`
- Create: `internal/web/templates/base.gohtml`
- Create: `internal/web/templates/index.gohtml`
- Create: `internal/web/templates/area.gohtml`
- Create: `internal/web/templates/error.gohtml`
- Create: `internal/web/static/app.css`
- Create: `internal/web/render_test.go`

**Interfaces:**
- Consumes: `i18n.Catalogue`, `snapshot.Holder`, `snapshot.AreaMeta`, `api.Scales`.
- Produces:
  - `type Renderer struct { ... }`, `func NewRenderer(cat *i18n.Catalogue, holder *snapshot.Holder, baseURL string) (*Renderer, error)`
  - `func (rr *Renderer) Routes() *http.ServeMux`
  - `func (rr *Renderer) RenderError(w http.ResponseWriter, r *http.Request, status int, kind string)`
  - `type PageData struct { ... }`

- [ ] **Step 1: Write the failing test**

Create `internal/web/render_test.go`:

```go
package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/web"
)

func renderer(t *testing.T, snap *snapshot.Snapshot) *web.Renderer {
	t.Helper()
	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	h := snapshot.NewHolder()
	if snap != nil {
		h.Store(snap)
	}
	rr, err := web.NewRenderer(cat, h, "https://airbg.org")
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return rr
}

func fixture(t *testing.T) *snapshot.Snapshot {
	t.Helper()
	return &snapshot.Snapshot{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		KnownSlugs: map[string]snapshot.AreaMeta{
			"sofia": {Slug: "sofia", Kind: "oblast", NameBG: "София", NameEN: "Sofia",
				CentroidLon: 23.32, CentroidLat: 42.69, DefaultZoom: 9,
				Covered: true, SensorCount: 12},
			"vidin": {Slug: "vidin", Kind: "oblast", NameBG: "Видин", NameEN: "Vidin",
				CentroidLon: 22.87, CentroidLat: 43.99, DefaultZoom: 9,
				Covered: false, SensorCount: 1},
		},
	}
}

func fetch(t *testing.T, rr *web.Renderer, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	rr.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexRendersInBulgarianByDefault(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `lang="bg"`) {
		t.Error(`the <html> element does not carry lang="bg"; screen readers will use the wrong pronunciation for the whole page`)
	}
	if !strings.Contains(body, "Мръсен въздух") {
		t.Error("the Bulgarian title is missing")
	}
}

func TestEnglishPrefixRendersInEnglish(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/en/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `lang="en"`) {
		t.Error(`the <html> element does not carry lang="en"`)
	}
	if !strings.Contains(body, "Dusty Air") {
		t.Error("the English title is missing")
	}
}

// TestAreaPageStatesInsufficientCoverage: an area below the 3-sensor threshold
// must SAY so. Rendering nothing where a number belongs reads as "clean air"
// to anyone scanning the page, which is the single most consequential way this
// site could mislead.
func TestAreaPageStatesInsufficientCoverage(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/area/vidin")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Недостатъчно данни") {
		t.Errorf("the uncovered area page does not state that coverage is insufficient:\n%s", body)
	}
	if strings.Contains(body, "µg/m³") {
		t.Error("the uncovered area page shows a unit, implying a measurement it does not have")
	}
}

func TestAreaPageCarriesTheDisclaimer(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/area/sofia")
	if !strings.Contains(rec.Body.String(), "индикативни") {
		t.Error("the indicative-data disclaimer is missing from a page that shows values")
	}
}

// TestUnknownAreaIs404WithAPage: an unknown slug must produce a rendered 404,
// not a blank body under a 404 status and not a 200.
func TestUnknownAreaIs404WithAPage(t *testing.T) {
	rec := fetch(t, renderer(t, fixture(t)), "/area/atlantis")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Страницата не е намерена") {
		t.Errorf("the 404 has no rendered body:\n%s", rec.Body.String())
	}
}

// TestSlugIsEscapedInOutput. html/template escapes by context, which is why the
// templates use it — but the escaping only holds if the value is interpolated as
// DATA. A slug pasted into an attribute without quotes, or into a <script>,
// escapes differently. Asserting on a hostile slug pins that.
func TestSlugIsEscapedInOutput(t *testing.T) {
	snap := fixture(t)
	hostile := `"><script>alert(1)</script>`
	snap.KnownSlugs[hostile] = snapshot.AreaMeta{
		Slug: hostile, Kind: "oblast", NameBG: hostile, NameEN: hostile,
		DefaultZoom: 9, Covered: true, SensorCount: 5,
	}

	rec := httptest.NewRecorder()
	// url.PathEscape, not a hand-rolled replace: the slug contains characters
	// that would otherwise terminate the request target and change what is
	// being tested.
	req := httptest.NewRequest(http.MethodGet, "/area/"+url.PathEscape(hostile), nil)
	renderer(t, snap).Routes().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "<script>alert(1)</script>") {
		t.Error("a script tag from a slug reached the output unescaped")
	}
}

func TestErrorPageRendersInTheRequestLanguage(t *testing.T) {
	rr := renderer(t, fixture(t))

	bg := fetch(t, rr, "/area/nope")
	if !strings.Contains(bg.Body.String(), "Страницата не е намерена") {
		t.Error("the Bulgarian 404 is not in Bulgarian")
	}

	en := fetch(t, rr, "/en/area/nope")
	if !strings.Contains(en.Body.String(), "Page not found") {
		t.Errorf("the English 404 is not in English:\n%s", en.Body.String())
	}
}

// TestPageIs503BeforeTheFirstSnapshot — same rule as the API: no data means say
// so, never render an empty country.
func TestPageIs503BeforeTheFirstSnapshot(t *testing.T) {
	rec := fetch(t, renderer(t, nil), "/")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// TestNoInlineScriptOrStyle: the CSP forbids 'unsafe-inline', so any inline
// <script> or style="" the templates emit would be blocked in a real browser
// and silently do nothing — a page that renders correctly in a test and is
// broken in production.
func TestNoInlineScriptOrStyle(t *testing.T) {
	for _, path := range []string{"/", "/en/", "/area/sofia", "/area/vidin"} {
		body := fetch(t, renderer(t, fixture(t)), path).Body.String()
		if strings.Contains(body, "<script>") {
			t.Errorf("%s contains an inline <script>, which the CSP blocks", path)
		}
		if strings.Contains(body, "style=\"") {
			t.Errorf("%s contains an inline style attribute, which the CSP blocks", path)
		}
	}
}

// TestAlternateLanguageLinks: hreflang pairs are how a search engine learns the
// two URLs are the same page in different languages, and how a reader switches
// without losing their place.
func TestAlternateLanguageLinks(t *testing.T) {
	body := fetch(t, renderer(t, fixture(t)), "/area/sofia").Body.String()

	for _, want := range []string{
		`hreflang="bg"`,
		`hreflang="en"`,
		`https://airbg.org/area/sofia`,
		`https://airbg.org/en/area/sofia`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %q", want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/web/ -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the templates**

Create `internal/web/templates/base.gohtml`:

```gohtml
{{define "base"}}<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{block "title" .}}{{.T "site.title"}}{{end}}</title>
<meta name="description" content="{{.T "site.tagline"}}">
<link rel="canonical" href="{{.CanonicalURL}}">
{{range .Alternates}}<link rel="alternate" hreflang="{{.Lang}}" href="{{.URL}}">
{{end}}<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<header>
  <a href="{{.Path "/"}}"><strong>{{.T "site.title"}}</strong></a>
  <nav>
    <a href="{{.Path "/"}}">{{.T "nav.map"}}</a>
    <a href="{{.Path "/areas"}}">{{.T "nav.areas"}}</a>
    <a href="{{.OtherLangURL}}" hreflang="{{.OtherLang}}">{{.T "lang.switch"}}</a>
  </nav>
</header>

<main>
{{block "main" .}}{{end}}
</main>

<footer>
  <p class="disclaimer">{{.T "disclaimer"}}</p>
  <p>{{.T "footer.data"}}</p>
  <p>{{.T "footer.boundaries"}}</p>
  {{if not .GeneratedAt.IsZero}}<p>{{.T "area.updated"}}: <time datetime="{{.GeneratedAtISO}}">{{.GeneratedAtHuman}}</time></p>{{end}}
</footer>
</body>
</html>{{end}}
```

Create `internal/web/templates/index.gohtml`:

```gohtml
{{define "main"}}
<h1>{{.T "site.title"}}</h1>
<p>{{.T "site.tagline"}}</p>

{{/* Phase 3 replaces this list with a MapLibre island mounted on #map.
     The list stays underneath as the no-JavaScript fallback and as the
     crawlable content — a map alone is invisible to a search engine and
     unusable with a screen reader. */}}
<div id="map" data-island="map" data-zoom="7" data-lon="25.4858" data-lat="42.7339"></div>

<h2>{{.T "nav.areas"}}</h2>
<ul class="areas">
{{range .Areas}}
  <li>
    <a href="{{$.Path (printf "/area/%s" .Slug)}}">{{.Name}}</a>
    {{if .Covered}}<span class="count">{{.SensorCount}} {{$.T "area.sensors"}}</span>
    {{else}}<span class="uncovered">{{$.T "area.no_coverage"}}</span>{{end}}
  </li>
{{end}}
</ul>
{{end}}
```

Create `internal/web/templates/area.gohtml`:

```gohtml
{{define "title"}}{{.Area.Name}} — {{.T "site.title"}}{{end}}

{{define "main"}}
<nav aria-label="breadcrumb"><a href="{{.Path "/"}}">{{.T "area.back_to_map"}}</a></nav>

<h1>{{.Area.Name}}</h1>

{{if .Area.Covered}}
  <p>{{.Area.SensorCount}} {{.T "area.sensors"}}</p>
  {{/* Phase 3 mounts the chart island here; the data comes from
       /api/v1/area/{slug}/series. Server-side we render the sensor count and
       the coverage state only — the numbers themselves live behind the API so
       there is exactly one place that applies the quality filter. */}}
  <div id="chart" data-island="chart" data-slug="{{.Area.Slug}}"></div>
{{else}}
  <div class="notice">
    <p><strong>{{.T "area.no_coverage"}}</strong></p>
    <p>{{.T "area.no_coverage_detail"}}</p>
  </div>
{{end}}

<div id="area-map" data-island="map"
     data-slug="{{.Area.Slug}}"
     data-zoom="{{.Area.Zoom}}"
     data-lon="{{.Area.Lon}}"
     data-lat="{{.Area.Lat}}"></div>
{{end}}
```

Create `internal/web/templates/error.gohtml`:

```gohtml
{{define "title"}}{{.T .TitleKey}} — {{.T "site.title"}}{{end}}

{{define "main"}}
<h1>{{.T .TitleKey}}</h1>
<p>{{.T .BodyKey}}</p>
<p><a href="{{.Path "/"}}">{{.T "area.back_to_map"}}</a></p>
{{end}}
```

Create `internal/web/static/app.css` — minimal, and external because the CSP forbids inline styles:

```css
:root { --fg: #1a1a1a; --muted: #666; --bg: #fff; --accent: #0b6; --warn: #a40; }
* { box-sizing: border-box; }
body { margin: 0; font: 16px/1.5 system-ui, sans-serif; color: var(--fg); background: var(--bg); }
header, main, footer { max-width: 60rem; margin: 0 auto; padding: 1rem; }
header { display: flex; gap: 1rem; align-items: baseline; justify-content: space-between; border-bottom: 1px solid #ddd; }
nav a { margin-left: 1rem; }
a { color: var(--accent); }
.areas { list-style: none; padding: 0; }
.areas li { display: flex; justify-content: space-between; padding: .4rem 0; border-bottom: 1px solid #eee; }
.count { color: var(--muted); }
.uncovered { color: var(--warn); }
.notice { border-left: 4px solid var(--warn); padding: .5rem 1rem; background: #fff8f0; }
.disclaimer { color: var(--muted); }
footer { border-top: 1px solid #ddd; color: var(--muted); font-size: .875rem; }
#map, #area-map { height: 24rem; background: #f2f2f2; border: 1px solid #ddd; }
#chart { min-height: 12rem; }
/* Honour the reduced-motion preference before Phase 3 adds any transitions. */
@media (prefers-reduced-motion: reduce) { * { animation: none !important; transition: none !important; } }
```

- [ ] **Step 4: Write the renderer**

Create `internal/web/render.go`:

```go
// Package web renders the server-side HTML.
//
// Server-rendered rather than an SPA shell (Phase 1 §9.1): the pages work with
// JavaScript disabled, they are crawlable, and the first paint does not wait on
// a bundle. Phase 3 hydrates islands into this same markup — the data-island
// attributes are the mount points.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Renderer struct {
	cat     *i18n.Catalogue
	holder  *snapshot.Holder
	baseURL string

	// One parsed template set per page, each cloned from the base. A single
	// set would not work: every page defines "main", and the last parse would
	// win for all of them.
	pages map[string]*template.Template
}

func NewRenderer(cat *i18n.Catalogue, holder *snapshot.Holder, baseURL string) (*Renderer, error) {
	rr := &Renderer{
		cat: cat, holder: holder,
		baseURL: strings.TrimSuffix(baseURL, "/"),
		pages:   make(map[string]*template.Template),
	}

	for _, page := range []string{"index", "area", "error"} {
		t, err := template.New("base.gohtml").ParseFS(templateFS,
			"templates/base.gohtml", "templates/"+page+".gohtml")
		if err != nil {
			// Parsed at startup, not per request: a template typo must fail the
			// process at boot, not produce a 500 the first time a user hits
			// that page.
			return nil, fmt.Errorf("web: parsing %s: %w", page, err)
		}
		rr.pages[page] = t
	}
	return rr, nil
}

// PageData is what every template sees. Methods rather than precomputed fields
// where the value depends on the template's own argument (T, Path).
type PageData struct {
	Lang        string
	OtherLang   string
	RequestPath string // language-stripped, e.g. "/area/sofia"
	BaseURL     string
	GeneratedAt time.Time

	Areas []AreaRow
	Area  *AreaRow

	TitleKey string
	BodyKey  string

	cat *i18n.Catalogue
}

type AreaRow struct {
	Slug        string
	Name        string
	Kind        string
	Lon, Lat    float64
	Zoom        int
	Covered     bool
	SensorCount int
}

type alternate struct {
	Lang string
	URL  string
}

func (p PageData) T(key string) string { return p.cat.T(p.Lang, key) }

// Path prefixes an in-site path with the current language, so every link in a
// template stays in the language the reader chose. A template that hardcoded
// "/area/…" would silently drop an English reader back to Bulgarian.
func (p PageData) Path(path string) string {
	if p.Lang == i18n.DefaultLang {
		return path
	}
	if path == "/" {
		return "/" + p.Lang + "/"
	}
	return "/" + p.Lang + path
}

func (p PageData) CanonicalURL() string { return p.BaseURL + p.Path(p.RequestPath) }

func (p PageData) OtherLangURL() string {
	other := PageData{Lang: p.OtherLang, RequestPath: p.RequestPath, BaseURL: p.BaseURL}
	return other.BaseURL + other.Path(p.RequestPath)
}

func (p PageData) Alternates() []alternate {
	out := make([]alternate, 0, len(i18n.Languages))
	for _, lang := range i18n.Languages {
		other := PageData{Lang: lang, RequestPath: p.RequestPath, BaseURL: p.BaseURL}
		out = append(out, alternate{Lang: lang, URL: other.BaseURL + other.Path(p.RequestPath)})
	}
	return out
}

func (p PageData) GeneratedAtISO() string { return p.GeneratedAt.UTC().Format(time.RFC3339) }

func (p PageData) GeneratedAtHuman() string { return p.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC") }

// newPageData builds the common fields for one request.
func (rr *Renderer) newPageData(lang, path string, generatedAt time.Time) PageData {
	other := "en"
	if lang == "en" {
		other = "bg"
	}
	return PageData{
		Lang: lang, OtherLang: other, RequestPath: path,
		BaseURL: rr.baseURL, GeneratedAt: generatedAt, cat: rr.cat,
	}
}

// render executes one page.
//
// Rendered into a buffer first, then copied out. Writing straight to the
// ResponseWriter means a template error halfway through leaves a truncated page
// under a 200 that has already been committed — the client sees a broken page
// and the status says everything is fine.
func (rr *Renderer) render(w http.ResponseWriter, status int, page string, data PageData) {
	t, ok := rr.pages[page]
	if !ok {
		rr.writePlain(w, http.StatusInternalServerError)
		return
	}

	var buf strings.Builder
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		// Do not fall back to rendering the error page through the same broken
		// machinery; emit fixed plain text instead.
		rr.writePlain(w, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=150")
	w.Header().Set("Vary", "Accept-Encoding")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

func (rr *Renderer) writePlain(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("Internal server error.\n"))
}

// RenderError renders the error page in the request's language.
//
// kind is "not_found", "unavailable" or "internal" — a fixed set, so the keys
// it builds always exist in the catalogue.
func (rr *Renderer) RenderError(w http.ResponseWriter, r *http.Request, status int, kind string) {
	lang, path := i18n.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, time.Time{})
	data.TitleKey = "error." + kind + ".title"
	data.BodyKey = "error." + kind + ".body"
	w.Header().Set("Cache-Control", "no-store")
	rr.render(w, status, "error", data)
}
```

- [ ] **Step 5: Write the page handlers**

Create `internal/web/pages.go`:

```go
package web

import (
	"net/http"
	"sort"

	"airbg.org/internal/i18n"
	"airbg.org/internal/snapshot"
)

// Routes returns the page routes plus the embedded static assets.
//
// The language prefix is handled by registering each pattern twice rather than
// by a rewriting middleware: ServeMux then owns the matching, {slug} is parsed
// by the same code for both languages, and there is no path-mangling step where
// "/energy" could be mistaken for English.
func (rr *Renderer) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	for _, prefix := range []string{"", "/en"} {
		root := prefix + "/"
		if prefix == "" {
			root = "/{$}" // exact "/" only, so it does not swallow every path
		} else {
			root = prefix + "/{$}"
		}
		mux.HandleFunc("GET "+root, rr.handleIndex)
		mux.HandleFunc("GET "+prefix+"/areas", rr.handleIndex)
		mux.HandleFunc("GET "+prefix+"/area/{slug}", rr.handleArea)
	}

	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	// Anything unmatched is a rendered 404, not net/http's bare text one.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rr.RenderError(w, r, http.StatusNotFound, "not_found")
	})

	return mux
}

func (rr *Renderer) handleIndex(w http.ResponseWriter, r *http.Request) {
	snap := rr.holder.Load()
	if snap == nil {
		rr.RenderError(w, r, http.StatusServiceUnavailable, "unavailable")
		return
	}

	lang, path := i18n.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, snap.GeneratedAt)
	data.Areas = areaRows(snap, lang, "oblast")
	rr.render(w, http.StatusOK, "index", data)
}

func (rr *Renderer) handleArea(w http.ResponseWriter, r *http.Request) {
	snap := rr.holder.Load()
	if snap == nil {
		rr.RenderError(w, r, http.StatusServiceUnavailable, "unavailable")
		return
	}

	// Validated against the snapshot, so no caller-supplied slug is ever used
	// for anything but a map lookup.
	meta, ok := snap.KnownSlugs[r.PathValue("slug")]
	if !ok {
		rr.RenderError(w, r, http.StatusNotFound, "not_found")
		return
	}

	lang, path := i18n.LangFromPath(r.URL.Path)
	data := rr.newPageData(lang, path, snap.GeneratedAt)
	row := rowFrom(meta, lang)
	data.Area = &row
	rr.render(w, http.StatusOK, "area", data)
}

func areaRows(snap *snapshot.Snapshot, lang, kind string) []AreaRow {
	rows := make([]AreaRow, 0, len(snap.KnownSlugs))
	for _, meta := range snap.KnownSlugs {
		if kind != "" && meta.Kind != kind {
			continue
		}
		rows = append(rows, rowFrom(meta, lang))
	}
	// Sorted by name so the list is stable between requests. Map iteration
	// order would reshuffle it on every page load — visibly wrong to a reader
	// and pointless cache churn at the edge.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

func rowFrom(meta snapshot.AreaMeta, lang string) AreaRow {
	name := meta.NameBG
	if lang == "en" && meta.NameEN != "" {
		name = meta.NameEN
	}
	return AreaRow{
		Slug: meta.Slug, Name: name, Kind: meta.Kind,
		Lon: meta.CentroidLon, Lat: meta.CentroidLat, Zoom: meta.DefaultZoom,
		Covered: meta.Covered, SensorCount: meta.SensorCount,
	}
}
```

- [ ] **Step 6: Run the tests**

```bash
go test ./internal/web/ -race -v -count=1
```

Expected: PASS, all ten.

- [ ] **Step 7: Prove the coverage and escaping tests are not tautologies**

In `area.gohtml`, change `{{if .Area.Covered}}` to `{{if true}}` and confirm `TestAreaPageStatesInsufficientCoverage` FAILS. Revert. Change the `{{.Area.Name}}` interpolation to `{{.Area.Name | safeHTML}}` — you will need to add a funcmap entry returning `template.HTML` to do it — confirm `TestSlugIsEscapedInOutput` FAILS, then revert BOTH the template change and the funcmap. Delete the `{{range .Alternates}}` block and confirm `TestAlternateLanguageLinks` FAILS. Revert.

- [ ] **Step 8: Commit**

```bash
git add internal/web/
git commit -m "feat(web): render the index, area and error pages in Bulgarian and English"
```

---

## Task 16: Configuration and the snapshot publisher

The serving knobs join `internal/config`, and the ingest cycle learns to publish a snapshot when it finishes.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/ingest/ingest.go`
- Modify: `internal/ingest/ingest_test.go`

**Interfaces:**
- Consumes: `snapshot.Holder`, `snapshot.Build`.
- Produces:
  - `Config.ListenAddr`, `Config.MetricsAddr`, `Config.TrustedProxyCIDRs []string`, `Config.BaseURL string`
  - `type SnapshotPublisher interface { Publish(ctx context.Context, now time.Time) error }`
  - `func (i *Ingester) SetSnapshotPublisher(p SnapshotPublisher)`

- [ ] **Step 1: Write the failing config test**

Append to `internal/config/config_test.go`:

```go
func TestServeDefaults(t *testing.T) {
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q, want the loopback default", cfg.ListenAddr)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("MetricsAddr = %q, want the loopback default", cfg.MetricsAddr)
	}
	// Defaulting the trusted-proxy list to Cloudflare's ranges would mean a
	// developer running locally trusts CF-Connecting-IP from anyone on their
	// machine. Empty by default: trust nothing until an operator says so.
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Errorf("TrustedProxyCIDRs = %v, want empty by default", cfg.TrustedProxyCIDRs)
	}
	if cfg.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestTrustedProxyCIDRsSplitsAndTrims(t *testing.T) {
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_TRUSTED_PROXY_CIDRS", " 173.245.48.0/20 , 2400:cb00::/32 ,")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"173.245.48.0/20", "2400:cb00::/32"}
	if len(cfg.TrustedProxyCIDRs) != len(want) {
		t.Fatalf("TrustedProxyCIDRs = %v, want %v", cfg.TrustedProxyCIDRs, want)
	}
	for i := range want {
		if cfg.TrustedProxyCIDRs[i] != want[i] {
			t.Errorf("TrustedProxyCIDRs[%d] = %q, want %q", i, cfg.TrustedProxyCIDRs[i], want[i])
		}
	}
}

// TestMalformedTrustedProxyCIDRIsAStartupError. This list decides whose
// CF-Connecting-IP header is believed. A typo that is silently dropped shrinks
// the trusted set without telling anyone; a typo that is silently kept as a
// string is never matched. Either way the operator thinks the edge is trusted
// and it is not. Fail at boot.
func TestMalformedTrustedProxyCIDRIsAStartupError(t *testing.T) {
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_TRUSTED_PROXY_CIDRS", "173.245.48.0/20,not-a-cidr")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load accepted a malformed CIDR")
	}
}

func TestBaseURLMustBeAbsolute(t *testing.T) {
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_BASE_URL", "/airbg")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load accepted a relative AIRBG_BASE_URL; canonical and hreflang links would be broken")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/config/ -run 'Serve|TrustedProxy|BaseURL' -v
```

Expected: FAIL — `cfg.ListenAddr` undefined.

- [ ] **Step 3: Extend the config**

Add to the `Config` struct in `internal/config/config.go`:

```go
	// ListenAddr is the public HTTP listener. Loopback by default: in
	// production Cloudflare reaches the origin over a tunnel, and a default of
	// 0.0.0.0 would expose an origin that has never seen a rate limit to the
	// open internet the first time someone runs it on a public host.
	ListenAddr string

	// MetricsAddr serves /metrics and /healthz on a separate listener, so the
	// public chain cannot route to them at all.
	MetricsAddr string

	// TrustedProxyCIDRs lists the peer ranges whose CF-Connecting-IP header is
	// believed. Empty means trust nobody.
	TrustedProxyCIDRs []string

	// BaseURL is the public origin, used to build canonical and hreflang links.
	BaseURL string
```

Add the constants:

```go
const (
	defaultListenAddr  = "127.0.0.1:8080"
	defaultMetricsAddr = "127.0.0.1:9090"
	defaultBaseURL     = "http://localhost:8080"
)
```

Add to `Load`, before the final `return`:

```go
	cfg.ListenAddr = envOr("AIRBG_LISTEN_ADDR", defaultListenAddr)
	cfg.MetricsAddr = envOr("AIRBG_METRICS_ADDR", defaultMetricsAddr)

	if cfg.ListenAddr == cfg.MetricsAddr {
		// Same address means /metrics is reachable from the public chain,
		// which hands an attacker the counters that show whether their probing
		// is being rate limited.
		return Config{}, fmt.Errorf("config: AIRBG_LISTEN_ADDR and AIRBG_METRICS_ADDR are both %q; the private listener must be separate", cfg.ListenAddr)
	}

	for _, raw := range strings.Split(os.Getenv("AIRBG_TRUSTED_PROXY_CIDRS"), ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if _, err := netip.ParsePrefix(item); err != nil {
			return Config{}, fmt.Errorf("config: AIRBG_TRUSTED_PROXY_CIDRS entry %q: %w", item, err)
		}
		cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, item)
	}

	cfg.BaseURL = strings.TrimSuffix(envOr("AIRBG_BASE_URL", defaultBaseURL), "/")
	if u, err := url.Parse(cfg.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("config: AIRBG_BASE_URL must be absolute, e.g. https://airbg.org (got %q)", cfg.BaseURL)
	}
```

Add `"net/netip"`, `"net/url"` and `"strings"` to the imports if they are not already there. If `envOr` does not exist in this file, add it:

```go
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 4: Run the config tests**

```bash
go test ./internal/config/ -count=1
```

Expected: PASS, including the pre-existing tests.

- [ ] **Step 5: Write the failing publisher test**

Append to `internal/ingest/ingest_test.go`:

```go
type recordingPublisher struct {
	calls int
	err   error
	when  time.Time
}

func (p *recordingPublisher) Publish(_ context.Context, now time.Time) error {
	p.calls++
	p.when = now
	return p.err
}

// TestRunOncePublishesASnapshot: the snapshot is built at the END of a cycle.
// Built on a timer instead, it could read the reading table mid-write and
// publish an area average over a partially inserted cycle.
func TestRunOncePublishesASnapshot(t *testing.T) {
	// Reuse this package's existing harness for a successful RunOnce. Follow
	// whatever the neighbouring success-path test does to build the Ingester.
	ing, cleanup := newTestIngester(t)
	defer cleanup()

	pub := &recordingPublisher{}
	ing.SetSnapshotPublisher(pub)

	if _, err := ing.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if pub.calls != 1 {
		t.Errorf("Publish called %d times, want 1", pub.calls)
	}
	if pub.when.IsZero() {
		t.Error("Publish was handed a zero time")
	}
}

// TestSnapshotFailureDoesNotFailTheCycle. Serving data one cycle stale is a
// degraded page; returning an error from RunOnce is a collector that a
// supervisor may restart-loop, and the readings for that cycle are then lost
// for good. The safety property runs the other way round from the usual one.
func TestSnapshotFailureDoesNotFailTheCycle(t *testing.T) {
	ing, cleanup := newTestIngester(t)
	defer cleanup()

	ing.SetSnapshotPublisher(&recordingPublisher{err: errors.New("boom")})

	if _, err := ing.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce = %v, want nil — a snapshot failure must not fail ingest", err)
	}
}

// TestNoPublisherIsFine — `airbg ingest` run as a bare cron job has no server
// attached and must not nil-panic.
func TestNoPublisherIsFine(t *testing.T) {
	ing, cleanup := newTestIngester(t)
	defer cleanup()

	if _, err := ing.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce with no publisher: %v", err)
	}
}
```

If `newTestIngester` does not already exist in `ingest_test.go`, extract it from the existing success-path test verbatim rather than writing a second harness — two harnesses drift.

- [ ] **Step 6: Run it to verify it fails**

```bash
go test ./internal/ingest/ -run 'Snapshot|NoPublisher' -v
```

Expected: FAIL — `SetSnapshotPublisher` undefined.

- [ ] **Step 7: Wire the publisher into the ingester**

Add to `internal/ingest/ingest.go`:

```go
// SnapshotPublisher rebuilds the served snapshot. Declared here as an
// interface rather than importing internal/snapshot, so ingest keeps no
// dependency on the serving side and the test needs no database.
type SnapshotPublisher interface {
	Publish(ctx context.Context, now time.Time) error
}

func (i *Ingester) SetSnapshotPublisher(p SnapshotPublisher) { i.publisher = p }
```

Add the `publisher SnapshotPublisher` field to the `Ingester` struct, and at the very end of `RunOnce` — after the readings are committed, after quality scoring, and after the rollup drain — immediately before the successful return:

```go
	i.publishSnapshot(ctx)

	return stats, nil
}

// publishSnapshot rebuilds the served snapshot at the end of a cycle.
//
// Errors are logged, never returned: a stale snapshot serves last cycle's data,
// while a failed RunOnce loses this cycle's readings and may restart-loop the
// collector. The weaker failure mode wins.
func (i *Ingester) publishSnapshot(ctx context.Context) {
	if i.publisher == nil {
		return
	}
	// i.now() is the injectable clock this package already uses everywhere;
	// calling time.Now directly would make the publisher the one part of the
	// cycle that ignores a test's fixed clock.
	if err := i.publisher.Publish(ctx, i.now()); err != nil {
		slog.Error("snapshot publish failed; serving the previous snapshot",
			"error", err)
	}
}
```

`RunOnce` returns `(Stats, error)` and `Ingester` logs through the package-level `slog`, not a field — match the surrounding code rather than introducing a second convention. Use the existing `stats` value in the return.

- [ ] **Step 8: Run the tests**

```bash
go test ./internal/config/ ./internal/ingest/ -count=1
```

Expected: PASS.

- [ ] **Step 9: Prove the failure-isolation test is not a tautology**

Change `publishSnapshot` to return the error and `RunOnce` to propagate it. Confirm `TestSnapshotFailureDoesNotFailTheCycle` FAILS. Revert. Then delete the `if i.publisher == nil` guard and confirm `TestNoPublisherIsFine` panics. Revert.

- [ ] **Step 10: Commit**

```bash
git add internal/config/ internal/ingest/
git commit -m "feat(config,ingest): add the serving configuration and publish a snapshot per cycle"
```

---

## Task 17: The server and the serve subcommand

Two listeners, hard timeouts, graceful shutdown, and the wiring that turns every previous task into a running process.

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/publisher.go`
- Create: `internal/server/server_test.go`
- Modify: `cmd/airbg/main.go`

**Interfaces:**
- Consumes: everything from Tasks 4–16.
- Produces:
  - `type Options struct { ... }`, `func New(opts Options) (*Server, error)`
  - `func (s *Server) Run(ctx context.Context) error`
  - `type Publisher struct { ... }`, `func NewPublisher(st *store.Store, h *snapshot.Holder, log *slog.Logger) *Publisher`

- [ ] **Step 1: Write the failing test**

Create `internal/server/server_test.go`:

```go
package server_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/i18n"
	"airbg.org/internal/server"
	"airbg.org/internal/snapshot"
)

func free(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func running(t *testing.T) (public, private string) {
	t.Helper()

	cat, err := i18n.Load()
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}
	holder := snapshot.NewHolder()
	holder.Store(&snapshot.Snapshot{
		GeneratedAt: time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		KnownSlugs:  map[string]snapshot.AreaMeta{},
		Overview:    snapshot.Body{JSON: []byte(`{"areas":[]}`), ETag: `"t"`},
	})

	public, private = free(t), free(t)
	srv, err := server.New(server.Options{
		ListenAddr: public, MetricsAddr: private,
		Catalogue: cat, Snapshots: holder, BaseURL: "http://" + public,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not return within 10s of cancellation; shutdown is not graceful, it is stuck")
		}
	})

	waitReady(t, private)
	return public, private
}

func waitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the private listener never came up")
}

func get(t *testing.T, addr, path string) *http.Response {
	t.Helper()
	resp, err := http.Get("http://" + addr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestPublicListenerServesPagesAndAPI(t *testing.T) {
	public, _ := running(t)

	if got := get(t, public, "/").StatusCode; got != http.StatusOK {
		t.Errorf("GET / = %d, want 200", got)
	}
	if got := get(t, public, "/api/v1/overview").StatusCode; got != http.StatusOK {
		t.Errorf("GET /api/v1/overview = %d, want 200", got)
	}
}

// TestMetricsAreNotOnThePublicListener. /metrics reports rate-limit and
// enumeration counters — precisely the feedback signal a scraper needs to tune
// its request rate to stay under the limit. It must live on the private
// listener only.
func TestMetricsAreNotOnThePublicListener(t *testing.T) {
	public, private := running(t)

	if got := get(t, public, "/metrics").StatusCode; got == http.StatusOK {
		t.Error("/metrics is reachable on the public listener")
	}
	if got := get(t, private, "/metrics").StatusCode; got != http.StatusOK {
		t.Errorf("GET /metrics on the private listener = %d, want 200", got)
	}
}

func TestSecurityHeadersOnEveryPublicResponse(t *testing.T) {
	public, _ := running(t)

	for _, path := range []string{"/", "/api/v1/overview", "/nope"} {
		h := get(t, public, path).Header
		if h.Get("Content-Security-Policy") == "" {
			t.Errorf("%s has no CSP", path)
		}
		if h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s is missing nosniff", path)
		}
	}
}

// TestRequestBodyIsCapped: an unbounded body on a GET-only service is free
// memory pressure for an attacker.
func TestRequestBodyIsCapped(t *testing.T) {
	public, _ := running(t)

	resp, err := http.Post("http://"+public+"/api/v1/overview", "application/json",
		strings.NewReader(strings.Repeat("x", 4<<20)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	// 405 from the method-qualified route is the expected answer; what must NOT
	// happen is a 200 or a hang.
	if resp.StatusCode == http.StatusOK {
		t.Errorf("a 4 MiB POST to a GET route returned 200")
	}
}

// TestReadHeaderTimeoutIsSet is asserted by behaviour, not by reading the
// struct: a connection that opens and sends nothing must be closed by the
// server. Without ReadHeaderTimeout, a few thousand such connections exhaust
// the listener with no traffic at all (slowloris).
func TestSlowClientIsDisconnected(t *testing.T) {
	public, _ := running(t)

	conn, err := net.Dial("tcp", public)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\n")) // deliberately unfinished
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))

	if _, err := io.ReadAll(conn); err == nil {
		// A clean EOF means the server closed it, which is the pass condition.
		return
	} else if errors.Is(err, io.EOF) {
		return
	} else {
		t.Errorf("the server did not close an idle half-open request: %v", err)
	}
}

func TestHealthzOnPrivateListener(t *testing.T) {
	_, private := running(t)

	if got := get(t, private, "/healthz").StatusCode; got != http.StatusOK {
		t.Errorf("GET /healthz = %d, want 200", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/server/ -v
```

Expected: FAIL to build.

- [ ] **Step 3: Write the publisher**

Create `internal/server/publisher.go`:

```go
package server

import (
	"context"
	"log/slog"
	"time"

	"airbg.org/internal/metrics"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/store"
)

var (
	snapshotBuilds   = metrics.Counter("airbg_snapshot_builds_total", "Snapshots built.")
	snapshotFailures = metrics.Counter("airbg_snapshot_build_failures_total", "Snapshot builds that failed.")
	snapshotAge      = metrics.Gauge("airbg_snapshot_age_seconds", "Seconds since the served snapshot was built.")
)

// Publisher rebuilds the snapshot and swaps it into the holder. It satisfies
// ingest.SnapshotPublisher.
type Publisher struct {
	store  *store.Store
	holder *snapshot.Holder
	log    *slog.Logger
}

func NewPublisher(st *store.Store, h *snapshot.Holder, log *slog.Logger) *Publisher {
	return &Publisher{store: st, holder: h, log: log}
}

func (p *Publisher) Publish(ctx context.Context, now time.Time) error {
	snap, err := snapshot.Build(ctx, p.store, now)
	if err != nil {
		snapshotFailures.Inc()
		return err
	}
	// Stored only on success. A partial snapshot must never replace a good one:
	// serving last cycle's complete data beats serving this cycle's half of it.
	p.holder.Store(snap)
	snapshotBuilds.Inc()
	p.log.Info("snapshot published",
		"areas", len(snap.KnownSlugs), "generated_at", snap.GeneratedAt)
	return nil
}

// ObserveAge updates the age gauge. Called from the metrics handler path rather
// than on a ticker, so the value is computed at scrape time and a wedged
// publisher shows an age that keeps climbing.
func (p *Publisher) ObserveAge(now time.Time) {
	if snap := p.holder.Load(); snap != nil {
		snapshotAge.Set(now.Sub(snap.GeneratedAt).Seconds())
	}
}
```

- [ ] **Step 4: Write the server**

Create `internal/server/server.go`:

```go
// Package server assembles the two listeners.
//
// Two, not one: the public listener carries the middleware chain and the
// public routes; the private listener carries /metrics and /healthz. Separate
// listeners rather than a path prefix, because a prefix is one routing mistake
// away from exposing the counters that tell a scraper whether it is being
// throttled.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"airbg.org/internal/api"
	"airbg.org/internal/httpx"
	"airbg.org/internal/i18n"
	"airbg.org/internal/metrics"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/web"
)

type Options struct {
	ListenAddr  string
	MetricsAddr string

	Catalogue *i18n.Catalogue
	Snapshots *snapshot.Holder
	Store     api.DataSource
	Publisher *Publisher

	TrustedProxyCIDRs []string
	BaseURL           string
	Logger            *slog.Logger
}

type Server struct {
	public  *http.Server
	private *http.Server
	limiter *ratelimit.Limiter
	breadth *ratelimit.Breadth
	log     *slog.Logger
}

// Timeouts. Every one of these is a bound on what a single connection can cost.
const (
	// readHeaderTimeout is the slowloris bound: a connection that has not sent
	// a complete request line and headers by then is closed.
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownGrace     = 15 * time.Second

	// evictInterval sweeps the rate-limit and breadth maps.
	evictInterval = 5 * time.Minute

	// maxBodyBytes: this service answers GETs. Anything larger than a
	// generously sized header block is not a request we serve.
	maxBodyBytes = 64 << 10
)

// The rate limit. Deliberately generous for a human reading the map — a page
// load fans out to several API calls — and tight enough that a scraper walking
// every area hits it long before it finishes.
//
// One limit for the whole public surface, not one per route: separate buckets
// would let a client spend its full budget on every route in turn, so the real
// ceiling would be the sum, which is not the number anyone reasoned about.
var apiRate = ratelimit.Rate{PerSecond: 10, Burst: 60}

// bucketTTL is how long an idle client's bucket is kept. Long enough that a
// reader who steps away and comes back is still throttled on their old bucket
// rather than handed a fresh burst; short enough that the map stays bounded.
const bucketTTL = 30 * time.Minute

func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Catalogue == nil || opts.Snapshots == nil {
		return nil, errors.New("server: Catalogue and Snapshots are required")
	}

	resolver, err := httpx.NewIPResolver(opts.TrustedProxyCIDRs)
	if err != nil {
		return nil, fmt.Errorf("server: trusted proxies: %w", err)
	}

	renderer, err := web.NewRenderer(opts.Catalogue, opts.Snapshots, opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("server: renderer: %w", err)
	}

	limiter := ratelimit.New(apiRate, bucketTTL)
	breadth := ratelimit.NewBreadth(
		ratelimit.DistinctAreaLimit,
		ratelimit.DistinctSensorLimit,
		ratelimit.EnumerationWindow,
	)

	apiMux := api.NewRouter(api.Deps{
		Snapshots: opts.Snapshots,
		Breadth:   breadth,
		Store:     opts.Store,
		BaseURL:   opts.BaseURL,
	})

	// The API mounts under /api/; everything else is a page. One mux at the
	// root so exactly one middleware chain wraps the whole surface — a second
	// chain is a second place for a header to be forgotten.
	root := http.NewServeMux()
	root.Handle("/api/", apiMux)
	root.Handle("/", renderer.Routes())

	chain := httpx.Chain{
		Resolver:     resolver,
		Limiter:      limiter,
		MaxBodyBytes: maxBodyBytes,
	}

	s := &Server{
		public: &http.Server{
			Addr:              opts.ListenAddr,
			Handler:           chain.Wrap(metrics.Instrument(root)),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		},
		private: &http.Server{
			Addr:              opts.MetricsAddr,
			Handler:           privateMux(opts),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
		},
		limiter: limiter,
		breadth: breadth,
		log:     opts.Logger,
	}
	return s, nil
}

func privateMux(opts Options) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Liveness, not readiness: the process is up. Readiness would depend on
		// the snapshot, and a restart loop caused by a slow first ingest is a
		// worse outcome than a page that says "data is not ready yet".
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	metricsHandler := metrics.Handler()
	mux.Handle("GET /metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts.Publisher != nil {
			opts.Publisher.ObserveAge(time.Now().UTC())
		}
		metricsHandler.ServeHTTP(w, r)
	}))

	return mux
}

// Run starts both listeners and blocks until ctx is cancelled, then drains.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 2)

	go func() { errCh <- listen(s.public) }()
	go func() { errCh <- listen(s.private) }()

	// Sweeping the limiter and breadth maps is what keeps them bounded. Without
	// it the defence against memory exhaustion is itself the leak. Both
	// StartEvicting calls stop when ctx is cancelled.
	s.limiter.StartEvicting(ctx, evictInterval)
	s.breadth.StartEvicting(ctx, evictInterval)

	select {
	case <-ctx.Done():
		return s.shutdown()
	case err := <-errCh:
		if err != nil {
			// One listener dying must bring the other down too, rather than
			// leaving a half-serving process that a health check calls healthy.
			_ = s.shutdown()
			return err
		}
		// A nil error means that listener returned ErrServerClosed, which only
		// happens once shutdown is already under way.
		return s.shutdown()
	}
}

func listen(srv *http.Server) error {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listening on %s: %w", srv.Addr, err)
	}
	return nil
}

func (s *Server) shutdown() error {
	// A fresh context: ctx is already cancelled, and passing it would make
	// Shutdown return immediately and kill in-flight requests — the opposite of
	// graceful.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	s.log.Info("shutting down")
	err := s.public.Shutdown(ctx)
	if perr := s.private.Shutdown(ctx); err == nil {
		err = perr
	}
	return err
}
```

- [ ] **Step 5: Add the metrics instrumentation wrapper**

Append to `internal/metrics/metrics.go`:

```go
// Vec carries exactly one label dimension (Task 5), so route and status are
// two vectors rather than one two-label vector. That is not a workaround: the
// cross product of route x status is the cardinality that would need bounding,
// and keeping them separate makes the bound structural.
var (
	httpRequests  = CounterVec("airbg_http_requests_total", "HTTP requests served, by route.", "route")
	httpResponses = CounterVec("airbg_http_responses_total", "HTTP responses served, by status.", "status")
)

// Instrument counts requests by ROUTE PATTERN and status.
//
// r.Pattern, never r.URL.Path: the path is attacker-controlled, and one label
// per distinct path turns the metrics registry into an unbounded map that any
// client can grow — the counter that reports the attack becomes the attack.
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			// An unmatched request has no pattern. Labelling it with the path
			// would hand an attacker a way to grow the map at will.
			route = "unmatched"
		}
		httpRequests.With(route).Inc()
		httpResponses.With(strconv.Itoa(rec.status)).Inc()
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
```

Add `"net/http"` and `"strconv"` to that file's imports.

- [ ] **Step 6: Write the serve subcommand**

In `cmd/airbg/main.go`, add `serve` alongside the existing subcommands:

`main` already opens the pool and defers `pool.Close()` before the subcommand switch, so `serve` gets `pool`, `cfg` and `ctx` for free. Add a `case "serve":` that calls:

```go
func runServe(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	log := slog.Default()
	st := store.New(pool)
	holder := snapshot.NewHolder()
	pub := server.NewPublisher(st, holder, log)

	cat, err := i18n.Load()
	if err != nil {
		return err
	}

	// Build once at startup so the first visitor is not met with a 503 for a
	// whole poll interval. A failure here is logged, not fatal: the process
	// still serves "data is not ready yet" and the next cycle fixes it.
	if err := pub.Publish(ctx, time.Now().UTC()); err != nil {
		log.Error("initial snapshot build failed; starting with no data", "error", err)
	}

	srv, err := server.New(server.Options{
		ListenAddr:        cfg.ListenAddr,
		MetricsAddr:       cfg.MetricsAddr,
		Catalogue:         cat,
		Snapshots:         holder,
		Store:             st,
		Publisher:         pub,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
		BaseURL:           cfg.BaseURL,
		Logger:            log,
	})
	if err != nil {
		return err
	}

	// Same construction as the existing "collect" case — one poller, not two.
	ing := ingest.New(
		upstream.New(cfg.UpstreamURL, 30*time.Second),
		st,
		quality.NewHistory(12),
	)
	ing.SetSnapshotPublisher(pub)

	// The poller and the server share one process because the snapshot lives in
	// this process's memory: a separately deployed collector could fill the
	// database but could never swap the pointer this server reads.
	pollCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling()

	polled := make(chan struct{})
	go func() {
		defer close(polled)
		ing.Loop(pollCtx, cfg.PollInterval) // returns when pollCtx is cancelled
	}()

	err = srv.Run(ctx)

	// Stop the poller and wait for it, so the process does not exit with a
	// half-written cycle in flight.
	stopPolling()
	<-polled
	return err
}
```

Register it in the subcommand switch as `case "serve": if err := runServe(ctx, cfg, pool); err != nil { slog.Error("serve", "error", err); os.Exit(1) }`, and make `main` cancel `ctx` on SIGINT/SIGTERM with `signal.NotifyContext` if it does not already.

- [ ] **Step 7: Run the tests**

```bash
go build ./... && go vet ./... && go test ./... -race -count=1
```

Expected: PASS across the module.

- [ ] **Step 8: Prove the private-listener and timeout tests are not tautologies**

Register `GET /metrics` on `root` in `New` and confirm `TestMetricsAreNotOnThePublicListener` FAILS. Revert. Remove `ReadHeaderTimeout` from the public server and confirm `TestSlowClientIsDisconnected` FAILS by timing out. Revert. Pass the cancelled `ctx` to `Shutdown` instead of a fresh one and confirm requests in flight are cut — the cleanup's 10 s guard still passes, so verify this one by reading the code, and leave the comment in place as the record.

- [ ] **Step 9: Commit**

```bash
git add internal/server/ internal/metrics/ cmd/airbg/main.go
git commit -m "feat(server): serve the pages and API behind two listeners with hard timeouts"
```

---

## Task 18: End-to-end verification and documentation

One test that exercises the real chain against a real database, and the README the operator reads.

**Files:**
- Create: `internal/server/e2e_test.go`
- Modify: `README.md`
- Modify: `.env.example`

**Interfaces:**
- Consumes: everything.
- Produces: nothing new.

- [ ] **Step 1: Write the end-to-end test**

Create `internal/server/e2e_test.go`:

```go
//go:build integration

package server_test

// This is the only test in Phase 2 that runs the real chain against a real
// PostGIS database. Everything else stubs the store, which is the right trade
// for speed — but a stub cannot catch a swapped coordinate order in the SQL, a
// missing migration, or a quality filter that was written but never applied.
```

Then, following the harness the existing `internal/ingest` integration tests use (testcontainers + goose migrations), write:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestEndToEndOverviewReflectsIngestedReadings walks the whole path: seed
// readings → build a snapshot → serve /api/v1/overview → assert the numbers.
func TestEndToEndOverviewReflectsIngestedReadings(t *testing.T) {
	st, cleanup := newIntegrationStore(t) // testcontainers + migrations + one area
	defer cleanup()

	seedArea(t, st, "sofia", "oblast", 23.32, 42.69)
	// Four sensors: three usable, one out of range. The published average must
	// be over the three, and coverage must be met on three, not four.
	seedReading(t, st, 1, 23.30, 42.68, "P2", 10, "ok")
	seedReading(t, st, 2, 23.32, 42.69, "P2", 20, "ok")
	seedReading(t, st, 3, 23.34, 42.70, "P2", 30, "no_neighbours")
	seedReading(t, st, 4, 23.36, 42.71, "P2", 9000, "out_of_range")

	public, _ := runningWith(t, st)

	resp := get(t, public, "/api/v1/overview")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var body struct {
		Areas []struct {
			Slug        string             `json:"slug"`
			SensorCount int                `json:"sensor_count"`
			Metrics     map[string]float64 `json:"metrics"`
		} `json:"areas"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Areas) != 1 {
		t.Fatalf("areas = %d, want 1", len(body.Areas))
	}

	area := body.Areas[0]
	if area.SensorCount != 3 {
		t.Errorf("sensor_count = %d, want 3 — the out-of-range sensor was counted", area.SensorCount)
	}
	if got, want := area.Metrics["P2"], 20.0; got != want {
		t.Errorf("P2 = %v, want %v — 9000 leaked into the average", got, want)
	}
}

// TestEndToEndCoordinatesAreNotSwapped. PostGIS geography is (lon, lat) and the
// legacy app was (lat, long). Bulgaria's lon (22.3–28.7) and lat (41.2–44.3)
// ranges do not overlap, so a swap anywhere in the SQL, the scan, or the JSON
// is caught by asserting each against its own range.
func TestEndToEndCoordinatesAreNotSwapped(t *testing.T) {
	st, cleanup := newIntegrationStore(t)
	defer cleanup()

	seedArea(t, st, "sofia", "oblast", 23.32, 42.69)
	seedReading(t, st, 1, 23.30, 42.68, "P2", 10, "ok")
	seedReading(t, st, 2, 23.32, 42.69, "P2", 12, "ok")
	seedReading(t, st, 3, 23.34, 42.70, "P2", 14, "ok")

	public, _ := runningWith(t, st)

	resp := get(t, public, "/api/v1/area/sofia/sensors")
	var body struct {
		Lon []float64 `json:"lon"`
		Lat []float64 `json:"lat"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Lon) != 3 || len(body.Lat) != 3 {
		t.Fatalf("lon=%d lat=%d, want 3 each", len(body.Lon), len(body.Lat))
	}
	for i := range body.Lon {
		if body.Lon[i] < 22.0 || body.Lon[i] > 29.0 {
			t.Errorf("lon[%d] = %v, outside Bulgaria — coordinates are swapped", i, body.Lon[i])
		}
		if body.Lat[i] < 41.0 || body.Lat[i] > 45.0 {
			t.Errorf("lat[%d] = %v, outside Bulgaria — coordinates are swapped", i, body.Lat[i])
		}
	}
}

// TestEndToEndEnumerationTrips: walk more distinct areas than the limit from one
// address and confirm the wall comes up — the anti-extraction property this
// whole phase exists for, verified through the real middleware chain rather
// than against the counter in isolation.
func TestEndToEndEnumerationTrips(t *testing.T) {
	st, cleanup := newIntegrationStore(t)
	defer cleanup()

	for i := 0; i < 20; i++ {
		seedArea(t, st, fmt.Sprintf("area-%02d", i), "oblast", 23.0+float64(i)/100, 42.0+float64(i)/100)
	}

	public, _ := runningWith(t, st)

	var lastStatus int
	for i := 0; i < 20; i++ {
		lastStatus = get(t, public, fmt.Sprintf("/api/v1/area/area-%02d/sensors", i)).StatusCode
		if lastStatus == http.StatusTooManyRequests {
			return
		}
	}
	t.Errorf("walked 20 distinct areas from one address without tripping; last status %d", lastStatus)
}

// TestEndToEndPageRendersFromTheDatabase — one assertion that the HTML path and
// the JSON path see the same data.
func TestEndToEndPageRendersFromTheDatabase(t *testing.T) {
	st, cleanup := newIntegrationStore(t)
	defer cleanup()

	seedArea(t, st, "sofia", "oblast", 23.32, 42.69)
	seedReading(t, st, 1, 23.30, 42.68, "P2", 10, "ok")
	seedReading(t, st, 2, 23.32, 42.69, "P2", 12, "ok")
	seedReading(t, st, 3, 23.34, 42.70, "P2", 14, "ok")

	public, _ := runningWith(t, st)

	body := readAll(t, get(t, public, "/area/sofia"))
	if !strings.Contains(body, "3") {
		t.Error("the area page does not show the sensor count")
	}
	if strings.Contains(body, "Недостатъчно данни") {
		t.Error("a covered area is shown as uncovered")
	}
}
```

Write `newIntegrationStore`, `seedArea`, `seedReading`, `runningWith` and `readAll` in the same file, reusing the container helper from `internal/ingest`'s integration tests. **All seeding goes through parameterised pgx queries** — no `fmt.Sprintf` into SQL, test helpers included.

- [ ] **Step 2: Run the integration tests**

```bash
go test ./internal/server/ -tags=integration -race -count=1 -v
```

Expected: PASS, all four. These need Docker.

- [ ] **Step 3: Prove the coordinate test is not a tautology**

In `store.LatestSensors`, swap the two arguments of `ST_MakePoint` (or swap the scan targets). Confirm `TestEndToEndCoordinatesAreNotSwapped` FAILS on both lon and lat. Revert. In `AreaAggregates`, drop the `quality = ANY(...)` filter and confirm `TestEndToEndOverviewReflectsIngestedReadings` FAILS on the average. Revert.

- [ ] **Step 4: Update `.env.example`**

Append:

```sh
# --- Serving (Phase 2) ---

# Public HTTP listener. Keep this on loopback and reach it through a Cloudflare
# tunnel. Binding 0.0.0.0 exposes the origin directly, and a client that reaches
# the origin directly is not covered by any Cloudflare protection — only by the
# in-process token buckets.
AIRBG_LISTEN_ADDR=127.0.0.1:8080

# Private listener: /metrics and /healthz. Never route this publicly.
AIRBG_METRICS_ADDR=127.0.0.1:9090

# Peer ranges whose CF-Connecting-IP header is believed. EMPTY MEANS TRUST
# NOBODY, which is the correct value for local development and for any origin
# not behind Cloudflare. Setting this while the origin is also directly
# reachable lets anyone who can reach it spoof their client IP and bypass every
# rate limit — restrict the origin first, then set this.
# AIRBG_TRUSTED_PROXY_CIDRS=173.245.48.0/20,103.21.244.0/22,2400:cb00::/32

# Public origin, used for canonical and hreflang links. Must be absolute.
AIRBG_BASE_URL=http://localhost:8080
```

- [ ] **Step 5: Update `README.md`**

Add a "Serving" section covering: `airbg serve` runs the poller and the HTTP server in one process because the snapshot lives in that process's memory; the two listeners and why; the four new environment variables with the trust warning repeated; the endpoint list; and this paragraph verbatim:

```markdown
### Why there is no bounding-box endpoint

No endpoint accepts a bounding box or a coordinate window. The API is tiered
instead: a country-level overview, a city-level overview, and per-area detail
that must be requested one named area at a time. This is the anti-extraction
design — a bbox parameter would let one request return the whole country at
full resolution, and no rate limit can distinguish that request from a
legitimate one. Bulk extraction therefore requires enumerating areas, which is
what the breadth counters detect: they count *distinct* areas and sensors per
client, not request volume, so a reader refreshing one city forever is never
throttled while a crawler walking every area trips within a dozen requests.

If a future change adds a bbox parameter, this entire defence is gone. The test
`TestOverviewTakesNoBoundingBox` exists to make that change fail loudly.
```

Also add a "Known limitations" entry:

```markdown
- The origin must be unreachable except through Cloudflare. `AIRBG_TRUSTED_PROXY_CIDRS`
  makes the origin believe `CF-Connecting-IP` from those ranges; it cannot stop a
  client that reaches the origin some other way from being rate-limited as itself.
  A directly reachable origin with no network restriction means one attacker with
  many source addresses bypasses the per-client limits entirely. Restrict at the
  network layer (tunnel, firewall, or origin-pull authentication) — the header
  trust setting is not a substitute.
```

- [ ] **Step 6: Full verification**

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race -count=1
go test ./... -tags=integration -race -count=1
```

Expected: no `gofmt` output, all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/server/e2e_test.go README.md .env.example
git commit -m "test(server): verify the served output end to end against a real database"
```

---

## Done

Phase 2 is complete when `airbg serve` runs, `/api/v1/overview` answers with real data, every area page renders in both languages, and `go test ./... -tags=integration -race` is green.

Phase 3 consumes exactly the endpoints listed in Task 10's route table. Nothing in that table takes a bounding box, and nothing added later should.
