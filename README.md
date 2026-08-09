# airbg.org

Air quality map for Bulgaria, using data from the sensor.community citizen
sensor network.

This repository is a rewrite of the legacy PHP application. See
`docs/superpowers/specs/2026-08-07-airbg-phase1-design.md` for the design and
`ANALYSIS.md` for the audit of the code it replaces.

## Running locally

`docker-compose.yml` is for local development only — it publishes Postgres
on the host with a plaintext password and none of the hardening a production
deployment needs. Do not use it in production.

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
| `import-areas <file.geojson> <city\|oblast\|neighbourhood\|country>` | Load boundaries and assign sensors |
| `backfill <sensor_id> <archive-csv-path>` | Load a sensor.community archive CSV into `reading_hourly` |
| `purge-outside-boundary` | Delete sensors (and their stored readings) outside the `country` boundary; refuses to run if no `country` boundary is imported |

**Importing a `country`-kind boundary is a hard prerequisite for ingesting anything.**
`collect` filters every incoming sensor against the `area.kind = 'country'`
boundary instead of trusting upstream's self-reported `country`
field, which is unreliable (see Known limitations). Until `import-areas
<file.geojson> country` has been run at least once, `collect` fails closed:
it polls upstream successfully but stores zero rows, every cycle, logging an
ERROR that names the exact remedy command. Nothing else in the system's
normal signals (the rollup backlog stays at 0, there are no other errors)
will look unusual, so this is easy to miss if the import step is skipped.

## Configuration

All configuration comes from environment variables (`internal/config/config.go`).
There are no config files and no secrets in the repo.

| Variable | Required | Default |
|---|---|---|
| `AIRBG_DATABASE_URL` | yes | — |
| `AIRBG_UPSTREAM_URL` | no | `https://data.sensor.community/airrohr/v1/filter/country=BG` |
| `AIRBG_POLL_INTERVAL` | no | `5m` |

`AIRBG_POLL_INTERVAL` must be at least **30s**. Anything smaller is rejected at
startup: `0s` and negative values would panic `time.NewTicker`, and a
sub-minimum positive value silently polls the public, volunteer-run
data.sensor.community API hundreds of times more often than intended — a good
way to get the collector's IP banned.

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

The three limitations previously listed here — no rollup watermark, untrusted
upstream `country`, and an 800 hPa pressure floor — have all been fixed. The
rollup now advances a transactional watermark and drains its backlog, alerting
at ERROR long before the 30-day raw retention could delete unaggregated rows;
sensors are filtered by `ST_Covers` against an imported national boundary
rather than by the self-declared `country` field; and the pressure floor is
650 hPa (~3600 m), above any Bulgarian sensor site.

What remains, that an operator should be aware of:

- **You must supply your own national boundary.** No production-grade
  Bulgaria outline ships with this repo. The polygon under
  `internal/area/testdata/` is a hand-authored test fixture, materially wrong
  along the eastern border, and is used only by tests — it is never loaded at
  runtime. Import an authoritative outline (Natural Earth or equivalent)
  before running `collect`; until you do, the collector fails closed and
  stores nothing.
- **Sensors ingested before the boundary filter existed are not removed
  automatically.** Rows stored while upstream `country` was still trusted
  persist, including foreign sensors. Run `purge-outside-boundary` once after
  importing the national boundary to delete them. It is deliberately never
  automatic — it deletes stored data, so it is an explicit operator action,
  and it refuses to run when no national boundary is present.
- **The container test suite can flake under load.** A single transient
  failure in `internal/store` has been observed during a full `-race` run,
  self-resolving on rerun. Suspected testcontainers resource contention
  rather than a code defect; worth watching if it recurs in CI.

## Data and attribution

- Sensor data: [sensor.community](https://sensor.community/)
- Boundaries: © OpenStreetMap contributors, ODbL
