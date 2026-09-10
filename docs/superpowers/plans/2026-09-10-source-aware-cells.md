# Source-Aware Cells Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the two network toggles ("Citizen sensors", "Official stations") act on the hex grid at every zoom, and show per-metric official-station coverage in the layer menu.

**Architecture:** Each aggregate hex cell in the snapshot gains per-network counts and medians (`by_source`), or a single `source` when only one network feeds it. The browser picks which numbers to draw from the body it already holds, so a toggle stays a repaint with no request and no new query parameter. A per-build `coverage` block on the same payload replaces the hardcoded `MEASURED` table and feeds the layer-menu counts.

**Tech Stack:** Go 1.22+ (`internal/snapshot`, `internal/i18n`, `internal/web`), plain-JS ES modules under `web/src` (Vite + Vitest), Playwright for e2e.

**Spec:** `docs/superpowers/specs/2026-09-10-source-aware-cells-design.md`

## Global Constraints

- No new third-party dependency. Permitted direct Go deps stay `pgx/v5`, `goose/v3`, `testcontainers-go`, and the Parquet reader already granted.
- No new query parameter on `/api/v1/hexes`. A network toggle must never issue a request.
- All SQL through pgx parameterised queries. This plan adds no SQL at all.
- No bulk comments. A comment states what the code does plus the concrete constraint that forced it. No narrative, no aphorisms, no restating the diff.
- Commit with explicit paths: `git commit -F <file> -- <paths>`. Never `git add .`. New files must be `git add`ed first. Never stage `CLAUDE.md`.
- Commit messages via file (`git commit -F`), never `-m`: backticks in a `-m` string execute.
- No `Co-Authored-By` trailer, no "Generated with" line.
- `www-root/` is the dead legacy app and is never modified.
- Never use `any` in JS-facing code without permission; here it means no `interface{}`/`any` in the Go additions either — the new types are concrete.
- `internal/i18n/en.json` and `internal/i18n/bg.json` must carry identical key sets (`TestCataloguesHaveIdenticalKeys` enforces it).
- `npm run build` deletes `internal/web/dist/.keep`; restore it before committing if you run a build.
- Metric names: `P1` = PM10, `P2` = PM2.5. Network ids are the two strings `"sensor.community"` and `"eea"`.
- Timelapse is out of scope. `/api/v1/timelapse` and `internal/store/frames.go` are not touched.

---

## File Structure

**Go — `internal/snapshot`**

- `hexes.go` — `hexEntry` gains `Source` and `BySource`; `hexPayload` gains `Coverage`; `hexBin` gains per-source accumulation; new `sourceEntry`, `sourceBin`, `coverageFrom`, `sourceOf`. `HexBody`'s clip path and `PointBody` copy `Coverage` through.
- `build.go` — computes coverage once per cycle and assigns it to every tier payload.
- `snapshot.go` — `Snapshot` gains an unexported `coverage` field so `PointBody` can serve the same block.
- `hexes_test.go`, `points_test.go` — new tests for the above.

**Web — `web/src/lib`**

- `sourcefilter.svelte.js` — `sourceOf` becomes exported and accepts both a hex entry and a GeoJSON feature; `MEASURED` and `measuredBy` are deleted (Task 5, with their consumer).
- `hexes.js` — `hexFeatures` gains an `enabled` argument and picks the per-network numbers.
- `__tests__/hexes.test.js`, `__tests__/sourcefilter.test.js` — tests.

**Web — `web/src/islands`**

- `map.js` — threads `getSources()` into `hexFeatures`, repaints the grid on a source change, holds `state.coverage`, rewrites `setSourceViewAvailability`, drops `state.onSensorTier` and `t.sensorTierOnly`.
- `__tests__/map.test.js` — tests.

**Templates and copy**

- `internal/web/templates/base.gohtml` — drops `data-t-sensor-tier-only`, adds `data-t-with-data`.
- `internal/i18n/en.json`, `internal/i18n/bg.json` — drop `map.view.sensor_tier_only`, add `map.view.with_data`.

**e2e**

- `web/e2e/sources.spec.js` — opens `/en/` and asserts the grid toggles.

---

### Task 1: Per-network numbers on aggregate cells

**Files:**
- Modify: `internal/snapshot/hexes.go` (`hexEntry` ~line 107, `hexBin` struct, `hexPayloadFrom` ~line 344, `pointsFrom` ~line 236)
- Test: `internal/snapshot/hexes_test.go`

**Interfaces:**
- Consumes: `store.SensorReading` (has `Source string`, already scanned at `internal/store/aggregate.go:295`), `median()`, `round1()`, `round4()`, `upstream.CanonicalMetrics()`.
- Produces:
  - `type sourceEntry struct { N int; Values map[string]float64 }` with JSON tags `n`, `values`.
  - `hexEntry.Source string` (`json:"source,omitempty"`), `hexEntry.BySource map[string]sourceEntry` (`json:"by_source,omitempty"`).
  - `func sourceOf(sr store.SensorReading) string` — returns `"sensor.community"` when `sr.Source` is empty.

- [ ] **Step 1: Write the failing tests**

Append to `internal/snapshot/hexes_test.go`:

