package config_test

import (
	"slices"
	"testing"

	"airbg.org/internal/config"
	"airbg.org/internal/upstream"
)

// The validator keeps its own copy of the canonical metric set because
// internal/upstream imports internal/config. This test is the only thing
// stopping the copy drifting again, as it did between c3f66bc and the fix wave
// (series.default_metric: NO2 was rejected, and the gas quality.ranges guard
// never ran).
func TestCanonicalMetricsMatchUpstream(t *testing.T) {
	got := make([]string, 0, len(config.CanonicalMetricsForTest()))
	for m := range config.CanonicalMetricsForTest() {
		got = append(got, m)
	}
	slices.Sort(got)

	if want := upstream.CanonicalMetrics(); !slices.Equal(got, want) {
		t.Errorf("config canonical metrics = %v, want upstream.CanonicalMetrics() = %v", got, want)
	}
}
