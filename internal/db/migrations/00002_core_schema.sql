-- +goose Up
CREATE TABLE sensor (
    sensor_id   bigint PRIMARY KEY,
    sensor_type text NOT NULL,
    location    geography(Point, 4326) NOT NULL,
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now(),
    active      boolean NOT NULL DEFAULT true
);

CREATE INDEX sensor_location_idx ON sensor USING gist (location);

CREATE TYPE quality_flag AS ENUM (
    'ok', 'out_of_range', 'stuck', 'spatial_outlier', 'no_neighbours'
);

CREATE TABLE reading (
    time      timestamptz NOT NULL,
    sensor_id bigint NOT NULL,
    metric    text NOT NULL,
    value     double precision NOT NULL,
    quality   quality_flag NOT NULL DEFAULT 'ok'
);

SELECT create_hypertable('reading', 'time', chunk_time_interval => interval '1 day');

-- Upserts key on this; it also serves per-sensor chart queries.
CREATE UNIQUE INDEX reading_sensor_metric_time_idx
    ON reading (sensor_id, metric, time DESC);

-- +goose Down
DROP TABLE reading;
DROP TYPE quality_flag;
DROP TABLE sensor;
