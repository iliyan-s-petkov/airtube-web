# airbg.org Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the capacity and denial-of-service gaps that Phase 2 left open, so the Phase 3a frontend lands on a server that already holds at thousands of concurrent readers.

**Architecture:** Five independent server-side changes, ordered by how much load each removes. The first moves the only per-request database query on a normal page view into the precomputed snapshot, making the steady-state page-load fan-out zero queries. The remaining four bound what is left: a non-blocking admission semaphore in front of the residual database paths, a short scoped statement timeout inside them, a counted `net.Listener` in front of the public server, and two more response headers.

**Tech Stack:** Go 1.26, stdlib only (`net`, `net/http`, `sync`), `pgx/v5` for parameterised SQL, PostgreSQL 18 + PostGIS + TimescaleDB. Tests are stdlib `testing`; database tests use the existing `internal/testsupport` testcontainer helpers.

**Source spec:** `docs/superpowers/specs/2026-08-11-airbg-phase3a-frontend-design.md` §7.1, §12.2, §12.3, §13.2, §13.3, §13.5.

**Scope boundary — read this before starting.** This plan covers only hardening that is independent of the frontend, so all five tasks land on `master` before any Node toolchain exists. Two hardening items from the spec are deliberately **not** here because they are meaningless without the frontend, and they belong to the Phase 3a plan:

- `CSPValue` becoming `CSP(basemapHost string) string` (spec §6.5, §13.2) — there is no basemap host until the map island exists.
- The npm supply-chain controls: committed lockfile, `npm ci --ignore-scripts`, exact-pinned direct dependencies, `npm audit --audit-level=high` failing the build (spec §13.1) — there is no `package.json` yet.

One further spec item, §12.3a (separate connection pools for the API and the collector), is **already done**: it landed on `master` as commit `258c4dc` before this plan was written. Do not re-implement it. `db.OpenPair`, `config.DBAPIConns` and `config.DBCollectorConns` exist and are what Task 2's default size is derived from.

## Global Constraints

- **No new third-party Go dependency, ever.** `go.mod`'s direct require block must not change. The permitted set is exactly: `github.com/jackc/pgx/v5 v5.10.0`, `github.com/pressly/goose/v3 v3.27.3`, `github.com/testcontainers/testcontainers-go v0.44.0`. Task 4 hand-rolls a limiting listener specifically to avoid `golang.org/x/net/netutil`.
- **Testing is stdlib `testing` only.** No testify, no ginkgo, no assertion helpers. Hand-written `if got != want { t.Errorf("X = %v, want %v", got, want) }`. Table-driven subtests via `t.Run`. `t.Setenv` for config isolation, `t.Cleanup` for teardown.
- **Every load-bearing property must be mutation-proven.** Twelve Phase 2 tests once passed while inert. Each task below has an explicit mutation step: break the production code, quote the real failure, revert. A mutation of *test* code proves nothing. If a mutation genuinely comes out inert, say so plainly rather than hunting a cleverer one.
- **SQLSTATE 42P18** (unreferenced bind parameter) is not valid mutation evidence — it is the SQL analogue of a compile error.
- **Never revert a mutation with `git checkout`.** Copy the file aside first (`cp x.go /tmp/x.go.orig`), restore from the copy, then verify `git diff` is byte-identical. `git checkout` has already destroyed uncommitted work once on this project.
- **All SQL through `pgx` parameterised queries.** String-concatenated SQL is forbidden project-wide, test helpers included.
- **`internal/metrics` counters are process-global.** Assert deltas, never absolute counts. Follow the `SeriesRateLimitedCountForTesting` pattern for any new counter a test must read.
- **Configuration is environment-only**, prefix `AIRBG_`, no secret in the repo, no config file. Every new variable must be added to `README.md` (both tables) and `.env.example` in the same commit as the code.
- **`www-root/`** is the legacy PHP app. Never modify it.
- **Commits:** no `Co-Authored-By: Claude` trailer, no "Generated with Claude Code" line. `CLAUDE.md` is gitignored and must never be staged, not even with `git add -f`.
- **`git log` needs `--no-show-signature`** in this repo (`log.showSignature` is enabled).
- Only `internal/server/e2e_test.go` carries `//go:build integration`. Store, db and snapshot testcontainer tests run in the default suite — so `go test ./...` starts containers, and that is expected.

---

### Task 1: Serve the default area series from the snapshot

**Why first:** this is the change that removes load rather than bounding it. Phase 2 serves `/api/v1/area/{slug}/series` from Postgres on every request, and nothing called it. Phase 3a calls it on every area page view, which would make the chart the only database-backed page view on the site. The 24-hour area mean changes once per ingest cycle — identical to every other payload already in the snapshot — so serving it per request from Postgres was the anomaly, not the optimisation.

**Files:**
- Modify: `internal/store/aggregate.go` — add `AllAreaSeries` and its two SQL constants beside the existing `areaRawSeriesSQL` / `areaHourlySeriesSQL`
- Modify: `internal/snapshot/snapshot.go` — `Snapshot.AreaSeries`, the exported `SeriesPayload` type, the three `DefaultSeries*` constants
- Modify: `internal/snapshot/build.go` — build `AreaSeries`, add `seriesPayloadFrom`
- Modify: `internal/api/series.go` — delete the local `seriesBody`, use `snapshot.SeriesPayload`, add the snapshot fast path to `handleAreaSeries`
- Test: `internal/store/aggregate_test.go` (append), `internal/snapshot/build_test.go` (append), `internal/api/series_test.go` (append)

**Interfaces:**
- Consumes: `store.Point{Time time.Time; Value float64}`, `snapshot.encode(payload any) (Body, error)`, `api.serveBody(w, r, b snapshot.Body, visibility string, maxAge int)`, `api.maxAgeFor(period string) int`, `api.cachePrivate`, `store.usableQuality = []string{"ok","no_neighbours"}`.
- Produces:
  - `func (s *Store) AllAreaSeries(ctx context.Context, metric string, since time.Time, hourly bool) (map[string][]Point, error)`
  - `snapshot.SeriesPayload` (exported; replaces the unexported `api.seriesBody` — same JSON tags)
  - `snapshot.Snapshot.AreaSeries map[string]Body`
  - `snapshot.DefaultSeriesMetric = "P2"`, `snapshot.DefaultSeriesPeriod = "24h"`, `snapshot.DefaultSeriesWindow = 24 * time.Hour`

**Design note for the implementer — read before writing code.** Two things here are easy to get subtly wrong.

1. **One query, not one per area.** `Build` runs on the collector pool, which has 4 connections. Looping `AreaSeries(slug)` over every known slug would issue one query per area per ingest cycle — hundreds, once the neighbourhood boundaries are imported. `AllAreaSeries` drops the `a.slug = $1` filter and groups by `(slug, time)` instead, returning every area in one round trip.
2. **The payload type moves rather than being duplicated.** `api.seriesBody` is unexported, and `snapshot` cannot import `api` (`api` imports `snapshot`). Defining a second struct with the same tags in `snapshot` would mean two JSON shapes that must be kept identical by discipline. Instead the type moves to `snapshot` and `api` uses it, so the snapshot-served and database-served responses are the same shape by construction. `snapshot.zeroGeneratedAt`'s `default:` branch already handles an unknown payload type correctly — `SeriesPayload` has no `GeneratedAt` field, so hashing it as-is is right.

- [ ] **Step 1: Write the failing store test**

Append to `internal/store/aggregate_test.go`:

```go
// TestAllAreaSeriesMatchesThePerAreaQuery is the anti-drift test. AllAreaSeries
// and AreaSeries are two SQL statements that must produce the same numbers, and
// the snapshot path only serves the right data for as long as they agree. Any
// future edit to one that is not mirrored in the other fails here rather than
// silently shipping a chart that disagrees with the database-backed fall-through.
func TestAllAreaSeriesMatchesThePerAreaQuery(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	s := New(pool)

	// Two sensors in one area, one in another, and two readings at the SAME
	// timestamp so the grouping is exercised rather than assumed.
	seedAreaSeriesFixture(t, ctx, pool)

	since := time.Now().UTC().Add(-24 * time.Hour)
	all, err := s.AllAreaSeries(ctx, "P2", since, false)
	if err != nil {
		t.Fatalf("AllAreaSeries: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("AllAreaSeries returned no areas; the fixture is not being seen, so this test proves nothing")
	}

	for slug, batched := range all {
		single, err := s.AreaSeries(ctx, slug, "P2", since, false)
		if err != nil {
			t.Fatalf("AreaSeries(%q): %v", slug, err)
		}
		if len(batched) != len(single) {
			t.Fatalf("area %q: AllAreaSeries returned %d points, AreaSeries returned %d", slug, len(batched), len(single))
		}
		for i := range single {
			if !batched[i].Time.Equal(single[i].Time) {
				t.Errorf("area %q point %d: time = %v, want %v", slug, i, batched[i].Time, single[i].Time)
			}
			if batched[i].Value != single[i].Value {
				t.Errorf("area %q point %d: value = %v, want %v", slug, i, batched[i].Value, single[i].Value)
			}
		}
	}
}

// TestAllAreaSeriesGroupsSensorsAtTheSameInstant pins the averaging directly.
// Without the (slug, time) grouping the result is every sensor's reading in
// timestamp order, which renders as a sawtooth a reader would interpret as
// rapid air-quality swings rather than as two sensors disagreeing.
func TestAllAreaSeriesGroupsSensorsAtTheSameInstant(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	s := New(pool)

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20) // one area, one timestamp, values 10 and 20

	points, err := s.AllAreaSeries(ctx, "P2", at.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AllAreaSeries: %v", err)
	}
	got := points[slug]
	if len(got) != 1 {
		t.Fatalf("got %d points for %q, want 1 — the two sensors were not grouped", len(got), slug)
	}
	if got[0].Value != 15 {
		t.Errorf("value = %v, want 15 (the mean of 10 and 20)", got[0].Value)
	}
}
```

