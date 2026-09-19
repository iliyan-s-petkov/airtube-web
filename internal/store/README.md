# store

Persists sensors and readings, and serves the aggregate queries the API and
snapshot builder read back.

## Batched writes

`writeBatchLimit` bounds how many statements ride in one `pgx.Batch` in
`WriteReadings`. Without it, an ingest cycle with an unusually large upstream
payload would queue every row into a single `SendBatch` call, holding it all
in memory and on the wire at once; flushing in bounded chunks keeps each
round trip's footprint constant regardless of input size.

`WriteReadings` upserts duplicate samples (value and quality overwritten)
rather than erroring, so a re-run of the same cycle is safe. The WHERE guard
in its SQL skips the write when the resubmitted value and quality are
unchanged, so a same-cycle rerun costs no row version — it does not change
what value ends up stored. The returned count is rows actually written or
updated; a resubmit the guard skips does not count, unlike a naive
`len(scored)`.

## AllAreaSeries's row limit

`AllAreaSeriesRowLimit` (aggregate.go) caps the total rows one `AllAreaSeries`
query can return across every area, since — unlike `AreaSeries`/`SensorSeries`
— no caller-scoped `since`/`until` bounds it otherwise. It is sized as a
safety valve well above the realistic worst case (see
`TestAllAreaSeriesRowLimitCoversRealConfig`, which derives that worst case
from the real `airbg.yaml` periods and committed boundary files), not as a
normal sizing constraint. `AllAreaSeries` omits an area's key entirely from
its result when the area has no data in the window — that is normal — but if
the query ever actually returns exactly the cap, some late-slug area's data
may have been truncated out silently, so a WARN is logged naming the limit
and the metric.
