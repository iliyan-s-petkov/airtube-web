# Official EEA/ExEA Stations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put Bulgaria's 32 government reference stations on the map alongside the sensor.community devices, as a layer the reader can switch on and off, with the EEA and ExEA credited under the map.

**Architecture:** A new `internal/upstream/eea` package polls the EEA Air Quality Download API hourly, decodes Parquet, joins coordinates from a cached metadata CSV, and writes through the existing `store` batching path. The `sensor` table gains a `source` column; official rows take synthetic ids from a reserved sequence. The frontend does not gain a second MapLibre source — it gains a source filter alongside the existing status filter, which is the pattern `web/src/lib/sensorfilter.svelte.js` already established.

**Tech Stack:** Go 1.22+, pgx/v5, goose/v3, TimescaleDB/PostGIS, `github.com/parquet-go/parquet-go` (the one permitted new dependency), Svelte islands + MapLibre, Vitest, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-09-eea-official-stations-design.md`

## Deviation from the spec, stated up front

Spec §5 says "Two MapLibre sources rather than one, so a layer toggle is a visibility change and not a refetch." The codebase does not support that shape cheaply: `web/src/islands/map.js` paints ONE source (`airbg-data`) for both the area tiers and the sensor tier, and a toggle over sensors is already implemented as a pure filter over the features in hand (`filterByStatus` + `repaintSensors`, `web/src/lib/sensorfilter.svelte.js`). A second source would need a second paint path, a second `maxzoom` handover, a second label layer and a second entry in every `airbg:paint` assertion.

**This plan implements the toggle as a filter, not a second source.** It still costs no refetch — `repaintSensors` redraws from `state.sensorBody`, which is exactly the property the spec asked for. Everything else in §5 is implemented as written.

## Global Constraints

- **No new third-party dependency** beyond the one scoped exception: `github.com/parquet-go/parquet-go`. Existing permitted direct deps are `pgx/v5`, `goose/v3`, `testcontainers-go`.
- **All SQL through pgx parameterised queries.** String-concatenated SQL is forbidden project-wide, test helpers included.
- **No hardcoded credentials or endpoints.** Everything configurable via `airbg.yaml` keys and `AIRBG_*` env vars.
- **An `AIRBG_*` key is a two-file change:** the Ansible role's `env.j2` and `deploy/.env.example` must both carry it, or the role aborts.
- **No bulk comments** in YAML, SQL, Dockerfiles, scripts or source. A one- or two-line pointer; the prose goes in a README or a doc.
- **Comment style: flat and factual.** A comment states what the code does and the concrete constraint that forced it — name the API, the type, the failure mode. No metaphor, no rhetorical contrast, no narrative voice. If a comment only restates intent, delete it.
- **Commit style: imperative subject + flat bullets.** No prose paragraphs arguing the design.
- **`www-root/` is the dead legacy app and is never modified.**
- **No endpoint accepts a bounding box or an unbounded list parameter.** Anti-extraction is tiering, not authentication.
- **Never use `any` in Go without explicit permission.**
- **Prefer composition over inheritance.**
- **Commit with explicit paths** — `git commit -F /tmp/airbg-commit-msg.txt -- <paths>` — never `git add . && git commit`. This repo is shared with other sessions. A NEW file must be `git add`ed before it can be named in an explicit-path commit.
- **Commit messages via file**, never `-m`: backticks in a `-m` string execute.
- **No `Co-Authored-By` trailer. Never stage `CLAUDE.md`.**
- **Mutation testing is mandatory** for any test asserting a security or correctness invariant: break the implementation, prove the test fails, restore.
- Shell CWD drifts — prefix every command with `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea &&`.
- DB-backed Go tests need colima plus `DOCKER_HOST="unix://$HOME/.colima/default/docker.sock"` and `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock`.
- `npm run build` deletes `internal/web/dist/.keep` — restore with `git checkout internal/web/dist/.keep` before committing.
- URL paths: `/en/…` works, `/bg/…` 404s (Bulgarian is served from `/`).

## File Structure

**Create:**
- `internal/db/migrations/00011_sources.sql` — source columns, station metadata, reserved id sequence, `source_invalid` quality value
- `internal/upstream/eea/parquet.go` — Parquet row type and the two decode helpers
- `internal/upstream/eea/parquet_test.go`
- `internal/upstream/eea/pollutant.go` — EEA pollutant code → canonical metric, and unit normalisation
- `internal/upstream/eea/pollutant_test.go`
- `internal/upstream/eea/metadata.go` — the frozen coordinate CSV, fetch + disk cache + lookup
- `internal/upstream/eea/metadata_test.go`
- `internal/upstream/eea/client.go` — `/ParquetFile/urls` and conditional file GETs
- `internal/upstream/eea/client_test.go`
- `internal/upstream/eea/collector.go` — the hourly loop
- `internal/upstream/eea/collector_test.go`
- `internal/upstream/eea/README.md` — the prose the source files do not carry
- `internal/upstream/eea/testdata/` — one truncated Parquet file, one metadata CSV extract
- `web/src/lib/sourcefilter.svelte.js` — the source filter store and its pure filter
- `web/src/lib/__tests__/sourcefilter.test.js`
- `web/e2e/sources.spec.js`

**Modify:**
- `internal/store/store.go` — `UpsertStations`, `WriteStationReadings`
- `internal/store/aggregate.go` — carry `Source` on `SensorReading`, add the station columns
- `internal/upstream/types.go` — the six new canonical metrics
- `internal/config/schema.go`, `resolve.go`, `validate.go` — the `eea:` block
- `airbg.yaml`, `deploy/.env.example`, the Ansible role's `env.j2`
- `cmd/airbg/main.go` — start the EEA collector in `collect` and `serve`
- `internal/api/scales.go` — five gas scale tables
- `internal/api/overview.go` — attribution becomes a list
- `internal/snapshot/build.go` — `source` and station columns in the sensor payload
- `internal/quality/…` config ranges for the new metrics (via `airbg.yaml`)
- `web/src/islands/map.js` — two view toggles, marker shape by source
- `web/src/lib/stations.js` — new meta columns
- `web/src/components/SensorPanel.svelte` — station name, type, area, network
- `internal/web/templates/base.gohtml` — footer entries and layer labels
- `internal/i18n/en.json`, `internal/i18n/bg.json`

---

### Task 1: Migration — source columns and the reserved id range

**Files:**
- Create: `internal/db/migrations/00011_sources.sql`
- Test: `internal/db/migrate_test.go` (add a case)

**Interfaces:**
- Consumes: nothing.
- Produces: `sensor.source`, `sensor.source_ref`, `sensor.station_code`, `sensor.station_name`, `sensor.station_type`, `sensor.station_area`; sequence `official_sensor_id_seq`; `quality_flag` value `'source_invalid'`.

- [ ] **Step 1: Write the failing test**

Append to `internal/db/migrate_test.go`:

```go
func TestSourceColumnsExist(t *testing.T) {
	ctx, pool := testsupport.MigratedPool(t)

	var def string
	err := pool.QueryRow(ctx,
		`SELECT column_default FROM information_schema.columns
		 WHERE table_name = 'sensor' AND column_name = 'source'`).Scan(&def)
	if err != nil {
		t.Fatalf("sensor.source is missing: %v", err)
	}
	if !strings.Contains(def, "sensor.community") {
		t.Errorf("sensor.source defaults to %q, want the sensor.community default", def)
	}

	// The reserved range must not collide with any community sensor id.
	var start int64
	if err := pool.QueryRow(ctx,
		`SELECT last_value FROM official_sensor_id_seq`).Scan(&start); err != nil {
		t.Fatalf("official_sensor_id_seq is missing: %v", err)
	}
	if start < 9_000_000_000 {
		t.Errorf("official_sensor_id_seq starts at %d, want >= 9000000000", start)
	}

	var ok bool
	if err := pool.QueryRow(ctx,
		`SELECT 'source_invalid' = ANY(enum_range(NULL::quality_flag)::text[])`).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("quality_flag has no source_invalid value")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test ./internal/db/ -run TestSourceColumnsExist -v`
Expected: FAIL — `sensor.source is missing`.

- [ ] **Step 3: Write the migration**

`internal/db/migrations/00011_sources.sql`:

```sql
-- +goose NO TRANSACTION
-- +goose Up
ALTER TYPE quality_flag ADD VALUE IF NOT EXISTS 'source_invalid';

-- +goose StatementBegin
ALTER TABLE sensor
    ADD COLUMN IF NOT EXISTS source       text NOT NULL DEFAULT 'sensor.community',
    ADD COLUMN IF NOT EXISTS source_ref   text,
    ADD COLUMN IF NOT EXISTS station_code text,
    ADD COLUMN IF NOT EXISTS station_name text,
    ADD COLUMN IF NOT EXISTS station_type text,
    ADD COLUMN IF NOT EXISTS station_area text;
-- +goose StatementEnd

ALTER TABLE sensor
    ADD CONSTRAINT sensor_source_known CHECK (source IN ('sensor.community', 'eea'));

CREATE UNIQUE INDEX IF NOT EXISTS sensor_source_ref_idx
    ON sensor (source_ref) WHERE source_ref IS NOT NULL;

CREATE INDEX IF NOT EXISTS sensor_source_idx ON sensor (source);

CREATE SEQUENCE IF NOT EXISTS official_sensor_id_seq START WITH 9000000000;

-- +goose Down
DROP SEQUENCE IF EXISTS official_sensor_id_seq;
DROP INDEX IF EXISTS sensor_source_idx;
DROP INDEX IF EXISTS sensor_source_ref_idx;
ALTER TABLE sensor DROP CONSTRAINT IF EXISTS sensor_source_known;
ALTER TABLE sensor
    DROP COLUMN IF EXISTS station_area,
    DROP COLUMN IF EXISTS station_type,
    DROP COLUMN IF EXISTS station_name,
    DROP COLUMN IF EXISTS station_code,
    DROP COLUMN IF EXISTS source_ref,
    DROP COLUMN IF EXISTS source;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM reading WHERE quality = 'source_invalid') THEN
        RAISE EXCEPTION 'readings still carry quality source_invalid';
    END IF;
END
$$;
-- +goose StatementEnd
```

- [ ] **Step 4: Run test to verify it passes**

Run: same command as Step 2.
Expected: PASS.

- [ ] **Step 5: Mutation-check the constraint**

Temporarily change the CHECK to `CHECK (true)`, run `go test ./internal/db/ -run TestSourceColumnsExist`. The test does not cover the CHECK — add this assertion to the test rather than leaving it unproven:

```go
	_, err = pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location, source)
		 VALUES (1, 'test', ST_SetSRID(ST_MakePoint(23.3, 42.7), 4326)::geography, 'made-up')`)
	if err == nil {
		t.Error("sensor.source accepted an unknown source")
	}
```

Restore the CHECK, re-run, confirm PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git add internal/db/migrations/00011_sources.sql
printf '%s\n' 'db: add sensor.source and official station columns' '' '- sensor.source: sensor.community | eea, CHECK constrained' '- sensor.source_ref: EEA sampling point, unique where not null' '- station_code/name/type/area: EEA classification, null for community' '- official_sensor_id_seq starts at 9e9 so synthetic ids cannot collide' '  with upstream sensor.community device ids' '- quality_flag gains source_invalid for EEA Validity <= 0' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/db/migrations/00011_sources.sql internal/db/migrate_test.go
```

---

### Task 2: Parquet decoding

**Files:**
- Create: `internal/upstream/eea/parquet.go`, `internal/upstream/eea/parquet_test.go`
- Create: `internal/upstream/eea/testdata/spo_bg0070a_06001_100.parquet`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Row struct{ Samplingpoint string; Pollutant int32; Start, End time.Time; Value float64; Unit, AggType string; Validity, Verification int32 }` and `func DecodeRows(r io.ReaderAt, size int64) ([]Row, error)`.

The EEA schema, as `parquet-go` reports it — this is why the two helpers exist:

```
message root {
    optional binary Samplingpoint (STRING);
    optional int32 Pollutant (INT(32,true));
    required int96 Start;
    required int96 End;
    optional fixed_len_byte_array(16) Value (DECIMAL(38,18));
    optional binary Unit (STRING);
    optional binary AggType (STRING);
    required int32 Validity (INT(32,true));
    required int32 Verification (INT(32,true));
    required int96 ResultTime;
    optional fixed_len_byte_array(16) DataCapture (DECIMAL(38,18));
    optional binary FkObservationLog (STRING);
}
```

`int96` is a deprecated Parquet type with no time helper in the library, and `DECIMAL(38,18)` arrives as a raw 16-byte big-endian integer. Both are decoded by hand below; both were verified against a live file on 2026-09-09 (14,712 rows, validity distribution `{1: 13599, -1: 1113}`).

- [ ] **Step 1: Fetch the test fixture**

The fixture must be a real EEA file, truncated to one row group. Do not hand-write one — the point is to pin the real schema.

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
mkdir -p internal/upstream/eea/testdata
```

Then, from the sandbox (Bash `curl` is blocked in this repo):

```
ctx_execute(language: "javascript", code: `
const urls = await (await fetch('https://eeadmz1-downloads-api-appservice.azurewebsites.net/ParquetFile/urls', {
  method: 'POST', headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({countries:['BG'], cities:[], pollutants:['PM2.5'], dataset:1, source:'API'}),
})).text()
const first = urls.split('\\n').find(l => l.includes('SPO-BG0070A'))
console.log(first)
`)
```

Download that one file to `internal/upstream/eea/testdata/spo_bg0070a_06001_100.parquet` with a second `ctx_execute` writing the body to disk. Keep it under 1 MB; if the live file is larger, keep only the first row group.

- [ ] **Step 2: Write the failing test**

`internal/upstream/eea/parquet_test.go`:

```go
package eea_test

import (
	"os"
	"testing"
	"time"

	"airbg.org/internal/upstream/eea"
)

