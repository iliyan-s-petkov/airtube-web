package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func good(t *testing.T) Config {
	t.Helper()
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile(airbg.yaml) error = %v, want nil", err)
	}
	return cfg
}

// The committed file plus a database URL must be valid. If this fails the
// shipped configuration cannot start.
func TestCommittedConfigValidates(t *testing.T) {
	_ = good(t)
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"metrics addr equal to public addr", func(c *Config) { c.Listen.MetricsAddr = c.Listen.Addr }, "must be separate"},
		{"zero max_conns", func(c *Config) { c.Listen.MaxConns = 0 }, "listen.max_conns"},
		{"unsafe-inline in csp", func(c *Config) { c.Listen.CSP += "; script-src 'unsafe-inline'" }, "unsafe-inline"},
		{"unsafe-eval in csp", func(c *Config) { c.Listen.CSP += "; script-src 'unsafe-eval'" }, "unsafe-eval"},
		{"bad cidr", func(c *Config) { c.Listen.TrustedProxyCIDRs = []string{"10.0.0.1"} }, "not a CIDR"},
		{"missing database url", func(c *Config) { c.Database.URL = "" }, DatabaseURLEnv},
		{"series timeout above default", func(c *Config) { c.Database.StatementTimeouts.Series = time.Minute }, "tighter budget"},
		{"burst below rate", func(c *Config) { c.RateLimit.Series.Burst = 0.5 }, "below .per_second"},
		{"evict beyond ttl", func(c *Config) { c.RateLimit.API.EvictInterval = 2 * time.Hour }, "outlive their bucket"},
		{"zero enumerate areas", func(c *Config) { c.RateLimit.Enumerate.AreasPerWindow = 0 }, "areas_per_window"},
		{"poll below floor", func(c *Config) { c.Upstream.PollInterval = 10 * time.Second }, "min_poll_interval"},
		{"cache above half poll", func(c *Config) { c.Cache.DataMaxAge = 4 * time.Minute }, "half of upstream.poll_interval"},
		{"zero coverage threshold", func(c *Config) { c.Store.CoverageThreshold = 0 }, "single sensor"},
		{"unknown default metric", func(c *Config) { c.Series.DefaultMetric = "PM9" }, "not a canonical metric"},
		{"default window matches no period", func(c *Config) { c.Series.DefaultWindow = 3 * time.Hour }, "matches no entry"},
		{"missing metric range", func(c *Config) { delete(c.Quality.Ranges, "pressure") }, "no entry for \"pressure\""},
		{"inverted range", func(c *Config) { c.Quality.Ranges["pressure"] = Range{Min: 1100, Max: 650} }, "must exceed min"},
		{"rejection fraction above one", func(c *Config) { c.Backfill.HighRejectionFraction = 1.5 }, "high_rejection_fraction"},
		{"bad colour", func(c *Config) { c.Frontend.NoDataColour = "grey" }, "hex colour"},
		{"zoom tiers inverted", func(c *Config) { c.Frontend.ZoomCity = 12 }, "must be below"},
		// Ported from the deleted internal/config/config_test.go (Task 8, Step
		// 1): these assert rules Validate() still owns, even though the old
		// loader tested them at env-parse time instead of validation time.
		{"malformed base_url", func(c *Config) { c.Listen.BaseURL = "not-a-url" }, "must use http or https"},
		{"relative base_url", func(c *Config) { c.Listen.BaseURL = "/path/to/site" }, "must use http or https"},
		{"zero database api_conns", func(c *Config) { c.Database.APIConns = 0 }, "database.api_conns"},
		{"zero database collector_conns", func(c *Config) { c.Database.CollectorConns = 0 }, "database.collector_conns"},
		{"zero database max_inflight", func(c *Config) { c.Database.MaxInflight = 0 }, "database.max_inflight"},
		// The equivalent of the deleted basemap.style_url host checks, carried
		// forward onto tiles.public_url: httpx.CSP-style header concatenation
		// means a host containing any of these characters could inject a new
		// CSP directive (up to reintroducing 'unsafe-inline') the moment it
		// reaches a response. hostPattern's charset is the only thing standing
		// between an operator-supplied host and that outcome.
		{"tiles host semicolon", func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example;object-src"}
		}, "not a valid hostname"},
		{"tiles host double quote", func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example\"evil"}
		}, "not a valid hostname"},
		{"tiles host single quote", func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example'evil"}
		}, "not a valid hostname"},
		{"tiles host comma", func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://tiles.example,evil"}
		}, "not a valid hostname"},
		// httpx.CSP concatenates the host into the policy twice (img-src and
		// connect-src), so an unbounded host doubles into an oversized header on
		// every response.
		{"tiles host longer than DNS limit", func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/tiles", PublicURL: "https://" + strings.Repeat("a", 254) + ".example"}
		}, "must be at most"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := good(t)
			tt.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want a rejection mentioning %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// Every violation must be reported in one pass. One-per-restart is a validator
// bug when the file has forty keys.
func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	cfg := good(t)
	cfg.Listen.MaxConns = 0
	cfg.Store.CoverageThreshold = 0
	cfg.Frontend.NoDataColour = "grey"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want three problems")
	}
	for _, want := range []string{"listen.max_conns", "store.coverage_threshold", "hex colour"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q:\n%s", want, err)
		}
	}
}

