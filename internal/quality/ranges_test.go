package quality

import (
	"math"
	"testing"
)

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
		if got := testScorer().InRange(c.metric, c.value); got != c.want {
			t.Errorf("InRange(%q, %v) = %v, want %v", c.metric, c.value, got, c.want)
		}
	}
}

// TestInRangeRejectsNonFiniteValues pins InRange's fail-closed behaviour on
// NaN and ±Inf for every canonical metric.
//
// This is reachable from upstream text, not a theoretical concern:
// strconv.ParseFloat — which both internal/upstream and internal/backfill use
// on raw upstream/archive fields — accepts "nan", "NaN", "inf", "+Inf" and
// "Infinity" and returns the corresponding non-finite float without error.
//
// The behaviour is currently correct only by accident of expression shape:
// `value >= r.min && value <= r.max` is false for NaN because *every*
// comparison against NaN is false. Nothing in ranges.go says so, and the
// equivalent-looking rewrite `!(value < r.min || value > r.max)` returns TRUE
// for NaN — silently admitting NaN into stored readings, into neighbour
// medians, and (via a NaN avg_value that json.Marshal refuses to encode) into
// a Phase 2 API response that would then fail in its entirety. This assertion
// is what makes that rewrite fail instead of pass.
func TestInRangeRejectsNonFiniteValues(t *testing.T) {
	metrics := []string{"P1", "P2", "temperature", "humidity", "pressure", "noise_LAeq", "noise_LA_max"}
	nonFinite := map[string]float64{
		"NaN":  math.NaN(),
		"+Inf": math.Inf(1),
		"-Inf": math.Inf(-1),
	}

	for _, metric := range metrics {
		for name, value := range nonFinite {
			if testScorer().InRange(metric, value) {
				t.Errorf("InRange(%q, %s) = true, want false — non-finite values must always fail closed", metric, name)
			}
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
	if !testScorer().InRange("pressure", 690) {
		t.Error("690 hPa must be InRange — this is exactly the case the pre-change 800 hPa floor wrongly rejected")
	}
}

// TestPressureRangeStillRejectsBelowFloor confirms widening the floor did not
// disable the check: a value below the new 650 hPa floor must still be
// rejected as out of range.
func TestPressureRangeStillRejectsBelowFloor(t *testing.T) {
	if testScorer().InRange("pressure", 649.9) {
		t.Error("649.9 hPa must be out of range — below the new 650 hPa floor")
	}
}

// TestInRangeRejectsUnknownMetric pins the branch the free function did not
// have: a metric with no configured range must be rejected, not accepted. An
// unknown metric flowing through unchecked is how bad data reaches an average.
func TestInRangeRejectsUnknownMetric(t *testing.T) {
	s := testScorer()
	if s.InRange("PM9", 5) {
		t.Error("InRange(\"PM9\", 5) = true, want false: an unconfigured metric must not pass unchecked")
	}
}