func TestDecodeRowsReadsARealFile(t *testing.T) {
	f, err := os.Open("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}

	rows, err := eea.DecodeRows(f, st.Size())
	if err != nil {
		t.Fatalf("DecodeRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows decoded")
	}

	r := rows[0]
	if r.Samplingpoint == "" {
		t.Error("Samplingpoint is empty")
	}
	if r.Pollutant != 6001 {
		t.Errorf("Pollutant = %d, want 6001", r.Pollutant)
	}
	// A misdecoded int96 lands in 1970 or in the far future; a range check
	// catches both.
	if r.Start.Before(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) ||
		r.Start.After(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Start = %v, outside the plausible range", r.Start)
	}
	if !r.End.After(r.Start) {
		t.Errorf("End %v is not after Start %v", r.End, r.Start)
	}
	// DECIMAL(38,18) decoded wrong is off by a factor of 1e18 either way.
	if r.Value < 0 || r.Value > 5000 {
		t.Errorf("Value = %v, outside the plausible µg/m³ range", r.Value)
	}
	if r.Unit != "ug.m-3" {
		t.Errorf("Unit = %q, want ug.m-3", r.Unit)
	}
	if r.Validity != 1 && r.Validity != -1 {
		t.Errorf("Validity = %d, want 1 or -1", r.Validity)
	}
}

func TestDecodeRowsRejectsGarbage(t *testing.T) {
	junk := []byte("this is not a parquet file at all, not even close")
	if _, err := eea.DecodeRows(bytesReaderAt(junk), int64(len(junk))); err == nil {
		t.Error("DecodeRows accepted a non-Parquet payload")
	}
}
```

Add the helper at the bottom of the same file:

```go
type bytesReaderAt []byte

func (b bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b)) {
		return 0, io.EOF
	}
	n := copy(p, b[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
```

with `"io"` added to the imports.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/eea/ -run TestDecodeRows -v`
Expected: FAIL — the package does not compile, `undefined: eea.DecodeRows`.

- [ ] **Step 4: Add the dependency**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
go get github.com/parquet-go/parquet-go@latest
go mod tidy
```

- [ ] **Step 5: Write the implementation**

`internal/upstream/eea/parquet.go`:

```go
// Package eea ingests Bulgaria's official reference stations from the European
// Environment Agency's Air Quality Download API. See README.md.
package eea

import (
	"fmt"
	"io"
	"math/big"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/deprecated"
)

// Row is one hourly observation as the file stores it. Only the columns we
// read are declared; parquet-go ignores the rest.
type Row struct {
	Samplingpoint string
	Pollutant     int32
	Start         time.Time
	End           time.Time
	Value         float64
	Unit          string
	AggType       string
	Validity      int32
	Verification  int32
}

// fileRow mirrors the file's own physical types. Start/End are int96 and Value
// is a DECIMAL(38,18) in a fixed_len_byte_array(16); neither has a Go mapping
// parquet-go will do for us.
type fileRow struct {
	Samplingpoint string           `parquet:"Samplingpoint"`
	Pollutant     int32            `parquet:"Pollutant"`
	Start         deprecated.Int96 `parquet:"Start"`
	End           deprecated.Int96 `parquet:"End"`
	Value         [16]byte         `parquet:"Value"`
	Unit          string           `parquet:"Unit"`
	AggType       string           `parquet:"AggType"`
	Validity      int32            `parquet:"Validity"`
	Verification  int32            `parquet:"Verification"`
}

// julianEpochDay is the Julian day number of 1970-01-01, the offset an int96
// timestamp's day word is expressed against.
const julianEpochDay = 2440588

// int96Time reads Parquet's deprecated int96 timestamp: nanoseconds-within-day
// in the low two 32-bit words, Julian day number in the third.
func int96Time(v deprecated.Int96) time.Time {
	nanos := int64(uint64(v[0]) | uint64(v[1])<<32)
	return time.Unix((int64(v[2])-julianEpochDay)*86400, nanos).UTC()
}

// dec18 reads a DECIMAL(38,18) stored big-endian in 16 bytes.
func dec18(b [16]byte) float64 {
	i := new(big.Int).SetBytes(b[:])
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(i), big.NewFloat(1e18)).Float64()
	return f
}

// DecodeRows reads a whole EEA Parquet file. size is required by the format:
// the footer is at the end, so the reader must be told where the end is.
func DecodeRows(r io.ReaderAt, size int64) ([]Row, error) {
	raw, err := parquet.Read[fileRow](r, size)
	if err != nil {
		return nil, fmt.Errorf("eea: read parquet: %w", err)
	}
	out := make([]Row, 0, len(raw))
	for _, fr := range raw {
		out = append(out, Row{
			Samplingpoint: fr.Samplingpoint,
			Pollutant:     fr.Pollutant,
			Start:         int96Time(fr.Start),
			End:           int96Time(fr.End),
			Value:         dec18(fr.Value),
			Unit:          fr.Unit,
			AggType:       fr.AggType,
			Validity:      fr.Validity,
			Verification:  fr.Verification,
		})
	}
	return out, nil
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/eea/ -run TestDecodeRows -v`
Expected: PASS, both tests.

- [ ] **Step 7: Mutation-check the two decoders**

Change `julianEpochDay` to `2440587`, run the test — `TestDecodeRowsReadsARealFile` must fail on `End is not after Start` or the range check. Restore.
Change `big.NewFloat(1e18)` to `big.NewFloat(1e15)`, run — the value range check must fail. Restore. Re-run, confirm PASS.

- [ ] **Step 8: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git add internal/upstream/eea/parquet.go internal/upstream/eea/parquet_test.go internal/upstream/eea/testdata
printf '%s\n' 'eea: decode the EEA parquet files' '' '- add github.com/parquet-go/parquet-go (scoped dependency exception)' '- Start/End are int96; parquet-go has no time helper, so int96Time()' '  decodes nanos-in-day + Julian day by hand' '- Value is DECIMAL(38,18) in fixed_len_byte_array(16); dec18() reads it' '  as a big-endian big.Int over 1e18' '- fixture is a real EEA file, not a synthetic one: the schema under' '  test is theirs, not ours' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/upstream/eea go.mod go.sum
```

---

### Task 3: Pollutant codes, metric names and units

**Files:**
- Create: `internal/upstream/eea/pollutant.go`, `internal/upstream/eea/pollutant_test.go`
- Modify: `internal/upstream/types.go`

**Interfaces:**
- Consumes: `Row` from Task 2.
- Produces: `func MetricFor(code int32) (string, bool)`, `func NormaliseValue(metric string, value float64, unit string) (float64, error)`, and six new entries in `upstream.canonicalMetrics`: `SO2`, `O3`, `NO2`, `CO`, `C6H6`, `NOX`.

Coverage measured on 2026-09-09, in sampling points: SO₂ 28, PM10 27, NO₂ 25, O₃ 20, CO 18, benzene 18, PM2.5 4, NOx-as-NO₂ 1.

- [ ] **Step 1: Write the failing test**

`internal/upstream/eea/pollutant_test.go`:

```go
package eea_test

import (
	"testing"

	"airbg.org/internal/upstream"
	"airbg.org/internal/upstream/eea"
)

// PM10 and PM2.5 map onto the metric names the citizen sensors already use, so
// one station and one nephelometer are comparable on the same scale table.
func TestMetricForMapsParticulatesOntoTheExistingNames(t *testing.T) {
	for code, want := range map[int32]string{5: "P1", 6001: "P2"} {
		got, ok := eea.MetricFor(code)
		if !ok || got != want {
			t.Errorf("MetricFor(%d) = %q, %v; want %q, true", code, got, ok, want)
		}
	}
}

func TestMetricForMapsTheGases(t *testing.T) {
	for code, want := range map[int32]string{
		1: "SO2", 7: "O3", 8: "NO2", 9: "NOX", 10: "CO", 20: "C6H6",
	} {
		got, ok := eea.MetricFor(code)
		if !ok || got != want {
			t.Errorf("MetricFor(%d) = %q, %v; want %q, true", code, got, ok, want)
		}
	}
}

// An unknown code is skipped, never guessed at: EEA publishes hundreds of
// pollutant codes and we have a scale table for eight of them.
func TestMetricForRejectsUnknownCodes(t *testing.T) {
	if _, ok := eea.MetricFor(38); ok {
		t.Error("MetricFor accepted an unmapped pollutant code")
	}
}

// Every metric we map must be one the rest of the system already knows how to
// store, colour and chart.
func TestEveryMappedMetricIsCanonical(t *testing.T) {
	for _, code := range []int32{1, 5, 7, 8, 9, 10, 20, 6001} {
		m, _ := eea.MetricFor(code)
		if !upstream.IsCanonicalMetric(m) {
			t.Errorf("MetricFor(%d) = %q, which is not a canonical metric", code, m)
		}
	}
}

// CO is the one pollutant the agency publishes in mg/m³. Storing it unconverted
// would put a real reading a thousandfold below every band edge.
func TestNormaliseValueConvertsCO(t *testing.T) {
	got, err := eea.NormaliseValue("CO", 1.25, "mg.m-3")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1250 {
		t.Errorf("NormaliseValue = %v, want 1250", got)
	}
}

func TestNormaliseValuePassesMicrogramsThrough(t *testing.T) {
	got, err := eea.NormaliseValue("P1", 42.5, "ug.m-3")
	if err != nil {
		t.Fatal(err)
	}
	if got != 42.5 {
		t.Errorf("NormaliseValue = %v, want 42.5", got)
	}
}

// An unrecognised unit must error, not pass through: the scale tables assume
// µg/m³.
func TestNormaliseValueRejectsAnUnknownUnit(t *testing.T) {
	if _, err := eea.NormaliseValue("P1", 42.5, "ppb"); err == nil {
		t.Error("NormaliseValue accepted an unknown unit")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/eea/ -run 'TestMetricFor|TestNormalise|TestEveryMapped' -v`
Expected: FAIL — `undefined: eea.MetricFor`.

- [ ] **Step 3: Extend the canonical metric set**

In `internal/upstream/types.go`, replace the `canonicalMetrics` map with:

```go
// canonicalMetrics is the exact set stored; anything else upstream sends is
// dropped. The last six come only from internal/upstream/eea — no
// sensor.community device measures them.
var canonicalMetrics = map[string]bool{
	"P1":           true,
	"P2":           true,
	"temperature":  true,
	"humidity":     true,
	"pressure":     true,
	"noise_LAeq":   true,
	"noise_LA_max": true,
	"SO2":          true,
	"O3":           true,
	"NO2":          true,
	"NOX":          true,
	"CO":           true,
	"C6H6":         true,
}
```

- [ ] **Step 4: Write the implementation**

`internal/upstream/eea/pollutant.go`:

```go
package eea

import "fmt"

// metricForCode maps EEA pollutant vocabulary codes onto canonical metric
// names. PM10 and PM2.5 reuse the sensor.community names P1 and P2 so both
// networks share one scale table.
var metricForCode = map[int32]string{
	1:    "SO2",
	5:    "P1",
	7:    "O3",
	8:    "NO2",
	9:    "NOX",
	10:   "CO",
	20:   "C6H6",
	6001: "P2",
}

// MetricFor returns the canonical metric name for an EEA pollutant code, and
// false for the hundreds of codes we do not carry.
func MetricFor(code int32) (string, bool) {
	m, ok := metricForCode[code]
	return m, ok
}

// unitFactor converts an EEA unit into µg/m³, the unit every scale table in
// internal/api/scales.go is written against. CO arrives in mg.m-3.
var unitFactor = map[string]float64{
	"ug.m-3": 1,
	"mg.m-3": 1000,
}

// NormaliseValue converts a reading into µg/m³. An unrecognised unit returns
// an error rather than passing the value through unscaled.
func NormaliseValue(metric string, value float64, unit string) (float64, error) {
	f, ok := unitFactor[unit]
	if !ok {
		return 0, fmt.Errorf("eea: metric %s: unknown unit %q", metric, unit)
	}
	return value * f, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/... -v`
Expected: PASS. `TestEveryMappedMetricIsCanonical` is what catches a code mapped to a metric name the rest of the system does not know.

- [ ] **Step 6: Mutation-check**

Remove `"SO2": true` from `canonicalMetrics` — `TestEveryMappedMetricIsCanonical` must fail. Restore.
Change `"mg.m-3": 1000` to `1` — `TestNormaliseValueConvertsCO` must fail. Restore. Re-run, confirm PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git add internal/upstream/eea/pollutant.go internal/upstream/eea/pollutant_test.go
printf '%s\n' 'eea: map pollutant codes onto canonical metric names' '' '- 5 -> P1, 6001 -> P2: reuse the sensor.community names so both' '  networks share one scale table and one chart axis' '- 1/7/8/9/10/20 -> SO2/O3/NO2/NOX/CO/C6H6, new canonical metrics' '- unmapped codes are skipped; EEA publishes hundreds' '- NormaliseValue converts to ug/m3; CO arrives in mg.m-3' '- an unknown unit is an error, not a pass-through' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/upstream/eea/pollutant.go internal/upstream/eea/pollutant_test.go internal/upstream/types.go
```

---

### Task 4: Station metadata — the coordinate join

**Files:**
- Create: `internal/upstream/eea/metadata.go`, `internal/upstream/eea/metadata_test.go`
- Create: `internal/upstream/eea/testdata/metadata_extract.csv`

**Interfaces:**
- Consumes: nothing.
- Produces:
  ```go
  type Station struct {
      SamplingPoint string
      Code          string // AirQualityStationEoICode
      Name          string
      Lon, Lat      float64
      Type          string // AirQualityStationType
      Area          string // AirQualityStationArea
  }
  type Metadata map[string]Station // keyed by SamplingPoint
  func ParseMetadata(r io.Reader, countries []string) (Metadata, error)
  func (m Metadata) Lookup(samplingPoint string) (Station, bool)
  ```

Coordinates are not in the download API. They come from `https://discomap.eea.europa.eu/map/fme/metadata/PanEuropean_metadata.csv` — 26 MB, last modified 2024-03-11, frozen. All 141 current BG sampling points resolve in it (checked 2026-09-09). A station commissioned after March 2024 would arrive without coordinates; the collector logs and skips it rather than guessing.

- [ ] **Step 1: Build the test fixture**

Extract ten BG rows plus the header into `internal/upstream/eea/testdata/metadata_extract.csv`, including at least one non-BG row so the country filter has something to reject:

```
ctx_execute(language: "shell", code: `
cd /tmp && python3 - <<'PY'
import csv, itertools, urllib.request
url='https://discomap.eea.europa.eu/map/fme/metadata/PanEuropean_metadata.csv'
out=open('/tmp/metadata_extract.csv','w',newline='')
src=csv.reader(l.decode('utf-8','replace') for l in urllib.request.urlopen(url))
hdr=next(src); w=csv.writer(out); w.writerow(hdr)
i=hdr.index('Countrycode')
bg=[r for r in itertools.islice((r for r in src if r[i]=='BG'), 10)]
PY
`)
```

Write the resulting file to `internal/upstream/eea/testdata/metadata_extract.csv`. Note the exact header names you observe — the implementation below reads them by name, so a column renamed upstream is a decode error, not a silent zero coordinate.

- [ ] **Step 2: Write the failing test**

`internal/upstream/eea/metadata_test.go`:

```go
package eea_test

import (
	"os"
	"strings"
	"testing"

	"airbg.org/internal/upstream/eea"
)

func TestParseMetadataResolvesBulgarianSamplingPoints(t *testing.T) {
	f, err := os.Open("testdata/metadata_extract.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	md, err := eea.ParseMetadata(f, []string{"BG"})
	if err != nil {
		t.Fatalf("ParseMetadata: %v", err)
	}
	if len(md) == 0 {
		t.Fatal("no stations parsed")
	}

	for sp, st := range md {
		if !strings.HasPrefix(sp, "BG/") {
			t.Errorf("sampling point %q is not Bulgarian; the country filter leaked", sp)
		}
		if st.Code == "" {
			t.Errorf("%s has no EoI code", sp)
		}
		// Bulgaria's bounding box, generously drawn. A swapped lon/lat or a
		// decimal-comma parse lands well outside it.
		if st.Lon < 22 || st.Lon > 29 || st.Lat < 41 || st.Lat > 45 {
			t.Errorf("%s is at %v,%v — outside Bulgaria", sp, st.Lon, st.Lat)
		}
	}
}

func TestParseMetadataRejectsAMissingColumn(t *testing.T) {
	csv := "Countrycode,SamplingPoint\nBG,BG/SPO-BG0070A_06001_100\n"
	if _, err := eea.ParseMetadata(strings.NewReader(csv), []string{"BG"}); err == nil {
		t.Error("ParseMetadata accepted a header with no coordinate columns")
	}
}

func TestLookupMissesAnUnknownSamplingPoint(t *testing.T) {
	md := eea.Metadata{}
	if _, ok := md.Lookup("BG/SPO-NOPE"); ok {
		t.Error("Lookup claimed to know an absent sampling point")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/eea/ -run 'TestParseMetadata|TestLookup' -v`
Expected: FAIL — `undefined: eea.ParseMetadata`.

- [ ] **Step 4: Write the implementation**

`internal/upstream/eea/metadata.go`:

```go
package eea

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// Station is one reference station's identity and position.
type Station struct {
	SamplingPoint string
	Code          string
	Name          string
	Lon           float64
	Lat           float64
	Type          string
	Area          string
}

// Metadata is every station we can place, keyed by sampling point.
type Metadata map[string]Station

// requiredColumns are read by name; a rename upstream then fails the parse
// instead of yielding zero coordinates.
var requiredColumns = []string{
	"Countrycode", "SamplingPoint", "AirQualityStationEoICode",
	"AirQualityStationNatCode", "Longitude", "Latitude",
	"AirQualityStationType", "AirQualityStationArea",
}

// ParseMetadata reads the pan-European metadata CSV, keeping only the named
// countries. See README.md on why coordinates come from a separate, frozen file.
func ParseMetadata(r io.Reader, countries []string) (Metadata, error) {
	keep := make(map[string]bool, len(countries))
	for _, c := range countries {
		keep[c] = true
	}

	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("eea: metadata header: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[h] = i
	}
	for _, c := range requiredColumns {
		if _, ok := idx[c]; !ok {
			return nil, fmt.Errorf("eea: metadata is missing column %q", c)
		}
	}

	at := func(rec []string, col string) string {
		i := idx[col]
		if i >= len(rec) {
			return ""
		}
		return rec[i]
	}

	md := Metadata{}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("eea: metadata row: %w", err)
		}
		if !keep[at(rec, "Countrycode")] {
			continue
		}
		sp := at(rec, "SamplingPoint")
		if sp == "" {
			continue
		}
		lon, errLon := strconv.ParseFloat(at(rec, "Longitude"), 64)
		lat, errLat := strconv.ParseFloat(at(rec, "Latitude"), 64)
		if errLon != nil || errLat != nil {
			continue // no usable position; the collector counts the misses
		}
		md[sp] = Station{
			SamplingPoint: sp,
			Code:          at(rec, "AirQualityStationEoICode"),
			Name:          at(rec, "AirQualityStationNatCode"),
			Lon:           lon,
			Lat:           lat,
			Type:          at(rec, "AirQualityStationType"),
			Area:          at(rec, "AirQualityStationArea"),
		}
	}
	return md, nil
}

// Lookup resolves a sampling point to its station; the download API does not
// carry positions.
func (m Metadata) Lookup(samplingPoint string) (Station, bool) {
	st, ok := m[samplingPoint]
	return st, ok
}
```

If the fixture's header names differ from `requiredColumns`, correct `requiredColumns` and the `at()` calls to the observed names — the fixture is the authority, not this plan.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/eea/ -run 'TestParseMetadata|TestLookup' -v`
Expected: PASS.

- [ ] **Step 6: Mutation-check the country filter**

Change `if !keep[at(rec, "Countrycode")]` to `if false`, run — `TestParseMetadataResolvesBulgarianSamplingPoints` must fail on the `BG/` prefix assertion (the fixture must contain at least one non-BG row for this to bite; if it does not, add one). Restore.
Swap `Longitude` and `Latitude` in the two `at()` calls — the bounding-box assertion must fail. Restore. Re-run, confirm PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git add internal/upstream/eea/metadata.go internal/upstream/eea/metadata_test.go internal/upstream/eea/testdata/metadata_extract.csv
printf '%s\n' 'eea: resolve station coordinates from the metadata CSV' '' '- the download API returns readings only; positions come from' '  discomap PanEuropean_metadata.csv (26 MB, frozen 2024-03-11)' '- columns read by name; a missing required column is a decode error' '  rather than a station at 0,0' '- a sampling point with unparseable coordinates is skipped, not' '  defaulted; the collector counts them' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/upstream/eea/metadata.go internal/upstream/eea/metadata_test.go internal/upstream/eea/testdata/metadata_extract.csv
```

---

### Task 5: The HTTP client

**Files:**
- Create: `internal/upstream/eea/client.go`, `internal/upstream/eea/client_test.go`

**Interfaces:**
- Consumes: `config.EEA` (Task 6 defines it; this task defines it first and Task 6 wires it).
- Produces:
  ```go
  type Client struct{ … }
  func New(cfg config.EEA) *Client
  func (c *Client) FileURLs(ctx context.Context) ([]string, error)
  func (c *Client) FetchFile(ctx context.Context, url string, since time.Time) ([]byte, bool, error) // body, modified, err
  func (c *Client) FetchMetadata(ctx context.Context) (Metadata, error)
  ```

`FetchFile` returns `modified == false` on a 304 and an empty body. Every read is bounded by `cfg.MaxPayloadBytes`, for the reason `upstream.Client` bounds its own: a hostile or broken response must not exhaust memory.

- [ ] **Step 1: Write the failing test**

`internal/upstream/eea/client_test.go`:

```go
package eea_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/upstream/eea"
)

func testConfig(base, metadata string) config.EEA {
	return config.EEA{
		Enabled:         true,
		URL:             base,
		MetadataURL:     metadata,
		Countries:       []string{"BG"},
		RequestTimeout:  5 * time.Second,
		PollInterval:    time.Hour,
		MinPollInterval: 15 * time.Minute,
		MaxPayloadBytes: 1 << 20,
	}
}

func TestFileURLsPostsTheDocumentedBody(t *testing.T) {
	var got struct {
		Countries  []string `json:"countries"`
		Pollutants []string `json:"pollutants"`
		Dataset    int      `json:"dataset"`
		Source     string   `json:"source"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ParquetFile/urls" {
			t.Errorf("got %s %s, want POST /ParquetFile/urls", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte("https://example.invalid/a.parquet\nhttps://example.invalid/b.parquet\n"))
	}))
	defer srv.Close()

	urls, err := eea.New(testConfig(srv.URL, srv.URL)).FileURLs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 {
		t.Fatalf("got %d urls, want 2", len(urls))
	}
	if len(got.Countries) != 1 || got.Countries[0] != "BG" {
		t.Errorf("countries = %v, want [BG]", got.Countries)
	}
	// dataset 1 is UTD, the near-real-time set. 2 and 3 are the verified
	// archives, which are years behind.
	if got.Dataset != 1 {
		t.Errorf("dataset = %d, want 1", got.Dataset)
	}
}

func TestFetchFileReportsNotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Modified-Since") == "" {
			t.Error("no If-Modified-Since header; every unchanged file would be refetched")
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	body, modified, err := eea.New(testConfig(srv.URL, srv.URL)).
		FetchFile(context.Background(), srv.URL+"/a.parquet", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Error("a 304 was reported as modified")
	}
	if len(body) != 0 {
		t.Errorf("a 304 returned %d bytes of body", len(body))
	}
}

// The bound is the whole defence against a hostile or broken response: without
// it one oversized file is an out-of-memory kill of the whole server process.
func TestFetchFileBoundsTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	cfg := testConfig(srv.URL, srv.URL)
	cfg.MaxPayloadBytes = 1024
	body, modified, err := eea.New(cfg).FetchFile(context.Background(), srv.URL+"/a.parquet", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Fatal("a 200 was reported as not modified")
	}
	if len(body) > 1024 {
		t.Errorf("read %d bytes, want at most 1024", len(body))
	}
}

func TestFileURLsRejectsANonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := eea.New(testConfig(srv.URL, srv.URL)).FileURLs(context.Background()); err == nil {
		t.Error("FileURLs accepted a 502")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/eea/ -run 'TestFileURLs|TestFetchFile' -v`
Expected: FAIL — `undefined: config.EEA`, `undefined: eea.New`.

- [ ] **Step 3: Add the config type**

This is the minimum `config.EEA` the client needs; Task 6 adds the YAML plumbing around it. In `internal/config/resolve.go`, after the `Wind` struct:

```go
// EEA configures the official-station feed. See internal/upstream/eea/README.md.
type EEA struct {
	Enabled bool
	// URL is the download API base; MetadataURL is a different host on a much
	// longer refresh cycle.
	URL             string
	MetadataURL     string
	MetadataCache   string
	Countries       []string
	RequestTimeout  time.Duration
	PollInterval    time.Duration
	MinPollInterval time.Duration
	MetadataInterval time.Duration
	MaxPayloadBytes int64
}
```

and add `EEA EEA` to the `Config` struct after `Wind`.

- [ ] **Step 4: Write the implementation**

`internal/upstream/eea/client.go`:

```go
package eea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"airbg.org/internal/config"
)

const userAgent = "airbg.org collector (+https://airbg.org)"

// datasetUTD is the near-real-time set, ~1h behind. Datasets 2 and 3 are the
// verified archives and lag by years.
const datasetUTD = 1

type Client struct {
	cfg  config.EEA
	http *http.Client
}

func New(cfg config.EEA) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.RequestTimeout}}
}

type urlsRequest struct {
	Countries  []string `json:"countries"`
	Cities     []string `json:"cities"`
	Pollutants []string `json:"pollutants"`
	Dataset    int      `json:"dataset"`
	Source     string   `json:"source"`
}

// FileURLs returns the parquet file URLs for the configured countries. One
// request covers the whole country list.
func (c *Client) FileURLs(ctx context.Context) ([]string, error) {
	body, err := json.Marshal(urlsRequest{
		Countries:  c.cfg.Countries,
		Cities:     []string{},
		Pollutants: []string{},
		Dataset:    datasetUTD,
		Source:     "API",
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(c.cfg.URL, "/")+"/ParquetFile/urls", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eea: file urls: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("eea: file urls: status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("eea: file urls: read body: %w", err)
	}

	var urls []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "http") {
			urls = append(urls, line)
		}
	}
	return urls, nil
}

// FetchFile downloads one Parquet file. modified is false on a 304, where the
// body is empty and the caller keeps what it already stored.
func (c *Client) FetchFile(ctx context.Context, url string, since time.Time) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", userAgent)
	if !since.IsZero() {
		req.Header.Set("If-Modified-Since", since.UTC().Format(http.TimeFormat))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("eea: fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("eea: fetch file: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes))
	if err != nil {
		return nil, false, fmt.Errorf("eea: fetch file: read body: %w", err)
	}
	return body, true, nil
}

// FetchMetadata downloads and parses the coordinate CSV. It is 26 MB, so
// MaxPayloadBytes must be sized for it — see the validate rule in
// internal/config/validate.go.
func (c *Client) FetchMetadata(ctx context.Context) (Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.MetadataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eea: fetch metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("eea: fetch metadata: status %d", resp.StatusCode)
	}
	return ParseMetadata(io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes), c.cfg.Countries)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/upstream/eea/ -v`
Expected: PASS, all tests in the package.

- [ ] **Step 6: Mutation-check the bound and the conditional**

Replace `io.LimitReader(resp.Body, c.cfg.MaxPayloadBytes)` in `FetchFile` with `resp.Body` — `TestFetchFileBoundsTheBody` must fail. Restore.
Delete the `If-Modified-Since` header line — `TestFetchFileReportsNotModified` must fail. Restore. Re-run, confirm PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git add internal/upstream/eea/client.go internal/upstream/eea/client_test.go
printf '%s\n' 'eea: add the download API client' '' '- POST /ParquetFile/urls, dataset 1 (UTD near-real-time)' '- per-file GET with If-Modified-Since; a full pass is ~99 MB over 141' '  files, so an unchanged hour costs 141 x 304 instead' '- every response body read through io.LimitReader(MaxPayloadBytes)' '- non-200 is an error; 304 returns modified=false and an empty body' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/upstream/eea/client.go internal/upstream/eea/client_test.go internal/config/resolve.go
```

---

### Task 6: Configuration

**Files:**
- Modify: `internal/config/schema.go`, `internal/config/resolve.go`, `internal/config/validate.go`
- Modify: `airbg.yaml`, `deploy/.env.example`, and the Ansible role's `env.j2`
- Test: `internal/config/validate_test.go`, `internal/config/committed_config_test.go`

**Interfaces:**
- Consumes: `config.EEA` from Task 5.
- Produces: the `eea:` block, fully validated, reachable as `cfg.EEA`.

Editing the Ansible role is authorised — `env.j2` is half of every `AIRBG_*` key and the role aborts if the two files disagree.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/validate_test.go`:

```go
func TestEEAValidationRejectsBadSettings(t *testing.T) {
	for name, mutate := range map[string]func(*config.Config){
		"http url":            func(c *config.Config) { c.EEA.URL = "http://example.invalid" },
		"relative url":        func(c *config.Config) { c.EEA.URL = "/ParquetFile" },
		"no countries":        func(c *config.Config) { c.EEA.Countries = nil },
		"zero poll interval":  func(c *config.Config) { c.EEA.PollInterval = 0 },
		"poll under minimum":  func(c *config.Config) { c.EEA.PollInterval = time.Minute; c.EEA.MinPollInterval = time.Hour },
		"zero payload bound":  func(c *config.Config) { c.EEA.MaxPayloadBytes = 0 },
		"metadata url is http": func(c *config.Config) { c.EEA.MetadataURL = "http://example.invalid/x.csv" },
	} {
		t.Run(name, func(t *testing.T) {
			c := validConfig()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Errorf("Validate accepted %s", name)
			}
		})
	}
}

// The block is validated even when disabled, so an operator turning it on does
// not discover the settings are wrong at that moment.
func TestEEAIsValidatedWhenDisabled(t *testing.T) {
	c := validConfig()
	c.EEA.Enabled = false
	c.EEA.URL = "not a url at all"
	if err := c.Validate(); err == nil {
		t.Error("Validate skipped the eea block because it was disabled")
	}
}
```

`validConfig()` is the existing helper in that file; extend it with a valid `EEA` block matching `airbg.yaml`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/config/ -run TestEEA -v`
Expected: FAIL — `Validate accepted http url` etc., because nothing validates the block.

- [ ] **Step 3: Add the schema**

In `internal/config/schema.go`, add to `raw` after `Wind`:

```go
	EEA       *rawEEA       `yaml:"eea"`
```

and the struct after `rawWind`:

```go
// rawEEA configures the official-station feed. Shaped like rawWind: enabled is
// an explicit key, and the block is validated whether or not it is on.
type rawEEA struct {
	Enabled          *bool     `yaml:"enabled"`
	URL              *string   `yaml:"url"`
	MetadataURL      *string   `yaml:"metadata_url"`
	MetadataCache    *string   `yaml:"metadata_cache"`
	Countries        *[]string `yaml:"countries"`
	RequestTimeout   *Duration `yaml:"request_timeout"`
	PollInterval     *Duration `yaml:"poll_interval"`
	MinPollInterval  *Duration `yaml:"min_poll_interval"`
	MetadataInterval *Duration `yaml:"metadata_interval"`
	MaxPayloadBytes  *int64    `yaml:"max_payload_bytes"`
}
```

- [ ] **Step 4: Add the resolve**

In `internal/config/resolve.go`, after the `Wind:` block in the returned `Config`:

```go
		EEA: EEA{
			Enabled:          *r.EEA.Enabled,
			URL:              *r.EEA.URL,
			MetadataURL:      *r.EEA.MetadataURL,
			MetadataCache:    *r.EEA.MetadataCache,
			Countries:        *r.EEA.Countries,
			RequestTimeout:   r.EEA.RequestTimeout.Std(),
			PollInterval:     r.EEA.PollInterval.Std(),
			MinPollInterval:  r.EEA.MinPollInterval.Std(),
			MetadataInterval: r.EEA.MetadataInterval.Std(),
			MaxPayloadBytes:  *r.EEA.MaxPayloadBytes,
		},
```

- [ ] **Step 5: Add the validation**

In `internal/config/validate.go`, add `c.validateEEA(&p)` beside `c.validateWind(&p)`, and:

```go
// validateEEA runs whether or not the feed is enabled, so a bad setting fails
// at startup rather than when an operator switches it on.
func (c Config) validateEEA(p *problems) {
	for name, raw := range map[string]string{"eea.url": c.EEA.URL, "eea.metadata_url": c.EEA.MetadataURL} {
		u, err := url.Parse(raw)
		if err != nil {
			p.addf("%s = %q is not a URL: %v", name, raw, err)
			continue
		}
		if u.Scheme != "https" {
			p.addf("%s = %q must use https", name, raw)
		}
		if u.Host == "" {
			p.addf("%s = %q must be absolute", name, raw)
		}
	}

	if len(c.EEA.Countries) == 0 {
		p.addf("eea.countries must name at least one ISO 3166-1 alpha-2 code")
	}
	for _, code := range c.EEA.Countries {
		if !IsCountryCode(code) {
			p.addf("eea.countries contains %q, which is not an ISO 3166-1 alpha-2 code", code)
		}
	}

	p.positive("eea.request_timeout", c.EEA.RequestTimeout)
	p.positive("eea.poll_interval", c.EEA.PollInterval)
	p.positive("eea.min_poll_interval", c.EEA.MinPollInterval)
	p.positive("eea.metadata_interval", c.EEA.MetadataInterval)

	if c.EEA.PollInterval > 0 && c.EEA.MinPollInterval > 0 && c.EEA.PollInterval < c.EEA.MinPollInterval {
		p.addf("eea.poll_interval (%v) is below eea.min_poll_interval (%v); the agency's own cadence is hourly",
			c.EEA.PollInterval, c.EEA.MinPollInterval)
	}
	if c.EEA.MaxPayloadBytes <= 0 {
		p.addf("eea.max_payload_bytes must be positive, got %d", c.EEA.MaxPayloadBytes)
	}
	if c.EEA.MetadataCache == "" {
		p.addf("eea.metadata_cache must name a directory for the coordinate file")
	}
}
```

Use the existing country-code helper if it is named differently in `internal/config/country.go` — read that file and match its exported name.

- [ ] **Step 6: Add the block to airbg.yaml**

After the `wind:` block:

```yaml
# The official reference stations. See internal/upstream/eea/README.md.
eea:
  enabled: true
  url: "https://eeadmz1-downloads-api-appservice.azurewebsites.net"
  metadata_url: "https://discomap.eea.europa.eu/map/fme/metadata/PanEuropean_metadata.csv"
  metadata_cache: "/var/lib/airbg/eea"
  countries: ["BG"]
  request_timeout: "60s"
  poll_interval: "1h"
  min_poll_interval: "30m"
  metadata_interval: "168h"
  max_payload_bytes: 67108864
```

- [ ] **Step 7: Add the env keys to both files**

`deploy/.env.example` and the Ansible role's `env.j2` must both gain, in the same order:

```
AIRBG_EEA_ENABLED=
AIRBG_EEA_URL=
AIRBG_EEA_METADATA_URL=
AIRBG_EEA_METADATA_CACHE=
AIRBG_EEA_COUNTRIES=
AIRBG_EEA_REQUEST_TIMEOUT=
AIRBG_EEA_POLL_INTERVAL=
AIRBG_EEA_MIN_POLL_INTERVAL=
AIRBG_EEA_METADATA_INTERVAL=
AIRBG_EEA_MAX_PAYLOAD_BYTES=
```

Verify the derived names against `envName` in `internal/config/load.go` before writing them — the tag path is the authority, not this list.

- [ ] **Step 8: Run the whole config suite**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/config/ -v`
Expected: PASS, including `TestCommittedConfigLoads` and the missing-keys test, which is what catches a `raw` field with no `airbg.yaml` key.

- [ ] **Step 9: Mutation-check the poll floor**

Change `c.EEA.PollInterval < c.EEA.MinPollInterval` to `>`, run — the `poll under minimum` subtest must fail. Restore, re-run, confirm PASS.

- [ ] **Step 10: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
printf '%s\n' 'config: add the eea block' '' '- url, metadata_url, metadata_cache, countries, timeouts, intervals,' '  max_payload_bytes; matching AIRBG_EEA_* env keys in .env.example' '  and the role env.j2' '- min_poll_interval floors poll_interval; a mistyped interval would be' '  ~99 MB per tick against a third-party public service' '- the block is validated even when enabled=false' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/config airbg.yaml deploy/.env.example
```

Commit the Ansible role change separately, with its own paths, in whatever repo it lives in.

---

### Task 7: Store — writing stations and their readings

**Files:**
- Modify: `internal/store/store.go`, `internal/store/aggregate.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `eea.Station` (Task 4), `eea.Row` (Task 2).
- Produces:
  ```go
  type StationUpsert struct {
      SourceRef, Code, Name, Type, Area string
      Lon, Lat                          float64
      LastSeen                          time.Time
  }
  func (s *Store) UpsertStations(ctx context.Context, sts []StationUpsert) (map[string]int64, error)

  type StationReading struct {
      SensorID  int64
      Metric    string
      Value     float64
      Timestamp time.Time
      Quality   string
  }
  func (s *Store) WriteStationReadings(ctx context.Context, rs []StationReading) (int64, error)
  ```
  and `SensorReading` gains `Source, StationCode, StationName, StationType, StationArea string`.

`UpsertStations` returns sampling point → assigned `sensor_id`, so the caller can key readings without a second round trip.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/store_test.go`:

```go
func TestUpsertStationsAssignsStableIDs(t *testing.T) {
	ctx, pool := testsupport.MigratedPool(t)
	s := store.New(pool, testStoreConfig(), 5*time.Second)

	sts := []store.StationUpsert{{
		SourceRef: "BG/SPO-BG0070A_06001_100",
		Code:      "BG0070A", Name: "Пловдив Каменица",
		Type: "background", Area: "urban",
		Lon: 24.75, Lat: 42.14, LastSeen: time.Now().UTC().Truncate(time.Hour),
	}}

	first, err := s.UpsertStations(ctx, sts)
	if err != nil {
		t.Fatal(err)
	}
	id := first["BG/SPO-BG0070A_06001_100"]
	if id < 9_000_000_000 {
		t.Errorf("assigned id %d is outside the reserved official range", id)
	}

	// A second pass must reuse the id, not mint a new one. Without this the
	// station's whole reading history detaches on every collector cycle.
	second, err := s.UpsertStations(ctx, sts)
	if err != nil {
		t.Fatal(err)
	}
	if second["BG/SPO-BG0070A_06001_100"] != id {
		t.Errorf("second upsert assigned %d, want %d", second["BG/SPO-BG0070A_06001_100"], id)
	}

	var source, code string
	if err := pool.QueryRow(ctx,
		`SELECT source, station_code FROM sensor WHERE sensor_id = $1`, id).Scan(&source, &code); err != nil {
		t.Fatal(err)
	}
	if source != "eea" {
		t.Errorf("source = %q, want eea", source)
	}
	if code != "BG0070A" {
		t.Errorf("station_code = %q, want BG0070A", code)
	}
}

func TestWriteStationReadingsUpsertsOnRerun(t *testing.T) {
	ctx, pool := testsupport.MigratedPool(t)
	s := store.New(pool, testStoreConfig(), 5*time.Second)

	ids, err := s.UpsertStations(ctx, []store.StationUpsert{{
		SourceRef: "BG/SPO-BG0070A_00005_100", Code: "BG0070A", Name: "x",
		Lon: 24.75, Lat: 42.14, LastSeen: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := ids["BG/SPO-BG0070A_00005_100"]
	at := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)

	if _, err := s.WriteStationReadings(ctx, []store.StationReading{
		{SensorID: id, Metric: "P1", Value: 31.5, Timestamp: at, Quality: "ok"},
	}); err != nil {
		t.Fatal(err)
	}
	// The same hour re-fetched with a corrected value must overwrite, not
	// duplicate: the agency revises its own near-real-time data.
	if _, err := s.WriteStationReadings(ctx, []store.StationReading{
		{SensorID: id, Metric: "P1", Value: 33.0, Timestamp: at, Quality: "ok"},
	}); err != nil {
		t.Fatal(err)
	}

	var n int
	var v float64
	if err := pool.QueryRow(ctx,
		`SELECT count(*), max(value) FROM reading WHERE sensor_id = $1 AND metric = 'P1'`, id).Scan(&n, &v); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("got %d rows, want 1", n)
	}
	if v != 33.0 {
		t.Errorf("value = %v, want 33", v)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test ./internal/store/ -run 'TestUpsertStations|TestWriteStationReadings' -v`
Expected: FAIL — `undefined: store.StationUpsert`.

- [ ] **Step 3: Write the implementation**

Append to `internal/store/store.go`:

```go
// StationUpsert is one official reference station, keyed by its EEA sampling
// point.
type StationUpsert struct {
	SourceRef string
	Code      string
	Name      string
	Type      string
	Area      string
	Lon       float64
	Lat       float64
	LastSeen  time.Time
}

// UpsertStations records every station and returns sampling point -> sensor_id.
// source_ref is unique, so the conflict path returns the id assigned on first
// insert; the id must stay stable or the reading history detaches from it.
func (s *Store) UpsertStations(ctx context.Context, sts []StationUpsert) (map[string]int64, error) {
	ids := make(map[string]int64, len(sts))
	if len(sts) == 0 {
		return ids, nil
	}

	batch := &pgx.Batch{}
	for _, st := range sts {
		batch.Queue(
			`INSERT INTO sensor (sensor_id, sensor_type, location, last_seen,
			                     source, source_ref, station_code, station_name,
			                     station_type, station_area)
			 VALUES (nextval('official_sensor_id_seq'), 'eea_reference',
			         ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, $3,
			         'eea', $4, $5, $6, $7, $8)
			 ON CONFLICT (source_ref) WHERE source_ref IS NOT NULL DO UPDATE
			   SET location     = EXCLUDED.location,
			       last_seen    = EXCLUDED.last_seen,
			       station_code = EXCLUDED.station_code,
			       station_name = EXCLUDED.station_name,
			       station_type = EXCLUDED.station_type,
			       station_area = EXCLUDED.station_area,
			       active       = true
			 RETURNING sensor_id, source_ref`,
			st.Lon, st.Lat, st.LastSeen, st.SourceRef, st.Code, st.Name, st.Type, st.Area)
	}

	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range sts {
		var id int64
		var ref string
		if err := br.QueryRow().Scan(&id, &ref); err != nil {
			return nil, err
		}
		ids[ref] = id
	}
	return ids, nil
}

// StationReading is one hourly observation, already mapped to a canonical
// metric and normalised to µg/m³.
type StationReading struct {
	SensorID  int64
	Metric    string
	Value     float64
	Timestamp time.Time
	Quality   string
}

// WriteStationReadings persists hourly observations, flagged ones included.
// The UTD dataset is revised in place upstream, so a re-fetched hour
// overwrites.
func (s *Store) WriteStationReadings(ctx context.Context, rs []StationReading) (int64, error) {
	if len(rs) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	for _, r := range rs {
		batch.Queue(
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (sensor_id, metric, time) DO UPDATE
			   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
			r.Timestamp, r.SensorID, r.Metric, r.Value, r.Quality)
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return int64(len(rs)), nil
}
```

- [ ] **Step 4: Carry the source through reads**

In `internal/store/aggregate.go`, add to `SensorReading`:

```go
	// Source is "sensor.community" or "eea"; the map layer control filters on
	// it and the sensor panel displays it.
	Source string
	// StationCode, StationName, StationType and StationArea are the EEA
	// classification, empty for a sensor.community device.
	StationCode string
	StationName string
	StationType string
	StationArea string
```

Then extend `sensorsSelect` to project `s.source, s.station_code, s.station_name, s.station_type, s.station_area` and `scanSensorReadings` to scan them, using `COALESCE(s.station_code, '')` and so on so the scan target can stay a plain `string`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test ./internal/store/ -v`
Expected: PASS, whole package.

- [ ] **Step 6: Mutation-check id stability**

Change the conflict clause to `DO NOTHING` — `TestUpsertStationsAssignsStableIDs` must fail (no row returned). Change `nextval(...)` to a literal — the reserved-range assertion must fail. Restore both, re-run, confirm PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
printf '%s\n' 'store: add the official-station write path' '' '- UpsertStations keys on source_ref (the EEA sampling point) and' '  returns sampling point -> sensor_id; the id comes from' '  official_sensor_id_seq on first insert and is stable after, so the' '  reading history stays attached across cycles' '- WriteStationReadings upserts on (sensor_id, metric, time): the UTD' '  dataset is revised in place upstream' '- SensorReading carries source and the four station columns' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/store
```

---

### Task 8: The collector

**Files:**
- Create: `internal/upstream/eea/collector.go`, `internal/upstream/eea/collector_test.go`, `internal/upstream/eea/README.md`
- Modify: `cmd/airbg/main.go`

**Interfaces:**
- Consumes: `Client` (Task 5), `DecodeRows` (Task 2), `MetricFor`/`NormaliseValue` (Task 3), `Metadata` (Task 4), `store.UpsertStations`/`store.WriteStationReadings` (Task 7).
- Produces:
  ```go
  type Collector struct{ … }
  func NewCollector(cfg config.EEA, s *store.Store) *Collector
  func (c *Collector) RunOnce(ctx context.Context) (Stats, error)
  func (c *Collector) Loop(ctx context.Context)
  type Stats struct{ Files, Unmodified, Rows, Written, Unplaceable, Invalid, UnknownPollutant int }
  ```

`Unplaceable` counts sampling points absent from the metadata CSV — the frozen-file risk, made visible in a log line rather than left silent.

Quality mapping: `Validity > 0` → `"ok"`, otherwise `"source_invalid"`. `usableQuality` in `internal/store/aggregate.go` is `{"ok", "no_neighbours"}` and is deliberately left alone, so an invalid row is stored, charted as a gap, and never averaged.

- [ ] **Step 1: Write the failing test**

`internal/upstream/eea/collector_test.go` — the test drives the collector against a fake HTTP server serving the real fixture file, and a real database:

```go
package eea_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream/eea"
)

func TestRunOnceStoresStationsAndReadings(t *testing.T) {
	parquet, err := os.ReadFile("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile("testdata/metadata_extract.csv")
	if err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ParquetFile/urls":
			_, _ = w.Write([]byte(srv.URL + "/a.parquet\n"))
		case "/metadata.csv":
			_, _ = w.Write(metadata)
		default:
			_, _ = w.Write(parquet)
		}
	}))
	defer srv.Close()

	ctx, pool := testsupport.MigratedPool(t)
	s := store.New(pool, testsupport.StoreConfig(), 5*time.Second)

	cfg := testConfig(srv.URL, srv.URL+"/metadata.csv")
	cfg.MetadataCache = t.TempDir()
	cfg.MaxPayloadBytes = 64 << 20

	st, err := eea.NewCollector(cfg, s).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if st.Written == 0 {
		t.Fatal("no readings written")
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM sensor WHERE source = 'eea'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("no official stations recorded")
	}

	// Validity <= 0 rows are stored with quality 'source_invalid', not dropped,
	// so a rejected reading is distinguishable from a missing one.
	var invalid int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM reading WHERE quality = 'source_invalid'`).Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	if invalid == 0 {
		t.Error("no source_invalid rows; the fixture is known to carry Validity = -1 rows")
	}
}

// The metadata CSV is frozen at 2024-03-11, so a newer sampling point has no
// coordinates. It must be skipped and counted, not guessed at.
func TestRunOnceCountsUnplaceableSamplingPoints(t *testing.T) {
	parquet, err := os.ReadFile("testdata/spo_bg0070a_06001_100.parquet")
	if err != nil {
		t.Fatal(err)
	}

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ParquetFile/urls":
			_, _ = w.Write([]byte(srv.URL + "/a.parquet\n"))
		case "/metadata.csv":
			// Header only: parses cleanly, resolves no sampling point.
			_, _ = w.Write([]byte("Countrycode,SamplingPoint,AirQualityStationEoICode,AirQualityStationNatCode,Longitude,Latitude,AirQualityStationType,AirQualityStationArea\n"))
		default:
			_, _ = w.Write(parquet)
		}
	}))
	defer srv.Close()

	ctx, pool := testsupport.MigratedPool(t)
	s := store.New(pool, testsupport.StoreConfig(), 5*time.Second)

	cfg := testConfig(srv.URL, srv.URL+"/metadata.csv")
	cfg.MetadataCache = t.TempDir()
	cfg.MaxPayloadBytes = 64 << 20

	st, err := eea.NewCollector(cfg, s).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if st.Unplaceable == 0 {
		t.Error("an unplaceable sampling point was not counted")
	}
	if st.Written != 0 {
		t.Errorf("wrote %d readings for a station with no coordinates", st.Written)
	}
}
```

Add `testsupport.StoreConfig()` if it does not exist — read `internal/testsupport/` first and reuse whatever helper `internal/store/store_test.go` already uses.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test ./internal/upstream/eea/ -run TestRunOnce -v`
Expected: FAIL — `undefined: eea.NewCollector`.

- [ ] **Step 3: Write the implementation**

`internal/upstream/eea/collector.go`:

```go
package eea

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/store"
)

// Stats is one cycle's outcome. Each discard reason gets its own counter so a
// zero-reading cycle can be diagnosed from the log line alone.
type Stats struct {
	Files            int
	Unmodified       int
	Rows             int
	Written          int
	Unplaceable      int
	Invalid          int
	UnknownPollutant int
}

type Collector struct {
	cfg    config.EEA
	client *Client
	store  *store.Store
	clock  func() time.Time

	metadata      Metadata
	metadataAt    time.Time
	lastFileFetch map[string]time.Time
}

func NewCollector(cfg config.EEA, s *store.Store) *Collector {
	return &Collector{
		cfg:           cfg,
		client:        New(cfg),
		store:         s,
		clock:         time.Now,
		lastFileFetch: map[string]time.Time{},
	}
}

func (c *Collector) SetClockForTesting(clock func() time.Time) { c.clock = clock }

// metadataPath caches the 26 MB coordinate file between restarts. Upstream has
// not changed it since 2024-03-11.
func (c *Collector) metadataPath() string {
	return filepath.Join(c.cfg.MetadataCache, "PanEuropean_metadata.csv")
}

func (c *Collector) loadMetadata(ctx context.Context) error {
	now := c.clock().UTC()
	if c.metadata != nil && now.Sub(c.metadataAt) < c.cfg.MetadataInterval {
		return nil
	}

	md, err := c.client.FetchMetadata(ctx)
	if err != nil {
		if c.metadata != nil {
			// Station coordinates are static, so a stale copy is preferable to
			// an empty official layer.
			slog.Warn("eea metadata refresh failed, keeping the cached copy", "error", err)
			return nil
		}
		if cached, readErr := os.ReadFile(c.metadataPath()); readErr == nil {
			md, err = ParseMetadata(bytes.NewReader(cached), c.cfg.Countries)
			if err != nil {
				return err
			}
			c.metadata, c.metadataAt = md, now
			return nil
		}
		return err
	}

	c.metadata, c.metadataAt = md, now
	return nil
}

// RunOnce fetches, decodes and stores one pass over the configured countries.
func (c *Collector) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats

	if err := c.loadMetadata(ctx); err != nil {
		return st, err
	}

	urls, err := c.client.FileURLs(ctx)
	if err != nil {
		return st, err
	}

	stations := map[string]Station{}
	var rows []Row

	for _, u := range urls {
		st.Files++
		body, modified, err := c.client.FetchFile(ctx, u, c.lastFileFetch[u])
		if err != nil {
			slog.Warn("eea file fetch failed", "url", u, "error", err)
			continue
		}
		if !modified {
			st.Unmodified++
			continue
		}
		c.lastFileFetch[u] = c.clock().UTC()

		decoded, err := DecodeRows(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			slog.Warn("eea file decode failed", "url", u, "error", err)
			continue
		}
		rows = append(rows, decoded...)
	}
	st.Rows = len(rows)

	unplaceable := map[string]bool{}
	type keyed struct {
		row     Row
		station Station
	}
	usable := make([]keyed, 0, len(rows))

	for _, r := range rows {
		station, ok := c.metadata.Lookup(r.Samplingpoint)
		if !ok {
			unplaceable[r.Samplingpoint] = true
			continue
		}
		if _, ok := MetricFor(r.Pollutant); !ok {
			st.UnknownPollutant++
			continue
		}
		stations[r.Samplingpoint] = station
		usable = append(usable, keyed{row: r, station: station})
	}
	st.Unplaceable = len(unplaceable)
	for sp := range unplaceable {
		slog.Warn("eea sampling point has no coordinates in the metadata file", "sampling_point", sp)
	}

	if len(stations) == 0 {
		return st, nil
	}

	upserts := make([]store.StationUpsert, 0, len(stations))
	for sp, s := range stations {
		upserts = append(upserts, store.StationUpsert{
			SourceRef: sp,
			Code:      s.Code,
			Name:      s.Name,
			Type:      s.Type,
			Area:      s.Area,
			Lon:       s.Lon,
			Lat:       s.Lat,
			LastSeen:  c.clock().UTC(),
		})
	}
	ids, err := c.store.UpsertStations(ctx, upserts)
	if err != nil {
		return st, err
	}

	readings := make([]store.StationReading, 0, len(usable))
	for _, k := range usable {
		metric, _ := MetricFor(k.row.Pollutant)
		value, err := NormaliseValue(metric, k.row.Value, k.row.Unit)
		if err != nil {
			slog.Warn("eea reading has an unusable unit", "sampling_point", k.row.Samplingpoint, "error", err)
			continue
		}
		// Validity > 0 is EEA's own usable flag. Anything else is stored with a
		// quality that usableQuality excludes, so the hour reads as a gap.
		quality := "ok"
		if k.row.Validity <= 0 {
			quality = "source_invalid"
			st.Invalid++
		}
		readings = append(readings, store.StationReading{
			SensorID:  ids[k.row.Samplingpoint],
			Metric:    metric,
			Value:     value,
			Timestamp: k.row.Start.UTC(),
			Quality:   quality,
		})
	}

	n, err := c.store.WriteStationReadings(ctx, readings)
	if err != nil {
		return st, err
	}
	st.Written = int(n)
	return st, nil
}

// Loop runs RunOnce on the configured interval until ctx is done. A failed
// cycle is logged and the loop continues; the layer falls back to the last
// stored readings.
func (c *Collector) Loop(ctx context.Context) {
	run := func() {
		s, err := c.RunOnce(ctx)
		if err != nil {
			slog.Error("eea cycle failed", "error", err)
			return
		}
		slog.Info("eea cycle complete",
			"files", s.Files, "unmodified", s.Unmodified, "rows", s.Rows,
			"written", s.Written, "unplaceable", s.Unplaceable,
			"invalid", s.Invalid, "unknown_pollutant", s.UnknownPollutant)
	}

	run()
	t := time.NewTicker(c.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
```

- [ ] **Step 4: Wire it into main.go**

In `cmd/airbg/main.go`, in the `collect` case, beside the wind goroutine:

```go
		if cfg.EEA.Enabled {
			go eea.NewCollector(cfg.EEA, store.New(pool, cfg.Store, cfg.Database.StatementTimeouts.Series)).Loop(ctx)
		}
```

and in `serveCommand`, beside the wind loop:

```go
	// Hourly, matching EEA's own publish cadence. Shares the collector pool.
	eeaDone := make(chan struct{})
	if cfg.EEA.Enabled {
		ec := eea.NewCollector(cfg.EEA, collectorStore)
		go func() {
			defer close(eeaDone)
			ec.Loop(pollCtx)
		}()
	} else {
		close(eeaDone)
	}
```

with `<-eeaDone` added beside `<-windDone` at the end, and the import `"airbg.org/internal/upstream/eea"`.

- [ ] **Step 5: Write the README**

`internal/upstream/eea/README.md` carries the prose the source files deliberately do not: why the government portal is not the source (all 32 stations returned `value: 0` on 2026-09-09, and its TLS chain is incomplete), why coordinates come from a frozen separate file, the pollutant code table, the `Validity` vocabulary, and the PM2.5-at-4-of-32 gotcha. Copy the facts from `docs/superpowers/specs/2026-09-09-eea-official-stations-design.md` §1 and §8.

- [ ] **Step 6: Run the tests**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test ./internal/upstream/... ./cmd/... -v`
Expected: PASS.

- [ ] **Step 7: Mutation-check the validity mapping and the skip**

Change `if k.row.Validity <= 0` to `if false` — `TestRunOnceStoresStationsAndReadings` must fail on the `source_invalid` count. Restore.
Change the unplaceable branch to fall through instead of `continue` — `TestRunOnceCountsUnplaceableSamplingPoints` must fail on `st.Written != 0`. Restore. Re-run, confirm PASS.

- [ ] **Step 8: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git add internal/upstream/eea/collector.go internal/upstream/eea/collector_test.go internal/upstream/eea/README.md
printf '%s\n' 'eea: add the hourly collector and wire it into collect and serve' '' '- RunOnce: metadata -> file urls -> conditional fetch -> decode ->' '  UpsertStations -> WriteStationReadings' '- Stats counts each discard separately (unmodified, unplaceable,' '  invalid, unknown_pollutant) so an empty result is diagnosable' '- Validity <= 0 stores quality=source_invalid, which usableQuality' '  excludes; the row is kept so a rejected hour reads as a gap' '- metadata is cached on disk and refreshed on MetadataInterval; a' '  failed refresh keeps the previous copy' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/upstream/eea cmd/airbg/main.go
```

---

### Task 9: Scale tables for the five gases

**Files:**
- Modify: `internal/api/scales.go`
- Test: `internal/api/scales_test.go`

**Interfaces:**
- Consumes: the metric names from Task 3.
- Produces: EAQI `Scale` entries for `NO2`, `O3`, `SO2`, plus axis-only tables for `CO`, `C6H6`, `NOX`.

EAQI band edges, µg/m³ (source: `https://airindex.eea.europa.eu/`):

| | Good | Fair | Moderate | Poor | Very poor | Extremely poor |
|---|---|---|---|---|---|---|
| NO₂ | 40 | 90 | 120 | 230 | 340 | open |
| O₃ | 50 | 100 | 130 | 240 | 380 | open |
| SO₂ | 100 | 200 | 350 | 500 | 750 | open |

CO, C6H6 and NOX have no EAQI band set. They get axis-only tables with `Source: ""`, exactly as temperature/humidity/pressure do, and notes saying so in both languages.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/scales_test.go`:

```go
// A metric with no scale table gets no colour ramp and no unit.
func TestEveryCanonicalMetricHasAScale(t *testing.T) {
	have := map[string]bool{}
	for _, s := range api.Scales() {
		have[s.Metric] = true
	}
	for _, m := range upstream.CanonicalMetrics() {
		if !have[m] {
			t.Errorf("metric %q has no scale table", m)
		}
	}
}

func TestGasScalesCiteTheEAQI(t *testing.T) {
	want := map[string]float64{"NO2": 40, "O3": 50, "SO2": 100}
	for _, s := range api.Scales() {
		edge, ok := want[s.Metric]
		if !ok || s.Name != "eaqi" {
			continue
		}
		if s.Source != "https://airindex.eea.europa.eu/" {
			t.Errorf("%s eaqi cites %q", s.Metric, s.Source)
		}
		if s.Unit != "µg/m³" {
			t.Errorf("%s eaqi is in %q, want µg/m³", s.Metric, s.Unit)
		}
		if s.Bands[0].Upper == nil || *s.Bands[0].Upper != edge {
			t.Errorf("%s first band edge is not %v", s.Metric, edge)
		}
		delete(want, s.Metric)
	}
	for m := range want {
		t.Errorf("%s has no eaqi table", m)
	}
}

// An axis-only table must not claim a guideline it does not have.
func TestUnlegislatedGasesCiteNobody(t *testing.T) {
	for _, s := range api.Scales() {
		switch s.Metric {
		case "CO", "C6H6", "NOX":
			if s.Source != "" {
				t.Errorf("%s cites %q but has no guideline behind it", s.Metric, s.Source)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/api/ -run 'TestEveryCanonicalMetricHasAScale|TestGasScales|TestUnlegislatedGases' -v`
Expected: FAIL — `metric "SO2" has no scale table` and five siblings.

- [ ] **Step 3: Write the tables**

In `internal/api/scales.go`, extend the source comment at the top:

```go
//   - EAQI: European Environment Agency, European Air Quality Index bands for
//     PM10, PM2.5, NO2, O3 and SO2. The particulate bands are 24-hour running
//     means; the gas bands are hourly means, which is exactly the cadence the
//     official stations publish (internal/upstream/eea).
```

Then, inside `Scales()`, after `particulate`:

```go
	// EEA-only metrics. The EAQI publishes bands for three of the six.
	gasEAQI := func(metric string, edges [5]float64, notes, notesBG string) Scale {
		colours := [6]string{"#50f0e6", "#50ccaa", "#f0e641", "#ff5050", "#960032", "#7d2181"}
		labels := [6][2]string{
			{"Good", "Добро"}, {"Fair", "Задоволително"}, {"Moderate", "Умерено"},
			{"Poor", "Лошо"}, {"Very poor", "Много лошо"},
			{"Extremely poor", "Изключително лошо"},
		}
		bands := make([]Band, 0, 6)
		for i := range labels {
			var up *float64
			if i < len(edges) {
				up = upper(edges[i])
			}
			bands = append(bands, Band{
				Label: labels[i][0], LabelBG: labels[i][1], Upper: up, Colour: colours[i],
			})
		}
		// Headroom above the top stated edge so an off-scale reading still
		// varies visibly rather than clamping flat.
		ceiling := edges[4] * 1.5
		return Scale{
			Name: "eaqi", Metric: metric, Unit: "µg/m³", Bands: bands,
			Ceiling: &ceiling,
			Notes:   notes + " " + officialOnly,
			NotesBG: notesBG + " " + officialOnlyBG,
			Source:  "https://airindex.eea.europa.eu/",
		}
	}

	gases := []Scale{
		gasEAQI("NO2", [5]float64{40, 90, 120, 230, 340},
			"European Air Quality Index bands for nitrogen dioxide, hourly mean.",
			"Класове на Европейския индекс за качество на въздуха за азотен диоксид, часова средна стойност."),
		gasEAQI("O3", [5]float64{50, 100, 130, 240, 380},
			"European Air Quality Index bands for ozone, hourly mean.",
			"Класове на Европейския индекс за качество на въздуха за озон, часова средна стойност."),
		gasEAQI("SO2", [5]float64{100, 200, 350, 500, 750},
			"European Air Quality Index bands for sulphur dioxide, hourly mean.",
			"Класове на Европейския индекс за качество на въздуха за серен диоксид, часова средна стойност."),
	}

	// No index publishes bands for these three. Axis-only tables give the
	// metric a unit and the map a ramp, and cite no source, like the meteo
	// tables.
	axisOnly := []Scale{
		{Name: "axis", Metric: "CO", Unit: "µg/m³", Ceiling: upper(10000),
			Bands: []Band{
				{Label: "Lower", LabelBG: "По-ниско", Upper: upper(4000), Colour: "#50ccaa"},
				{Label: "Higher", LabelBG: "По-високо", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "Carbon monoxide has no European Air Quality Index band set; this is an axis, not a health scale. " + officialOnly,
			NotesBG: "За въглероден оксид няма класове по Европейския индекс за качество на въздуха; това е скала, а не здравна оценка. " + officialOnlyBG},
		{Name: "axis", Metric: "C6H6", Unit: "µg/m³", Ceiling: upper(20),
			Bands: []Band{
				{Label: "Lower", LabelBG: "По-ниско", Upper: upper(5), Colour: "#50ccaa"},
				{Label: "Higher", LabelBG: "По-високо", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "Benzene has no European Air Quality Index band set; this is an axis, not a health scale. " + officialOnly,
			NotesBG: "За бензен няма класове по Европейския индекс за качество на въздуха; това е скала, а не здравна оценка. " + officialOnlyBG},
		{Name: "axis", Metric: "NOX", Unit: "µg/m³", Ceiling: upper(500),
			Bands: []Band{
				{Label: "Lower", LabelBG: "По-ниско", Upper: upper(100), Colour: "#50ccaa"},
				{Label: "Higher", LabelBG: "По-високо", Upper: nil, Colour: "#ff5050"},
			},
			Notes:   "Nitrogen oxides as NO2 have no European Air Quality Index band set; this is an axis, not a health scale. " + officialOnly,
			NotesBG: "За азотни оксиди като NO2 няма класове по Европейския индекс за качество на въздуха; това е скала, а не здравна оценка. " + officialOnlyBG},
	}
```

with these consts beside `indicative`:

```go
	const officialOnly = "Measured only at official reference stations."
	const officialOnlyBG = "Измерва се само в официалните референтни станции."
```

Append `gases` and `axisOnly` to the slice `Scales()` returns, wherever `particulate` is appended.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/api/ -v`
Expected: PASS. `TestEveryCanonicalMetricHasAScale` is the one that stops a future metric shipping with no table.

- [ ] **Step 5: Mutation-check**

Change the NO₂ first edge from 40 to 45 — `TestGasScalesCiteTheEAQI` must fail. Restore.
Give the CO table `Source: "https://example.invalid"` — `TestUnlegislatedGasesCiteNobody` must fail. Restore. Re-run, confirm PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
printf '%s\n' 'api: add scale tables for the six gas metrics' '' '- NO2, O3 and SO2 get EAQI hourly-mean bands, cited to' '  airindex.eea.europa.eu' '- CO, C6H6 and NOX have no published band set; they get axis-only' '  tables with an empty Source, as the meteo tables do' '- new test asserts every canonical metric has a table: without one the' '  map paints a flat ramp and the panel prints a unitless number' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/api/scales.go internal/api/scales_test.go
```

---

### Task 10: The source reaches the client

**Files:**
- Modify: `internal/snapshot/build.go`
- Modify: `web/src/lib/stations.js`
- Test: `internal/snapshot/build_test.go`, `web/src/lib/__tests__/stations.test.js`

**Interfaces:**
- Consumes: `store.SensorReading.Source` and the station fields (Task 7).
- Produces: `source`, `station_code`, `station_name`, `station_type`, `station_area` columns in the `/api/v1/area/{slug}/sensors` payload, and those five names added to `META_COLUMNS` in `stations.js`.

The `META_COLUMNS` addition is load-bearing: `stations.js` derives the metric list by exclusion from that set, so a new non-metric column that is not listed would be presented to the reader as a metric.

- [ ] **Step 1: Write the failing tests**

Append to `internal/snapshot/build_test.go`:

```go
func TestSensorPayloadCarriesTheSource(t *testing.T) {
	body := sensorPayloadFrom(time.Now().UTC(), []store.SensorReading{
		{SensorID: 1, SensorType: "SDS011", Lon: 23.3, Lat: 42.7, Quality: "ok",
			Source: "sensor.community", Values: map[string]float64{"P1": 20}},
		{SensorID: 9_000_000_001, SensorType: "eea_reference", Lon: 24.75, Lat: 42.14, Quality: "ok",
			Source: "eea", StationCode: "BG0070A", StationName: "Пловдив Каменица",
			StationType: "background", StationArea: "urban",
			Values: map[string]float64{"P1": 31.5}},
	})

	raw, err := json.Marshal(body.Sensors)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"source", "station_code", "station_name", "station_type", "station_area"} {
		if _, ok := got[col]; !ok {
			t.Errorf("the payload has no %q column", col)
		}
	}

	var sources []string
	if err := json.Unmarshal(got["source"], &sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[0] != "sensor.community" || sources[1] != "eea" {
		t.Errorf("source column = %v, want [sensor.community eea]", sources)
	}
}
```

Append to `web/src/lib/__tests__/stations.test.js`:

```js
import { META_COLUMNS } from '../stations.js'

// stations.js derives the metric list by EXCLUSION from this set. A non-metric
// column missing from it is offered to the reader as if it were a reading.
it('knows the source columns are not metrics', () => {
  for (const col of ['source', 'station_code', 'station_name', 'station_type', 'station_area']) {
    expect(META_COLUMNS.has(col)).toBe(true)
  }
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/snapshot/ -run TestSensorPayloadCarriesTheSource -v
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && npm --prefix web test -- stations
```
Expected: both FAIL.

- [ ] **Step 3: Extend the payload**

In `internal/snapshot/build.go`, add to `sensorColumns`:

```go
	// Source is "sensor.community" or "eea", one per row, len(ID).
	Source []string `json:"source"`
	// EEA classification, empty strings for a sensor.community device. Each
	// len(ID).
	StationCode []string `json:"station_code"`
	StationName []string `json:"station_name"`
	StationType []string `json:"station_type"`
	StationArea []string `json:"station_area"`
```

add the five to the `MarshalJSON` map, allocate them in `sensorPayloadFrom`'s `cols` literal (`make([]string, 0, n)` each), and append `sr.Source`, `sr.StationCode`, `sr.StationName`, `sr.StationType`, `sr.StationArea` in the loop.

- [ ] **Step 4: Extend META_COLUMNS**

In `web/src/lib/stations.js`:

```js
export const META_COLUMNS = new Set([
  'id', 'type', 'lon', 'lat', 'quality', 'station', 'measures', 'first_seen', 'last_seen',
  'source', 'station_code', 'station_name', 'station_type', 'station_area',
])
```

and extend that file's header comment block by one line naming `source` as the network the row came from.

- [ ] **Step 5: Run tests to verify they pass**

Run both commands from Step 2. Expected: PASS.

- [ ] **Step 6: Mutation-check**

Remove `'source'` from `META_COLUMNS` — the Vitest case must fail. Restore.
Remove `"source": c.Source` from `MarshalJSON` — the Go test must fail. Restore. Re-run both, confirm PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
printf '%s\n' 'web: carry source and station columns in the sensor payload' '' '- sensorColumns gains source, station_code, station_name,' '  station_type, station_area, all len(ID)' '- stations.js derives metrics by exclusion from META_COLUMNS, so the' '  five names are added there too or they render as metrics' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/snapshot web/src/lib/stations.js web/src/lib/__tests__/stations.test.js
```

---

### Task 11: The layer control

**Files:**
- Create: `web/src/lib/sourcefilter.svelte.js`, `web/src/lib/__tests__/sourcefilter.test.js`
- Modify: `web/src/islands/map.js`
- Modify: `internal/web/templates/base.gohtml`, `internal/i18n/en.json`, `internal/i18n/bg.json`

**Interfaces:**
- Consumes: the `source` column (Task 10), `upstream.CanonicalMetrics` (Task 3).
- Produces:
  ```js
  export const SOURCES = ['sensor.community', 'eea']
  export function getSources()                      // -> Set<string>
  export function setSourceEnabled(source, on)      // -> void
  export function onSourceChange(fn)                // -> unsubscribe
  export function filterBySource(features, sources) // -> features
  export function measuredBy(source, metric)        // -> boolean
  export function resetSourceFilterForTests()
  ```

`measuredBy` is what implements the spec's "auto-hide, and say why": a metric no citizen device measures greys the community box out. It is a pure function over a table, testable without a map.

- [ ] **Step 1: Write the failing test**

`web/src/lib/__tests__/sourcefilter.test.js`:

```js
import { beforeEach, describe, expect, it } from 'vitest'
import {
  SOURCES, filterBySource, getSources, measuredBy, onSourceChange,
  resetSourceFilterForTests, setSourceEnabled,
} from '../sourcefilter.svelte.js'

beforeEach(() => resetSourceFilterForTests())

describe('the source filter', () => {
  it('starts with every source on', () => {
    expect([...getSources()].sort()).toEqual([...SOURCES].sort())
  })

  it('drops the features of a source that is off', () => {
    const features = [
      { properties: { source: 'sensor.community' } },
      { properties: { source: 'eea' } },
    ]
    setSourceEnabled('eea', false)
    const got = filterBySource(features, getSources())
    expect(got).toHaveLength(1)
    expect(got[0].properties.source).toBe('sensor.community')
  })

  // Snapshots written before the source column exists have no source.
  it('treats an absent source as sensor.community', () => {
    const features = [{ properties: {} }]
    expect(filterBySource(features, new Set(['sensor.community']))).toHaveLength(1)
    expect(filterBySource(features, new Set(['eea']))).toHaveLength(0)
  })

  it('notifies subscribers', () => {
    let seen = null
    onSourceChange((s) => { seen = s })
    setSourceEnabled('eea', false)
    expect(seen && seen.has('eea')).toBe(false)
  })
})

describe('what each network measures', () => {
  it('knows the gases are official-only', () => {
    for (const metric of ['SO2', 'O3', 'NO2', 'NOX', 'CO', 'C6H6']) {
      expect(measuredBy('eea', metric)).toBe(true)
      expect(measuredBy('sensor.community', metric)).toBe(false)
    }
  })

  it('knows the weather metrics are citizen-only', () => {
    for (const metric of ['temperature', 'humidity', 'pressure', 'noise_LAeq', 'noise_LA_max']) {
      expect(measuredBy('sensor.community', metric)).toBe(true)
      expect(measuredBy('eea', metric)).toBe(false)
    }
  })

  it('knows both networks measure particulates', () => {
    for (const metric of ['P1', 'P2']) {
      expect(measuredBy('sensor.community', metric)).toBe(true)
      expect(measuredBy('eea', metric)).toBe(true)
    }
  })

  it('assumes an unknown metric is measured by everyone', () => {
    expect(measuredBy('eea', 'brand_new')).toBe(true)
    expect(measuredBy('sensor.community', 'brand_new')).toBe(true)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && npm --prefix web test -- sourcefilter`
Expected: FAIL — module not found.

- [ ] **Step 3: Write the implementation**

`web/src/lib/sourcefilter.svelte.js`:

```js
// Network filter for the sensor tier. A filter, not a second MapLibre source:
// islands/map.js paints one source ('airbg-data') for both the area tiers and
// the sensor tier, and repaintSensors redraws from state.sensorBody without a
// refetch. Module-level $state, not viewstate.svelte.js — that file mirrors the
// URL hash and this is not in the hash. Mirrors sensorfilter.svelte.js.
export const SOURCES = ['sensor.community', 'eea']

export const DEFAULT_SOURCES = SOURCES

let sources = $state(new Set(DEFAULT_SOURCES))

const listeners = new Set()

export function getSources() {
  return sources
}

export function setSourceEnabled(source, on) {
  if (!SOURCES.includes(source)) return
  const next = new Set(sources)
  if (on) next.add(source)
  else next.delete(source)
  if (next.size === sources.size) return
  sources = next
  for (const fn of listeners) fn(sources)
}

export function onSourceChange(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

// TEST-ONLY reset seam — see sensorfilter.svelte.js's own.
export function resetSourceFilterForTests() {
  sources = new Set(DEFAULT_SOURCES)
  listeners.clear()
}

// A snapshot written before the source column exists carries no source; those
// rows are all sensor.community.
function sourceOf(feature) {
  return feature.properties.source || 'sensor.community'
}

export function filterBySource(features, enabled) {
  return features.filter((f) => enabled.has(sourceOf(f)))
}

// Metric coverage per network as of 2026-09-09. A metric in neither set is
// treated as measured by both, so a metric added server-side does not blank a
// layer until this table is updated.
const MEASURED = {
  'sensor.community': new Set([
    'P1', 'P2', 'temperature', 'humidity', 'pressure', 'noise_LAeq', 'noise_LA_max',
  ]),
  eea: new Set(['P1', 'P2', 'SO2', 'O3', 'NO2', 'NOX', 'CO', 'C6H6']),
}

// measuredBy reports whether source has any data for metric; the layer control
// disables the checkbox when it does not.
export function measuredBy(source, metric) {
  const known = new Set([...MEASURED['sensor.community'], ...MEASURED.eea])
  if (!known.has(metric)) return true
  return MEASURED[source]?.has(metric) === true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && npm --prefix web test -- sourcefilter`
Expected: PASS.

- [ ] **Step 5: Add the two view toggles**

In `web/src/islands/map.js`, import beside the sensorfilter import:

```js
import {
  filterBySource, getSources, measuredBy, onSourceChange, setSourceEnabled,
} from '../lib/sourcefilter.svelte.js'
```

Build the two views beside `windView` and `boundaryView`:

```js
    // One toggle per network, both on by default (no defaultOff).
    // setSourceViewAvailability disables the one that has no data for the
    // selected metric.
    const sourceViews = [
      {
        id: 'communitySensors',
        label: cfg.t.viewCommunitySensors,
        apply: (on) => { setSourceEnabled('sensor.community', on); return on },
      },
      {
        id: 'officialStations',
        label: cfg.t.viewOfficialStations,
        apply: (on) => { setSourceEnabled('eea', on); return on },
      },
    ]
```

and pass them in:

```js
      views: [...chrome.layerViews, ...sourceViews, windView, boundaryView],
```

Then add the two functions, near `repaintSensors`:

```js
// setSourceViewAvailability disables the checkbox of a network that has no data
// for metric and appends t.notMeasured to its label. The five gases exist only
// at EEA stations, the weather metrics only on sensor.community devices.
export function setSourceViewAvailability(chrome, metric, t) {
  for (const [id, source] of [['communitySensors', 'sensor.community'], ['officialStations', 'eea']]) {
    const input = chrome.layersUI?.fieldset?.querySelector(`[data-layer-key="view:${id}"]`)
    if (!input) continue
    const measures = measuredBy(source, metric)
    input.disabled = !measures
    const span = input.parentElement?.querySelector('span')
    if (span) span.textContent = measures ? t[id] : `${t[id]} — ${t.notMeasured}`
  }
}
```

Extend `repaintSensors` and the two `sensorFeatures` call sites to compose both filters:

```js
  const features = filterBySource(
    filterByStatus(
      sensorFeatures(state.sensorBody, cfg.metric, state.scales, cfg.noDataColour),
      getSensorStatus(),
    ),
    getSources(),
  )
```

Register `onSourceChange(() => repaintSensors(map, state, cfg))` wherever `onSensorStatusChange` is registered, and call `setSourceViewAvailability(chrome, cfg.metric, cfg.t)` from `onMetricChange` and once at mount.

- [ ] **Step 6: Give the official stations their own shape**

In the marker paint, distinguish the two by outline width. Fill colour already encodes the reading and cannot carry a second meaning.

In `layerPaint(cfg)`, replace the fixed `'circle-stroke-width'` with:

```js
    // Stroke width marks the network; fill stays the reading's scale colour.
    'circle-stroke-width': ['case', ['==', ['get', 'source'], 'eea'], 3, 1],
```

and add `source` to the properties `sensorFeatures` emits, reading it from the `source` column with `'sensor.community'` as the fallback.

- [ ] **Step 7: Add the copy**

`internal/web/templates/base.gohtml`, inside `mapLayerLabels`:

```
     data-t-view-community-sensors="{{.T "map.view.community_sensors"}}"
     data-t-view-official-stations="{{.T "map.view.official_stations"}}"
     data-t-not-measured="{{.T "map.view.not_measured"}}"
```

`internal/i18n/en.json`:

```json
  "map.view.community_sensors": "Citizen sensors",
  "map.view.official_stations": "Official stations",
  "map.view.not_measured": "does not measure this",
  "metric.C6H6": "Benzene",
  "metric.CO": "Carbon monoxide",
  "metric.NO2": "Nitrogen dioxide",
  "metric.NOX": "Nitrogen oxides",
  "metric.O3": "Ozone",
  "metric.SO2": "Sulphur dioxide",
```

`internal/i18n/bg.json`, the same keys:

```json
  "map.view.community_sensors": "Граждански сензори",
  "map.view.official_stations": "Официални станции",
  "map.view.not_measured": "не измерва това",
  "metric.C6H6": "Бензен",
  "metric.CO": "Въглероден оксид",
  "metric.NO2": "Азотен диоксид",
  "metric.NOX": "Азотни оксиди",
  "metric.O3": "Озон",
  "metric.SO2": "Серен диоксид",
```

Read the `cfg.t` construction in `map.js` and add `viewCommunitySensors`, `viewOfficialStations`, `notMeasured`, `communitySensors` and `officialStations` to it from the new dataset keys, matching the dataset-to-camelCase convention that file already uses.

- [ ] **Step 8: Run the full frontend suite**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && npm --prefix web test && go test ./internal/web/ ./internal/i18n/ -v`
Expected: PASS. `internal/web`'s `template_keys_test.go` is what catches a `data-t-*` attribute with no catalogue entry, and `internal/i18n` is what catches en/bg drift.

- [ ] **Step 9: Mutation-check**

Change `measuredBy` to `return true` unconditionally — the two coverage cases in `sourcefilter.test.js` must fail. Restore.
Delete `"map.view.not_measured"` from `bg.json` — the i18n parity test must fail. Restore. Re-run, confirm PASS.

- [ ] **Step 10: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git checkout internal/web/dist/.keep
git add web/src/lib/sourcefilter.svelte.js web/src/lib/__tests__/sourcefilter.test.js
printf '%s\n' 'web: add a per-network layer toggle' '' '- sourcefilter.svelte.js mirrors sensorfilter.svelte.js: a pure filter' '  over the features already in state.sensorBody, so a toggle repaints' '  and does not refetch' '- two installLayers views, both on by default' '- measuredBy() greys the box for a network that does not measure the' '  selected metric and appends the reason to the label; five gases are' '  EEA-only, four meteo metrics are community-only' '- official markers get circle-stroke-width 3; fill still encodes value' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- web/src internal/web/templates/base.gohtml internal/i18n
```

---

### Task 12: The panel names the station

**Files:**
- Modify: `web/src/components/SensorPanel.svelte`
- Test: `web/src/components/__tests__/SensorPanel.test.js`

**Interfaces:**
- Consumes: the station columns (Task 10).
- Produces: the panel showing station name, EEA code, type and area for an official station, and the network for every sensor.

- [ ] **Step 1: Write the failing test**

Append to `web/src/components/__tests__/SensorPanel.test.js`:

```js
it('names an official station rather than a number', async () => {
  const { getByText } = render(SensorPanel, {
    props: sensorPanelProps({
      id: 9000000001,
      source: 'eea',
      station_name: 'Пловдив Каменица',
      station_code: 'BG0070A',
      station_type: 'background',
      station_area: 'urban',
    }),
  })
  expect(getByText(/Пловдив Каменица/)).toBeTruthy()
  expect(getByText(/BG0070A/)).toBeTruthy()
})

it('says which network the reading came from', async () => {
  const { getByText } = render(SensorPanel, {
    props: sensorPanelProps({ id: 1, source: 'sensor.community' }),
  })
  expect(getByText(/sensor\.community/)).toBeTruthy()
})

it('shows no station fields for a citizen device', async () => {
  const { queryByText } = render(SensorPanel, {
    props: sensorPanelProps({ id: 1, source: 'sensor.community', station_code: '' }),
  })
  expect(queryByText(/background/)).toBeNull()
})
```

Match `sensorPanelProps` to the existing helper in that file; if there is none, build the props inline the way the neighbouring tests do.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && npm --prefix web test -- SensorPanel`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**

In `SensorPanel.svelte`, replace the heading's sensor-id text with the station name where there is one, and add a metadata block:

```svelte
<h2>{sensor.station_name || `${t.sensorLabel} ${sensor.id}`}</h2>

{#if sensor.source === 'eea'}
  <!-- EEA classification: traffic/industrial/background, urban/suburban/rural. -->
  <dl class="panel__meta">
    <dt>{t.stationCode}</dt><dd>{sensor.station_code}</dd>
    {#if sensor.station_type}<dt>{t.stationType}</dt><dd>{sensor.station_type}</dd>{/if}
    {#if sensor.station_area}<dt>{t.stationArea}</dt><dd>{sensor.station_area}</dd>{/if}
  </dl>
{/if}

<p class="panel__source">{t.network}: {sensor.source || 'sensor.community'}</p>
```

Add `panel.station_code`, `panel.station_type`, `panel.station_area` and `panel.network` to both catalogues and to the panel island's `data-t-*` attributes in `base.gohtml`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && npm --prefix web test && go test ./internal/web/ ./internal/i18n/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git checkout internal/web/dist/.keep
printf '%s\n' 'web: show station identity in the sensor panel' '' '- heading uses station_name when present; the synthetic 9e9 sensor id' '  is not a useful label' '- official stations show EoI code, station type and area' '- every sensor shows which network published the reading' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- web/src/components internal/web/templates/base.gohtml internal/i18n
```

---

### Task 13: Attribution under the map

**Files:**
- Modify: `internal/api/overview.go`, `internal/web/templates/base.gohtml`, `internal/i18n/en.json`, `internal/i18n/bg.json`
- Test: `internal/api/overview_test.go`, `internal/web/render_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Attribution struct { Source, Text, URL string }`, `func Attributions() []Attribution`, and `metaBody.Attributions []Attribution` replacing `metaBody.Attribution string`.

`DataAttribution` is removed. `BoundaryAttribution` becomes one entry in the list. This is a wire-format change to `/api/v1/meta`; nothing in `web/src` currently reads `attribution` (grep confirmed), so no client needs updating — but check again before deleting the field.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/overview_test.go`:

```go
func TestAttributionsNameBothNetworksAndTheirMaps(t *testing.T) {
	got := api.Attributions()

	want := map[string]string{
		"sensor.community": "https://maps.sensor.community/",
		"eea":              "https://eea.government.bg/kav/",
	}
	seen := map[string]bool{}
	for _, a := range got {
		if url, ok := want[a.Source]; ok {
			if a.URL != url {
				t.Errorf("%s links to %q, want %q", a.Source, a.URL, url)
			}
			seen[a.Source] = true
		}
		if a.Text == "" {
			t.Errorf("attribution %q has no text", a.Source)
		}
	}
	for s := range want {
		if !seen[s] {
			t.Errorf("no attribution for %s", s)
		}
	}
}

// ODbL 1.0 and the EEA reuse terms both require attribution.
func TestEveryIngestedSourceIsCredited(t *testing.T) {
	credited := map[string]bool{}
	for _, a := range api.Attributions() {
		credited[a.Source] = true
	}
	for _, s := range []string{"sensor.community", "eea", "openstreetmap"} {
		if !credited[s] {
			t.Errorf("%s is used and not credited", s)
		}
	}
}
```

Append to `internal/web/render_test.go`:

```go
func TestFooterCreditsTheOfficialProgramme(t *testing.T) {
	page := renderIndex(t)
	for _, want := range []string{"eea.government.bg/kav", "maps.sensor.community"} {
		if !strings.Contains(page, want) {
			t.Errorf("the footer does not link to %s", want)
		}
	}
}
```

Match `renderIndex` to whatever helper that file already uses.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./internal/api/ ./internal/web/ -run 'TestAttributions|TestEveryIngestedSource|TestFooterCredits' -v`
Expected: FAIL — `undefined: api.Attributions`.

- [ ] **Step 3: Write the implementation**

In `internal/api/overview.go`, replace the two constants:

```go
// Attribution is one credited data source and the URL its licence requires.
type Attribution struct {
	Source string `json:"source"`
	Text   string `json:"text"`
	URL    string `json:"url"`
}

// Attributions lists every ingested source. ODbL 1.0 and the EEA reuse terms
// both require the credit, so this must stay in step with the collectors.
func Attributions() []Attribution {
	return []Attribution{
		{
			Source: "sensor.community",
			Text:   "Citizen data from sensor.community contributors, ODbL 1.0",
			URL:    "https://maps.sensor.community/",
		},
		{
			Source: "eea",
			Text: "Official data from the Executive Environment Agency (ИАОС) " +
				"via the European Environment Agency's air quality programme",
			URL: "https://eea.government.bg/kav/",
		},
		{
			Source: "openstreetmap",
			Text:   "Boundaries © OpenStreetMap contributors, ODbL 1.0",
			URL:    "https://www.openstreetmap.org/copyright",
		},
	}
}
```

Change `metaBody`'s two attribution fields to `Attributions []Attribution \`json:"attributions"\`` and set it from `Attributions()`. Fix every compile error the removal of the two constants produces.

- [ ] **Step 4: Render it in the footer**

In `base.gohtml`, replace the two footer paragraphs:

```gohtml
  <p>{{.T "footer.data"}} <a href="https://maps.sensor.community/" rel="noopener">{{.T "footer.data.map"}}</a></p>
  <p>{{.T "footer.official"}} <a href="https://eea.government.bg/kav/" rel="noopener">{{.T "footer.official.map"}}</a></p>
  <p>{{.T "footer.boundaries"}}</p>
```

`internal/i18n/en.json`:

```json
  "footer.data.map": "sensor.community map",
  "footer.official": "Official data from the Executive Environment Agency (ИАОС) via the European Environment Agency's air quality programme.",
  "footer.official.map": "Official government map",
```

`internal/i18n/bg.json`:

```json
  "footer.data.map": "карта на sensor.community",
  "footer.official": "Официални данни от Изпълнителна агенция по околна среда (ИАОС) чрез програмата за качество на въздуха на Европейската агенция по околна среда.",
  "footer.official.map": "Официална държавна карта",
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && go test ./... 2>&1 | tail -30`
Expected: PASS across the tree. This is the run that catches every remaining `DataAttribution` reference.

- [ ] **Step 6: Mutation-check**

Remove the `eea` entry from `Attributions()` — both new API tests must fail. Restore.
Change the government link to `https://example.invalid` — `TestAttributionsNameBothNetworksAndTheirMaps` and `TestFooterCreditsTheOfficialProgramme` must both fail. Restore. Re-run, confirm PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
printf '%s\n' 'web: replace the single attribution string with a per-source list' '' '- Attributions() returns sensor.community, eea and openstreetmap, each' '  with the map URL a reader can check it against' '- /api/v1/meta gains attributions[], replacing attribution and' '  boundary_attribution' '- footer links maps.sensor.community and eea.government.bg/kav' '- a test asserts every ingested source is credited: ODbL and the EEA' '  reuse terms both require it' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- internal/api internal/web internal/i18n
```

---

### Task 14: End-to-end

**Files:**
- Create: `web/e2e/sources.spec.js`
- Modify: `internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: browser proof that the toggle hides the layer, that the greying explains itself, and that the footer carries both links.

- [ ] **Step 1: Seed an official station in the fixtures**

In `internal/e2e/e2e_test.go`'s `seedFixtures`, add one `source = 'eea'` sensor inside the Sofia fixture area, with a `P1` reading and an `O3` reading, using the same parameterised insert style the existing fixtures use. Give it `station_code = 'BG0050A'` and `station_name = 'София Дружба'`.

- [ ] **Step 2: Write the failing spec**

`web/e2e/sources.spec.js`:

```js
import { test, expect } from './fixtures.js'

// EN routes throughout (see metric.spec.js). One shared context per file, as in
// locate.spec.js: a second cold load spends the rate limiter's burst on assets.
test.describe.serial('the network layers', () => {
  let page

  test.beforeAll(async ({ ctx }) => {
    page = await ctx.newPage()
    const grid = page.waitForResponse(/\/api\/v1\/hexes/)
    await page.goto('/en/')
    await grid
  })

  test.afterAll(async () => { await page.close() })

  test('both networks are offered and both are on', async () => {
    await page.getByRole('button', { name: 'Layers' }).click()
    await expect(page.getByRole('checkbox', { name: 'Citizen sensors' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'Official stations' })).toBeChecked()
  })

  test('switching a network off repaints without a request', async () => {
    const requests = []
    page.on('request', (r) => { if (r.url().includes('/api/v1/')) requests.push(r.url()) })
    const painted = page.evaluate(() => new Promise((resolve) => {
      document.querySelector('[data-island="map"]')
        .addEventListener('airbg:paint', (e) => resolve(e.detail.source), { once: true })
    }))
    await page.getByRole('checkbox', { name: 'Official stations' }).uncheck()
    expect(await painted).toBe('airbg-data')
    expect(requests).toHaveLength(0)
  })

  test('a metric only one network measures explains itself', async () => {
    await page.getByRole('button', { name: /^Metric:/ }).click()
    await page.getByRole('radio', { name: 'Ozone' }).check()
    const citizen = page.getByRole('checkbox', { name: /Citizen sensors/ })
    await expect(citizen).toBeDisabled()
    await expect(page.getByText(/does not measure this/)).toBeVisible()
  })
})

test('the footer credits both programmes', async ({ ctx }) => {
  const page = await ctx.newPage()
  await page.goto('/en/')
  await expect(page.getByRole('link', { name: 'Official government map' }))
    .toHaveAttribute('href', 'https://eea.government.bg/kav/')
  await expect(page.getByRole('link', { name: 'sensor.community map' }))
    .toHaveAttribute('href', 'https://maps.sensor.community/')
  await page.close()
})
```

- [ ] **Step 3: Run the suite to verify it fails**

Run:
```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
npm --prefix web run build && git checkout internal/web/dist/.keep
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test -tags e2e ./internal/e2e/ -run TestE2E -v
```
Expected: FAIL on the new spec.

- [ ] **Step 4: Fix whatever the browser disagrees with**

The unit tests cover the pure logic; this step is where the real accessible names, the real disabled state and the real event ordering get reconciled. Adjust the implementation — not the assertions — until the spec passes, unless an assertion is demonstrably describing a UI that was never built.

- [ ] **Step 5: Run the suite twice**

Run the Step 3 command twice back to back. Both must be green. One green run of a suite with a shared browser context does not prove it is not order-dependent.

- [ ] **Step 6: Commit**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
git checkout internal/web/dist/.keep
git add web/e2e/sources.spec.js
printf '%s\n' 'e2e: cover the network layer toggle' '' '- asserts a toggle fires airbg:paint and puts zero /api/v1/ requests' '  on the wire, which is why a filter was used over a second source' '- asserts the disabled state and the reason text for a metric only one' '  network measures; the unit test cannot see the real disabled' '  attribute or its accessible name' '- asserts both footer attribution links' > /tmp/airbg-commit-msg.txt
git commit -F /tmp/airbg-commit-msg.txt -- web/e2e/sources.spec.js internal/e2e/e2e_test.go
```

---

### Task 15: Deploy and verify

**Files:** none — this is the verification gate.

- [ ] **Step 1: Full test run**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea
npm --prefix web test
npm --prefix web run build && git checkout internal/web/dist/.keep
DOCKER_HOST="unix://$HOME/.colima/default/docker.sock" TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock go test ./... 2>&1 | tail -40
```
Expected: no failures. Report the output verbatim if there are any.

- [ ] **Step 2: Confirm a clean tree**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && git status --porcelain
```
Expected: empty. `deploy` asserts a clean tree, and `internal/web/dist/.keep` is the file that is usually missing.

- [ ] **Step 3: Deploy**

```bash
cd /Users/iliyan/Work/DojoBits/infra/github/airtube-web2.0-eea && ./tools/deploy-airbg.sh
```
Run backgrounded — it takes over two minutes.

- [ ] **Step 4: Verify the running image, not the exit code**

```bash
ssh ubuntu@192.168.1.176 'sudo docker ps --format "{{.Names}} {{.Image}}"'
```
The app container must carry the tag of the commit just pushed. Deploy can exit 0 while the guest keeps a stale image; the tag is the only proof.

- [ ] **Step 5: Verify the feed ran**

```bash
ssh ubuntu@192.168.1.176 'sudo docker logs --since 90m $(sudo docker ps -qf name=airbg) 2>&1 | grep "eea cycle"'
```
Expected: an `eea cycle complete` line with a non-zero `written`, and `unplaceable=0`. A non-zero `unplaceable` is the frozen-metadata risk arriving — report the sampling points rather than papering over them.

- [ ] **Step 6: Verify the map**

Load `https://<dev host>/en/`, open the layers menu, confirm both networks are offered, switch each off in turn, and check the footer's two links. Report what you saw.

---

## Self-Review

**Spec coverage:**

| Spec section | Task |
|---|---|
| §1 data source, metadata CSV, frozen-file risk | 4, 5, 8 |
| §2 all eight pollutants | 3, 9 |
| §2 independent selectable layers, auto-hide with reason | 11 |
| §2 both sources feed the aggregates | 7 (source carried through reads; `usableQuality` unchanged, so both are counted) |
| §2 full history backfilled | 8 — the UTD files carry the history back to 2025-01-01 and are ingested whole on first pass |
| §2 one new dependency | 2 |
| §3 migration, columns, reserved id range, Validity → quality | 1, 7, 8 |
| §4 hourly poll, conditional GET, pollutant map, config block | 5, 6, 8 |
| §5 layer control, hash state, marker shapes, panel fields | 11, 12 |
| §6 attribution per source with both maps linked | 13 |
| §7 resource impact | no code; the sizing already absorbs it |
| §8 known risks | surfaced as `Stats.Unplaceable` (Task 8) and the greying (Task 11) |

**Two gaps, stated rather than hidden:**

1. **Spec §5's `#layers=community,official` hash key is not implemented.** The two existing sensor-side controls (status filter, and every `view:` toggle) persist in `localStorage` via `maplayers.js`'s `readState`/`writeState`, not in the hash — `viewstate.js` carries only `metric` and `sensor`. Putting layers in the hash would make this the one control that behaves differently from its neighbours. The toggles persist across reloads via the same storage every other toggle uses; they are not shareable in a link. If shareable links matter, that is a follow-up that should move all the view toggles at once, not just these two.

2. **Spec §2's "source composition reported alongside every number" is not implemented.** Both sources feed the aggregates (nothing excludes them, and `usableQuality` is untouched), but no endpoint publishes a per-area breakdown of how many of each network contributed. That is a real reporting feature with its own API surface and its own UI, and folding it in here would make an already-fifteen-task plan span two subsystems. Recommend a separate spec.

**Type consistency check:** `Row`/`Station`/`Metadata` (Tasks 2–4) → `StationUpsert`/`StationReading` (Task 7) → `Collector.RunOnce` (Task 8) type-check against each other. `config.EEA` is defined once, in Task 5, and only extended by wiring in Task 6. `filterBySource(features, enabled)` takes a `Set` in both its definition and both call sites. `measuredBy(source, metric)` takes the wire source name (`'sensor.community'`, `'eea'`), not the view id (`'communitySensors'`), and Task 11's `setSourceViewAvailability` maps between them explicitly.
