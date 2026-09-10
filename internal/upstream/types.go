// Package upstream fetches and normalises readings from sensor.community.
package upstream

import (
	"sort"
	"time"
)

// Reading is one metric from one sensor at one instant, already normalised.
type Reading struct {
	SensorID   int64
	SensorType string
	Lon        float64 // longitude first, matching PostGIS geography
	Lat        float64
	Metric     string
	Value      float64
	Timestamp  time.Time
}

// canonicalMetrics is the exact set stored; anything else upstream sends is
// dropped. The last six come only from internal/upstream/eea — no
// sensor.community device measures them.
var canonicalMetrics = map[string]bool{
	"P1":           true,
	"P2":           true,
	"temperature":  true,
	"humidity":     true,
	"pressure":     true,
	"noise_LAeq":   true,
	"noise_LA_max": true,
	"SO2":          true,
	"O3":           true,
	"NO2":          true,
	"NOX":          true,
	"CO":           true,
	"C6H6":         true,
}

func IsCanonicalMetric(m string) bool { return canonicalMetrics[m] }

// CanonicalMetrics returns the set as a sorted slice, so an error message can
// tell an operator what was expected instead of only what was wrong. Sorted
// because map iteration order would make the same error read differently on
// each run.
func CanonicalMetrics() []string {
	names := make([]string, 0, len(canonicalMetrics))
	for m := range canonicalMetrics {
		names = append(names, m)
	}
	sort.Strings(names)
	return names
}
