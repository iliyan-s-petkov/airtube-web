package eea

import "fmt"

// metricForCode maps EEA pollutant vocabulary codes onto canonical metric
// names. PM10 and PM2.5 reuse the sensor.community names P1 and P2 so both
// networks share one scale table.
var metricForCode = map[int32]string{
	1:    "SO2",
	5:    "P1",
	7:    "O3",
	8:    "NO2",
	9:    "NOX",
	10:   "CO",
	20:   "C6H6",
	6001: "P2",
}

// MetricFor returns the canonical metric name for an EEA pollutant code, and
// false for the hundreds of codes we do not carry.
func MetricFor(code int32) (string, bool) {
	m, ok := metricForCode[code]
	return m, ok
}

// unitFactor converts an EEA unit into µg/m³, the unit every scale table in
// internal/api/scales.go is written against. CO arrives in mg.m-3.
var unitFactor = map[string]float64{
	"ug.m-3": 1,
	"mg.m-3": 1000,
}

// NormaliseValue converts a reading into µg/m³. An unrecognised unit returns
// an error rather than passing the value through unscaled.
func NormaliseValue(metric string, value float64, unit string) (float64, error) {
	f, ok := unitFactor[unit]
	if !ok {
		return 0, fmt.Errorf("eea: metric %s: unknown unit %q", metric, unit)
	}
	return value * f, nil
}