Write `seedAreaSeriesFixture` and `seedTwoSensorsOneInstant` as helpers in the same file, following whatever seeding helper `aggregate_test.go` already uses — read the existing tests in that file first and reuse their inserts rather than writing new ones. All inserts go through `pool.Exec(ctx, "INSERT ...", args...)` with bind parameters; never build SQL with `fmt.Sprintf`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run AllAreaSeries -count=1`
Expected: FAIL, `s.AllAreaSeries undefined (type *Store has no field or method AllAreaSeries)`.

- [ ] **Step 3: Implement `AllAreaSeries`**

In `internal/store/aggregate.go`, directly below `areaHourlySeriesSQL`:

```go
// allAreaRawSeriesSQL is areaRawSeriesSQL for EVERY area in one round trip.
//
// The per-area query exists for the database-backed fall-through, where the
// caller has named one slug. This one exists for snapshot.Build, which needs all
// of them at once: looping the per-area query would issue one query per area per
// ingest cycle — hundreds once neighbourhood boundaries are imported — against
// the collector pool's four connections.
//
// Grouped by (slug, time), so a sensor belonging to two areas contributes to
// both means, and two sensors reporting at the same instant produce one point.
// Ordered by slug then time, so the scan below can rely on time order within
// each slug without sorting afterwards.
const allAreaRawSeriesSQL = `
SELECT a.slug, r.time, avg(r.value)
  FROM reading r
  JOIN area_sensor asx ON asx.sensor_id = r.sensor_id
  JOIN area a          ON a.slug = asx.area_slug
 WHERE r.metric = $1
   AND r.time  >= $2
   AND r.quality = ANY($3::quality_flag[])
 GROUP BY a.slug, r.time
 ORDER BY a.slug, r.time`

// allAreaHourlySeriesSQL is the same over the rollup. reading_hourly carries no
// quality column: the rollup is built from readings that already passed the
// filter.
const allAreaHourlySeriesSQL = `
SELECT a.slug, h.bucket, avg(h.avg_value)
  FROM reading_hourly h
  JOIN area_sensor asx ON asx.sensor_id = h.sensor_id
  JOIN area a          ON a.slug = asx.area_slug
 WHERE h.metric = $1
   AND h.bucket >= $2
 GROUP BY a.slug, h.bucket
 ORDER BY a.slug, h.bucket`

