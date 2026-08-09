// Package config loads runtime configuration from the environment.
// No configuration is read from files, and no secret is ever compiled in.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

const defaultUpstreamURL = "https://data.sensor.community/airrohr/v1/filter/country=BG"

// MinPollInterval is the smallest accepted AIRBG_POLL_INTERVAL.
//
// Two distinct failures live below this floor, and both must be rejected here
// — at configuration load, with a message naming the variable — rather than
// several layers down where the symptom no longer names its cause:
//
//   - "0s" and any negative value parse cleanly as a duration and then panic
//     inside time.NewTicker ("non-positive interval for NewTicker") on the
//     collector's first tick. A typo in a deployment env var must produce the
//     same clean slog.Error + exit(1) every other configuration mistake gets,
//     not a stack trace.
//   - A small positive value ("1s") never panics; it silently polls
//     data.sensor.community 300x more often than the 5-minute default. That is
//     a public, volunteer-run community API, and hammering it is the kind of
//     thing that gets a collector's IP banned — taking the whole site's data
//     with it. 30s is far below any interval we would deliberately configure
//     and far above the rate at which we become a nuisance.
const MinPollInterval = 30 * time.Second

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
			return Config{}, fmt.Errorf("config: AIRBG_POLL_INTERVAL %q is not a valid duration (e.g. \"5m\", \"90s\"): %w", v, err)
		}
		if d < MinPollInterval {
			return Config{}, fmt.Errorf(
				"config: AIRBG_POLL_INTERVAL %v is below the %v minimum — zero or negative panics the ticker, and a sub-minimum interval hammers the public sensor.community API",
				d, MinPollInterval)
		}
		cfg.PollInterval = d
	}
	return cfg, nil
}
