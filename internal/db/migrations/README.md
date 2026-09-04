# Migrations

Applied by `airbg migrate`, embedded via `embed.go`. Each file is `-- +goose
Up` / `-- +goose Down`; a `DO $$` block needs `-- +goose StatementBegin/End`
around it or goose splits it on the semicolons inside.

The reasoning behind a migration lives here, not in the file. Numbers below are
the ones documented so far.

## 00010 — `wind_forecast`

Storage for the forecast overlay (`docs/wind-overlay.md`). The only table here
that holds something we did not measure.

**Keyed by axial hex coordinate, not by centre lon/lat.** The centre is derived
from `snapshot.HexResolutionKM` and `hexRefLat`; change either and every stored
centre names a different place, silently. The axial pair is the grid's own
identity and moves only when the grid does.

`resolution_km` is stored on the row anyway, because that move is exactly what
invalidates the data: rows written at one resolution must be recognisable as
stale rather than merged with rows written at another.

**Retention is 7 days against a 24-hour forecast horizon.** A superseded
forecast is not history worth keeping. The margin over the horizon exists so a
collector that has been down for a day still has rows to serve while it
catches up, and `config.validateWind` refuses a retention shorter than the
horizon for the same reason.

There is no foreign key to `sensor` or `area`. A hex is not a stored entity —
it is computed each cycle from wherever the sensors happen to be — so there is
nothing to reference. Rows for a hex that no longer has sensors are left to
retention rather than deleted eagerly; they cost a few bytes and a sensor
coming back online is common.

## 00009 — the `clamped` flag

Adds a `quality_flag` enum value for an instrument pegged at the top of its
scale (`internal/quality/clamp.go`). Separate from `out_of_range` because the
two need different responses: a saturated sensor is working, a broken one is
not.

**`-- +goose NO TRANSACTION`.** `ALTER TYPE … ADD VALUE` may not be followed by
a use of the new value in the same transaction. Nothing here uses it, but the
statement is a single DDL with nothing to be atomic with, so the annotation
costs nothing and removes the constraint entirely.

**Down does not remove the value.** Postgres has no `DROP VALUE`; undoing it
means recreating the type and rewriting every `reading` row that references it.
An unused enum label is inert, so Down instead refuses if any row is actually
flagged `clamped` — rolling back under the older code would otherwise leave
rows it cannot decode. The remedy is in the exception message.

## 00008 — country codes

Supports the widened ingest filter (`docs/ingest-filter.md`): the boundary test
now runs against a configured set of countries rather than one national
outline.

**`area.country_code`.** Which country a boundary is has to be a fact the row
carries. `slug` cannot serve as the code — the committed slugs are
transliterated Bulgarian names (`bulgaria`, `varna`), a rule
`slug_rule_test.go` pins — and the config allow list names countries by ISO
3166-1 alpha-2.

The value comes from the `iso_a2` property of the boundary GeoJSON, so an
operator cannot mislabel a country without editing the geometry it labels.

**Statement order is load-bearing.** `ADD CONSTRAINT` validates existing rows,
so the backfill runs first: on any live database the `bulgaria` boundary is
already imported with no code, and declaring the constraint first would fail
the migration. The `DO` block between them stops any *other* pre-existing
country row with an instruction rather than leaving the constraint to reject it
with a bare violation.

**The constraint is two-sided, and written as a `CASE`.** Only `country` rows
carry a code, and every one must. A city row with a country code would make
"which countries are enabled" ambiguous; a country row without one is invisible
to the allow list and would silently stop ingesting.

The `CASE` is not a style choice. The obvious form —
`(kind = 'country' AND country_code ~ '…') OR (kind <> 'country' AND
country_code IS NULL)` — does not enforce presence: `NULL ~ '…'` is NULL, so
the whole expression evaluates to NULL, and a CHECK only rejects FALSE. A
country row with no code passed it. `CASE` returns a real boolean on every
branch.

**`sensor.country_code`.** Decided geometrically at ingest by the same
`ST_Covers` test that admits the sensor. Persisted rather than recomputed: the
snapshot builder needs it once per cycle for the hex grid, and a spatial join
against every country boundary on every build doubles the most expensive query
the collector already runs.

Nullable, and not backfilled. Rows written before this migration were only ever
tested against the Bulgarian outline; setting them to `BG` would be true today
and a lie the moment the allow list widens, so they stay NULL until their next
reading re-establishes it. `UpsertSensors` uses `COALESCE(EXCLUDED.country_code,
sensor.country_code)` so a cycle that simply did not see a sensor does not reset
its code.
