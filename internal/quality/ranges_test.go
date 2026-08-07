package quality

import "testing"

func TestInRange(t *testing.T) {
	cases := []struct {
		metric string
		value  float64
		want   bool
	}{
		{"P1", 24.3, true},
		{"P1", 0, true},
		{"P1", 1000, true},
		{"P1", 1000.1, false},
		{"P1", -1, false},
		{"temperature", -40, true},
		{"temperature", 60, true},
		{"temperature", -999, false},
		{"temperature", 61, false},
		{"humidity", 0, true},
		{"humidity", 100, true},
		{"humidity", 101, false},
		{"pressure", 94210, false}, // Pascals, not hPa — must be converted first
		{"pressure", 942.1, true},
		{"noise_LAeq", 24.9, false},
		{"noise_LAeq", 25, true},
		{"noise_LAeq", 120, true},
		{"unknown_metric", 5, false},
	}

	for _, c := range cases {
		if got := InRange(c.metric, c.value); got != c.want {
			t.Errorf("InRange(%q, %v) = %v, want %v", c.metric, c.value, got, c.want)
		}
	}
}
