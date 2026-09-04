-- +goose NO TRANSACTION
-- +goose Up

-- The 'clamped' quality flag. See README.md.

ALTER TYPE quality_flag ADD VALUE IF NOT EXISTS 'clamped';

-- +goose Down

-- +goose StatementBegin
DO $$
DECLARE n bigint;
BEGIN
    SELECT count(*) INTO n FROM reading WHERE quality = 'clamped';
    IF n > 0 THEN
        RAISE EXCEPTION
            'cannot roll back 00009: % reading row(s) are flagged ''clamped'' and the older code cannot read that value. Re-flag them first (UPDATE reading SET quality = ''out_of_range'' WHERE quality = ''clamped'').', n;
    END IF;
END $$;
-- +goose StatementEnd
