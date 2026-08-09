-- +goose Up

-- Task 17: sensors are now filtered against the national boundary using the
-- same ST_Covers predicate area.AssignSensors already uses for city/oblast/
-- neighbourhood areas. The national boundary is one more row in the same
-- `area` table, distinguished by kind = 'country' so it is never confused
-- with (or accidentally matched by a query that means) a city or oblast.
ALTER TABLE area DROP CONSTRAINT area_kind_check;
ALTER TABLE area ADD CONSTRAINT area_kind_check
    CHECK (kind IN ('city', 'oblast', 'neighbourhood', 'country'));

-- +goose Down

-- Rolling this back with a country row present cannot succeed: the narrowed
-- CHECK is validated against existing rows. Left alone that surfaces as an
-- opaque "check constraint area_kind_check is violated by some row", which says
-- neither which row nor what to do about it.
--
-- The rollback deliberately does not delete the offending rows to make itself
-- pass. A migration that quietly deleted the national boundary would take the
-- collector's fail-closed boundary filter down with it, and an operator running
-- a rollback would have no way to know that happened. Raising names the row
-- count and the remedy and leaves the decision with them.
-- +goose StatementBegin
DO $$
DECLARE n bigint;
BEGIN
    SELECT count(*) INTO n FROM area WHERE kind = 'country';
    IF n > 0 THEN
        RAISE EXCEPTION
            'cannot roll back 00006: % area row(s) have kind = ''country'', which the restored CHECK forbids. Delete them first (DELETE FROM area WHERE kind = ''country''), noting that this disables the collector''s boundary filter until a boundary is re-imported.', n;
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE area DROP CONSTRAINT area_kind_check;
ALTER TABLE area ADD CONSTRAINT area_kind_check
    CHECK (kind IN ('city', 'oblast', 'neighbourhood'));
