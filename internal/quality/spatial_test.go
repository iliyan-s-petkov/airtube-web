package quality

import (
	"math"
	"testing"
)

func TestMedianAbsoluteDeviation(t *testing.T) {
	median, mad := MedianAbsoluteDeviation([]float64{1, 2, 3, 4, 5})
	if median != 3 {
		t.Errorf("median = %v, want 3", median)
	}
	if mad != 1 {
		t.Errorf("mad = %v, want 1", mad)
	}
}

func TestMedianIsRobustToOneWildOutlier(t *testing.T) {
	// The whole reason for median/MAD over mean/stddev: one sensor stuck at 900
	// must not drag the reference far enough to make itself look normal.
	median, _ := MedianAbsoluteDeviation([]float64{20, 21, 22, 23, 900})
	if median != 22 {
		t.Errorf("median = %v, want 22 — outlier contaminated the reference", median)
	}
}

func TestTemperatureOutlierIsFlagged(t *testing.T) {
	// The canonical case from the spec: 22 °C among −10 °C neighbours.
	neighbours := []Neighbour{
		{Value: -10}, {Value: -9.5}, {Value: -10.5}, {Value: -11},
	}
	if got := testScorer().SpatialCheck("temperature", 22, neighbours); got != FlagSpatialOutlier {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagSpatialOutlier)
	}
}

func TestNormalTemperatureVariationIsNotFlagged(t *testing.T) {
	neighbours := []Neighbour{
		{Value: -10}, {Value: -9.5}, {Value: -10.5}, {Value: -11},
	}
	if got := testScorer().SpatialCheck("temperature", -9.8, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagOK)
	}
}

func TestTightNeighbourhoodDoesNotFlagNormalVariation(t *testing.T) {
	// MAD is zero here. Without the per-metric floor, any deviation at all
	// would be infinitely many MADs and every reading would flag.
	neighbours := []Neighbour{
		{Value: 20}, {Value: 20}, {Value: 20}, {Value: 20},
	}
	if got := testScorer().SpatialCheck("temperature", 21, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v — floor not applied", got, FlagOK)
	}
}

func TestRealPMEpisodeIsNotFlagged(t *testing.T) {
	// A genuine winter inversion: this sensor reads 200 µg/m³ and so do its
	// neighbours. Flagging this would delete exactly the pollution the site
	// exists to report (spec §6.3).
	neighbours := []Neighbour{
		{Value: 180}, {Value: 210}, {Value: 195}, {Value: 220},
	}
	if got := testScorer().SpatialCheck("P1", 200, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagOK)
	}
}

func TestLocalPMSourceIsNotFlagged(t *testing.T) {
	// One neighbour burning wet wood. 4x the median but below the absolute
	// threshold, so it must survive — this is a real local source, not a fault.
	neighbours := []Neighbour{
		{Value: 30}, {Value: 28}, {Value: 32}, {Value: 25},
	}
	if got := testScorer().SpatialCheck("P1", 120, neighbours); got != FlagOK {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagOK)
	}
}

func TestBrokenPMSensorIsFlagged(t *testing.T) {
	// 900 against a street reading 30: over 5x the median AND over 150 absolute.
	neighbours := []Neighbour{
		{Value: 30}, {Value: 28}, {Value: 32}, {Value: 25},
	}
	if got := testScorer().SpatialCheck("P1", 900, neighbours); got != FlagSpatialOutlier {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagSpatialOutlier)
	}
}