// AllAreaSeries returns the area-mean series for one metric, for every area
// that has data in the window, keyed by slug.
//
// Areas with no readings are absent from the map rather than present with an
// empty slice. snapshot.Build iterates its known slugs and looks each one up, so
// a missing key is the correct representation of "no data" there — and a caller
// that needs an entry per area must iterate its own slug set, not this map.
func (s *Store) AllAreaSeries(ctx context.Context, metric string, since time.Time, hourly bool) (map[string][]Point, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if hourly {
		rows, err = s.pool.Query(ctx, allAreaHourlySeriesSQL, metric, since)
	} else {
		rows, err = s.pool.Query(ctx, allAreaRawSeriesSQL, metric, since, usableQuality)
	}
	if err != nil {
		return nil, fmt.Errorf("store: all area series for %q: %w", metric, err)
	}
	defer rows.Close()

	out := make(map[string][]Point)
	for rows.Next() {
		var (
			slug string
			p    Point
		)
		if err := rows.Scan(&slug, &p.Time, &p.Value); err != nil {
			return nil, fmt.Errorf("store: scan all area series: %w", err)
		}
		out[slug] = append(out[slug], p)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the store tests**

Run: `go test ./internal/store/ -run AllAreaSeries -count=1`
Expected: PASS.

- [ ] **Step 5: Mutation-prove the grouping and the parity**

Two mutations, one at a time, each reverted via a file copy:

1. In `allAreaRawSeriesSQL`, change `GROUP BY a.slug, r.time` to `GROUP BY a.slug, r.time, r.sensor_id`. Expected: `TestAllAreaSeriesGroupsSensorsAtTheSameInstant` fails with `got 2 points for "…", want 1`, and the parity test fails on the point count.
2. Change `r.quality = ANY($3::quality_flag[])` to `r.quality IS NOT NULL`. Expected: the parity test fails, because `AreaSeries` still filters and this one no longer does. If the fixture has no non-usable readings, this mutation is inert — in that case add an `out_of_range` reading to `seedAreaSeriesFixture` first, then re-run, so the filter is genuinely covered.

Quote the real failure output for each. Revert with `cp`, then confirm `git diff internal/store/aggregate.go` shows only the intended addition.

- [ ] **Step 6: Commit the store half**

```bash
git add internal/store/aggregate.go internal/store/aggregate_test.go
git commit -m "feat(store): add AllAreaSeries, one query for every area's mean series"
```

- [ ] **Step 7: Write the failing snapshot test**

Append to `internal/snapshot/build_test.go`:

```go
// TestBuildIncludesAreaSeriesForEveryKnownSlug mirrors the AreaSensors rule: a
// missing key must mean "no such area" (404), never "this area happens to have
// no history" (which must be a 200 with empty arrays). An area page for a quiet
// area must render an empty chart, not a not-found.
func TestBuildIncludesAreaSeriesForEveryKnownSlug(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	seedAreasWithOneEmptyArea(t, ctx, pool)

	snap, err := Build(ctx, store.New(pool), time.Now().UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.KnownSlugs) == 0 {
		t.Fatal("no known slugs; the fixture is not being seen")
	}
	for slug := range snap.KnownSlugs {
		body, ok := snap.AreaSeries[slug]
		if !ok {
			t.Errorf("AreaSeries has no entry for known slug %q", slug)
			continue
		}
		if len(body.JSON) == 0 || len(body.Gzip) == 0 || body.ETag == "" {
			t.Errorf("AreaSeries[%q] is not fully prepared: json=%d gzip=%d etag=%q",
				slug, len(body.JSON), len(body.Gzip), body.ETag)
		}
	}
}

// TestAreaSeriesPayloadUsesEmptyArraysNotNull guards the exact failure a nil
// slice causes: `null` reaches uPlot, which throws instead of drawing an empty
// axis. writeSeries already allocates with make for the same reason; the
// snapshot path must not reintroduce the bug on the other side.
func TestAreaSeriesPayloadUsesEmptyArraysNotNull(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	seedAreasWithOneEmptyArea(t, ctx, pool)

	snap, err := Build(ctx, store.New(pool), time.Now().UTC())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var found bool
	for slug, body := range snap.AreaSeries {
		if !strings.Contains(string(body.JSON), `"t":[]`) {
			continue
		}
		found = true
		if strings.Contains(string(body.JSON), "null") {
			t.Errorf("AreaSeries[%q] contains null: %s", slug, body.JSON)
		}
	}
	if !found {
		t.Fatal("no empty series in the snapshot; the fixture must include an area with no readings or this test proves nothing")
	}
}
```

`seedAreasWithOneEmptyArea` must reuse whatever seeding the existing `TestBuildIncludesEmptyAreasInAreaSensors` uses — read it and extend it rather than writing a parallel fixture.

- [ ] **Step 8: Run to verify it fails**

Run: `go test ./internal/snapshot/ -run 'AreaSeries' -count=1`
Expected: FAIL, `snap.AreaSeries undefined (type *Snapshot has no field or method AreaSeries)`.

- [ ] **Step 9: Add the type, the constants and the field**

In `internal/snapshot/snapshot.go`:

```go
// The one series the frontend draws by default. Exported because two packages
// must agree on it: snapshot.Build precomputes exactly this combination, and
// api.handleAreaSeries serves from the snapshot for exactly this combination.
// A literal in each package would let them drift, and the symptom would be a
// silent fall-through to the database on every page view — which is the thing
// this whole change exists to prevent.
//
// DefaultSeriesWindow must equal the window api.parsePeriod derives from
// DefaultSeriesPeriod. TestDefaultSeriesPeriodMatchesParsePeriod pins that.
const (
	DefaultSeriesMetric = "P2"
	DefaultSeriesPeriod = "24h"
	DefaultSeriesWindow = 24 * time.Hour
)

// SeriesPayload is the wire shape of both series endpoints.
//
// Columnar because uPlot consumes parallel arrays directly and same-typed
// adjacent values compress well. It lives here, rather than in the api package
// where it started, because the snapshot must produce byte-identical responses
// to the database-backed path: api can import snapshot, snapshot cannot import
// api, and two structs with matching tags would be a shape that has to be kept
// identical by discipline instead of by the compiler.
type SeriesPayload struct {
	SensorID *int64      `json:"sensor_id,omitempty"`
	Slug     string      `json:"slug,omitempty"`
	Metric   string      `json:"metric"`
	Period   string      `json:"period"`
	Hourly   bool        `json:"hourly"`
	Times    []time.Time `json:"t"`
	Values   []float64   `json:"v"`
}
```

Add to `Snapshot`, below `AreaSensors`:

```go
	// AreaSeries is the DefaultSeriesMetric / DefaultSeriesPeriod history for
	// each area, keyed by slug. Present for every known slug, with empty arrays
	// where an area has no readings — same rule as AreaSensors, for the same
	// reason: a missing key must mean 404, not "quiet area".
	//
	// Only the default combination is precomputed. Every other metric and
	// period stays database-backed on purpose: precomputing them means a
	// payload per area per metric per period, which is a cache larger than the
	// data.
	AreaSeries map[string]Body
```

- [ ] **Step 10: Build the series bodies**

In `internal/snapshot/build.go`, inside `Build`, after the `LatestSensors` call:

```go
	// One query for every area, not one per area: Build runs on the collector
	// pool (4 connections) and the neighbourhood import multiplies the area
	// count by an order of magnitude.
	seriesBySlug, err := s.AllAreaSeries(ctx, DefaultSeriesMetric, now.Add(-DefaultSeriesWindow), false)
	if err != nil {
		return nil, fmt.Errorf("snapshot: area series: %w", err)
	}
```

Add `AreaSeries: make(map[string]Body, len(all))` to the `&Snapshot{...}` literal, then extend the existing `for slug := range snap.KnownSlugs` loop (the one that fills `AreaSensors`) with:

```go
		seriesBody, err := encode(seriesPayloadFrom(slug, seriesBySlug[slug]))
		if err != nil {
			return nil, fmt.Errorf("snapshot: encode series for %q: %w", slug, err)
		}
		snap.AreaSeries[slug] = seriesBody
```

And the helper, beside `sensorPayloadFrom`:

```go
// seriesPayloadFrom converts store points to the wire shape.
//
// The slices are allocated with make even when there are no points: a nil slice
// marshals to `null`, and a charting library handed null throws instead of
// drawing an empty axis.
func seriesPayloadFrom(slug string, points []store.Point) SeriesPayload {
	p := SeriesPayload{
		Slug:   slug,
		Metric: DefaultSeriesMetric,
		Period: DefaultSeriesPeriod,
		Hourly: false,
		Times:  make([]time.Time, 0, len(points)),
		Values: make([]float64, 0, len(points)),
	}
	for _, pt := range points {
		p.Times = append(p.Times, pt.Time)
		p.Values = append(p.Values, pt.Value)
	}
	return p
}
```

- [ ] **Step 11: Run the snapshot tests**

Run: `go test ./internal/snapshot/ -count=1`
Expected: PASS, including the pre-existing tests.

- [ ] **Step 12: Mutation-prove the snapshot half**

1. Change `Times: make([]time.Time, 0, len(points))` to `var` declarations (nil slices). Expected: `TestAreaSeriesPayloadUsesEmptyArraysNotNull` fails on `"t":null`.
2. Change the fill loop to iterate `seriesBySlug` instead of `snap.KnownSlugs`. Expected: `TestBuildIncludesAreaSeriesForEveryKnownSlug` fails with `AreaSeries has no entry for known slug "…"` for the empty area.

Revert each with `cp`. Confirm `git diff` clean of mutations.

- [ ] **Step 13: Commit the snapshot half**

```bash
git add internal/snapshot/snapshot.go internal/snapshot/build.go internal/snapshot/build_test.go
git commit -m "feat(snapshot): precompute the default 24h area series per area"
```

- [ ] **Step 14: Write the failing handler tests**

Append to `internal/api/series_test.go`. Read the file first: it already has a stub `DataSource` — extend that stub with a call counter rather than writing a second one.

```go
// TestDefaultAreaSeriesIsServedFromTheSnapshot is the whole point of the change:
// the combination the frontend requests on every area page view must cost zero
// database queries.
func TestDefaultAreaSeriesIsServedFromTheSnapshot(t *testing.T) {
	stub := &stubSource{}
	srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, "sofia"))

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=24h"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	if stub.areaSeriesCalls != 0 {
		t.Errorf("AreaSeries called %d times, want 0 — the request reached the database", stub.areaSeriesCalls)
	}
	if got := rec.Header().Get("ETag"); got == "" {
		t.Error("no ETag; the snapshot path must serve the prepared body, not a re-marshalled one")
	}
	// private, not public: a series response is keyed by slug, so it is
	// enumerable and must never sit in a shared cache the breadth counter
	// cannot see.
	if got := rec.Header().Get("Cache-Control"); got != "private, max-age=150" {
		t.Errorf("Cache-Control = %q, want \"private, max-age=150\"", got)
	}
}

// TestNonDefaultAreaSeriesFallsThroughToTheDatabase. Only one combination is
// precomputed; every other one must still work, or the metric and period
// selectors in 3b have nothing to call.
func TestNonDefaultAreaSeriesFallsThroughToTheDatabase(t *testing.T) {
	for _, q := range []string{"metric=P1&period=24h", "metric=P2&period=7d"} {
		t.Run(q, func(t *testing.T) {
			stub := &stubSource{}
			srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, "sofia"))

			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?"+q))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
			}
			if stub.areaSeriesCalls != 1 {
				t.Errorf("AreaSeries called %d times, want 1", stub.areaSeriesCalls)
			}
		})
	}
}

// TestSnapshotSeriesSpendsNoSeriesToken. The series bucket is 1 rps / burst 10
// and exists to protect Postgres. A request that issues no query must not spend
// from it, or the frontend's own page views would exhaust the budget that is
// there to bound the expensive path.
func TestSnapshotSeriesSpendsNoSeriesToken(t *testing.T) {
	stub := &stubSource{}
	srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, "sofia"))

	before := SeriesRateLimitedCountForTesting("area")
	// Comfortably more than the burst of 10.
	for i := 0; i < 40; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=24h"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 — the snapshot path is spending tokens", i, rec.Code)
		}
	}
	// Delta, not absolute: the counter is process-global.
	if got := SeriesRateLimitedCountForTesting("area") - before; got != 0 {
		t.Errorf("series refusals = %d, want 0", got)
	}
}

// TestSnapshotSeriesIsStillCountedForBreadth. The response is per-entity and
// enumerable whether it came from memory or from Postgres. If the fast path
// skipped ObserveArea, a scraper could walk every slug's history for free —
// which is precisely the extraction the tiering design exists to prevent.
func TestSnapshotSeriesIsStillCountedForBreadth(t *testing.T) {
	stub := &stubSource{}
	slugs := manySlugs(t, breadthAreaLimitForTesting()+5) // more distinct slugs than the limit allows
	srv := newTestRouter(t, stub, snapshotWithAreaSeries(t, slugs...))

	var refused bool
	for _, slug := range slugs {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/"+slug+"/series?metric=P2&period=24h"))
		if rec.Code == http.StatusTooManyRequests {
			refused = true
			break
		}
	}
	if !refused {
		t.Error("walked more distinct slugs than the breadth limit allows without a single refusal")
	}
}

// TestDefaultSeriesPeriodMatchesParsePeriod ties the snapshot's hardcoded window
// to the api package's period vocabulary. Build derives `since` from
// DefaultSeriesWindow; the handler derives it from parsePeriod. If those two ever
// disagree, the snapshot serves a window that is not the one the label claims,
// and nothing else in the suite would notice.
func TestDefaultSeriesPeriodMatchesParsePeriod(t *testing.T) {
	window, hourly, ok := parsePeriod(snapshot.DefaultSeriesPeriod)
	if !ok {
		t.Fatalf("parsePeriod(%q) rejected the snapshot's default period", snapshot.DefaultSeriesPeriod)
	}
	if window != snapshot.DefaultSeriesWindow {
		t.Errorf("window = %v, want %v", window, snapshot.DefaultSeriesWindow)
	}
	if hourly {
		t.Error("hourly = true, but Build precomputes the raw series (hourly=false)")
	}
}
```

`newTestRouter`, `newSeriesRequest`, `snapshotWithAreaSeries`, `manySlugs` and `breadthAreaLimitForTesting` must be built from what `internal/api`'s existing tests already do — read `series_test.go` and `sensors_test.go` and reuse their router construction and their client-IP context plumbing (the breadth counter and the limiter key both come from `httpx.BucketKeyFrom`, so a request without that context keys everything to the same bucket). If no exported accessor for the breadth area limit exists, add one next to `SeriesRateLimitedCountForTesting`, following its comment style.

- [ ] **Step 15: Run to verify they fail**

Run: `go test ./internal/api/ -run 'AreaSeries|SnapshotSeries|DefaultSeriesPeriod' -count=1`
Expected: FAIL — `snapshotWithAreaSeries` undefined, and once that compiles, `AreaSeries called 1 times, want 0`.

- [ ] **Step 16: Replace `seriesBody` with `snapshot.SeriesPayload`**

In `internal/api/series.go`: delete the `seriesBody` struct, and change `writeSeries`'s signature to `func writeSeries(w http.ResponseWriter, body snapshot.SeriesPayload, points []store.Point)`. Update its two call sites (`handleSensorSeries`, `handleAreaSeries`) to construct `snapshot.SeriesPayload{...}`. The field names and tags are identical, so this is mechanical. Keep the existing comment on the `make` allocations — it explains the `null` hazard and is still load-bearing.

- [ ] **Step 17: Add the fast path**

In `handleAreaSeries`, between the breadth check and `d.allowSeriesQuery`:

```go
	// Serve the one precomputed combination from memory. Placed here, not
	// earlier, and not later, for two reasons that are both load-bearing:
	//
	//   - AFTER the breadth check, because the response is per-entity and
	//     enumerable regardless of where it came from. A fast path that skipped
	//     ObserveArea would let a scraper walk every slug's history for free.
	//   - BEFORE allowSeriesQuery, because this request issues no query. The
	//     series bucket exists to protect Postgres, and spending its tokens on
	//     requests that never reach Postgres would starve the path it is
	//     actually guarding.
	if metric == snapshot.DefaultSeriesMetric && period == snapshot.DefaultSeriesPeriod {
		if body, ok := snap.AreaSeries[slug]; ok {
			serveBody(w, r, body, cachePrivate, maxAgeFor(period))
			return
		}
		// No entry for a known slug means a snapshot built before this field
		// existed, or a build that failed partway. Fall through to the database
		// rather than 404 a slug we know exists.
	}
```

- [ ] **Step 18: Run the api tests, then the whole suite**

Run: `go test ./internal/api/ -count=1` then `go test ./... -count=1` and `go test -tags=integration ./internal/server/ -count=1`
Expected: PASS everywhere. If an existing e2e assertion breaks because the default series no longer hits the stub store, fix the assertion to match the new (correct) behaviour and say so in the commit.

- [ ] **Step 19: Mutation-prove the handler placement**

Three mutations, one at a time — placement is the entire design here, so each ordering property gets its own proof:

1. Move the fast-path block **above** the `d.Breadth.ObserveArea` call. Expected: `TestSnapshotSeriesIsStillCountedForBreadth` fails with `walked more distinct slugs than the breadth limit allows without a single refusal`.
2. Move it **below** `d.allowSeriesQuery`. Expected: `TestSnapshotSeriesSpendsNoSeriesToken` fails with a 429 partway through the 40 requests.
3. Change the condition to `metric == snapshot.DefaultSeriesMetric` only (dropping the period check). Expected: `TestNonDefaultAreaSeriesFallsThroughToTheDatabase/metric=P2&period=7d` fails with `AreaSeries called 0 times, want 1` — a 7-day request answered with 24 hours of data.

Quote each real failure. Revert via `cp`.

- [ ] **Step 20: Commit**

```bash
git add internal/api/series.go internal/api/series_test.go
git commit -m "feat(api): serve the default area series from the snapshot"
```

---

### Task 2: Non-blocking admission control on the database-backed routes

**Why:** the series limiter is per IP prefix — 1 rps, burst 10 — so it bounds one client and says nothing about aggregate load. N distinct client prefixes are collectively permitted N requests per second, funnelled into 8 connections. Excess requests block inside `pool.Acquire`, each holding a goroutine and a socket, until the 30 s `WriteTimeout` fires — at which point a large fraction fail at once. That is queue collapse, and the per-IP limiter never sees it coming, because every individual client is behaving perfectly. Failing 10 % of requests in 2 ms is a functioning site under load; queueing 100 % of them for 30 s is an outage for every reader who only wanted the map.

**Files:**
- Create: `internal/admit/admit.go`, `internal/admit/admit_test.go`
- Modify: `internal/api/router.go` — `Deps.Admission`, the fail-closed default
- Modify: `internal/api/series.go` — `admitQuery`, wired into both series queries
- Modify: `internal/api/locate.go` — wired around `AreaAtPoint`
- Modify: `internal/config/config.go`, `internal/config/config_test.go` — `AIRBG_MAX_DB_INFLIGHT`
- Modify: `internal/server/server.go` — pass `Options.MaxDBInflight` into `api.Deps`
- Modify: `cmd/airbg/main.go` — pass `cfg.MaxDBInflight` into `server.Options`
- Modify: `README.md`, `.env.example`
- Test: `internal/api/series_test.go`, `internal/api/locate_test.go` (append)

**Interfaces:**
- Consumes: `config.envPositiveInt32(key string, fallback int32) (int32, error)` — already exists from `258c4dc`; reuse it, do not write a second parser. `metrics.CounterVec(name, help, label string) *Vec`.
- Produces:
  - `func admit.New(size int) (*admit.Semaphore, error)`
  - `func (s *Semaphore) TryAcquire() (release func(), ok bool)`
  - `func (s *Semaphore) InFlight() int`
  - `config.Config.MaxDBInflight int32`, env `AIRBG_MAX_DB_INFLIGHT`, default `16`
  - `server.Options.MaxDBInflight int32`
  - metric `airbg_admission_rejected_total{route}`

- [ ] **Step 1: Write the failing semaphore test**

Create `internal/admit/admit_test.go`:

```go
package admit

import "testing"

// TestTryAcquireRefusesWhenFull is the property the whole package exists for:
// refusal must be immediate. A blocking acquire would just move the queue from
// pgxpool into this package.
func TestTryAcquireRefusesWhenFull(t *testing.T) {
	s, err := New(2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r1, ok := s.TryAcquire()
	if !ok {
		t.Fatal("first acquire failed on an empty semaphore")
	}
	r2, ok := s.TryAcquire()
	if !ok {
		t.Fatal("second acquire failed with a slot still free")
	}
	if _, ok := s.TryAcquire(); ok {
		t.Fatal("third acquire succeeded; the size of 2 is not being enforced")
	}
	if got := s.InFlight(); got != 2 {
		t.Errorf("InFlight = %d, want 2", got)
	}

	r1()
	if _, ok := s.TryAcquire(); !ok {
		t.Fatal("acquire failed after a release; the slot was not returned")
	}
	r2()
}

// TestReleaseIsIdempotent. A handler with an early return can plausibly call
// release twice. Crediting a slot twice would let the semaphore hand out more
// than `size` concurrent slots — a cap that silently stops capping is worse
// than no cap, because nothing looks wrong.
func TestReleaseIsIdempotent(t *testing.T) {
	s, err := New(1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	release, ok := s.TryAcquire()
	if !ok {
		t.Fatal("acquire failed on an empty semaphore")
	}
	release()
	release()
	release()

	if got := s.InFlight(); got != 0 {
		t.Errorf("InFlight = %d, want 0", got)
	}
	if _, ok := s.TryAcquire(); !ok {
		t.Fatal("acquire failed after release")
	}
	if _, ok := s.TryAcquire(); ok {
		t.Fatal("a second slot was available; the repeated releases over-credited the semaphore")
	}
}

// TestNewRejectsNonPositiveSizes fails closed. A zero-sized semaphore refuses
// every request (a total outage) and a negative one is meaningless; both are
// configuration mistakes that must be caught at startup, where the message can
// name the variable.
func TestNewRejectsNonPositiveSizes(t *testing.T) {
	for _, size := range []int{0, -1} {
		if _, err := New(size); err == nil {
			t.Errorf("New(%d) returned no error", size)
		}
	}
}

// TestConcurrentAcquireNeverExceedsTheSize is the -race test. The counter and
// the slot channel must agree under contention, or the cap is advisory.
func TestConcurrentAcquireNeverExceedsTheSize(t *testing.T) {
	const size = 4
	s, err := New(size)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := make(chan struct{})
	done := make(chan struct{})
	for i := 0; i < 64; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			<-start
			release, ok := s.TryAcquire()
			if !ok {
				return
			}
			if n := s.InFlight(); n > size {
				t.Errorf("InFlight = %d, want <= %d", n, size)
			}
			release()
		}()
	}
	close(start)
	for i := 0; i < 64; i++ {
		<-done
	}
	if got := s.InFlight(); got != 0 {
		t.Errorf("InFlight = %d after all releases, want 0", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/admit/ -count=1`
Expected: FAIL — the package does not exist yet (`no Go files in .../internal/admit`), then `undefined: New`.

- [ ] **Step 3: Implement the semaphore**

Create `internal/admit/admit.go`:

```go
// Package admit bounds how many requests may be in a section of code at once.
//
// This is admission control, and it is a different control from rate limiting
// and from the connection-pool bulkhead. A rate limiter bounds ONE client. The
// bulkhead (db.OpenPair) stops two workloads from consuming each other's
// capacity. Neither bounds THE CROWD: N well-behaved clients, each within its
// own limit, can still collectively queue far more concurrent database work than
// the pool can serve, and the excess waits inside pgxpool.Acquire holding a
// goroutine and a socket until the write timeout fires. That is queue collapse,
// and every per-client control correctly reports a healthy system while it
// happens.
//
// The refusal is therefore immediate and never queued. Failing a fraction of
// requests in microseconds keeps the site working; queueing all of them for 30
// seconds is an outage.
package admit

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Semaphore is a counted, non-blocking gate. Safe for concurrent use.
type Semaphore struct {
	slots    chan struct{}
	inFlight atomic.Int64
}

// New returns a semaphore admitting size concurrent holders.
//
// size must be positive: a zero-sized semaphore refuses every request, which is
// a total outage dressed as a capacity setting.
func New(size int) (*Semaphore, error) {
	if size < 1 {
		return nil, fmt.Errorf("admit: size must be at least 1, got %d", size)
	}
	return &Semaphore{slots: make(chan struct{}, size)}, nil
}

// TryAcquire takes a slot if one is free. ok reports whether it did; release
// must be called exactly when ok is true, and is safe to call more than once.
//
// The returned closure is idempotent because handlers have early returns, and a
// double release would credit a slot that was never held — letting the
// semaphore admit more than size holders. A cap that silently stops capping is
// worse than no cap, because nothing looks wrong.
func (s *Semaphore) TryAcquire() (release func(), ok bool) {
	select {
	case s.slots <- struct{}{}:
		s.inFlight.Add(1)
		var once sync.Once
		return func() {
			once.Do(func() {
				s.inFlight.Add(-1)
				<-s.slots
			})
		}, true
	default:
		return nil, false
	}
}

// InFlight reports the current holder count, for metrics and tests.
func (s *Semaphore) InFlight() int { return int(s.inFlight.Load()) }
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/admit/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Mutation-prove the semaphore**

1. Replace the `select` with an unconditional `s.slots <- struct{}{}` … no: that blocks and would hang the suite. Instead, change `make(chan struct{}, size)` to `make(chan struct{}, size+1)`. Expected: `TestTryAcquireRefusesWhenFull` fails with `third acquire succeeded; the size of 2 is not being enforced`.
2. Remove the `sync.Once` (call the body directly). Expected: `TestReleaseIsIdempotent` fails — the third release either panics on the counter going negative or the follow-up acquire finds a second slot, printing `the repeated releases over-credited the semaphore`.

Revert via `cp` and confirm `git diff`.

- [ ] **Step 6: Commit the package**

```bash
git add internal/admit/
git commit -m "feat(admit): add a non-blocking counted semaphore for admission control"
```

- [ ] **Step 7: Write the failing handler test**

Append to `internal/api/series_test.go`:

```go
// TestSeriesRefusesWhenAdmissionIsFull. The status is 503 with Retry-After, not
// 429: the client did nothing wrong and its own limit is not the thing that was
// exceeded. Telling it "too many requests" would be a lie, and a client that
// backs off per-client when the server is globally saturated backs off wrongly.
func TestSeriesRefusesWhenAdmissionIsFull(t *testing.T) {
	sem, err := admit.New(1)
	if err != nil {
		t.Fatalf("admit.New: %v", err)
	}
	// Occupy the only slot for the duration of the request. Deterministic: no
	// sleep and no race, the handler either finds a slot or it does not.
	release, ok := sem.TryAcquire()
	if !ok {
		t.Fatal("could not occupy the semaphore")
	}
	defer release()

	stub := &stubSource{}
	srv := newTestRouterWithAdmission(t, stub, snapshotWithAreaSeries(t, "sofia"), sem)

	before := AdmissionRejectedCountForTesting("area_series")
	rec := httptest.NewRecorder()
	// A period the snapshot does not precompute, so the request really wants
	// the database.
	srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=7d"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want \"2\"", got)
	}
	if stub.areaSeriesCalls != 0 {
		t.Errorf("AreaSeries called %d times, want 0 — a refused request must cost no database work", stub.areaSeriesCalls)
	}
	if got := AdmissionRejectedCountForTesting("area_series") - before; got != 1 {
		t.Errorf("admission refusals = %d, want 1", got)
	}
}

