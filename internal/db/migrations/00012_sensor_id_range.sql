-- +goose Up

-- The 9e9 floor was a sequence START value and a comment: nothing stopped a
-- community sensor_id from landing in the official range. A collision there is
-- silent rather than loud, because the community upsert's ON CONFLICT DO UPDATE
-- leaves source, source_ref and the station_* columns untouched, so the row
-- would keep its EEA badge and station identity at a citizen device's
-- coordinates. This makes the two ranges mutually exclusive at the database.
ALTER TABLE sensor
    ADD CONSTRAINT sensor_id_matches_source
    CHECK ((source = 'eea') = (sensor_id >= 9000000000));

-- +goose Down

ALTER TABLE sensor DROP CONSTRAINT IF EXISTS sensor_id_matches_source;
