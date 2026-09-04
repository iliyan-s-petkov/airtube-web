package quality_test

import (
	"testing"
	"time"

	"airbg.org/internal/config"
	"airbg.org/internal/quality"
	"airbg.org/internal/upstream"
)

// clampScorer is the committed configuration's shape: P1 pegs above its range,
// P2 pegs inside it. That asymmetry is the whole reason the check exists, so
// the fixture has to preserve it rather than tidy it.
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

// TestClampedSentinelIsFlagged is the live defect, measured rather than
// assumed: on 2026-09-04 the upstream payload for the six enabled countries
// carried 30 SDS011s reporting exactly P1 1999.9 and P2 999.9, always as a
// pair, never one without the other. The P2 half sat inside the configured
// 0–1000 range and so was scored ok.
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

// TestClampedTakesPrecedenceOverOutOfRange pins the check order. P1's sentinel
// is out of range too, so without an explicit order the pair would be split
// across two flags for one physical event.
func TestClampedTakesPrecedenceOverOutOfRange(t *testing.T) {
	s := clampScorer()
	got := s.Score([]upstream.Reading{clampReading(1, "P1", 1999.9, 23.3, 42.7)}, quality.NewHistory(6))
	if got[0].Flag == quality.FlagOutOfRange {
		t.Error("P1 sentinel scored out_of_range; the clamp check must run first so both halves of the pair carry the same flag")
	}
}

// TestNearSentinelValuesAreUntouched is the reason for exact equality. 999.8 is
// a real, reportable concentration; a range-based sentinel check would discard
// it along with the peg.
func TestNearSentinelValuesAreUntouched(t *testing.T) {
	s := clampScorer()
	// 1999.8 is out of range on its own merits — the point is that it is not
	// swallowed by the clamp check, whose flag would say the wrong thing.
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

// TestSentinelIsExcludedFromTheReferencePopulation is the harm the flag alone
// does not undo. A pegged sensor was in range for P2, so it entered the median
// its own neighbours were scored against — one saturated instrument could push
// a whole neighbourhood's reference up by hundreds of µg/m³.
func TestSentinelIsExcludedFromTheReferencePopulation(t *testing.T) {
	s := clampScorer()

	// Three ordinary neighbours and three pegged ones. The proportion is
	// constructed, not typical — the sentinels have to carry the median past
	// the subject for the effect to be visible at all, and a fixture where the
	// verdict does not change proves nothing about the exclusion.
	//
	// Three clean neighbours is also the floor: drop below MinNeighbours and
	// the subject scores no_neighbours whatever the sentinels do.
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
	// With the sentinels in the population the median lands around 500 and 120
	// looks ordinary. Without them, 120 against a ~11 neighbourhood is an
	// outlier. Verified by mutation: dropping the IsClamped term from Score's
	// reference loop turns this red.
	if subject != quality.FlagSpatialOutlier {
		t.Errorf("subject scored %q, want %q — the sentinel is still in the reference median", subject, quality.FlagSpatialOutlier)
	}
}

// TestClampedIsNotUsable: the flag has to keep the reading out of aggregates.
// Flagging it and then averaging it in would be worse than not checking.
func TestClampedIsNotUsable(t *testing.T) {
	if quality.FlagClamped.Usable() {
		t.Error("FlagClamped.Usable() = true; a saturated reading must not reach an average")
	}
}
