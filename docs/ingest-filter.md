# The ingest filter

What decides whether a sensor's reading is stored, and why the answer has two
independent parts.

## There are two filters, not one

Widening "the country filter" means changing both. Changing either alone
produces a system that looks like it widened and did not.

**1. The fetch filter — what upstream sends us.**

`upstream.url` is `https://data.sensor.community/airrohr/v1/filter/`, and the
client appends `country=<codes>` built from `upstream.countries`
(`internal/upstream/client.go`, `fetchURL`). This is a bandwidth decision, not a
correctness one: the global feed is tens of megabytes per poll and almost all of
it would be discarded a moment later. It is not a trust boundary — the filter
runs on sensor.community's own `country` field, which is exactly the field the
next stage refuses to believe.

Config validation rejects a `upstream.url` that still ends in its own
`country=` segment. sensor.community honours the first filter it sees, so a
leftover segment would silently keep the fetch narrow while the boundary set
widened — and a country that fetches nothing is indistinguishable from a country
with no sensors.

**2. The boundary filter — what we agree to store.**

`area.FilterByBoundary` (`internal/area/filter.go`) tests each sensor's
coordinates with PostGIS `ST_Covers` against the imported national boundary
polygons whose `country_code` is in `upstream.countries`. This is the trust
boundary, and it does not read the country field at all.

It cannot, because the field lies. Sensor 48524 reports country `BG` from
London's coordinates. On a map of Bulgaria that is one marker in the North Sea;
in an oblast average it is a reading from another climate. Only geometry
decides, which is why the allow list names countries by code rather than
filtering on what a sensor claims: **the code selects which polygons to test
against, never what a sensor says about itself.**

## Why it fails closed

If no enabled country has an imported boundary, `BoundaryPresent` is false and
the ingester drops the entire batch rather than storing it unfiltered. The
alternative — store everything when the filter is unavailable — means one
missing import turns the map into a global sensor list with a Bulgarian
title. A dropped cycle is recoverable in five minutes; a polluted store is not.

An operator who widens the allow list before sourcing the geometry hits a middle
state: the enabled countries that *do* have boundaries keep ingesting, and the
ones that do not are named in a startup warning (`MissingCountries`) telling them
to run `airbg import-areas`. Not an error — it is a normal intermediate state —
but not silent either, for the same reason as above.

## Where it sits in the pipeline

The filter runs **before** `quality.Score`, and the ordering is load-bearing.
The scorer's spatial-outlier check compares a reading against the median and MAD
of its neighbours. A London sensor admitted into a Bulgarian batch does not just
add a wrong point; it drags the median that every *correct* reading is judged
against. Filtering first means the statistics are computed over a population
that is actually a population.

Widening to the neighbours improves this rather than diluting it. A sensor in
Kyustendil previously had neighbours on one side only — the boundary cut its
sample in half at exactly the place where a cross-border smog event would be
visible. Now the median it is compared against includes the sensors 20 km west
in North Macedonia.

## What a country code is for after ingest

`FilterByBoundary` returns the code of the polygon that admitted each sensor, and
`store.UpsertSensors` writes it to `sensor.country_code`. Two consequences worth
knowing:

- The hex grid names each bin's country from these codes (modal value within the
  bin, ties broken by code so the ETag is stable). It is therefore geometric,
  never self-reported.
- `area.AssignSensors` deliberately excludes `kind = 'country'` from
  `area_sensor`. Foreign sensors get no browsable area and appear in no oblast
  or city aggregate — they exist in the hex grid and nowhere else. Adding a
  neighbour widens the map's data, not its navigation.

A sensor already in the table that is missing from a later cycle's map keeps its
existing code (`COALESCE(EXCLUDED.country_code, sensor.country_code)`) rather
than being reset to NULL by a cycle that simply did not see it.

## Adding a country

1. Add the alpha-2 code to `upstream.countries` in `airbg.yaml`.
2. Commit `data/boundaries/<slug>.geojson` with an `iso_a2` property. The code
   comes from the file, not from a CLI flag, so an operator cannot mislabel a
   country without editing the boundary itself.
3. `airbg import-areas data/boundaries/<slug>.geojson country`.
4. Check the new slug does not collide with an existing one —
   `TestSlugsAreUniqueAcrossEveryFile` globs the directory and will tell you.

Order matters only in that step 1 without steps 2–3 produces the warning
described above.
