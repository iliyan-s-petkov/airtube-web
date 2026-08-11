package config

import (
	"strings"
	"testing"
	"time"
)

// clearEnv pins every variable Load reads, so a test's result never depends on
// what happens to be exported in the developer's (or CI runner's) shell. Before
// this, TestLoadDefaults set only AIRBG_DATABASE_URL and then asserted on the
// defaults for the other two — which an ambient AIRBG_POLL_INTERVAL would
// silently break, or worse, silently satisfy.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AIRBG_DATABASE_URL", "")
	t.Setenv("AIRBG_UPSTREAM_URL", "")
	t.Setenv("AIRBG_POLL_INTERVAL", "")
	t.Setenv("AIRBG_LISTEN_ADDR", "")
	t.Setenv("AIRBG_METRICS_ADDR", "")
	t.Setenv("AIRBG_TRUSTED_PROXY_CIDRS", "")
	t.Setenv("AIRBG_BASE_URL", "")
	t.Setenv("AIRBG_DB_API_CONNS", "")
	t.Setenv("AIRBG_DB_COLLECTOR_CONNS", "")
	t.Setenv("AIRBG_MAX_DB_INFLIGHT", "")
	t.Setenv("AIRBG_MAX_CONNS", "")
}

// TestPoolSizeDefaults pins the bulkhead's sizing. These are two separate pools
// because the collector may hold a connection for a minute under
// AssignStatementTimeout; the defaults must be stated numbers rather than
// max(4, numCPU), or the deployed capacity silently tracks the container's core
// allocation.
func TestPoolSizeDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DBAPIConns != defaultDBAPIConns {
		t.Errorf("DBAPIConns = %d, want %d", cfg.DBAPIConns, defaultDBAPIConns)
	}
	if cfg.DBCollectorConns != defaultDBCollectorConns {
		t.Errorf("DBCollectorConns = %d, want %d", cfg.DBCollectorConns, defaultDBCollectorConns)
	}
}

// TestMaxDBInflightDefault pins the admission cap's default: defaultDBAPIConns
// doubled, so a router built without explicit configuration behaves like the
// deployed one (see api.defaultAdmission).
func TestMaxDBInflightDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxDBInflight != defaultMaxDBInflight {
		t.Errorf("MaxDBInflight = %d, want %d", cfg.MaxDBInflight, defaultMaxDBInflight)
	}
}

func TestMaxDBInflightOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_MAX_DB_INFLIGHT", "32")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxDBInflight != 32 {
		t.Errorf("MaxDBInflight = %d, want 32", cfg.MaxDBInflight)
	}
}

// TestMaxConnsDefault pins the connection cap's default: generous relative to
// MaxDBInflight, because this bounds sockets rather than requests in flight.
func TestMaxConnsDefault(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxConns != defaultMaxConns {
		t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, defaultMaxConns)
	}
}

func TestMaxConnsOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_MAX_CONNS", "128")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MaxConns != 128 {
		t.Errorf("MaxConns = %d, want 128", cfg.MaxConns)
	}
}

func TestPoolSizeOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_DB_API_CONNS", "24")
	t.Setenv("AIRBG_DB_COLLECTOR_CONNS", "6")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DBAPIConns != 24 {
		t.Errorf("DBAPIConns = %d, want 24", cfg.DBAPIConns)
	}
	if cfg.DBCollectorConns != 6 {
		t.Errorf("DBCollectorConns = %d, want 6", cfg.DBCollectorConns)
	}
}

// TestRejectsNonPositivePoolSizes fails closed at startup. pgxpool reads
// MaxConns <= 0 as "use the default", so a "0" that Load waved through would
// restore max(4, numCPU) on that pool — the operator would have asked for a
// specific capacity and silently got the host's core count instead.
func TestRejectsNonPositivePoolSizes(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"zero api", "AIRBG_DB_API_CONNS", "0"},
		{"zero collector", "AIRBG_DB_COLLECTOR_CONNS", "0"},
		{"negative api", "AIRBG_DB_API_CONNS", "-1"},
		{"negative collector", "AIRBG_DB_COLLECTOR_CONNS", "-4"},
		{"not a number", "AIRBG_DB_API_CONNS", "eight"},
		{"fractional", "AIRBG_DB_COLLECTOR_CONNS", "2.5"},
		{"zero inflight", "AIRBG_MAX_DB_INFLIGHT", "0"},
		{"zero max conns", "AIRBG_MAX_CONNS", "0"},
		{"negative max conns", "AIRBG_MAX_CONNS", "-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
			t.Setenv(tc.key, tc.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() accepted %s=%q", tc.key, tc.value)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error %v does not name %s; a startup failure must name the variable that caused it", err, tc.key)
			}
		})
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	clearEnv(t)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when AIRBG_DATABASE_URL is unset, got nil")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PollInterval != 5*time.Minute {
		t.Errorf("PollInterval = %v, want 5m", cfg.PollInterval)
	}
	if cfg.UpstreamURL != "https://data.sensor.community/airrohr/v1/filter/country=BG" {
		t.Errorf("UpstreamURL = %q, unexpected default", cfg.UpstreamURL)
	}
}

