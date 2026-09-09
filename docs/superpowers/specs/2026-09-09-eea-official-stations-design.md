# Official stations on the map — design

Adds Bulgaria's 32 government reference stations (ИАОС/ExEA, published through
the European Environment Agency) alongside the sensor.community devices the map
already carries, as a second layer the reader can switch on and off.

## 1. Where the data comes from

The government's own portal is not a usable source. Its map calls
`GET https://eea.government.bg/kav/ajax/getStationsForMapJson?hours=N`, which on
2026-09-09 returned all 32 stations with coordinates, Bulgarian names, addresses
and station types — and `value: 0`, empty `componentName`, empty `dt` for every
one of them. The per-station endpoint `getStationDataJson` returned a zero-length
body. That server also presents an incomplete TLS chain (leaf only, no Sectigo
intermediate), so any client of it needs an explicit CA bundle.

The same stations reach us through the EEA Air Quality Download API, which is
live and about one hour behind:

| | |
|---|---|
| URL list | `POST https://eeadmz1-downloads-api-appservice.azurewebsites.net/ParquetFile/urls` |
| body | `{"countries":["BG"],"cities":[],"pollutants":[],"dataset":1,"source":"API"}` |
| dataset 1 | UTD — unverified, near-real-time |
| result | 141 sampling points over 32 stations, ~717 KB each, ~99 MB total |
| format | Parquet, snappy, 3 row groups of 5000 rows |
| columns | `Samplingpoint, Pollutant, Start, End, Value, Unit, AggType, Validity, Verification, ResultTime, DataCapture, FkObservationLog` |
| readings | hourly means in `ug.m-3`, history back to 2025-01-01 |

Coordinates are not in that API. They come from
`https://discomap.eea.europa.eu/map/fme/metadata/PanEuropean_metadata.csv`
(26 MB, last modified 2024-03-11), which carries `SamplingPoint`,
`AirQualityStationEoICode`, `Longitude`, `Latitude`, `AirQualityStationType` and
`AirQualityStationArea`. All 141 current sampling points resolve in it. The file
is frozen, so a station commissioned after March 2024 would arrive without
coordinates — the collector must log and skip such a sampling point rather than
guess.

Pollutant coverage, in sampling points: SO₂ 28, PM10 27, NO₂ 25, O₃ 20, CO 18,
benzene 18, PM2.5 4, NOx-as-NO₂ 1.

## 2. Decisions taken

- **All eight pollutants** are ingested, not just the two the site charts today.
- **The layers are independent and individually selectable.** Choosing a metric
  no citizen sensor measures greys the community layer out in the layer control
  and says why, rather than leaving a blank map unexplained.
- **Both sources feed the aggregates** — area averages and the hex grid — with
  the source composition reported alongside every number. Existing averages will
  change value on the day this ships; that is expected, not a regression.
- **The full history is backfilled** on first ingest: ~2M hourly rows from
  2025-01-01, which the downloaded files already contain.
- **One new direct dependency** is permitted, a Go Parquet reader, because the
  EEA publishes no other format. This is a scoped exception; the rest of the
  no-new-dependency rule stands.

## 3. Data model

A new goose migration, `00011_sources.sql`:

- `sensor.source text not null default 'sensor.community'`, constrained to
  `'sensor.community' | 'eea'`.
- `sensor.source_ref text` — the EEA sampling point (`BG/SPO-BG0070A_06001_100`)
  for official rows, null for community ones, unique where not null.
- `sensor.station_code text` and `sensor.station_name text` — the EoI code and
  the Bulgarian station name, so a panel can name the station rather than a
  number.
- `sensor.station_type text`, `sensor.station_area text` — the EEA
  classification (traffic/industrial/background, urban/suburban/rural), which is
  the honest way to say what a reference station is measuring.
- Official sensors take synthetic `sensor_id`s from a reserved high range so the
  existing bigint primary key and every foreign key stay unchanged.

`reading` and `reading_hourly` need no new column: `metric` is already free
text, and EEA's `Validity` maps onto the existing `quality` vocabulary — a row
with `Validity != 1` is stored with a non-usable quality rather than dropped, so
the panel can distinguish "the station reported nothing" from "the station is
not there".

## 4. Ingest

A new `internal/upstream/eea` package, polled hourly (the data's own cadence):

1. `POST /ParquetFile/urls` for the configured countries.
2. Conditional `GET` per sampling point with `If-Modified-Since`; unchanged files
   cost a 304.
3. Decode, keep rows newer than the stored watermark, map `Pollutant` codes to
   metric names (1 → SO2, 5 → PM10, 7 → O3, 8 → NO2, 10 → CO, 20 → C6H6,
   6001 → PM2.5, 9 → NOX).
4. Write through the existing batching path, parameterised, into the collector
   pool.

The metadata CSV is fetched on a much longer cycle (weekly is generous for a
file that has not changed since March 2024) and cached on disk.

A worst-case pass is ~99 MB, so ~72 GB/month — ingress, which is unmetered on
the hosts under consideration. Configuration lives under a new `eea:` block in
`airbg.yaml` with the same shape as `upstream:`: url, countries, poll interval,
minimum poll interval, request timeout, max payload.

## 5. Frontend

- A layer control beside the metric menu, two checkboxes, deep-linked in the
  hash (`#layers=community,official`) the way `#metric=` already is.
- Two MapLibre sources rather than one, so a layer toggle is a visibility change
  and not a refetch. `paintSource`'s `airbg:paint` event gains the source name
  it already carries, and the "one zoom, one redraw" e2e invariant extends to
  the new layer.
- Official markers are shaped differently from community ones — a reference
  station and a €30 nephelometer should not look identical at a glance.
- `SensorPanel` gains the station's name, type and area for official stations,
  and says which network the reading came from.

## 6. Attribution

Below the map, replacing the single `DataAttribution` constant in
`internal/api/overview.go` with one entry per source, rendered as a short block:

- Citizen data from sensor.community contributors, ODbL 1.0 — linking to
  <https://maps.sensor.community/>.
- Official data from the Executive Environment Agency (ИАОС) via the European
  Environment Agency's air quality programme — linking to
  <https://eea.government.bg/kav/> and to the EEA download service.

Both strings are i18n keys in `internal/i18n/bg.json` and `en.json`, so the
overlay directory can correct them without a rebuild.

## 7. Resource impact

None that changes the hosting decision.

| | |
|---|---|
| ongoing rows | 141 × 24 = 3,384/day, 0.14% of current ingest |
| backfill | ~2.07M rows into `reading_hourly`, ~500 MB, one pass |
| steady growth | ~600 MB over the 2-year rollup retention |
| bandwidth | ~99 MB/hour worst case, ~72 GB/month ingress |
| CPU | decoding 141 snappy files hourly — seconds, in a once-an-hour spike |

The 4 vCPU / 8 GB / 80 GB sizing absorbs all of it.

## 8. Known risks

- The metadata CSV is frozen at 2024-03-11. It covers every current sampling
  point, but it is a single point of failure for coordinates and has no
  successor identified.
- PM2.5 exists at only 4 of 32 stations, and PM2.5 is the site's default metric,
  so the official layer looks nearly empty until the reader switches to PM10.
- Feeding both sources into area averages changes numbers that are already
  published.
- The five gas metrics have no community counterpart, so half the metric menu
  draws a single-layer map.
