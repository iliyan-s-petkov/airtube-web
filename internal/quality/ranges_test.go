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

// TestPressureRangeAllowsBulgarianAltitude guards the 650 hPa floor. Before
// this change the floor was 800 hPa (~2000 m), which flagged real readings
// from high-altitude Bulgarian sites (Rila, Pirin — Musala summit is 2925 m,
// ~715 hPa) as out_of_range. 690 hPa (~3100 m) must be accepted; this
// assertion fails against the pre-change 800 hPa floor, demonstrating the bug
// this widened range fixes.
func TestPressureRangeAllowsBulgarianAltitude(t *testing.T) {
	if !InRange("pressure", 690) {
		t.Error("690 hPa must be InRange — this is exactly the case the pre-change 800 hPa floor wrongly rejected")
	}
}

// TestPressureRangeStillRejectsBelowFloor confirms widening the floor did not
// disable the check: a value below the new 650 hPa floor must still be
// rejected as out of range.
func TestPressureRangeStillRejectsBelowFloor(t *testing.T) {
	if InRange("pressure", 649.9) {
		t.Error("649.9 hPa must be out of range — below the new 650 hPa floor")
	}
}
