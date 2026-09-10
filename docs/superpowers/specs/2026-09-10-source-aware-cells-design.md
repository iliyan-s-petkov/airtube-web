# Source-aware cells — design

Date: 2026-09-10. Follows `2026-09-09-eea-official-stations-design.md`.

## Problem

On `/en/` (country tier) the two network checkboxes are disabled with the
suffix "only when single sensors are shown". Aggregate cells blend both
networks and the toggle has nothing to act on. A visitor unticks "Citizen
sensors", nothing changes, and a reload does not help. Second half: the default
metric is P2 (PM2.5), which only 4 of ~141 official stations report, so the
official layer looks empty on arrival with no explanation.

## Decisions (user, 2026-09-10)

1. Network toggles act at every tier, cells included.
2. Semantics are honest: citizen off → cell shows the official-only median;
   official off → citizen-only median; both on → median over both networks'
   sensors; both off → no cells, `noSources` hint.
3. Default metric stays P2. The layer menu shows how many official stations
   currently have data for the selected metric.
4. No new query parameter. A toggle is a repaint from the body in hand, never a
   request (`web/e2e/sources.spec.js` pins this). The anti-enumeration surface
   of `/api/v1/hexes` is unchanged.
5. Timelapse is out of scope: `reading_hourly` frames carry no source and are a
   separate precomputed cube. The toggles do not affect an animation.

## API shape

`/api/v1/hexes` keeps `generated_at`, `resolution_km`, `hexes[]`. Each
aggregate-tier entry gains one optional field:

```json
{
  "lon": 23.32, "lat": 42.69, "n": 14, "country": "BG",
  "values": {"P1": 21.5, "P2": 12.0, "temperature": 18.2},
  "by_source": {
    "sensor.community": {"n": 13, "values": {"P1": 21.0, "P2": 12.0, "temperature": 18.2}},
    "eea":              {"n": 1,  "values": {"P1": 27.3}}
  }
}
```

- `values` stays the median over every sensor in the bin (both networks). It
  cannot be derived from the two per-source medians, so it is kept, not
  recomputed client-side.
- `by_source[s].values[m]` is the median over that network's sensors in the bin
  that have a usable reading for `m`. A metric with no reading in that network
  is absent, never zero.
- `by_source` is omitted when the bin has exactly one network: the client then
  treats `values` as that network's own. The source of a single-network bin is
  named by `source` on the entry (`"source": "eea"`), also `omitempty`, so a
  single-network cell can still be dropped when its network is off.
- Point tier (`resolution_km: 0`): entries already are one sensor. They carry
  `source` and never `by_source`.
- Rows written before the source column existed have empty `Source`; they are
  `sensor.community` (same rule as `sourceOf` in `sourcefilter.svelte.js`).

The payload gains a top-level `coverage` block, identical on every tier and
window (so it is computed once per build and copied):

```json
"coverage": {
  "eea":              {"P1": 27, "P2": 4, "SO2": 28, "NO2": 25, "O3": 20, "CO": 18, "C6H6": 17, "NOX": 1},
  "sensor.community": {"P1": 1180, "P2": 1180, "temperature": 900, "humidity": 900, "pressure": 610}
}
```

`coverage[s][m]` = number of sensors of network `s` with a usable reading for
`m` in the snapshot's fresh set. Zero entries are omitted.

## Server (`internal/snapshot`)

- `hexBin` gains `bySource map[string]*sourceBin` where `sourceBin{n int; vals
  map[string][]float64}`. `hexPayloadFrom` appends each reading to both the
  blended and its network's lists. Emission: if `len(bySource) == 1` set
  `Source`, else set `BySource` with one `sourceEntry{N, Values}` per network,
  values through the same `round1(median(...))`.
- `pointsFrom` sets `Source` on each entry.
- `hexEntry` gains `Source string \`json:"source,omitempty"\`` and `BySource
  map[string]sourceEntry \`json:"by_source,omitempty"\``.
- `hexPayload` gains `Coverage map[string]map[string]int \`json:"coverage"\``,
  computed by a new `coverageFrom(sensors []store.SensorReading)` in
  `hexes.go` from `sr.Source` and the keys of `sr.Values`. Built once in
  `build.go` beside the tier loop and assigned to every tier's payload.
- Ordering, ETag and gzip are unchanged: `by_source` map keys serialise in Go's
  sorted order, so the body is still a function of the readings.