// TestPMGuardThresholdsAreTheConfiguredOnes pins each half of the PM guard
// separately, against a case the OTHER half does not already decide.
//
// The existing PM tests cannot do this: with the shipped values every one of
// them still passes when either threshold alone is mutated, because whichever
// guard is left intact keeps deciding the verdict (a 120 against a median of 29
// is under 5x AND under 150). So the two constants were pinned by nothing
// before they moved into airbg.yaml, and inert_test.go only pins the file →
// Config half. This is the Config → SpatialCheck half.
func TestPMGuardThresholdsAreTheConfiguredOnes(t *testing.T) {
	// Median 40. 5 x 40 = 200, so the ratio guard alone decides anything
	// between 150 and 200, and the absolute guard alone decides anything above
	// 200 that is under the absolute floor.
	neighbours := []Neighbour{{Value: 38}, {Value: 42}, {Value: 40}, {Value: 40}}

	// 180: over the absolute floor (150) but only 4.5x the median, so the ratio
	// guard is what spares it.
	if got := testScorer().SpatialCheck("P1", 180, neighbours); got != FlagOK {
		t.Fatalf("shipped values: SpatialCheck(180) = %v, want %v", got, FlagOK)
	}
	tighter := testQuality()
	tighter.PMRatioThreshold = 4.0
	if got := NewScorer(tighter).SpatialCheck("P1", 180, neighbours); got != FlagSpatialOutlier {
		t.Errorf("with pm_ratio_threshold = 4: SpatialCheck(180) = %v, want %v; "+
			"the ratio guard is not reading quality.pm_ratio_threshold", got, FlagSpatialOutlier)
	}

	// 210: 5.25x the median AND over 150, so both guards agree. Raising the
	// absolute floor above it must spare it, which only the absolute guard can
	// do.
	if got := testScorer().SpatialCheck("P1", 210, neighbours); got != FlagSpatialOutlier {
		t.Fatalf("shipped values: SpatialCheck(210) = %v, want %v", got, FlagSpatialOutlier)
	}
	looser := testQuality()
	looser.PMAbsoluteThreshold = 250.0
	if got := NewScorer(looser).SpatialCheck("P1", 210, neighbours); got != FlagOK {
		t.Errorf("with pm_absolute_threshold = 250: SpatialCheck(210) = %v, want %v; "+
			"the absolute guard is not reading quality.pm_absolute_threshold", got, FlagOK)
	}
}

// TestSmoothFieldFloorIsTheConfiguredOne pins that the per-metric floor comes
// from quality.smooth_field_floors, and that a metric's ABSENCE from that map
// still means "no spatial expectation at all" rather than "floor of zero".
func TestSmoothFieldFloorIsTheConfiguredOne(t *testing.T) {
	// MAD is zero, so the floor is the whole decision.
	neighbours := []Neighbour{{Value: 20}, {Value: 20}, {Value: 20}, {Value: 20}}

	if got := testScorer().SpatialCheck("temperature", 21, neighbours); got != FlagOK {
		t.Fatalf("shipped floor 1.5: SpatialCheck(21) = %v, want %v", got, FlagOK)
	}
	tighter := testQuality()
	tighter.SmoothFieldFloors["temperature"] = 0.5
	if got := NewScorer(tighter).SpatialCheck("temperature", 21, neighbours); got != FlagSpatialOutlier {
		t.Errorf("with smooth_field_floors.temperature = 0.5: SpatialCheck(21) = %v, want %v",
			got, FlagSpatialOutlier)
	}

	// Noise is not in the map and must stay unchecked: a wildly deviating
	// value is FlagOK, not an outlier.
	if got := testScorer().SpatialCheck("noise_LAeq", 119, neighbours); got != FlagOK {
		t.Errorf("noise_LAeq is absent from smooth_field_floors: SpatialCheck = %v, want %v", got, FlagOK)
	}
}

func TestTooFewNeighbours(t *testing.T) {
	neighbours := []Neighbour{{Value: -10}, {Value: -9}}
	if got := testScorer().SpatialCheck("temperature", 22, neighbours); got != FlagNoNeighbours {
		t.Errorf("SpatialCheck = %v, want %v", got, FlagNoNeighbours)
	}
}

func TestNoNeighboursIsUsable(t *testing.T) {
	if !FlagNoNeighbours.Usable() {
		t.Error("no_neighbours must count toward aggregates — the check merely could not run")
	}
	if FlagSpatialOutlier.Usable() {
		t.Error("spatial_outlier must never count toward aggregates")
	}
}

func TestSpatialCheckIsDeterministic(t *testing.T) {
	neighbours := []Neighbour{{Value: -10}, {Value: -9.5}, {Value: -10.5}}
	first := testScorer().SpatialCheck("temperature", 22, neighbours)
	for i := 0; i < 100; i++ {
		if got := testScorer().SpatialCheck("temperature", 22, neighbours); got != first {
			t.Fatalf("non-deterministic: got %v then %v", first, got)
		}
	}
}

func TestSpatialCheckDoesNotMutateInput(t *testing.T) {
	neighbours := []Neighbour{{Value: 5}, {Value: 1}, {Value: 3}}
	testScorer().SpatialCheck("temperature", 3, neighbours)
	want := []float64{5, 1, 3}
	for i, n := range neighbours {
		if math.Abs(n.Value-want[i]) > 1e-9 {
			t.Fatalf("input reordered: neighbours[%d] = %v, want %v", i, n.Value, want[i])
		}
	}
}