```go
func sensorFrom(id int64, lon, lat float64, source string, values map[string]float64) store.SensorReading {
	sr := sensorAt(id, lon, lat, values)
	sr.Source = source
	return sr
}

// A bin holding both networks reports each network's own median beside the
// blended one. Neither per-source number is derivable from the blended median,
// which is why all three are carried.
func TestMixedBinReportsEachNetworkSeparately(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "sensor.community", map[string]float64{"P1": 10}),
		sensorFrom(2, 23.3220, 42.6978, "sensor.community", map[string]float64{"P1": 20}),
		sensorFrom(3, 23.3221, 42.6979, "sensor.community", map[string]float64{"P1": 30}),
		sensorFrom(4, 23.3222, 42.6980, "eea", map[string]float64{"P1": 100}),
	}, HexResolutionKM)

	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 hex, got %d", len(p.Hexes))
	}
	h := p.Hexes[0]
	if h.N != 4 {
		t.Errorf("n = %d, want 4", h.N)
	}
	if got := h.Values["P1"]; got != 25 {
		t.Errorf("blended P1 = %v, want 25", got)
	}
	if h.Source != "" {
		t.Errorf("source = %q, want empty on a two-network bin", h.Source)
	}
	sc, ok := h.BySource["sensor.community"]
	if !ok {
		t.Fatalf("by_source has no sensor.community: %#v", h.BySource)
	}
	if sc.N != 3 || sc.Values["P1"] != 20 {
		t.Errorf("sensor.community = {n:%d P1:%v}, want {n:3 P1:20}", sc.N, sc.Values["P1"])
	}
	eea, ok := h.BySource["eea"]
	if !ok {
		t.Fatalf("by_source has no eea: %#v", h.BySource)
	}
	if eea.N != 1 || eea.Values["P1"] != 100 {
		t.Errorf("eea = {n:%d P1:%v}, want {n:1 P1:100}", eea.N, eea.Values["P1"])
	}
}

// One network in the bin: the entry names it and omits by_source, because
// `values` already IS that network's numbers and repeating them would double
// the payload of the common case.
func TestSingleNetworkBinNamesItsSourceAndOmitsBySource(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "eea", map[string]float64{"P1": 40}),
		sensorFrom(2, 23.3220, 42.6978, "eea", map[string]float64{"P1": 60}),
	}, HexResolutionKM)

	if len(p.Hexes) != 1 {
		t.Fatalf("want 1 hex, got %d", len(p.Hexes))
	}
	h := p.Hexes[0]
	if h.Source != "eea" {
		t.Errorf("source = %q, want \"eea\"", h.Source)
	}
	if h.BySource != nil {
		t.Errorf("by_source = %#v, want nil on a one-network bin", h.BySource)
	}
	if got := h.Values["P1"]; got != 50 {
		t.Errorf("P1 = %v, want 50", got)
	}
}

// Rows written before the source column existed carry an empty Source. They are
// sensor.community, not a third network.
func TestBlankSourceCountsAsCommunity(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "", map[string]float64{"P1": 10}),
		sensorFrom(2, 23.3220, 42.6978, "eea", map[string]float64{"P1": 30}),
	}, HexResolutionKM)

	h := p.Hexes[0]
	if _, ok := h.BySource[""]; ok {
		t.Fatalf("by_source has an empty-string network: %#v", h.BySource)
	}
	sc, ok := h.BySource["sensor.community"]
	if !ok {
		t.Fatalf("by_source has no sensor.community: %#v", h.BySource)
	}
	if sc.N != 1 || sc.Values["P1"] != 10 {
		t.Errorf("sensor.community = {n:%d P1:%v}, want {n:1 P1:10}", sc.N, sc.Values["P1"])
	}
}

// A metric no sensor of a network reported is ABSENT from that network's
// values, never present as zero: 0 µg/m³ is a reading.
func TestNetworkWithoutTheMetricOmitsIt(t *testing.T) {
	p := hexPayloadFrom(time.Now(), []store.SensorReading{
		sensorFrom(1, 23.3219, 42.6977, "sensor.community", map[string]float64{"P1": 10, "humidity": 55}),
		sensorFrom(2, 23.3220, 42.6978, "eea", map[string]float64{"P1": 30}),
	}, HexResolutionKM)

	eea := p.Hexes[0].BySource["eea"]
	if _, ok := eea.Values["humidity"]; ok {
		t.Errorf("eea values carry humidity: %#v", eea.Values)
	}
	sc := p.Hexes[0].BySource["sensor.community"]
	if got := sc.Values["humidity"]; got != 55 {
		t.Errorf("sensor.community humidity = %v, want 55", got)
	}
}

// The point tier is one sensor per entry, so it names its network and never
// carries by_source.
func TestPointEntriesNameTheirNetwork(t *testing.T) {
	pts := pointsFrom([]store.SensorReading{
		sensorFrom(7, 23.3219, 42.6977, "eea", map[string]float64{"P1": 12}),
		sensorFrom(8, 23.3220, 42.6978, "", map[string]float64{"P1": 14}),
	})

	if len(pts) != 2 {
		t.Fatalf("want 2 points, got %d", len(pts))
	}
	if pts[0].Source != "eea" {
		t.Errorf("point 7 source = %q, want \"eea\"", pts[0].Source)
	}
	if pts[1].Source != "sensor.community" {
		t.Errorf("point 8 source = %q, want \"sensor.community\"", pts[1].Source)
	}
	if pts[0].BySource != nil || pts[1].BySource != nil {
		t.Error("a point entry carries by_source")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/snapshot/ -run 'TestMixedBin|TestSingleNetworkBin|TestBlankSource|TestNetworkWithout|TestPointEntriesName' -v`
Expected: FAIL — `h.Source undefined (type hexEntry has no field or method Source)`.

- [ ] **Step 3: Add the types and the source helper**

In `internal/snapshot/hexes.go`, extend `hexEntry` (the struct at ~line 107) with two fields after `Values`:

```go
	// Source names the ONE network behind this entry: every sensor on the point
	// tier, and an aggregate bin that only one network reaches. Omitted on a bin
	// fed by both, which carries BySource instead — an entry cannot be both.
	Source string `json:"source,omitempty"`
	// BySource carries each network's own count and medians, so the browser can
	// answer a network toggle from the body it already holds rather than by
	// asking for a filtered one. Omitted on a single-network entry, where Values
	// already is that network's numbers.
	BySource map[string]sourceEntry `json:"by_source,omitempty"`
```

Add beside it:

```go
type sourceEntry struct {
	N      int                `json:"n"`
	Values map[string]float64 `json:"values"`
}

// sourceOf names the network a reading came from. A row written before the
// source column existed carries an empty Source and is sensor.community; the
// browser's sourcefilter.svelte.js applies the same rule to features.
func sourceOf(sr store.SensorReading) string {
	if sr.Source == "" {
		return "sensor.community"
	}
	return sr.Source
}
```

- [ ] **Step 4: Accumulate per network in the bin**

Extend `hexBin` with:

```go
	// bySource repeats vals per network. A network's median is not derivable
	// from the blended one, so a bin fed by both has to keep both sets.
	bySource map[string]*sourceBin
```

and add:

```go
type sourceBin struct {
	n    int
	vals map[string][]float64
}
```

In `hexPayloadFrom`, the bin allocation becomes:

```go
		if b == nil {
			b = &hexBin{coord: c, vals: map[string][]float64{},
				countries: map[string]int{}, bySource: map[string]*sourceBin{}}
			bins[c] = b
		}
```

and the accumulation loop gains the per-network half:

```go
		b.n++
		if sr.Country != "" {
			b.countries[sr.Country]++
		}
		src := sourceOf(sr)
		sb := b.bySource[src]
		if sb == nil {
			sb = &sourceBin{vals: map[string][]float64{}}
			b.bySource[src] = sb
		}
		sb.n++
		for _, m := range upstream.CanonicalMetrics() {
			if v, ok := sr.Values[m]; ok {
				b.vals[m] = append(b.vals[m], v)
				sb.vals[m] = append(sb.vals[m], v)
			}
		}
```

- [ ] **Step 5: Emit Source or BySource**

In `hexPayloadFrom`'s emission loop, replace the `p.Hexes = append(...)` call with:

```go
		e := hexEntry{
			Lon:     round4(lon),
			Lat:     round4(lat),
			N:       b.n,
			Country: b.modalCountry(),
			Values:  values,
		}
		if len(b.bySource) == 1 {
			for src := range b.bySource {
				e.Source = src
			}
		} else {
			e.BySource = make(map[string]sourceEntry, len(b.bySource))
			for src, sb := range b.bySource {
				sv := make(map[string]float64, len(sb.vals))
				for m, vs := range sb.vals {
					if len(vs) > 0 {
						sv[m] = round1(median(vs))
					}
				}
				e.BySource[src] = sourceEntry{N: sb.n, Values: sv}
			}
		}
		p.Hexes = append(p.Hexes, e)
```

- [ ] **Step 6: Name the network on point entries**

In `pointsFrom`, the append becomes:

```go
		out = append(out, hexEntry{
			Lon: sr.Lon, Lat: sr.Lat, SensorID: sr.SensorID,
			N: 1, Country: country, Values: values, Source: sourceOf(sr),
		})
```

