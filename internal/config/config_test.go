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