// TestSeriesReleasesItsSlot. Without this the cap is a one-way ratchet: the
// service would work for exactly `size` requests and refuse everything after,
// which is a failure mode that only appears in production and looks like a
// database outage.
func TestSeriesReleasesItsSlot(t *testing.T) {
	sem, err := admit.New(1)
	if err != nil {
		t.Fatalf("admit.New: %v", err)
	}
	stub := &stubSource{}
	srv := newTestRouterWithAdmission(t, stub, snapshotWithAreaSeries(t, "sofia"), sem)

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=7d"))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 — the slot was not released", i, rec.Code)
		}
	}
	if got := sem.InFlight(); got != 0 {
		t.Errorf("InFlight = %d after three completed requests, want 0", got)
	}
}

// TestSnapshotSeriesDoesNotConsumeAdmission. The snapshot path issues no query,
// so it must not compete for a slot sized against the database.
func TestSnapshotSeriesDoesNotConsumeAdmission(t *testing.T) {
	sem, err := admit.New(1)
	if err != nil {
		t.Fatalf("admit.New: %v", err)
	}
	release, ok := sem.TryAcquire()
	if !ok {
		t.Fatal("could not occupy the semaphore")
	}
	defer release()

	srv := newTestRouterWithAdmission(t, &stubSource{}, snapshotWithAreaSeries(t, "sofia"), sem)

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, newSeriesRequest("/api/v1/area/sofia/series?metric=P2&period=24h"))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — a memory-served response was refused by a database cap", rec.Code)
	}
}
```

Add the equivalent for `/api/v1/locate` in `internal/api/locate_test.go`, asserting 503 + `Retry-After: 2` and that `AreaAtPoint` was not called, with route label `"locate"`.

- [ ] **Step 8: Run to verify it fails**

Run: `go test ./internal/api/ -run 'Admission' -count=1`
Expected: FAIL — `undefined: newTestRouterWithAdmission`, `undefined: AdmissionRejectedCountForTesting`.

- [ ] **Step 9: Add the metric, the dep and the helper**

In `internal/api/series.go`, beside `seriesRateLimited`:

```go
// admissionRejected counts requests shed by the admission semaphore.
//
// Separate from the two rate-limit counters because it answers a different
// operational question. A rate-limit refusal says "this client is asking for too
// much"; an admission refusal says "the service is at its database capacity
// regardless of who is asking". Under load an operator needs to tell those
// apart: the first is somebody misbehaving, the second is a sizing decision that
// has been reached.
//
// The label is the route, chosen from a fixed set of literals in the handlers.
// No request input reaches it, so cardinality is bounded by the code — the same
// rule as enumerationTrips and seriesRateLimited.
var admissionRejected = metrics.CounterVec(
	"airbg_admission_rejected_total",
	"Requests shed by the database admission semaphore, by route.",
	"route")