- `store.SensorReading.Source` is already selected (`aggregate.go:295`); no SQL
  change.

## Client

`web/src/lib/hexes.js`

- `hexFeatures` gains a parameter `enabled` (Set of sources). Per entry, pick
  the number to draw:
  - both on → `h.values`, `h.n`.
  - one on, entry has `by_source` → that network's `values`/`n`; entry lacks it
    for that network → skip the entry.
  - one on, entry has no `by_source` → keep iff `sourceOf(h)` is that network.
  - none on → return `[]`.
  The partition into no-data / data uses the picked `values`.
- Properties keep `colour, value, n, sensorId`; `n` is the picked count.

`web/src/lib/sourcefilter.svelte.js`

- `sourceOf(entry)` is exported and reads `entry.source ?? entry.properties?.source`
  so it serves both hex entries and sensor features.
- `MEASURED` and `measuredBy` are deleted. Coverage is data now.

`web/src/islands/map.js`

- `refreshHexes` and the timelapse `paint` pass `getSources()` into
  `hexFeatures`; timelapse ignores it (frames have no source) — it passes
  `null`, and `hexFeatures` treats `null` as "both on".
- `onSourceChange` also calls `refreshHexes` (a repaint: URL unchanged, no
  fetch), as `onSensorStatusChange` already does.
- `setSourceViewAvailability(chrome, metric, t, coverage)` replaces the
  `onSensorTier` gate. Both checkboxes are always enabled. Label text:
  - `coverage[s][metric] > 0` → `` `${label} — ${count} ${t.withData}` ``, with a
    new i18n key `map.view.with_data` = "with data" (EN) / "с данни" (BG). The
    number is composed client-side, as `sensorCountLine` already does:
    `i18n.Catalogue.T` takes no parameters, so a sentence with a number in it is
    assembled from catalogue parts.
  - otherwise → `"{label} — {t.notMeasured}"`, checkbox still enabled (the
    reader may turn it off; it hides nothing).
- `state.onSensorTier`, `t.sensorTierOnly`, `tSensorTierOnly`, and i18n key
  `map.view.sensor_tier_only` are removed. `state.coverage` is set from every
  hex body received in `refreshHexes` and drives the label refresh on metric
  change (`map.js:867`) and after each hex fetch.
- `mapHint` unchanged: both off still shows `noSources`, now for the grid too.

## Tests

Go (`internal/snapshot`):

- `hexPayloadFrom`: a bin with 13 community + 1 eea readings emits
  `by_source` with n 13/1 and per-network medians; `values` is the median over
  all 14; a community-only bin emits `source` and no `by_source`; an empty
  `Source` counts as `sensor.community`; a metric missing from one network is
  absent from that network's `values`.
- `pointsFrom` sets `source`.
- `coverageFrom` counts per network per metric and omits zeros.
- ETag stability test extended: reordering input readings leaves the body
  byte-identical.
- Mutation checks: swap the per-network median to the blended one and the
  by_source test must fail; drop the empty-Source rule and the test must fail.

Vitest (`web/src/lib/__tests__/hexes.test.js`, `sourcefilter.test.js`,
`web/src/islands/__tests__/map.test.js`):

- `hexFeatures` with `enabled = {eea}` draws the eea median and eea `n`, drops
  cells without eea, keeps a `source: "eea"` single-network cell, drops a
  `source: "sensor.community"` one; `enabled` empty → `[]`; `null` → blended.
- `onSourceChange` triggers a grid repaint with zero fetches.
- `setSourceViewAvailability` renders "Official stations — 4 with data" for P2
  and keeps both inputs enabled on the country tier; renders `notMeasured`
  when the count is 0. Its tier-gate tests (`map.test.js` "disables both away
  from the sensor tier", "disables the network checkboxes at the country tier",
  "re-enables a network when the map returns to the sensor tier") are replaced
  rather than kept.
- `measuredBy` tests are deleted with the function.

Playwright (`web/e2e/sources.spec.js`):

- Open `/en/` instead of `/en/area/sofia`; both checkboxes enabled; unticking
  "Citizen sensors" fires `airbg:paint` with source `airbg-hexes` and no
  request; the "explains itself" test asserts the count suffix rather than
  `toBeDisabled`.

## Out of scope

- Timelapse per-source frames.
- Per-number source composition in the area panel (prior spec §2 carry-over).
- Any change to the `/api/v1/timelapse` or area endpoints.
