package upstream

import (
	"os"
	"testing"
	"time"
)

func TestNormaliseSelectsCanonicalMetrics(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, skipped, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	// 2 from sensor 12345 (P1, P2 — durP1 and signal dropped),
	// 3 from sensor 12346. Sensor 12347 has no coordinates.
	if len(readings) != 5 {
		t.Fatalf("len(readings) = %d, want 5", len(readings))
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}

	for _, r := range readings {
		if !IsCanonicalMetric(r.Metric) {
			t.Errorf("non-canonical metric %q survived normalisation", r.Metric)
		}
	}
}

func TestNormaliseCoordinateOrder(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	for _, r := range readings {
		if r.Lon < 22 || r.Lon > 29 {
			t.Errorf("sensor %d Lon = %v, outside Bulgaria — lat/lon swapped", r.SensorID, r.Lon)
		}
		if r.Lat < 41 || r.Lat > 45 {
			t.Errorf("sensor %d Lat = %v, outside Bulgaria — lat/lon swapped", r.SensorID, r.Lat)
		}
	}
}

func TestNormaliseParsesTimestamp(t *testing.T) {
	payload, err := os.ReadFile("testdata/bg_sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	readings, _, err := Normalise(payload)
	if err != nil {
		t.Fatalf("Normalise: %v", err)
	}

	want := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if !readings[0].Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", readings[0].Timestamp, want)
	}
}

func TestNormaliseRejectsGarbage(t *testing.T) {
	if _, _, err := Normalise([]byte("not json")); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}
