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
| `backfill <sensor_id> <archive-csv-path>` | Load a sensor.community archive CSV into `reading_hourly` |

## Configuration

All configuration comes from environment variables (`internal/config/config.go`).
There are no config files and no secrets in the repo.

| Variable | Required | Default |
|---|---|---|
| `AIRBG_DATABASE_URL` | yes | — |
| `AIRBG_UPSTREAM_URL` | no | `https://data.sensor.community/airrohr/v1/filter/country=BG` |
| `AIRBG_POLL_INTERVAL` | no | `5m` |

No secret is ever committed. Configuration is environment-only.

## Database

PostgreSQL 16 with **both PostGIS and TimescaleDB** is required. Use the
`timescale/timescaledb-ha:pg16` image, as in `docker-compose.yml` — the plain
`timescaledb` image does not include PostGIS, and the app will fail to start
against it (area boundary storage and sensor assignment depend on PostGIS
geometry types).

`reading` (raw readings) has a 30-day retention policy. `reading_hourly` (the
hourly rollup) retains 2 years and is a plain hypertable, deliberately not a
continuous aggregate — the rollup is written by the ingest daemon itself, not
computed by TimescaleDB.

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

This test makes a real network call to data.sensor.community and is skipped
unless `AIRBG_LIVE_TEST=1` is set.

## Container image

```bash
docker build -t airbg:dev .
```

Produces a distroless, non-root image with a single static binary as its
entrypoint (default command `collect`). No shell, no package manager.

## Known limitations

These are known, deliberately unfixed issues carried forward for a project
owner decision. They are not blockers for Phase 1, but an operator should be
aware of them.

- **No rollup watermark.** Raw `reading` rows are dropped by the 30-day
  retention policy, while `reading_hourly` is built from them by a stateless
  per-bucket rollup. If the rollup falls behind (outage, crash loop), raw
  rows can be deleted before they are ever aggregated — silently, with no
  error surfaced.
- **Upstream `country` is not a trustworthy geographic filter.** At least one
  observed sensor (48524) reports `country: "BG"` while its coordinates place
  it in London.
- **Pressure range floor.** `internal/quality/ranges.go` flags station
  pressure below 800 hPa as `out_of_range`, but Bulgaria's altitude range
  produces legitimate lower readings — 7.7% of live readings were observed
  below the floor. Those readings are excluded from the rollup.

## Data and attribution

- Sensor data: [sensor.community](https://sensor.community/)
- Boundaries: © OpenStreetMap contributors, ODbL
