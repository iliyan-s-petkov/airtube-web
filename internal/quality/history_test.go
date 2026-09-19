package quality

import "testing"

func TestIsStuckAfterIdenticalObservations(t *testing.T) {
	h := NewHistory(12)

	for i := 0; i < 11; i++ {
		h.Observe(1, "P1", 42.0)
		if h.IsStuck(1, "P1") {
			t.Fatalf("stuck after %d identical observations, want at least 12", i+1)
		}
	}
	h.Observe(1, "P1", 42.0)
	if !h.IsStuck(1, "P1") {
		t.Error("not stuck after 12 identical observations")
	}
}

func TestJitterResetsStuck(t *testing.T) {
	h := NewHistory(12)
	for i := 0; i < 12; i++ {
		h.Observe(1, "P1", 42.0)
	}
	h.Observe(1, "P1", 42.1)
	if h.IsStuck(1, "P1") {
		t.Error("still stuck after the value changed")
	}
}

func TestExemptValuesNeverStick(t *testing.T) {
	// Humidity pinned at 100 %, PM at exactly 0, and humidity at 0 all occur
	// legitimately and must not be flagged (spec §6.2).
	cases := []struct {
		metric string
		value  float64
	}{
		{"humidity", 100},
		{"humidity", 0},
		{"P1", 0},
		{"P2", 0},
	}
	for _, c := range cases {
		h := NewHistory(12)
		for i := 0; i < 20; i++ {
			h.Observe(1, c.metric, c.value)
		}
		if h.IsStuck(1, c.metric) {
			t.Errorf("%s at %v flagged stuck, but this value is exempt", c.metric, c.value)
		}
	}
}

// TestHistoryEvictsOldestSensorOverCap proves History does not grow without
// bound as the upstream sensor population churns: once the tracked-sensor
// cap is exceeded, the oldest untouched sensor's state is dropped.
func TestHistoryEvictsOldestSensorOverCap(t *testing.T) {
	restore := SetHistoryMaxTrackedSensorsForTesting(2)
	defer restore()

	h := NewHistory(3)
	h.Observe(1, "P1", 10)
	h.Observe(1, "P1", 10)
	h.Observe(1, "P1", 10)
	if !h.IsStuck(1, "P1") {
		t.Fatal("sensor 1 should be stuck before eviction")
	}

	// Sensors 2 and 3 push the tracked set to 3, one over the cap of 2, so
	// sensor 1 — the oldest — must be evicted.
	h.Observe(2, "P1", 20)
	h.Observe(3, "P1", 30)

	if h.IsStuck(1, "P1") {
		t.Error("sensor 1's state should have been evicted once the cap was exceeded, so it reports not stuck (state was discarded, not that it is actually healthy)")
	}
}

func TestSensorsAreIndependent(t *testing.T) {
	h := NewHistory(12)
	for i := 0; i < 12; i++ {
		h.Observe(1, "P1", 42.0)
		h.Observe(2, "P1", float64(i))
	}
	if !h.IsStuck(1, "P1") {
		t.Error("sensor 1 should be stuck")
	}
	if h.IsStuck(2, "P1") {
		t.Error("sensor 2 varies and must not be stuck")
	}
}
