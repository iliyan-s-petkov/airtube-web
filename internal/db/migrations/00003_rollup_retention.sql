-- +goose Up

-- A plain hypertable rather than a continuous aggregate: the one-year archive
-- backfill writes hourly buckets directly, and continuous aggregates are not
-- insertable. The rollup job in internal/store/rollup.go maintains it from raw
-- readings, filtering on quality IN ('ok', 'no_neighbours') so flagged data can
-- never contaminate a published average (spec §5.3). 'no_neighbours' is included
-- because it records that the spatial-outlier check could not run — there was
-- nothing to compare against — not that the reading failed it; excluding it
-- would silently drop every rural sensor that has no neighbour within range.
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
