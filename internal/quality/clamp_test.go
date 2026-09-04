package quality_test

import (
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/quality"
	"airbg.org/internal/upstream"
)

// clampScorer mirrors the committed config: P1 pegs above its range, P2 inside
// it. The asymmetry is the point — see README.md.
func clampScorer() *quality.Scorer {
	return quality.NewScorer(config.Quality{
		MinNeighbours:         3,
		MADScale:              1.4826,
		MADThreshold:          3.5,
		NeighbourRadiusMetres: 15000,
		EarthRadiusMetres:     6371000,
		HistoryDepth:          6,
		PMRatioThreshold:      3,
		PMAbsoluteThreshold:   50,
		SmoothFieldFloors:     map[string]float64{"temperature": 1.5},
		Ranges: map[string]config.Range{
			"P1": {Min: 0, Max: 1000},
			"P2": {Min: 0, Max: 1000},
		},
		ClampSentinels: map[string]float64{"P1": 1999.9, "P2": 999.9},
	})
}

func clampReading(id int64, metric string, value, lon, lat float64) upstream.Reading {
	return upstream.Reading{
		SensorID: id, Metric: metric, Value: value,
		Lon: lon, Lat: lat, Timestamp: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
	}
}

// The live defect, measured on the 2026-09-04 payload: 30 pegged SDS011s, and
// the P2 half scored ok.
func TestClampedSentinelIsFlagged(t *testing.T) {
	s := clampScorer()
	for _, tc := range []struct {
		metric string
		value  float64
	}{
		{"P1", 1999.9},
		{"P2", 999.9},
	} {
		got := s.Score([]upstream.Reading{clampReading(1, tc.metric, tc.value, 23.3, 42.7)}, quality.NewHistory(6))
		if got[0].Flag != quality.FlagClamped {
			t.Errorf("%s = %v scored %q, want %q", tc.metric, tc.value, got[0].Flag, quality.FlagClamped)
		}
	}
}

// Pins the check order: P1's sentinel is out of range too, so without one the
// pair splits across two flags.
func TestClampedTakesPrecedenceOverOutOfRange(t *testing.T) {
	s := clampScorer()
	got := s.Score([]upstream.Reading{clampReading(1, "P1", 1999.9, 23.3, 42.7)}, quality.NewHistory(6))
	if got[0].Flag == quality.FlagOutOfRange {
		t.Error("P1 sentinel scored out_of_range; the clamp check must run first so both halves of the pair carry the same flag")
	}
}

// The reason for exact equality: 999.8 is a real concentration.
func TestNearSentinelValuesAreUntouched(t *testing.T) {
	s := clampScorer()
	for _, tc := range []struct {
		metric string
		value  float64
	}{
		{"P2", 999.8},
		{"P2", 998},
		{"P1", 1999.8},
	} {
		got := s.Score([]upstream.Reading{clampReading(1, tc.metric, tc.value, 23.3, 42.7)}, quality.NewHistory(6))
		if got[0].Flag == quality.FlagClamped {
			t.Errorf("%s = %v scored clamped; only the exact sentinel may be", tc.metric, tc.value)
		}
	}
}

// The harm the flag alone does not undo: a pegged sensor was dragging the
// median its own neighbours were scored against.
func TestSentinelIsExcludedFromTheReferencePopulation(t *testing.T) {
	s := clampScorer()

	// Proportion is constructed, not typical: the sentinels must carry the
	// median past the subject, and three clean neighbours is the MinNeighbours
	// floor.
	readings := []upstream.Reading{
		clampReading(1, "P2", 120, 23.300, 42.700), // subject
		clampReading(2, "P2", 10, 23.310, 42.700),
		clampReading(3, "P2", 12, 23.320, 42.700),
		clampReading(4, "P2", 11, 23.330, 42.700),
		clampReading(5, "P2", 999.9, 23.340, 42.700),
		clampReading(6, "P2", 999.9, 23.350, 42.700),
		clampReading(7, "P2", 999.9, 23.360, 42.700),
	}
	got := s.Score(readings, quality.NewHistory(6))

	var subject quality.Flag
	for _, sc := range got {
		if sc.Reading.SensorID == 1 {
			subject = sc.Flag
		}
	}
	// Verified by mutation: dropping the IsClamped term from Score's reference
	// loop turns this red.
	if subject != quality.FlagSpatialOutlier {
		t.Errorf("subject scored %q, want %q — the sentinel is still in the reference median", subject, quality.FlagSpatialOutlier)
	}
}

// Flagging a reading and then averaging it in is worse than not checking.
func TestClampedIsNotUsable(t *testing.T) {
	if quality.FlagClamped.Usable() {
		t.Error("FlagClamped.Usable() = true; a saturated reading must not reach an average")
	}
}
