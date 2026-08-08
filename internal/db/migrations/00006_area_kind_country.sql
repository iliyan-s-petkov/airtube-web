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
ALTER TABLE area DROP CONSTRAINT area_kind_check;
ALTER TABLE area ADD CONSTRAINT area_kind_check
    CHECK (kind IN ('city', 'oblast', 'neighbourhood'));