- [ ] **Step 7: Run the new tests to verify they pass**

Run: `go test ./internal/snapshot/ -run 'TestMixedBin|TestSingleNetworkBin|TestBlankSource|TestNetworkWithout|TestPointEntriesName' -v`
Expected: PASS (5 tests).

- [ ] **Step 8: Run the whole snapshot package**

Run: `go test ./internal/snapshot/`
Expected: PASS. Existing hex tests construct readings through `sensorAt`, which leaves `Source` empty, so every one of their bins is now a single-network `sensor.community` bin — `Values` and `N` are unchanged and those assertions still hold.

- [ ] **Step 9: Mutation check — the per-network median must be its own**

Temporarily change the per-network median line in `hexPayloadFrom` to read from the blended list:

```go
					sv[m] = round1(median(b.vals[m]))
```

Run: `go test ./internal/snapshot/ -run TestMixedBinReportsEachNetworkSeparately`
Expected: FAIL (`sensor.community = {n:3 P1:25}, want {n:3 P1:20}`). Revert the line to `median(vs)` and re-run to confirm PASS. If the mutation does NOT fail the test, the test is inert — fix the test before continuing.

- [ ] **Step 10: Mutation check — the blank-source rule must be load-bearing**

Temporarily change `sourceOf` to `return sr.Source`.

Run: `go test ./internal/snapshot/ -run TestBlankSourceCountsAsCommunity`
Expected: FAIL (`by_source has an empty-string network`). Revert and re-run to confirm PASS.

- [ ] **Step 11: Commit**

```bash
printf '%s\n' 'snapshot: carry each network'"'"'s own numbers on a cell' '' \
  '- a mixed bin gains by_source with per-network n and medians' \
  '- a one-network bin names its source and omits by_source' \
  '- point entries name their network' \
  '- an empty source column is sensor.community' > /tmp/t1.txt
git commit -F /tmp/t1.txt -- internal/snapshot/hexes.go internal/snapshot/hexes_test.go
```

---

### Task 2: Coverage block on the hex payload

**Files:**
- Modify: `internal/snapshot/hexes.go` (`hexPayload` ~line 101, `HexBody` ~line 163, `PointBody` ~line 217), `internal/snapshot/build.go` (~line 211), `internal/snapshot/snapshot.go` (`Snapshot` struct ~line 96)
- Test: `internal/snapshot/hexes_test.go`

**Interfaces:**
- Consumes: `sourceOf(sr store.SensorReading) string` from Task 1.
- Produces:
  - `func coverageFrom(sensors []store.SensorReading) map[string]map[string]int`
  - `hexPayload.Coverage map[string]map[string]int` (`json:"coverage,omitempty"`)
  - `Snapshot.coverage map[string]map[string]int` (unexported field)

- [ ] **Step 1: Write the failing tests**

Append to `internal/snapshot/hexes_test.go`:

```go
// Coverage counts SENSORS WITH A USABLE READING per network per metric. It is
// what the layer menu says about a metric before the reader picks it, so a
// network that reports nothing for a metric must not appear under it at all.
func TestCoverageCountsSensorsWithAReadingPerNetwork(t *testing.T) {
	cov := coverageFrom([]store.SensorReading{
		sensorFrom(1, 23.32, 42.69, "sensor.community", map[string]float64{"P1": 10, "P2": 5}),
		sensorFrom(2, 23.33, 42.70, "sensor.community", map[string]float64{"P1": 12}),
		sensorFrom(3, 24.00, 43.00, "eea", map[string]float64{"P1": 30, "O3": 60}),
		sensorFrom(4, 24.10, 43.10, "eea", map[string]float64{"O3": 55}),
	})

	if got := cov["sensor.community"]["P1"]; got != 2 {
		t.Errorf("community P1 = %d, want 2", got)
	}
	if got := cov["sensor.community"]["P2"]; got != 1 {
		t.Errorf("community P2 = %d, want 1", got)
	}
	if got := cov["eea"]["O3"]; got != 2 {
		t.Errorf("eea O3 = %d, want 2", got)
	}
	if _, ok := cov["eea"]["P2"]; ok {
		t.Errorf("eea carries a P2 entry with no eea P2 reading: %#v", cov["eea"])
	}
	if _, ok := cov[""]; ok {
		t.Errorf("coverage has an empty-string network: %#v", cov)
	}
}

// Every tier answers the same question about coverage, so the block survives
// the viewport clip. Without this a reader who has panned sees the counts
// vanish from the layer menu.
func TestClippedHexBodyKeepsCoverage(t *testing.T) {
	s := &Snapshot{
		GeneratedAt: time.Now(),
		coverage:    map[string]map[string]int{"eea": {"P2": 4}},
		hexTiers: map[float64]hexPayload{
			HexResolutionKM: {
				ResolutionKM: HexResolutionKM,
				Coverage:     map[string]map[string]int{"eea": {"P2": 4}},
				Hexes: []hexEntry{
					{Lon: 23.32, Lat: 42.69, N: 1, Values: map[string]float64{"P2": 9}},
				},
			},
		},
	}
	b, err := s.HexBody(HexResolutionKM, BBox{West: 23, South: 42, East: 24, North: 43}, true)
	if err != nil {
		t.Fatalf("HexBody: %v", err)
	}
	var got hexPayload
	if err := json.Unmarshal(b.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Coverage["eea"]["P2"] != 4 {
		t.Errorf("coverage = %#v, want eea P2 = 4", got.Coverage)
	}
}

// The point tier is served from its own builder, so it needs the block wired
// separately or the menu empties out at the deepest zoom.
func TestPointBodyCarriesCoverage(t *testing.T) {
	s := &Snapshot{
		GeneratedAt: time.Now(),
		coverage:    map[string]map[string]int{"eea": {"P1": 27}},
		points: []hexEntry{
			{Lon: 23.32, Lat: 42.69, SensorID: 1, N: 1, Source: "eea",
				Values: map[string]float64{"P1": 20}},
		},
	}
	b, err := s.PointBody(BBox{West: 23, South: 42, East: 24, North: 43})
	if err != nil {
		t.Fatalf("PointBody: %v", err)
	}
	var got hexPayload
	if err := json.Unmarshal(b.JSON, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Coverage["eea"]["P1"] != 27 {
		t.Errorf("coverage = %#v, want eea P1 = 27", got.Coverage)
	}
}
```

