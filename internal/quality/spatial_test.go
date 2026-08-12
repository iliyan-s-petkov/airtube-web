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
