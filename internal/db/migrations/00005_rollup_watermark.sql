-- +goose Up

-- RollupHour (internal/store/rollup.go) is deliberately stateless: it
-- recomputes and replaces whichever bucket it is handed and remembers
-- nothing. That means nothing tracks how far the hourly rollup has actually
-- progressed, so if it stalls (crash loop, long outage, a swallowed error)
-- the 30-day retention policy on `reading` can delete raw rows before they
-- were ever aggregated — a silent, permanent, unrecoverable loss of hourly
-- history for those hours.
--
-- This table is the missing memory: one row recording the last bucket that
-- was successfully rolled up and when. One row is enough because the rollup
-- position is global, not per-sensor. `id` is constrained to the literal
-- `true` so a second row can never be inserted.
CREATE TABLE rollup_watermark (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    bucket     timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE rollup_watermark;
