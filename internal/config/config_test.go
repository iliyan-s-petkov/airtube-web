package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("AIRBG_DATABASE_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when AIRBG_DATABASE_URL is unset, got nil")
	}
}

func TestLoadDefaults(t *testing.T) {
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
