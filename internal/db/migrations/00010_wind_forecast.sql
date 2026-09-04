-- +goose Up

-- Forecast wind on the hex grid. Not a measurement. See docs/wind-overlay.md.

CREATE TABLE wind_forecast (
    valid_at      timestamptz NOT NULL,
    hex_q         integer NOT NULL,
    hex_r         integer NOT NULL,
    resolution_km double precision NOT NULL,
    speed_ms      double precision NOT NULL CHECK (speed_ms >= 0),
    direction_deg double precision NOT NULL CHECK (direction_deg >= 0 AND direction_deg < 360),
    model         text NOT NULL,
    fetched_at    timestamptz NOT NULL
);

SELECT create_hypertable('wind_forecast', 'valid_at', chunk_time_interval => interval '1 day');

CREATE UNIQUE INDEX wind_forecast_key_idx
    ON wind_forecast (hex_q, hex_r, valid_at DESC);

SELECT add_retention_policy('wind_forecast', drop_after => interval '7 days');

-- +goose Down
SELECT remove_retention_policy('wind_forecast');
DROP TABLE wind_forecast;