// admitQuery takes an admission slot or answers 503.
//
// Called immediately before the query and released immediately after, so the
// slot covers the database round trip and nothing else. Wrapping the whole
// handler would hold a slot through JSON encoding and the response write, which
// are not the scarce resource.
//
// 503 rather than 429 on purpose: the client is within its own limit and did
// nothing wrong. Retry-After is 2 seconds — long enough for the in-flight
// queries to drain, short enough that a legitimate reader's chart appears late
// rather than never.
func (d Deps) admitQuery(w http.ResponseWriter, route string) (release func(), ok bool) {
	release, ok = d.Admission.TryAcquire()
	if ok {
		return release, true
	}
	admissionRejected.With(route).Inc()
	w.Header().Set("Retry-After", "2")
	writeError(w, http.StatusServiceUnavailable, "unavailable",
		"The service is busy. Please try again shortly.")
	return nil, false
}

// AdmissionRejectedCountForTesting reads the shed counter for one route so a
// test can assert in DELTA. The counter is process-global, so an absolute count
// would depend on which other tests had already run.
func AdmissionRejectedCountForTesting(route string) int64 {
	return admissionRejected.With(route).Value()
}
```

In `internal/api/router.go`, add to `Deps`:

```go
	// Admission bounds how many requests may be inside a database query at
	// once, across all clients. SeriesLimiter bounds one client; this bounds the
	// crowd. NewRouter substitutes a default when nil, so a handler is never
	// admitted without a cap.
	Admission *admit.Semaphore
```

and in `NewRouter`, beside the `SeriesLimiter` fallback:

```go
	// Fail closed, exactly as with SeriesLimiter: a nil semaphore would leave
	// the database paths uncapped, which is the hole this closes. The default is
	// sized to the API pool's default (config.defaultDBAPIConns) doubled, so a
	// router built without explicit configuration behaves like the deployed one.
	if d.Admission == nil {
		d.Admission = defaultAdmission()
	}