Add `"encoding/json"` to the import block of `internal/snapshot/hexes_test.go` if it is not already there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/snapshot/ -run 'TestCoverage|TestClippedHexBodyKeepsCoverage|TestPointBodyCarriesCoverage' -v`
Expected: FAIL — `undefined: coverageFrom`.

- [ ] **Step 3: Add the payload field and the builder**

In `internal/snapshot/hexes.go`, extend `hexPayload`:

```go
type hexPayload struct {
	GeneratedAt  time.Time  `json:"generated_at"`
	ResolutionKM float64    `json:"resolution_km"`
	Hexes        []hexEntry `json:"hexes"`
	// Coverage is how many sensors of each network have a usable reading for
	// each metric, country-wide and identical on every tier. The layer menu
	// says it about a metric the reader has not picked yet, which no per-cell
	// number can answer. Omitted when empty so a fixture-built payload does
	// not serialise a null.
	Coverage map[string]map[string]int `json:"coverage,omitempty"`
}
```

Add near `sourceOf`:

```go
// coverageFrom counts, per network, how many sensors currently hold a usable
// reading for each metric. Zero counts are omitted rather than written as 0:
// the layer menu distinguishes "no station reports this" from "some do", and an
// explicit zero is the same fact as an absent key with an extra byte per metric.
func coverageFrom(sensors []store.SensorReading) map[string]map[string]int {
	cov := make(map[string]map[string]int, 2)
	for _, sr := range sensors {
		src := sourceOf(sr)
		per := cov[src]
		if per == nil {
			per = make(map[string]int, len(upstream.CanonicalMetrics()))
			cov[src] = per
		}
		for _, m := range upstream.CanonicalMetrics() {
			if _, ok := sr.Values[m]; ok {
				per[m]++
			}
		}
	}
	return cov
}
```

- [ ] **Step 4: Carry coverage through both serving paths**

In `HexBody`, the clip branch's payload construction becomes:

```go
	out := hexPayload{GeneratedAt: p.GeneratedAt, ResolutionKM: p.ResolutionKM,
		Coverage: p.Coverage, Hexes: make([]hexEntry, 0, len(p.Hexes))}
```

In `PointBody`:

```go
	out := hexPayload{GeneratedAt: s.GeneratedAt, ResolutionKM: PointResolutionKM,
		Coverage: s.coverage, Hexes: make([]hexEntry, 0, len(s.points))}
```

- [ ] **Step 5: Hold coverage on the snapshot and fill every tier**

In `internal/snapshot/snapshot.go`, add to the `Snapshot` struct, directly under the `points` field:

```go
	// coverage is the per-network per-metric sensor count the hex payloads
	// publish. Held here as well as on each tier payload because PointBody
	// builds its envelope from scratch rather than from hexTiers.
	coverage map[string]map[string]int
```

In `internal/snapshot/build.go`, replace the tier loop at ~line 211 with:

```go
	snap.coverage = coverageFrom(sensors)
	snap.hexTiers = make(map[float64]hexPayload, len(HexTiersKM))
	for _, res := range HexTiersKM {
		p := hexPayloadFrom(now, sensors, res)
		p.Coverage = snap.coverage
		snap.hexTiers[res] = p
	}
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `go test ./internal/snapshot/ -run 'TestCoverage|TestClippedHexBodyKeepsCoverage|TestPointBodyCarriesCoverage' -v`
Expected: PASS (3 tests).

- [ ] **Step 7: Run the whole package and the API package**

Run: `go test ./internal/snapshot/ ./internal/api/`
Expected: PASS. The ETag is computed from the marshalled payload with `GeneratedAt` zeroed; `Coverage` is a map, and Go marshals map keys in sorted order, so the body stays a function of the readings.

- [ ] **Step 8: Mutation check — the clip path must copy coverage**

Temporarily drop `Coverage: p.Coverage,` from `HexBody`'s clip branch.

Run: `go test ./internal/snapshot/ -run TestClippedHexBodyKeepsCoverage`
Expected: FAIL (`coverage = map[], want eea P2 = 4`). Restore the field and re-run to confirm PASS.

- [ ] **Step 9: Commit**

```bash
printf '%s\n' 'snapshot: publish per-network metric coverage' '' \
  '- coverage counts sensors with a usable reading, per network per metric' \
  '- the block rides every hex tier and the point tier, clipped or not' \
  '- zero counts are absent, not written as 0' > /tmp/t2.txt
git commit -F /tmp/t2.txt -- internal/snapshot/hexes.go internal/snapshot/hexes_test.go internal/snapshot/build.go internal/snapshot/snapshot.go
```

---

### Task 3: The browser picks a network's numbers

**Files:**
- Modify: `web/src/lib/sourcefilter.svelte.js`, `web/src/lib/hexes.js` (`hexFeatures`, ~line 253)
- Test: `web/src/lib/__tests__/sourcefilter.test.js`, `web/src/lib/__tests__/hexes.test.js`

**Interfaces:**
- Consumes: the payload shape from Tasks 1-2 — `hexes[].source`, `hexes[].by_source[network].{n,values}`, `hexes[].values`, `hexes[].n`.
- Produces:
  - `export function sourceOf(x)` in `sourcefilter.svelte.js` — accepts a hex entry (`x.source`) or a GeoJSON feature (`x.properties.source`), defaults to `'sensor.community'`.
  - `hexFeatures(body, metric, bands, noDataColour, colourOf, pointResKM = 0, enabled = null)` — `enabled` is a `Set` of network ids, or `null` meaning "draw the blended numbers".

- [ ] **Step 1: Write the failing tests**

In `web/src/lib/__tests__/sourcefilter.test.js`, add `sourceOf` to the import list from `../sourcefilter.svelte.js` and append inside the existing top-level `describe` (or as a new one):

```js
describe('sourceOf', () => {
  it('reads a hex entry directly', () => {
    expect(sourceOf({ source: 'eea', n: 1 })).toBe('eea')
  })

  it('reads a GeoJSON feature through properties', () => {
    expect(sourceOf({ properties: { source: 'eea' } })).toBe('eea')
  })

  it('treats an absent source as sensor.community on either shape', () => {
    expect(sourceOf({ n: 3 })).toBe('sensor.community')
    expect(sourceOf({ properties: {} })).toBe('sensor.community')
  })
})
```

In `web/src/lib/__tests__/hexes.test.js`, append:

```js
describe('hexFeatures with a network filter', () => {
  const bands = [{ upper: 10, colour: '#00ff00' }, { upper: 1000, colour: '#ff0000' }]
  const body = {
    resolution_km: 15,
    hexes: [
      // Mixed: both networks reach this cell.
      {
        lon: 23.32, lat: 42.69, n: 4, values: { P2: 25 },
        by_source: {
          'sensor.community': { n: 3, values: { P2: 20 } },
          eea: { n: 1, values: { P2: 100 } },
        },
      },
      // Community only.
      { lon: 24.0, lat: 43.0, n: 2, source: 'sensor.community', values: { P2: 30 } },
      // Official only.
      { lon: 25.0, lat: 43.5, n: 1, source: 'eea', values: { P2: 40 } },
    ],
  }
  const draw = (enabled) =>
    hexFeatures(body, 'P2', bands, '#cccccc', rampColour, 0, enabled)

  it('draws the blended number when both networks are on', () => {
    const f = draw(new Set(['sensor.community', 'eea']))
    expect(f).toHaveLength(3)
    const mixed = f.find((x) => x.geometry.coordinates[0][0][0] !== undefined && x.properties.n === 4)
    expect(mixed.properties.value).toBe(25)
  })

  it('draws one network its own median and count', () => {
    const f = draw(new Set(['eea']))
    // The mixed cell's eea half plus the eea-only cell. The community-only cell
    // is gone: it has no eea reading to summarise.
    expect(f).toHaveLength(2)
    expect(f.map((x) => x.properties.value).sort((a, b) => a - b)).toEqual([40, 100])
    expect(f.map((x) => x.properties.n).sort((a, b) => a - b)).toEqual([1, 1])
  })

  it('drops a single-network cell whose network is off', () => {
    const f = draw(new Set(['sensor.community']))
    expect(f).toHaveLength(2)
    expect(f.map((x) => x.properties.value).sort((a, b) => a - b)).toEqual([20, 30])
    expect(f.map((x) => x.properties.n).sort((a, b) => a - b)).toEqual([2, 3])
  })

  it('draws nothing when every network is off', () => {
    expect(draw(new Set())).toEqual([])
  })

  it('draws the blended numbers when no filter is given', () => {
    expect(hexFeatures(body, 'P2', bands, '#cccccc', rampColour, 0).map((x) => x.properties.value))
      .toEqual([25, 30, 40])
  })

  it('treats a cell with no source at all as sensor.community', () => {
    const legacy = { resolution_km: 15, hexes: [{ lon: 23.3, lat: 42.7, n: 2, values: { P2: 11 } }] }
    expect(hexFeatures(legacy, 'P2', bands, '#cccccc', rampColour, 0, new Set(['sensor.community'])))
      .toHaveLength(1)
    expect(hexFeatures(legacy, 'P2', bands, '#cccccc', rampColour, 0, new Set(['eea'])))
      .toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- --run sourcefilter hexes`
