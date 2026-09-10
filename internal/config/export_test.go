package config

// CanonicalMetricsForTest exposes the validator's metric set to the external
// config_test package, which is where the comparison against
// upstream.CanonicalMetrics() has to live: internal/upstream imports
// internal/config, so an in-package test importing upstream would be a cycle.
func CanonicalMetricsForTest() map[string]bool { return canonicalMetrics }
