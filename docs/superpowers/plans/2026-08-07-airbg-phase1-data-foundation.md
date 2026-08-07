# airbg.org Phase 1 — Data Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the ingest, quality-scoring, and storage layer for airbg.org — polling sensor.community every five minutes, scoring every reading, and persisting it to PostgreSQL with PostGIS and TimescaleDB.

**Architecture:** A single Go binary with four internal packages (`upstream`, `quality`, `ingest`, `area`) plus a `backfill` subcommand. Quality scoring is a pure function over one poll batch, so spatial neighbour comparison happens in memory rather than via database round-trips. Storage is one PostgreSQL instance: PostGIS for spatial questions, TimescaleDB for time-series, both over the same rows.

**Tech Stack:** Go 1.26, `pgx/v5`, `goose` migrations (embedded), `testcontainers-go`, PostgreSQL 16 + PostGIS 3.4 + TimescaleDB 2.x (`timescale/timescaledb-ha:pg16` image, which bundles both extensions).

Design source: `docs/superpowers/specs/2026-08-07-airbg-phase1-design.md`. Legacy audit: `ANALYSIS.md`.

This plan covers §4.1 (`ingest`, `quality`), §5 (data model), §6 (quality scoring), and §10's ingest-related failure handling. The API (§7, §8) and frontend (§9) are separate plans.

## Global Constraints

- Go module path: `airbg.org`. Go 1.26 toolchain, declared in `go.mod`.
- **Coordinates are stored as `geography(Point, 4326)` — (longitude, latitude).** This is the inverse of the legacy `[lat, long]`. Task 3 contains a mandatory regression test.
- All SQL is parameterised via `pgx`. String-concatenated SQL is forbidden anywhere in this plan.
- No secrets in the repository. Configuration comes from environment variables only.
- Metric identifiers are exactly: `P1`, `P2`, `temperature`, `humidity`, `pressure`, `noise_LAeq`, `noise_LA_max`. Any other upstream `value_type` is ignored.
- Quality flag values are exactly: `ok`, `out_of_range`, `stuck`, `spatial_outlier`, `no_neighbours`.
- A malformed reading fails that reading only, never the cycle (spec §10).
- Bad readings are flagged, never deleted (spec §5.2).
- Commit after every task. Never commit `CLAUDE.md`.

## Deviation from the spec, recorded deliberately

Spec §5.3 describes `reading_hourly` as a TimescaleDB *continuous aggregate* and also states that backfill writes to it directly. Those two requirements are incompatible: continuous aggregates are not directly insertable.

