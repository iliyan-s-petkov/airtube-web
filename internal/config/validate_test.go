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
		{"basemap userinfo", func(c *Config) { c.Basemap.StyleURL = "https://u:p@tiles.example.org/s.json" }, "userinfo"},
		// Ported from the deleted internal/config/config_test.go (Task 8, Step
		// 1): these assert rules Validate() still owns, even though the old
		// loader tested them at env-parse time instead of validation time.
		{"malformed base_url", func(c *Config) { c.Listen.BaseURL = "not-a-url" }, "must use http or https"},
		{"relative base_url", func(c *Config) { c.Listen.BaseURL = "/path/to/site" }, "must use http or https"},
		{"empty basemap style_url", func(c *Config) { c.Basemap.StyleURL = "" }, "basemap.style_url is empty"},
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

// A secret in the environment must never be required to be in the file, and the
// {key} placeholder must be substituted before the URL is validated or served.
func TestBasemapKeySubstitution(t *testing.T) {
	t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/airbg")
	t.Setenv(BasemapKeyEnv, "s3cr3t")
	cfg, err := LoadFile(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("LoadFile error = %v, want nil", err)
	}
	if strings.Contains(cfg.Basemap.StyleURL, "{key}") {
		t.Errorf("Basemap.StyleURL still contains the {key} placeholder: %q", cfg.Basemap.StyleURL)
	}
	if !strings.Contains(cfg.Basemap.StyleURL, "s3cr3t") {
		t.Errorf("Basemap.StyleURL = %q, want the key substituted", cfg.Basemap.StyleURL)
	}
}