// TestLoadUpstreamURLOverride pins the AIRBG_UPSTREAM_URL branch, which had no
// coverage at all: a regression that ignored the variable (or applied the
// default unconditionally) would have passed the whole suite, and the only
// symptom would have been a collector quietly polling production upstream from
// a staging deployment.
func TestLoadUpstreamURLOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_UPSTREAM_URL", "http://127.0.0.1:9999/fixture")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.UpstreamURL != "http://127.0.0.1:9999/fixture" {
		t.Errorf("UpstreamURL = %q, want the AIRBG_UPSTREAM_URL override", cfg.UpstreamURL)
	}
}

func TestLoadPollIntervalOverride(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_POLL_INTERVAL", "90s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.PollInterval != 90*time.Second {
		t.Errorf("PollInterval = %v, want 90s", cfg.PollInterval)
	}
}

// TestLoadRejectsBadPollInterval covers every way AIRBG_POLL_INTERVAL can be
// wrong. The "0s" and "-5m" rows are the important ones: both parse cleanly as
// durations, so before this change Load accepted them and the process panicked
// later inside time.NewTicker instead of failing here with a message naming the
// variable. "1s" and "5s" parse and never panic — they just quietly poll the
// public community API hundreds of times more often than intended.
func TestLoadRejectsBadPollInterval(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"zero panics time.NewTicker", "0s"},
		{"negative panics time.NewTicker", "-5m"},
		{"below the floor hammers upstream", "1s"},
		{"just below the floor", "29s"},
		{"unparseable", "five minutes"},
		{"bare number without a unit", "300"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
			t.Setenv("AIRBG_POLL_INTERVAL", c.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() with AIRBG_POLL_INTERVAL=%q returned no error, want a rejection at config load", c.value)
			}
			if !strings.Contains(err.Error(), "AIRBG_POLL_INTERVAL") {
				t.Errorf("error = %q, want it to name AIRBG_POLL_INTERVAL so an operator can act on it", err)
			}
		})
	}
}

// TestLoadAcceptsExactlyTheMinimum pins the boundary: the floor is inclusive,
// so a deployment deliberately configured at the documented minimum is valid.
func TestLoadAcceptsExactlyTheMinimum(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_POLL_INTERVAL", MinPollInterval.String())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() at exactly MinPollInterval error = %v, want it accepted", err)
	}
	if cfg.PollInterval != MinPollInterval {
		t.Errorf("PollInterval = %v, want %v", cfg.PollInterval, MinPollInterval)
	}
}

func TestServeDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Errorf("ListenAddr = %q, want the loopback default", cfg.ListenAddr)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Errorf("MetricsAddr = %q, want the loopback default", cfg.MetricsAddr)
	}
	// Defaulting the trusted-proxy list to Cloudflare's ranges would mean a
	// developer running locally trusts CF-Connecting-IP from anyone on their
	// machine. Empty by default: trust nothing until an operator says so.
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Errorf("TrustedProxyCIDRs = %v, want empty by default", cfg.TrustedProxyCIDRs)
	}
	if cfg.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
}

func TestTrustedProxyCIDRsSplitsAndTrims(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_TRUSTED_PROXY_CIDRS", " 173.245.48.0/20 , 2400:cb00::/32 ,")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"173.245.48.0/20", "2400:cb00::/32"}
	if len(cfg.TrustedProxyCIDRs) != len(want) {
		t.Fatalf("TrustedProxyCIDRs = %v, want %v", cfg.TrustedProxyCIDRs, want)
	}
	for i := range want {
		if cfg.TrustedProxyCIDRs[i] != want[i] {
			t.Errorf("TrustedProxyCIDRs[%d] = %q, want %q", i, cfg.TrustedProxyCIDRs[i], want[i])
		}
	}
}

// TestMalformedTrustedProxyCIDRIsAStartupError. This list decides whose
// CF-Connecting-IP header is believed. A typo that is silently dropped shrinks
// the trusted set without telling anyone; a typo that is silently kept as a
// string is never matched. Either way the operator thinks the edge is trusted
// and it is not. Fail at boot.
func TestMalformedTrustedProxyCIDRIsAStartupError(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_TRUSTED_PROXY_CIDRS", "173.245.48.0/20,not-a-cidr")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a malformed CIDR")
	}
}

func TestBaseURLMustBeAbsolute(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_BASE_URL", "/airbg")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a relative AIRBG_BASE_URL; canonical and hreflang links would be broken")
	}
}
