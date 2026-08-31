package config

import (
	"path/filepath"
	"testing"
	"time"
)

// resolve must reproduce the committed file's values exactly. These assertions
// are the anchor for the behaviour-unchanged requirement: each value here equals
// the constant it replaces in the package named in the comment.
func TestResolveCommittedConfig(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)

	if got, want := cfg.Listen.MaxConns, int32(4096); got != want {
		t.Errorf("Listen.MaxConns = %d, want %d", got, want) // server.defaultMaxConns
	}
	if got, want := cfg.Timeouts.ReadHeader, 5*time.Second; got != want {
		t.Errorf("Timeouts.ReadHeader = %v, want %v", got, want) // server.readHeaderTimeout
	}
	if got, want := cfg.Database.StatementTimeouts.Series, 5*time.Second; got != want {
		t.Errorf("StatementTimeouts.Series = %v, want %v", got, want) // db.SeriesStatementTimeout
	}
	if got, want := cfg.RateLimit.Enumerate.AreasPerWindow, 20; got != want {
		t.Errorf("Enumerate.AreasPerWindow = %d, want %d", got, want) // raised from 12, see docs/superpowers/specs/2026-08-17-airbg-deployment-design.md
	}
	if got, want := cfg.Cache.DataMaxAge, 150*time.Second; got != want {
		t.Errorf("Cache.DataMaxAge = %v, want %v", got, want) // api.dataMaxAge
	}
	if got, want := cfg.Store.CoverageThreshold, 3; got != want {
		t.Errorf("Store.CoverageThreshold = %d, want %d", got, want) // store.CoverageThreshold
	}
	if got, want := cfg.Quality.MADScale, 1.4826; got != want {
		t.Errorf("Quality.MADScale = %v, want %v", got, want) // quality.madScale
	}
	if got, want := cfg.Quality.Ranges["pressure"].Min, 650.0; got != want {
		t.Errorf("Quality.Ranges[pressure].Min = %v, want %v", got, want) // quality.ranges
	}
	if got, want := cfg.Backfill.HighRejectionFraction, 0.5; got != want {
		t.Errorf("Backfill.HighRejectionFraction = %v, want %v", got, want)
	}
	if got, want := cfg.Frontend.NoDataColour, "#9ca3af"; got != want {
		t.Errorf("Frontend.NoDataColour = %q, want %q", got, want) // colour.js NO_DATA_COLOUR
	}
}

// All seven canonical metrics must have a range. A missing entry would mean a
// metric whose readings are never plausibility-checked.
func TestResolveHasEveryMetricRange(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)
	for _, m := range []string{"P1", "P2", "temperature", "humidity", "pressure", "noise_LAeq", "noise_LA_max"} {
		rng, ok := cfg.Quality.Ranges[m]
		if !ok {
			t.Errorf("Quality.Ranges is missing %q", m)
			continue
		}
		if rng.Max <= rng.Min {
			t.Errorf("Quality.Ranges[%q] = %+v, want Max > Min", m, rng)
		}
	}
}

// The four periods must resolve with their per-period cache lifetimes, and the
// 1-year period must be hourly: raw readings are retained for 30 days, so a
// 1-year window against the raw table returns 30 days under a "1 year" label.
func TestResolvePeriods(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)
	want := map[string]Period{
		"24h": {24 * time.Hour, false, 5 * time.Minute, 150 * time.Second},
		"7d":  {7 * 24 * time.Hour, false, time.Hour, 600 * time.Second},
		"30d": {30 * 24 * time.Hour, false, 6 * time.Hour, 1800 * time.Second},
		"1y":  {365 * 24 * time.Hour, true, 24 * time.Hour, 10800 * time.Second},
	}
	for name, w := range want {
		got, ok := cfg.Series.Periods[name]
		if !ok {
			t.Errorf("Series.Periods is missing %q", name)
			continue
		}
		if got != w {
			t.Errorf("Series.Periods[%q] = %+v, want %+v", name, got, w)
		}
	}
	if got, want := len(cfg.Series.PeriodNames), 4; got != want {
		t.Errorf("len(Series.PeriodNames) = %d, want %d", got, want)
	}
	if got, want := cfg.Series.PeriodNames[0], "24h"; got != want {
		t.Errorf("Series.PeriodNames[0] = %q, want %q (file order is UI order)", got, want)
	}
}

// Duration fields must be converted using .Std() to get time.Duration.
// A mutation that swaps the .Std() call or removes it should break this test.
func TestResolveDurationConversion(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)

	// Check that duration fields are correct time.Duration values, not
	// truncated to seconds or nanoseconds.
	if got, want := cfg.Timeouts.Read, 10*time.Second; got != want {
		t.Errorf("Timeouts.Read = %v, want %v", got, want)
	}
	if got, want := cfg.Cache.ScalesMaxAge, 86400*time.Second; got != want {
		t.Errorf("Cache.ScalesMaxAge = %v, want %v", got, want)
	}
	if got, want := cfg.Upstream.MinPollInterval, 30*time.Second; got != want {
		t.Errorf("Upstream.MinPollInterval = %v, want %v", got, want)
	}
}

// Series.PeriodNames preserves file order. A mutation that sorts or reverses
// the slice should break this test.
func TestResolvePeriodOrderPreservation(t *testing.T) {
	r, err := readRaw(filepath.Join("..", "..", "airbg.yaml"))
	if err != nil {
		t.Fatalf("readRaw error = %v, want nil", err)
	}
	cfg := resolve(r)

	// File order must be preserved: 24h, 7d, 30d, 1y.
	want := []string{"24h", "7d", "30d", "1y"}
	if len(cfg.Series.PeriodNames) != len(want) {
		t.Fatalf("len(Series.PeriodNames) = %d, want %d", len(cfg.Series.PeriodNames), len(want))
	}
	for i, name := range want {
		if got := cfg.Series.PeriodNames[i]; got != name {
			t.Errorf("Series.PeriodNames[%d] = %q, want %q (file order violation)", i, got, name)
		}
	}
}