Expected: FAIL — `sourceOf is not a function`, and the filter cases return all three features.

- [ ] **Step 3: Export sourceOf for both shapes**

In `web/src/lib/sourcefilter.svelte.js`, replace the existing private `sourceOf` with:

```js
// sourceOf names the network behind a hex entry or a GeoJSON feature. One
// function for both shapes because the same toggle governs both layers, and a
// payload written before the source column exists carries none — those rows are
// all sensor.community.
export function sourceOf(x) {
  return x?.source || x?.properties?.source || 'sensor.community'
}
```

`filterBySource` keeps calling it unchanged.

- [ ] **Step 4: Teach hexFeatures to pick a network**

In `web/src/lib/hexes.js`, import the helper at the top of the file:

```js
import { sourceOf } from './sourcefilter.svelte.js'
```

Change the signature and insert the picker before the ordering step:

```js
export function hexFeatures(body, metric, bands, noDataColour, colourOf, pointResKM = 0, enabled = null) {
```

Immediately after the `drawKM` line, add:

```js
  // Which numbers a cell reports under the current network toggles. `null`
  // means no filter is in play (the timelapse, whose frames carry no source) and
  // takes the blended values the payload leads with. A cell with nothing left to
  // report is dropped rather than drawn grey: it holds no reading from any
  // enabled network, which is not the same fact as a silent sensor.
  const pick = (h) => {
    if (enabled === null) return { values: h.values, n: h.n }
    if (enabled.size === 0) return null
    if (enabled.size > 1 && h.by_source) return { values: h.values, n: h.n }
    if (h.by_source) {
      for (const src of enabled) {
        const part = h.by_source[src]
        if (part) return { values: part.values, n: part.n }
      }
      return null
    }
    return enabled.has(sourceOf(h)) ? { values: h.values, n: h.n } : null
  }
```

Replace the `hexes`/`ordered` block with:

```js
  const raw = points && drawKM > 0 ? snapToLattice(body?.hexes ?? [], drawKM) : (body?.hexes ?? [])
  const hexes = []
  for (const h of raw) {
    const p = pick(h)
    if (p) hexes.push({ ...h, values: p.values, n: p.n })
  }
  const ordered = [
    ...hexes.filter((h) => (h.values?.[metric] ?? null) === null),
    ...hexes.filter((h) => (h.values?.[metric] ?? null) !== null),
  ]
```

The `ordered.map` body below is unchanged: it already reads `h.values`, `h.n` and `h.sensor_id`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `npm --prefix web test -- --run sourcefilter hexes`
Expected: PASS. The pre-existing `hexFeatures` cases pass an argument list ending at `pointResKM`, so `enabled` defaults to `null` and they keep drawing the blended numbers.

- [ ] **Step 6: Run the whole web suite**

Run: `npm --prefix web test`
Expected: PASS. `map.js` still calls `hexFeatures` with six arguments; nothing has changed for it yet.

- [ ] **Step 7: Mutation check — a mixed cell must not answer with the blend under one network**

Temporarily change the `h.by_source` branch to `return { values: h.values, n: h.n }`.

Run: `npm --prefix web test -- --run hexes`
Expected: FAIL (`draws one network its own median and count` reports `[25, 40]`, want `[40, 100]`). Revert and re-run to confirm PASS.

- [ ] **Step 8: Commit**

```bash
printf '%s\n' 'web: let a cell report one network at a time' '' \
  '- hexFeatures takes the enabled networks and picks that network'"'"'s median and count' \
  '- a cell with no reading from an enabled network is dropped' \
  '- no filter means the blended numbers, which is what the timelapse asks for' \
  '- sourceOf is exported and reads an entry or a feature' > /tmp/t3.txt
git commit -F /tmp/t3.txt -- web/src/lib/hexes.js web/src/lib/sourcefilter.svelte.js web/src/lib/__tests__/hexes.test.js web/src/lib/__tests__/sourcefilter.test.js
```

---

### Task 4: A network toggle repaints the grid

**Files:**
- Modify: `web/src/islands/map.js` (`refreshHexes` ~line 1126, the `installTimelapse` `paint` ~line 1184, the `onSourceChange` handler ~line 496)
- Test: `web/src/islands/__tests__/map.test.js`

