package quality

import (
	"testing"
	"time"

	"airbg.org/internal/upstream"
)

// Sofia, and points roughly 1 km apart from it.
func at(id int64, metric string, value float64, lonOffset float64) upstream.Reading {
	return upstream.Reading{
		SensorID:   id,
		SensorType: "BME280",
		Lon:        23.3327 + lonOffset,
		Lat:        42.6957,
		Metric:     metric,
		Value:      value,
		Timestamp:  time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC),
	}
}

func flagOf(t *testing.T, scored []Scored, id int64) Flag {
	t.Helper()
	for _, s := range scored {
		if s.Reading.SensorID == id {
			return s.Flag
		}
	}
	t.Fatalf("sensor %d missing from scored output", id)
	return ""
}

func TestScoreFlagsOutOfRangeBeforeAnythingElse(t *testing.T) {
	readings := []upstream.Reading{
		at(1, "temperature", -999, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	scored := Score(readings, NewHistory(12))
	if got := flagOf(t, scored, 1); got != FlagOutOfRange {
		t.Errorf("flag = %v, want %v", got, FlagOutOfRange)
	}
}

func TestScoreFlagsTheSpecExample(t *testing.T) {
	// One sensor reporting 22 °C while every neighbour reports about −10 °C.
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	scored := Score(readings, NewHistory(12))

	if got := flagOf(t, scored, 1); got != FlagSpatialOutlier {
		t.Errorf("broken sensor flag = %v, want %v", got, FlagSpatialOutlier)
	}
	for _, id := range []int64{2, 3, 4} {
		if got := flagOf(t, scored, id); got != FlagOK {
			t.Errorf("healthy sensor %d flag = %v, want %v", id, got, FlagOK)
		}
	}
}

func TestScoreExcludesOutOfRangeNeighboursFromTheReference(t *testing.T) {
	// A dead sensor reporting −999 must not drag the neighbourhood median.
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -999, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
		at(5, "temperature", -10.2, 0.04),
	}
	scored := Score(readings, NewHistory(12))
	if got := flagOf(t, scored, 1); got != FlagOK {
		t.Errorf("healthy sensor flag = %v, want %v — dead neighbour polluted the reference", got, FlagOK)
	}
}

func TestScoreReturnsEveryReading(t *testing.T) {
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(2, "temperature", -10, 0.01),
	}
	scored := Score(readings, NewHistory(12))
	if len(scored) != len(readings) {
		t.Fatalf("len(scored) = %d, want %d — readings must never be dropped", len(scored), len(readings))
	}
}

func TestScoreIsolatesMetrics(t *testing.T) {
	// A temperature outlier must not affect humidity scoring at the same sensor.
	readings := []upstream.Reading{
		at(1, "temperature", 22, 0),
		at(1, "humidity", 50, 0),
		at(2, "temperature", -10, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
		at(2, "humidity", 52, 0.01),
		at(3, "humidity", 48, 0.02),
		at(4, "humidity", 51, 0.03),
	}
	scored := Score(readings, NewHistory(12))

	var tempFlag, humFlag Flag
	for _, s := range scored {
		if s.Reading.SensorID != 1 {
			continue
		}
		switch s.Reading.Metric {
		case "temperature":
			tempFlag = s.Flag
		case "humidity":
			humFlag = s.Flag
		}
	}
	if tempFlag != FlagSpatialOutlier {
		t.Errorf("temperature flag = %v, want %v", tempFlag, FlagSpatialOutlier)
	}
	if humFlag != FlagOK {
		t.Errorf("humidity flag = %v, want %v", humFlag, FlagOK)
	}
}

func TestScoreFlagsStuckSensor(t *testing.T) {
	hist := NewHistory(3)
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10.2, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	var scored []Scored
	for i := 0; i < 3; i++ {
		scored = Score(readings, hist)
	}
	if got := flagOf(t, scored, 1); got != FlagStuck {
		t.Errorf("flag = %v, want %v", got, FlagStuck)
	}
}

func TestPropertyReadingEqualToNeighbourMedianIsNeverFlagged(t *testing.T) {
	// Invariant: a reading identical to its neighbours' median is always OK,
	// for any in-range value and any neighbour count above the minimum.
	for _, base := range []float64{-30, -10, 0, 5, 20, 35, 55} {
		for n := minNeighbours; n <= 12; n++ {
			readings := []upstream.Reading{at(1, "temperature", base, 0)}
			for i := 1; i <= n; i++ {
				readings = append(readings, at(int64(i+1), "temperature", base, float64(i)*0.005))
			}
			scored := Score(readings, NewHistory(1000))
			if got := flagOf(t, scored, 1); got != FlagOK {
				t.Fatalf("base=%v n=%d: flag = %v, want %v", base, n, got, FlagOK)
			}
		}
	}
}

func TestPropertyAddingMedianNeighbourNeverCausesAFlag(t *testing.T) {
	// Invariant: adding a neighbour equal to the median cannot turn a previously
	// OK reading into a flagged one.
	readings := []upstream.Reading{
		at(1, "temperature", -10, 0),
		at(2, "temperature", -10.2, 0.01),
		at(3, "temperature", -10.5, 0.02),
		at(4, "temperature", -9.5, 0.03),
	}
	if got := flagOf(t, Score(readings, NewHistory(1000)), 1); got != FlagOK {
		t.Fatalf("precondition failed: flag = %v", got)
	}
	for i := 0; i < 20; i++ {
		readings = append(readings, at(int64(100+i), "temperature", -10.2, float64(i)*0.004))
		if got := flagOf(t, Score(readings, NewHistory(1000)), 1); got != FlagOK {
			t.Fatalf("after adding %d median neighbours: flag = %v, want %v", i+1, got, FlagOK)
		}
	}
}
