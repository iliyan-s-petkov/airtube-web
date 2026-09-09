package eea_test

import (
	"testing"

	"airbg.org/internal/upstream"
	"airbg.org/internal/upstream/eea"
)

// PM10 and PM2.5 map onto the metric names the citizen sensors already use, so
// one station and one nephelometer are comparable on the same scale table.
func TestMetricForMapsParticulatesOntoTheExistingNames(t *testing.T) {
	for code, want := range map[int32]string{5: "P1", 6001: "P2"} {
		got, ok := eea.MetricFor(code)
		if !ok || got != want {
			t.Errorf("MetricFor(%d) = %q, %v; want %q, true", code, got, ok, want)
		}
	}
}

func TestMetricForMapsTheGases(t *testing.T) {
	for code, want := range map[int32]string{
		1: "SO2", 7: "O3", 8: "NO2", 9: "NOX", 10: "CO", 20: "C6H6",
	} {
		got, ok := eea.MetricFor(code)
		if !ok || got != want {
			t.Errorf("MetricFor(%d) = %q, %v; want %q, true", code, got, ok, want)
		}
	}
}

// An unknown code is skipped, never guessed at: EEA publishes hundreds of
// pollutant codes and we have a scale table for eight of them.
func TestMetricForRejectsUnknownCodes(t *testing.T) {
	if _, ok := eea.MetricFor(38); ok {
		t.Error("MetricFor accepted an unmapped pollutant code")
	}
}

// Every metric we map must be one the rest of the system already knows how to
// store, colour and chart.
func TestEveryMappedMetricIsCanonical(t *testing.T) {
	for _, code := range []int32{1, 5, 7, 8, 9, 10, 20, 6001} {
		m, _ := eea.MetricFor(code)
		if !upstream.IsCanonicalMetric(m) {
			t.Errorf("MetricFor(%d) = %q, which is not a canonical metric", code, m)
		}
	}
}

// CO is the one pollutant the agency publishes in mg/m³. Storing it unconverted
// would put a real reading a thousandfold below every band edge.
func TestNormaliseValueConvertsCO(t *testing.T) {
	got, err := eea.NormaliseValue("CO", 1.25, "mg.m-3")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1250 {
		t.Errorf("NormaliseValue = %v, want 1250", got)
	}
}

func TestNormaliseValuePassesMicrogramsThrough(t *testing.T) {
	got, err := eea.NormaliseValue("P1", 42.5, "ug.m-3")
	if err != nil {
		t.Fatal(err)
	}
	if got != 42.5 {
		t.Errorf("NormaliseValue = %v, want 42.5", got)
	}
}

// An unrecognised unit must error, not pass through: the scale tables assume
// µg/m³.
func TestNormaliseValueRejectsAnUnknownUnit(t *testing.T) {
	if _, err := eea.NormaliseValue("P1", 42.5, "ppb"); err == nil {
		t.Error("NormaliseValue accepted an unknown unit")
	}
}