**Interfaces:**
- Consumes: `hexFeatures(body, metric, bands, noDataColour, colourOf, pointResKM, enabled)` from Task 3; `getSources()` and `onSourceChange(fn)` from `sourcefilter.svelte.js`.
- Produces: `state.coverage` — the `coverage` object of the most recent hex body, or `null`. Task 5 reads it.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/islands/__tests__/map.test.js`, inside the existing `describe('refreshHexes', ...)` if there is one, otherwise as a new top-level `describe`. Use the file's existing `fakeMap` and `cfg` helpers:

```js
describe('the network toggles and the grid', () => {
  beforeEach(() => { resetSourceFilterForTests() })
  afterEach(() => { resetSourceFilterForTests() })

  const hexBody = {
    generated_at: '2026-09-10T09:00:00Z',
    resolution_km: 15,
    coverage: { eea: { P2: 4 }, 'sensor.community': { P2: 1180 } },
    hexes: [
      {
        lon: 23.32, lat: 42.69, n: 4, values: { P2: 25 },
        by_source: {
          'sensor.community': { n: 3, values: { P2: 20 } },
          eea: { n: 1, values: { P2: 100 } },
        },
      },
      { lon: 25.0, lat: 43.5, n: 1, source: 'eea', values: { P2: 40 } },
    ],
  }

  it('draws one network its own numbers without fetching again', async () => {
    const map = fakeMap()
    const state = { scales: null, hexUrl: null, hexBody: null }
    const fetchJSON = vi.fn(async () => hexBody)

    await refreshHexes(map, state, cfg, fetchJSON)
    expect(fetchJSON).toHaveBeenCalledTimes(1)

    setSourceEnabled('sensor.community', false)
    await refreshHexes(map, state, cfg, fetchJSON)

    // Still one call: the URL has not moved, so this was a repaint.
    expect(fetchJSON).toHaveBeenCalledTimes(1)
    const drawn = map.getSource(HEX_SOURCE_ID).setData.mock.calls.at(-1)[0]
    expect(drawn.features.map((f) => f.properties.value).sort((a, b) => a - b)).toEqual([40, 100])
  })

  it('holds the coverage block from the body it drew', async () => {
    const map = fakeMap()
    const state = { scales: null, hexUrl: null, hexBody: null }

    await refreshHexes(map, state, cfg, async () => hexBody)

    expect(state.coverage).toEqual({ eea: { P2: 4 }, 'sensor.community': { P2: 1180 } })
  })

  it('empties the grid when every network is off', async () => {
    const map = fakeMap()
    const state = { scales: null, hexUrl: null, hexBody: null }

    await refreshHexes(map, state, cfg, async () => hexBody)
    setSourceEnabled('sensor.community', false)
    setSourceEnabled('eea', false)
    await refreshHexes(map, state, cfg, async () => hexBody)

    const drawn = map.getSource(HEX_SOURCE_ID).setData.mock.calls.at(-1)[0]
    expect(drawn.features).toEqual([])
  })
})
```

Add `setSourceEnabled` and `resetSourceFilterForTests` to the file's import from `../../lib/sourcefilter.svelte.js`, and `HEX_SOURCE_ID` to the import from `../map.js` if either is missing. If `HEX_SOURCE_ID` is not exported from `map.js`, export it beside the existing `HEX_LABEL_LAYER_ID` export:

```js
export const HEX_SOURCE_ID = 'airbg-hexes'
```

(replacing the current `const HEX_SOURCE_ID = 'airbg-hexes'` at line 98).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- --run map`
Expected: FAIL — the drawn values are `[25, 40]` (the blend), and `state.coverage` is `undefined`.

- [ ] **Step 3: Thread the enabled networks into the grid**

In `web/src/islands/map.js`, inside `refreshHexes`, after `state.hexBody = body`, record the coverage:

```js
    state.hexUrl = url
    state.hexBody = body
  }
  // Held for the layer menu, which says how many stations of each network have
  // data for the selected metric. Read off whichever body was drawn last, so a
  // window or viewport change updates it without a second request.
  state.coverage = state.hexBody?.coverage ?? null
```

and pass the networks to `hexFeatures`:

```js
  const features = hexFeatures(
    state.hexBody, cfg.metric, bands, cfg.noDataColour, rampColour,
    resolutionForZoom(Math.round(map.getZoom())), getSources(),
  )
```

- [ ] **Step 4: Leave the timelapse on the blended numbers**

In `installTimelapse`'s `paint`, pass `null` explicitly, so the argument is a decision rather than an omission:

```js
    const features = hexFeatures(
      frameBody(body, i), cfg.metric, bands, cfg.noDataColour, rampColour,
      // No network filter: a frame is folded from reading_hourly, which carries
      // no source column, so there is nothing to filter it by.
      resolutionForZoom(Math.round(map.getZoom())), null,
    )
```

- [ ] **Step 5: Repaint the grid on a source change**

In the `onSourceChange` handler (~line 496), add the grid call and trim the stale comment:

```js
    unfilterSource = onSourceChange(() => {
      repaintSensors(map, state, cfg)
      // The grid too: refreshHexes short-circuits the fetch when the URL has not
      // moved, so this is a repaint, not a call.
      refreshHexes(map, state, cfg)
      // Unticking both networks empties the map, and the repaints alone would
      // leave that unexplained. Recomputed here rather than in repaintSensors
      // because the hint is chrome, not paint.
      chrome.showHint(mapHint(cfg.t, { fellBack: state.fellBack, sources: getSources() }))
    })
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `npm --prefix web test -- --run map`
Expected: PASS.

- [ ] **Step 7: Run the whole web suite**

Run: `npm --prefix web test`
Expected: PASS.

- [ ] **Step 8: Mutation check — the toggle must not refetch**

Temporarily change `refreshHexes`'s dedup guard from `if (url !== state.hexUrl) {` to `if (true) {`.

Run: `npm --prefix web test -- --run map`
Expected: FAIL (`expected "spy" to be called 1 times, but got 2 times`). Revert and re-run to confirm PASS.

- [ ] **Step 9: Commit**

```bash
printf '%s\n' 'web: repaint the grid when a network is switched off' '' \
  '- refreshHexes passes the enabled networks to hexFeatures' \
  '- a source change repaints the grid from the body in hand, no request' \
  '- the timelapse stays on the blended numbers; a frame carries no source' \
  '- state.coverage holds the block the drawn body carried' > /tmp/t4.txt
git commit -F /tmp/t4.txt -- web/src/islands/map.js web/src/islands/__tests__/map.test.js
```

---

### Task 5: The layer menu counts stations instead of gating on the tier

**Files:**
- Modify: `web/src/islands/map.js` (`setSourceViewAvailability` ~line 1096, its three call sites ~lines 448, 867, 968, `readConfig` ~line 1686), `web/src/lib/sourcefilter.svelte.js` (delete `MEASURED`/`measuredBy`), `internal/web/templates/base.gohtml:106-109`, `internal/i18n/en.json:100-108`, `internal/i18n/bg.json:100-108`
- Test: `web/src/islands/__tests__/map.test.js`, `web/src/lib/__tests__/sourcefilter.test.js`, `internal/i18n/i18n_test.go` (runs unchanged)

**Interfaces:**
- Consumes: `state.coverage` from Task 4.
- Produces: `setSourceViewAvailability(chrome, metric, t, coverage)` — `coverage` is the payload block or `null`. No return value. `t` must carry `communitySensors`, `officialStations`, `notMeasured`, `withData`.

- [ ] **Step 1: Replace the availability tests**

In `web/src/islands/__tests__/map.test.js`, replace the whole `describe('setSourceViewAvailability', ...)` block (~line 3243) with:

```js
describe('setSourceViewAvailability', () => {
  const t = {
    communitySensors: 'Citizen sensors',
    officialStations: 'Official stations',
    notMeasured: 'does not measure this',
    withData: 'with data',
  }
  const coverage = {
    'sensor.community': { P1: 1180, P2: 1180 },
    eea: { P1: 27, P2: 4, O3: 20 },
  }

  function menu() {
    const fieldset = document.createElement('fieldset')
    const boxes = {}
    for (const id of ['communitySensors', 'officialStations']) {
      const label = document.createElement('label')
      const input = document.createElement('input')
      input.type = 'checkbox'
      input.dataset.layerKey = `view:${id}`
      const span = document.createElement('span')
      label.append(input, span)
      fieldset.appendChild(label)
      boxes[id] = { input, span }
    }
    return { chrome: { layersUI: { fieldset } }, boxes }
  }

  // The bug this replaces: both boxes were disabled anywhere but the sensor
  // tier, so unticking a network on the opening map did nothing.
  it('leaves both live and says how many stations have the metric', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'P2', t, coverage)

    expect(boxes.communitySensors.input.disabled).toBe(false)
    expect(boxes.officialStations.input.disabled).toBe(false)
    expect(boxes.officialStations.span.textContent).toBe('Official stations — 4 with data')
    expect(boxes.communitySensors.span.textContent).toBe('Citizen sensors — 1180 with data')
  })

  it('names the metric a network does not measure, and still lets it be switched off', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'O3', t, coverage)

    expect(boxes.communitySensors.span.textContent).toBe('Citizen sensors — does not measure this')
    expect(boxes.communitySensors.input.disabled).toBe(false)
    expect(boxes.officialStations.span.textContent).toBe('Official stations — 20 with data')
  })

  it('falls back to the bare label before any coverage has arrived', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'P2', t, null)

    expect(boxes.officialStations.span.textContent).toBe('Official stations')
    expect(boxes.officialStations.input.disabled).toBe(false)
  })

  it('follows the metric from one call to the next', () => {
    const { chrome, boxes } = menu()
    setSourceViewAvailability(chrome, 'O3', t, coverage)
    setSourceViewAvailability(chrome, 'P1', t, coverage)

    expect(boxes.communitySensors.span.textContent).toBe('Citizen sensors — 1180 with data')
  })
})
```

Then delete the tier-gate test `it('disables the network checkboxes at the country tier', ...)` at ~line 729 and replace it with:

```js
  // The bug: the country tier used to disable both boxes, so the toggle on the
  // opening map was inert. They stay live at every tier now.
  it('leaves the network checkboxes live at the country tier', async () => {
    vi.stubGlobal('fetch', stubFetch({ scalesOk: true }))
    const fieldset = document.createElement('fieldset')
    const boxes = ['communitySensors', 'officialStations'].map((id) => {
      const label = document.createElement('label')
      const input = document.createElement('input')
      input.type = 'checkbox'
      input.dataset.layerKey = `view:${id}`
      label.append(input, document.createElement('span'))
      fieldset.appendChild(label)
      return input
    })
    const chrome = { ...hintController(() => {}), showLegend: () => {}, layersUI: { fieldset } }

    await initData(fakeMap(7), { slug: null, tier: null, scales: null }, cfg, chrome)

    expect(boxes.map((b) => b.disabled)).toEqual([false, false])
  })
