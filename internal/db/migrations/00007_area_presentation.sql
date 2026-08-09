-- +goose Up

-- Two presentation columns, both derived rather than supplied.
--
-- centroid: /api/v1/locate snaps a visitor to the containing area's centre
-- (Phase 1 §9.4), and every area page centres its map. Deriving it from geom in
-- a trigger rather than accepting it from the caller removes a whole class of
-- bug: a centroid that disagrees with its own polygon. There is no code path
-- that can produce one.
--
-- default_zoom: the zoom a map opens at for this area's kind. The values
-- straddle 11 deliberately — Phase 1 §7.1 switches from the choropleth tier to
-- the individual-sensor tier at z >= 11, so an oblast resolves to 9 (still a
-- choropleth; a region that size cannot usefully render 300 markers) and a
-- neighbourhood to 13 (individual sensors, which is the whole point of looking
-- at a neighbourhood).

ALTER TABLE area ADD COLUMN centroid     geography(Point, 4326);
ALTER TABLE area ADD COLUMN default_zoom smallint;

-- +goose StatementBegin
CREATE FUNCTION area_derive_presentation() RETURNS trigger AS $$
BEGIN
    NEW.centroid := ST_Centroid(NEW.geom);
    NEW.default_zoom := CASE NEW.kind
        WHEN 'country'       THEN 7
        WHEN 'oblast'        THEN 9
        WHEN 'city'          THEN 11
        WHEN 'neighbourhood' THEN 13
    END;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER area_presentation
    BEFORE INSERT OR UPDATE OF geom, kind ON area
    FOR EACH ROW EXECUTE FUNCTION area_derive_presentation();

-- Backfill any rows imported before this migration, then enforce NOT NULL. The
-- order matters: adding NOT NULL first would fail on existing rows.
UPDATE area SET geom = geom;

ALTER TABLE area ALTER COLUMN centroid     SET NOT NULL;
ALTER TABLE area ALTER COLUMN default_zoom SET NOT NULL;

-- +goose Down
DROP TRIGGER area_presentation ON area;
DROP FUNCTION area_derive_presentation();
ALTER TABLE area DROP COLUMN default_zoom;
ALTER TABLE area DROP COLUMN centroid;
