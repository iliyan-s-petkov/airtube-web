// Package config loads runtime configuration from the environment.
// No configuration is read from files, and no secret is ever compiled in.
package config

import (
	"errors"
	"os"
	"time"
)

const defaultUpstreamURL = "https://data.sensor.community/airrohr/v1/filter/country=BG"

type Config struct {
	DatabaseURL  string
	UpstreamURL  string
	PollInterval time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:  os.Getenv("AIRBG_DATABASE_URL"),
		UpstreamURL:  os.Getenv("AIRBG_UPSTREAM_URL"),
		PollInterval: 5 * time.Minute,
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: AIRBG_DATABASE_URL is required")
	}
	if cfg.UpstreamURL == "" {
		cfg.UpstreamURL = defaultUpstreamURL
	}
	if v := os.Getenv("AIRBG_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, err
		}
		cfg.PollInterval = d
	}
	return cfg, nil
}