```

In `web/src/lib/__tests__/sourcefilter.test.js`, delete the four `measuredBy` cases (`'knows the gases are official-only'`, `'knows the weather metrics are citizen-only'`, `'knows both networks measure particulates'`, `'assumes an unknown metric is measured by everyone'`) and remove `measuredBy` from the import list.

In `web/src/islands/__tests__/map.test.js`, replace every `tSensorTierOnly:` and `sensorTierOnly:` fixture entry (~lines 320, 369, 3248 and the `initData` fixture at ~1388's neighbourhood) with the `withData` equivalents: `tWithData: 'with data'` on a dataset fixture, `withData: 'with data'` on a `t` object.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm --prefix web test -- --run map sourcefilter`
Expected: FAIL — the label reads `Official stations — only when single sensors are shown`, and the country-tier case still reports `[true, true]`.

- [ ] **Step 3: Rewrite setSourceViewAvailability**

In `web/src/islands/map.js`, replace the function (~line 1096) and its comment block with:

```js
// setSourceViewAvailability labels each network's checkbox with how many of its
// stations currently hold a reading for the selected metric.
//
// Never disables. The count is the whole message: with P2 the default metric and
// four official stations reporting it, a reader who sees an empty official layer
// needs the number, not a dead control. A network that does not measure the
// metric at all says so instead of showing a zero.
export function setSourceViewAvailability(chrome, metric, t, coverage) {
  for (const [id, source] of [['communitySensors', 'sensor.community'], ['officialStations', 'eea']]) {
    const input = chrome.layersUI?.fieldset?.querySelector(`[data-layer-key="view:${id}"]`)
    if (!input) continue
    const span = input.parentElement?.querySelector('span')
    if (!span) continue
    // No coverage yet — the first paint runs before the grid has answered. The
    // bare label is the honest thing to show; a "0 with data" would be a claim
    // about the network rather than about what we have loaded.
    const per = coverage?.[source]
    if (!per) {
      span.textContent = t[id]
      continue
    }
    const n = per[metric] ?? 0
    // Composed from catalogue parts, as sensorCountLine is: i18n.Catalogue.T
    // takes no parameters, so a sentence with a number in it is assembled here.
    span.textContent = n > 0 ? `${t[id]} — ${n} ${t.withData}` : `${t[id]} — ${t.notMeasured}`
  }
}
```

- [ ] **Step 4: Update the three call sites and drop the tier flag**

At ~line 448 (in the layer install):

```js
    setSourceViewAvailability(chrome, cfg.metric, cfg.t, state.coverage)
```

At ~line 867 (in `onMetricChange`), keep it after `refreshHexes` so the newest coverage is in hand:

```js
  refreshHexes(map, state, cfg)
  setSourceViewAvailability(chrome, metric, cfg.t, state.coverage)
```

At ~line 968 (in `refresh`), delete the `state.onSensorTier` assignment and its comment, and call:

```js
  setSourceViewAvailability(chrome, cfg.metric, cfg.t, state.coverage)
```

Delete the now-unused `state.onSensorTier` reads elsewhere in the file (search `onSensorTier` — there must be none left).

Update the `sourceViews` comment at ~line 426 to:

```js
    // One toggle per network, both on by default (no defaultOff).
    // setSourceViewAvailability labels each with its station count for the
    // selected metric.
```

- [ ] **Step 5: Refresh the labels when coverage arrives**

At the end of `refreshHexes`, after `state.coverage` is set, the menu has to be told. `refreshHexes` has no `chrome`, so the update rides the callers that do. In the `onSourceChange` handler and in `refresh` the call already follows. Add it to the initial load path in `initData`, immediately after its `refreshHexes` await:

```js
  setSourceViewAvailability(chrome, cfg.metric, cfg.t, state.coverage)
```

- [ ] **Step 6: Delete the hardcoded coverage table**

In `web/src/lib/sourcefilter.svelte.js`, delete the `MEASURED` constant and the `measuredBy` function entirely, including their comment blocks. In `web/src/islands/map.js`, remove `measuredBy` from the import at ~line 23.

- [ ] **Step 7: Swap the copy**

In `internal/i18n/en.json`, remove `"map.view.sensor_tier_only"` and add, in alphabetical position:

```json
  "map.view.with_data": "with data",
```

In `internal/i18n/bg.json`, remove `"map.view.sensor_tier_only"` and add:

```json
  "map.view.with_data": "с данни",
```

In `internal/web/templates/base.gohtml`, replace line 109:

```gohtml
     data-t-with-data="{{.T "map.view.with_data"}}"
```

In `web/src/islands/map.js`'s `readConfig` (~line 1686), replace `sensorTierOnly: d.tSensorTierOnly || '',` with:

```js
      withData: d.tWithData || '',
```

- [ ] **Step 8: Run the web tests to verify they pass**

Run: `npm --prefix web test`
Expected: PASS.

- [ ] **Step 9: Run the Go tests for copy and templates**

Run: `go test ./internal/i18n/ ./internal/web/`
Expected: PASS. `TestCataloguesHaveIdenticalKeys` covers the two catalogues; the template tests cover the attribute rename. If a template test asserts the old attribute by name, update that assertion to `data-t-with-data`.