```

with, beside `defaultSeriesLimiter`:

```go
// defaultAdmission is the substitute NewRouter uses when Deps carries none.
// Built once per process, like defaultSeriesLimiter and for the same reason: a
// fresh one per NewRouter call would give each router its own cap, so an
// embedder holding several would collectively exceed the number.
//
// The error from admit.New is impossible here — the literal is positive — and is
// discarded rather than plumbed into a signature that has no way to report it.
var defaultAdmission = sync.OnceValue(func() *admit.Semaphore {
	s, _ := admit.New(defaultMaxDBInflight)
	return s
})

// defaultMaxDBInflight matches config's default: the API pool's 8 connections,
// doubled, so a little queueing inside pgxpool is allowed but a pile-up is not.
const defaultMaxDBInflight = 16
```

- [ ] **Step 10: Wire it into the three query sites**

In `handleAreaSeries` and `handleSensorSeries`, replace the bare query with:

```go
	release, ok := d.admitQuery(w, "area_series") // "sensor_series" in the sensor handler
	if !ok {
		return
	}
	points, err := d.Store.AreaSeries(r.Context(), slug, metric, since, hourly)
	release()
	if err != nil {
```

Release before the error branch, not in a `defer`: the slot must be given back the instant the query returns, and a `defer` would hold it through `writeSeries`'s marshal and write. Do the same in `handleLocate` around `d.Store.AreaAtPoint`, with route `"locate"`.

- [ ] **Step 11: Config, server and main**

`internal/config/config.go` — add beside the pool-size constants:

```go
// defaultMaxDBInflight is defaultDBAPIConns doubled: enough that a brief burst
// queues inside pgxpool rather than being shed, and low enough that a sustained
// one is shed in microseconds instead of piling up until the write timeout.
const defaultMaxDBInflight int32 = 16
```

Add `MaxDBInflight int32` to `Config` with a doc comment pointing at `admit`, and in `Load()`, beside the pool-size reads:

```go
	maxInflight, err := envPositiveInt32("AIRBG_MAX_DB_INFLIGHT", defaultMaxDBInflight)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxDBInflight = maxInflight
```

Add `AIRBG_MAX_DB_INFLIGHT` to `clearEnv` in `config_test.go`, plus a default test, an override test, and a rejection row for `"0"` — following the shape of `TestPoolSizeDefaults` / `TestRejectsNonPositivePoolSizes` exactly.

`internal/server/server.go` — add `MaxDBInflight int32` to `Options`, build the semaphore in `New` (returning the error, since `New` already returns one), and pass it into `api.Deps`. `cmd/airbg/main.go` — pass `MaxDBInflight: cfg.MaxDBInflight` in the `server.Options` literal inside `runServe`.

- [ ] **Step 12: Document the variable**

`README.md`: add `| AIRBG_MAX_DB_INFLIGHT | no | 16 |` to the first table and a row to the serving table explaining that it bounds concurrent database work across all clients and that refusals are 503, not 429. Update the "Six environment variables configure serving" count to seven. `.env.example`: add the variable with a comment covering why refusal beats queueing.

- [ ] **Step 13: Run everything**

Run: `go test ./... -race -count=1` then `go test -tags=integration ./internal/server/ -count=1`
Expected: PASS.

- [ ] **Step 14: Mutation-prove the wiring**

1. Make `admitQuery` always return `func() {}, true`. Expected: `TestSeriesRefusesWhenAdmissionIsFull` fails with `status = 200, want 503`.
2. Change `release()` to a `defer release()` placed *after* `writeSeries` — this one is expected to be **inert**, because the test's requests are sequential. Say so rather than claiming a proof: the property "the slot is released before the response is written" is not covered by this suite, and the honest statement is that only "the slot is released at all" is. `TestSeriesReleasesItsSlot` covers that; deleting the `release()` call entirely must fail it with `request 1: status = 503`.
3. Delete the `admissionRejected.With(route).Inc()` line. Expected: `admission refusals = 0, want 1`.

- [ ] **Step 15: Commit**

```bash
git add internal/api/ internal/config/ internal/server/server.go cmd/airbg/main.go README.md .env.example
git commit -m "feat(api): shed load with a non-blocking admission cap on the database routes"
```

---

### Task 3: A short scoped statement timeout on the fall-through series queries

**Why:** the admission semaphore sheds load, but while Postgres is unreachable or pathologically slow, each of the 16 in-flight slots is held for the full pool statement timeout of 15 s before failing. That turns a database problem into 16 slots of dead weight and a wall of 503s for everyone else. A spec-level alternative was a circuit breaker; this achieves the same effect with no breaker state to get wrong (spec §13.5).

**Files:**
- Modify: `internal/db/timeout.go` — add `SeriesStatementTimeout`
- Modify: `internal/store/aggregate.go` — wrap `AreaSeries` and `SensorSeries` in a transaction with the scoped timeout
- Test: `internal/store/aggregate_test.go` (append)

**Interfaces:**
- Consumes: `db.SetLocalStatementTimeout(ctx context.Context, tx pgx.Tx, value string) error` — already exists, used by `area.AssignSensors` and `area.PurgeOutsideBoundary`. It uses `set_config('statement_timeout', $1, true)` rather than `SET LOCAL` because `SET` does not accept bind parameters.
- Produces: `db.SeriesStatementTimeout = "5s"`.

Note the import direction: `internal/store` does not currently import `internal/db`. It may — `db` imports only `db/migrations`, so there is no cycle.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/aggregate_test.go`:

```go
// TestSeriesQueriesUseTheShortStatementTimeout pins the scoped bound. The pool
// default is 15s, which is right for an ordinary read but far too long to hold
// one of 16 admission slots while Postgres is unwell: sixteen slots x fifteen
// seconds is a wall of 503s for every other reader.
//
// pg_sleep is the only honest way to assert a timeout — the query must actually
// exceed it. 6s sleeps against a 5s bound, so the margin is a full second in
// each direction.
func TestSeriesQueriesUseTheShortStatementTimeout(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer tx.Rollback(ctx)

	if err := db.SetLocalStatementTimeout(ctx, tx, db.SeriesStatementTimeout); err != nil {
		t.Fatalf("SetLocalStatementTimeout: %v", err)
	}

	start := time.Now()
	_, err = tx.Exec(ctx, `SELECT pg_sleep(6)`)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a 6s query completed under the series statement timeout")
	}
	// The bound must be the database's, not the test's patience.
	if elapsed > 10*time.Second {
		t.Errorf("took %v; the statement timeout was not applied", elapsed)
	}
	if db.SeriesStatementTimeout != "5s" {
		t.Errorf("SeriesStatementTimeout = %q; this test's 6s sleep assumes 5s", db.SeriesStatementTimeout)
	}
}

// TestAreaSeriesStillReturnsDataInsideItsTransaction. Wrapping a read in a
// transaction is exactly the kind of change that can return an empty result set
// while every timeout test still passes — a chart that is blank rather than
// wrong, which is harder to notice in review than a failure.
func TestAreaSeriesStillReturnsDataInsideItsTransaction(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	s := New(pool)

	slug, at := seedTwoSensorsOneInstant(t, ctx, pool, 10, 20)

	points, err := s.AreaSeries(ctx, slug, "P2", at.Add(-time.Hour), false)
	if err != nil {
		t.Fatalf("AreaSeries: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("got %d points, want 1", len(points))
	}
	if points[0].Value != 15 {
		t.Errorf("value = %v, want 15", points[0].Value)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/store/ -run 'StatementTimeout|InsideItsTransaction' -count=1`
Expected: FAIL, `undefined: db.SeriesStatementTimeout`.

- [ ] **Step 3: Add the constant**

In `internal/db/timeout.go`, beside the existing two:

```go
	// SeriesStatementTimeout bounds the two database-backed series queries.
	//
	// Shorter than the pool default of 15s, and deliberately so. These queries
	// run while holding one of a small number of admission slots
	// (internal/admit), so the time each one may hold a slot while Postgres is
	// unwell is the thing being bounded — not the query's own reasonable
	// duration. A healthy 1-year rollup query returns in well under a second;
	// anything approaching five is a symptom, and failing fast frees the slot
	// for a request that can be served.
	//
	// This is the alternative to a circuit breaker (spec §13.5): the same
	// fail-fast effect, with no breaker state to get wrong.
	SeriesStatementTimeout = "5s"
```

- [ ] **Step 4: Scope the timeout in the two queries**

In `internal/store/aggregate.go`, replace the body of `AreaSeries` so the query runs inside a transaction:

```go
func (s *Store) AreaSeries(ctx context.Context, slug, metric string, since time.Time, hourly bool) ([]Point, error) {
	// A transaction only so statement_timeout can be scoped: set_config's local
	// flag is transaction-scoped, and this read must not inherit the pool-wide
	// 15s. Rolled back rather than committed — nothing is written, and a rollback
	// of a read-only transaction is the cheaper of the two.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: begin area series: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := db.SetLocalStatementTimeout(ctx, tx, db.SeriesStatementTimeout); err != nil {
		return nil, fmt.Errorf("store: area series timeout: %w", err)
	}

	var rows pgx.Rows
	if hourly {
		rows, err = tx.Query(ctx, areaHourlySeriesSQL, slug, metric, since)
	} else {
		rows, err = tx.Query(ctx, areaRawSeriesSQL, slug, metric, since, usableQuality)
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

Apply the same change to `SensorSeries`. Leave `AllAreaSeries` on the pool default: it runs on the collector pool from `snapshot.Build`, holds no admission slot, and a snapshot build that fails is already handled (last-known-good stays published).

- [ ] **Step 5: Run the store tests**

Run: `go test ./internal/store/ -count=1`
Expected: PASS. The `pg_sleep` test takes ~5 s; that is the cost of asserting a real timeout.

- [ ] **Step 6: Mutation-prove it**

1. Delete the `SetLocalStatementTimeout` call from `AreaSeries`, and add a temporary test that runs `AreaSeries` against a query rewritten to `SELECT pg_sleep(20), 0` — if that is too invasive, state plainly that the production call site's timeout is covered only by the constant's own test and that the mutation is inert here. Do not manufacture a proof.
2. Change `SeriesStatementTimeout` to `"30s"`. Expected: `TestSeriesQueriesUseTheShortStatementTimeout` fails with `a 6s query completed under the series statement timeout`.
3. Change `tx.Query` back to `s.pool.Query` in `AreaSeries`. Expected: inert for correctness (the data still returns) — and that is exactly why mutation 2 exists. Report it as inert.

- [ ] **Step 7: Commit**

```bash
git add internal/db/timeout.go internal/store/aggregate.go internal/store/aggregate_test.go
git commit -m "fix(store): bound the series fall-through queries with a 5s scoped timeout"
```

---

### Task 4: A connection-limiting listener on the public server

**Why:** Phase 2's timeouts bound how long a single request may take. Nothing bounds how many sockets the host may hold open, so file-descriptor exhaustion is unaddressed: 50 000 mostly-idle connections that each dribble a header byte every few seconds cost an attacker almost nothing and defeat every control currently in place, because no request ever completes to be counted. This overlaps with Cloudflare's protection, and the overlap is the point — the origin being reachable only through Cloudflare is an unverified assumption today (spec §11), and a control that only works when an unverified assumption holds is not a control.

**Files:**
- Create: `internal/httpx/limitlistener.go`, `internal/httpx/limitlistener_test.go`
- Modify: `internal/server/server.go` — `Options.MaxConns`, public listener construction
- Modify: `internal/config/config.go`, `internal/config/config_test.go` — `AIRBG_MAX_CONNS`
- Modify: `cmd/airbg/main.go`, `README.md`, `.env.example`

**Interfaces:**
- Produces:
  - `func httpx.LimitListener(ln net.Listener, max int) net.Listener`
  - metric `airbg_connections_rejected_total`
  - `config.Config.MaxConns int32`, env `AIRBG_MAX_CONNS`, default `4096`
  - `server.Options.MaxConns int32`

Hand-rolled rather than `golang.org/x/net/netutil` — the no-new-Go-dependency rule holds, and the whole thing is ~40 lines.

- [ ] **Step 1: Write the failing listener test**

Create `internal/httpx/limitlistener_test.go`:

```go
package httpx

import (
	"io"
	"net"
	"testing"
	"time"
)

// TestLimitListenerClosesConnectionsOverTheCap.
//
// Over-cap connections are CLOSED, not queued. Queuing them is what the kernel
// backlog already does; the point of this listener is that a connection past the
// cap costs the process no file descriptor and no goroutine. The client sees an
// immediate EOF, which is the honest signal.
func TestLimitListenerClosesConnectionsOverTheCap(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln := LimitListener(base, 1)
	t.Cleanup(func() { ln.Close() })

	// Accept in the background: Accept must be called for the limiter to see
	// connections at all.
	accepted := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()

	first, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	t.Cleanup(func() { first.Close() })
	held := <-accepted // the one permitted connection, still open

	second, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	t.Cleanup(func() { second.Close() })

	// The over-cap connection must be closed by the server without ever being
	// handed to Accept.
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := second.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read on the over-cap connection = %v, want io.EOF — it was not closed", err)
	}

	// And releasing the first must free the slot.
	held.Close()
	third, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 3: %v", err)
	}
	t.Cleanup(func() { third.Close() })
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Error("a connection after the first was closed was not accepted; the slot was not released")
	}
}

