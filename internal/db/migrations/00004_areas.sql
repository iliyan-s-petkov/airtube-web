-- +goose Up
CREATE TABLE area (
    slug    text PRIMARY KEY,
    kind    text NOT NULL CHECK (kind IN ('city', 'oblast', 'neighbourhood')),
    name_bg text NOT NULL,
    name_en text NOT NULL,
    geom    geography(MultiPolygon, 4326) NOT NULL
);

CREATE INDEX area_geom_idx ON area USING gist (geom);
CREATE INDEX area_kind_idx ON area (kind);

CREATE TABLE area_sensor (
    area_slug text   NOT NULL REFERENCES area(slug) ON DELETE CASCADE,
    sensor_id bigint NOT NULL REFERENCES sensor(sensor_id) ON DELETE CASCADE,
    PRIMARY KEY (area_slug, sensor_id)
);

CREATE INDEX area_sensor_sensor_idx ON area_sensor (sensor_id);

CREATE TABLE api_key (
    id         bigserial PRIMARY KEY,
    label      text NOT NULL,
    key_hash   text NOT NULL UNIQUE,
    rate_limit integer NOT NULL DEFAULT 60,
    quota      bigint NOT NULL DEFAULT 100000,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

-- +goose Down
DROP TABLE api_key;
DROP TABLE area_sensor;
DROP TABLE area;
