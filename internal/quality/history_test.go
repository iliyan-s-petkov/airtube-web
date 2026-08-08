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