// TestLimitListenerCountsRejections. A cap that sheds silently is a cap nobody
// can size: an operator needs to see this number climbing before they hear about
// it from users.
func TestLimitListenerCountsRejections(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln := LimitListener(base, 1)
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			if _, err := ln.Accept(); err != nil {
				return
			}
		}
	}()

	before := ConnectionsRejectedCountForTesting()

	first, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	t.Cleanup(func() { first.Close() })
	second, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	t.Cleanup(func() { second.Close() })
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = second.Read(make([]byte, 1))

	// Delta, not absolute: the counter is process-global.
	if got := ConnectionsRejectedCountForTesting() - before; got < 1 {
		t.Errorf("rejections = %d, want at least 1", got)
	}
}

// TestLimitListenerWithNonPositiveMaxIsUnlimited. A zero cap must not mean "no
// connections": a mis-set variable that silently refuses every visitor is a
// worse outcome than one that silently declines to limit, and config already
// rejects a non-positive value before this is reached.
func TestLimitListenerWithNonPositiveMaxIsUnlimited(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if got := LimitListener(base, 0); got != base {
		t.Error("LimitListener(ln, 0) wrapped the listener; a non-positive cap must be a no-op")
	}
	base.Close()
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/httpx/ -run LimitListener -count=1`
Expected: FAIL, `undefined: LimitListener`.

- [ ] **Step 3: Implement the listener**

Create `internal/httpx/limitlistener.go`:

```go
package httpx

import (
	"net"
	"sync"

	"airbg.org/internal/metrics"
)

// connectionsRejected counts connections closed for exceeding the cap.
//
// Unlabelled: there is exactly one public listener, and the peer address would
// be unbounded cardinality handed straight to an attacker.
var connectionsRejected = metrics.Counter(
	"airbg_connections_rejected_total",
	"Connections closed immediately because the concurrent-connection cap was reached.")

// LimitListener bounds how many connections may be open at once.
//
// The gap this closes: the server's timeouts bound how long one request may
// take, and nothing bounds how many sockets the process may hold. Tens of
// thousands of mostly-idle connections, each dribbling one header byte every few
// seconds, exhaust file descriptors while completing no request — so no rate
// limiter, breadth counter or admission cap ever sees them.
//
// An over-cap connection is accepted from the kernel and closed immediately,
// rather than left in the backlog. Leaving it queued would make the process look
// healthy while clients hung; closing it is the honest signal, and the
// descriptor is released in the same breath.
//
// A non-positive max returns ln unchanged. A cap that is accidentally zero must
// degrade to "no limiting", never to "no service".
//
// Hand-rolled rather than golang.org/x/net/netutil: the project takes no new Go
// dependency, and this is the whole of what netutil.LimitListener does.
func LimitListener(ln net.Listener, max int) net.Listener {
	if max < 1 {
		return ln
	}
	return &limitListener{Listener: ln, slots: make(chan struct{}, max)}
}

type limitListener struct {
	net.Listener
	slots chan struct{}
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &limitConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			// Counted before the close, so a shed connection is never invisible
			// to metrics even if the close itself errors.
			connectionsRejected.Inc()
			c.Close()
			// Keep accepting: returning an error here would take down the
			// server's Accept loop, turning a shed connection into an outage.
		}
	}
}

// limitConn returns its slot when closed. net/http closes every connection it
// finishes with, including on timeout and on panic recovery, so Close is the
// correct hook — there is no path where the server keeps a connection it has
// stopped serving.
type limitConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	// Once, because a double Close must not credit a slot that was already
	// returned: over-crediting would let the listener exceed its cap, and a cap
	// that silently stops capping is worse than none.
	c.once.Do(c.release)
	return err
}

// ConnectionsRejectedCountForTesting reads the shed counter so a test can assert
// in DELTA. Process-global, so an absolute count would depend on test order.
func ConnectionsRejectedCountForTesting() int64 { return connectionsRejected.Value() }
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/httpx/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Wire it into the public server only**

`internal/server/server.go`: add `MaxConns int32` to `Options`, store it on `Server`, and split the public listener out of `listen`:

```go
// servePublic listens and serves the public server under the connection cap.
//
// Separate from listen() because only the public listener is capped: the private
// listener carries /metrics and /healthz on loopback, and capping it would mean a
// connection flood could also blind the operator to the flood.
func (s *Server) servePublic() error {
	ln, err := net.Listen("tcp", s.public.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.public.Addr, err)
	}
	if err := s.public.Serve(httpx.LimitListener(ln, int(s.maxConns))); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving %s: %w", s.public.Addr, err)
	}
	return nil
}
```