This plan implements `reading_hourly` as a **plain hypertable** maintained by an explicit rollup step after each ingest cycle. This satisfies both requirements with one code path, keeps the `quality = 'ok'` filter, and makes the one-year backfill a straightforward upsert. Retention policies are unaffected.

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod`, `go.sum` | Module definition |
| `cmd/airbg/main.go` | Entry point; subcommand dispatch (`serve`, `migrate`, `backfill`, `import-areas`) |
| `internal/config/config.go` | Environment-variable configuration, validation |
| `internal/db/db.go` | Pool construction, `Migrate` |
| `internal/db/migrations/*.sql` | Goose migrations, embedded |
| `internal/upstream/types.go` | `Reading`, canonical metric set |
| `internal/upstream/client.go` | sensor.community HTTP client, normalisation |
| `internal/upstream/testdata/` | Recorded upstream fixtures |
| `internal/quality/flag.go` | `Flag` type and constants |
| `internal/quality/ranges.go` | Range check |
| `internal/quality/history.go` | Stuck-value detection |
| `internal/quality/spatial.go` | Median/MAD spatial outlier check |
| `internal/quality/score.go` | Orchestration: the pure scoring function |
| `internal/store/store.go` | Reading and sensor persistence |
| `internal/store/rollup.go` | Hourly rollup upsert |
| `internal/ingest/ingest.go` | One poll cycle, error isolation, stats |
| `internal/area/import.go` | GeoJSON boundary import |
| `internal/area/assign.go` | Sensor-to-area assignment |
| `internal/backfill/backfill.go` | Archive CSV import |
| `docker-compose.yml` | Local PostgreSQL with PostGIS + TimescaleDB |
| `.github/workflows/ci.yml` | Build, vet, staticcheck, govulncheck, test |

Quality is split across four files rather than one because each check has genuinely different logic and test data, and §6's per-metric asymmetry lives entirely in `spatial.go`. Keeping it isolated means the rule that protects real pollution readings from being deleted is reviewable on its own.

---

## Task 1: Repository scaffold, configuration, and CI

**Files:**
- Create: `go.mod`, `cmd/airbg/main.go`, `internal/config/config.go`, `docker-compose.yml`, `.github/workflows/ci.yml`, `.gitignore` (modify)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `config.Config{DatabaseURL string, UpstreamURL string, PollInterval time.Duration}`, `config.Load() (Config, error)`

- [ ] **Step 1: Initialise the module**

```bash
go mod init airbg.org
go mod edit -go=1.26
```

- [ ] **Step 2: Write the failing config test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("AIRBG_DATABASE_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when AIRBG_DATABASE_URL is unset, got nil")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PollInterval != 5*time.Minute {
		t.Errorf("PollInterval = %v, want 5m", cfg.PollInterval)
	}
	if cfg.UpstreamURL != "https://data.sensor.community/airrohr/v1/filter/country=BG" {
		t.Errorf("UpstreamURL = %q, unexpected default", cfg.UpstreamURL)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: Load`

- [ ] **Step 4: Implement the config package**

Create `internal/config/config.go`:

```go
// Package config loads runtime configuration from the environment.
// No configuration is read from files, and no secret is ever compiled in.
package config

import (
	"errors"
	"os"
	"time"
)

const defaultUpstreamURL = "https://data.sensor.community/airrohr/v1/filter/country=BG"

type Config struct {
	DatabaseURL  string
	UpstreamURL  string
	PollInterval time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:  os.Getenv("AIRBG_DATABASE_URL"),
		UpstreamURL:  os.Getenv("AIRBG_UPSTREAM_URL"),
		PollInterval: 5 * time.Minute,
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: AIRBG_DATABASE_URL is required")
	}
	if cfg.UpstreamURL == "" {
		cfg.UpstreamURL = defaultUpstreamURL
	}
	if v := os.Getenv("AIRBG_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, err
		}
		cfg.PollInterval = d
	}
	return cfg, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/config/`
Expected: PASS

- [ ] **Step 6: Add the entry point**

Create `cmd/airbg/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: airbg <migrate|serve|backfill|import-areas>")
		os.Exit(2)
	}
	switch os.Args[1] {
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}
```

- [ ] **Step 7: Add the local database**

Create `docker-compose.yml`:

```yaml
services:
  db:
    image: timescale/timescaledb-ha:pg16
    environment:
      POSTGRES_PASSWORD: airbg
      POSTGRES_USER: airbg
      POSTGRES_DB: airbg
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U airbg"]
      interval: 5s
      timeout: 5s
      retries: 10
```

The `timescaledb-ha` image bundles PostGIS; the plain `timescaledb` image does not.

- [ ] **Step 8: Add CI**

Create `.github/workflows/ci.yml`:

```yaml
name: ci
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - run: go build ./...
      - run: go vet ./...
      - run: go run honnef.co/go/tools/cmd/staticcheck@latest ./...
      - run: go run golang.org/x/vuln/cmd/govulncheck@latest ./...
      - run: go test ./... -race
```

- [ ] **Step 9: Extend `.gitignore`**

Append to `.gitignore` (keep the existing `CLAUDE.md` line):

```
/airbg
*.pmtiles
.env
```

- [ ] **Step 10: Verify the build**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build succeeds, tests pass

- [ ] **Step 11: Commit**

```bash
git add go.mod cmd internal/config docker-compose.yml .github .gitignore
git commit -m "feat: scaffold Go module, config loading, and CI"
```

---

## Task 2: Database connection and migration runner

**Files:**
- Create: `internal/db/db.go`, `internal/db/migrations/embed.go`
- Test: `internal/db/db_test.go`, `internal/testsupport/postgres.go`

**Interfaces:**
- Consumes: `config.Config`
- Produces: `db.Open(ctx, url string) (*pgxpool.Pool, error)`, `db.Migrate(ctx, pool *pgxpool.Pool) error`, `testsupport.NewPostgres(t *testing.T) *pgxpool.Pool`

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/jackc/pgx/v5/pgxpool@latest
go get github.com/pressly/goose/v3@latest
go get github.com/testcontainers/testcontainers-go@latest
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
```

- [ ] **Step 2: Write the test helper**

Create `internal/testsupport/postgres.go`:

```go
// Package testsupport provides throwaway PostgreSQL instances for tests.
// Every test gets a real database with PostGIS and TimescaleDB, because the
// behaviour under test (spatial containment, hypertables, retention) cannot
// be faked.
package testsupport

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func NewPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"timescale/timescaledb-ha:pg16",
		tcpostgres.WithDatabase("airbg"),
		tcpostgres.WithUsername("airbg"),
		tcpostgres.WithPassword("airbg"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(120*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
```

- [ ] **Step 3: Write the failing migration test**

Create `internal/db/db_test.go`:

```go
package db_test

import (
	"context"
	"testing"

	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}
```

- [ ] **Step 4: Run the test to verify it fails**

Run: `go test ./internal/db/`
Expected: FAIL — `undefined: db.Migrate`

- [ ] **Step 5: Implement the db package**

Create `internal/db/migrations/embed.go`:

```go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

Create `internal/db/db.go`:

```go
// Package db owns the connection pool and schema migrations.
package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"airbg.org/internal/db/migrations"
)

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	// A statement timeout bounds every query, so a pathological plan cannot
	// pin a connection indefinitely (spec §10).
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	return pgxpool.NewWithConfig(ctx, cfg)
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	return goose.UpContext(ctx, sqlDB, ".")
}
```

- [ ] **Step 6: Add an empty first migration so the runner has something to apply**

Create `internal/db/migrations/00001_extensions.sql`:

```sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- +goose Down
DROP EXTENSION IF EXISTS timescaledb;
DROP EXTENSION IF EXISTS postgis;
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `go test ./internal/db/ -v`
Expected: PASS (first run pulls the container image; allow several minutes)

- [ ] **Step 8: Commit**

```bash
git add internal/db internal/testsupport go.mod go.sum
git commit -m "feat: add connection pool and embedded goose migrations"
```

---

## Task 3: Core schema — sensors, readings, and the coordinate-order guard

**Files:**
- Create: `internal/db/migrations/00002_core_schema.sql`
- Test: `internal/db/schema_test.go`

**Interfaces:**
- Consumes: `db.Migrate`
- Produces: tables `sensor`, `reading`; enum `quality_flag`

- [ ] **Step 1: Write the failing schema tests**

Create `internal/db/schema_test.go`:

```go
package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

// Sofia's Alexander Nevsky Cathedral. Longitude first — PostGIS geography is
// (lon, lat), the inverse of the legacy [lat, long] convention. Swapping these
// silently relocates every Bulgarian sensor into the Indian Ocean, which is why
// this test exists (spec §5, §11.2).
const (
	sofiaLon = 23.3327
	sofiaLat = 42.6957
)

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, pool
}

func TestSensorCoordinateOrder(t *testing.T) {
	ctx, pool := migrated(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography)`,
		int64(1), "SDS011", sofiaLon, sofiaLat)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	var lon, lat float64
	err = pool.QueryRow(ctx,
		`SELECT ST_X(location::geometry), ST_Y(location::geometry)
		 FROM sensor WHERE sensor_id = $1`, int64(1)).Scan(&lon, &lat)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if lon < 22 || lon > 29 {
		t.Errorf("longitude = %v, outside Bulgaria — coordinates are swapped", lon)
	}
	if lat < 41 || lat > 45 {
		t.Errorf("latitude = %v, outside Bulgaria — coordinates are swapped", lat)
	}
}

func TestReadingIsHypertable(t *testing.T) {
	ctx, pool := migrated(t)

	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM timescaledb_information.hypertables
		 WHERE hypertable_name = 'reading'`).Scan(&count)
	if err != nil {
		t.Fatalf("query hypertables: %v", err)
	}
	if count != 1 {
		t.Fatalf("reading is not a hypertable (found %d)", count)
	}
}

func TestReadingRejectsDuplicateSamples(t *testing.T) {
	ctx, pool := migrated(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	mustInsertSensor(t, ctx, pool, 1)

	insert := func() error {
		_, err := pool.Exec(ctx,
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, $3, $4, 'ok')
			 ON CONFLICT (sensor_id, metric, time) DO NOTHING`,
			ts, int64(1), "P1", 24.3)
		return err
	}
	if err := insert(); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := insert(); err != nil {
		t.Fatalf("second insert should be a no-op, got: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading count = %d, want 1 (duplicate not suppressed)", n)
	}
}

func mustInsertSensor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES ($1, 'SDS011', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography)`,
		id, sofiaLon, sofiaLat)
	if err != nil {
		t.Fatalf("insert sensor %d: %v", id, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/db/ -run 'TestSensor|TestReading' -v`
Expected: FAIL — relation `sensor` does not exist

- [ ] **Step 3: Write the schema migration**

Create `internal/db/migrations/00002_core_schema.sql`:

```sql
-- +goose Up
CREATE TABLE sensor (
    sensor_id   bigint PRIMARY KEY,
    sensor_type text NOT NULL,
    location    geography(Point, 4326) NOT NULL,
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now(),
    active      boolean NOT NULL DEFAULT true
);

CREATE INDEX sensor_location_idx ON sensor USING gist (location);

CREATE TYPE quality_flag AS ENUM (
    'ok', 'out_of_range', 'stuck', 'spatial_outlier', 'no_neighbours'
);

CREATE TABLE reading (
    time      timestamptz NOT NULL,
    sensor_id bigint NOT NULL,
    metric    text NOT NULL,
    value     double precision NOT NULL,
    quality   quality_flag NOT NULL DEFAULT 'ok'
);

SELECT create_hypertable('reading', 'time', chunk_time_interval => interval '1 day');

-- Upserts key on this; it also serves per-sensor chart queries.
CREATE UNIQUE INDEX reading_sensor_metric_time_idx
    ON reading (sensor_id, metric, time DESC);

-- +goose Down
DROP TABLE reading;
DROP TYPE quality_flag;
DROP TABLE sensor;
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/db/ -v`
Expected: PASS, including `TestSensorCoordinateOrder`

- [ ] **Step 5: Commit**

```bash
git add internal/db
git commit -m "feat: add sensor and reading schema with coordinate-order regression test"
```

---

## Task 4: Hourly rollup table and retention policies

**Files:**
- Create: `internal/db/migrations/00003_rollup_retention.sql`
- Test: `internal/db/retention_test.go`

**Interfaces:**
- Produces: table `reading_hourly`, retention policies on `reading` and `reading_hourly`

- [ ] **Step 1: Write the failing test**

Create `internal/db/retention_test.go`:

```go
package db_test

import "testing"

func TestReadingHourlyIsHypertable(t *testing.T) {
	ctx, pool := migrated(t)

	var count int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM timescaledb_information.hypertables
		 WHERE hypertable_name = 'reading_hourly'`).Scan(&count)
	if err != nil {
		t.Fatalf("query hypertables: %v", err)
	}
	if count != 1 {
		t.Fatalf("reading_hourly is not a hypertable (found %d)", count)
	}
}

func TestRetentionPoliciesExist(t *testing.T) {
	ctx, pool := migrated(t)

	rows, err := pool.Query(ctx,
		`SELECT hypertable_name FROM timescaledb_information.jobs
		 WHERE proc_name = 'policy_retention'`)
	if err != nil {
		t.Fatalf("query jobs: %v", err)
	}
	defer rows.Close()

	found := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found[name] = true
	}
	for _, want := range []string{"reading", "reading_hourly"} {
		if !found[want] {
			t.Errorf("no retention policy on %s", want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/db/ -run 'TestReadingHourly|TestRetention' -v`
Expected: FAIL — relation `reading_hourly` does not exist

- [ ] **Step 3: Write the migration**

Create `internal/db/migrations/00003_rollup_retention.sql`:

```sql
-- +goose Up

-- A plain hypertable rather than a continuous aggregate: the one-year archive
-- backfill writes hourly buckets directly, and continuous aggregates are not
-- insertable. The rollup job in internal/store/rollup.go maintains it from raw
-- readings, filtering on quality = 'ok' so flagged data can never contaminate a
-- published average (spec §5.3).
CREATE TABLE reading_hourly (
    bucket    timestamptz NOT NULL,
    sensor_id bigint NOT NULL,
    metric    text NOT NULL,
    avg_value double precision NOT NULL,
    min_value double precision NOT NULL,
    max_value double precision NOT NULL,
    sample_count integer NOT NULL
);

SELECT create_hypertable('reading_hourly', 'bucket', chunk_time_interval => interval '7 days');

CREATE UNIQUE INDEX reading_hourly_key_idx
    ON reading_hourly (sensor_id, metric, bucket DESC);

SELECT add_retention_policy('reading', drop_after => interval '30 days');
SELECT add_retention_policy('reading_hourly', drop_after => interval '2 years');

-- +goose Down
SELECT remove_retention_policy('reading_hourly');
SELECT remove_retention_policy('reading');
DROP TABLE reading_hourly;
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/db/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/db
git commit -m "feat: add hourly rollup hypertable and retention policies"
```

---

## Task 5: Upstream client and normalisation

**Files:**
- Create: `internal/upstream/types.go`, `internal/upstream/client.go`, `internal/upstream/testdata/bg_sample.json`
- Test: `internal/upstream/client_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `upstream.Reading{SensorID int64, SensorType string, Lon, Lat float64, Metric string, Value float64, Timestamp time.Time}`
  - `upstream.Client` with `New(baseURL string, timeout time.Duration) *Client` and `Fetch(ctx context.Context) ([]Reading, error)`
  - `upstream.Normalise(payload []byte) ([]Reading, int, error)` returning readings, count of skipped entries, error

- [ ] **Step 1: Record the fixture**

Create `internal/upstream/testdata/bg_sample.json`. This is a trimmed, hand-checked sample matching the upstream shape, including entries that must be skipped:

```json
[
  {
    "id": 100001,
    "timestamp": "2026-08-07 12:00:00",
    "location": {"id": 500, "latitude": "42.6957", "longitude": "23.3327", "country": "BG"},
    "sensor": {"id": 12345, "sensor_type": {"name": "SDS011"}},
    "sensordatavalues": [
      {"value_type": "P1", "value": "24.30"},
      {"value_type": "P2", "value": "16.10"},
      {"value_type": "durP1", "value": "1234"},
      {"value_type": "signal", "value": "-78 dBm"}
    ]
  },
  {
    "id": 100002,
    "timestamp": "2026-08-07 12:00:00",
    "location": {"id": 501, "latitude": "42.1354", "longitude": "24.7453", "country": "BG"},
    "sensor": {"id": 12346, "sensor_type": {"name": "BME280"}},
    "sensordatavalues": [
      {"value_type": "temperature", "value": "21.50"},
      {"value_type": "humidity", "value": "48.20"},
      {"value_type": "pressure", "value": "94210.00"}
    ]
  },
  {
    "id": 100003,
    "timestamp": "2026-08-07 12:00:00",
    "location": {"id": 502, "latitude": "", "longitude": "", "country": "BG"},
    "sensor": {"id": 12347, "sensor_type": {"name": "SDS011"}},
    "sensordatavalues": [
      {"value_type": "P1", "value": "30.00"}
    ]
  }
]
```

Entry 3 has no coordinates and must be skipped without failing the batch. `signal` and `durP1` are outside the canonical metric set and must be dropped — note that `signal` carries the value `"-78 dBm"`, the exact string that broke the legacy collector's line-protocol writes (`ANALYSIS.md` B3).

- [ ] **Step 2: Write the failing tests**

Create `internal/upstream/client_test.go`:

```go
package upstream

import (
	"os"
	"testing"
	"time"
)

func TestNormaliseSelectsCanonicalMetrics(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, skipped, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	// 2 from sensor 12345 (P1, P2 — durP1 and signal dropped),
	// 3 from sensor 12346. Sensor 12347 has no coordinates.
	if len(readings) != 5 {
		t.Fatalf("len(readings) = %d, want 5", len(readings))
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}

	for _, r := range readings {
		if !IsCanonicalMetric(r.Metric) {
			t.Errorf("non-canonical metric %q survived normalisation", r.Metric)
		}
	}
}

func TestNormaliseCoordinateOrder(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	for _, r := range readings {
		if r.Lon < 22 || r.Lon > 29 {
			t.Errorf("sensor %d Lon = %v, outside Bulgaria — lat/lon swapped", r.SensorID, r.Lon)
		}
		if r.Lat < 41 || r.Lat > 45 {
			t.Errorf("sensor %d Lat = %v, outside Bulgaria — lat/lon swapped", r.SensorID, r.Lat)
		}
	}
}

func TestNormaliseParsesTimestamp(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	want := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if !readings[0].Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", readings[0].Timestamp, want)
	}
}

func TestNormaliseRejectsGarbage(t *testing.T) {
	if _, _, err := Normalise([]byte("not json")); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/upstream/`
Expected: FAIL — `undefined: Normalise`

- [ ] **Step 4: Implement types**

Create `internal/upstream/types.go`:

```go
// Package upstream fetches and normalises readings from sensor.community.
package upstream

import "time"

// Reading is one metric from one sensor at one instant, already normalised.
type Reading struct {
	SensorID   int64
	SensorType string
	Lon        float64 // longitude first, matching PostGIS geography
	Lat        float64
	Metric     string
	Value      float64
	Timestamp  time.Time
}

// canonicalMetrics is the exact set stored. Upstream sends many more
// (durP1, ratioP1, signal, …); everything outside this set is dropped.
var canonicalMetrics = map[string]bool{
	"P1":            true,
	"P2":            true,
	"temperature":   true,
	"humidity":      true,
	"pressure":      true,
	"noise_LAeq":    true,
	"noise_LA_max":  true,
}

func IsCanonicalMetric(m string) bool { return canonicalMetrics[m] }
```

- [ ] **Step 5: Implement the client**

Create `internal/upstream/client.go`:

```go
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// upstreamTimeLayout is sensor.community's timestamp format. It carries no zone
// and is documented as UTC.
const upstreamTimeLayout = "2006-01-02 15:04:05"

// maxPayloadBytes bounds what we will read from upstream, so a malformed or
// hostile response cannot exhaust memory.
const maxPayloadBytes = 64 << 20

type apiEntry struct {
	Timestamp string `json:"timestamp"`
	Location  struct {
		Latitude  string `json:"latitude"`
		Longitude string `json:"longitude"`
		Country   string `json:"country"`
	} `json:"location"`
	Sensor struct {
		ID         int64 `json:"id"`
		SensorType struct {
			Name string `json:"name"`
		} `json:"sensor_type"`
	} `json:"sensor"`
	Values []struct {
		ValueType string `json:"value_type"`
		Value     string `json:"value"`
	} `json:"sensordatavalues"`
}

// Normalise converts an upstream payload into canonical readings. It returns the
// readings, the number of entries skipped as unusable, and an error only when
// the payload as a whole cannot be parsed. A single malformed entry never fails
// the batch (spec §10).
func Normalise(payload []byte) ([]Reading, int, error) {
	var entries []apiEntry
	if err := json.Unmarshal(payload, &entries); err != nil {
		return nil, 0, fmt.Errorf("upstream: parse payload: %w", err)
	}

	readings := make([]Reading, 0, len(entries)*2)
	skipped := 0

	for _, e := range entries {
		lat, errLat := strconv.ParseFloat(e.Location.Latitude, 64)
		lon, errLon := strconv.ParseFloat(e.Location.Longitude, 64)
		ts, errTS := time.Parse(upstreamTimeLayout, e.Timestamp)
		if errLat != nil || errLon != nil || errTS != nil || e.Sensor.ID == 0 {
			skipped++
			continue
		}

		emitted := 0
		for _, v := range e.Values {
			if !canonicalMetrics[v.ValueType] {
				continue
			}
			value, err := strconv.ParseFloat(v.Value, 64)
			if err != nil {
				// e.g. signal's "-78 dBm". Drop the value, keep the entry.
				continue
			}
			readings = append(readings, Reading{
				SensorID:   e.Sensor.ID,
				SensorType: e.Sensor.SensorType.Name,
				Lon:        lon,
				Lat:        lat,
				Metric:     v.ValueType,
				Value:      value,
				Timestamp:  ts.UTC(),
			})
			emitted++
		}
		if emitted == 0 {
			skipped++
		}
	}
	return readings, skipped, nil
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Fetch(ctx context.Context) ([]Reading, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "airbg.org collector (+https://airbg.org)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream: fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream: status %d", resp.StatusCode)
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxPayloadBytes))
	if err != nil {
		return nil, fmt.Errorf("upstream: read body: %w", err)
	}

	readings, _, err := Normalise(payload)
	return readings, err
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/upstream/ -v`
Expected: PASS (4 tests)

- [ ] **Step 7: Commit**

```bash
git add internal/upstream
git commit -m "feat: add sensor.community client with fixture-backed normalisation"
```

---

## Task 6: Quality — range check

**Files:**
- Create: `internal/quality/flag.go`, `internal/quality/ranges.go`
- Test: `internal/quality/ranges_test.go`

**Interfaces:**
- Consumes: `upstream.Reading`
- Produces: `quality.Flag` (string type) with constants `FlagOK`, `FlagOutOfRange`, `FlagStuck`, `FlagSpatialOutlier`, `FlagNoNeighbours`; `quality.InRange(metric string, value float64) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/quality/ranges_test.go`:

```go
package quality

import "testing"

func TestInRange(t *testing.T) {
	cases := []struct {
		metric string
		value  float64
		want   bool
	}{
		{"P1", 24.3, true},
		{"P1", 0, true},
		{"P1", 1000, true},
		{"P1", 1000.1, false},
		{"P1", -1, false},
		{"temperature", -40, true},
		{"temperature", 60, true},
		{"temperature", -999, false},
		{"temperature", 61, false},
		{"humidity", 0, true},
		{"humidity", 100, true},
		{"humidity", 101, false},
		{"pressure", 94210, false}, // Pascals, not hPa — must be converted first
		{"pressure", 942.1, true},
		{"noise_LAeq", 24.9, false},
		{"noise_LAeq", 25, true},
		{"noise_LAeq", 120, true},
		{"unknown_metric", 5, false},
	}

	for _, c := range cases {
		if got := InRange(c.metric, c.value); got != c.want {
			t.Errorf("InRange(%q, %v) = %v, want %v", c.metric, c.value, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/quality/`
Expected: FAIL — `undefined: InRange`

- [ ] **Step 3: Implement the flag type**

Create `internal/quality/flag.go`:

```go
// Package quality scores readings for plausibility. Bad readings are flagged,
// never discarded: the map shows them greyed out, and aggregates exclude them
// (spec §5.2, §6).
package quality

// Flag matches the quality_flag enum in the database exactly.
type Flag string

const (
	FlagOK             Flag = "ok"
	FlagOutOfRange     Flag = "out_of_range"
	FlagStuck          Flag = "stuck"
	FlagSpatialOutlier Flag = "spatial_outlier"
	// FlagNoNeighbours records that the spatial check could not run. It is not
	// a failure: the reading displays normally and counts toward aggregates.
	FlagNoNeighbours Flag = "no_neighbours"
)

// Usable reports whether a flagged reading may contribute to an aggregate.
func (f Flag) Usable() bool {
	return f == FlagOK || f == FlagNoNeighbours
}
```

- [ ] **Step 4: Implement the range check**

Create `internal/quality/ranges.go`:

```go
package quality

type valueRange struct{ min, max float64 }

// Physical plausibility bounds (spec §6.1). The SDS011 saturates near
// 999 µg/m³, so readings at the ceiling are meaningless rather than extreme;
// the bound is set at 1000 so saturated values are retained but anything beyond
// is rejected outright.
var metricRanges = map[string]valueRange{
	"P1":           {0, 1000},
	"P2":           {0, 1000},
	"temperature":  {-40, 60},
	"humidity":     {0, 100},
	"pressure":     {800, 1100}, // hPa
	"noise_LAeq":   {25, 120},
	"noise_LA_max": {25, 120},
}

func InRange(metric string, value float64) bool {
	r, ok := metricRanges[metric]
	if !ok {
		return false
	}
	return value >= r.min && value <= r.max
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/quality/ -v`
Expected: PASS

- [ ] **Step 6: Handle the pressure unit at normalisation**

The fixture shows upstream sends pressure in Pascals (`94210.00`), while the range is in hPa. Add the conversion in `internal/upstream/client.go`, inside the value loop, immediately after `ParseFloat` succeeds:

```go
			// Upstream reports pressure in Pascals; canonical storage is hPa.
			if v.ValueType == "pressure" {
				value /= 100
			}
```

- [ ] **Step 7: Add the conversion test**

Append to `internal/upstream/client_test.go`:

```go
func TestNormaliseConvertsPressureToHectopascals(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	var found bool
	for _, r := range readings {
		if r.Metric != "pressure" {
			continue
		}
		found = true
		if r.Value < 800 || r.Value > 1100 {
			t.Errorf("pressure = %v hPa, outside plausible range — unit not converted", r.Value)
		}
	}
	if !found {
		t.Fatal("no pressure reading in fixture output")
	}
}
```

- [ ] **Step 8: Run all tests**

Run: `go test ./internal/upstream/ ./internal/quality/ -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/quality internal/upstream
git commit -m "feat: add quality flags, range checks, and pressure unit conversion"
```

---

## Task 7: Quality — stuck-value detection

**Files:**
- Create: `internal/quality/history.go`
- Test: `internal/quality/history_test.go`

**Interfaces:**
- Consumes: nothing
- Produces: `quality.History` with `NewHistory(depth int) *History`, `(*History).Observe(sensorID int64, metric string, value float64)`, `(*History).IsStuck(sensorID int64, metric string) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/quality/history_test.go`:

```go
package quality

import "testing"

func TestIsStuckAfterIdenticalObservations(t *testing.T) {
	h := NewHistory(12)

	for i := 0; i < 11; i++ {
		h.Observe(1, "P1", 42.0)
		if h.IsStuck(1, "P1") {
			t.Fatalf("stuck after %d identical observations, want at least 12", i+1)
		}
	}
	h.Observe(1, "P1", 42.0)
	if !h.IsStuck(1, "P1") {
		t.Error("not stuck after 12 identical observations")
	}
}

func TestJitterResetsStuck(t *testing.T) {
	h := NewHistory(12)
	for i := 0; i < 12; i++ {
		h.Observe(1, "P1", 42.0)
	}
	h.Observe(1, "P1", 42.1)
	if h.IsStuck(1, "P1") {
		t.Error("still stuck after the value changed")
	}
}

func TestExemptValuesNeverStick(t *testing.T) {
	// Humidity pinned at 100 %, PM at exactly 0, and humidity at 0 all occur
	// legitimately and must not be flagged (spec §6.2).
	cases := []struct {
		metric string
		value  float64
	}{
		{"humidity", 100},
		{"humidity", 0},
		{"P1", 0},
		{"P2", 0},
	}
	for _, c := range cases {
		h := NewHistory(12)
		for i := 0; i < 20; i++ {
			h.Observe(1, c.metric, c.value)
		}
		if h.IsStuck(1, c.metric) {
			t.Errorf("%s at %v flagged stuck, but this value is exempt", c.metric, c.value)
		}
	}
}

func TestSensorsAreIndependent(t *testing.T) {
	h := NewHistory(12)
	for i := 0; i < 12; i++ {
		h.Observe(1, "P1", 42.0)
		h.Observe(2, "P1", float64(i))
	}
	if !h.IsStuck(1, "P1") {
		t.Error("sensor 1 should be stuck")
	}
	if h.IsStuck(2, "P1") {
		t.Error("sensor 2 varies and must not be stuck")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/quality/ -run TestIsStuck`
Expected: FAIL — `undefined: NewHistory`

- [ ] **Step 3: Implement history**

Create `internal/quality/history.go`:

```go
package quality

import "sync"

// exemptStuckValues are readings that legitimately repeat forever. Flagging them
// would mark healthy sensors as broken (spec §6.2).
var exemptStuckValues = map[string][]float64{
	"humidity": {0, 100},
	"P1":       {0},
	"P2":       {0},
}

type seriesState struct {
	last  float64
	runs  int
	valid bool
}

// History tracks consecutive identical readings per (sensor, metric).
//
// State lives in memory and is empty after a restart, so stuck detection needs
// `depth` cycles (about one hour at a five-minute cadence) to warm up. That is
// acceptable: a stuck sensor stays stuck, so it is detected on the next warm
// window rather than missed.
type History struct {
	mu    sync.Mutex
	depth int
	state map[int64]map[string]*seriesState
}

func NewHistory(depth int) *History {
	return &History{
		depth: depth,
		state: make(map[int64]map[string]*seriesState),
	}
}

func (h *History) Observe(sensorID int64, metric string, value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	byMetric, ok := h.state[sensorID]
	if !ok {
		byMetric = make(map[string]*seriesState)
		h.state[sensorID] = byMetric
	}
	s, ok := byMetric[metric]
	if !ok {
		byMetric[metric] = &seriesState{last: value, runs: 1, valid: true}
		return
	}
	if s.valid && s.last == value {
		s.runs++
		return
	}
	s.last = value
	s.runs = 1
	s.valid = true
}

func (h *History) IsStuck(sensorID int64, metric string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.state[sensorID][metric]
	if !ok || s.runs < h.depth {
		return false
	}
	for _, exempt := range exemptStuckValues[metric] {
		if s.last == exempt {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/quality/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/quality
git commit -m "feat: add stuck-value detection with legitimate-value exemptions"
```

---

## Task 8: Quality — spatial outlier detection

This is the task that decides whether real pollution data survives. Read spec §6.3 before implementing.

**Files:**
- Create: `internal/quality/spatial.go`
- Test: `internal/quality/spatial_test.go`

**Interfaces:**
- Consumes: `upstream.Reading`
- Produces:
  - `quality.Neighbour{Lon, Lat, Value float64}`
  - `quality.SpatialCheck(metric string, value float64, neighbours []Neighbour) Flag`
  - `quality.MedianAbsoluteDeviation(values []float64) (median, mad float64)`

- [ ] **Step 1: Write the failing tests**

Create `internal/quality/spatial_test.go`:

```go
package quality

import (
	"math"
	"testing"
)

func TestMedianAbsoluteDeviation(t *testing.T) {
	median, mad := MedianAbsoluteDeviation([]float64{1, 2, 3, 4, 5})
	if median != 3 {
		t.Errorf("median = %v, want 3", median)
	}
	if mad != 1 {
		t.Errorf("mad = %v, want 1", mad)
	}
}

func TestMedianIsRobustToOneWildOutlier(t *testing.T) {
	// The whole reason for median/MAD over mean/stddev: one sensor stuck at 900
	// must not drag the reference far enough to make itself look normal.
	median, _ := MedianAbsoluteDeviation([]float64{20, 21, 22, 23, 900})
	if median != 22 {
		t.Errorf("median = %v, want 22 — outlier contaminated the reference", median)
	}
}

func TestTemperatureOutlierIsFlagged(t *testing.T) {
	// The canonical case from the spec: 22 °C among −10 °C neighbours.
	neighbours := []Neighbour{
		{Value: -10}, {Value: -9.5}, {Value: -10.5}, {Value: -11},
	}
	if got := SpatialCheck("temperature", 22, neighbours); got != FlagSpatialOutlier {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagSpatialOutlier)
	}
}

func TestNormalTemperatureVariationIsNotFlagged(t *testing.T) {
	neighbours := []Neighbour{
		{Value: -10}, {Value: -9.5}, {Value: -10.5}, {Value: -11},
	}
	if got := SpatialCheck("temperature", -9.8, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagOK)
	}
}

func TestTightNeighbourhoodDoesNotFlagNormalVariation(t *testing.T) {
	// MAD is zero here. Without the per-metric floor, any deviation at all
	// would be infinitely many MADs and every reading would flag.
	neighbours := []Neighbour{
		{Value: 20}, {Value: 20}, {Value: 20}, {Value: 20},
	}
	if got := SpatialCheck("temperature", 21, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v — floor not applied", got, FlagOK)
	}
}

func TestRealPMEpisodeIsNotFlagged(t *testing.T) {
	// A genuine winter inversion: this sensor reads 200 µg/m³ and so do its
	// neighbours. Flagging this would delete exactly the pollution the site
	// exists to report (spec §6.3).
	neighbours := []Neighbour{
		{Value: 180}, {Value: 210}, {Value: 195}, {Value: 220},
	}
	if got := SpatialCheck("P1", 200, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagOK)
	}
}

func TestLocalPMSourceIsNotFlagged(t *testing.T) {
	// One neighbour burning wet wood. 4x the median but below the absolute
	// threshold, so it must survive — this is a real local source, not a fault.
	neighbours := []Neighbour{
		{Value: 30}, {Value: 28}, {Value: 32}, {Value: 25},
	}
	if got := SpatialCheck("P1", 120, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagOK)
	}
}

func TestBrokenPMSensorIsFlagged(t *testing.T) {
	// 900 against a street reading 30: over 5x the median AND over 150 absolute.
	neighbours := []Neighbour{
		{Value: 30}, {Value: 28}, {Value: 32}, {Value: 25},
	}
	if got := SpatialCheck("P1", 900, neighbours); got != FlagSpatialOutlier {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagSpatialOutlier)
	}
}

func TestTooFewNeighbours(t *testing.T) {
	neighbours := []Neighbour{{Value: -10}, {Value: -9}}
	if got := SpatialCheck("temperature", 22, neighbours); got != FlagNoNeighbours {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagNoNeighbours)
	}
}

func TestNoNeighboursIsUsable(t *testing.T) {
	if !FlagNoNeighbours.Usable() {
		t.Error("no_neighbours must count toward aggregates — the check merely could not run")
	}
	if FlagSpatialOutlier.Usable() {
		t.Error("spatial_outlier must never count toward aggregates")
	}
}

func TestSpatialCheckIsDeterministic(t *testing.T) {
	neighbours := []Neighbour{{Value: -10}, {Value: -9.5}, {Value: -10.5}}
	first := SpatialCheck("temperature", 22, neighbours)
	for i := 0; i < 100; i++ {
		if got := SpatialCheck("temperature", 22, neighbours); got != first {
			t.Fatalf("non-deterministic: got %v then %v", first, got)
		}
	}
}

func TestSpatialCheckDoesNotMutateInput(t *testing.T) {
	neighbours := []Neighbour{{Value: 5}, {Value: 1}, {Value: 3}}
	SpatialCheck("temperature", 3, neighbours)
	want := []float64{5, 1, 3}
	for i, n := range neighbours {
		if math.Abs(n.Value-want[i]) > 1e-9 {
			t.Fatalf("input reordered: neighbours[%d] = %v, want %v", i, n.Value, want[i])
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/quality/ -run 'TestSpatial|TestMedian|TestTemperature|TestReal|TestLocal|TestBroken|TestTooFew|TestNoNeigh|TestTight|TestNormal'`
Expected: FAIL — `undefined: SpatialCheck`

- [ ] **Step 3: Implement the spatial check**

Create `internal/quality/spatial.go`:

```go
package quality

import (
	"math"
	"slices"
)

// minNeighbours is the smallest sample from which a median and MAD mean
// anything. Below this the check does not run.
const minNeighbours = 3

// madScale converts MAD to a standard-deviation-equivalent for normally
// distributed data.
const madScale = 1.4826

// madThreshold is how many scaled MADs constitutes an outlier.
const madThreshold = 3.5

// smoothFieldFloors lists the metrics whose values vary smoothly across space,
// so neighbouring sensors genuinely should agree. The floor is the smallest
// deviation that may ever be called an outlier, which prevents an unusually
// tight neighbourhood (MAD near zero) from flagging normal variation.
//
// PM is deliberately absent: it is dominated by point sources, so genuine
// extreme local readings are the signal, not noise. See pmSpatialCheck.
var smoothFieldFloors = map[string]float64{
	"temperature": 1.5,
	"humidity":    8,
	"pressure":    3,
}

// PM guard thresholds. A reading must exceed BOTH to be flagged: many times the
// neighbourhood median AND high in absolute terms. This catches a sensor stuck
// at 900 on a street reading 30, while leaving a genuine 200 µg/m³ inversion
// episode untouched (spec §6.3).
const (
	pmRatioThreshold    = 5.0
	pmAbsoluteThreshold = 150.0
)

type Neighbour struct {
	Lon, Lat float64
	Value    float64
}

// MedianAbsoluteDeviation returns the median of values and the median of the
// absolute deviations from that median. Both are robust to up to half the
// sample being invalid, unlike mean and standard deviation, which are dragged
// by the very outlier being detected.
func MedianAbsoluteDeviation(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	median := medianOfSorted(sorted)

	deviations := make([]float64, len(values))
	for i, v := range values {
		deviations[i] = math.Abs(v - median)
	}
	slices.Sort(deviations)
	return median, medianOfSorted(deviations)
}

func medianOfSorted(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// SpatialCheck compares a reading against its neighbours. It never mutates the
// input slice and is a pure function of its arguments.
func SpatialCheck(metric string, value float64, neighbours []Neighbour) Flag {
	if len(neighbours) < minNeighbours {
		return FlagNoNeighbours
	}

	values := make([]float64, len(neighbours))
	for i, n := range neighbours {
		values[i] = n.Value
	}

	if metric == "P1" || metric == "P2" {
		return pmSpatialCheck(value, values)
	}

	floor, isSmooth := smoothFieldFloors[metric]
	if !isSmooth {
		// Noise is neither smooth nor guarded: it is genuinely local and has no
		// meaningful spatial expectation.
		return FlagOK
	}

	median, mad := MedianAbsoluteDeviation(values)
	deviation := math.Abs(value - median)
	limit := math.Max(madThreshold*madScale*mad, floor)
	if deviation > limit {
		return FlagSpatialOutlier
	}
	return FlagOK
}

func pmSpatialCheck(value float64, values []float64) Flag {
	median, _ := MedianAbsoluteDeviation(values)
	if median <= 0 {
		return FlagOK
	}
	if value > pmRatioThreshold*median && value > pmAbsoluteThreshold {
		return FlagSpatialOutlier
	}
	return FlagOK
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/quality/ -v`
Expected: PASS (all 12 spatial tests plus the earlier ones)

- [ ] **Step 5: Commit**

```bash
git add internal/quality
git commit -m "feat: add spatial outlier detection with PM-specific guard"
```

---

## Task 9: Quality — scoring orchestration with neighbour lookup

**Files:**
- Create: `internal/quality/score.go`
- Test: `internal/quality/score_test.go`

**Interfaces:**
- Consumes: `upstream.Reading`, `quality.History`, `quality.SpatialCheck`, `quality.InRange`
- Produces:
  - `quality.Scored{Reading upstream.Reading, Flag Flag}`
  - `quality.Score(readings []upstream.Reading, hist *History) []Scored`
  - `quality.NeighbourRadiusMetres = 15000.0`

- [ ] **Step 1: Write the failing tests**

Create `internal/quality/score_test.go`:

```go
package quality

import (
	"testing"
	"time"

	"airbg.org/internal/upstream"
)

// Sofia, and points roughly 1 km apart from it.
func at(id int64, metric string, value float64, lonOffset float64) upstream.Reading {
	return upstream.Reading{
		SensorID:   id,
		SensorType: "BME280",
		Lon:        23.3327 + lonOffset,
		Lat:        42.6957,
		Metric:     metric,
		Value:      value,
		Timestamp:  time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC),
	}
}

func flagOf(t *testing.T, scored []Scored, id int64) Flag {
	t.Helper()
	for _, s := range scored {
		if s.Reading.SensorID == id {
			return s.Flag
		}
	}
	t.Fatalf("sensor %d missing from scored output", id)
	return ""
}

func TestScoreFlagsOutOfRangeBeforeAnythingElse(t *testing.T) {
	readings := []upstream.Reading{
		at(1, "temperature", -999, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	scored := Score(readings, NewHistory(12))
	if got := flagOf(t, scored, 1); got != FlagOutOfRange {
		t.Errorf("flag = %v, want %v", got, FlagOutOfRange)
	}
}

func TestScoreFlagsTheSpecExample(t *testing.T) {
	// One sensor reporting 22 °C while every neighbour reports about −10 °C.
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	scored := Score(readings, NewHistory(12))

	if got := flagOf(t, scored, 1); got != FlagSpatialOutlier {
		t.Errorf("broken sensor flag = %v, want %v", got, FlagSpatialOutlier)
	}
	for _, id := range []int64{2, 3, 4} {
		if got := flagOf(t, scored, id); got != FlagOK {
			t.Errorf("healthy sensor %d flag = %v, want %v", id, got, FlagOK)
		}
	}
}

func TestScoreExcludesOutOfRangeNeighboursFromTheReference(t *testing.T) {
	// A dead sensor reporting −999 must not drag the neighbourhood median.
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -999, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
		at(5, "temperature", -10.2, 0.04),
	}
	scored := Score(readings, NewHistory(12))
	if got := flagOf(t, scored, 1); got != FlagOK {
		t.Errorf("healthy sensor flag = %v, want %v — dead neighbour polluted the reference", got, FlagOK)
	}
}

func TestScoreReturnsEveryReading(t *testing.T) {
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(2, "temperature", -10, 0.01),
	}
	scored := Score(readings, NewHistory(12))
	if len(scored) != len(readings) {
		t.Fatalf("len(scored) = %d, want %d — readings must never be dropped", len(scored), len(readings))
	}
}

func TestScoreIsolatesMetrics(t *testing.T) {
	// A temperature outlier must not affect humidity scoring at the same sensor.
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(1, "humidity", 50, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
		at(2, "humidity", 52, 0.01),
		at(3, "humidity", 48, 0.02),
		at(4, "humidity", 51, 0.03),
	}
	scored := Score(readings, NewHistory(12))

	var tempFlag, humFlag Flag
	for _, s := range scored {
		if s.Reading.SensorID != 1 {
			continue
		}
		switch s.Reading.Metric {
		case "temperature":
			tempFlag = s.Flag
		case "humidity":
			humFlag = s.Flag
		}
	}
	if tempFlag != FlagSpatialOutlier {
		t.Errorf("temperature flag = %v, want %v", tempFlag, FlagSpatialOutlier)
	}
	if humFlag != FlagOK {
		t.Errorf("humidity flag = %v, want %v", humFlag, FlagOK)
	}
}

func TestScoreFlagsStuckSensor(t *testing.T) {
	hist := NewHistory(3)
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10.2, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	var scored []Scored
	for i := 0; i < 3; i++ {
		scored = Score(readings, hist)
	}
	if got := flagOf(t, scored, 1); got != FlagStuck {
		t.Errorf("flag = %v, want %v", got, FlagStuck)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/quality/ -run TestScore`
Expected: FAIL — `undefined: Score`

- [ ] **Step 3: Implement scoring**

Create `internal/quality/score.go`:

```go
package quality

import (
	"math"

	"airbg.org/internal/upstream"
)

// NeighbourRadiusMetres is the search radius for the spatial check (spec §6.3).
const NeighbourRadiusMetres = 15000.0

const earthRadiusMetres = 6371000.0

type Scored struct {
	Reading upstream.Reading
	Flag    Flag
}

// Score evaluates a whole poll batch. Neighbour comparison runs in memory over
// the batch rather than against the database: one poll returns every Bulgarian
// sensor at once, so the neighbourhood is already in hand.
//
// Checks run in order and the first failure wins: range, then stuck, then
// spatial. Every input reading appears in the output — readings are flagged,
// never dropped.
func Score(readings []upstream.Reading, hist *History) []Scored {
	// Group by metric so a sensor is only ever compared against the same
	// quantity, and so one bad metric cannot influence another.
	byMetric := make(map[string][]upstream.Reading)
	for _, r := range readings {
		byMetric[r.Metric] = append(byMetric[r.Metric], r)
	}

	// The reference population excludes out-of-range values: a sensor reporting
	// -999 must not drag the neighbourhood median it is being compared against.
	reference := make(map[string][]upstream.Reading, len(byMetric))
	for metric, group := range byMetric {
		valid := make([]upstream.Reading, 0, len(group))
		for _, r := range group {
			if InRange(metric, r.Value) {
				valid = append(valid, r)
			}
		}
		reference[metric] = valid
	}

	out := make([]Scored, 0, len(readings))
	for _, r := range readings {
		out = append(out, Scored{Reading: r, Flag: scoreOne(r, reference[r.Metric], hist)})
	}
	return out
}

func scoreOne(r upstream.Reading, population []upstream.Reading, hist *History) Flag {
	if !InRange(r.Metric, r.Value) {
		return FlagOutOfRange
	}

	hist.Observe(r.SensorID, r.Metric, r.Value)
	if hist.IsStuck(r.SensorID, r.Metric) {
		return FlagStuck
	}

	neighbours := make([]Neighbour, 0, 8)
	for _, other := range population {
		if other.SensorID == r.SensorID {
			continue
		}
		if haversineMetres(r.Lon, r.Lat, other.Lon, other.Lat) > NeighbourRadiusMetres {
			continue
		}
		neighbours = append(neighbours, Neighbour{Lon: other.Lon, Lat: other.Lat, Value: other.Value})
	}
	return SpatialCheck(r.Metric, r.Value, neighbours)
}

// haversineMetres returns great-circle distance. Accurate enough at the 15 km
// scale this is used for, and avoids a database round-trip per reading.
func haversineMetres(lon1, lat1, lon2, lat2 float64) float64 {
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	Δφ := (lat2 - lat1) * math.Pi / 180
	Δλ := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(Δφ/2)*math.Sin(Δφ/2) +
		math.Cos(φ1)*math.Cos(φ2)*math.Sin(Δλ/2)*math.Sin(Δλ/2)
	return 2 * earthRadiusMetres * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/quality/ -v`
Expected: PASS

- [ ] **Step 5: Add a property-based test**

Spec §11.1 calls for property testing on the scorer. Append to `internal/quality/score_test.go`:

```go
func TestPropertyReadingEqualToNeighbourMedianIsNeverFlagged(t *testing.T) {
	// Invariant: a reading identical to its neighbours' median is always OK,
	// for any in-range value and any neighbour count above the minimum.
	for _, base := range []float64{-30, -10, 0, 5, 20, 35, 55} {
		for n := minNeighbours; n <= 12; n++ {
			readings := []upstream.Reading{at(1, "temperature", base, 0)}
			for i := 1; i <= n; i++ {
				readings = append(readings, at(int64(i+1), "temperature", base, float64(i)*0.005))
			}
			scored := Score(readings, NewHistory(1000))
			if got := flagOf(t, scored, 1); got != FlagOK {
				t.Fatalf("base=%v n=%d: flag = %v, want %v", base, n, got, FlagOK)
			}
		}
	}
}

func TestPropertyAddingMedianNeighbourNeverCausesAFlag(t *testing.T) {
	// Invariant: adding a neighbour equal to the median cannot turn a previously
	// OK reading into a flagged one.
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10.2, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	if got := flagOf(t, Score(readings, NewHistory(1000)), 1); got != FlagOK {
		t.Fatalf("precondition failed: flag = %v", got)
	}
	for i := 0; i < 20; i++ {
		readings = append(readings, at(int64(100+i), "temperature", -10.2, float64(i)*0.004))
		if got := flagOf(t, Score(readings, NewHistory(1000)), 1); got != FlagOK {
			t.Fatalf("after adding %d median neighbours: flag = %v, want %v", i+1, got, FlagOK)
		}
	}
}
```

- [ ] **Step 6: Run all quality tests**

Run: `go test ./internal/quality/ -v -race`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/quality
git commit -m "feat: add batch quality scoring with in-memory neighbour lookup"
```

---

## Task 10: Persistence — sensor upsert, reading writes, and hourly rollup

**Files:**
- Create: `internal/store/store.go`, `internal/store/rollup.go`
- Test: `internal/store/store_test.go`, `internal/store/rollup_test.go`

**Interfaces:**
- Consumes: `quality.Scored`, `*pgxpool.Pool`
- Produces:
  - `store.Store` with `New(pool *pgxpool.Pool) *Store`
  - `(*Store).UpsertSensors(ctx context.Context, scored []quality.Scored) error`
  - `(*Store).WriteReadings(ctx context.Context, scored []quality.Scored) (int64, error)`
  - `(*Store).RollupHour(ctx context.Context, bucket time.Time) (int64, error)`

- [ ] **Step 1: Write the failing store tests**

Create `internal/store/store_test.go`:

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/db"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

func newStore(t *testing.T) (context.Context, *pgxpool.Pool, *store.Store) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, pool, store.New(pool)
}

func sample(id int64, metric string, value float64, flag quality.Flag, ts time.Time) quality.Scored {
	return quality.Scored{
		Reading: upstream.Reading{
			SensorID:   id,
			SensorType: "SDS011",
			Lon:        23.3327,
			Lat:        42.6957,
			Metric:     metric,
			Value:      value,
			Timestamp:  ts,
		},
		Flag: flag,
	}
}

func TestUpsertSensorsIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{
		sample(1, "P1", 24.3, quality.FlagOK, ts),
		sample(1, "P2", 16.1, quality.FlagOK, ts),
	}

	for i := 0; i < 2; i++ {
		if err := s.UpsertSensors(ctx, scored); err != nil {
			t.Fatalf("UpsertSensors: %v", err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("sensor count = %d, want 1", n)
	}
}

func TestUpsertSensorsStoresCoordinatesInBulgaria(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	if err := s.UpsertSensors(ctx, []quality.Scored{sample(1, "P1", 24.3, quality.FlagOK, ts)}); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}

	var lon, lat float64
	err := pool.QueryRow(ctx,
		`SELECT ST_X(location::geometry), ST_Y(location::geometry) FROM sensor WHERE sensor_id = 1`).
		Scan(&lon, &lat)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if lon < 22 || lon > 29 || lat < 41 || lat > 45 {
		t.Errorf("stored (%v, %v) is outside Bulgaria — coordinates swapped", lon, lat)
	}
}

func TestWriteReadingsPersistsFlags(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{
		sample(1, "P1", 24.3, quality.FlagOK, ts),
		sample(2, "P1", 900, quality.FlagSpatialOutlier, ts),
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}

	n, err := s.WriteReadings(ctx, scored)
	if err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}
	if n != 2 {
		t.Errorf("wrote %d rows, want 2", n)
	}

	var flag string
	err = pool.QueryRow(ctx,
		`SELECT quality::text FROM reading WHERE sensor_id = 2`).Scan(&flag)
	if err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if flag != "spatial_outlier" {
		t.Errorf("flag = %q, want %q — bad readings must be stored, not dropped", flag, "spatial_outlier")
	}
}

func TestWriteReadingsIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	scored := []quality.Scored{sample(1, "P1", 24.3, quality.FlagOK, ts)}

	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.WriteReadings(ctx, scored); err != nil {
			t.Fatalf("WriteReadings run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading count = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store/`
Expected: FAIL — `undefined: store.New`

- [ ] **Step 3: Implement the store**

Create `internal/store/store.go`:

```go
// Package store persists sensors and readings.
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/quality"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// UpsertSensors records every distinct sensor in the batch. Location is
// refreshed on conflict because sensors are occasionally relocated upstream.
func (s *Store) UpsertSensors(ctx context.Context, scored []quality.Scored) error {
	seen := make(map[int64]bool, len(scored))
	batch := &pgx.Batch{}

	for _, sc := range scored {
		r := sc.Reading
		if seen[r.SensorID] {
			continue
		}
		seen[r.SensorID] = true
		batch.Queue(
			`INSERT INTO sensor (sensor_id, sensor_type, location, last_seen)
			 VALUES ($1, $2, ST_SetSRID(ST_MakePoint($3, $4), 4326)::geography, $5)
			 ON CONFLICT (sensor_id) DO UPDATE
			   SET location = EXCLUDED.location,
			       sensor_type = EXCLUDED.sensor_type,
			       last_seen = EXCLUDED.last_seen,
			       active = true`,
			r.SensorID, r.SensorType, r.Lon, r.Lat, r.Timestamp)
	}
	if batch.Len() == 0 {
		return nil
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

// WriteReadings persists every scored reading, including flagged ones. Duplicate
// samples are ignored rather than erroring, so a re-run of the same cycle is
// safe. Returns the number of statements sent.
func (s *Store) WriteReadings(ctx context.Context, scored []quality.Scored) (int64, error) {
	batch := &pgx.Batch{}
	for _, sc := range scored {
		r := sc.Reading
		batch.Queue(
			`INSERT INTO reading (time, sensor_id, metric, value, quality)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (sensor_id, metric, time) DO UPDATE
			   SET value = EXCLUDED.value, quality = EXCLUDED.quality`,
			r.Timestamp, r.SensorID, r.Metric, r.Value, string(sc.Flag))
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	if err := s.pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return int64(len(scored)), nil
}

// TruncateHour returns the UTC hour bucket containing t.
func TruncateHour(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Write the failing rollup test**

Create `internal/store/rollup_test.go`:

```go
package store_test

import (
	"testing"
	"time"

	"airbg.org/internal/quality"
	"airbg.org/internal/store"
)

func TestRollupHourExcludesFlaggedReadings(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucket := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{
		sample(1, "P1", 20, quality.FlagOK, bucket.Add(1*time.Minute)),
		sample(1, "P1", 30, quality.FlagOK, bucket.Add(2*time.Minute)),
		// A spatial outlier in the same bucket. If this leaks into the average,
		// the published number is 316 instead of 25.
		sample(1, "P1", 900, quality.FlagSpatialOutlier, bucket.Add(3*time.Minute)),
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	if _, err := s.RollupHour(ctx, bucket); err != nil {
		t.Fatalf("RollupHour: %v", err)
	}

	var avg float64
	var count int
	err := pool.QueryRow(ctx,
		`SELECT avg_value, sample_count FROM reading_hourly
		 WHERE sensor_id = 1 AND metric = 'P1' AND bucket = $1`, bucket).
		Scan(&avg, &count)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if avg != 25 {
		t.Errorf("avg_value = %v, want 25 — flagged reading contaminated the average", avg)
	}
	if count != 2 {
		t.Errorf("sample_count = %d, want 2", count)
	}
}

func TestRollupHourIncludesNoNeighbours(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucket := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{
		sample(1, "P1", 20, quality.FlagOK, bucket.Add(1*time.Minute)),
		sample(1, "P1", 30, quality.FlagNoNeighbours, bucket.Add(2*time.Minute)),
	}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}
	if _, err := s.RollupHour(ctx, bucket); err != nil {
		t.Fatalf("RollupHour: %v", err)
	}

	var count int
	err := pool.QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1'`).
		Scan(&count)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if count != 2 {
		t.Errorf("sample_count = %d, want 2 — no_neighbours must count toward aggregates", count)
	}
}

func TestRollupHourIsIdempotent(t *testing.T) {
	ctx, pool, s := newStore(t)
	bucket := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	scored := []quality.Scored{sample(1, "P1", 20, quality.FlagOK, bucket.Add(time.Minute))}
	if err := s.UpsertSensors(ctx, scored); err != nil {
		t.Fatalf("UpsertSensors: %v", err)
	}
	if _, err := s.WriteReadings(ctx, scored); err != nil {
		t.Fatalf("WriteReadings: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := s.RollupHour(ctx, bucket); err != nil {
			t.Fatalf("RollupHour run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading_hourly rows = %d, want 1", n)
	}
}

func TestTruncateHour(t *testing.T) {
	got := store.TruncateHour(time.Date(2026, 1, 1, 12, 34, 56, 0, time.UTC))
	want := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("TruncateHour = %v, want %v", got, want)
	}
}
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `go test ./internal/store/ -run TestRollup`
Expected: FAIL — `s.RollupHour undefined`

- [ ] **Step 7: Implement the rollup**

Create `internal/store/rollup.go`:

```go
package store

import (
	"context"
	"time"
)

// RollupHour recomputes the hourly aggregate for one bucket from raw readings.
//
// Only readings whose quality flag permits aggregation are included, so a
// flagged sensor is structurally incapable of moving a published average
// (spec §5.3). Recomputing rather than incrementing makes the operation
// idempotent and safe to re-run over any bucket.
func (s *Store) RollupHour(ctx context.Context, bucket time.Time) (int64, error) {
	bucket = TruncateHour(bucket)

	tag, err := s.pool.Exec(ctx,
		`INSERT INTO reading_hourly
		     (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
		 SELECT $1, sensor_id, metric, avg(value), min(value), max(value), count(*)
		 FROM reading
		 WHERE time >= $1 AND time < $1 + interval '1 hour'
		   AND quality IN ('ok', 'no_neighbours')
		 GROUP BY sensor_id, metric
		 ON CONFLICT (sensor_id, metric, bucket) DO UPDATE
		   SET avg_value = EXCLUDED.avg_value,
		       min_value = EXCLUDED.min_value,
		       max_value = EXCLUDED.max_value,
		       sample_count = EXCLUDED.sample_count`,
		bucket)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/store
git commit -m "feat: add sensor and reading persistence with idempotent hourly rollup"
```

---

## Task 11: Ingest cycle

**Files:**
- Create: `internal/ingest/ingest.go`
- Modify: `cmd/airbg/main.go`
- Test: `internal/ingest/ingest_test.go`

**Interfaces:**
- Consumes: `upstream.Client`, `quality.Score`, `store.Store`
- Produces:
  - `ingest.Fetcher` interface: `Fetch(ctx context.Context) ([]upstream.Reading, error)`
  - `ingest.Stats{Fetched, Written int, Flagged map[quality.Flag]int, Err error}`
  - `ingest.Ingester` with `New(f Fetcher, s *store.Store, hist *quality.History) *Ingester`
  - `(*Ingester).RunOnce(ctx context.Context) (Stats, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/ingest/ingest_test.go`:

```go
package ingest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

type stubFetcher struct {
	readings []upstream.Reading
	err      error
}

func (s stubFetcher) Fetch(context.Context) ([]upstream.Reading, error) {
	return s.readings, s.err
}

func reading(id int64, metric string, value float64, lonOffset float64, ts time.Time) upstream.Reading {
	return upstream.Reading{
		SensorID:   id,
		SensorType: "BME280",
		Lon:        23.3327 + lonOffset,
		Lat:        42.6957,
		Metric:     metric,
		Value:      value,
		Timestamp:  ts,
	}
}

func newIngester(t *testing.T, f ingest.Fetcher) (context.Context, *store.Store, *ingest.Ingester) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st := store.New(pool)
	return ctx, st, ingest.New(f, st, quality.NewHistory(12))
}

func TestRunOnceStoresScoredReadings(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 3, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "temperature", 22, 0, ts),
		reading(2, "temperature", -10, 0.01, ts),
		reading(3, "temperature", -10.5, 0.02, ts),
		reading(4, "temperature", -9.5, 0.03, ts),
	}}
	ctx, _, ing := newIngester(t, f)

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Fetched != 4 {
		t.Errorf("Fetched = %d, want 4", stats.Fetched)
	}
	if stats.Written != 4 {
		t.Errorf("Written = %d, want 4 — flagged readings must still be stored", stats.Written)
	}
	if stats.Flagged[quality.FlagSpatialOutlier] != 1 {
		t.Errorf("spatial_outlier count = %d, want 1", stats.Flagged[quality.FlagSpatialOutlier])
	}
}

func TestRunOncePropagatesFetchFailure(t *testing.T) {
	wantErr := errors.New("upstream down")
	ctx, _, ing := newIngester(t, stubFetcher{err: wantErr})

	if _, err := ing.RunOnce(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestRunOnceHandlesEmptyBatch(t *testing.T) {
	ctx, _, ing := newIngester(t, stubFetcher{})

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce on empty batch: %v", err)
	}
	if stats.Written != 0 {
		t.Errorf("Written = %d, want 0", stats.Written)
	}
}

func TestRunOnceUpdatesRollup(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 3, 0, 0, time.UTC)
	f := stubFetcher{readings: []upstream.Reading{
		reading(1, "P1", 20, 0, ts),
	}}
	ctx, st, ing := newIngester(t, f)

	if _, err := ing.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// The rollup for the current hour must exist after a cycle.
	var count int
	err := st.Pool().QueryRow(ctx,
		`SELECT sample_count FROM reading_hourly WHERE sensor_id = 1 AND metric = 'P1'`).
		Scan(&count)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	if count != 1 {
		t.Errorf("sample_count = %d, want 1", count)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/ingest/`
Expected: FAIL — `undefined: ingest.New`

- [ ] **Step 3: Expose the pool from the store**

The rollup assertion needs read access. Append to `internal/store/store.go`:

```go
// Pool exposes the underlying connection pool for callers that need ad-hoc
// reads, such as tests and the API's chart queries.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }
```

- [ ] **Step 4: Implement the ingester**

Create `internal/ingest/ingest.go`:

```go
// Package ingest runs one poll-score-persist cycle.
package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

type Fetcher interface {
	Fetch(ctx context.Context) ([]upstream.Reading, error)
}

type Stats struct {
	Fetched int
	Written int
	Flagged map[quality.Flag]int
}

type Ingester struct {
	fetcher Fetcher
	store   *store.Store
	history *quality.History
}

func New(f Fetcher, s *store.Store, hist *quality.History) *Ingester {
	return &Ingester{fetcher: f, store: s, history: hist}
}

// RunOnce performs a single cycle: fetch, score, persist, roll up.
//
// An error from the fetch aborts the cycle, leaving previously stored data
// untouched — the caller keeps serving the last good snapshot (spec §10).
func (i *Ingester) RunOnce(ctx context.Context) (Stats, error) {
	readings, err := i.fetcher.Fetch(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("ingest: fetch: %w", err)
	}

	scored := quality.Score(readings, i.history)

	stats := Stats{Fetched: len(readings), Flagged: make(map[quality.Flag]int)}
	for _, s := range scored {
		stats.Flagged[s.Flag]++
	}

	if len(scored) == 0 {
		return stats, nil
	}

	if err := i.store.UpsertSensors(ctx, scored); err != nil {
		return stats, fmt.Errorf("ingest: upsert sensors: %w", err)
	}
	written, err := i.store.WriteReadings(ctx, scored)
	if err != nil {
		return stats, fmt.Errorf("ingest: write readings: %w", err)
	}
	stats.Written = int(written)

	// Roll up the bucket the batch landed in. Recomputing is idempotent, so
	// re-rolling the same hour every cycle is correct and cheap.
	bucket := store.TruncateHour(scored[0].Reading.Timestamp)
	if _, err := i.store.RollupHour(ctx, bucket); err != nil {
		return stats, fmt.Errorf("ingest: rollup: %w", err)
	}

	slog.Info("ingest cycle complete",
		"fetched", stats.Fetched,
		"written", stats.Written,
		"out_of_range", stats.Flagged[quality.FlagOutOfRange],
		"stuck", stats.Flagged[quality.FlagStuck],
		"spatial_outlier", stats.Flagged[quality.FlagSpatialOutlier],
	)
	return stats, nil
}

// Loop runs RunOnce on a ticker until the context is cancelled. A failed cycle
// is logged and retried on the next tick; it never terminates the loop.
func (i *Ingester) Loop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if _, err := i.RunOnce(ctx); err != nil {
			slog.Error("ingest cycle failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/ingest/ -v`
Expected: PASS

- [ ] **Step 6: Wire the subcommands**

Replace `cmd/airbg/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/upstream"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: airbg <migrate|collect|backfill|import-areas>")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(1)
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	switch os.Args[1] {
	case "migrate":
		if err := db.Migrate(ctx, pool); err != nil {
			slog.Error("migrate", "error", err)
			os.Exit(1)
		}
		slog.Info("migrations applied")

	case "collect":
		client := upstream.New(cfg.UpstreamURL, 30*time.Second)
		ing := ingest.New(client, store.New(pool), quality.NewHistory(12))
		ing.Loop(ctx, cfg.PollInterval)

	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}
```

- [ ] **Step 7: Verify the build and full test suite**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: build succeeds, all tests pass

- [ ] **Step 8: Commit**

```bash
git add internal/ingest internal/store cmd/airbg
git commit -m "feat: add ingest cycle and collect subcommand"
```

---

## Task 12: Area boundaries — schema, import, and sensor assignment

**Files:**
- Create: `internal/db/migrations/00004_areas.sql`, `internal/area/import.go`, `internal/area/assign.go`
- Modify: `cmd/airbg/main.go`
- Test: `internal/area/area_test.go`, `internal/area/testdata/sofia.geojson`

**Interfaces:**
- Consumes: `*pgxpool.Pool`
- Produces:
  - `area.Import(ctx context.Context, pool *pgxpool.Pool, path string, kind string) (int, error)`
  - `area.AssignSensors(ctx context.Context, pool *pgxpool.Pool) (int64, error)`

- [ ] **Step 1: Write the migration**

Create `internal/db/migrations/00004_areas.sql`:

```sql
-- +goose Up
CREATE TABLE area (
    slug    text PRIMARY KEY,
    kind    text NOT NULL CHECK (kind IN ('city', 'oblast', 'neighbourhood')),
    name_bg text NOT NULL,
    name_en text NOT NULL,
    geom    geography(MultiPolygon, 4326) NOT NULL
);

CREATE INDEX area_geom_idx ON area USING gist (geom);
CREATE INDEX area_kind_idx ON area (kind);

CREATE TABLE area_sensor (
    area_slug text   NOT NULL REFERENCES area(slug) ON DELETE CASCADE,
    sensor_id bigint NOT NULL REFERENCES sensor(sensor_id) ON DELETE CASCADE,
    PRIMARY KEY (area_slug, sensor_id)
);

CREATE INDEX area_sensor_sensor_idx ON area_sensor (sensor_id);

CREATE TABLE api_key (
    id         bigserial PRIMARY KEY,
    label      text NOT NULL,
    key_hash   text NOT NULL UNIQUE,
    rate_limit integer NOT NULL DEFAULT 60,
    quota      bigint NOT NULL DEFAULT 100000,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

-- +goose Down
DROP TABLE api_key;
DROP TABLE area_sensor;
DROP TABLE area;
```

- [ ] **Step 2: Create the test fixture**

Create `internal/area/testdata/sofia.geojson` — a square around central Sofia and a second around Plovdiv:

```json
{
  "type": "FeatureCollection",
  "features": [
    {
      "type": "Feature",
      "properties": {"slug": "sofia", "name_bg": "София", "name_en": "Sofia"},
      "geometry": {
        "type": "Polygon",
        "coordinates": [[[23.2, 42.6], [23.5, 42.6], [23.5, 42.8], [23.2, 42.8], [23.2, 42.6]]]
      }
    },
    {
      "type": "Feature",
      "properties": {"slug": "plovdiv", "name_bg": "Пловдив", "name_en": "Plovdiv"},
      "geometry": {
        "type": "Polygon",
        "coordinates": [[[24.6, 42.0], [24.9, 42.0], [24.9, 42.2], [24.6, 42.2], [24.6, 42.0]]]
      }
    }
  ]
}
```

Note the coordinate order: GeoJSON is `[longitude, latitude]`, matching PostGIS geography and the `upstream.Reading` field order.

- [ ] **Step 3: Write the failing tests**

Create `internal/area/area_test.go`:

```go
package area_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/area"
	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

func migrated(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return ctx, pool
}

func TestImportLoadsFeatures(t *testing.T) {
	ctx, pool := migrated(t)

	n, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if n != 2 {
		t.Fatalf("imported %d areas, want 2", n)
	}

	var nameBG string
	err = pool.QueryRow(ctx, `SELECT name_bg FROM area WHERE slug = 'sofia'`).Scan(&nameBG)
	if err != nil {
		t.Fatalf("read area: %v", err)
	}
	if nameBG != "София" {
		t.Errorf("name_bg = %q, want %q", nameBG, "София")
	}
}

func TestImportIsIdempotent(t *testing.T) {
	ctx, pool := migrated(t)

	for i := 0; i < 2; i++ {
		if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
			t.Fatalf("Import run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM area`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("area count = %d, want 2", n)
	}
}

// This is the §11.2 mandatory test: a sensor at Sofia's real coordinates must
// land inside the Sofia polygon. A latitude/longitude swap places it in the
// Indian Ocean and this assertion fails.
func TestAssignSensorsPlacesSofiaSensorInSofia(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	if _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	var slug string
	err = pool.QueryRow(ctx, `SELECT area_slug FROM area_sensor WHERE sensor_id = 1`).Scan(&slug)
	if err != nil {
		t.Fatalf("read assignment: %v — sensor was not placed in any area", err)
	}
	if slug != "sofia" {
		t.Errorf("area_slug = %q, want %q", slug, "sofia")
	}
}

func TestAssignSensorsSkipsSensorsOutsideEveryArea(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	// A rural sensor between the two cities, inside neither polygon.
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (2, 'SDS011', ST_SetSRID(ST_MakePoint(24.0, 42.4), 4326)::geography)`)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	if _, err := area.AssignSensors(ctx, pool); err != nil {
		t.Fatalf("AssignSensors: %v", err)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM area_sensor WHERE sensor_id = 2`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("assignments = %d, want 0 — a sensor outside every polygon must not be assigned", n)
	}
}

func TestAssignSensorsIsIdempotent(t *testing.T) {
	ctx, pool := migrated(t)

	if _, err := area.Import(ctx, pool, "testdata/sofia.geojson", "city"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO sensor (sensor_id, sensor_type, location)
		 VALUES (1, 'SDS011', ST_SetSRID(ST_MakePoint(23.3327, 42.6957), 4326)::geography)`)
	if err != nil {
		t.Fatalf("insert sensor: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := area.AssignSensors(ctx, pool); err != nil {
			t.Fatalf("AssignSensors run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM area_sensor`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("assignment count = %d, want 1", n)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/area/`
Expected: FAIL — `undefined: area.Import`

- [ ] **Step 5: Implement the importer**

Create `internal/area/import.go`:

```go
// Package area imports administrative boundaries and assigns sensors to them.
package area

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type featureCollection struct {
	Features []struct {
		Properties struct {
			Slug   string `json:"slug"`
			NameBG string `json:"name_bg"`
			NameEN string `json:"name_en"`
		} `json:"properties"`
		Geometry json.RawMessage `json:"geometry"`
	} `json:"features"`
}

// Import loads a GeoJSON FeatureCollection into the area table. Each feature
// needs slug, name_bg and name_en properties. Geometry is handed to PostGIS as
// raw GeoJSON and coerced to MultiPolygon, so both Polygon and MultiPolygon
// inputs work.
//
// GeoJSON coordinates are [longitude, latitude], which matches the storage
// convention. No axis swapping happens anywhere in this function, and none
// should be added.
func Import(ctx context.Context, pool *pgxpool.Pool, path, kind string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("area: read %s: %w", path, err)
	}

	var fc featureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		return 0, fmt.Errorf("area: parse %s: %w", path, err)
	}

	batch := &pgx.Batch{}
	for _, f := range fc.Features {
		if f.Properties.Slug == "" {
			return 0, fmt.Errorf("area: feature in %s has no slug property", path)
		}
		batch.Queue(
			`INSERT INTO area (slug, kind, name_bg, name_en, geom)
			 VALUES ($1, $2, $3, $4,
			         ST_Multi(ST_GeomFromGeoJSON($5))::geography)
			 ON CONFLICT (slug) DO UPDATE
			   SET kind = EXCLUDED.kind,
			       name_bg = EXCLUDED.name_bg,
			       name_en = EXCLUDED.name_en,
			       geom = EXCLUDED.geom`,
			f.Properties.Slug, kind, f.Properties.NameBG, f.Properties.NameEN,
			string(f.Geometry))
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, fmt.Errorf("area: import %s: %w", path, err)
	}
	return len(fc.Features), nil
}
```

- [ ] **Step 6: Implement assignment**

Create `internal/area/assign.go`:

```go
package area

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AssignSensors recomputes the sensor-to-area mapping by point-in-polygon
// containment. Sensors do not move, so this runs when boundaries or sensors
// change — never per request (spec §5.5).
//
// A sensor may belong to several areas at once: a Sofia sensor is in the Sofia
// city polygon, the Sofia-grad oblast, and its neighbourhood. That is intended,
// and the composite primary key keeps each pairing unique.
func AssignSensors(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx,
		`INSERT INTO area_sensor (area_slug, sensor_id)
		 SELECT a.slug, s.sensor_id
		 FROM area a
		 JOIN sensor s ON ST_Covers(a.geom, s.location)
		 ON CONFLICT (area_slug, sensor_id) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/area/ -v`
Expected: PASS (5 tests)

- [ ] **Step 8: Wire the subcommand**

Add to the `switch` in `cmd/airbg/main.go`, before `default`:

```go
	case "import-areas":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: airbg import-areas <path.geojson> <city|oblast|neighbourhood>")
			os.Exit(2)
		}
		n, err := area.Import(ctx, pool, os.Args[2], os.Args[3])
		if err != nil {
			slog.Error("import areas", "error", err)
			os.Exit(1)
		}
		assigned, err := area.AssignSensors(ctx, pool)
		if err != nil {
			slog.Error("assign sensors", "error", err)
			os.Exit(1)
		}
		slog.Info("areas imported", "areas", n, "assignments", assigned)
```

Add `"airbg.org/internal/area"` to the imports.

- [ ] **Step 9: Assign sensors after each ingest cycle**

New sensors appear continuously and must be placed. In `internal/ingest/ingest.go`, add the import `"airbg.org/internal/area"` and insert this immediately after the `RollupHour` call in `RunOnce`:

```go
	if _, err := area.AssignSensors(ctx, i.store.Pool()); err != nil {
		return stats, fmt.Errorf("ingest: assign sensors: %w", err)
	}
```

- [ ] **Step 10: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/db internal/area internal/ingest cmd/airbg
git commit -m "feat: add area boundaries, GeoJSON import, and sensor assignment"
```

---

## Task 13: Archive backfill

**Files:**
- Create: `internal/backfill/backfill.go`
- Modify: `cmd/airbg/main.go`
- Test: `internal/backfill/backfill_test.go`, `internal/backfill/testdata/sample.csv`

**Interfaces:**
- Consumes: `*store.Store`
- Produces:
  - `backfill.HourlyBucket{SensorID int64, Metric string, Bucket time.Time, Avg, Min, Max float64, Count int}`
  - `backfill.ParseCSV(r io.Reader, sensorID int64) ([]HourlyBucket, error)`
  - `backfill.WriteBuckets(ctx context.Context, pool *pgxpool.Pool, buckets []HourlyBucket) (int64, error)`

- [ ] **Step 1: Create the fixture**

Create `internal/backfill/testdata/sample.csv`. The archive uses semicolon-separated columns:

```
sensor_id;sensor_type;location;lat;lon;timestamp;P1;durP1;ratioP1;P2;durP2;ratioP2
12345;SDS011;500;42.696;23.333;2025-08-07T10:05:00;20.00;;;10.00;;
12345;SDS011;500;42.696;23.333;2025-08-07T10:35:00;30.00;;;14.00;;
12345;SDS011;500;42.696;23.333;2025-08-07T11:05:00;50.00;;;20.00;;
12345;SDS011;500;42.696;23.333;2025-08-07T11:35:00;70.00;;;30.00;;
```

- [ ] **Step 2: Write the failing tests**

Create `internal/backfill/backfill_test.go`:

```go
package backfill_test

import (
	"context"
	"os"
	"testing"
	"time"

	"airbg.org/internal/backfill"
	"airbg.org/internal/db"
	"airbg.org/internal/testsupport"
)

func TestParseCSVGroupsIntoHourlyBuckets(t *testing.T) {
	f, err := os.Open("testdata/sample.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	// Two hours × two metrics.
	if len(buckets) != 4 {
		t.Fatalf("len(buckets) = %d, want 4", len(buckets))
	}

	want := time.Date(2025, 8, 7, 10, 0, 0, 0, time.UTC)
	var found bool
	for _, b := range buckets {
		if b.Metric != "P1" || !b.Bucket.Equal(want) {
			continue
		}
		found = true
		if b.Avg != 25 {
			t.Errorf("P1 10:00 avg = %v, want 25", b.Avg)
		}
		if b.Min != 20 || b.Max != 30 {
			t.Errorf("P1 10:00 min/max = %v/%v, want 20/30", b.Min, b.Max)
		}
		if b.Count != 2 {
			t.Errorf("P1 10:00 count = %d, want 2", b.Count)
		}
	}
	if !found {
		t.Fatal("no P1 bucket for 10:00")
	}
}

func TestParseCSVSkipsNonCanonicalColumns(t *testing.T) {
	f, err := os.Open("testdata/sample.csv")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	buckets, err := backfill.ParseCSV(f, 12345)
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	for _, b := range buckets {
		if b.Metric != "P1" && b.Metric != "P2" {
			t.Errorf("non-canonical metric %q in output", b.Metric)
		}
	}
}

func TestWriteBucketsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	buckets := []backfill.HourlyBucket{{
		SensorID: 12345,
		Metric:   "P1",
		Bucket:   time.Date(2025, 8, 7, 10, 0, 0, 0, time.UTC),
		Avg:      25, Min: 20, Max: 30, Count: 2,
	}}

	for i := 0; i < 2; i++ {
		if _, err := backfill.WriteBuckets(ctx, pool, buckets); err != nil {
			t.Fatalf("WriteBuckets run %d: %v", i, err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("reading_hourly rows = %d, want 1", n)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/backfill/`
Expected: FAIL — `undefined: backfill.ParseCSV`

- [ ] **Step 4: Implement backfill**

Create `internal/backfill/backfill.go`:

```go
// Package backfill imports historical hourly data from the public
// sensor.community archive (archive.sensor.community), which publishes one CSV
// per sensor per day.
//
// Only hourly buckets are imported. Raw rows are dropped after 30 days by the
// retention policy, so importing raw history would be deleted almost
// immediately (spec §5.2, §5.3).
package backfill

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"airbg.org/internal/upstream"
)

const archiveTimeLayout = "2006-01-02T15:04:05"

type HourlyBucket struct {
	SensorID int64
	Metric   string
	Bucket   time.Time
	Avg      float64
	Min      float64
	Max      float64
	Count    int
}

type accumulator struct {
	sum   float64
	min   float64
	max   float64
	count int
}

type key struct {
	metric string
	bucket time.Time
}

// ParseCSV reads one archive CSV and aggregates it into hourly buckets.
// Unparseable rows are skipped rather than failing the file — an archive day
// with one corrupt line still yields a usable import.
func ParseCSV(r io.Reader, sensorID int64) ([]HourlyBucket, error) {
	reader := csv.NewReader(r)
	reader.Comma = ';'
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("backfill: read header: %w", err)
	}

	tsCol := -1
	metricCols := map[int]string{}
	for i, name := range header {
		if name == "timestamp" {
			tsCol = i
			continue
		}
		if upstream.IsCanonicalMetric(name) {
			metricCols[i] = name
		}
	}
	if tsCol == -1 {
		return nil, fmt.Errorf("backfill: no timestamp column")
	}

	acc := map[key]*accumulator{}

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed row, keep the file
		}
		if tsCol >= len(record) {
			continue
		}
		ts, err := time.Parse(archiveTimeLayout, record[tsCol])
		if err != nil {
			continue
		}
		bucket := ts.UTC().Truncate(time.Hour)

		for col, metric := range metricCols {
			if col >= len(record) || record[col] == "" {
				continue
			}
			value, err := strconv.ParseFloat(record[col], 64)
			if err != nil {
				continue
			}
			if metric == "pressure" {
				value /= 100 // archive matches the live API: Pascals
			}
			k := key{metric: metric, bucket: bucket}
			a, ok := acc[k]
			if !ok {
				acc[k] = &accumulator{sum: value, min: value, max: value, count: 1}
				continue
			}
			a.sum += value
			a.count++
			if value < a.min {
				a.min = value
			}
			if value > a.max {
				a.max = value
			}
		}
	}

	buckets := make([]HourlyBucket, 0, len(acc))
	for k, a := range acc {
		buckets = append(buckets, HourlyBucket{
			SensorID: sensorID,
			Metric:   k.metric,
			Bucket:   k.bucket,
			Avg:      a.sum / float64(a.count),
			Min:      a.min,
			Max:      a.max,
			Count:    a.count,
		})
	}
	return buckets, nil
}

// WriteBuckets upserts hourly buckets. Re-importing the same day is safe.
func WriteBuckets(ctx context.Context, pool *pgxpool.Pool, buckets []HourlyBucket) (int64, error) {
	batch := &pgx.Batch{}
	for _, b := range buckets {
		batch.Queue(
			`INSERT INTO reading_hourly
			     (bucket, sensor_id, metric, avg_value, min_value, max_value, sample_count)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 ON CONFLICT (sensor_id, metric, bucket) DO UPDATE
			   SET avg_value = EXCLUDED.avg_value,
			       min_value = EXCLUDED.min_value,
			       max_value = EXCLUDED.max_value,
			       sample_count = EXCLUDED.sample_count`,
			b.Bucket, b.SensorID, b.Metric, b.Avg, b.Min, b.Max, b.Count)
	}
	if batch.Len() == 0 {
		return 0, nil
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return 0, err
	}
	return int64(len(buckets)), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/backfill/ -v`
Expected: PASS

- [ ] **Step 6: Run the full suite**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/backfill cmd/airbg
git commit -m "feat: add archive CSV backfill into hourly buckets"
```

---

## Task 14: End-to-end verification against real upstream data

**Files:**
- Create: `internal/ingest/e2e_test.go`
- Test: the same file

**Interfaces:**
- Consumes: everything built so far

- [ ] **Step 1: Write the end-to-end test**

Create `internal/ingest/e2e_test.go`:

```go
package ingest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"airbg.org/internal/db"
	"airbg.org/internal/ingest"
	"airbg.org/internal/quality"
	"airbg.org/internal/store"
	"airbg.org/internal/testsupport"
	"airbg.org/internal/upstream"
)

// TestEndToEndFromRecordedPayload runs the whole pipeline against the recorded
// upstream fixture served over HTTP: fetch, normalise, score, persist, roll up.
func TestEndToEndFromRecordedPayload(t *testing.T) {
	payload, err := os.ReadFile("../upstream/testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	ctx := context.Background()
	pool := testsupport.NewPostgres(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	client := upstream.New(srv.URL, 10*time.Second)
	ing := ingest.New(client, store.New(pool), quality.NewHistory(12))

	stats, err := ing.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if stats.Written != 5 {
		t.Errorf("Written = %d, want 5", stats.Written)
	}

	var sensors int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sensor`).Scan(&sensors); err != nil {
		t.Fatalf("count sensors: %v", err)
	}
	if sensors != 2 {
		t.Errorf("sensor count = %d, want 2 (the third fixture entry has no coordinates)", sensors)
	}

	// Every stored sensor must be inside Bulgaria.
	var outside int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM sensor
		 WHERE ST_X(location::geometry) NOT BETWEEN 22 AND 29
		    OR ST_Y(location::geometry) NOT BETWEEN 41 AND 45`).Scan(&outside)
	if err != nil {
		t.Fatalf("bounds check: %v", err)
	}
	if outside != 0 {
		t.Errorf("%d sensors stored outside Bulgaria — coordinates swapped", outside)
	}

	var hourly int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reading_hourly`).Scan(&hourly); err != nil {
		t.Fatalf("count rollup: %v", err)
	}
	if hourly == 0 {
		t.Error("no hourly rollup rows after a cycle")
	}
}

// TestUpstreamContractLive is opt-in and hits the real API. It exists to detect
// upstream schema drift. Run with: AIRBG_LIVE_TEST=1 go test ./internal/ingest/
func TestUpstreamContractLive(t *testing.T) {
	if os.Getenv("AIRBG_LIVE_TEST") == "" {
		t.Skip("set AIRBG_LIVE_TEST=1 to run against the live upstream API")
	}

	client := upstream.New(
		"https://data.sensor.community/airrohr/v1/filter/country=BG",
		30*time.Second)

	readings, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("live fetch: %v", err)
	}
	if len(readings) == 0 {
		t.Fatal("live fetch returned no readings — upstream schema may have changed")
	}
	for _, r := range readings {
		if r.Lon < 22 || r.Lon > 29 || r.Lat < 41 || r.Lat > 45 {
			t.Fatalf("sensor %d at (%v, %v) is outside Bulgaria", r.SensorID, r.Lon, r.Lat)
		}
	}
}
```

- [ ] **Step 2: Run the test**

Run: `go test ./internal/ingest/ -run TestEndToEnd -v`
Expected: PASS

- [ ] **Step 3: Verify against live upstream once, manually**

Run: `AIRBG_LIVE_TEST=1 go test ./internal/ingest/ -run TestUpstreamContractLive -v`
Expected: PASS. If it fails, the recorded fixture is stale — re-record it before continuing.

- [ ] **Step 4: Run everything**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest
git commit -m "test: add end-to-end pipeline test and opt-in live contract check"
```

---

## Task 15: Container image and operational README

**Files:**
- Create: `Dockerfile`, `README.md`
- Modify: `docker-compose.yml`

**Interfaces:**
- Consumes: the built binary

- [ ] **Step 1: Write the Dockerfile**

Create `Dockerfile`:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/airbg ./cmd/airbg

# Distroless: no shell, no package manager, no writable document root. Nothing
# dropped into the container can be executed the way anything in the legacy
# www-root/ could be (spec §4.1).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/airbg /airbg
USER nonroot:nonroot
ENTRYPOINT ["/airbg"]
CMD ["collect"]
```

- [ ] **Step 2: Add the collector to compose**

Append to `docker-compose.yml`:

```yaml
  collector:
    build: .
    command: ["collect"]
    environment:
      AIRBG_DATABASE_URL: postgres://airbg:airbg@db:5432/airbg?sslmode=disable
    depends_on:
      db:
        condition: service_healthy
    read_only: true
    cap_drop: ["ALL"]
    security_opt: ["no-new-privileges:true"]
```

- [ ] **Step 3: Write the README**

Create `README.md`:

````markdown
# airbg.org

Air quality map for Bulgaria, using data from the sensor.community citizen
sensor network.

This repository is a rewrite of the legacy PHP application. See
`docs/superpowers/specs/2026-08-07-airbg-phase1-design.md` for the design and
`ANALYSIS.md` for the audit of the code it replaces.

## Running locally

```bash
docker compose up -d db
export AIRBG_DATABASE_URL='postgres://airbg:airbg@localhost:5432/airbg?sslmode=disable'
go run ./cmd/airbg migrate
go run ./cmd/airbg collect
```

## Subcommands

| Command | Purpose |
|---|---|
| `migrate` | Apply schema migrations |
| `collect` | Poll sensor.community on a loop, score, and store |
| `import-areas <file.geojson> <city\|oblast\|neighbourhood>` | Load boundaries and assign sensors |

## Configuration

| Variable | Required | Default |
|---|---|---|
| `AIRBG_DATABASE_URL` | yes | — |
| `AIRBG_UPSTREAM_URL` | no | `https://data.sensor.community/airrohr/v1/filter/country=BG` |
| `AIRBG_POLL_INTERVAL` | no | `5m` |

No secret is ever committed. Configuration is environment-only.

## Tests

```bash
go test ./... -race
```

Integration tests start real PostgreSQL containers via testcontainers, so Docker
must be running. The first run pulls `timescale/timescaledb-ha:pg16`.

To check the live upstream contract:

```bash
AIRBG_LIVE_TEST=1 go test ./internal/ingest/ -run TestUpstreamContractLive
```

## Data and attribution

- Sensor data: [sensor.community](https://sensor.community/)
- Boundaries: © OpenStreetMap contributors, ODbL
````

- [ ] **Step 4: Verify the image builds**

Run: `docker build -t airbg:dev .`
Expected: build succeeds

- [ ] **Step 5: Commit**

```bash
git add Dockerfile README.md docker-compose.yml
git commit -m "feat: add distroless container image and operational README"
```

---

## Definition of done

- [ ] `go build ./... && go vet ./... && go test ./... -race` passes
- [ ] `docker build -t airbg:dev .` succeeds
- [ ] `AIRBG_LIVE_TEST=1 go test ./internal/ingest/ -run TestUpstreamContractLive` passes against the real API
- [ ] Running `collect` against a live database populates `sensor`, `reading`, and `reading_hourly`
- [ ] `TestSensorCoordinateOrder` and `TestAssignSensorsPlacesSofiaSensorInSofia` both pass — the coordinate-order hazard is guarded
- [ ] `TestRealPMEpisodeIsNotFlagged` and `TestLocalPMSourceIsNotFlagged` pass — real pollution survives quality scoring
- [ ] No `CLAUDE.md` in any commit

## What this plan does not build

Deliberately out of scope, covered by the following plans:

- Snapshot construction and the tiered payloads (spec §7.1) — Plan 2
- HTTP handlers, middleware, rate limiting, partner API (spec §7, §8) — Plan 2
- Frontend, tiles, MapLibre, charts, area pages, i18n (spec §9) — Plan 3
- Coverage-threshold presentation (spec §5.7) — enforced when areas are rendered, Plan 2
- SigNoz metric export (spec §10) — Plan 2, alongside the HTTP server
