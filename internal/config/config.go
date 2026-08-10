// Package config loads runtime configuration from the environment.
// No configuration is read from files, and no secret is ever compiled in.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultUpstreamURL = "https://data.sensor.community/airrohr/v1/filter/country=BG"

const (
	defaultListenAddr  = "127.0.0.1:8080"
	defaultMetricsAddr = "127.0.0.1:9090"
	defaultBaseURL     = "http://localhost:8080"
)

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

	// ListenAddr is the public HTTP listener. Loopback by default: in
	// production Cloudflare reaches the origin over a tunnel, and a default of
	// 0.0.0.0 would expose an origin that has never seen a rate limit to the
	// open internet the first time someone runs it on a public host.
	ListenAddr string

	// MetricsAddr serves /metrics and /healthz on a separate listener, so the
	// public chain cannot route to them at all.
	MetricsAddr string

	// TrustedProxyCIDRs lists the peer ranges whose CF-Connecting-IP header is
	// believed. Empty means trust nobody.
	TrustedProxyCIDRs []string

	// BaseURL is the public origin, used to build canonical and hreflang links.
	BaseURL string
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

	cfg.ListenAddr = envOr("AIRBG_LISTEN_ADDR", defaultListenAddr)
	cfg.MetricsAddr = envOr("AIRBG_METRICS_ADDR", defaultMetricsAddr)

	if cfg.ListenAddr == cfg.MetricsAddr {
		// Same address means /metrics is reachable from the public chain,
		// which hands an attacker the counters that show whether their probing
		// is being rate limited.
		return Config{}, fmt.Errorf("config: AIRBG_LISTEN_ADDR and AIRBG_METRICS_ADDR are both %q; the private listener must be separate", cfg.ListenAddr)
	}

	for _, raw := range strings.Split(os.Getenv("AIRBG_TRUSTED_PROXY_CIDRS"), ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if _, err := netip.ParsePrefix(item); err != nil {
			return Config{}, fmt.Errorf("config: AIRBG_TRUSTED_PROXY_CIDRS entry %q: %w", item, err)
		}
		cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, item)
	}

	cfg.BaseURL = strings.TrimSuffix(envOr("AIRBG_BASE_URL", defaultBaseURL), "/")
	if u, err := url.Parse(cfg.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
		return Config{}, fmt.Errorf("config: AIRBG_BASE_URL must be absolute, e.g. https://airbg.org (got %q)", cfg.BaseURL)
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
