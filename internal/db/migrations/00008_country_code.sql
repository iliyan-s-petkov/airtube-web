-- +goose Up

-- Country codes for the widened ingest filter. See README.md.

ALTER TABLE area ADD COLUMN country_code text;

UPDATE area SET country_code = 'BG' WHERE kind = 'country' AND slug = 'bulgaria';

-- +goose StatementBegin
DO $$
DECLARE n bigint;
BEGIN
    SELECT count(*) INTO n FROM area WHERE kind = 'country' AND country_code IS NULL;
    IF n > 0 THEN
        RAISE EXCEPTION
            'cannot apply 00008: % area row(s) of kind ''country'' have no country_code and are not the known ''bulgaria'' row. Re-import them with a geojson carrying an iso_a2 property (airbg import-areas <path.geojson> country), or delete them first.', n;
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE area ADD CONSTRAINT area_country_code_check CHECK (
    CASE kind
        WHEN 'country' THEN country_code IS NOT NULL AND country_code ~ '^[A-Z]{2}$'
        ELSE country_code IS NULL
    END
);

ALTER TABLE sensor ADD COLUMN country_code text;
ALTER TABLE sensor ADD CONSTRAINT sensor_country_code_check
    CHECK (country_code IS NULL OR country_code ~ '^[A-Z]{2}$');

-- +goose Down

ALTER TABLE sensor DROP CONSTRAINT sensor_country_code_check;
ALTER TABLE sensor DROP COLUMN country_code;
ALTER TABLE area DROP CONSTRAINT area_country_code_check;
ALTER TABLE area DROP COLUMN country_code;