- [ ] **Step 10: Confirm the removed strings are gone**

Run: `grep -rn "sensorTierOnly\|sensor_tier_only\|measuredBy\|MEASURED\|onSensorTier" web/src internal/i18n internal/web/templates`
Expected: no output.

- [ ] **Step 11: Mutation check — the count must come from coverage**

Temporarily change the count line to `const n = 1`.

Run: `npm --prefix web test -- --run map`
Expected: FAIL (`Official stations — 1 with data`, want `— 4 with data`). Revert and re-run to confirm PASS.

- [ ] **Step 12: Commit**

```bash
printf '%s\n' 'web: say how many stations have the metric, and stop disabling the toggles' '' \
  '- the layer menu labels each network with its station count for the metric' \
  '- a network that does not measure the metric says so; neither box is disabled' \
  '- the hardcoded MEASURED table goes; the count comes from the payload' \
  '- map.view.sensor_tier_only is replaced by map.view.with_data' > /tmp/t5.txt
git add internal/i18n/en.json internal/i18n/bg.json
git commit -F /tmp/t5.txt -- web/src/islands/map.js web/src/islands/__tests__/map.test.js web/src/lib/sourcefilter.svelte.js web/src/lib/__tests__/sourcefilter.test.js internal/i18n/en.json internal/i18n/bg.json internal/web/templates/base.gohtml
```

---

### Task 6: End-to-end on the opening map

**Files:**
- Modify: `web/e2e/sources.spec.js`
- Test: the same file

**Interfaces:**
- Consumes: everything above. No new exports.

- [ ] **Step 1: Rewrite the spec against the country tier**

Replace the `test.describe.serial('the network layers', ...)` block in `web/e2e/sources.spec.js` with:

```js
// EN routes throughout (see metric.spec.js). One shared context per file, as in
// locate.spec.js: a second cold load spends the rate limiter's burst on assets.
test.describe.serial('the network layers', () => {
  let page

  test.beforeAll(async ({ ctx }) => {
    page = await ctx.newPage()
    const grid = page.waitForResponse(/\/api\/v1\/hexes/)
    // '/en/', the opening map, deliberately: the toggles used to be disabled
    // anywhere but the sensor tier, so this is the page where the bug lived.
    await page.goto('/en/')
    await grid
  })

  test.afterAll(async () => { await page.close() })

  test('both networks are offered, on, and live on the opening map', async () => {
    await page.getByRole('button', { name: 'Layers' }).click()
    await expect(page.getByRole('checkbox', { name: /Citizen sensors/ })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: /Citizen sensors/ })).toBeEnabled()
    await expect(page.getByRole('checkbox', { name: /Official stations/ })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: /Official stations/ })).toBeEnabled()
  })

  test('switching a network off repaints the grid without a request', async () => {
    const requests = []
    page.on('request', (r) => { if (r.url().includes('/api/v1/')) requests.push(r.url()) })
    const painted = page.evaluate(() => new Promise((resolve) => {
      document.querySelector('[data-island="map"]')
        .addEventListener('airbg:paint', (e) => resolve(e.detail.source), { once: true })
    }))
    await page.getByRole('checkbox', { name: /Citizen sensors/ }).uncheck()
    expect(await painted).toBe('airbg-hexes')
    expect(requests).toHaveLength(0)
    await page.getByRole('checkbox', { name: /Citizen sensors/ }).check()
  })

  test('the layer menu counts the stations that have the metric', async () => {
    await expect(page.getByText(/Official stations — \d+ with data/)).toBeVisible()
  })

  test('a metric only one network measures explains itself', async () => {
    await page.getByRole('button', { name: /^Metric:/ }).click()
    await page.getByRole('radio', { name: 'Ozone' }).check()
    // The metric switcher is its own disclosure, outside the layers root, so
    // picking a metric there closes the layers panel (mountLayers' own
    // outside-mousedown handler) — reopen it to reach the label.
    await page.getByRole('button', { name: 'Layers' }).click()
    await expect(page.getByText(/Citizen sensors — does not measure this/)).toBeVisible()
    await expect(page.getByRole('checkbox', { name: /Citizen sensors/ })).toBeEnabled()
  })
})
```

The footer test at the bottom of the file is unchanged.

- [ ] **Step 2: Confirm the paint event names the grid source**

Run: `grep -n "airbg:paint" web/src/islands/map.js`
Expected: one dispatch, inside `paintSource` (~line 829), carrying `detail.source = sourceId`. `refreshHexes` paints through `paintSource(map, HEX_SOURCE_ID, ...)` at ~line 1160, so a grid repaint already fires the event with `'airbg-hexes'`. No change needed; this step is a check, not an edit.

- [ ] **Step 3: Build the web bundle**

Run: `npm --prefix web run build && touch internal/web/dist/.keep`
Expected: build succeeds. The `touch` restores the file `vite build` deletes.

- [ ] **Step 4: Run the tagged e2e suite**

Run:

```bash
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" \
TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock \
go test -tags e2e ./internal/e2e/... -run TestE2E -v
```

Expected: PASS, including the four `the network layers` cases.

- [ ] **Step 5: Run the full Go and web suites once more**

Run: `go test ./... && npm --prefix web test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
printf '%s\n' 'web: prove the toggles work on the opening map' '' \
  '- the sources e2e opens /en/, the tier where the toggles were inert' \
  '- unticking a network repaints the grid and issues no request' \
  '- the station count and the not-measured label are asserted on screen' > /tmp/t6.txt
git commit -F /tmp/t6.txt -- web/e2e/sources.spec.js internal/web/dist/.keep
```

If `internal/web/dist/.keep` is unchanged, drop it from the path list rather than committing an empty change.

---

## Self-Review

**Spec coverage**

| Spec section | Task |
|---|---|
| `by_source` per-network n and medians | 1 |
| `source` on single-network and point entries | 1 |
| blended `values` kept, not recomputed | 1, 3 |
| empty `Source` is `sensor.community` | 1, 3 |
| `coverage` block, all tiers and the point tier | 2 |
| client picks per toggle, drops empty cells | 3 |
| both off → empty grid + `noSources` hint | 3 (empty features), 4 (hint already wired) |
| toggle is a repaint, never a request | 4 |
| timelapse untouched | 4 (explicit `null`) |
| layer menu counts, no disabling | 5 |
| `MEASURED`/`measuredBy` deleted | 5 |
| `sensorTierOnly` / `onSensorTier` removed | 5 |
| e2e on `/en/` | 6 |

**Placeholder scan:** none. Every code step carries the code.

**Type consistency:** `sourceEntry{N,Values}` (Task 1) is what `hexEntry.BySource` holds and what Task 3's `pick` reads as `by_source[src].{n,values}`. `coverageFrom` returns `map[string]map[string]int` (Task 2), served as `coverage`, held as `state.coverage` (Task 4), consumed as `coverage[source][metric]` (Task 5). `hexFeatures`'s seventh argument is `enabled` throughout Tasks 3, 4 and 6. `setSourceViewAvailability`'s fourth argument is `coverage` at all four call sites.