// Ported from the deleted config_test.go's TestAcceptsBasemapHostWithPort: a
// self-hosted tile server on a non-standard port is a legitimate deployment,
// and hostPattern must accept "host:port" rather than only a bare host.
func TestValidateAcceptsTilesHostWithPort(t *testing.T) {
	cfg := good(t)
	cfg.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/var/lib/airbg/tiles", PublicURL: "https://tiles.example:8443"}
	cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.example:8443"
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want a host:port tiles.public_url accepted", err)
	}
}

// Ported from the deleted config_test.go's TestLoadAcceptsExactlyTheMinimum:
// the poll-interval floor is inclusive, so a deployment configured at exactly
// upstream.min_poll_interval must validate.
func TestValidateAcceptsPollIntervalAtTheFloor(t *testing.T) {
	cfg := good(t)
	cfg.Upstream.PollInterval = cfg.Upstream.MinPollInterval
	// cache.data_max_age must also stay within half of the (now much shorter)
	// poll_interval — that is a separate rule (validateUpstreamAndCache) this
	// test is not exercising, so satisfy it rather than let it mask the
	// assertion this test exists for.
	cfg.Cache.DataMaxAge = cfg.Upstream.PollInterval / 2
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want poll_interval == min_poll_interval accepted", err)
	}
}

// validConfig loads the committed configuration, so these tests mutate the
// values the service actually ships with rather than a second copy that drifts.
func validConfig(t *testing.T) Config {
	t.Helper()
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	return cfg
}

// TestTilesAllOrNothing. Two of three keys set is the shape that produces a
// running server with a map that silently fetches from nowhere.
func TestTilesAllOrNothing(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"addr only":       func(c *Config) { c.Tiles = Tiles{Addr: "127.0.0.1:8082"} },
		"dir only":        func(c *Config) { c.Tiles = Tiles{Dir: "/var/lib/airbg/tiles"} },
		"public_url only": func(c *Config) { c.Tiles = Tiles{PublicURL: "https://tiles.airbg.org"} },
		"missing dir": func(c *Config) {
			c.Tiles = Tiles{Addr: "127.0.0.1:8082", PublicURL: "https://tiles.airbg.org"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate returned nil, want an error: tiles.* is all-or-nothing")
			}
		})
	}
}

// TestTilesEmptyIsLegal. No tiles configured means no basemap: the map island
// renders markers over frontend.empty_basemap_colour. Local development must
// not need a 300 MB file.
func TestTilesEmptyIsLegal(t *testing.T) {
	cfg := validConfig(t)
	cfg.Tiles = Tiles{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with no tiles configured = %v, want nil", err)
	}
	if cfg.Tiles.Enabled() {
		t.Error("Enabled() = true with every key empty")
	}
	if got := cfg.Tiles.StyleURL(); got != "" {
		t.Errorf("StyleURL() = %q, want empty", got)
	}
}

// TestTilesHostMustBeInConnectSrc. MapLibre fetches the style, the glyphs and
// the .pmtiles ranges over fetch/XHR. A CSP that omits the host fails closed and
// the map is blank, with nothing in any server log to say why.
func TestTilesHostMustBeInConnectSrc(t *testing.T) {
	cfg := validConfig(t)
	cfg.Tiles = Tiles{
		Addr:      "127.0.0.1:8082",
		Dir:       "/var/lib/airbg/tiles",
		PublicURL: "https://tiles.airbg.org",
	}
	cfg.Listen.CSP = "default-src 'self'; connect-src 'self'"
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate returned nil, want an error: connect-src omits tiles.airbg.org")
	}

	cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate with the host in connect-src = %v, want nil", err)
	}
	if got, want := cfg.Tiles.StyleURL(), "https://tiles.airbg.org/style.json"; got != want {
		t.Errorf("StyleURL() = %q, want %q", got, want)
	}
}

// TestTilesAddrIsSeparate. Sharing a listener address with the application or
// the metrics listener is the "three listeners simplified back to two" mistake
// in configuration form.
func TestTilesAddrIsSeparate(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "127.0.0.1:9090"} {
		cfg := validConfig(t)
		cfg.Listen.Addr = "127.0.0.1:8080"
		cfg.Listen.MetricsAddr = "127.0.0.1:9090"
		cfg.Tiles = Tiles{
			Addr:      addr,
			Dir:       "/var/lib/airbg/tiles",
			PublicURL: "https://tiles.airbg.org",
		}
		cfg.Listen.CSP = "default-src 'self'; connect-src 'self' https://tiles.airbg.org"
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate with tiles.addr = %q returned nil, want an error", addr)
		}
	}
}

// TestTilesPublicURLShape. The host reaches a Content-Security-Policy header
// assembled by concatenation, so anything but a plain absolute http(s) URL is
// rejected — the same rule the deleted basemap.style_url carried.
func TestTilesPublicURLShape(t *testing.T) {
	for name, u := range map[string]string{
		"no scheme": "tiles.airbg.org",
		"ftp":       "ftp://tiles.airbg.org",
		"userinfo":  "https://user:pass@tiles.airbg.org",
		"space":     "https://tiles airbg.org",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig(t)
			cfg.Tiles = Tiles{Addr: "127.0.0.1:8082", Dir: "/var/lib/airbg/tiles", PublicURL: u}
			cfg.Listen.CSP = "default-src 'self'; connect-src 'self' " + u
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate with tiles.public_url = %q returned nil, want an error", u)
			}
		})
	}
}
