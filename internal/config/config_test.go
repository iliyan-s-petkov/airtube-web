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
	t.Setenv("AIRBG_BASEMAP_STYLE_URL", "")
	t.Setenv("AIRBG_BASEMAP_KEY", "")
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

// TestBasemapKeyIsSubstitutedIntoTheStyleURL. An unsubstituted {key} reaches the
// browser and fails every tile request with a vendor error that looks nothing
// like "you forgot an environment variable".
func TestBasemapKeyIsSubstitutedIntoTheStyleURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_BASEMAP_STYLE_URL", "https://tiles.example/style.json?key={key}")
	t.Setenv("AIRBG_BASEMAP_KEY", "s3cret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.BasemapStyleURL, "https://tiles.example/style.json?key=s3cret"; got != want {
		t.Errorf("BasemapStyleURL = %q, want %q", got, want)
	}
	if got, want := cfg.BasemapHost, "tiles.example"; got != want {
		t.Errorf("BasemapHost = %q, want %q", got, want)
	}
}

// TestNoBasemapConfiguredIsNotAnError. Local development must work with no
// vendor account: the map renders markers over a plain background.
func TestNoBasemapConfiguredIsNotAnError(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BasemapStyleURL != "" || cfg.BasemapHost != "" {
		t.Errorf("BasemapStyleURL = %q, BasemapHost = %q, want both empty", cfg.BasemapStyleURL, cfg.BasemapHost)
	}
}

// TestRejectsNonHTTPSBasemapURL. An http tile source is a mixed-content failure
// in every browser, so accepting it would ship a map that cannot work.
func TestRejectsNonHTTPSBasemapURL(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"plain http", "http://tiles.example/style.json"},
		{"no scheme", "tiles.example/style.json"},
		{"no host", "https:///style.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
			t.Setenv("AIRBG_BASEMAP_STYLE_URL", tc.value)

			if _, err := Load(); err == nil {
				t.Errorf("Load() accepted AIRBG_BASEMAP_STYLE_URL=%q", tc.value)
			} else if !strings.Contains(err.Error(), "AIRBG_BASEMAP_STYLE_URL") {
				t.Errorf("error does not name the variable: %v", err)
			}
		})
	}
}

// TestRejectsHostileBasemapHost is the injection-surface test the CSP change
// creates. httpx.CSP builds the header by string concatenation, so a
// AIRBG_BASEMAP_STYLE_URL whose host contains a semicolon, a quote or an
// apostrophe could otherwise widen the policy with an attacker-chosen
// directive — up to and including reintroducing 'unsafe-inline' — the moment
// it reaches a response. net/url's Host field does not protect against this on
// its own: it accepts each of these characters as long as the value has no
// bare space (proven by exploration, not assumed), so the rejection has to
// happen here, at config load, rather than by trusting url.Parse succeeding.
func TestRejectsHostileBasemapHost(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"semicolon injects a directive", "https://tiles.example;object-src/style.json"},
		{"double quote", "https://tiles.example\"evil/style.json"},
		{"single quote", "https://tiles.example'evil/style.json"},
		{"comma", "https://tiles.example,evil/style.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
			t.Setenv("AIRBG_BASEMAP_STYLE_URL", tc.value)

			if _, err := Load(); err == nil {
				t.Errorf("Load() accepted hostile AIRBG_BASEMAP_STYLE_URL=%q", tc.value)
			} else if !strings.Contains(err.Error(), "AIRBG_BASEMAP_STYLE_URL") {
				t.Errorf("error does not name the variable: %v", err)
			}
		})
	}
}

// TestAcceptsBasemapHostWithPort. "host:port" is explicitly in scope — a
// self-hosted tile server behind a non-standard port is a legitimate
// deployment, and rejecting it would push an operator toward a workaround
// this validation cannot see.
func TestAcceptsBasemapHostWithPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_BASEMAP_STYLE_URL", "https://tiles.example:8443/style.json")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := cfg.BasemapHost, "tiles.example:8443"; got != want {
		t.Errorf("BasemapHost = %q, want %q", got, want)
	}
}

// TestRejectsBasemapURLWithCredentials. PageData.BasemapStyleURL ships this
// string verbatim to every browser that loads the map (Task 6), so a URL
// carrying "user:pw@" would leak those credentials to every visitor the
// moment that field is rendered. Nothing consumes the field yet, which is the
// only reason this is not already a live leak — Load must reject it outright
// rather than silently stripping the userinfo, so a misconfigured
// authenticated basemap fails loudly instead of quietly serving as if
// unauthenticated. The value below is an obvious placeholder, not a
// realistic-looking credential.
func TestRejectsBasemapURLWithCredentials(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	t.Setenv("AIRBG_BASEMAP_STYLE_URL", "https://placeholder-user:placeholder-pass@tiles.example/style.json")

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() accepted a AIRBG_BASEMAP_STYLE_URL with userinfo; BasemapStyleURL = %q", cfg.BasemapStyleURL)
	}
	if !strings.Contains(err.Error(), "AIRBG_BASEMAP_STYLE_URL") {
		t.Errorf("error does not name the variable: %v", err)
	}
	if cfg.BasemapStyleURL != "" {
		t.Errorf("BasemapStyleURL = %q on error, want empty", cfg.BasemapStyleURL)
	}
}

// TestRejectsBasemapHostLongerThanDNSLimit. hostPattern's charset check does
// not bound length, and httpx.CSP concatenates the host into the policy
// twice (img-src and connect-src) — an operator-supplied host of unbounded
// length would double into an oversized header on every response. 253 is the
// DNS name limit (RFC 1035 §3.1); one character past it must be rejected.
func TestRejectsBasemapHostLongerThanDNSLimit(t *testing.T) {
	clearEnv(t)
	t.Setenv("AIRBG_DATABASE_URL", "postgres://localhost/airbg")
	longHost := strings.Repeat("a", 254) + ".example"
	t.Setenv("AIRBG_BASEMAP_STYLE_URL", "https://"+longHost+"/style.json")

	if _, err := Load(); err == nil {
		t.Errorf("Load() accepted a %d-character AIRBG_BASEMAP_STYLE_URL host", len(longHost))
	} else if !strings.Contains(err.Error(), "AIRBG_BASEMAP_STYLE_URL") {
		t.Errorf("error does not name the variable: %v", err)
	}
}
