-- +goose NO TRANSACTION
-- +goose Up
ALTER TYPE quality_flag ADD VALUE IF NOT EXISTS 'source_invalid';

-- +goose StatementBegin
ALTER TABLE sensor
    ADD COLUMN IF NOT EXISTS source       text NOT NULL DEFAULT 'sensor.community',
    ADD COLUMN IF NOT EXISTS source_ref   text,
    ADD COLUMN IF NOT EXISTS station_code text,
    ADD COLUMN IF NOT EXISTS station_name text,
    ADD COLUMN IF NOT EXISTS station_type text,
    ADD COLUMN IF NOT EXISTS station_area text;
-- +goose StatementEnd

ALTER TABLE sensor
    ADD CONSTRAINT sensor_source_known CHECK (source IN ('sensor.community', 'eea'));

CREATE UNIQUE INDEX IF NOT EXISTS sensor_source_ref_idx
    ON sensor (source_ref) WHERE source_ref IS NOT NULL;

CREATE INDEX IF NOT EXISTS sensor_source_idx ON sensor (source);

CREATE SEQUENCE IF NOT EXISTS official_sensor_id_seq START WITH 9000000000;

-- +goose Down
DROP SEQUENCE IF EXISTS official_sensor_id_seq;
DROP INDEX IF EXISTS sensor_source_idx;
DROP INDEX IF EXISTS sensor_source_ref_idx;
ALTER TABLE sensor DROP CONSTRAINT IF EXISTS sensor_source_known;
ALTER TABLE sensor
    DROP COLUMN IF EXISTS station_area,
    DROP COLUMN IF EXISTS station_type,
    DROP COLUMN IF EXISTS station_name,
    DROP COLUMN IF EXISTS station_code,
    DROP COLUMN IF EXISTS source_ref,
    DROP COLUMN IF EXISTS source;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM reading WHERE quality = 'source_invalid') THEN
        RAISE EXCEPTION 'readings still carry quality source_invalid';
    END IF;
END
$$;
-- +goose StatementEnd
