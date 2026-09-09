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

-- Guard runs before any destructive DDL. NO TRANSACTION means each Down
-- statement commits independently; a guard placed after the DROPs would let
-- sensor.source (the only column that tells an EEA row from a community row)
-- be gone by the time the guard raises, with no way to undo that.
-- +goose StatementBegin
DO $$
DECLARE n bigint;
BEGIN
    SELECT count(*) INTO n FROM reading WHERE quality = 'source_invalid';
    IF n > 0 THEN
        RAISE EXCEPTION
            'cannot roll back 00011: % reading row(s) are flagged ''source_invalid'' and the older code cannot read that value. Re-flag them first (UPDATE reading SET quality = ''out_of_range'' WHERE quality = ''source_invalid'').', n;
    END IF;
END $$;
-- +goose StatementEnd

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