and in `Run`, replace `go func() { errCh <- listen(s.public) }()` with `go func() { errCh <- s.servePublic() }()`. Leave the private listener on `listen`.

`internal/config/config.go`: `defaultMaxConns int32 = 4096`, `Config.MaxConns`, and an `envPositiveInt32("AIRBG_MAX_CONNS", defaultMaxConns)` read in `Load()`. Add the variable to `clearEnv` and add default/override/rejection tests, following `TestPoolSizeDefaults`'s shape. `cmd/airbg/main.go`: pass `MaxConns: cfg.MaxConns` in `server.Options`.

- [ ] **Step 6: Document and run everything**

`README.md` (both tables, and bump the serving-variable count) and `.env.example`, explaining that this bounds sockets rather than requests and that it deliberately overlaps with Cloudflare because the origin-lock is unverified.

Run: `go test ./... -race -count=1` then `go test -tags=integration ./internal/server/ -count=1`
Expected: PASS. The e2e suite exercises the real listener, so a mistake in `servePublic` shows up there.

- [ ] **Step 7: Mutation-prove it**

1. Change the `select` to an unconditional `l.slots <- struct{}{}`… do not: it blocks. Instead change `make(chan struct{}, max)` to `make(chan struct{}, max+1)`. Expected: `TestLimitListenerClosesConnectionsOverTheCap` fails with `read on the over-cap connection = <nil>, want io.EOF`.
2. Remove the `sync.Once` from `limitConn.Close`. Expected: this may be inert, since the test closes each connection once. If it is, say so and note that the guard is there for `net/http`'s own double-close paths, verified by reading rather than by this test.
3. Change `if max < 1 { return ln }` to `if max < 0`. Expected: `TestLimitListenerWithNonPositiveMaxIsUnlimited` fails with `wrapped the listener; a non-positive cap must be a no-op`.

- [ ] **Step 8: Commit**

```bash
git add internal/httpx/limitlistener.go internal/httpx/limitlistener_test.go internal/server/server.go internal/config/ cmd/airbg/main.go README.md .env.example
git commit -m "feat(httpx): cap concurrent connections on the public listener"
```

---

### Task 5: `Permissions-Policy` and `Cross-Origin-Resource-Policy`

**Why:** two headers that cost nothing and close two specific holes. `Permissions-Policy` is what stops a compromised frontend bundle from silently reaching for device capabilities — the exact failure mode of an npm supply-chain compromise, which Phase 3a is about to introduce. `Cross-Origin-Resource-Policy` stops the JSON payloads being pulled into a third-party document context, and it is the header that does real work in the space CORS is usually mistaken for.

**Files:**
- Modify: `internal/httpx/headers.go`
- Modify: `internal/httpx/headers_test.go` (append; create if absent)

**Interfaces:** produces `httpx.PermissionsPolicyValue`. No signature changes.

- [ ] **Step 1: Write the failing test**

```go
// TestSecurityHeadersDenyDeviceCapabilities. Phase 3a embeds a bundle built from
// hundreds of transitive npm packages and serves it same-origin under a CSP that
// trusts 'self'. A malicious package does not need to escape a sandbox — it only
// needs to be in the bundle. This header is what keeps that bundle from reaching
// for the camera, the microphone or the user's location.
func TestSecurityHeadersDenyDeviceCapabilities(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("Permissions-Policy")
	if got == "" {
		t.Fatal("no Permissions-Policy header")
	}
	for _, feature := range []string{"geolocation=()", "camera=()", "microphone=()", "payment=()", "usb=()"} {
		if !strings.Contains(got, feature) {
			t.Errorf("Permissions-Policy = %q, missing %s", got, feature)
		}
	}
}

// TestSecurityHeadersSetCORP. The API responses are per-entity and enumerable.
// same-origin keeps them out of a third-party document context — which is the
// job people mistakenly expect CORS to do. There are deliberately no
// Access-Control-* headers anywhere in this project: their absence stops
// other-origin browser JS from READING a response and stops nothing else, so it
// is not an anti-extraction control and must not be presented as one.
func TestSecurityHeadersSetCORP(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("Cross-Origin-Resource-Policy = %q, want \"same-origin\"", got)
	}
}

// TestNoCORSHeaders pins the deliberate absence. An undocumented missing header
// looks like an oversight, and the plausible "fix" — a permissive ACAO — would
// be a straight downgrade with no compensating benefit.
func TestNoCORSHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods"} {
		if got := rec.Header().Get(h); got != "" {
			t.Errorf("%s = %q, want absent", h, got)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/httpx/ -run 'DeviceCapabilities|CORP|NoCORS' -count=1`
Expected: FAIL, `no Permissions-Policy header`.

- [ ] **Step 3: Add the headers**

In `internal/httpx/headers.go`:

```go
// PermissionsPolicyValue denies every browser capability the site does not use.
//
// The threat this addresses is the frontend bundle itself: it is built from
// hundreds of transitive npm packages and served same-origin under a CSP that
// trusts 'self', so a malicious package does not need to escape a sandbox — it
// only needs to be in the bundle. A denial here is enforced by the browser
// regardless of what the bundle asks for.
//
// geolocation is denied even though Phase 3b's "sensors near me" button will
// need it. Opening it then is a reviewed one-line change; having it open before
// anything uses it is an allowance nobody chose.
const PermissionsPolicyValue = "geolocation=(), camera=(), microphone=(), payment=(), usb=()"
```

and inside `SecurityHeaders`, beside the existing sets:

```go
			h.Set("Permissions-Policy", PermissionsPolicyValue)
			// same-origin, so the per-entity JSON payloads cannot be pulled into
			// a third-party document context. This is the header that does the
			// job CORS is commonly mistaken for; see the deliberate absence of
			// every Access-Control-* header, pinned by TestNoCORSHeaders.
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/httpx/ -count=1` then `go test ./... -count=1`
Expected: PASS. If an e2e test asserts an exact header set, extend it rather than loosening it.

- [ ] **Step 5: Mutation-prove**

1. Delete the `Permissions-Policy` set. Expected: `no Permissions-Policy header`.
2. Change CORP to `cross-origin`. Expected: `Cross-Origin-Resource-Policy = "cross-origin", want "same-origin"`.
3. Add `h.Set("Access-Control-Allow-Origin", "*")`. Expected: `TestNoCORSHeaders` fails — proving the absence is actually guarded and not merely described in a comment.

- [ ] **Step 6: Commit**

```bash
git add internal/httpx/headers.go internal/httpx/headers_test.go
git commit -m "feat(httpx): deny unused device capabilities and set CORP"
```

---

## Self-review

**Spec coverage.** §7.1 → Task 1. §12.2 → Tasks 1 and 2. §12.3 → Task 2 (`AIRBG_MAX_DB_INFLIGHT`, non-blocking, 503 + `Retry-After: 2`, `airbg_admission_rejected_total`). §12.3a → already landed in `258c4dc`, stated in the scope boundary. §13.2's `Permissions-Policy` and CORP → Task 5; §13.2's `CSP(basemapHost)` → deferred to the 3a plan with a reason. §13.3 → Task 4 (`AIRBG_MAX_CONNS`, default 4096, hand-rolled). §13.5's "scoped timeout instead of a Postgres circuit breaker" → Task 3; its CORS and CSRF decisions → pinned by `TestNoCORSHeaders` and by the fact that every route is a GET. §13.1's npm controls → deferred to the 3a plan with a reason. §12.4's measurement table → Phase 4, unchanged by this plan.

**Two spec items this plan deliberately does not do.** `pool_max_conns` in the deployed `AIRBG_DATABASE_URL` (§12.3, last paragraph) is obsolete: `db.OpenPair` sets `MaxConns` explicitly per pool and overrides the URL, which `TestOpenPairOverridesPoolMaxConnsInTheURL` pins. And §12.3's "size defaults to `pool_max_conns × 2`" is implemented as the literal 16 (`defaultDBAPIConns` 8, doubled) rather than as a computed expression, because config's two values are independent env vars and deriving one from the other at load time would make a deployment that raises only the pool size silently also raise the cap.

**Type consistency.** `snapshot.SeriesPayload` replaces `api.seriesBody` in Task 1 and every later reference uses the new name. `admit.New` returns `(*Semaphore, error)` at every call site, including `defaultAdmission`, which discards the error with a stated reason. `config.envPositiveInt32` returns `int32`, so `Config.MaxDBInflight` and `Config.MaxConns` are `int32` and are converted at the two use sites that want `int` (`admit.New`, `httpx.LimitListener`). `db.SetLocalStatementTimeout(ctx, tx, value)` is used with the signature it already has.

**Honesty about inert mutations.** Three steps (Task 2 step 14.2, Task 3 step 6.1 and 6.3, Task 4 step 7.2) predict that a mutation may come out inert and instruct the implementer to report it as such rather than manufacture a proof. That is deliberate, per the project rule.
