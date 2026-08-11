// Package config loads runtime configuration from the environment.
// No configuration is read from files, and no secret is ever compiled in.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"airbg.org/internal/admit"
)

const defaultUpstreamURL = "https://data.sensor.community/airrohr/v1/filter/country=BG"

const (
	defaultListenAddr  = "127.0.0.1:8080"
	defaultMetricsAddr = "127.0.0.1:9090"
	defaultBaseURL     = "http://localhost:8080"
)

// Pool sizes for the serve command's two pools. Their sum is what Postgres sees
// from one instance, so raising either is a decision about the database's
// max_connections, not just about this process.
const (
	defaultDBAPIConns       int32 = 8
	defaultDBCollectorConns int32 = 4
)

// defaultMaxDBInflight is defaultDBAPIConns doubled — see admit.DefaultSize,
// which is the single source of this number for config, api and server alike.
const defaultMaxDBInflight int32 = admit.DefaultSize

// defaultMaxConns bounds how many connections the public listener holds open
// at once. Generous relative to defaultMaxDBInflight: most held connections are
// idle keep-alives or slow trickles, not requests in flight, so this cap exists
// to stop file-descriptor exhaustion rather than to shape request concurrency —
// that is what MaxDBInflight and the rate limiter are for.
const defaultMaxConns int32 = 4096

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

	// DBAPIConns and DBCollectorConns size the two connection pools the serve
	// command opens. They are separate because the two workloads are separate:
	// the collector may hold a connection for up to AssignStatementTimeout (60s)
	// on every poll cycle, and while both shared one pool, request handlers
	// starved behind it on a schedule. See db.OpenPair.
	//
	// Stated numbers rather than pgxpool's max(4, numCPU) default, so deployed
	// capacity is a decision and not a side effect of the container's core
	// allocation.
	DBAPIConns       int32
	DBCollectorConns int32

	// MaxDBInflight bounds how many requests may be inside a database query at
	// once, across every client — see internal/admit. A per-client rate limiter
	// says nothing about the crowd: N well-behaved clients can still collectively
	// queue more concurrent work than the pool can serve, and the excess waits
	// inside pgxpool.Acquire until the write timeout fires.
	MaxDBInflight int32

	// MaxConns bounds how many connections the public listener holds open at
	// once — a socket-count cap, not a request cap. Nothing else in this
	// process bounds how many mostly-idle connections the host may hold, and
	// that is a separate failure mode from request concurrency: file-descriptor
	// exhaustion needs no completed request to happen. See internal/httpx.LimitListener.
	MaxConns int32
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

	apiConns, err := envPositiveInt32("AIRBG_DB_API_CONNS", defaultDBAPIConns)
	if err != nil {
		return Config{}, err
	}
	collectorConns, err := envPositiveInt32("AIRBG_DB_COLLECTOR_CONNS", defaultDBCollectorConns)
	if err != nil {
		return Config{}, err
	}
	cfg.DBAPIConns, cfg.DBCollectorConns = apiConns, collectorConns

	maxInflight, err := envPositiveInt32("AIRBG_MAX_DB_INFLIGHT", defaultMaxDBInflight)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxDBInflight = maxInflight

	maxConns, err := envPositiveInt32("AIRBG_MAX_CONNS", defaultMaxConns)
	if err != nil {
		return Config{}, err
	}
	cfg.MaxConns = maxConns

	return cfg, nil
}

// envPositiveInt32 reads a pool size. Rejecting zero and negatives here, naming
// the variable, is the point: pgxpool reads MaxConns <= 0 as "use the default",
// so a "0" waved through at load time would look like an explicit choice of no
// capacity and silently become max(4, numCPU) instead.
func envPositiveInt32(key string, fallback int32) (int32, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("config: %s %q is not a whole number: %w", key, v, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("config: %s must be at least 1, got %d", key, n)
	}
	return int32(n), nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
