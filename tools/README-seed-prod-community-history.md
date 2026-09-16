# Seeding production with community history

`seed-prod-community-history.sh` copies `sensor.community` history from staging
into production. It was written for a one-off: production ingests live but has
no upstream archive to rebuild from, so its community history began the hour it
was switched on, while staging had been running for seventeen days. Both hold
the same sensors under the same upstream ids, which is what makes staging's
rollups a usable seed.

Run it again only if production loses community history the same way. It is
additive and re-runnable — every insert is `ON CONFLICT DO NOTHING`, so it can
never overwrite a row production already has.

## What it copies, and why that list

| Table | Copied | Reason |
|---|---|---|
| `sensor` | community rows | Rollup rows join to it; staging's set is a superset |
| `reading_hourly` | community rows | Every historical read goes through it |
| `reading` | community rows, a day at a time | Short sensor charts; expires at 30 days |

`reading_hourly` is the part that matters. Replay frames, the 24h/48h/7d map
windows and the area series all read it (`internal/store/frames.go`,
`window.go`, `aggregate.go`) — seeding it alone is what makes history appear.
Raw `reading` only serves fine-grained recent charts and the retention policy
in migration 00003 drops it after 30 days, so a backfill of it is temporary by
construction.

## The filter that keeps it safe

Everything is filtered on `source = 'sensor.community'`, and both staged tables
are checked for an official station before anything is inserted. This is
load-bearing, not defensive dressing: staging's EEA rollup held 397 rows against
production's 731,361, because official-station ingest is a production concern.
An unfiltered copy would replace a nine-month archive with five days of noise.

## Rolling back

Nothing else writes the seeded range, so removing it is a bounded delete —
community rows in `reading_hourly` and `reading` with a timestamp before the
first hour production collected for itself. `rollup_watermark` is never touched:
it tracks the newest rolled-up bucket and every seeded bucket is older.

## Operational notes

- psql runs inside each host's database container so credentials stay in the
  container environment and never reach the script, a log, or a transcript.
- Raw readings go a day at a time. Thirty-six million rows in one statement is
  one transaction, one failure mode and no way to resume; a day is ~2.4M rows
  and re-running any single day is free.
- Rows land through an `UNLOGGED` staging table because `COPY` cannot express
  `ON CONFLICT` and both targets carry a unique index.
